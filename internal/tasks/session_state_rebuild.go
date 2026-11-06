package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/sessioncache"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
)

// SessionStateStatus identifies one certified cache generation. The cursor
// certifies a completed scan, not the greatest hot tail that happened to settle.
type SessionStateStatus struct {
	Found                     bool
	Revision                  int64
	Dirty                     bool
	CompletedThroughJournalID int64
	CacheVersion              int64
}

// SessionStateRebuildOutcome reports one certified full or suffix publication.
type SessionStateRebuildOutcome struct {
	Sessions                  int
	Revision                  int64
	CompletedThroughJournalID int64
}

type sessionRebuildRow struct {
	harness, session                  string
	firstSeen, lastSeen               int64
	lastEvent                         string
	lastJournalID                     int64
	lastPromptAt, lastPromptJournalID int64
	hostEndAt, hostEndJournalID       int64
	lastResumeAt, lastResumeJournalID int64
	executions                        []sessionExecutionRow
}

type sessionExecutionRow struct {
	journalID, observedAt      int64
	kind, agent, turn, request string
	encoded                    []byte
}

func (r *sessionRebuildRow) addTimestamp(ts int64) {
	if ts <= 0 {
		return
	}
	if r.firstSeen == 0 || ts < r.firstSeen {
		r.firstSeen = ts
	}
	if ts > r.lastSeen {
		r.lastSeen = ts
	}
}

type sessionStatusReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readSessionStatus(ctx context.Context, reader sessionStatusReader) (SessionStateStatus, error) {
	var status SessionStateStatus
	var dirty int
	err := reader.QueryRowContext(ctx, `SELECT revision,dirty,completed_through_jid,cache_version FROM pasture_session_api_state WHERE singleton_id=0`).Scan(&status.Revision, &dirty, &status.CompletedThroughJournalID, &status.CacheVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.Found = true
	status.Dirty = dirty != 0
	return status, nil
}

func ReadSessionStateStatus(ctx context.Context, tracker protocol.TaskTracker) (SessionStateStatus, error) {
	store, ok := tracker.(unifiedStoreHandle)
	if !ok || store.auditDBHandle() == nil {
		return SessionStateStatus{}, sessionClaimError("the tracker has no unified database handle", "use tasks.OpenTaskTracker")
	}
	bounded, cancel := context.WithTimeout(ctx, store.lifecycleTimeoutProfile().WorkflowResult())
	defer cancel()
	status, err := readSessionStatus(bounded, store.auditDBHandle())
	if err != nil {
		return status, sessionClaimError("the store refused the session-state status read: "+err.Error(), "repair the database before starting the API")
	}
	return status, nil
}

// EnsureSessionState uses a bounded suffix scan for a clean current cache and
// a full scan for dirty, absent or older-version cache recovery.
func EnsureSessionState(ctx context.Context, tracker protocol.TaskTracker) (SessionStateRebuildOutcome, bool, error) {
	return refreshSessionState(ctx, tracker, false, true, nil)
}

// RefreshSessionState applies the same cache decision as a read (full when the
// cache is dirty, absent or an older layout; suffix otherwise) WITHOUT the
// per-read workflow-result bound. Explicit maintenance callers use it so a
// large suffix can finish instead of timing out.
func RefreshSessionState(ctx context.Context, tracker protocol.TaskTracker) (SessionStateRebuildOutcome, bool, error) {
	return refreshSessionState(ctx, tracker, false, false, nil)
}

// RebuildSessionState explicitly repairs the complete disposable cache. It
// publishes only after the same generation and current-evidence fences used by
// normal suffix refresh, and never fabricates claims from historical evidence.
func RebuildSessionState(ctx context.Context, tracker protocol.TaskTracker) (SessionStateRebuildOutcome, error) {
	out, _, err := refreshSessionState(ctx, tracker, true, false, nil)
	return out, err
}

type sessionReplayStage uint8

const sessionReplayScanned sessionReplayStage = 1
const sessionReplayMaxAttempts = 8

var errSessionReplayChanged = errors.New("session replay generation or journal changed")

// ErrSessionReplayExhausted identifies contention across every bounded replay attempt.
var ErrSessionReplayExhausted = errors.New("session replay publication attempts exhausted")

// The observer is private, per-invocation dependency injection for deterministic
// interleavings. Both public production entry points supply nil. It is not an
// execution-publication hook: publication itself is one concrete SQL path below.
// sessionCatchUpBudget bounds the suffix chase after an explicit rebuild. The
// pinned snapshot is already coherent; this only closes the gap opened by
// writers during the scan, and stops rather than chasing a hot journal forever.
const sessionCatchUpBudget = 2 * time.Minute

func refreshSessionState(ctx context.Context, tracker protocol.TaskTracker, forceFull bool, boundedByWorkflowResult bool, observer func(sessionReplayStage)) (SessionStateRebuildOutcome, bool, error) {
	store, ok := tracker.(lifecycleReceiptStore)
	if !ok || store.auditDBHandle() == nil || store.Journal() == nil {
		return SessionStateRebuildOutcome{}, false, sessionClaimError("the tracker has no unified database and lifecycle journal", "use tasks.OpenTaskTracker")
	}
	// An explicit repair walks the whole journal, which takes minutes on a large
	// store, so it runs under the caller's context: an interrupt still stops it
	// and a daemon start waits for it. The implicit per-read refresh must stay
	// inside one workflow-result wait, and its failure names --backfill as the
	// repair.
	bounded := ctx
	if boundedByWorkflowResult {
		var cancel context.CancelFunc
		bounded, cancel = context.WithTimeout(ctx, store.lifecycleTimeoutProfile().WorkflowResult())
		defer cancel()
	}
	catchUpDeadline := time.Time{}
	if forceFull {
		catchUpDeadline = time.Now().Add(sessionCatchUpBudget)
	}
	var last SessionStateRebuildOutcome
	published := false
	for range sessionReplayMaxAttempts {
		if err := bounded.Err(); err != nil {
			return SessionStateRebuildOutcome{}, false, staleSessionReplay(err)
		}
		start, err := readSessionStatus(bounded, store.auditDBHandle())
		if err != nil {
			return SessionStateRebuildOutcome{}, false, staleSessionReplay(err)
		}
		if start.CacheVersion > audit.SessionCacheVersion {
			return SessionStateRebuildOutcome{}, false, fmt.Errorf("%w: cache version %d is newer than supported version %d; upgrade Pasture", sessioncache.ErrNewerLayout, start.CacheVersion, audit.SessionCacheVersion)
		}
		full := forceFull || !start.Found || start.Dirty || start.Revision == 0 || start.CacheVersion != audit.SessionCacheVersion
		after := provenance.JournalID(start.CompletedThroughJournalID)
		if full {
			after = 0
		}
		rows, watermark, found, err := scanSessionReplay(bounded, store, after)
		if err != nil {
			return SessionStateRebuildOutcome{}, false, staleSessionReplay(err)
		}
		if !full && !found {
			if published {
				return last, true, nil
			}
			return SessionStateRebuildOutcome{}, false, nil
		}
		// Ownership reads are pooled and must finish before taking the writer
		// connection. A clean no-op suffix never pays for ownership inference.
		// Current binding and claim rows are read again in the transaction.
		inferred, inferenceErr := planSessionInference(bounded, store)
		if inferenceErr != nil {
			writeSessionInferenceDiagnostic(store, sessionInferenceStagePlan, inferenceErr)
		}
		if observer != nil {
			observer(sessionReplayScanned)
		}
		out, err := publishSessionReplay(bounded, store, start, watermark, rows, full, inferred, inferenceErr == nil)
		if errors.Is(err, errSessionReplayChanged) {
			continue
		}
		if err != nil {
			return SessionStateRebuildOutcome{}, false, staleSessionReplay(err)
		}
		last, published = out, true
		// The publish is coherent at the pinned snapshot. Close the gap opened by
		// writers during the scan with a suffix pass; stop when the tail is empty,
		// when the implicit budget is spent, or when an explicit repair's catch-up
		// window closes. A cache that is behind is still a valid snapshot.
		advanced, tailErr := sessionJournalAdvanced(bounded, store, provenance.JournalID(out.CompletedThroughJournalID))
		if tailErr != nil || !advanced {
			return out, true, nil
		}
		if !forceFull || time.Now().After(catchUpDeadline) {
			return out, true, nil
		}
	}
	if published {
		return last, true, nil
	}
	return SessionStateRebuildOutcome{}, false, staleSessionReplay(fmt.Errorf("%w: the store changed during all %d bounded publication attempts", ErrSessionReplayExhausted, sessionReplayMaxAttempts))
}

func staleSessionReplay(cause error) error {
	return fmt.Errorf("session state cache is stale: bounded replay could not certify its publication (internal/tasks/session_state_rebuild.go): %w; the previous certified cache is preserved; retry when store writers settle, or repair the journal/payload evidence named by the error", cause)
}

func sessionReplayRow(rows map[sessionKey]*sessionRebuildRow, harness, session string) *sessionRebuildRow {
	key := sessionKey{harness: harness, session: session}
	row := rows[key]
	if row == nil {
		row = &sessionRebuildRow{harness: harness, session: session}
		rows[key] = row
	}
	return row
}

func scanSessionReplay(ctx context.Context, store lifecycleReceiptStore, after provenance.JournalID) (map[sessionKey]*sessionRebuildRow, provenance.JournalID, bool, error) {
	rows := make(map[sessionKey]*sessionRebuildRow)
	// Retained execution bytes are bounded independently of journal length.
	// A scan that exceeds this budget refuses publication rather than losing
	// observations or holding the live writer while it reads more pages.
	const executionBudget = provenance.MaxFactPageSize * model.MaxNativePayloadBytes
	executionBytes := 0
	request := provenance.FactPageRequest{Limit: provenance.MaxFactPageSize, AfterJournalID: after}
	var watermark provenance.JournalID
	found := false
	for {
		page, err := readSessionOccurrencePage(ctx, store.auditDBHandle(), request)
		if err != nil {
			return nil, 0, false, err
		}
		// Pin the first resolved snapshot for the whole replay. Rows at or below
		// it are immutable, so a lifecycle writer appending during the scan cannot
		// move the target; anything newer is the next suffix refresh's work.
		if request.SnapshotMaxJournalID == 0 {
			request.SnapshotMaxJournalID = page.SnapshotMaxJournalID
		}
		watermark = page.SnapshotMaxJournalID
		for _, fact := range page.Facts {
			if err := ctx.Err(); err != nil {
				return nil, 0, false, err
			}
			found = true
			session, err := lifecycleSession(fact.Bindings)
			if err != nil {
				return nil, 0, false, err
			}
			if session == "" {
				continue
			}
			if err := model.ValidateBindingText("session", session); err != nil {
				return nil, 0, false, err
			}
			row := sessionReplayRow(rows, string(fact.Contract.Harness()), session)
			jid := int64(fact.JournalID)
			if fact.Execution != nil {
				encoded, err := json.Marshal(fact.Execution)
				if err != nil {
					return nil, 0, false, fmt.Errorf("encode execution metadata for occurrence %d: %w", jid, err)
				}
				executionBytes += len(encoded)
				if executionBytes > executionBudget {
					return nil, 0, false, fmt.Errorf("session execution replay exceeds its %d-byte memory budget; previous cache is preserved; reduce the journal or use a larger-capacity reader", executionBudget)
				}
				row.executions = append(row.executions, sessionExecutionRow{journalID: jid, observedAt: fact.ReceivedAt, kind: string(fact.Execution.Kind), agent: fact.Execution.AgentID, turn: fact.Execution.TurnID, request: fact.Execution.RequestID, encoded: encoded})
			}
			if row.firstSeen == 0 || fact.ReceivedAt < row.firstSeen {
				row.firstSeen = fact.ReceivedAt
			}
			if jid > row.lastJournalID {
				row.lastSeen, row.lastJournalID = fact.ReceivedAt, jid
				// Unknown kinds have no evidenced native name; do not invent one.
				row.lastEvent, _ = registration.NativeName(fact.Contract, fact.Event)
			}
			transition, err := replaySessionTransition(ctx, store, fact)
			if err != nil {
				return nil, 0, false, err
			}
			switch transition {
			case model.SessionTransitionPrompt:
				if jid > row.lastPromptJournalID {
					row.lastPromptAt, row.lastPromptJournalID = fact.ReceivedAt, jid
				}
			case model.SessionTransitionEnd:
				if jid > row.hostEndJournalID {
					row.hostEndAt, row.hostEndJournalID = fact.ReceivedAt, jid
				}
			case model.SessionTransitionResume:
				if jid > row.lastResumeJournalID {
					row.lastResumeAt, row.lastResumeJournalID = fact.ReceivedAt, jid
				}
			}
		}
		if page.Next == nil {
			return rows, watermark, found, nil
		}
		request.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		request.AfterJournalID = page.Next.AfterJournalID
	}
}

// A read-only transaction is deliberately different from the publication
// transaction: the pinned modernc driver uses plain BEGIN for ReadOnly even
// when the handle's write mode is immediate (modernc.org/sqlite/tx.go). This
// bounds pool acquisition and every evidence query by the shared caller budget
// without taking write ownership. Close each page lease before payload reads,
// inference, observers or the final publication transaction.
func readSessionOccurrencePage(ctx context.Context, db *sql.DB, request provenance.FactPageRequest) (projection.OccurrenceFactPage, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return projection.OccurrenceFactPage{}, err
	}
	reader, err := provenance.NewTransactionEvidenceReader(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return projection.OccurrenceFactPage{}, err
	}
	page, readErr := projection.ReadOccurrenceFactPage(ctx, reader, tx, request)
	closeErr := tx.Rollback()
	if readErr != nil {
		return projection.OccurrenceFactPage{}, readErr
	}
	if closeErr != nil && !errors.Is(closeErr, sql.ErrTxDone) {
		return projection.OccurrenceFactPage{}, closeErr
	}
	if err := ctx.Err(); err != nil {
		return projection.OccurrenceFactPage{}, err
	}
	return page, nil
}

