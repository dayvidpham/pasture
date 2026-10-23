package tasks

// session_infer_test.go proves the one rule inferred bindings are allowed to
// follow: a claimed session gets an inferred binding only when its claim actor
// owns EXACTLY ONE active owner-responsibility episode on a projected epoch
// task. Every subject drives the production RebuildSessionState through the
// production store opener; the store is read back with plain SQL, so an
// assertion about a row is an assertion about the database and not about the
// reader that wrote it.

import (
	"bytes"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// claimAs writes a real session claim through the production claim writer with
// the caller's raw actor string. It exists because the writer deliberately
// accepts values the actor parser later refuses, and inference must survive
// them.
func claimAs(t *testing.T, store *trackerImpl, actor, session string) {
	t.Helper()
	bindings := []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: session}}
	_, err := RecordLifecycleSessionClaim(
		t.Context(), store, gateHarness, registration.EventSessionStart, bindings,
		ActorClaim(actor), gateClock{},
	)
	require.NoError(t, err)
}

// inferredSnapshot is one row of the inference cache, read back with plain SQL.
type inferredSnapshot struct {
	BindingID     string
	Harness       string
	Session       string
	EpochID       string
	Role          string
	BoundAt       int64
	InferredAt    int64
	UnboundAt     sql.NullInt64
	UnboundReason sql.NullString
	Reason        string
	Confidence    string
}

// newInferenceStore opens the production store with a fixed store clock and a
// captured diagnostic sink, so `inferred_at` is an exact instant and a skipped
// inference is an observable line.
func newInferenceStore(t *testing.T) (*trackerImpl, *bytes.Buffer) {
	t.Helper()
	diagnostics := &bytes.Buffer{}
	store, _ := openGateStore(t, WithStoreClock(gateClock{}), WithDiagnosticSink(diagnostics))
	return store, diagnostics
}

// seedEpochProjection makes task an epoch in the engine's projection, which is
// the store's only definition of "epoch task" for inference.
func seedEpochProjection(t *testing.T, store *trackerImpl, task provenance.TaskID) {
	t.Helper()
	_, err := store.auditDB.Exec(`
		CREATE TABLE IF NOT EXISTS epoch_state_projection (
			epoch_id      TEXT PRIMARY KEY,
			current_phase TEXT NOT NULL,
			state_json    TEXT NOT NULL,
			updated_at    INTEGER NOT NULL
		)`)
	require.NoError(t, err)
	_, err = store.auditDB.Exec(`
		INSERT INTO epoch_state_projection (epoch_id, current_phase, state_json, updated_at)
		VALUES (?, 'p3-plan', '{}', 1)`, task.String())
	require.NoError(t, err)
}

// seedOwnedEpochTask creates one task, projects it as an epoch, and starts one
// owner-responsibility episode held by actor: the one shape inference accepts.
func seedOwnedEpochTask(t *testing.T, store *trackerImpl, actor provenance.ActorID, handle string) provenance.TaskID {
	t.Helper()
	task := createHumanTestTask(t, store, handle)
	seedEpochProjection(t, store, task)
	seedAssignmentEpisode(t, store, task,
		provenance.AssignmentID(handle+"-assignment"),
		RoleOwnerResponsibility, actor,
		provenance.OperationID(handle+"-operation"))
	return task
}

// seedOwnedPlainTask starts an owner-responsibility episode on a task that is
// not projected as an epoch.
func seedOwnedPlainTask(t *testing.T, store *trackerImpl, actor provenance.ActorID, handle string) provenance.TaskID {
	t.Helper()
	task := createHumanTestTask(t, store, handle)
	seedAssignmentEpisode(t, store, task,
		provenance.AssignmentID(handle+"-assignment"),
		RoleOwnerResponsibility, actor,
		provenance.OperationID(handle+"-operation"))
	return task
}

func rebuildForInference(t *testing.T, store *trackerImpl) {
	t.Helper()
	_, err := RebuildSessionState(t.Context(), store)
	require.NoError(t, err, "inference is additive; no inference outcome may fail the rebuild")
}

