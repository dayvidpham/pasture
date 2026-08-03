package tasks_test

// legacy_import_test.go drives the atomic ImportLegacyAuditRow command against a REAL
// journaled task store (no mocks): create real tasks, import a raw legacy audit row
// that references them, and assert the one-atomic-operation, sorted-unique fan-out,
// actor-text fallback, idempotent replay, and compound-conflict semantics the issue's
// import-command section specifies.

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/provadapter"
	"github.com/dayvidpham/pasture/internal/tasks"
)

func openImporter(t *testing.T) (tasks.LegacyAuditImporter, provenanceTracker) {
	t.Helper()
	tr, err := tasks.OpenTaskTracker(filepath.Join(t.TempDir(), "pasture.db"))
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	imp, ok := tr.(tasks.LegacyAuditImporter)
	if !ok {
		t.Fatalf("OpenTaskTracker result does not satisfy tasks.LegacyAuditImporter")
	}
	full, ok := tr.(provenanceTracker)
	if !ok {
		t.Fatalf("OpenTaskTracker result does not satisfy provenanceTracker")
	}
	return imp, full
}

func createImportTask(t *testing.T, tr provenanceTracker, title string) provenance.TaskID {
	t.Helper()
	task, err := tr.Create("aura-plugins", title, "legacy-import test",
		provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseRequest)
	if err != nil {
		t.Fatalf("Create(%q): %v", title, err)
	}
	return task.ID
}

func fallbackResolver() tasks.LegacyActorResolver {
	return tasks.LegacyActorResolver{
		Fallback: provenance.ActorID{Namespace: "legacy", UUID: uuid.Must(uuid.NewV7())},
	}
}

// TestImportLegacyAuditRow_AtomicAndAttributed proves one row imports as one committed
// operation attached to its referenced task, with the actor-text fallback applied and
// the journal still reproducible afterwards.
func TestImportLegacyAuditRow_AtomicAndAttributed(t *testing.T) {
	t.Parallel()
	imp, tr := openImporter(t)
	task := createImportTask(t, tr, "atomic")

	row := provadapter.LegacyAuditEvent{
		LegacyRowID: "1",
		SourceTable: "audit_events",
		RecordedAt:  time.Unix(1700000000, 0).UTC(),
		RawActor:    "some-legacy-worker",
		RawContexts: []string{"EpochContext:" + task.String()},
		Payload:     json.RawMessage(`{"event":"legacy.note"}`),
	}

	out, err := imp.ImportLegacyAuditRow(row, fallbackResolver())
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if out.Outcome.Kind != provadapter.OutcomeCommitted {
		t.Fatalf("outcome = %v, want Committed", out.Outcome.Kind)
	}
	if out.Outcome.Committed.ShortCircuited {
		t.Errorf("first import short-circuited; want a fresh commit")
	}
	if len(out.Tasks) != 1 || out.Tasks[0] != task {
		t.Errorf("fan-out tasks = %v, want [%v]", out.Tasks, task)
	}
	if !out.FallbackApplied {
		t.Errorf("fallback should have been applied for an unmapped raw actor")
	}
	if _, err := tr.Journal().ReplayProjections(); err != nil {
		t.Errorf("ReplayProjections after import: %v", err)
	}
}

// TestImportLegacyAuditRow_IdempotentReplay proves re-importing the identical row
// replays through the journal short-circuit — no duplicate rows.
func TestImportLegacyAuditRow_IdempotentReplay(t *testing.T) {
	t.Parallel()
	imp, tr := openImporter(t)
	task := createImportTask(t, tr, "replay")

	row := provadapter.LegacyAuditEvent{
		LegacyRowID: "7",
		SourceTable: "audit_events",
		RecordedAt:  time.Unix(1700000100, 0).UTC(),
		RawActor:    "worker",
		RawContexts: []string{task.String()},
		Payload:     json.RawMessage(`{"event":"x"}`),
	}
	res := fallbackResolver()

	first, err := imp.ImportLegacyAuditRow(row, res)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.Outcome.Committed.ShortCircuited {
		t.Fatalf("first import unexpectedly short-circuited")
	}

	second, err := imp.ImportLegacyAuditRow(row, res)
	if err != nil {
		t.Fatalf("replay import: %v", err)
	}
	if second.Outcome.Kind != provadapter.OutcomeCommitted {
		t.Fatalf("replay outcome = %v, want Committed", second.Outcome.Kind)
	}
	if !second.Outcome.Committed.ShortCircuited {
		t.Errorf("replay did not short-circuit; a re-import must be idempotent")
	}
	if first.OperationID != second.OperationID {
		t.Errorf("replay operation id %q != first %q; source-keyed id must be stable", second.OperationID, first.OperationID)
	}
	if _, err := tr.Journal().ReplayProjections(); err != nil {
		t.Errorf("ReplayProjections after replay: %v", err)
	}
}

