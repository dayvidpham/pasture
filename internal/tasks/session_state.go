package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// SessionTailInput is the post-receipt continuation shared by the direct hook
// handler and the daemon lane. It carries exactly the committed receipt facts
// plus the verified event bindings.
type SessionTailInput struct {
	Harness      ir.HarnessID
	Contract     ir.RuntimeContractID
	EventKind    model.ContractEventKind
	Bindings     []model.NativeBinding
	OccurrenceID model.OccurrenceID
	RecordedAt   time.Time
	ActorClaim   ActorClaim
	Clock        receipt.Clock
	StateWriter  SessionStateWriter
	Diagnostics  io.Writer
}

// SessionStateOutcome reports what the cache write did. A state-only failure
// is not a host failure; the receipt is already committed.
type SessionStateOutcome struct {
	Written bool
	Dirty   bool
}

// SessionTailOutcome is the full post-receipt outcome.
type SessionTailOutcome struct {
	Claim SessionClaimOutcome
	State SessionStateOutcome
}

// SessionStateWriter is the injectable cache write seam. Production uses the
// SQL implementation; tests inject a failing writer to prove that the claim
// survives and the dirty marker is set.
type SessionStateWriter interface {
	UpsertSessionState(ctx context.Context, tx *sql.Tx, in SessionTailInput, session string) error
}

type sessionTailStore interface {
	auditDBHandle() *sql.DB
	lifecycleTimeoutProfile() timeouts.Profile
}

type sqlSessionStateWriter struct{}

func (sqlSessionStateWriter) UpsertSessionState(ctx context.Context, tx *sql.Tx, in SessionTailInput, session string) error {
	return upsertSessionStateTx(ctx, tx, in, session)
}

