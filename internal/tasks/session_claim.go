package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ActorClaim is the actor identifier supplied by the host environment. Its zero
// value means no claim. It is session state, never a receipt author. An unknown
// actor is retained as claimed; the authority reader decides whether it exists.
type ActorClaim string

// SessionClaimState names what ONE session-claim attempt did. It is the typed
// half of the claim write's result: the hook path may never fail because a
// claim was not stored, so the caller that wants to know what happened reads
// this instead of an error.
type SessionClaimState int

const (
	// SessionClaimNotAttempted is the zero value: the invocation carried no
	// actor, carried no session binding, or was refused before the store was
	// reached, so no claim row was read or written. It is the zero value on
	// purpose, so a caller that ignores the outcome cannot mistake a skipped
	// claim for a written one.
	SessionClaimNotAttempted SessionClaimState = iota
	// SessionClaimWrote means this invocation inserted the claim row, so the
	// store now holds the actor it attempted.
	SessionClaimWrote
	// SessionClaimAlreadyClaimed means a claim row already existed; this
	// invocation read it and wrote nothing.
	SessionClaimAlreadyClaimed
	// SessionClaimLostRace means the INSERT ... ON CONFLICT DO NOTHING changed
	// no row because another writer claimed the session between this
	// invocation's read and its insert. StoredActor names the winner when it
	// could be read back.
	SessionClaimLostRace
)

// String returns the stable, lower-case spelling of the state, for a diagnostic
// a maintainer can grep. The zero value answers "not_attempted" rather than the
// empty string, so a member that was never set is distinguishable in text.
func (s SessionClaimState) String() string {
	switch s {
	case SessionClaimWrote:
		return "wrote"
	case SessionClaimAlreadyClaimed:
		return "already_claimed"
	case SessionClaimLostRace:
		return "lost_race"
	default:
		return "not_attempted"
	}
}

// SessionClaimOutcome is what one session-claim attempt did, plus the actor the
// store holds for the session afterwards. It is returned beside an error, and
// the error is reserved for a genuine fault: the three ordinary results — a
// written claim, an already-claimed session, and a claim that lost the race —
// are all nil-error outcomes, because none of them may fail the hook.
type SessionClaimOutcome struct {
	State SessionClaimState
	// StoredActor is the actor the store now holds for the session: the actor
	// read before a write that found an existing row, the actor this invocation
	// wrote, or the winner read back after a lost race. It is empty when no
	// claim row exists or the winner could not be read back.
	StoredActor string
}

// Mismatch reports whether this outcome names a stored actor different from the
// attempted one. It is true only for an already-claimed session or a lost race
// whose stored actor differs; a written claim and a skipped claim are never a
// mismatch. The comparison is deliberately not attempted for a state that
// stores nobody, so a non-empty actor on a sessionless event cannot manufacture
// a diagnostic about an actor nobody recorded.
func (o SessionClaimOutcome) Mismatch(attempted ActorClaim) bool {
	switch o.State {
	case SessionClaimAlreadyClaimed, SessionClaimLostRace:
		return o.StoredActor != string(attempted)
	default:
		return false
	}
}

// RecordLifecycleSessionClaim stores a claim the first time a session is
// observed. It reads the row for the event's session and, only when no row
// exists, performs one INSERT ... ON CONFLICT DO NOTHING with no retry.
//
// THE TRIGGER IS FIRST OBSERVATION, NOT THE SESSION-START EVENT. A host that
// publishes its session-start event before its plugins can subscribe leaves
// that first session unclaimed for its whole life — OpenCode v2 publishes
// session.created inside Session.create, and the plugin's subscription is
// established later with no replay — and every gate on it records UNBOUND and
// fails open. Binding on the first event that carries the session makes the
// claim independent of which event arrived first; a session-start event still
// binds when it is the first one observed. The rule is the same on every
// harness.
//
// A zero claim, an event with no session binding, and an event for a session
// that already has a claim all return a nil error without reading a clock or
// taking a write lock beyond the claim read. The supplied actor is deliberately
// not resolved here: an unknown claim is still a claim, and is not an unbound
// session. The first claim wins; a claim that loses the race between this read
// and this insert reports SessionClaimLostRace rather than a fault, because the
// hot hook path must not fail every subsequent event of an already-claimed
// session. The typed outcome is what lets a caller name the winner that the
// silent no-op used to discard.
func RecordLifecycleSessionClaim(ctx context.Context, tracker protocol.TaskTracker, harness ir.HarnessID, event model.ContractEventKind, bindings []model.NativeBinding, claim ActorClaim, clock receipt.Clock) (SessionClaimOutcome, error) {
	if claim == "" {
		return SessionClaimOutcome{}, nil
	}
	session, err := lifecycleSession(bindings)
	if err != nil {
		return SessionClaimOutcome{}, err
	}
	if strings.TrimSpace(session) == "" {
		return SessionClaimOutcome{}, nil
	}
	if ctx == nil || clock == nil || strings.TrimSpace(string(claim)) != string(claim) {
		return SessionClaimOutcome{}, sessionClaimError("the context, clock, session identity, or actor claim is invalid", "pass a context, clock, verified session identity, and non-blank actor identifier")
	}
	store, ok := tracker.(lifecycleReceiptStore)
	if !ok || store.auditDBHandle() == nil {
		return SessionClaimOutcome{}, sessionClaimError("the tracker has no unified database handle", "use tasks.OpenTaskTracker")
	}
	bounded, cancel := context.WithTimeout(ctx, store.lifecycleTimeoutProfile().SQLiteBusy())
	defer cancel()
	// READ FIRST. The common case on the hot hook path is a session that is
	// already claimed, and that case must take no write lock. A read fault is a
	// fault: reporting an already-claimed session as claimable would let a
	// second writer race the first.
	if stored, present, readErr := readSessionClaimRow(bounded, store.auditDBHandle(), harness, session); readErr != nil {
		return SessionClaimOutcome{}, sessionClaimError("the store refused the claim read: "+readErr.Error(), "release other store writers or repair the database, then start a new session")
	} else if present {
		return SessionClaimOutcome{State: SessionClaimAlreadyClaimed, StoredActor: stored}, nil
	}
	result, err := store.auditDBHandle().ExecContext(bounded,
		`INSERT INTO pasture_session_claim (harness, session, actor, claimed_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(harness, session) DO NOTHING`, string(harness), session, string(claim), clock.Now().UnixNano())
	if err != nil {
		return SessionClaimOutcome{}, sessionClaimError("the store refused the claim write: "+err.Error(), "release other store writers or repair the database, then start a new session")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return SessionClaimOutcome{}, sessionClaimError("the store did not report whether it stored the claim: "+err.Error(), "inspect pasture_session_claim before you start a new session")
	}
	if affected == 0 {
		// A RowsAffected of 0 means another writer claimed the session between
		// this read and this insert. The first claim wins and this one wrote
		// nothing. Naming the winner is BEST EFFORT: the receipt is already
		// committed and the first claim already stands, so a re-read fault must
		// not turn a lost race into a hook failure. An empty StoredActor is the
		// honest answer when the winner cannot be read back, and the caller's
		// diagnostic still fires because the attempted actor cannot equal it.
		winner, present, readErr := readSessionClaimRow(bounded, store.auditDBHandle(), harness, session)
		if readErr != nil || !present {
			return SessionClaimOutcome{State: SessionClaimLostRace}, nil
		}
		return SessionClaimOutcome{State: SessionClaimLostRace, StoredActor: winner}, nil
	}
	return SessionClaimOutcome{State: SessionClaimWrote, StoredActor: string(claim)}, nil
}

