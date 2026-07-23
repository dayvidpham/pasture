package provadapter

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/pkg/protocol/portable"
)

// facade_test.go exercises the thin Apply/LookupCommitted facade end-to-end against
// a REAL Provenance store (OpenMemory), proving the closed Absent/Committed/Conflict
// surface, idempotent replay, and typed-error round-tripping — all through the
// adapter's own ref/digest conversions.

// registerActor registers a software agent on a real store and returns its ActorID
// (AgentID is the ActorID domain).
func registerActor(t *testing.T, tr provenance.Tracker, name string) provenance.ActorID {
	t.Helper()
	sa, err := tr.RegisterSoftwareAgent("pasture-test", name, "0", "test")
	if err != nil {
		t.Fatalf("RegisterSoftwareAgent(%q): %v", name, err)
	}
	return sa.ID
}

// facadeGenesis opens a real store, wraps its journal in the facade, registers a
// committing actor, and commits the genesis bootstrap authority THROUGH the facade.
// It returns the facade, the committing actor, and the established bootstrap
// authority JournalID (from the committed result's "auth" slot).
func facadeGenesis(t *testing.T) (*Journal, provenance.Tracker, provenance.ActorID, provenance.JournalID) {
	t.Helper()
	tr, err := provenance.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })

	facade, err := NewJournal(tr.Journal())
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	actor := registerActor(t, tr, "committer")

	out, err := facade.Apply(ApplyRequest{
		Mutation:   mustMutationRef(t, "pasture.genesis"),
		Actor:      actor,
		Authority:  nil, // genesis
		Command:    mustDigest(t, []byte("genesis-command")),
		RecordedAt: time.Now().UTC().UnixNano(),
		Effects: []provenance.Effect{{
			Sort:           provenance.EffectBootstrapAuthority,
			BootstrapLabel: "pasture-system",
			ResultSlot:     "auth",
		}},
	})
	if err != nil {
		t.Fatalf("facade genesis Apply: %v", err)
	}
	if out.Kind != OutcomeCommitted {
		t.Fatalf("genesis outcome = %s, want OutcomeCommitted", out.Kind)
	}
	var boot provenance.JournalID
	found := false
	for _, b := range out.Committed.ResultSlots {
		if string(b.Slot) == "auth" {
			boot = b.ProducedJournalID
			found = true
		}
	}
	if !found {
		t.Fatalf("genesis produced no bootstrap authority slot: %+v", out.Committed)
	}
	return facade, tr, actor, boot
}

// createTaskRequest builds a valid task-create ApplyRequest under the bootstrap
// authority, minting a fresh task id.
func createTaskRequest(actor provenance.ActorID, boot provenance.JournalID, mutation portable.MutationRef, command ir.CanonicalCommandDigest) ApplyRequest {
	authority := boot
	return ApplyRequest{
		Mutation:   mutation,
		Actor:      actor,
		Authority:  &authority,
		Command:    command,
		RecordedAt: time.Now().UTC().UnixNano(),
		Effects: []provenance.Effect{{
			Sort:        provenance.EffectTaskCreate,
			TaskID:      provenance.TaskID{Namespace: "pasture-test", UUID: uuid.Must(uuid.NewV7())},
			Title:       "migrated task",
			Description: "created through the facade",
			Type:        provenance.TaskTypeTask,
			Priority:    provenance.PriorityMedium,
			Phase:       provenance.PhaseWorkerSlices,
		}},
	}
}

// TestFacade_ApplyCommitsAndLooksUp proves a fresh Apply commits (OutcomeCommitted)
// and a subsequent LookupCommitted on the same mutation reference reconstructs the
// committed result, while an unknown reference is OutcomeAbsent.
func TestFacade_ApplyCommitsAndLooksUp(t *testing.T) {
	facade, _, actor, boot := facadeGenesis(t)

	mut := mustMutationRef(t, "pasture.task.create.001")
	req := createTaskRequest(actor, boot, mut, mustDigest(t, []byte("create-001-command")))

	out, err := facade.Apply(req)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.Kind != OutcomeCommitted {
		t.Fatalf("Apply outcome = %s, want OutcomeCommitted", out.Kind)
	}
	if out.Committed.ShortCircuited {
		t.Fatalf("a fresh Apply must not be a short-circuited replay")
	}
	if out.Committed.AnchorJournalID == 0 {
		t.Fatalf("committed result carries no anchor journal id")
	}

	look, err := facade.LookupCommitted(mut)
	if err != nil {
		t.Fatalf("LookupCommitted: %v", err)
	}
	if look.Kind != OutcomeCommitted {
		t.Fatalf("LookupCommitted outcome = %s, want OutcomeCommitted", look.Kind)
	}
	if look.Committed.AnchorJournalID != out.Committed.AnchorJournalID {
		t.Fatalf("lookup anchor %d != apply anchor %d", look.Committed.AnchorJournalID, out.Committed.AnchorJournalID)
	}

	absent, err := facade.LookupCommitted(mustMutationRef(t, "pasture.never.applied"))
	if err != nil {
		t.Fatalf("LookupCommitted(unknown): %v", err)
	}
	if absent.Kind != OutcomeAbsent {
		t.Fatalf("unknown lookup outcome = %s, want OutcomeAbsent", absent.Kind)
	}
}

