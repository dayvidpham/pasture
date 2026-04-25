package tasks_test

// tracker_test.go — Unit + integration tests for the TaskTracker wrapper.
//
// PROPOSAL-2 §10.3 (testing strategy): the system under test is the
// trackerImpl wrapper. Its dependencies (provenance.Tracker, audit.Trail) may
// be mocked when the test only cares about forwarding correctness; the
// pasture-only methods (SetAgentCategories, AttachContext, etc.) require a
// real *sql.DB so the SQL layer is exercised end-to-end.
//
// Per pasture/CLAUDE.md and IMPL_PLAN §1.2: file-backed `t.TempDir()` only —
// never in-memory SQLite (which bypasses WAL / busy_timeout / fsync).

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Test helpers ────────────────────────────────────────────────────────────

// tempDBPath returns a unique file path under t.TempDir() for a SQLite DB.
// The file is created on first open; the parent dir already exists (TempDir
// is materialised before this function is called).
func tempDBPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

// openTrackerForTest opens a real TaskTracker against a temp file, registers
// cleanup, and returns it. Used by integration tests; unit tests that mock
// the dependencies should construct trackerImpl directly via OpenTaskTracker
// (the wrapper struct is unexported by design).
func openTrackerForTest(t *testing.T) (protocol.TaskTracker, string) {
	t.Helper()
	dbPath := tempDBPath(t, "pasture.db")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker(%q) failed: %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := tracker.Close(); err != nil {
			t.Errorf("Close failed during cleanup: %v", err)
		}
	})
	return tracker, dbPath
}

// registerSoftwareAgentForTest creates a SoftwareAgent for use in agent-side
// tests. Returns the AgentID.
func registerSoftwareAgentForTest(t *testing.T, tracker protocol.TaskTracker, name string) provenance.AgentID {
	t.Helper()
	sa, err := tracker.RegisterSoftwareAgent("pasture-test", name, "0.0.0", "test")
	if err != nil {
		t.Fatalf("RegisterSoftwareAgent(%q) failed: %v", name, err)
	}
	return sa.ID
}

// recordEventForTest records one audit event and returns the event ID via a
// direct SELECT (audit.Trail.RecordEvent doesn't return the row ID; we need
// it for AttachContext). Adequate for tests; production callers will get the
// ID from a future audit-side enhancement (out of scope for S5).
func recordEventForTest(t *testing.T, ctx context.Context, tracker protocol.TaskTracker, dbPath string, ev protocol.AuditEvent) int64 {
	t.Helper()
	if err := tracker.RecordEvent(ctx, ev); err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}
	// Read back the most-recently-inserted row ID via a side channel.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open for ID lookup failed: %v", err)
	}
	defer db.Close()
	var id int64
	if err := db.QueryRow(`SELECT MAX(id) FROM audit_events`).Scan(&id); err != nil {
		t.Fatalf("SELECT MAX(id) failed: %v", err)
	}
	return id
}

// ─── BDD Scenario 1: Single .db file with epoch alignment ────────────────────
//
// Given a fresh ~/.local/share/pasture/pasture.db,
// When the user creates a REQUEST task, records one audit event, and attaches
//   an EpochContext edge,
// Then the database contains: a row in `tasks` (Provenance), one row in
//   audit_events (audit), and a matching row in context_edges with
//   kind=EpochContext and context_id=<task-id-string>,
// Should not there be a separate audit.db or provenance.db file.

