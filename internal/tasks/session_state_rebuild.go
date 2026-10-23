package tasks

import (
	"context"
	"database/sql"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

var sessionOccurrenceEvidenceKinds = []provenance.EvidenceKind{
	"pasture.lifecycle.occurrence.v1",
	"pasture.lifecycle.occurrence.v2",
}

// SessionStateStatus is the cache marker the read API and the serve startup
// use to decide whether the session state table still reflects the journal.
type SessionStateStatus struct {
	Found                     bool
	Revision                  int64
	Dirty                     bool
	CompletedThroughJournalID int64
}

// SessionStateRebuildOutcome reports one full rebuild.
type SessionStateRebuildOutcome struct {
	Sessions                  int
	Revision                  int64
	CompletedThroughJournalID int64
}

type sessionRebuildRow struct {
	harness       string
	session       string
	firstSeen     int64
	lastSeen      int64
	lastEvent     string
	lastJournalID int64
	endAt         int64
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

// ReadSessionStateStatus reads the singleton cache marker without rebuilding.
func ReadSessionStateStatus(ctx context.Context, tracker protocol.TaskTracker) (SessionStateStatus, error) {
	store, ok := tracker.(sessionTailStore)
	if !ok || store.auditDBHandle() == nil {
		return SessionStateStatus{}, sessionClaimError("the tracker has no unified database handle", "use tasks.OpenTaskTracker")
	}
	bounded, cancel := context.WithTimeout(ctx, store.lifecycleTimeoutProfile().WorkflowResult())
	defer cancel()
	var status SessionStateStatus
	var dirty int64
	err := store.auditDBHandle().QueryRowContext(bounded,
		`SELECT revision, dirty, completed_through_jid FROM pasture_session_api_state WHERE singleton_id = 0`).
		Scan(&status.Revision, &dirty, &status.CompletedThroughJournalID)
	if err == sql.ErrNoRows {
		return SessionStateStatus{}, nil
	}
	if err != nil {
		return SessionStateStatus{}, sessionClaimError("the store refused the session-state status read: "+err.Error(), "repair the database before starting the API")
	}
	status.Found = true
	status.Dirty = dirty != 0
	return status, nil
}

// EnsureSessionState rebuilds the cache only when it is dirty, absent, or a
// committed lifecycle occurrence exists after the marker. The check reads the
// journal's occurrence evidence with a one-row page, so unrelated task facts do
// not force a replay and a crash between the receipt commit and the tail is
// still detected.
func EnsureSessionState(ctx context.Context, tracker protocol.TaskTracker) (SessionStateRebuildOutcome, bool, error) {
	status, err := ReadSessionStateStatus(ctx, tracker)
	if err != nil {
		return SessionStateRebuildOutcome{}, false, err
	}
	if !status.Found || status.Dirty || status.Revision == 0 {
		out, err := RebuildSessionState(ctx, tracker)
		return out, true, err
	}
	store, ok := tracker.(lifecycleReceiptStore)
	if !ok || store.Journal() == nil {
		return SessionStateRebuildOutcome{}, false, sessionClaimError("the tracker has no lifecycle journal", "use tasks.OpenTaskTracker")
	}
	pageRequest := provenance.FactPageRequest{
		Limit:          1,
		AfterJournalID: provenance.JournalID(status.CompletedThroughJournalID),
	}
	if status.CompletedThroughJournalID > 0 {
		pageRequest.SnapshotMaxJournalID = provenance.JournalID(status.CompletedThroughJournalID)
	}
	page, err := store.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  sessionOccurrenceEvidenceKinds,
		Page:   pageRequest,
	})
	if err != nil {
		return SessionStateRebuildOutcome{}, false, sessionClaimError("the store refused the lifecycle occurrence watermark read: "+err.Error(), "repair the journal before starting the API")
	}
	if len(page.Rows) == 0 {
		return SessionStateRebuildOutcome{}, false, nil
	}
	out, err := RebuildSessionState(ctx, tracker)
	return out, true, err
}