// lifecycleSession extracts the session identity from verified native
// bindings. Zero session bindings is "" and is not an error: the event carried
// no session identity. More than one is an error, because two session
// identities name two sessions and the gate cannot choose between them.
//
// Both the claim write and the gate read go through this helper, so the two
// paths can never disagree about which binding is the session.
func lifecycleSession(bindings []model.NativeBinding) (string, error) {
	var session string
	for _, binding := range bindings {
		if binding.Kind == model.BindingSession {
			if session != "" {
				return "", sessionClaimError("more than one session identity was supplied", "pass exactly one verified session binding")
			}
			session = binding.Value
		}
	}
	return session, nil
}

// LifecycleSession is the exported face of the helper above, and it exists for
// the one caller that cannot reach an unexported name in this package: the
// lifecycle gate, which asks the SAME question of the SAME bindings before it
// reads a claim. Two loops over the bindings could disagree about which one is
// the session — one reading a verified binding as an identity and the other
// skipping it — and the gate would then look up a session nobody ever claimed,
// which reads as an unbound session and proceeds. So the answer has one home.
func LifecycleSession(bindings []model.NativeBinding) (string, error) {
	return lifecycleSession(bindings)
}

// readLifecycleSessionClaim reads the actor claimed for one harness session.
//
// The whole read is one SELECT under the store's own SQLite busy tier, and the
// pooled connection is released before this function returns. That release is
// load-bearing, and the reason is the pool, not the claim: the ownership read
// that follows borrows the SAME *sql.DB through Provenance's borrowed open
// (open_unified.go), and that pool's default size is one connection, so a public
// call issued while this lease was still held would queue behind the very
// connection the reader is holding. The hazard is a self-wait at the POOL, and it
// is not even bounded by the caller's context: the borrowed handle's liveness
// precheck pings with a background context of its own, so only releasing the
// lease ends the wait (TestGateReaderPublicCallBorrowsTheStoresOwnConnection).
//
// present is false when no claim row exists, which is an unbound session and
// not an error. A returned error is a read fault; the caller decides its kind.
func (t *trackerImpl) readLifecycleSessionClaim(ctx context.Context, harness ir.HarnessID, session string) (actor string, present bool, err error) {
	bounded, cancel := context.WithTimeout(ctx, t.timeoutProfile.SQLiteBusy())
	defer cancel()
	return readSessionClaimRow(bounded, t.auditDB, harness, session)
}

// readSessionClaimRow is the ONE point lookup on (harness, session) that both
// the claim write and the gate read perform, so the two can never key
// differently. present is false when no claim row exists, which is an unbound
// session and not an error. A returned error is a read fault; the caller
// decides its kind. The caller owns the connection lease and releases it when
// the scan returns.
func readSessionClaimRow(ctx context.Context, db *sql.DB, harness ir.HarnessID, session string) (actor string, present bool, err error) {
	var claimed string
	scanErr := db.QueryRowContext(ctx,
		`SELECT actor FROM pasture_session_claim WHERE harness=? AND session=?`,
		string(harness), session,
	).Scan(&claimed)
	if scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, scanErr
	}
	return claimed, true, nil
}

func sessionClaimError(why, fix string) error {
	return fmt.Errorf("The session actor claim was refused because %s. Where: internal/tasks/session_claim.go, during the session claim write. Impact: this claim failure is not a policy decision; the receipt author remains the system actor. Fix: %s.", why, fix)
}