// The admitted journal coordinate itself proves prompt/end. Resume additionally
// requires the retained native source member. Its one bounded body read happens
// during the same paged scan, never under the publication transaction.
func replaySessionTransition(ctx context.Context, store lifecycleReceiptStore, fact projection.OccurrenceFact) (model.SessionTransition, error) {
	if fact.Capture != model.CaptureValid {
		return model.SessionTransitionNone, nil
	}
	// A v3 record carries the constructor-owned transition. It is durable
	// authority and is read BEFORE the manifest, so a later pinned contract
	// cannot make a genuine resume disappear from this build's inventory.
	if fact.Version == projection.OccurrenceEvidenceKindV3 && fact.SessionTransition != model.SessionTransitionNone {
		return fact.SessionTransition, nil
	}
	event, known := registration.EventFor(fact.Contract, fact.Event)
	if !known {
		return model.SessionTransitionNone, nil
	}
	if event.SessionTransition != model.SessionTransitionResume {
		return event.SessionTransition, nil
	}
	if fact.Version == projection.OccurrenceEvidenceKindV3 {
		return model.SessionTransitionNone, nil
	}
	ref, err := digest.Parse(fact.PayloadDigest)
	if err != nil || ref.Algorithm() != digest.SHA256 {
		return model.SessionTransitionNone, fmt.Errorf("legacy occurrence %d has an invalid payload digest; restore its original payload evidence", fact.JournalID)
	}
	var body []byte
	var declared, length int64
	err = store.auditDBHandle().QueryRowContext(ctx, `SELECT byte_count,length(body),substr(body,1,?) FROM lifecycle_payload_blobs WHERE digest=?`, model.MaxNativePayloadBytes+1, ref.String()).Scan(&declared, &length, &body)
	if errors.Is(err, sql.ErrNoRows) {
		if diagnostics := store.lifecycleDiagnosticSink(); diagnostics != nil {
			fmt.Fprintf(diagnostics, "pasture: legacy occurrence %d has no retained payload; resume source is unavailable and no reopening was inferred; restore the original body from a verified backup and request a full session cache rebuild for complete evidence\n", fact.JournalID)
		}
		return model.SessionTransitionNone, nil
	}
	if err != nil {
		return model.SessionTransitionNone, err
	}
	if length > model.MaxNativePayloadBytes || length != declared || int64(len(body)) != length || digest.FromBytes(body) != ref {
		return model.SessionTransitionNone, fmt.Errorf("legacy occurrence %d payload exceeds its bound or fails digest/length authentication; restore the verified payload before rebuilding", fact.JournalID)
	}
	return ingress.ExtractSessionTransition(fact.Contract, fact.Event, body, fact.Capture, fact.Bindings), nil
}

