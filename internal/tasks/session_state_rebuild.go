package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
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
	return refreshSessionState(ctx, tracker, false, nil)
}

// RebuildSessionState explicitly repairs the complete disposable cache. It
// publishes only after the same generation and current-evidence fences used by
// normal suffix refresh, and never fabricates claims from historical evidence.
func RebuildSessionState(ctx context.Context, tracker protocol.TaskTracker) (SessionStateRebuildOutcome, error) {
	out, _, err := refreshSessionState(ctx, tracker, true, nil)
	return out, err
}

type sessionReplayStage uint8

const sessionReplayScanned sessionReplayStage = 1
const sessionReplayMaxAttempts = 8

var errSessionReplayChanged = errors.New("session replay generation or journal changed")

// The observer is private, per-invocation dependency injection for deterministic
// interleavings. Both public production entry points supply nil. It is not an
// execution-publication hook: publication itself is one concrete SQL path below.
func refreshSessionState(ctx context.Context, tracker protocol.TaskTracker, forceFull bool, observer func(sessionReplayStage)) (SessionStateRebuildOutcome, bool, error) {
	store, ok := tracker.(lifecycleReceiptStore)
	if !ok || store.auditDBHandle() == nil || store.Journal() == nil {
		return SessionStateRebuildOutcome{}, false, sessionClaimError("the tracker has no unified database and lifecycle journal", "use tasks.OpenTaskTracker")
	}
	bounded, cancel := context.WithTimeout(ctx, store.lifecycleTimeoutProfile().WorkflowResult())
	defer cancel()
	for range sessionReplayMaxAttempts {
		if err := bounded.Err(); err != nil {
			return SessionStateRebuildOutcome{}, false, staleSessionReplay(err)
		}
		start, err := readSessionStatus(bounded, store.auditDBHandle())
		if err != nil {
			return SessionStateRebuildOutcome{}, false, staleSessionReplay(err)
		}
		if start.CacheVersion > audit.SessionCacheVersion {
			return SessionStateRebuildOutcome{}, false, staleSessionReplay(fmt.Errorf("cache version %d is newer than supported version %d; upgrade Pasture", start.CacheVersion, audit.SessionCacheVersion))
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
		return out, true, nil
	}
	return SessionStateRebuildOutcome{}, false, staleSessionReplay(fmt.Errorf("the store changed during all %d bounded publication attempts", sessionReplayMaxAttempts))
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
				var known bool
				row.lastEvent, known = registration.NativeName(fact.Contract, fact.Event)
				if !known {
					row.lastEvent = fmt.Sprintf("event-%d", fact.Event)
				}
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
	var event registration.Event
	for _, manifest := range []registration.Manifest{registration.ClaudeCode__2_1_261(), registration.Codex__0_153_0(), registration.OpenCode__1_18_29(), registration.OpenCode__2_0_20()} {
		if manifest.Contract != fact.Contract {
			continue
		}
		for _, candidate := range manifest.Events {
			if candidate.Kind == fact.Event {
				event = candidate
				break
			}
		}
		break
	}
	if event.SessionTransition != model.SessionTransitionResume {
		return event.SessionTransition, nil
	}
	if fact.Version == "pasture.lifecycle.occurrence.v3" {
		return fact.SessionTransition, nil
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
	if current != start {
		return SessionStateRebuildOutcome{}, errSessionReplayChanged
	}
	// CURRENT transaction evidence, not a query pinned at the old scan W. All
	// pooled scans/ownership work ended before this single connection was held.
	reader, err := provenance.NewTransactionEvidenceReader(ctx, tx)
	if err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	post, err := projection.ReadOccurrenceFactPage(ctx, reader, tx, provenance.FactPageRequest{Limit: 1, AfterJournalID: watermark})
	if err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	if len(post.Facts) > 0 {
		return SessionStateRebuildOutcome{}, errSessionReplayChanged
	}
	explicit, err := addCurrentSessionBindingsAndClaims(ctx, tx, rows)
	if err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	if full {
		// Even a full rebuild must not silently rewrite an existing observation
		// under the same journal identity.
		for _, row := range rows {
			for _, observation := range row.executions {
				var exists, matches bool
				err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pasture_session_execution WHERE journal_id=?), EXISTS(SELECT 1 FROM pasture_session_execution WHERE journal_id=? AND harness=? AND session_id=? AND kind=? AND native_agent_id=? AND turn_id=? AND request_id=? AND observed_at=? AND execution_json=?)`, observation.journalID, observation.journalID, row.harness, row.session, observation.kind, observation.agent, observation.turn, observation.request, observation.observedAt, observation.encoded).Scan(&exists, &matches)
				if err != nil {
					return SessionStateRebuildOutcome{}, err
				}
				if exists && !matches {
					return SessionStateRebuildOutcome{}, fmt.Errorf("execution cache occurrence %d conflicts with journal evidence; inspect the cache and journal before retrying", observation.journalID)
				}
			}
		}
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
	if _, err := tx.ExecContext(ctx, `UPDATE pasture_session_api_state SET revision=revision+1,completed_through_jid=?,cache_version=?,dirty=CASE WHEN ? THEN 0 ELSE dirty END WHERE singleton_id=0`, watermark, audit.SessionCacheVersion, full); err != nil {
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