func TestScenario1_SingleDBFileWithEpochAlignment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "pasture.db")

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker failed: %v", err)
	}
	defer func() {
		if err := tracker.Close(); err != nil {
			t.Errorf("Close failed: %v", err)
		}
	}()

	// ─── Given: fresh DB ───────────────────────────────────────────────
	// Verify only ONE pasture.db file exists in the temp dir (no
	// audit.db / provenance.db sidecars).
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, e := range entries {
		base := e.Name()
		// SQLite WAL mode creates pasture.db-wal and pasture.db-shm
		// sidecars while the file is open — those are expected.
		if base == "pasture.db" || base == "pasture.db-wal" || base == "pasture.db-shm" {
			continue
		}
		t.Errorf("unexpected sidecar file in tempDir: %q (only pasture.db[-wal,-shm] are permitted)", base)
	}

	// ─── When: create REQUEST task via Provenance ──────────────────────
	req, err := tracker.Create(
		"aura-plugins-test",
		"Build X",
		"Scenario 1 test request",
		provenance.TypeFeature,
		provenance.PriorityP2,
		provenance.PhaseRequest,
	)
	if err != nil {
		t.Fatalf("Create REQUEST failed: %v", err)
	}
	epochID := req.ID.String()

	// ─── When: record one audit event + attach EpochContext ────────────
	now := time.Now().UTC()
	ev := protocol.AuditEvent{
		EpochID:   epochID,
		Phase:     protocol.PhaseRequest,
		Role:      "human",
		EventType: protocol.EventPhaseTransition,
		Payload:   map[string]any{"to": "elicit"},
		Timestamp: now,
	}
	eventID := recordEventForTest(t, ctx, tracker, dbPath, ev)
	if eventID <= 0 {
		t.Fatalf("recordEventForTest returned non-positive eventID %d", eventID)
	}

	if err := tracker.AttachContext(ctx, eventID, protocol.ContextEpoch, epochID); err != nil {
		t.Fatalf("AttachContext failed: %v", err)
	}

	// ─── Then: tasks row exists ────────────────────────────────────────
	got, err := tracker.Show(req.ID)
	if err != nil {
		t.Fatalf("Show after Create failed: %v", err)
	}
	if got.Title != "Build X" {
		t.Errorf("Show returned title %q, want %q", got.Title, "Build X")
	}

	// ─── Then: audit_events row exists ─────────────────────────────────
	events, err := tracker.QueryEvents(ctx, epochID, nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("QueryEvents returned %d events, want 1", len(events))
	}

	// ─── Then: context_edges row exists for (event, ContextEpoch, epochID) ─
	contexts, err := tracker.EventContexts(ctx, eventID)
	if err != nil {
		t.Fatalf("EventContexts failed: %v", err)
	}
	if len(contexts) != 1 {
		t.Fatalf("EventContexts returned %d contexts, want 1", len(contexts))
	}
	if contexts[0].Kind != protocol.ContextEpoch {
		t.Errorf("context kind = %q, want %q", contexts[0].Kind, protocol.ContextEpoch)
	}
	if contexts[0].ContextID != epochID {
		t.Errorf("context_id = %q, want %q", contexts[0].ContextID, epochID)
	}

	// ─── Then: Timeline finds the event via the context edge ───────────
	timeline, err := tracker.Timeline(ctx, protocol.ContextEpoch, epochID)
	if err != nil {
		t.Fatalf("Timeline failed: %v", err)
	}
	if len(timeline) != 1 {
		t.Fatalf("Timeline returned %d events, want 1", len(timeline))
	}
	if timeline[0].EventType != protocol.EventPhaseTransition {
		t.Errorf("timeline event type = %q, want %q", timeline[0].EventType, protocol.EventPhaseTransition)
	}
}

// ─── BDD Scenario 7: Multi-context attachment ────────────────────────────────
//
// Given a workflow running for epochID=E1 with active slice S1,
// When an event is recorded and attached to BOTH ContextEpoch=E1 and
//   ContextSlice=S1 via two AttachContext calls,
// Then Timeline(ContextEpoch, E1) AND Timeline(ContextSlice, S1) both include
//   the event.
// Should not the event be findable only via one context.