// RecordLifecycleSessionTail writes the first-claim-wins claim, the session
// cache, and the API revision in one bounded transaction. It runs after the
// lifecycle receipt has committed, from both the direct handler and the daemon
// lane's Settle path.
//
// The claim keeps its existing host contract: a claim fault is returned and the
// caller wraps it with ErrLifecycleCommittedWithoutContinuation. A state-write
// fault is isolated by a savepoint: the claim is preserved, the cache is marked
// dirty, one diagnostic is written, and the caller receives no error.
func RecordLifecycleSessionTail(ctx context.Context, tracker protocol.TaskTracker, in SessionTailInput) (SessionTailOutcome, error) {
	var out SessionTailOutcome
	if ctx == nil || in.Clock == nil {
		return out, sessionClaimError("the context or clock is invalid", "pass a live context and the store clock")
	}
	if in.OccurrenceID.JournalID() == 0 || in.RecordedAt.IsZero() {
		return out, sessionClaimError("the committed occurrence identity or recorded time is missing", "pass the settled receipt identity and its recorded time")
	}
	session, err := lifecycleSession(in.Bindings)
	if err != nil {
		return out, err
	}
	if session == "" {
		return out, nil
	}
	store, ok := tracker.(sessionTailStore)
	if !ok || store.auditDBHandle() == nil {
		return out, sessionClaimError("the tracker has no unified database handle", "use tasks.OpenTaskTracker")
	}
	bounded, cancel := context.WithTimeout(ctx, store.lifecycleTimeoutProfile().Ingress())
	defer cancel()

	tx, err := store.auditDBHandle().BeginTx(bounded, nil)
	if err != nil {
		return out, sessionClaimError("the store refused the session-tail transaction: "+err.Error(), "release other store writers or repair the database, then retry the delivery")
	}
	defer func() { _ = tx.Rollback() }()

	if in.ActorClaim != "" {
		claim, claimErr := claimSessionTx(bounded, tx, in.Harness, session, in.ActorClaim, in.Clock)
		if claimErr != nil {
			return out, claimErr
		}
		out.Claim = claim
	}

	writer := in.StateWriter
	if writer == nil {
		writer = sqlSessionStateWriter{}
	}
	if _, err := tx.ExecContext(bounded, `SAVEPOINT session_state`); err != nil {
		return out, sessionClaimError("the store refused the session-state savepoint: "+err.Error(), "release other store writers or repair the database, then retry the delivery")
	}
	stateErr := writer.UpsertSessionState(bounded, tx, in, session)
	if stateErr == nil && isSessionEnd(in.Harness, in.Contract, in.EventKind) {
		stateErr = closeSessionBindingTx(bounded, tx, in.Harness, session, in.RecordedAt.UnixNano())
	}
	if stateErr != nil {
		// The savepoint is still open when UpsertSessionState fails. Roll it
		// back and preserve the claim, then mark the cache dirty in the same
		// outer transaction so the marker cannot outlive a failed claim.
		if _, err := tx.ExecContext(bounded, `ROLLBACK TO SAVEPOINT session_state`); err != nil {
			return out, sessionClaimError("the store refused to roll back the failed session-state write: "+err.Error(), "repair the database before retrying the delivery")
		}
		_, _ = tx.ExecContext(bounded, `RELEASE SAVEPOINT session_state`)
		_, _ = tx.ExecContext(bounded, `UPDATE pasture_session_api_state SET dirty = 1 WHERE singleton_id = 0`)
		out.State.Dirty = true
		if in.Diagnostics != nil {
			fmt.Fprintf(in.Diagnostics, "pasture: session state cache write failed for harness %q session %q: %v; the receipt remains committed and the cache is marked dirty for the next rebuild\n", string(in.Harness), session, stateErr)
		}
	} else {
		if _, err := tx.ExecContext(bounded, `RELEASE SAVEPOINT session_state`); err != nil {
			return out, sessionClaimError("the store refused to release the session-state savepoint: "+err.Error(), "repair the database before retrying the delivery")
		}
		if _, err := tx.ExecContext(bounded,
			`UPDATE pasture_session_api_state SET revision = revision + 1, completed_through_jid = max(completed_through_jid, ?) WHERE singleton_id = 0`,
			in.OccurrenceID.JournalID()); err != nil {
			return out, sessionClaimError("the store refused the session API revision update: "+err.Error(), "repair the database before retrying the delivery")
		}
		out.State.Written = true
	}

	if err := tx.Commit(); err != nil {
		return out, sessionClaimError("the store refused to commit the session-tail transaction: "+err.Error(), "release other store writers or repair the database, then retry the delivery")
	}
	return out, nil
}

// MarkSessionStateDirty marks the session cache behind. The raw lifecycle
// import path commits occurrences without running the claim tail, so it uses
// this marker to force a rebuild before the next serve reads the cache.
func MarkSessionStateDirty(ctx context.Context, tracker protocol.TaskTracker) error {
	if ctx == nil {
		return sessionClaimError("the context is invalid", "pass a live context")
	}
	store, ok := tracker.(sessionTailStore)
	if !ok || store.auditDBHandle() == nil {
		return sessionClaimError("the tracker has no unified database handle", "use tasks.OpenTaskTracker")
	}
	bounded, cancel := context.WithTimeout(ctx, store.lifecycleTimeoutProfile().Ingress())
	defer cancel()
	if _, err := store.auditDBHandle().ExecContext(bounded, `UPDATE pasture_session_api_state SET dirty = 1 WHERE singleton_id = 0`); err != nil {
		return sessionClaimError("the store refused the session-state dirty marker: "+err.Error(), "repair the database before retrying")
	}
	return nil
}

