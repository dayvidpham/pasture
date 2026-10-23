package tasks

// session_infer.go derives a provisional session-to-epoch binding from the
// store's own claim and ownership facts.
//
// THE RULE. A claimed session gets an inferred binding only when the claim
// actor owns EXACTLY ONE active owner-responsibility episode whose task carries
// an epoch_state_projection row. Zero candidates, more than one candidate, an
// actor the store does not know, and ownership of tasks that are not epochs all
// produce NO inference. There is no fallback and no tie-break: a session with
// ambiguous ownership stays unbound, because a wrong epoch is worse than an
// absent one.
//
// WHERE IT RUNS. Only inside RebuildSessionState, and therefore only through
// EnsureSessionState. Inference is a cache-rebuild concern and never a hook
// ingress concern: the hook path must not pay for an ownership read, and a
// lifecycle event must never write an inferred row.
//
// WHAT IT MAY TOUCH. pasture_session_inferred_binding and nothing else. An
// inferred row is never written into pasture_session_epoch_binding. An explicit
// binding row of ANY state is authoritative: if a session has ever been
// explicitly bound, the rebuild does not infer for it. That is what makes an
// explicit unbind stick — unbind closes the explicit row instead of deleting it,
// so the next rebuild still sees the authoritative history and leaves the
// session unbound rather than resurrecting a guess. A session that was never
// explicitly bound can still be ended by its host; the session-end path clears
// its inferred rows (see closeSessionBindingTx in session_state.go).
//
// READ ORDER. Everything that can lease a second connection is read BEFORE the
// rebuild's write transaction opens. The store's pool is one connection in
// production, so a pooled ownership read issued while the rebuild holds its
// write transaction would queue behind that transaction's own connection and
// wait forever.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/dayvidpham/provenance"
)

const (
	// sessionInferredBindingTable is the disposable inference cache.
	sessionInferredBindingTable = "pasture_session_inferred_binding"

	// sessionInferenceReason is the stable token recorded on every inferred
	// row. The read model exposes the column as history, so the value is a
	// token rather than a sentence: wording may change, the meaning may not.
	sessionInferenceReason = "claim-ownership"

	// sessionInferenceRole is the assignment slot the derivation reads. The
	// row records the slot, not a protocol session role: no command gave this
	// session a role, and inventing one would be a guess. A test pins the token
	// against the gate vocabulary so the two cannot drift.
	sessionInferenceRole = "owner-responsibility"

	// sessionInferenceConfidence is the lifecycle confidence the claim-ownership
	// rule earns. The rule correlates a first-claim-wins claim with an active
	// ownership episode; no command recorded this session's intent, so calling
	// it "verified" would overstate it. The read model carries the same token.
	sessionInferenceConfidence = "observed"
)

// errSessionInferenceUnsupported reports a build whose store cannot answer the
// one ownership read inference is bound to. There is no fallback read: a second
// source would answer a different question than the one this rule is stated
// over.
var errSessionInferenceUnsupported = errors.New("the store's journal does not expose provenance.ActorOwnershipQueryAPI, which session inference is bound to")

// sessionInferredBinding is one row the rebuild may write into the inference
// cache.
type sessionInferredBinding struct {
	BindingID  string
	Harness    string
	Session    string
	EpochID    string
	Role       string
	BoundAt    int64
	InferredAt int64
	Reason     string
	Confidence string
}

// sessionKey identifies one session without a binding row.
type sessionKey struct {
	harness string
	session string
}

// sessionClaimRow is one first-claim-wins claim, read before the rebuild's
// write transaction.
type sessionClaimRow struct {
	harness   string
	session   string
	actor     string
	claimedAt int64
}