func TestScenario7_MultiContextAttachment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tracker, dbPath := openTrackerForTest(t)
	const (
		epochID = "aura-plugins--01968a3c-1234-7000-8000-000000000001"
		sliceID = "aura-plugins--01968a3c-1234-7000-8000-000000000002"
	)

	ev := protocol.AuditEvent{
		EpochID:   epochID,
		Phase:     protocol.PhaseWorkerSlices,
		Role:      "worker",
		EventType: protocol.EventSliceStarted,
		Payload:   map[string]any{"slice": "S5"},
		Timestamp: time.Now().UTC(),
	}
	eventID := recordEventForTest(t, ctx, tracker, dbPath, ev)

	// ─── When: attach to both contexts ─────────────────────────────────
	if err := tracker.AttachContext(ctx, eventID, protocol.ContextEpoch, epochID); err != nil {
		t.Fatalf("AttachContext(Epoch) failed: %v", err)
	}
	if err := tracker.AttachContext(ctx, eventID, protocol.ContextSlice, sliceID); err != nil {
		t.Fatalf("AttachContext(Slice) failed: %v", err)
	}

	// ─── Then: both timelines include the event ────────────────────────
	epochEvents, err := tracker.Timeline(ctx, protocol.ContextEpoch, epochID)
	if err != nil {
		t.Fatalf("Timeline(Epoch) failed: %v", err)
	}
	if len(epochEvents) != 1 {
		t.Errorf("Timeline(Epoch) returned %d events, want 1", len(epochEvents))
	}

	sliceEvents, err := tracker.Timeline(ctx, protocol.ContextSlice, sliceID)
	if err != nil {
		t.Fatalf("Timeline(Slice) failed: %v", err)
	}
	if len(sliceEvents) != 1 {
		t.Errorf("Timeline(Slice) returned %d events, want 1", len(sliceEvents))
	}

	// ─── Then: EventContexts returns BOTH edges ────────────────────────
	contexts, err := tracker.EventContexts(ctx, eventID)
	if err != nil {
		t.Fatalf("EventContexts failed: %v", err)
	}
	if len(contexts) != 2 {
		t.Fatalf("EventContexts returned %d contexts, want 2", len(contexts))
	}
	gotKinds := make(map[protocol.ContextKind]bool)
	for _, c := range contexts {
		gotKinds[c.Kind] = true
	}
	if !gotKinds[protocol.ContextEpoch] || !gotKinds[protocol.ContextSlice] {
		t.Errorf("EventContexts kinds = %v, want both ContextEpoch and ContextSlice", gotKinds)
	}
}

// ─── Validation tests for AttachContext ──────────────────────────────────────

func TestAttachContext_RejectsInvalidKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, _ := openTrackerForTest(t)

	err := tracker.AttachContext(ctx, 1, protocol.ContextKind("Bogus"), "any")
	if err == nil {
		t.Fatal("AttachContext(invalid kind) returned nil, want validation error")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("error is not *StructuredError: %v", err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryValidation)
	}
}

func TestAttachContext_RejectsEmptyContextID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, _ := openTrackerForTest(t)

	err := tracker.AttachContext(ctx, 1, protocol.ContextEpoch, "")
	if err == nil {
		t.Fatal("AttachContext(empty contextID) returned nil, want validation error")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("error is not *StructuredError: %v", err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryValidation)
	}
}

func TestAttachContext_RejectsNonPositiveEventID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, _ := openTrackerForTest(t)

	err := tracker.AttachContext(ctx, 0, protocol.ContextEpoch, "epoch-1")
	if err == nil {
		t.Fatal("AttachContext(eventID=0) returned nil, want validation error")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("error is not *StructuredError: %v", err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryValidation)
	}
}