// claimSessionTx is the transaction-scoped first-claim-wins claim. It mirrors
// RecordLifecycleSessionClaim's read-first fast path but keeps every statement
// on the caller's transaction.
func claimSessionTx(ctx context.Context, tx *sql.Tx, harness ir.HarnessID, session string, actor ActorClaim, clock receipt.Clock) (SessionClaimOutcome, error) {
	var stored string
	err := tx.QueryRowContext(ctx, `SELECT actor FROM pasture_session_claim WHERE harness=? AND session=?`, string(harness), session).Scan(&stored)
	if err == nil {
		return SessionClaimOutcome{State: SessionClaimAlreadyClaimed, StoredActor: stored}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SessionClaimOutcome{}, sessionClaimError("the store refused the claim read: "+err.Error(), "release other store writers or repair the database, then retry the delivery")
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO pasture_session_claim (harness, session, actor, claimed_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(harness, session) DO NOTHING`, string(harness), session, string(actor), clock.Now().UnixNano())
	if err != nil {
		return SessionClaimOutcome{}, sessionClaimError("the store refused the claim write: "+err.Error(), "release other store writers or repair the database, then retry the delivery")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return SessionClaimOutcome{}, sessionClaimError("the store did not report whether it stored the claim: "+err.Error(), "inspect pasture_session_claim before starting a new session")
	}
	if affected == 0 {
		var winner string
		_ = tx.QueryRowContext(ctx, `SELECT actor FROM pasture_session_claim WHERE harness=? AND session=?`, string(harness), session).Scan(&winner)
		return SessionClaimOutcome{State: SessionClaimLostRace, StoredActor: winner}, nil
	}
	return SessionClaimOutcome{State: SessionClaimWrote, StoredActor: string(actor)}, nil
}

// upsertSessionStateTx writes the activity cache. first_seen is preserved on
// update; last_seen/last_event follow the highest journal id so an out-of-order
// delivery cannot rewind the cache.
func upsertSessionStateTx(ctx context.Context, tx *sql.Tx, in SessionTailInput, session string) error {
	nativeName, ok := registration.NativeName(in.Contract, in.EventKind)
	if !ok {
		nativeName = fmt.Sprintf("event-%d", in.EventKind)
	}
	recordedAt := in.RecordedAt.UnixNano()
	journalID := int64(in.OccurrenceID.JournalID())
	_, err := tx.ExecContext(ctx, `
		INSERT INTO pasture_session_state
			(harness, session, first_seen, last_seen, last_event, last_journal_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(harness, session) DO UPDATE SET
			last_seen = CASE WHEN excluded.last_journal_id >= pasture_session_state.last_journal_id THEN excluded.last_seen ELSE pasture_session_state.last_seen END,
			last_event = CASE WHEN excluded.last_journal_id >= pasture_session_state.last_journal_id THEN excluded.last_event ELSE pasture_session_state.last_event END,
			last_journal_id = max(pasture_session_state.last_journal_id, excluded.last_journal_id),
			updated_at = max(pasture_session_state.updated_at, excluded.updated_at)`,
		string(in.Harness), session, recordedAt, recordedAt, nativeName, journalID, recordedAt)
	if err != nil {
		return sessionClaimError("the store refused the session-state write: "+err.Error(), "repair the database before retrying the delivery")
	}
	return nil
}

// isSessionEnd names the one host lifecycle event that ends an open binding.
// OpenCode's sessionless end events never carry a session binding and are not
// part of this predicate; an OpenCode session ends through explicit unbind.
func isSessionEnd(harness ir.HarnessID, contract ir.RuntimeContractID, kind model.ContractEventKind) bool {
	if harness != ir.HarnessClaudeCode && harness != ir.HarnessCodex {
		return false
	}
	name, ok := registration.NativeName(contract, kind)
	return ok && name == "SessionEnd"
}

func closeSessionBindingTx(ctx context.Context, tx *sql.Tx, harness ir.HarnessID, session string, at int64) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE pasture_session_epoch_binding
		SET unbound_at = ?, unbound_reason = 'session_end'
		WHERE harness = ? AND session = ? AND unbound_at IS NULL`, at, string(harness), session); err != nil {
		return sessionClaimError("the store refused to close the session binding: "+err.Error(), "repair the database before retrying the delivery")
	}
	// An inferred binding never outlives the session it was derived for. The
	// row is cleared here rather than merely closed: inference keeps no history,
	// and a closed interval would state an end instant the store only knows as
	// an upper bound.
	if err := clearSessionInferredBindings(ctx, tx, string(harness), session); err != nil {
		return err
	}
	return nil
}
