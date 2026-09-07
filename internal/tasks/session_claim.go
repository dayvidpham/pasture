package tasks

import (
	"context"
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ActorClaim is the actor identifier supplied by the host environment. Its zero
// value means no claim. It is session state, never a receipt author. An unknown
// actor is retained as claimed; the authority reader decides whether it exists.
type ActorClaim string

// RecordLifecycleSessionClaim stores a claim after a valid native session-start
// receipt commits. It performs at most one INSERT, with no retry. A zero claim
// or an event other than session start does not read a clock or touch the store.
// The supplied actor is deliberately not resolved here: an unknown claim is
// still a claim, and is not an unbound session.
func RecordLifecycleSessionClaim(ctx context.Context, tracker protocol.TaskTracker, harness ir.HarnessID, event model.ContractEventKind, bindings []model.NativeBinding, claim ActorClaim, clock receipt.Clock) error {
	if claim == "" {
		return nil
	}
	start := (harness == ir.HarnessClaudeCode && event == registration.EventSessionStart) ||
		(harness == ir.HarnessCodex && event == registration.EventCodexSessionStart) ||
		(harness == ir.HarnessOpenCode && event == registration.EventOpenCodeSessionCreated)
	if !start {
		return nil
	}
	var session string
	for _, binding := range bindings {
		if binding.Kind == model.BindingSession {
			if session != "" {
				return sessionClaimError("more than one session identity was supplied", "pass the verified session-start bindings")
			}
			session = binding.Value
		}
	}
	if ctx == nil || clock == nil || strings.TrimSpace(session) == "" || strings.TrimSpace(string(claim)) != string(claim) {
		return sessionClaimError("the context, clock, session identity, or actor claim is invalid", "pass a context, clock, verified session identity, and non-blank actor identifier")
	}
	store, ok := tracker.(lifecycleReceiptStore)
	if !ok || store.auditDBHandle() == nil {
		return sessionClaimError("the tracker has no unified database handle", "use tasks.OpenTaskTracker")
	}
	bounded, cancel := context.WithTimeout(ctx, store.lifecycleTimeoutProfile().SQLiteBusy())
	defer cancel()
	result, err := store.auditDBHandle().ExecContext(bounded,
		`INSERT INTO pasture_session_claim (harness, session, actor, claimed_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(harness, session) DO NOTHING`, string(harness), session, string(claim), clock.Now().UnixNano())
	if err != nil {
		return sessionClaimError("the store refused the claim write: "+err.Error(), "release other store writers or repair the database, then start a new session")
	}
	written, err := result.RowsAffected()
	if err != nil {
		return sessionClaimError("the store did not report whether it stored the claim: "+err.Error(), "inspect pasture_session_claim before you start a new session")
	}
	if written == 0 {
		return sessionClaimError(fmt.Sprintf("session %q on harness %q already has an actor claim; the first claim is unchanged", session, harness), "keep the original actor or start a new session to claim a different actor")
	}
	return nil
}

func sessionClaimError(why, fix string) error {
	return fmt.Errorf("The session actor claim was refused because %s. Where: internal/tasks/session_claim.go, during the session-start claim write. Impact: this claim failure is not a policy decision; the receipt author remains the system actor. Fix: %s.", why, fix)
}