func TestAttachContext_IsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, dbPath := openTrackerForTest(t)

	ev := protocol.AuditEvent{
		EpochID:   "epoch-x",
		Phase:     protocol.PhaseRequest,
		Role:      "human",
		EventType: protocol.EventPhaseTransition,
		Payload:   map[string]any{},
		Timestamp: time.Now().UTC(),
	}
	eventID := recordEventForTest(t, ctx, tracker, dbPath, ev)

	for i := 0; i < 3; i++ {
		if err := tracker.AttachContext(ctx, eventID, protocol.ContextEpoch, "epoch-x"); err != nil {
			t.Fatalf("AttachContext call %d failed: %v", i, err)
		}
	}

	contexts, err := tracker.EventContexts(ctx, eventID)
	if err != nil {
		t.Fatalf("EventContexts failed: %v", err)
	}
	if len(contexts) != 1 {
		t.Errorf("after 3 idempotent AttachContext calls, EventContexts = %d, want 1 (BCNF composite PK enforces uniqueness)", len(contexts))
	}
}

// ─── SetAgentCategories / AgentCategories ────────────────────────────────────

func TestSetAndGetAgentCategories_RoundTrip(t *testing.T) {
	t.Parallel()
	tracker, _ := openTrackerForTest(t)

	id := registerSoftwareAgentForTest(t, tracker, "pasture/test/round-trip")

	if err := tracker.SetAgentCategories(id, protocol.AutomatonRoleHookHandler, protocol.PastureRoleNone); err != nil {
		t.Fatalf("SetAgentCategories failed: %v", err)
	}

	gotAuto, gotPast, err := tracker.AgentCategories(id)
	if err != nil {
		t.Fatalf("AgentCategories failed: %v", err)
	}
	if gotAuto != protocol.AutomatonRoleHookHandler {
		t.Errorf("automaton = %q, want %q", gotAuto, protocol.AutomatonRoleHookHandler)
	}
	if gotPast != protocol.PastureRoleNone {
		t.Errorf("pasture role = %q, want %q", gotPast, protocol.PastureRoleNone)
	}
}

func TestAgentCategories_ReturnsNoneWhenNoRow(t *testing.T) {
	t.Parallel()
	tracker, _ := openTrackerForTest(t)

	// Use a freshly-minted AgentID that we never SetAgentCategories on.
	id := provenance.AgentID{Namespace: "pasture-test", UUID: uuid.Must(uuid.NewV7())}

	auto, past, err := tracker.AgentCategories(id)
	if err != nil {
		t.Fatalf("AgentCategories on unknown agent: %v", err)
	}
	if auto != protocol.AutomatonRoleNone {
		t.Errorf("automaton = %q, want %q (default for missing row)", auto, protocol.AutomatonRoleNone)
	}
	if past != protocol.PastureRoleNone {
		t.Errorf("pasture role = %q, want %q (default for missing row)", past, protocol.PastureRoleNone)
	}
}

func TestSetAgentCategories_RejectsInvalidAutomatonRole(t *testing.T) {
	t.Parallel()
	tracker, _ := openTrackerForTest(t)

	id := registerSoftwareAgentForTest(t, tracker, "pasture/test/invalid-auto")

	err := tracker.SetAgentCategories(id, protocol.AutomatonRole("Bogus"), protocol.PastureRoleNone)
	if err == nil {
		t.Fatal("SetAgentCategories(invalid automaton) returned nil, want validation error")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("error is not *StructuredError: %v", err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryValidation)
	}
}

func TestSetAgentCategories_RejectsInvalidPastureRole(t *testing.T) {
	t.Parallel()
	tracker, _ := openTrackerForTest(t)

	id := registerSoftwareAgentForTest(t, tracker, "pasture/test/invalid-past")

	err := tracker.SetAgentCategories(id, protocol.AutomatonRoleNone, protocol.PastureRole("Bogus"))
	if err == nil {
		t.Fatal("SetAgentCategories(invalid pasture) returned nil, want validation error")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("error is not *StructuredError: %v", err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryValidation)
	}
}

