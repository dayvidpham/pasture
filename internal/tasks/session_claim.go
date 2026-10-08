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
// that already has a claim all return nil without reading a clock or taking a
// write lock beyond the claim read. The supplied actor is deliberately not
// resolved here: an unknown claim is still a claim, and is not an unbound
// session. The first claim wins; a claim that loses the race between this read
// and this insert is a silent no-op, not a fault, because the hot hook path
// must not fail every subsequent event of an already-claimed session.
func RecordLifecycleSessionClaim(ctx context.Context, tracker protocol.TaskTracker, harness ir.HarnessID, event model.ContractEventKind, bindings []model.NativeBinding, claim ActorClaim, clock receipt.Clock) error {
	if claim == "" {
		return nil
	}
	session, err := lifecycleSession(bindings)
	if err != nil {
		return err
	}
	if strings.TrimSpace(session) == "" {
		return nil
	}
	if ctx == nil || clock == nil || strings.TrimSpace(string(claim)) != string(claim) {
		return sessionClaimError("the context, clock, session identity, or actor claim is invalid", "pass a context, clock, verified session identity, and non-blank actor identifier")
	}
	store, ok := tracker.(lifecycleReceiptStore)
	if !ok || store.auditDBHandle() == nil {
		return sessionClaimError("the tracker has no unified database handle", "use tasks.OpenTaskTracker")
	}
	bounded, cancel := context.WithTimeout(ctx, store.lifecycleTimeoutProfile().SQLiteBusy())
	defer cancel()
	// READ FIRST. The common case on the hot hook path is a session that is
	// already claimed, and that case must take no write lock. A read fault is a
	// fault: reporting an already-claimed session as claimable would let a
	// second writer race the first.
	if _, present, readErr := readSessionClaimRow(bounded, store.auditDBHandle(), harness, session); readErr != nil {
		return sessionClaimError("the store refused the claim read: "+readErr.Error(), "release other store writers or repair the database, then start a new session")
	} else if present {
		return nil
	}
	result, err := store.auditDBHandle().ExecContext(bounded,
		`INSERT INTO pasture_session_claim (harness, session, actor, claimed_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(harness, session) DO NOTHING`, string(harness), session, string(claim), clock.Now().UnixNano())
	if err != nil {
		return sessionClaimError("the store refused the claim write: "+err.Error(), "release other store writers or repair the database, then start a new session")
	}
	if _, err := result.RowsAffected(); err != nil {
		return sessionClaimError("the store did not report whether it stored the claim: "+err.Error(), "inspect pasture_session_claim before you start a new session")
	}
	// A RowsAffected of 0 means another writer claimed the session between this
	// read and this insert. The first claim wins and this one is a silent no-op.
	return nil
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
