package provadapter

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dayvidpham/provenance"
)

// migrate_test.go exercises the finite serial baseline-migration coordinator
// against a REAL Provenance store (OpenMemory), covering the success path with a
// genuine anchor, the idempotent re-run (replay) short-circuit, whole-batch
// stop-on-first-failure with typed-error round-tripping, deterministic ordering,
// and the read-only source-integrity guard.
//
// A legacy-baseline marker projects onto an EXISTING task row, so each test first
// journal-creates its tasks through the facade (which inserts the task row) and
// then migrates a marker-only (no-owner) baseline over them. That drives the real
// anchor-creation and §9.4 idempotent short-circuit through the public surface
// without the Provenance-internal legacy-seed helper.

// createMarkerTask journal-creates one task through the facade and returns its id,
// so a marker-only legacy baseline can later be migrated over an existing row.
func createMarkerTask(t *testing.T, facade *Journal, actor provenance.ActorID, boot provenance.JournalID, suffix string) provenance.TaskID {
	t.Helper()
	id := provenance.TaskID{Namespace: "pasture-legacy", UUID: uuid.Must(uuid.NewV7())}
	authority := boot
	out, err := facade.Apply(ApplyRequest{
		Mutation:       mustMutationRef(t, "pasture.task.create."+suffix),
		Actor:          actor,
		Authority:      &authority,
		Command:        mustDigest(t, []byte("create-command-"+suffix)),
		MutationDigest: []byte("create-mutation-" + suffix),
		RecordedAt:     time.Now().UTC().UnixNano(),
		Effects: []provenance.Effect{{
			Sort:     provenance.EffectTaskCreate,
			TaskID:   id,
			Title:    "legacy candidate " + suffix,
			Type:     provenance.TaskTypeTask,
			Priority: provenance.PriorityMedium,
			Phase:    provenance.PhaseWorkerSlices,
		}},
	})
	if err != nil {
		t.Fatalf("journal-create task %s: %v", suffix, err)
	}
	if out.Kind != OutcomeCommitted {
		t.Fatalf("journal-create task %s outcome = %s, want OutcomeCommitted", suffix, out.Kind)
	}
	return id
}

// staticHasher returns a SourceHasher that always yields the same bytes, modelling
// a source that is not mutated across the migration.
func staticHasher(b []byte) SourceHasher {
	return func() ([]byte, error) { return b, nil }
}