func publishSessionReplay(ctx context.Context, store lifecycleReceiptStore, start SessionStateStatus, watermark provenance.JournalID, rows map[sessionKey]*sessionRebuildRow, full bool, inferred []sessionInferredBinding, planned bool) (SessionStateRebuildOutcome, error) {
	tx, err := store.auditDBHandle().BeginTx(ctx, nil)
	if err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	defer func() { _ = tx.Rollback() }()
	created, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO pasture_session_api_state(singleton_id,revision,dirty,completed_through_jid,cache_version) VALUES(0,0,1,0,?)`, audit.SessionCacheVersion)
	if err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pasture_session_api_state SET revision=revision WHERE singleton_id=0`); err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	current, err := readSessionStatus(ctx, tx)
	if err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	inserted, err := created.RowsAffected()
	if err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	if !start.Found && inserted == 1 {
		start = current
	}
	// Fence on what a PUBLICATION changes, not on what an ordinary write changes.
	// Every incremental lifecycle write bumps revision (and a failed one sets
	// dirty), so comparing the whole status made a replay impossible under an
	// active journal: the scan could never win its own compare-and-swap.
	// Another publisher advances the watermark; a layout change moves the cache
	// version. Both still fence.
	if current.CacheVersion != start.CacheVersion || current.CompletedThroughJournalID != start.CompletedThroughJournalID {
		// Another writer published while this replay scanned. If its cache is
		// already at or beyond our pinned snapshot and clean, its work subsumes
		// ours: report it instead of rescanning. A newer layout, a dirty marker,
		// or an older watermark still needs a fresh attempt.
		if !current.Dirty && current.CacheVersion == audit.SessionCacheVersion && provenance.JournalID(current.CompletedThroughJournalID) >= watermark {
			var out SessionStateRebuildOutcome
			out.CompletedThroughJournalID = current.CompletedThroughJournalID
			out.Revision = current.Revision
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pasture_session_state`).Scan(&out.Sessions); err != nil {
				return SessionStateRebuildOutcome{}, err
			}
			return out, nil
		}
		return SessionStateRebuildOutcome{}, errSessionReplayChanged
	}
	explicit, err := addCurrentSessionBindingsAndClaims(ctx, tx, rows)
	if err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	if full {
		if _, err := tx.ExecContext(ctx, `DELETE FROM pasture_session_execution`); err != nil {
			return SessionStateRebuildOutcome{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM pasture_session_state`); err != nil {
			return SessionStateRebuildOutcome{}, err
		}
	}
	for _, row := range rows {
		if err := upsertReplayedSessionRow(ctx, tx, row); err != nil {
			return SessionStateRebuildOutcome{}, err
		}
		for _, observation := range row.executions {
			result, err := tx.ExecContext(ctx, `INSERT INTO pasture_session_execution(journal_id,harness,session_id,kind,native_agent_id,turn_id,request_id,observed_at,execution_json) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(journal_id) DO UPDATE SET journal_id=excluded.journal_id WHERE harness=excluded.harness AND session_id=excluded.session_id AND kind=excluded.kind AND native_agent_id=excluded.native_agent_id AND turn_id=excluded.turn_id AND request_id=excluded.request_id AND observed_at=excluded.observed_at AND execution_json=excluded.execution_json`, observation.journalID, row.harness, row.session, observation.kind, observation.agent, observation.turn, observation.request, observation.observedAt, observation.encoded)
			if err != nil {
				return SessionStateRebuildOutcome{}, err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return SessionStateRebuildOutcome{}, err
			}
			if count != 1 {
				return SessionStateRebuildOutcome{}, fmt.Errorf("execution cache occurrence %d conflicts with journal evidence; inspect the cache and journal before retrying", observation.journalID)
			}
		}
	}
	ended, err := readUnsupersededSessionEnds(ctx, tx)
	if err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	for key, at := range ended {
		if err := closeSessionBindingTx(ctx, tx, ir.HarnessID(key.harness), key.session, at); err != nil {
			return SessionStateRebuildOutcome{}, err
		}
	}
	if planned {
		if _, err := tx.ExecContext(ctx, `SAVEPOINT session_inference`); err != nil {
			return SessionStateRebuildOutcome{}, err
		}
		excluded := make(map[sessionKey]struct{}, len(ended))
		for key := range ended {
			excluded[key] = struct{}{}
		}
		if writeErr := replaceSessionInferredBindings(ctx, tx, inferred, explicit, excluded); writeErr != nil {
			if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT session_inference`); err != nil {
				return SessionStateRebuildOutcome{}, err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM pasture_session_inferred_binding`); err != nil {
				return SessionStateRebuildOutcome{}, err
			}
			writeSessionInferenceDiagnostic(store, sessionInferenceStageWrite, writeErr)
		}
		if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT session_inference`); err != nil {
			return SessionStateRebuildOutcome{}, err
		}
	}
	// The publication is coherent at the pinned snapshot, so it clears the dirty
	// marker even for a suffix: a dirty marker that appeared during the scan
	// refers to a write whose row is inside the scanned range, and a marker that
	// predates the scan forced a full rebuild. Rows after the watermark are the
	// next suffix refresh's work, not a cache inconsistency.
	if _, err := tx.ExecContext(ctx, `UPDATE pasture_session_api_state SET revision=revision+1,completed_through_jid=?,cache_version=?,dirty=0 WHERE singleton_id=0`, watermark, audit.SessionCacheVersion); err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	var out SessionStateRebuildOutcome
	out.CompletedThroughJournalID = int64(watermark)
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM pasture_session_api_state WHERE singleton_id=0`).Scan(&out.Revision); err != nil {
		return out, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pasture_session_state`).Scan(&out.Sessions); err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	return out, nil
}

func addCurrentSessionBindingsAndClaims(ctx context.Context, tx *sql.Tx, rows map[sessionKey]*sessionRebuildRow) (map[sessionKey]struct{}, error) {
	explicit := make(map[sessionKey]struct{})
	bindings, err := tx.QueryContext(ctx, `SELECT harness,session,bound_at,unbound_at FROM pasture_session_epoch_binding ORDER BY bound_at,rowid`)
	if err != nil {
		return nil, err
	}
	for bindings.Next() {
		var harness, session string
		var bound int64
		var unbound sql.NullInt64
		if err := bindings.Scan(&harness, &session, &bound, &unbound); err != nil {
			_ = bindings.Close()
			return nil, err
		}
		explicit[sessionKey{harness: harness, session: session}] = struct{}{}
		row := sessionReplayRow(rows, harness, session)
		row.addTimestamp(bound)
		if unbound.Valid {
			row.addTimestamp(unbound.Int64)
		}
	}
	err = bindings.Err()
	if closeErr := bindings.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	claims, err := tx.QueryContext(ctx, `SELECT harness,session,claimed_at FROM pasture_session_claim`)
	if err != nil {
		return nil, err
	}
	for claims.Next() {
		var harness, session string
		var at int64
		if err := claims.Scan(&harness, &session, &at); err != nil {
			_ = claims.Close()
			return nil, err
		}
		sessionReplayRow(rows, harness, session).addTimestamp(at)
	}
	err = claims.Err()
	if closeErr := claims.Close(); err == nil {
		err = closeErr
	}
	return explicit, err
}

func upsertReplayedSessionRow(ctx context.Context, tx *sql.Tx, row *sessionRebuildRow) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO pasture_session_state
		(harness,session,first_seen,last_seen,last_event,last_journal_id,updated_at,last_prompt_at,last_prompt_journal_id,host_end_at,host_end_journal_id,last_resume_at,last_resume_journal_id)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(harness,session) DO UPDATE SET
		first_seen=min(pasture_session_state.first_seen,excluded.first_seen),
		last_seen=CASE WHEN excluded.last_journal_id>pasture_session_state.last_journal_id THEN excluded.last_seen WHEN excluded.last_journal_id=0 THEN max(pasture_session_state.last_seen,excluded.last_seen) ELSE pasture_session_state.last_seen END,
		last_event=CASE WHEN excluded.last_journal_id>pasture_session_state.last_journal_id THEN excluded.last_event ELSE pasture_session_state.last_event END,
		last_journal_id=max(pasture_session_state.last_journal_id,excluded.last_journal_id),updated_at=max(pasture_session_state.updated_at,excluded.updated_at),
		last_prompt_at=CASE WHEN excluded.last_prompt_journal_id>pasture_session_state.last_prompt_journal_id THEN excluded.last_prompt_at ELSE pasture_session_state.last_prompt_at END,
		last_prompt_journal_id=max(pasture_session_state.last_prompt_journal_id,excluded.last_prompt_journal_id),
		host_end_at=CASE WHEN excluded.host_end_journal_id>pasture_session_state.host_end_journal_id THEN excluded.host_end_at ELSE pasture_session_state.host_end_at END,
		host_end_journal_id=max(pasture_session_state.host_end_journal_id,excluded.host_end_journal_id),
		last_resume_at=CASE WHEN excluded.last_resume_journal_id>pasture_session_state.last_resume_journal_id THEN excluded.last_resume_at ELSE pasture_session_state.last_resume_at END,
		last_resume_journal_id=max(pasture_session_state.last_resume_journal_id,excluded.last_resume_journal_id)`, row.harness, row.session, row.firstSeen, row.lastSeen, row.lastEvent, row.lastJournalID, row.lastSeen, row.lastPromptAt, row.lastPromptJournalID, row.hostEndAt, row.hostEndJournalID, row.lastResumeAt, row.lastResumeJournalID)
	return err
}

func readUnsupersededSessionEnds(ctx context.Context, tx *sql.Tx) (map[sessionKey]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT harness,session,host_end_at FROM pasture_session_state WHERE host_end_journal_id>last_resume_journal_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ended := make(map[sessionKey]int64)
	for rows.Next() {
		var key sessionKey
		var at int64
		if err := rows.Scan(&key.harness, &key.session, &at); err != nil {
			return nil, err
		}
		ended[key] = at
	}
	return ended, rows.Err()
}

// sessionJournalAdvanced reports whether the journal holds rows beyond the
// certified watermark. Those rows belong to the next suffix refresh.
func sessionJournalAdvanced(ctx context.Context, store lifecycleReceiptStore, watermark provenance.JournalID) (bool, error) {
	var max sql.NullInt64
	if err := store.auditDBHandle().QueryRowContext(ctx, `SELECT MAX(journal_id) FROM journal`).Scan(&max); err != nil {
		return false, err
	}
	return max.Valid && provenance.JournalID(max.Int64) > watermark, nil
}