// RebuildSessionState rebuilds the disposable session cache from journal truth
// in ONE immediate transaction. It deliberately reads occurrence facts without
// replaying the occurrence projection or its semantic envelope checks. The
// binding rows own the end; activity fields are derived from the latest
// occurrence plus the first/last timestamps of bindings and the first-claim-
// wins claim.
func RebuildSessionState(ctx context.Context, tracker protocol.TaskTracker) (SessionStateRebuildOutcome, error) {
	store, ok := tracker.(sessionTailStore)
	if !ok || store.auditDBHandle() == nil {
		return SessionStateRebuildOutcome{}, sessionClaimError("the tracker has no unified database handle", "use tasks.OpenTaskTracker")
	}
	storeWithJournal, ok := tracker.(lifecycleReceiptStore)
	if !ok || storeWithJournal.Journal() == nil {
		return SessionStateRebuildOutcome{}, sessionClaimError("the tracker has no lifecycle journal", "use tasks.OpenTaskTracker")
	}
	bounded, cancel := context.WithTimeout(ctx, store.lifecycleTimeoutProfile().WorkflowResult())
	defer cancel()
	// Derive the inferred bindings BEFORE the journal facts and the write
	// transaction. The derivation reads the claim table and one provenance
	// ownership snapshot per claim actor, and the tracker's pool is size one in
	// production, so a pooled ownership read issued inside the transaction
	// would wait on the transaction's own connection. A derivation fault is not
	// a rebuild fault: the previous inferred cache is left in place (this
	// rebuild has nothing better to publish), one diagnostic is written, and
	// the read model stays usable.
	inferred, inferenceErr := planSessionInference(bounded, storeWithJournal)
	inferencePlanned := inferenceErr == nil
	if inferenceErr != nil {
		writeSessionInferenceDiagnostic(storeWithJournal, sessionInferenceStagePlan, inferenceErr)
	}
	// Read journal truth BEFORE taking the write transaction, for the same
	// pool reason as the derivation above.
	facts, err := projection.ReadOccurrenceFacts(bounded, storeWithJournal.Journal())
	if err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	completedThrough := int64(0)
	for _, fact := range facts {
		if int64(fact.JournalID) > completedThrough {
			completedThrough = int64(fact.JournalID)
		}
	}

	tx, err := store.auditDBHandle().BeginTx(bounded, nil)
	if err != nil {
		return SessionStateRebuildOutcome{}, sessionClaimError("the store refused the session-state rebuild transaction: "+err.Error(), "release other store writers or repair the database, then retry")
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	rows, explicitBindings, err := readSessionRebuildRows(bounded, tx, facts)
	if err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	if _, err := tx.ExecContext(bounded, `DELETE FROM pasture_session_state`); err != nil {
		return SessionStateRebuildOutcome{}, sessionClaimError("the store refused to clear the session-state cache: "+err.Error(), "repair the database before rebuilding the session cache")
	}
	ended := make(map[sessionKey]struct{}, len(rows))
	for _, row := range rows {
		if row.endAt > 0 {
			ended[sessionKey{harness: row.harness, session: row.session}] = struct{}{}
			if err := closeSessionBindingTx(bounded, tx, ir.HarnessID(row.harness), row.session, row.endAt); err != nil {
				return SessionStateRebuildOutcome{}, err
			}
		}
		if _, err := tx.ExecContext(bounded, `
			INSERT INTO pasture_session_state
				(harness, session, first_seen, last_seen, last_event, last_journal_id, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			row.harness, row.session, row.firstSeen, row.lastSeen, row.lastEvent, row.lastJournalID, row.lastSeen); err != nil {
			return SessionStateRebuildOutcome{}, sessionClaimError("the store refused a rebuilt session-state row: "+err.Error(), "repair the database before rebuilding the session cache")
		}
	}
	// The inferred rows are written under their own savepoint: the inference
	// cache is additive, so a fault writing it must not roll back the activity
	// cache that the read model depends on. The derivation ran before the
	// transaction; this is only the write. A failed plan skips this block
	// entirely, which is what leaves the previous derivation in place.
	if inferencePlanned {
		if _, err := tx.ExecContext(bounded, `SAVEPOINT session_inference`); err != nil {
			return SessionStateRebuildOutcome{}, sessionClaimError("the store refused the session inference savepoint: "+err.Error(), "release other store writers or repair the database, then retry")
		}
		if inferenceWriteErr := replaceSessionInferredBindings(bounded, tx, inferred, explicitBindings, ended); inferenceWriteErr != nil {
			if _, err := tx.ExecContext(bounded, `ROLLBACK TO SAVEPOINT session_inference`); err != nil {
				return SessionStateRebuildOutcome{}, sessionClaimError("the store refused to roll back the failed session inference write: "+err.Error(), "repair the database before rebuilding the session cache")
			}
			// The rollback restores the pre-savepoint cache, which may hold an
			// open interval for a session this rebuild already saw end. Clearing
			// the cache AFTER the rollback is what keeps "an ended session keeps
			// no inferred interval" true even when the inference write fails.
			// The cache is disposable, so clearing all of it is honest: this
			// rebuild could not establish today's derivation and presents none
			// rather than a guess.
			if _, err := tx.ExecContext(bounded, `DELETE FROM pasture_session_inferred_binding`); err != nil {
				return SessionStateRebuildOutcome{}, sessionClaimError("the store refused to clear the inferred session bindings after a failed inference write: "+err.Error(), "repair the database before rebuilding the session cache")
			}
			writeSessionInferenceDiagnostic(storeWithJournal, sessionInferenceStageWrite, inferenceWriteErr)
		}
		if _, err := tx.ExecContext(bounded, `RELEASE SAVEPOINT session_inference`); err != nil {
			return SessionStateRebuildOutcome{}, sessionClaimError("the store refused to release the session inference savepoint: "+err.Error(), "repair the database before rebuilding the session cache")
		}
	}
	if _, err := tx.ExecContext(bounded, `
		UPDATE pasture_session_api_state
		SET revision = revision + 1, dirty = 0, completed_through_jid = ?
		WHERE singleton_id = 0`, completedThrough); err != nil {
		return SessionStateRebuildOutcome{}, sessionClaimError("the store refused the rebuilt session API marker: "+err.Error(), "repair the database before rebuilding the session cache")
	}
	var revision int64
	if err := tx.QueryRowContext(bounded, `SELECT revision FROM pasture_session_api_state WHERE singleton_id = 0`).Scan(&revision); err != nil {
		return SessionStateRebuildOutcome{}, sessionClaimError("the store refused the rebuilt session API revision read: "+err.Error(), "repair the database before rebuilding the session cache")
	}
	if err := tx.Commit(); err != nil {
		return SessionStateRebuildOutcome{}, sessionClaimError("the store refused to commit the session-state rebuild: "+err.Error(), "release other store writers or repair the database, then retry")
	}
	committed = true
	return SessionStateRebuildOutcome{Sessions: len(rows), Revision: revision, CompletedThroughJournalID: completedThrough}, nil
}

// readSessionRebuildRows folds the journal facts and the store's binding/claim
// tables into the rebuild rows. It also returns the sessions that carry an
// explicit binding row in ANY state, which is the authoritative set inference
// may never override; reading both from the transaction's own snapshot is what
// closes the window between the pre-transaction inference derivation and this
// commit.
func readSessionRebuildRows(ctx context.Context, tx *sql.Tx, facts []projection.OccurrenceFact) ([]*sessionRebuildRow, map[sessionKey]struct{}, error) {
	byKey := make(map[string]*sessionRebuildRow)
	get := func(harness, session string) *sessionRebuildRow {
		key := harness + "\x00" + session
		row := byKey[key]
		if row == nil {
			row = &sessionRebuildRow{harness: harness, session: session}
			byKey[key] = row
		}
		return row
	}

	// The facts are already journal-ordered and carry verified native bindings;
	// this loop only selects the session identity and folds activity.
	lastJournal := int64(-1)
	for _, fact := range facts {
		var session string
		for _, binding := range fact.Bindings {
			if binding.Kind != model.BindingSession {
				continue
			}
			if session != "" {
				return nil, nil, sessionClaimError("an occurrence declares more than one session identity", "repair the occurrence bindings before rebuilding the session cache")
			}
			session = binding.Value
		}
		if session == "" {
			continue
		}
		journalID := int64(fact.JournalID)
		if journalID == lastJournal {
			return nil, nil, sessionClaimError("an occurrence declares more than one session identity", "repair the occurrence bindings before rebuilding the session cache")
		}
		lastJournal = journalID
		harness := string(fact.Contract.Harness())
		row := get(harness, session)
		row.addTimestamp(fact.ReceivedAt)
		row.lastSeen = maxInt64(row.lastSeen, fact.ReceivedAt)
		if journalID >= row.lastJournalID {
			row.lastJournalID = journalID
			row.endAt = 0
			if nativeName, ok := registration.NativeName(fact.Contract, fact.Event); ok {
				row.lastEvent = nativeName
			}
			if isSessionEnd(ir.HarnessID(harness), fact.Contract, fact.Event) {
				row.endAt = fact.ReceivedAt
			}
		}
	}

	explicit := make(map[sessionKey]struct{}, 8)
	bindingRows, err := tx.QueryContext(ctx, `
		SELECT harness, session, bound_at, unbound_at
		FROM pasture_session_epoch_binding
		ORDER BY bound_at, rowid`)
	if err != nil {
		return nil, nil, sessionClaimError("the store refused the binding rebuild read: "+err.Error(), "repair the database before rebuilding the session cache")
	}
	defer bindingRows.Close()
	for bindingRows.Next() {
		var harness, session string
		var boundAt int64
		var unboundAt sql.NullInt64
		if err := bindingRows.Scan(&harness, &session, &boundAt, &unboundAt); err != nil {
			return nil, nil, sessionClaimError("a rebuilt binding row could not be decoded: "+err.Error(), "repair the database before rebuilding the session cache")
		}
		explicit[sessionKey{harness: harness, session: session}] = struct{}{}
		row := get(harness, session)
		row.addTimestamp(boundAt)
		if unboundAt.Valid {
			row.addTimestamp(unboundAt.Int64)
		}
	}
	if err := bindingRows.Err(); err != nil {
		return nil, nil, sessionClaimError("the binding rebuild read failed: "+err.Error(), "repair the database before rebuilding the session cache")
	}

	claimRows, err := tx.QueryContext(ctx, `
		SELECT harness, session, claimed_at
		FROM pasture_session_claim`)
	if err != nil {
		return nil, nil, sessionClaimError("the store refused the claim rebuild read: "+err.Error(), "repair the database before rebuilding the session cache")
	}
	defer claimRows.Close()
	for claimRows.Next() {
		var harness, session string
		var claimedAt int64
		if err := claimRows.Scan(&harness, &session, &claimedAt); err != nil {
			return nil, nil, sessionClaimError("a rebuilt claim row could not be decoded: "+err.Error(), "repair the database before rebuilding the session cache")
		}
		get(harness, session).addTimestamp(claimedAt)
	}
	if err := claimRows.Err(); err != nil {
		return nil, nil, sessionClaimError("the claim rebuild read failed: "+err.Error(), "repair the database before rebuilding the session cache")
	}

	out := make([]*sessionRebuildRow, 0, len(byKey))
	for _, row := range byKey {
		if row.firstSeen == 0 {
			row.firstSeen = row.lastSeen
		}
		if row.lastSeen == 0 {
			row.lastSeen = row.firstSeen
		}
		out = append(out, row)
	}
	return out, explicit, nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