// TestFacade_IdempotentReplay proves re-applying the exact same operation hits the
// §9.4 short-circuit: OutcomeCommitted with ShortCircuited=true and the same anchor.
func TestFacade_IdempotentReplay(t *testing.T) {
	facade, _, actor, boot := facadeGenesis(t)

	mut := mustMutationRef(t, "pasture.task.create.replay")
	req := createTaskRequest(actor, boot, mut, mustDigest(t, []byte("replay-command")))

	first, err := facade.Apply(req)
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	// Re-apply the identical request (same mutation ref, actor, authority, digests).
	second, err := facade.Apply(req)
	if err != nil {
		t.Fatalf("replay Apply: %v", err)
	}
	if second.Kind != OutcomeCommitted {
		t.Fatalf("replay outcome = %s, want OutcomeCommitted", second.Kind)
	}
	if !second.Committed.ShortCircuited {
		t.Fatalf("replay of an identical operation must be ShortCircuited")
	}
	if second.Committed.AnchorJournalID != first.Committed.AnchorJournalID {
		t.Fatalf("replay anchor %d != original %d (a duplicate was written)", second.Committed.AnchorJournalID, first.Committed.AnchorJournalID)
	}
}

// TestFacade_ConflictRoundTripsTypedError proves reusing a mutation reference with a
// differing four-field identity yields OutcomeConflict AND round-trips the typed
// provenance error (errors.Is/As).
func TestFacade_ConflictRoundTripsTypedError(t *testing.T) {
	facade, _, actor, boot := facadeGenesis(t)

	mut := mustMutationRef(t, "pasture.task.create.conflict")
	first := createTaskRequest(actor, boot, mut, mustDigest(t, []byte("conflict-command")))
	if _, err := facade.Apply(first); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	// Same mutation reference (same OperationID) but a different canonical effect
	// operand: the canonical mutation identity mismatches.
	conflicting := first
	conflicting.Effects[0].Title = "different title"

	out, err := facade.Apply(conflicting)
	if err == nil {
		t.Fatalf("expected a conflict error on reused mutation reference with a different identity")
	}
	if out.Kind != OutcomeConflict {
		t.Fatalf("conflict outcome = %s, want OutcomeConflict", out.Kind)
	}
	if out.Conflict == nil {
		t.Fatalf("conflict outcome carries no typed OperationConflict")
	}
	if !errors.Is(err, provenance.ErrOperationConflict) {
		t.Fatalf("conflict error does not wrap provenance.ErrOperationConflict: %v", err)
	}
	var typed *provenance.OperationConflict
	if !errors.As(err, &typed) {
		t.Fatalf("conflict error does not round-trip *provenance.OperationConflict via errors.As: %v", err)
	}
	if typed.Field == "" {
		t.Fatalf("typed conflict does not name the differing identity field")
	}
}

// TestFacade_RejectsInvalidInput proves the facade's boundary validation rejects a
// nil journal, an empty mutation reference, a zero actor, and a zero digest with
// actionable errors before any store call.
func TestFacade_RejectsInvalidInput(t *testing.T) {
	if _, err := NewJournal(nil); err == nil {
		t.Fatalf("expected NewJournal(nil) to be rejected")
	}

	facade, _, actor, boot := facadeGenesis(t)
	authority := boot

	base := ApplyRequest{
		Mutation:   mustMutationRef(t, "pasture.valid.ref"),
		Actor:      actor,
		Authority:  &authority,
		Command:    mustDigest(t, []byte("valid-command")),
		RecordedAt: time.Now().UTC().UnixNano(),
	}

	// Empty mutation reference.
	empties := base
	empties.Mutation = portable.MutationRef{}
	if _, err := facade.Apply(empties); err == nil {
		t.Fatalf("expected empty mutation reference to be rejected")
	}
	// Zero (namespaceless) actor.
	zeroActor := base
	zeroActor.Actor = provenance.ActorID{}
	if _, err := facade.Apply(zeroActor); err == nil {
		t.Fatalf("expected zero actor to be rejected")
	}
	// Zero command digest.
	zeroDigest := base
	zeroDigest.Command = ir.CanonicalCommandDigest{}
	if _, err := facade.Apply(zeroDigest); err == nil {
		t.Fatalf("expected zero command digest to be rejected")
	}
	// LookupCommitted with an empty mutation reference is also rejected.
	if _, err := facade.LookupCommitted(portable.MutationRef{}); err == nil {
		t.Fatalf("expected empty mutation reference to be rejected by LookupCommitted")
	}
}