// TestRunBaselineMigration_SuccessAndIdempotentReplay migrates three marker-only
// legacy baselines in one whole-batch run, then re-runs the identical batch and
// asserts the §9.4 idempotent short-circuit produced zero new anchors — the
// coordinator surfaces the fresh-vs-replayed counts from MigrationResult.
func TestRunBaselineMigration_SuccessAndIdempotentReplay(t *testing.T) {
	facade, tr, system, boot := facadeGenesis(t)
	j := tr.Journal()

	base := time.Date(2025, 6, 11, 0, 0, 0, 0, time.UTC)
	// Create three tasks and present them OUT of RecordedAt order.
	idA := createMarkerTask(t, facade, system, boot, "a")
	idB := createMarkerTask(t, facade, system, boot, "b")
	idC := createMarkerTask(t, facade, system, boot, "c")
	rows := []provenance.LegacyTaskRow{
		{ID: idC, Status: provenance.TaskStatusOpen, CreatedAt: base.Add(3 * time.Hour), UpdatedAt: base.Add(3 * time.Hour)},
		{ID: idA, Status: provenance.TaskStatusOpen, CreatedAt: base.Add(1 * time.Hour), UpdatedAt: base.Add(1 * time.Hour)},
		{ID: idB, Status: provenance.TaskStatusOpen, CreatedAt: base.Add(2 * time.Hour), UpdatedAt: base.Add(2 * time.Hour)},
	}

	req := BaselineMigrationRequest{System: system, BootstrapAuthority: boot, Rows: rows}

	first, err := RunBaselineMigration(j, req, staticHasher([]byte("source-snapshot-v1")))
	if err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if !first.SourceUnchanged {
		t.Fatalf("expected SourceUnchanged=true on a clean run")
	}
	if first.Result.TasksMigrated != 3 || first.Result.BaselineAnchorsCreated != 3 || first.Result.ShortCircuited != 0 {
		t.Fatalf("first run result = %+v, want {created:3 shortCircuited:0 migrated:3}", first.Result)
	}
	// Deterministic (RecordedAt, LegacyRowID) presentation order: A, B, C by UpdatedAt.
	wantOrder := []provenance.TaskID{idA, idB, idC}
	if len(first.OrderedRowIDs) != len(wantOrder) {
		t.Fatalf("OrderedRowIDs len = %d, want %d", len(first.OrderedRowIDs), len(wantOrder))
	}
	for i := range wantOrder {
		if first.OrderedRowIDs[i].String() != wantOrder[i].String() {
			t.Fatalf("OrderedRowIDs[%d] = %s, want %s", i, first.OrderedRowIDs[i].String(), wantOrder[i].String())
		}
	}
	// Each baseline anchor is committed and looks up exactly (§13 item 1).
	for _, id := range wantOrder {
		res, err := j.LookupCommitted(provenance.MigrationBaselineOperationID(id))
		if err != nil {
			t.Fatalf("LookupCommitted baseline for %s: %v", id, err)
		}
		if res.Kind != provenance.CommittedExact {
			t.Fatalf("baseline for %s = %s, want CommittedExact", id, res.Kind)
		}
	}

	// Idempotent re-run (replay): the deterministic per-task OperationID hits the
	// §9.4 short-circuit, so zero new anchors are created.
	second, err := RunBaselineMigration(j, req, staticHasher([]byte("source-snapshot-v1")))
	if err != nil {
		t.Fatalf("second migration: %v", err)
	}
	if second.Result.BaselineAnchorsCreated != 0 || second.Result.ShortCircuited != 3 {
		t.Fatalf("second run result = %+v, want {created:0 shortCircuited:3}", second.Result)
	}
	if len(second.OrderedRowIDs) != len(first.OrderedRowIDs) {
		t.Fatalf("replay OrderedRowIDs len = %d, want %d", len(second.OrderedRowIDs), len(first.OrderedRowIDs))
	}
	for i := range second.OrderedRowIDs {
		if second.OrderedRowIDs[i].String() != first.OrderedRowIDs[i].String() {
			t.Fatalf("replay OrderedRowIDs[%d] = %s, want %s (order must be deterministic across runs)",
				i, second.OrderedRowIDs[i].String(), first.OrderedRowIDs[i].String())
		}
	}
}

// TestRunBaselineMigration_UnmappableOwnerStopsClosed proves a whole-batch
// stop-on-first-failure: one unmappable legacy owner aborts the ENTIRE batch with
// nothing committed, the typed provenance error round-trips (errors.Is/As), and the
// coordinator still confirms the source was not mutated.
func TestRunBaselineMigration_UnmappableOwnerStopsClosed(t *testing.T) {
	facade, tr, system, boot := facadeGenesis(t)
	j := tr.Journal()

	base := time.Date(2025, 6, 11, 0, 0, 0, 0, time.UTC)
	idA := createMarkerTask(t, facade, system, boot, "mappable")
	idB := createMarkerTask(t, facade, system, boot, "orphan")
	rows := []provenance.LegacyTaskRow{
		{ID: idA, Status: provenance.TaskStatusOpen, CreatedAt: base, UpdatedAt: base},
		// idB carries a non-empty legacy owner absent from Owners: unmappable.
		{ID: idB, RawOwner: "orphaned-free-text", Status: provenance.TaskStatusOpen, CreatedAt: base.Add(time.Hour), UpdatedAt: base.Add(time.Hour)},
	}

	req := BaselineMigrationRequest{System: system, BootstrapAuthority: boot, Rows: rows}
	out, err := RunBaselineMigration(j, req, staticHasher([]byte("src")))
	if err == nil {
		t.Fatalf("expected the whole batch to fail closed on an unmappable owner")
	}
	if !errors.Is(err, provenance.ErrMigrationOwnerUnmappable) {
		t.Fatalf("error does not wrap provenance.ErrMigrationOwnerUnmappable: %v", err)
	}
	var typed *provenance.MigrationOwnerUnmappableError
	if !errors.As(err, &typed) {
		t.Fatalf("error does not round-trip *provenance.MigrationOwnerUnmappableError: %v", err)
	}
	if typed.RawOwner != "orphaned-free-text" {
		t.Fatalf("typed error raw owner = %q, want %q", typed.RawOwner, "orphaned-free-text")
	}
	// Fail-closed: neither baseline was committed (§13 item 4).
	for _, id := range []provenance.TaskID{idA, idB} {
		res, lerr := j.LookupCommitted(provenance.MigrationBaselineOperationID(id))
		if lerr != nil {
			t.Fatalf("LookupCommitted after failed batch for %s: %v", id, lerr)
		}
		if res.Kind != provenance.CommittedAbsent {
			t.Fatalf("baseline for %s = %s after a failed batch, want CommittedAbsent (nothing committed)", id, res.Kind)
		}
	}
	// The coordinator still verified read-only source integrity on the failure path.
	if !out.SourceUnchanged {
		t.Fatalf("expected SourceUnchanged=true even on the fail-closed path")
	}
}