func TestSetAgentCategories_IsIdempotent(t *testing.T) {
	t.Parallel()
	tracker, _ := openTrackerForTest(t)
	id := registerSoftwareAgentForTest(t, tracker, "pasture/test/idem")

	if err := tracker.SetAgentCategories(id, protocol.AutomatonRoleConstraintChecker, protocol.PastureRoleNone); err != nil {
		t.Fatalf("first Set failed: %v", err)
	}
	// Second call replaces the row.
	if err := tracker.SetAgentCategories(id, protocol.AutomatonRoleHookHandler, protocol.PastureRoleNone); err != nil {
		t.Fatalf("second Set failed: %v", err)
	}

	auto, _, err := tracker.AgentCategories(id)
	if err != nil {
		t.Fatalf("AgentCategories failed: %v", err)
	}
	if auto != protocol.AutomatonRoleHookHandler {
		t.Errorf("after replacement, automaton = %q, want %q", auto, protocol.AutomatonRoleHookHandler)
	}
}

// ─── Timeline validation ─────────────────────────────────────────────────────

func TestTimeline_RejectsInvalidKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, _ := openTrackerForTest(t)

	_, err := tracker.Timeline(ctx, protocol.ContextKind("Bogus"), "any")
	if err == nil {
		t.Fatal("Timeline(invalid kind) returned nil, want validation error")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("error is not *StructuredError: %v", err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryValidation)
	}
}

func TestTimeline_EmptyContextIDReturnsEmptySlice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, _ := openTrackerForTest(t)

	events, err := tracker.Timeline(ctx, protocol.ContextEpoch, "")
	if err != nil {
		t.Fatalf("Timeline(empty contextID) failed: %v", err)
	}
	if events == nil {
		t.Error("Timeline returned nil slice; want empty non-nil slice")
	}
	if len(events) != 0 {
		t.Errorf("Timeline returned %d events, want 0", len(events))
	}
}

// ─── Close idempotency ───────────────────────────────────────────────────────

func TestClose_IsIdempotent(t *testing.T) {
	t.Parallel()
	dbPath := tempDBPath(t, "close.db")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker failed: %v", err)
	}

	if err := tracker.Close(); err != nil {
		t.Errorf("first Close failed: %v", err)
	}
	if err := tracker.Close(); err != nil {
		t.Errorf("second Close failed: %v (want nil — Close is idempotent)", err)
	}
	if err := tracker.Close(); err != nil {
		t.Errorf("third Close failed: %v (want nil — Close is idempotent)", err)
	}
}

// ─── OpenTaskTracker resolves DB path ────────────────────────────────────────

func TestOpenTaskTracker_CreatesParentDirectory(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	// Path with a non-existent intermediate directory.
	dbPath := filepath.Join(tempDir, "nested", "subdir", "pasture.db")

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker on nested path failed: %v", err)
	}
	defer tracker.Close()

	// Verify the file was created.
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("pasture.db not created at %q: %v", dbPath, err)
	}
}

// ─── Embedded interface forwarding (smoke test for provenance + audit) ──────

func TestForwarding_ProvenanceCreateAndShow(t *testing.T) {
	t.Parallel()
	tracker, _ := openTrackerForTest(t)

	task, err := tracker.Create("pasture-test", "fwd", "forward smoke", provenance.TypeFeature, provenance.PriorityP2, provenance.PhaseRequest)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := tracker.Show(task.ID)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if got.ID != task.ID {
		t.Errorf("Show returned %v, want %v", got.ID, task.ID)
	}
}

func TestForwarding_AuditRecordAndQuery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, _ := openTrackerForTest(t)

	ev := protocol.AuditEvent{
		EpochID:   "epoch-fwd",
		Phase:     protocol.PhaseRequest,
		Role:      "test",
		EventType: protocol.EventPhaseTransition,
		Payload:   map[string]any{"fwd": true},
		Timestamp: time.Now().UTC(),
	}
	if err := tracker.RecordEvent(ctx, ev); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	got, err := tracker.QueryEvents(ctx, "epoch-fwd", nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("QueryEvents returned %d events, want 1", len(got))
	}
}