// planSessionInference computes the inferred rows for one rebuild. It reads
// only the store: the claim table, one provenance ownership read per distinct
// claim ACTOR (not per session), and the epoch projection inventory.
//
// A fault is returned, never guessed around: the caller records one diagnostic
// and rebuilds the session cache without inferred rows, which is the
// conservative direction the rule already takes for ambiguous ownership.
func planSessionInference(ctx context.Context, store lifecycleReceiptStore) ([]sessionInferredBinding, error) {
	claims, err := readSessionClaims(ctx, store.auditDBHandle())
	if err != nil {
		return nil, err
	}
	if len(claims) == 0 {
		return nil, nil
	}
	epochs, err := readSessionEpochProjectionIDs(ctx, store.auditDBHandle())
	if err != nil {
		return nil, err
	}
	if len(epochs) == 0 {
		// No projected epoch exists, so no owned task can be an epoch task.
		return nil, nil
	}
	api, ok := store.Journal().(provenance.ActorOwnershipQueryAPI)
	if !ok {
		return nil, errSessionInferenceUnsupported
	}

	// One read per distinct actor keeps the cost proportional to the number of
	// actors, never to the number of sessions. The ordered slice keeps the read
	// order deterministic for tests and for an operator reading a store trace.
	actors := sessionClaimActors(claims)
	epochByActor := make(map[provenance.ActorID]string, len(actors))
	for _, actor := range actors {
		// No material or evidence kinds are requested: inference asks only
		// which tasks the actor owns, and asking for payloads the rule never
		// reads would enlarge the result for no answer.
		ownership, err := api.QueryActorOwnership(ctx, provenance.ActorOwnershipQuery{Actor: actor})
		if err != nil {
			return nil, fmt.Errorf("reading the active ownership of the session's claim actor %q: %w", actor.String(), err)
		}
		if !ownership.ActorKnown {
			// A claim may name an actor this store never registered. The store
			// cannot answer what that actor owns, so the session stays unbound.
			continue
		}
		if epochID, unambiguous := unambiguousOwnedEpoch(ownership.Tasks, epochs); unambiguous {
			epochByActor[actor] = epochID
		}
	}

	now := sessionInferenceNow(store)
	out := make([]sessionInferredBinding, 0, len(claims))
	for _, claim := range claims {
		// A claim whose actor is not a canonical actor identifier cannot be
		// attributed to any ownership, and is not an error for inference.
		actor, err := provenance.ParseActorID(claim.actor)
		if err != nil {
			continue
		}
		epochID, ok := epochByActor[actor]
		if !ok {
			continue
		}
		if claim.claimedAt <= 0 {
			// A claim with no instant cannot date an interval. Never backdate
			// from a zero value.
			continue
		}
		out = append(out, sessionInferredBinding{
			BindingID:  sessionInferredBindingID(claim.harness, claim.session, epochID),
			Harness:    claim.harness,
			Session:    claim.session,
			EpochID:    epochID,
			Role:       sessionInferenceRole,
			BoundAt:    claim.claimedAt,
			InferredAt: now,
			Reason:     sessionInferenceReason,
			Confidence: sessionInferenceConfidence,
		})
	}
	return out, nil
}

// unambiguousOwnedEpoch reports the single owned task that is an epoch, when
// exactly one such task is owned. Duplicate rows for one task are one candidate,
// not two.
func unambiguousOwnedEpoch(tasks []provenance.OwnedTaskRow, epochs map[string]struct{}) (string, bool) {
	var candidate string
	seen := make(map[string]struct{}, len(tasks))
	for _, row := range tasks {
		taskID := row.TaskID.String()
		if _, isEpoch := epochs[taskID]; !isEpoch {
			continue
		}
		if _, duplicate := seen[taskID]; duplicate {
			continue
		}
		seen[taskID] = struct{}{}
		candidate = taskID
	}
	return candidate, len(seen) == 1
}

// sessionClaimActors returns every distinct parseable actor in the claims, in a
// deterministic order.
func sessionClaimActors(claims []sessionClaimRow) []provenance.ActorID {
	seen := make(map[provenance.ActorID]struct{}, len(claims))
	actors := make([]provenance.ActorID, 0, len(claims))
	for _, claim := range claims {
		actor, err := provenance.ParseActorID(claim.actor)
		if err != nil {
			continue
		}
		if _, ok := seen[actor]; ok {
			continue
		}
		seen[actor] = struct{}{}
		actors = append(actors, actor)
	}
	sort.Slice(actors, func(i, j int) bool { return actors[i].String() < actors[j].String() })
	return actors
}

// readSessionClaims reads every first-claim-wins claim. Claims are the only
// sessions inference answers about; a session with no claim has no actor whose
// ownership could be read.
func readSessionClaims(ctx context.Context, db *sql.DB) ([]sessionClaimRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT harness, session, actor, claimed_at
		FROM pasture_session_claim
		ORDER BY harness, session`)
	if err != nil {
		return nil, fmt.Errorf("reading session claims for binding inference: %w", err)
	}
	defer rows.Close()
	out := make([]sessionClaimRow, 0, 16)
	for rows.Next() {
		var claim sessionClaimRow
		if err := rows.Scan(&claim.harness, &claim.session, &claim.actor, &claim.claimedAt); err != nil {
			return nil, fmt.Errorf("decoding a session claim for binding inference: %w", err)
		}
		out = append(out, claim)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating session claims for binding inference: %w", err)
	}
	return out, nil
}

// readSessionEpochProjectionIDs reads the set of tasks the engine has projected
// as epochs. The projection table is created by the engine, not by this store,
// so a store that never ran the engine has no epochs and therefore no
// inference.
func readSessionEpochProjectionIDs(ctx context.Context, db *sql.DB) (map[string]struct{}, error) {
	exists, err := sessionTableExists(ctx, db, "epoch_state_projection")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT epoch_id FROM epoch_state_projection`)
	if err != nil {
		return nil, fmt.Errorf("reading the epoch projection inventory for binding inference: %w", err)
	}
	defer rows.Close()
	out := make(map[string]struct{}, 8)
	for rows.Next() {
		var epochID string
		if err := rows.Scan(&epochID); err != nil {
			return nil, fmt.Errorf("decoding an epoch projection row for binding inference: %w", err)
		}
		out[epochID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating epoch projection rows for binding inference: %w", err)
	}
	return out, nil
}