// TestRunBaselineMigration_SourceMutationDetected proves the read-only integrity
// guard: a source whose byte hash changes across the migration fails closed with
// ErrSourceMutatedDuringMigration.
func TestRunBaselineMigration_SourceMutationDetected(t *testing.T) {
	_, tr, system, boot := facadeGenesis(t)
	j := tr.Journal()

	// A hasher that returns different bytes on each call models a source mutated
	// underneath the migration.
	var calls int
	mutating := func() ([]byte, error) {
		calls++
		return []byte(fmt.Sprintf("source-rev-%d", calls)), nil
	}

	req := BaselineMigrationRequest{System: system, BootstrapAuthority: boot, Rows: nil}
	_, err := RunBaselineMigration(j, req, mutating)
	if err == nil {
		t.Fatalf("expected a source-mutation integrity failure")
	}
	if !errors.Is(err, ErrSourceMutatedDuringMigration) {
		t.Fatalf("error does not wrap ErrSourceMutatedDuringMigration: %v", err)
	}
}

// TestRunBaselineMigration_RejectsInvalidInput proves the coordinator's boundary
// validation rejects a nil journal, a nil hasher, and an invalid system actor.
func TestRunBaselineMigration_RejectsInvalidInput(t *testing.T) {
	_, tr, system, boot := facadeGenesis(t)
	j := tr.Journal()
	req := BaselineMigrationRequest{System: system, BootstrapAuthority: boot}

	if _, err := RunBaselineMigration(nil, req, staticHasher(nil)); err == nil {
		t.Fatalf("expected nil journal to be rejected")
	}
	if _, err := RunBaselineMigration(j, req, nil); err == nil {
		t.Fatalf("expected nil hasher to be rejected")
	}
	badActor := BaselineMigrationRequest{System: provenance.ActorID{}, BootstrapAuthority: boot}
	if _, err := RunBaselineMigration(j, badActor, staticHasher(nil)); err == nil {
		t.Fatalf("expected an invalid system actor to be rejected")
	}
}

// TestRunBaselineMigration_EmptyBatch proves an empty batch is a clean no-op that
// still verifies source integrity and surfaces a zero result.
func TestRunBaselineMigration_EmptyBatch(t *testing.T) {
	_, tr, system, boot := facadeGenesis(t)
	j := tr.Journal()

	out, err := RunBaselineMigration(j, BaselineMigrationRequest{System: system, BootstrapAuthority: boot}, staticHasher([]byte("empty")))
	if err != nil {
		t.Fatalf("empty batch migration: %v", err)
	}
	if !out.SourceUnchanged {
		t.Fatalf("expected SourceUnchanged=true on an empty batch")
	}
	if out.Result.TasksMigrated != 0 || out.Result.BaselineAnchorsCreated != 0 {
		t.Fatalf("empty batch result = %+v, want zero", out.Result)
	}
	if len(out.OrderedRowIDs) != 0 {
		t.Fatalf("empty batch OrderedRowIDs = %v, want empty", out.OrderedRowIDs)
	}
}
