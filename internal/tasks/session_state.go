package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
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
	Harness   ir.HarnessID
	Contract  ir.RuntimeContractID
	EventKind model.ContractEventKind
	Bindings  []model.NativeBinding
	// NativeBody and Capture are supplied by the admitted parser on direct and
	// served paths. Legacy replay without a retained body cannot prove resume.
	NativeBody   []byte
	Capture      model.CaptureDisposition
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
	// The result is true only when this delivery changes cached facts. An old
	// or duplicate delivery must not mutate bindings or advance the revision.
	UpsertSessionState(ctx context.Context, tx *sql.Tx, in SessionTailInput, session string) (bool, error)
}

// unifiedStoreHandle is the borrowable unified-store write handle shared by the
// session and project API stores. It intentionally names neither feature: both
// stores read and write the same pasture.db file.
type unifiedStoreHandle interface {
	auditDBHandle() *sql.DB
	lifecycleTimeoutProfile() timeouts.Profile
}

type sqlSessionStateWriter struct{}

func (sqlSessionStateWriter) UpsertSessionState(ctx context.Context, tx *sql.Tx, in SessionTailInput, session string) (bool, error) {
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
	store, ok := tracker.(unifiedStoreHandle)
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
	changed, stateErr := writer.UpsertSessionState(bounded, tx, in, session)
	if stateErr == nil && changed && sessionTailTransition(in) == model.SessionTransitionEnd {
		var unsuperseded bool
		stateErr = tx.QueryRowContext(bounded, `SELECT host_end_journal_id = ? AND host_end_journal_id > last_resume_journal_id FROM pasture_session_state WHERE harness=? AND session=?`, in.OccurrenceID.JournalID(), string(in.Harness), session).Scan(&unsuperseded)
		if stateErr == nil && unsuperseded {
			stateErr = closeSessionBindingTx(bounded, tx, in.Harness, session, in.RecordedAt.UnixNano())
		}
	}
	if stateErr != nil {
		// The savepoint is still open when UpsertSessionState fails. Roll it
		// back and preserve the claim, then mark the cache dirty in the same
		// outer transaction so the marker cannot outlive a failed claim.
		if _, err := tx.ExecContext(bounded, `ROLLBACK TO SAVEPOINT session_state`); err != nil {
			return out, sessionClaimError("the store refused to roll back the failed session-state write: "+err.Error(), "repair the database before retrying the delivery")
		}
		if _, err := tx.ExecContext(bounded, `RELEASE SAVEPOINT session_state`); err != nil {
			return out, sessionClaimError("the store refused to release the failed session-state savepoint: "+err.Error(), "repair the database before retrying the delivery")
		}
		if _, err := tx.ExecContext(bounded, `UPDATE pasture_session_api_state SET dirty = 1, revision = revision + 1 WHERE singleton_id = 0`); err != nil {
			return out, sessionClaimError("the store refused to mark the failed session-state cache dirty: "+err.Error(), "repair the database before rebuilding the cache from the committed journal")
		}
		out.State.Dirty = true
		if in.Diagnostics != nil {
			fmt.Fprintf(in.Diagnostics, "pasture: session state cache write failed for harness %q session %q: %v; the receipt remains committed and the cache is marked dirty for the next rebuild\n", string(in.Harness), session, stateErr)
		}
	} else {
		if _, err := tx.ExecContext(bounded, `RELEASE SAVEPOINT session_state`); err != nil {
			return out, sessionClaimError("the store refused to release the session-state savepoint: "+err.Error(), "repair the database before retrying the delivery")
		}
		if changed || out.Claim.State == SessionClaimWrote {
			if _, err := tx.ExecContext(bounded, `UPDATE pasture_session_api_state SET revision = revision + 1 WHERE singleton_id = 0`); err != nil {
				return out, sessionClaimError("the store refused the session API revision update: "+err.Error(), "repair the database before retrying the delivery")
			}
		}
		// Only a completed journal scan certifies completed_through_jid. A hot
		// tail can settle beyond an earlier committed receipt with no tail yet.
		out.State.Written = changed
	}

	if err := tx.Commit(); err != nil {
		return out, sessionClaimError("the store refused to commit the session-tail transaction: "+err.Error(), "release other store writers or repair the database, then retry the delivery")
	}
	return out, nil
}

func sessionTailTransition(in SessionTailInput) model.SessionTransition {
	if in.NativeBody != nil {
		return ingress.ExtractSessionTransition(in.Contract, in.EventKind, in.NativeBody, in.Capture, in.Bindings)
	}
	// Old occurrence rows already record their admitted event coordinate. Their
	// end meaning remains readable without a body; resume never gets guessed.
	if isSessionEnd(in.Harness, in.Contract, in.EventKind) {
		return model.SessionTransitionEnd
	}
	return model.SessionTransitionNone
}