// TestImportLegacyAuditRow_CompoundConflict proves a distinct operation that reuses
// the same source identity but changes the payload is rejected as a conflict and
// commits nothing.
func TestImportLegacyAuditRow_CompoundConflict(t *testing.T) {
	t.Parallel()
	imp, tr := openImporter(t)
	task := createImportTask(t, tr, "conflict")

	base := provadapter.LegacyAuditEvent{
		LegacyRowID: "9",
		SourceTable: "audit_events",
		RecordedAt:  time.Unix(1700000200, 0).UTC(),
		RawActor:    "worker",
		RawContexts: []string{task.String()},
		Payload:     json.RawMessage(`{"event":"first"}`),
	}
	res := fallbackResolver()
	if _, err := imp.ImportLegacyAuditRow(base, res); err != nil {
		t.Fatalf("base import: %v", err)
	}

	// Same source identity, different payload => different replay identity => conflict.
	mutated := base
	mutated.Payload = json.RawMessage(`{"event":"changed"}`)
	out, err := imp.ImportLegacyAuditRow(mutated, res)
	if err == nil {
		t.Fatalf("expected compound-conflict rejection, got outcome %v", out.Outcome.Kind)
	}
	if out.Outcome.Kind != provadapter.OutcomeConflict {
		t.Errorf("conflict outcome = %v, want Conflict", out.Outcome.Kind)
	}
	if _, err := tr.Journal().ReplayProjections(); err != nil {
		t.Errorf("ReplayProjections after conflict must still converge (nothing committed): %v", err)
	}
}

// TestImportLegacyAuditRow_FanOutSortedUnique proves a row referencing several tasks
// (with a duplicate and unqualified/qualified spellings) fans out to the sorted,
// de-duplicated task set in one operation.
func TestImportLegacyAuditRow_FanOutSortedUnique(t *testing.T) {
	t.Parallel()
	imp, tr := openImporter(t)
	a := createImportTask(t, tr, "fanout-a")
	b := createImportTask(t, tr, "fanout-b")

	row := provadapter.LegacyAuditEvent{
		LegacyRowID: "11",
		SourceTable: "audit_events",
		RecordedAt:  time.Unix(1700000300, 0).UTC(),
		RawActor:    "worker",
		RawContexts: []string{
			"EpochContext:" + a.String(),
			b.String(),
			"SliceContext:" + a.String(), // duplicate of a via a different spelling
			"GitContext:0123456789abcdef0123456789abcdef01234567", // non-task, ignored
		},
		Payload: json.RawMessage(`{"event":"multi"}`),
	}

	out, err := imp.ImportLegacyAuditRow(row, fallbackResolver())
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(out.Tasks) != 2 {
		t.Fatalf("fan-out tasks = %v, want exactly 2 unique tasks", out.Tasks)
	}
	// Sorted by canonical string order.
	if out.Tasks[0].String() > out.Tasks[1].String() {
		t.Errorf("fan-out tasks not sorted: %v", out.Tasks)
	}
	got := map[provenance.TaskID]bool{out.Tasks[0]: true, out.Tasks[1]: true}
	if !got[a] || !got[b] {
		t.Errorf("fan-out %v missing task a=%v or b=%v", out.Tasks, a, b)
	}
}

// TestImportLegacyAuditRow_NoTaskRejected proves a row that references no task is
// rejected (kept as a #14 non-task row) rather than silently dropped.
func TestImportLegacyAuditRow_NoTaskRejected(t *testing.T) {
	t.Parallel()
	imp, _ := openImporter(t)

	row := provadapter.LegacyAuditEvent{
		LegacyRowID: "13",
		SourceTable: "audit_events",
		RecordedAt:  time.Unix(1700000400, 0).UTC(),
		RawActor:    "worker",
		RawContexts: []string{"SessionContext:not-a-task", "GitContext:deadbeef"},
		Payload:     json.RawMessage(`{"event":"orphan"}`),
	}
	if _, err := imp.ImportLegacyAuditRow(row, fallbackResolver()); err == nil {
		t.Fatalf("expected rejection for a task-free legacy audit row")
	}
}

// TestImportLegacyAuditRow_MappedActor proves a raw actor present in the resolver map
// is attributed to the mapped actor with no fallback.
func TestImportLegacyAuditRow_MappedActor(t *testing.T) {
	t.Parallel()
	imp, tr := openImporter(t)
	task := createImportTask(t, tr, "mapped")

	mapped := provenance.ActorID{Namespace: "aura-plugins", UUID: uuid.Must(uuid.NewV7())}
	res := tasks.LegacyActorResolver{
		Map:      map[string]provenance.ActorID{"known-worker": mapped},
		Fallback: provenance.ActorID{Namespace: "legacy", UUID: uuid.Must(uuid.NewV7())},
	}
	row := provadapter.LegacyAuditEvent{
		LegacyRowID: "15",
		SourceTable: "audit_events",
		RecordedAt:  time.Unix(1700000500, 0).UTC(),
		RawActor:    "known-worker",
		RawContexts: []string{task.String()},
		Payload:     json.RawMessage(`{"event":"mapped"}`),
	}

	out, err := imp.ImportLegacyAuditRow(row, res)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if out.FallbackApplied {
		t.Errorf("fallback should not apply for a mapped raw actor")
	}
	if out.AttributedActor != mapped {
		t.Errorf("attributed actor = %v, want mapped %v", out.AttributedActor, mapped)
	}
}

// TestImportLegacyAuditRow_RejectsMissingFallback proves the import refuses to run
// without a valid fallback actor.
func TestImportLegacyAuditRow_RejectsMissingFallback(t *testing.T) {
	t.Parallel()
	imp, tr := openImporter(t)
	task := createImportTask(t, tr, "no-fallback")

	row := provadapter.LegacyAuditEvent{
		LegacyRowID: "17",
		SourceTable: "audit_events",
		RecordedAt:  time.Unix(1700000600, 0).UTC(),
		RawContexts: []string{task.String()},
		Payload:     json.RawMessage(`{"event":"x"}`),
	}
	if _, err := imp.ImportLegacyAuditRow(row, tasks.LegacyActorResolver{}); err == nil {
		t.Fatalf("expected rejection when the resolver has no fallback actor")
	}
}