// readInferredBindings reads every inference-cache row, ordered for a stable
// comparison.
func readInferredBindings(t *testing.T, store *trackerImpl) []inferredSnapshot {
	t.Helper()
	rows, err := store.auditDB.Query(`
		SELECT binding_id, harness, session, epoch_id, role, bound_at,
		       inferred_at, unbound_at, unbound_reason, reason, confidence
		FROM pasture_session_inferred_binding
		ORDER BY harness, session`)
	require.NoError(t, err)
	defer rows.Close()
	out := make([]inferredSnapshot, 0, 4)
	for rows.Next() {
		var row inferredSnapshot
		require.NoError(t, rows.Scan(
			&row.BindingID, &row.Harness, &row.Session, &row.EpochID, &row.Role,
			&row.BoundAt, &row.InferredAt, &row.UnboundAt, &row.UnboundReason, &row.Reason,
			&row.Confidence))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

func countExplicitBindings(t *testing.T, store *trackerImpl) int {
	t.Helper()
	var count int
	require.NoError(t, store.auditDB.QueryRow(`SELECT count(*) FROM pasture_session_epoch_binding`).Scan(&count))
	return count
}

// TestSessionInferenceDerivesUniqueOwnedEpoch is the accepted path: one claim,
// one actor, one owned projected epoch. The row appears in the inference cache,
// never in the explicit table, and it is open.
func TestSessionInferenceDerivesUniqueOwnedEpoch(t *testing.T) {
	t.Parallel()
	store, diagnostics := newInferenceStore(t)
	actor := feasibilityActor(t, store, "infer-unique")
	epoch := seedOwnedEpochTask(t, store, actor, "infer-unique")
	gateClaim(t, store, actor, "session-unique")

	rebuildForInference(t, store)

	rows := readInferredBindings(t, store)
	require.Len(t, rows, 1)
	row := rows[0]
	require.Equal(t, string(gateHarness), row.Harness)
	require.Equal(t, "session-unique", row.Session)
	require.Equal(t, epoch.String(), row.EpochID)
	require.Equal(t, sessionInferenceRole, row.Role)
	require.Equal(t, sessionInferenceReason, row.Reason)
	require.Equal(t, sessionInferenceConfidence, row.Confidence,
		"the claim-ownership rule is observed, not verified by a recorded command")
	require.Equal(t, gateClock{}.Now().UnixNano(), row.BoundAt,
		"the interval starts at the claim instant, the earliest store fact the derivation rests on")
	require.Equal(t, gateClock{}.Now().UnixNano(), row.InferredAt)
	require.NotEmpty(t, row.BindingID)
	require.False(t, row.UnboundAt.Valid)
	require.Zero(t, countExplicitBindings(t, store),
		"an inferred row must never be written into the explicit binding table")
	require.Empty(t, diagnostics.String(), "a successful inference writes no diagnostic")
}

// TestSessionInferenceSkipsZeroOwners: a claimed actor who owns nothing gets no
// inferred binding. There is nothing to guess from.
func TestSessionInferenceSkipsZeroOwners(t *testing.T) {
	t.Parallel()
	store, _ := newInferenceStore(t)
	actor := feasibilityActor(t, store, "infer-zero")
	gateClaim(t, store, actor, "session-zero")

	rebuildForInference(t, store)

	require.Empty(t, readInferredBindings(t, store))
}

// TestSessionInferenceSkipsMultipleOwners: two active owner-responsibility
// episodes are ambiguous, and ambiguity leaves the session unbound.
func TestSessionInferenceSkipsMultipleOwners(t *testing.T) {
	t.Parallel()
	store, _ := newInferenceStore(t)
	actor := feasibilityActor(t, store, "infer-multiple")
	seedOwnedEpochTask(t, store, actor, "infer-multiple-a")
	seedOwnedEpochTask(t, store, actor, "infer-multiple-b")
	gateClaim(t, store, actor, "session-multiple")

	rebuildForInference(t, store)

	require.Empty(t, readInferredBindings(t, store),
		"two owned epochs are ambiguous; an inferred binding would be a guess")
}

// TestSessionInferenceCountsOnlyProjectedEpochs: ownership of a task with no
// epoch_state_projection row is not an epoch candidate, so one projected epoch
// plus any number of plain owned tasks stays unambiguous, and plain ownership
// alone yields nothing.
func TestSessionInferenceCountsOnlyProjectedEpochs(t *testing.T) {
	t.Parallel()

	t.Run("one projected epoch beside a plain owned task", func(t *testing.T) {
		t.Parallel()
		store, _ := newInferenceStore(t)
		actor := feasibilityActor(t, store, "infer-projected-mixed")
		epoch := seedOwnedEpochTask(t, store, actor, "infer-projected-mixed")
		seedOwnedPlainTask(t, store, actor, "infer-projected-mixed-plain")
		gateClaim(t, store, actor, "session-projected-mixed")

		rebuildForInference(t, store)

		rows := readInferredBindings(t, store)
		require.Len(t, rows, 1)
		require.Equal(t, epoch.String(), rows[0].EpochID)
	})

	t.Run("only plain owned tasks", func(t *testing.T) {
		t.Parallel()
		store, _ := newInferenceStore(t)
		// A projected epoch owned by a DIFFERENT actor keeps the epoch inventory
		// non-empty and gives the derivation a control row: without it, "no
		// inference" would also be explained by the empty-epoch early return and
		// the ownership read below would never run.
		owner := feasibilityActor(t, store, "infer-projected-plain-owner")
		seedOwnedEpochTask(t, store, owner, "infer-projected-plain-owner")
		gateClaim(t, store, owner, "session-projected-plain-owner")
		actor := feasibilityActor(t, store, "infer-projected-plain")
		seedOwnedPlainTask(t, store, actor, "infer-projected-plain")
		gateClaim(t, store, actor, "session-projected-plain")

		rebuildForInference(t, store)

		rows := readInferredBindings(t, store)
		require.Len(t, rows, 1, "only the control owner's projected epoch is an epoch candidate")
		require.Equal(t, "session-projected-plain-owner", rows[0].Session)
		require.NotEqual(t, "session-projected-plain", rows[0].Session,
			"a task with no epoch projection row is not an epoch, even though the ownership read ran")
	})
}

// TestSessionInferenceNeverOverridesExplicitBinding proves authoritative
// history in all three shapes: an open explicit binding, a closed one written
// by an unbind, and an explicit bind that lands AFTER inference already ran.
func TestSessionInferenceNeverOverridesExplicitBinding(t *testing.T) {
	t.Parallel()

	t.Run("open explicit binding", func(t *testing.T) {
		t.Parallel()
		store, _ := newInferenceStore(t)
		actor := feasibilityActor(t, store, "infer-explicit-open")
		epoch := seedOwnedEpochTask(t, store, actor, "infer-explicit-open")
		gateClaim(t, store, actor, "session-explicit-open")
		_, err := store.auditDB.Exec(`
			INSERT INTO pasture_session_epoch_binding
				(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason)
			VALUES ('explicit-open', 'claude-code', 'session-explicit-open', ?, 'architect', ?, NULL, NULL)`,
			epoch.String(), gateClock{}.Now().UnixNano())
		require.NoError(t, err)

		rebuildForInference(t, store)

		require.Empty(t, readInferredBindings(t, store), "an explicit open binding always wins")
		require.Equal(t, 1, countExplicitBindings(t, store))
	})

	t.Run("explicit unbind stays authoritative", func(t *testing.T) {
		t.Parallel()
		store, _ := newInferenceStore(t)
		actor := feasibilityActor(t, store, "infer-explicit-unbind")
		epoch := seedOwnedEpochTask(t, store, actor, "infer-explicit-unbind")
		gateClaim(t, store, actor, "session-explicit-unbind")
		_, err := store.auditDB.Exec(`
			INSERT INTO pasture_session_epoch_binding
				(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason)
			VALUES ('explicit-unbound', 'claude-code', 'session-explicit-unbind', ?, 'architect', ?, ?, 'unbind')`,
			epoch.String(), gateClock{}.Now().UnixNano(), gateClock{}.Now().UnixNano())
		require.NoError(t, err)

		rebuildForInference(t, store)

		require.Empty(t, readInferredBindings(t, store),
			"an explicit unbind must not be resurrected by a later rebuild")
		var reason string
		require.NoError(t, store.auditDB.QueryRow(`
			SELECT unbound_reason FROM pasture_session_epoch_binding WHERE binding_id = 'explicit-unbound'`).Scan(&reason))
		require.Equal(t, "unbind", reason, "the explicit row is untouched")
	})

	t.Run("explicit bind after inference clears the derivation", func(t *testing.T) {
		t.Parallel()
		store, _ := newInferenceStore(t)
		actor := feasibilityActor(t, store, "infer-explicit-late")
		epoch := seedOwnedEpochTask(t, store, actor, "infer-explicit-late")
		gateClaim(t, store, actor, "session-explicit-late")
		rebuildForInference(t, store)
		require.Len(t, readInferredBindings(t, store), 1)

		_, err := store.auditDB.Exec(`
			INSERT INTO pasture_session_epoch_binding
				(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason)
			VALUES ('explicit-late', 'claude-code', 'session-explicit-late', ?, 'architect', ?, NULL, NULL)`,
			epoch.String(), gateClock{}.Now().UnixNano())
		require.NoError(t, err)

		require.Empty(t, readInferredBindings(t, store),
			"the explicit write itself must clear the now-invalid derivation, without waiting for a rebuild")
		require.Equal(t, 1, countExplicitBindings(t, store))
	})
}

// TestSessionInferenceSkipsUnknownAndMalformedActors: a claim that names an
// actor the store never registered, and a claim whose actor is not a canonical
// actor identifier, both yield no inference. Neither is a rebuild fault.
//
// A projected epoch owned by a DIFFERENT actor is seeded first, and a control
// session claimed by that owner is rebuilt in the same pass. The control row is
// what proves the derivation actually ran: without it, an empty result would
// also be explained by the epoch inventory being empty (the derivation's early
// return) and the actor branches below would never execute.
func TestSessionInferenceSkipsUnknownAndMalformedActors(t *testing.T) {
	t.Parallel()
	store, diagnostics := newInferenceStore(t)
	owner := feasibilityActor(t, store, "infer-actor-owner")
	seedOwnedEpochTask(t, store, owner, "infer-actor-owner")
	gateClaim(t, store, owner, "session-control-owner")
	unknown, err := provenance.ParseActorID("human-service/0193f1c0-0000-7000-8000-0000000000aa")
	require.NoError(t, err)
	claimAs(t, store, unknown.String(), "session-unknown-actor")
	claimAs(t, store, "not-an-actor", "session-malformed-actor")

	rebuildForInference(t, store)

	rows := readInferredBindings(t, store)
	require.Len(t, rows, 1, "only the control session's unambiguous ownership is inferred")
	require.Equal(t, "session-control-owner", rows[0].Session)
	require.NotEqual(t, "session-unknown-actor", rows[0].Session)
	require.NotEqual(t, "session-malformed-actor", rows[0].Session)
	require.Empty(t, diagnostics.String())
}

// TestSessionInferenceSurvivesAbsentOwnershipAPI: a store whose journal does
// not expose the ownership read still rebuilds the session cache, publishes no
// new derivation, and says so at the PLAN stage on the diagnostic sink.
func TestSessionInferenceSurvivesAbsentOwnershipAPI(t *testing.T) {
	t.Parallel()
	store, diagnostics := newInferenceStore(t)
	actor := feasibilityActor(t, store, "infer-no-api")
	seedOwnedEpochTask(t, store, actor, "infer-no-api")
	gateClaim(t, store, actor, "session-no-api")
	gateJournal(t, store, gateHiddenJournal{Journal: store.Journal()})

	rebuildForInference(t, store)

	require.Empty(t, readInferredBindings(t, store))
	line := diagnostics.String()
	require.Contains(t, line, "session binding inference was skipped")
	require.Contains(t, line, "stage plan")
	require.Contains(t, line, "provenance.ActorOwnershipQueryAPI")
	require.Contains(t, line, "published no new inferred derivation and left the previous inferred cache in place")
	require.Contains(t, line, "the session cache is still usable")
	require.Equal(t, 1, bytes.Count([]byte(line), []byte("\n")), "exactly one diagnostic line")
}

// TestSessionInferencePlanFailureKeepsThePreviousCache: a derivation fault must
// not destroy the last good derivation. The diagnostic for the plan stage says
// so, and the previous rows are byte-for-byte unchanged.
func TestSessionInferencePlanFailureKeepsThePreviousCache(t *testing.T) {
	t.Parallel()
	store, diagnostics := newInferenceStore(t)
	actor := feasibilityActor(t, store, "infer-plan-keeps")
	seedOwnedEpochTask(t, store, actor, "infer-plan-keeps")
	gateClaim(t, store, actor, "session-plan-keeps")
	rebuildForInference(t, store)
	before := readInferredBindings(t, store)
	require.Len(t, before, 1)

	gateJournal(t, store, gateHiddenJournal{Journal: store.Journal()})
	rebuildForInference(t, store)

	require.Equal(t, before, readInferredBindings(t, store),
		"a plan failure leaves the previous inferred cache in place")
	line := diagnostics.String()
	require.Contains(t, line, "stage plan")
	require.Contains(t, line, "left the previous inferred cache in place")
	require.Equal(t, 1, bytes.Count([]byte(line), []byte("\n")), "exactly one diagnostic line")
}

// TestSessionInferenceOwnershipReadFaultIsDiagnosed: the ownership read is the
// one provenance call inference makes. A fault there must be diagnosed by name
// and must not write a row.
func TestSessionInferenceOwnershipReadFaultIsDiagnosed(t *testing.T) {
	t.Parallel()
	store, diagnostics := newInferenceStore(t)
	actor := feasibilityActor(t, store, "infer-read-fault")
	seedOwnedEpochTask(t, store, actor, "infer-read-fault")
	gateClaim(t, store, actor, "session-read-fault")
	gateJournal(t, store, gateFaultJournal{Journal: store.Journal(), err: errors.New("ownership read exploded")})

	rebuildForInference(t, store)

	require.Empty(t, readInferredBindings(t, store))
	line := diagnostics.String()
	require.Contains(t, line, "reading the active ownership of the session's claim actor")
	require.Contains(t, line, "ownership read exploded")
	require.Contains(t, line, "stage plan")
	require.Equal(t, 1, bytes.Count([]byte(line), []byte("\n")), "exactly one diagnostic line")
}

// TestSessionInferenceWriteFaultLeavesTheCacheUsable: the inferred rows are
// written under their own savepoint, so a store whose inference schema cannot be
// written still commits the activity cache the read model depends on, and says
// what happened on the diagnostic sink.
func TestSessionInferenceWriteFaultLeavesTheCacheUsable(t *testing.T) {
	t.Parallel()
	store, diagnostics := newInferenceStore(t)
	actor := feasibilityActor(t, store, "infer-write-fault")
	seedOwnedEpochTask(t, store, actor, "infer-write-fault")
	gateClaim(t, store, actor, "session-write-fault")
	// Derive once so the cache holds a real row the failed rebuild must not
	// resurrect from its rollback.
	rebuildForInference(t, store)
	require.Len(t, readInferredBindings(t, store), 1)
	// The table is created at open. Replace it with a shape that keeps every
	// column the clear needs but loses confidence, so DELETE succeeds and the
	// INSERT fails: exactly the fault the savepoint exists for.
	_, err := store.auditDB.Exec(`DROP TABLE pasture_session_inferred_binding`)
	require.NoError(t, err)
	_, err = store.auditDB.Exec(`
		CREATE TABLE pasture_session_inferred_binding (
			binding_id TEXT PRIMARY KEY, harness TEXT NOT NULL, session TEXT NOT NULL,
			epoch_id TEXT NOT NULL, role TEXT NOT NULL, bound_at INTEGER NOT NULL,
			unbound_at INTEGER, unbound_reason TEXT, inferred_at INTEGER NOT NULL, reason TEXT NOT NULL)`)
	require.NoError(t, err)

	_, err = RebuildSessionState(t.Context(), store)
	require.NoError(t, err, "an inference write fault must not fail the rebuild")

	var sessions int
	require.NoError(t, store.auditDB.QueryRow(`
		SELECT count(*) FROM pasture_session_state WHERE harness = 'claude-code' AND session = 'session-write-fault'`).Scan(&sessions))
	require.Equal(t, 1, sessions, "the activity cache still commits")
	var inferred int
	require.NoError(t, store.auditDB.QueryRow(`
		SELECT count(*) FROM pasture_session_inferred_binding`).Scan(&inferred))
	require.Zero(t, inferred, "a failed inference write commits no derivation rather than a stale one")
	line := diagnostics.String()
	require.Contains(t, line, "session binding inference was skipped")
	require.Contains(t, line, "stage write")
	require.Contains(t, line, "cleared the inferred binding cache")
	require.Contains(t, line, "the session cache is still usable")
	require.Equal(t, 1, bytes.Count([]byte(line), []byte("\n")), "exactly one diagnostic line")
}

// TestSessionInferenceClearsWhenOwnershipBecomesAmbiguous: the cache holds
// today's derivation only, so a rebuild that can no longer prove the binding
// removes it rather than leaving a stale guess.
func TestSessionInferenceClearsWhenOwnershipBecomesAmbiguous(t *testing.T) {
	t.Parallel()
	store, _ := newInferenceStore(t)
	actor := feasibilityActor(t, store, "infer-clears")
	seedOwnedEpochTask(t, store, actor, "infer-clears-a")
	gateClaim(t, store, actor, "session-clears")
	rebuildForInference(t, store)
	require.Len(t, readInferredBindings(t, store), 1)

	seedOwnedEpochTask(t, store, actor, "infer-clears-b")
	rebuildForInference(t, store)

	require.Empty(t, readInferredBindings(t, store),
		"a second owned epoch makes the session ambiguous, so the previous derivation is cleared")
}

// TestSessionInferenceSkipsUnclaimedSessions: an inferred binding requires a
// claim. A cache row with no claim has no actor to derive from.
func TestSessionInferenceSkipsUnclaimedSessions(t *testing.T) {
	t.Parallel()
	store, _ := newInferenceStore(t)
	actor := feasibilityActor(t, store, "infer-unclaimed")
	seedOwnedEpochTask(t, store, actor, "infer-unclaimed")
	_, err := store.auditDB.Exec(`
		INSERT INTO pasture_session_state (harness, session, first_seen, last_seen, last_event, last_journal_id, updated_at)
		VALUES ('claude-code', 'session-unclaimed', 1, 1, '', 0, 1)`)
	require.NoError(t, err)

	rebuildForInference(t, store)

	require.Empty(t, readInferredBindings(t, store))
}

// TestSessionInferenceTreatsDuplicateOwnedEpisodesAsOneCandidate: the ownership
// read can name the same epoch task from more than one row (a transfer or a
// replayed episode). One task is one candidate, not ambiguity, so the
// derivation still answers.
func TestSessionInferenceTreatsDuplicateOwnedEpisodesAsOneCandidate(t *testing.T) {
	t.Parallel()
	epoch, err := provenance.ParseTaskID("file://epoch/00000000-0000-7000-8000-000000000001")
	require.NoError(t, err)
	other, err := provenance.ParseTaskID("file://epoch/00000000-0000-7000-8000-000000000002")
	require.NoError(t, err)
	epochs := map[string]struct{}{epoch.String(): {}, other.String(): {}}

	duplicates := []provenance.OwnedTaskRow{
		{TaskID: epoch, AssignmentID: "assignment-a"},
		{TaskID: epoch, AssignmentID: "assignment-b"},
	}
	candidate, unambiguous := unambiguousOwnedEpoch(duplicates, epochs)
	require.True(t, unambiguous, "two episode rows on one epoch task are one candidate")
	require.Equal(t, epoch.String(), candidate)

	ambiguous := []provenance.OwnedTaskRow{{TaskID: epoch}, {TaskID: other}}
	_, unambiguous = unambiguousOwnedEpoch(ambiguous, epochs)
	require.False(t, unambiguous, "two distinct epoch tasks are still ambiguous")
}

// TestSessionInferenceSkipsZeroClaimInstant: a claim with no instant cannot date
// an interval, so the derivation never backdates one from a zero value. A
// control session claimed with a real instant proves the rebuild ran.
func TestSessionInferenceSkipsZeroClaimInstant(t *testing.T) {
	t.Parallel()
	store, diagnostics := newInferenceStore(t)
	actor := feasibilityActor(t, store, "infer-zero-claim")
	seedOwnedEpochTask(t, store, actor, "infer-zero-claim")
	gateClaim(t, store, actor, "session-live-claim")
	_, err := store.auditDB.Exec(`
		INSERT INTO pasture_session_claim (harness, session, actor, claimed_at)
		VALUES ('claude-code', 'session-zero-claim', ?, 0)`, actor.String())
	require.NoError(t, err)

	rebuildForInference(t, store)

	rows := readInferredBindings(t, store)
	require.Len(t, rows, 1)
	require.Equal(t, "session-live-claim", rows[0].Session)
	require.NotEqual(t, "session-zero-claim", rows[0].Session)
	require.Empty(t, diagnostics.String())
}

// TestSessionInferenceRoleMatchesGateVocabulary pins the stored role token to
// the assignment slot the derivation reads, so the two cannot drift apart.
func TestSessionInferenceRoleMatchesGateVocabulary(t *testing.T) {
	t.Parallel()
	require.Equal(t, gateauthority.RoleOwnerResponsibility.String(), sessionInferenceRole)
}

// TestSessionInferenceStoreClockDatesTheDerivation proves inferred_at comes
// from the store's own clock rather than a wall-clock read inside a helper.
func TestSessionInferenceStoreClockDatesTheDerivation(t *testing.T) {
	t.Parallel()
	store, _ := newInferenceStore(t)
	actor := feasibilityActor(t, store, "infer-clock")
	seedOwnedEpochTask(t, store, actor, "infer-clock")
	gateClaim(t, store, actor, "session-clock")

	rebuildForInference(t, store)

	rows := readInferredBindings(t, store)
	require.Len(t, rows, 1)
	require.Equal(t, time.Unix(1700000000, 0).UTC().UnixNano(), rows[0].InferredAt)
}

// TestSessionInferenceBindingIdentityIsStable: re-deriving the same session and
// epoch produces the same binding identity, so a client never sees a stable
// fact renamed by a rebuild.
func TestSessionInferenceBindingIdentityIsStable(t *testing.T) {
	t.Parallel()
	store, _ := newInferenceStore(t)
	actor := feasibilityActor(t, store, "infer-stable")
	seedOwnedEpochTask(t, store, actor, "infer-stable")
	gateClaim(t, store, actor, "session-stable")

	rebuildForInference(t, store)
	first := readInferredBindings(t, store)
	require.Len(t, first, 1)
	rebuildForInference(t, store)
	second := readInferredBindings(t, store)
	require.Len(t, second, 1)
	require.Equal(t, first[0].BindingID, second[0].BindingID)
	require.Equal(t, first[0].BoundAt, second[0].BoundAt)
}

// TestSessionInferenceRunsOnlyOnTheRebuildPath is the placement contract:
// planSessionInference is called from the rebuild and nowhere else, so no hook
// ingress source can derive an inferred binding.
func TestSessionInferenceRunsOnlyOnTheRebuildPath(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	callers := make([]string, 0, 2)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		require.NoError(t, err)
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if ok && ident.Name == "planSessionInference" {
				callers = append(callers, file)
			}
			return true
		})
	}
	require.Equal(t, []string{"session_state_rebuild.go"}, callers,
		"session inference is a rebuild concern; a call from a hook ingress source would put an ownership read on the host's path")
}