// MarkSessionStateDirty marks the session cache behind. The raw lifecycle
// import path commits occurrences without running the claim tail, so it uses
// this marker to force a rebuild before the next serve reads the cache.
func MarkSessionStateDirty(ctx context.Context, tracker protocol.TaskTracker) error {
	if ctx == nil {
		return sessionClaimError("the context is invalid", "pass a live context")
	}
	store, ok := tracker.(unifiedStoreHandle)
	if !ok || store.auditDBHandle() == nil {
		return sessionClaimError("the tracker has no unified database handle", "use tasks.OpenTaskTracker")
	}
	bounded, cancel := context.WithTimeout(ctx, store.lifecycleTimeoutProfile().Ingress())
	defer cancel()
	if _, err := store.auditDBHandle().ExecContext(bounded, `UPDATE pasture_session_api_state SET dirty = 1, revision = revision + 1 WHERE singleton_id = 0`); err != nil {
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
func upsertSessionStateTx(ctx context.Context, tx *sql.Tx, in SessionTailInput, session string) (bool, error) {
	// Unknown kinds have no evidenced native name; do not invent one.
	nativeName, _ := registration.NativeName(in.Contract, in.EventKind)
	recordedAt := in.RecordedAt.UnixNano()
	journalID := int64(in.OccurrenceID.JournalID())
	var promptAt, promptID, endAt, endID, resumeAt, resumeID int64
	switch sessionTailTransition(in) {
	case model.SessionTransitionPrompt:
		promptAt, promptID = recordedAt, journalID
	case model.SessionTransitionEnd:
		endAt, endID = recordedAt, journalID
	case model.SessionTransitionResume:
		resumeAt, resumeID = recordedAt, journalID
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO pasture_session_state
			(harness, session, first_seen, last_seen, last_event, last_journal_id, updated_at,
			 last_prompt_at,last_prompt_journal_id,host_end_at,host_end_journal_id,last_resume_at,last_resume_journal_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(harness, session) DO UPDATE SET
			last_seen = CASE WHEN excluded.last_journal_id > pasture_session_state.last_journal_id THEN excluded.last_seen ELSE pasture_session_state.last_seen END,
			last_event = CASE WHEN excluded.last_journal_id > pasture_session_state.last_journal_id THEN excluded.last_event ELSE pasture_session_state.last_event END,
			last_journal_id = max(pasture_session_state.last_journal_id, excluded.last_journal_id),
			last_prompt_at = CASE WHEN excluded.last_prompt_journal_id > pasture_session_state.last_prompt_journal_id THEN excluded.last_prompt_at ELSE pasture_session_state.last_prompt_at END,
			last_prompt_journal_id = max(pasture_session_state.last_prompt_journal_id,excluded.last_prompt_journal_id),
			host_end_at = CASE WHEN excluded.host_end_journal_id > pasture_session_state.host_end_journal_id THEN excluded.host_end_at ELSE pasture_session_state.host_end_at END,
			host_end_journal_id = max(pasture_session_state.host_end_journal_id,excluded.host_end_journal_id),
			last_resume_at = CASE WHEN excluded.last_resume_journal_id > pasture_session_state.last_resume_journal_id THEN excluded.last_resume_at ELSE pasture_session_state.last_resume_at END,
			last_resume_journal_id = max(pasture_session_state.last_resume_journal_id,excluded.last_resume_journal_id),
			updated_at = max(pasture_session_state.updated_at, excluded.updated_at)
		WHERE excluded.last_journal_id > pasture_session_state.last_journal_id
		   OR excluded.last_prompt_journal_id > pasture_session_state.last_prompt_journal_id
		   OR excluded.host_end_journal_id > pasture_session_state.host_end_journal_id
		   OR excluded.last_resume_journal_id > pasture_session_state.last_resume_journal_id`,
		string(in.Harness), session, recordedAt, recordedAt, nativeName, journalID, recordedAt, promptAt, promptID, endAt, endID, resumeAt, resumeID)
	if err != nil {
		return false, sessionClaimError("the store refused the session-state write: "+err.Error(), "repair the database before retrying the delivery")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, sessionClaimError("the store did not report the session-state change: "+err.Error(), "repair the database before rebuilding the cache")
	}
	return affected > 0, nil
}

// isSessionEnd names the one host lifecycle event that ends an open binding.
// OpenCode's sessionless end events never carry a session binding and are not
// part of this predicate; an OpenCode session ends through explicit unbind.
func isSessionEnd(harness ir.HarnessID, contract ir.RuntimeContractID, kind model.ContractEventKind) bool {
	if harness != ir.HarnessClaudeCode && harness != ir.HarnessCodex {
		return false
	}
	event, ok := registration.EventFor(contract, kind)
	return ok && event.SessionTransition == model.SessionTransitionEnd
}

func closeSessionBindingTx(ctx context.Context, tx *sql.Tx, harness ir.HarnessID, session string, at int64) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE pasture_session_epoch_binding
		SET unbound_at = ?, unbound_reason = 'session_end'
		WHERE harness = ? AND session = ? AND unbound_at IS NULL AND bound_at <= ?`, at, string(harness), session, at); err != nil {
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
