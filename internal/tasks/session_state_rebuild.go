package tasks

import (
	"context"
	"database/sql"

	"github.com/dayvidpham/pasture/artifact"
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

// RebuildSessionState rebuilds the disposable occurrence projection and the
// session cache in ONE immediate transaction. The binding rows own the end;
// activity fields are derived from the latest occurrence plus the first/last
// timestamps of bindings and the first-claim-wins claim.
func RebuildSessionState(ctx context.Context, tracker protocol.TaskTracker) (SessionStateRebuildOutcome, error) {
	store, ok := tracker.(sessionTailStore)
	if !ok || store.auditDBHandle() == nil {
		return SessionStateRebuildOutcome{}, sessionClaimError("the tracker has no unified database handle", "use tasks.OpenTaskTracker")
	}
	bounded, cancel := context.WithTimeout(ctx, store.lifecycleTimeoutProfile().WorkflowResult())
	defer cancel()
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

	reader, err := provenance.NewTransactionEvidenceReader(bounded, tx)
	if err != nil {
		return SessionStateRebuildOutcome{}, sessionClaimError("the transaction-bound journal reader could not start: "+err.Error(), "repair the journal before rebuilding the session cache")
	}
	if err := projection.RebuildOccurrencesInTx(bounded, tx, reader); err != nil {
		return SessionStateRebuildOutcome{}, err
	}

	page, err := reader.QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  sessionOccurrenceEvidenceKinds,
		Page:   provenance.FactPageRequest{Limit: 1},
	})
	if err != nil {
		return SessionStateRebuildOutcome{}, sessionClaimError("the store refused the rebuild watermark read: "+err.Error(), "repair the journal before rebuilding the session cache")
	}
	completedThrough := int64(page.SnapshotMaxJournalID)

	rows, err := readSessionRebuildRows(bounded, tx)
	if err != nil {
		return SessionStateRebuildOutcome{}, err
	}
	if _, err := tx.ExecContext(bounded, `DELETE FROM pasture_session_state`); err != nil {
		return SessionStateRebuildOutcome{}, sessionClaimError("the store refused to clear the session-state cache: "+err.Error(), "repair the database before rebuilding the session cache")
	}
	for _, row := range rows {
		if row.endAt > 0 {
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

func readSessionRebuildRows(ctx context.Context, tx *sql.Tx) ([]*sessionRebuildRow, error) {
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

	// The occurrence join is restricted to session bindings. The projection
	// enforces one session identity per occurrence, but the rebuild still
	// detects a second one rather than silently choosing.
	occurrenceRows, err := tx.QueryContext(ctx, `
		SELECT o.journal_id, o.contract, o.event_kind, o.received_at, b.binding_value
		FROM lifecycle_occurrences o
		JOIN lifecycle_occurrence_bindings b ON b.journal_id = o.journal_id
		WHERE b.binding_kind = ?
		ORDER BY o.journal_id, b.binding_index`, uint8(model.BindingSession))
	if err != nil {
		return nil, sessionClaimError("the store refused the occurrence rebuild read: "+err.Error(), "repair the projection before rebuilding the session cache")
	}
	defer occurrenceRows.Close()
	lastJournal := int64(-1)
	for occurrenceRows.Next() {
		var journalID, receivedAt, eventKind int64
		var contract string
		var session []byte
		if err := occurrenceRows.Scan(&journalID, &contract, &eventKind, &receivedAt, &session); err != nil {
			return nil, sessionClaimError("a rebuilt occurrence row could not be decoded: "+err.Error(), "repair the projection before rebuilding the session cache")
		}
		if journalID == lastJournal {
			return nil, sessionClaimError("an occurrence declares more than one session identity", "repair the occurrence bindings before rebuilding the session cache")
		}
		lastJournal = journalID
		contractID, err := artifact.ParseRuntimeContractID(contract)
		if err != nil {
			return nil, sessionClaimError("a rebuilt occurrence cites an invalid runtime contract: "+err.Error(), "repair the projection before rebuilding the session cache")
		}
		harness := string(ir.HarnessID(contractID.Harness()))
		row := get(harness, string(session))
		row.addTimestamp(receivedAt)
		row.lastSeen = maxInt64(row.lastSeen, receivedAt)
		if journalID >= row.lastJournalID {
			row.lastJournalID = journalID
			row.endAt = 0
			kind := model.ContractEventKind(eventKind)
			if nativeName, ok := registration.NativeName(contractID, kind); ok {
				row.lastEvent = nativeName
			}
			if isSessionEnd(ir.HarnessID(harness), contractID, kind) {
				row.endAt = receivedAt
			}
		}
	}
	if err := occurrenceRows.Err(); err != nil {
		return nil, sessionClaimError("the occurrence rebuild read failed: "+err.Error(), "repair the projection before rebuilding the session cache")
	}

	bindingRows, err := tx.QueryContext(ctx, `
		SELECT harness, session, bound_at, unbound_at
		FROM pasture_session_epoch_binding
		ORDER BY bound_at, rowid`)
	if err != nil {
		return nil, sessionClaimError("the store refused the binding rebuild read: "+err.Error(), "repair the database before rebuilding the session cache")
	}
	defer bindingRows.Close()
	for bindingRows.Next() {
		var harness, session string
		var boundAt int64
		var unboundAt sql.NullInt64
		if err := bindingRows.Scan(&harness, &session, &boundAt, &unboundAt); err != nil {
			return nil, sessionClaimError("a rebuilt binding row could not be decoded: "+err.Error(), "repair the database before rebuilding the session cache")
		}
		row := get(harness, session)
		row.addTimestamp(boundAt)
		if unboundAt.Valid {
			row.addTimestamp(unboundAt.Int64)
		}
	}
	if err := bindingRows.Err(); err != nil {
		return nil, sessionClaimError("the binding rebuild read failed: "+err.Error(), "repair the database before rebuilding the session cache")
	}

	claimRows, err := tx.QueryContext(ctx, `
		SELECT harness, session, claimed_at
		FROM pasture_session_claim`)
	if err != nil {
		return nil, sessionClaimError("the store refused the claim rebuild read: "+err.Error(), "repair the database before rebuilding the session cache")
	}
	defer claimRows.Close()
	for claimRows.Next() {
		var harness, session string
		var claimedAt int64
		if err := claimRows.Scan(&harness, &session, &claimedAt); err != nil {
			return nil, sessionClaimError("a rebuilt claim row could not be decoded: "+err.Error(), "repair the database before rebuilding the session cache")
		}
		get(harness, session).addTimestamp(claimedAt)
	}
	if err := claimRows.Err(); err != nil {
		return nil, sessionClaimError("the claim rebuild read failed: "+err.Error(), "repair the database before rebuilding the session cache")
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
	return out, nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