// sessionInferenceNow reads the store clock the hidden rows are stamped with.
func sessionInferenceNow(store lifecycleReceiptStore) int64 {
	clock := store.lifecycleStoreClock()
	if clock == nil {
		clock = wallClock{}
	}
	return clock.Now().UTC().UnixNano()
}

// sessionInferredBindingID is deterministic, so a rebuild that re-derives the
// same session/epoch pair produces the same binding identity and a client never
// sees a stable fact renamed. The epoch is part of the identity because a
// different epoch is a different binding, not the same one moved.
func sessionInferredBindingID(harness, session, epochID string) string {
	sum := sha256.Sum256([]byte(harness + "\x00" + session + "\x00" + epochID))
	return "inferred-" + hex.EncodeToString(sum[:])
}

// sessionInferenceFailureLine renders the ONE diagnostic line a skipped
// inference earns. It never fails the rebuild: the activity cache and the
// explicit bindings are still correct, and the next rebuild retries.
func sessionInferenceFailureLine(err error) string {
	return fmt.Sprintf(
		"pasture: session binding inference was skipped for this rebuild: %v. "+
			"Why: either the claim actor's active owner-responsibility episodes could not be read before the rebuild's write transaction, or the derived rows could not be stored. "+
			"Where: internal/tasks/session_infer.go, in planSessionInference or replaceSessionInferredBindings. "+
			"When: during the session cache rebuild, while deriving or writing the inferred bindings. "+
			"Impact: this rebuild cleared the inferred binding cache and wrote no replacement, so a provisional session-to-epoch binding is absent until the next successful rebuild; explicit bindings and session activity are unaffected, and the session cache is still usable. "+
			"Fix: retry the rebuild; if it keeps failing, release other writers on the database file, then run `pasture migrate` and check the store's journal integrity.",
		err)
}

// writeSessionInferenceDiagnostic writes the one line a skipped inference
// earns. A nil sink loses the line rather than the rebuild.
func writeSessionInferenceDiagnostic(store lifecycleReceiptStore, err error) {
	if err == nil {
		return
	}
	sink := store.lifecycleDiagnosticSink()
	if sink == nil {
		return
	}
	fmt.Fprintln(sink, sessionInferenceFailureLine(err))
}

// replaceSessionInferredBindings replaces the inference cache inside the
// rebuild's write transaction. The cache is disposable: it holds today's
// derivation and nothing else, so a full replacement is the only operation that
// cannot leave a stale guess behind.
//
// The table itself is created at store open (see ensurePastureTablesContext),
// so a read-only or rebuild path never has to create schema mid-transaction.
//
// A session is skipped when an explicit binding row exists in any state
// (explicit wins, dated or not) and when its latest occurrence ended the
// session. Both filters are applied from the transaction's own read, so a
// concurrent explicit bind committed before this transaction opened is seen.
func replaceSessionInferredBindings(ctx context.Context, tx *sql.Tx, planned []sessionInferredBinding, explicit, ended map[sessionKey]struct{}) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+sessionInferredBindingTable); err != nil {
		return fmt.Errorf("the store refused to clear the inferred session bindings: %w — repair the database before rebuilding the session cache", err)
	}
	for _, row := range planned {
		key := sessionKey{harness: row.Harness, session: row.Session}
		if _, authoritative := explicit[key]; authoritative {
			continue
		}
		if _, isEnded := ended[key]; isEnded {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO `+sessionInferredBindingTable+`
				(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason, inferred_at, reason, confidence)
			VALUES (?, ?, ?, ?, ?, ?, NULL, NULL, ?, ?, ?)`,
			row.BindingID, row.Harness, row.Session, row.EpochID, row.Role, row.BoundAt, row.InferredAt, row.Reason, row.Confidence); err != nil {
			return fmt.Errorf("the store refused an inferred session binding row for harness %q session %q: %w — repair the database before rebuilding the session cache", row.Harness, row.Session, err)
		}
	}
	return nil
}

// clearSessionInferredBindings removes one session's inferred derivation. It is
// the session-end cleanup: an inferred binding never outlives the session it was
// derived for. The row is deleted rather than closed because a closed interval
// would state an end instant the store only knows as an upper bound, and the
// next rebuild keeps no inferred history anyway.
func clearSessionInferredBindings(ctx context.Context, tx *sql.Tx, harness, session string) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM `+sessionInferredBindingTable+` WHERE harness = ? AND session = ?`,
		harness, session); err != nil {
		return fmt.Errorf("the store refused to clear the inferred binding for harness %q session %q: %w — repair the database before closing the session binding", harness, session, err)
	}
	return nil
}

// sessionRowQuerier is the read shape shared by *sql.DB and *sql.Tx, so the
// table probe works before and inside the rebuild's transaction.
type sessionRowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// sessionTableExists reports whether name is a table in the store.
func sessionTableExists(ctx context.Context, q sessionRowQuerier, name string) (bool, error) {
	var found string
	err := q.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking for table %q in the pasture database: %w — repair the database before retrying", name, err)
	}
	return true, nil
}
