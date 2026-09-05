package tasks_test

// tracker_test.go — Unit + integration tests for the TaskTracker wrapper.
//
// The system under test is the trackerImpl wrapper (internal/tasks/tracker.go).
// Its dependencies (provenance.Tracker, audit.Trail) may be mocked when the
// test only cares about forwarding correctness; the pasture-only methods
// (SetAgentCategories, AttachContext, etc.) require a real *sql.DB so the SQL
// layer is exercised end-to-end.
//
// File-backed `t.TempDir()` only — never in-memory SQLite (which bypasses
// WAL / busy_timeout / fsync).

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/testutil"
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
	return testutil.OpenGoldenTaskTracker(t)
}

// registerSoftwareAgentForTest creates a SoftwareAgent for use in agent-side
// tests. Returns the AgentId.
func registerSoftwareAgentForTest(t *testing.T, tracker protocol.TaskTracker, name string) provenance.AgentID {
	t.Helper()
	sa, err := tracker.RegisterSoftwareAgent("pasture-test", name, "0.0.0", "test")
	if err != nil {
		t.Fatalf("RegisterSoftwareAgent(%q) failed: %v", name, err)
	}
	return sa.ID
}

// recordEventForTest records one audit event and returns the event ID via a
// direct SELECT MAX(id) (audit.Trail.RecordEvent doesn't return the row ID;
// we need it for AttachContext). Adequate for these serial tests; production
// callers use RecordEventReturningId, which returns the id from the INSERT
// itself.
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

// ─── One database file holds tasks, events and the epoch edge between them ───
//
// Given a fresh ~/.local/share/pasture/pasture.db,
// When the user creates a request task, records one audit event, and attaches
//   an EpochContext edge,
// Then the database contains: a row in `tasks` (Provenance), one row in
//   audit_events (audit), and a matching row in context_edges with
//   kind=EpochContext and context_id=<task-id-string>,
// Should not there be a separate audit.db or provenance.db file.

func TestOneDatabaseFileHoldsTheTaskItsEventAndTheEpochEdgeThatLinksThem(t *testing.T) {
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

	// ─── When: create a request task via Provenance ────────────────────
	req, err := tracker.Create(
		"pasture-test",
		"Build X",
		"single-file test request",
		provenance.TaskTypeFeature,
		provenance.PriorityMedium,
		provenance.PhaseRequest,
	)
	if err != nil {
		t.Fatalf("Create REQUEST failed: %v", err)
	}
	epochId := req.ID.String()

	// ─── When: record one audit event + attach EpochContext ────────────
	now := time.Now().UTC()
	ev := protocol.AuditEvent{
		EpochId:   epochId,
		Phase:     protocol.PhaseRequest,
		Role:      "human",
		EventType: protocol.EventPhaseTransition,
		Payload:   map[string]any{"to": "elicit"},
		Timestamp: now,
	}
	eventId := recordEventForTest(t, ctx, tracker, dbPath, ev)
	if eventId <= 0 {
		t.Fatalf("recordEventForTest returned non-positive eventId %d", eventId)
	}

	if err := tracker.AttachContext(ctx, eventId, protocol.ContextEpoch, epochId); err != nil {
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
	events, err := tracker.QueryEvents(ctx, epochId, nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("QueryEvents returned %d events, want 1", len(events))
	}

	// ─── Then: context_edges row exists for (event, ContextEpoch, epochId) ─
	contexts, err := tracker.EventContexts(ctx, eventId)
	if err != nil {
		t.Fatalf("EventContexts failed: %v", err)
	}
	if len(contexts) != 1 {
		t.Fatalf("EventContexts returned %d contexts, want 1", len(contexts))
	}
	if contexts[0].Kind != protocol.ContextEpoch {
		t.Errorf("context kind = %q, want %q", contexts[0].Kind, protocol.ContextEpoch)
	}
	if contexts[0].ContextId != epochId {
		t.Errorf("context_id = %q, want %q", contexts[0].ContextId, epochId)
	}

	// ─── Then: Timeline finds the event via the context edge ───────────
	timeline, err := tracker.Timeline(ctx, protocol.ContextEpoch, epochId)
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

// ─── An event attached to two contexts appears on both timelines ─────────────
//
// Given a workflow running for one epoch id with one active slice id,
// When an event is recorded and attached to BOTH ContextEpoch=<epoch id> and
//   ContextSlice=<slice id> via two AttachContext calls,
// Then Timeline(ContextEpoch, <epoch id>) AND Timeline(ContextSlice, <slice id>)
//   both include the event.
// Should not the event be findable only via one context.

func TestAnEventAttachedToTwoContextsAppearsOnBothTimelines(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tracker, dbPath := openTrackerForTest(t)
	const (
		epochId = "epoch--01968a3c-1234-7000-8000-000000000001"
		sliceId = "slice--01968a3c-1234-7000-8000-000000000002"
	)

	ev := protocol.AuditEvent{
		EpochId:   epochId,
		Phase:     protocol.PhaseWorkerSlices,
		Role:      "worker",
		EventType: protocol.EventSliceStarted,
		Payload:   map[string]any{"slice": "slice-a"},
		Timestamp: time.Now().UTC(),
	}
	eventId := recordEventForTest(t, ctx, tracker, dbPath, ev)

	// ─── When: attach to both contexts ─────────────────────────────────
	if err := tracker.AttachContext(ctx, eventId, protocol.ContextEpoch, epochId); err != nil {
		t.Fatalf("AttachContext(Epoch) failed: %v", err)
	}
	if err := tracker.AttachContext(ctx, eventId, protocol.ContextSlice, sliceId); err != nil {
		t.Fatalf("AttachContext(Slice) failed: %v", err)
	}

	// ─── Then: both timelines include the event ────────────────────────
	epochEvents, err := tracker.Timeline(ctx, protocol.ContextEpoch, epochId)
	if err != nil {
		t.Fatalf("Timeline(Epoch) failed: %v", err)
	}
	if len(epochEvents) != 1 {
		t.Errorf("Timeline(Epoch) returned %d events, want 1", len(epochEvents))
	}

	sliceEvents, err := tracker.Timeline(ctx, protocol.ContextSlice, sliceId)
	if err != nil {
		t.Fatalf("Timeline(Slice) failed: %v", err)
	}
	if len(sliceEvents) != 1 {
		t.Errorf("Timeline(Slice) returned %d events, want 1", len(sliceEvents))
	}

	// ─── Then: EventContexts returns BOTH edges ────────────────────────
	contexts, err := tracker.EventContexts(ctx, eventId)
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

func TestAttachContext_RejectsEmptyContextId(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, _ := openTrackerForTest(t)

	err := tracker.AttachContext(ctx, 1, protocol.ContextEpoch, "")
	if err == nil {
		t.Fatal("AttachContext(empty contextId) returned nil, want validation error")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("error is not *StructuredError: %v", err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryValidation)
	}
}

func TestAttachContext_RejectsNonPositiveEventId(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, _ := openTrackerForTest(t)

	err := tracker.AttachContext(ctx, 0, protocol.ContextEpoch, "epoch-1")
	if err == nil {
		t.Fatal("AttachContext(eventId=0) returned nil, want validation error")
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
		EpochId:   "epoch-x",
		Phase:     protocol.PhaseRequest,
		Role:      "human",
		EventType: protocol.EventPhaseTransition,
		Payload:   map[string]any{},
		Timestamp: time.Now().UTC(),
	}
	eventId := recordEventForTest(t, ctx, tracker, dbPath, ev)

	for i := 0; i < 3; i++ {
		if err := tracker.AttachContext(ctx, eventId, protocol.ContextEpoch, "epoch-x"); err != nil {
			t.Fatalf("AttachContext call %d failed: %v", i, err)
		}
	}

	contexts, err := tracker.EventContexts(ctx, eventId)
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

	// Use a freshly-minted AgentId that we never SetAgentCategories on.
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
		t.Fatalf("Timeline(empty contextId) failed: %v", err)
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

	task, err := tracker.Create("pasture-test", "fwd", "forward smoke", provenance.TaskTypeFeature, provenance.PriorityMedium, provenance.PhaseRequest)
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
		EpochId:   "epoch-fwd",
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

// TestTheStoreRefusesANilDiagnosticSinkAndANilClock: the sink and the clock are
// REQUIRED construction inputs of the store, not library defaults. A nil is
// refused with a validation error naming the field, so a construction site
// nobody enumerated fails loudly at the place the enumeration was incomplete
// instead of working quietly with a default nobody chose.
func TestTheStoreRefusesANilDiagnosticSinkAndANilClock(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	_, err := tasks.OpenTaskTrackerWithOptions(dbPath, tasks.WithDiagnosticSink(nil))
	require.Error(t, err, "a nil diagnostics sink must be refused at open")
	assert.Contains(t, err.Error(), "has no diagnostics sink", "the refusal names the field")
	assert.Contains(t, err.Error(), "validation error", "a nil sink is a construction fault, not a storage fault")
	_, err = tasks.OpenTaskTrackerWithOptions(dbPath, tasks.WithStoreClock(nil))
	require.Error(t, err, "a nil clock must be refused at open")
	assert.Contains(t, err.Error(), "has no clock", "the refusal names the field")
	_, statErr := os.Stat(dbPath)
	assert.True(t, os.IsNotExist(statErr), "a refused open creates no database file")
}

// TestEveryProductionOpenerPassesTheProcessStderrAndTheWallClock is the
// ENUMERATION of production construction sites, derived from source rather
// than listed: every exported function of the tasks package that returns a
// protocol.TaskTracker is a production opener. For each one it checks two
// things, and the test name is true only because both are checked:
//
//  1. THE OPTIONS VALUE THE OPENER PASSES IS ROOTED IN productionOpenDefaults().
//     The closure of the opener over the package's non-test functions must
//     contain at least one call to openTaskTrackerWithOptions, and the
//     options argument of EVERY such call must be either the call
//     productionOpenDefaults() itself or a local variable whose declaration
//     in the enclosing function is assigned from that call. Reaching
//     productionOpenDefaults somewhere in the closure is NOT enough: an opener
//     that builds a fresh options literal and copies one field out of the
//     defaults reaches the function and passes neither the sink nor the
//     clock, and only the argument check catches it.
//  2. productionOpenDefaults() ITSELF NAMES os.Stderr AND wallClock{} in its
//     body, so a value rooted there carries the process stderr and the wall
//     clock.
//
// WHAT IT VISITS: every non-test .go file of internal/tasks; the bodies of
// the exported openers and of every same-package function they call by name;
// the arguments of each openTaskTrackerWithOptions call in those bodies; the
// declarations of the local variables those arguments name; and the body of
// productionOpenDefaults.
// WHAT IT DOES NOT READ: test files, method calls, any other package, or what
// an OpenTaskTrackerOption does to the value AFTER it was rooted in the
// defaults. An option that sets the sink or the clock to nil is not seen
// here; it is caught at runtime by the refusal in openTaskTrackerWithOptions,
// which TestTheStoreRefusesANilDiagnosticSinkAndANilClock pins.
// NON-VACUITY: at least two openers are derived (the plain opener and the
// options opener today); fewer is RED.
// MUTATIONS, each RED naming the opener: make OpenTaskTrackerWithOptions
// start from an empty options value instead of the production defaults; add
// an exported opener that passes openTaskTrackerOptions{timeouts:
// productionOpenDefaults().timeouts}, which reaches the defaults and passes
// neither the sink nor the clock; remove os.Stderr or wallClock{} from
// productionOpenDefaults.
func TestEveryProductionOpenerPassesTheProcessStderrAndTheWallClock(t *testing.T) {
	t.Parallel()
	dir := "."
	fset := token.NewFileSet()
	funcs := map[string]*ast.FuncDecl{}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		require.NoError(t, parseErr)
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil && function.Body != nil {
				funcs[function.Name.Name] = function
			}
		}
	}

	// 2. The defaults value names the process stderr and the wall clock.
	defaults, known := funcs["productionOpenDefaults"]
	require.True(t, known, "productionOpenDefaults must be declared in a non-test file of internal/tasks: it is the one place the production sink and clock are named")
	stderr, wall := false, false
	ast.Inspect(defaults.Body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := typed.X.(*ast.Ident); ok && pkg.Name == "os" && typed.Sel.Name == "Stderr" {
				stderr = true
			}
		case *ast.CompositeLit:
			if ident, ok := typed.Type.(*ast.Ident); ok && ident.Name == "wallClock" {
				wall = true
			}
		}
		return true
	})
	require.True(t, stderr, "productionOpenDefaults never names os.Stderr: the diagnostics sink is a required input and the production defaults must carry the process stderr")
	require.True(t, wall, "productionOpenDefaults never names wallClock{}: the store clock is a required input and the production defaults must carry the wall clock")

	// 1. Every opener passes a value rooted in the defaults, at every call.
	openers := []string{}
	for name, function := range funcs {
		if !function.Name.IsExported() || function.Type.Results == nil {
			continue
		}
		returnsTracker := false
		for _, result := range function.Type.Results.List {
			if selector, ok := result.Type.(*ast.SelectorExpr); ok {
				if pkg, isIdent := selector.X.(*ast.Ident); isIdent && pkg.Name == "protocol" && selector.Sel.Name == "TaskTracker" {
					returnsTracker = true
				}
			}
		}
		if !returnsTracker {
			continue
		}
		openers = append(openers, name)
		seen := map[string]bool{name: true}
		closure := []*ast.FuncDecl{function}
		constructorCalls := 0
		for index := 0; index < len(closure); index++ {
			enclosing := closure[index]
			ast.Inspect(enclosing.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				ident, ok := call.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				if ident.Name == "openTaskTrackerWithOptions" {
					constructorCalls++
					require.Len(t, call.Args, 2, "production opener %s: openTaskTrackerWithOptions takes the path and the options value", name)
					assert.True(t, optionsRootedInProductionDefaults(call.Args[1], enclosing.Body),
						"production opener %s passes openTaskTrackerWithOptions an options value that is not rooted in productionOpenDefaults() (in %s): it must pass the call itself or a local assigned from it, or the opener carries neither the process stderr nor the wall clock", name, enclosing.Name.Name)
					return true
				}
				if callee, known := funcs[ident.Name]; known && !seen[ident.Name] {
					seen[ident.Name] = true
					closure = append(closure, callee)
				}
				return true
			})
		}
		assert.Positive(t, constructorCalls, "production opener %s never reaches openTaskTrackerWithOptions through same-package calls, so nothing here can say what it passes", name)
	}
	sort.Strings(openers)
	require.GreaterOrEqual(t, len(openers), 2, "non-vacuity: at least two production openers must be derived; found %v", openers)
}

// optionsRootedInProductionDefaults reports whether an options argument is the
// call productionOpenDefaults() itself, or an identifier whose declaration in
// the enclosing body is assigned from that call (cfg := productionOpenDefaults()
// or var cfg = productionOpenDefaults()). Anything else, a composite literal
// in particular, is not rooted, whatever it reaches.
func optionsRootedInProductionDefaults(argument ast.Expr, enclosing *ast.BlockStmt) bool {
	switch typed := argument.(type) {
	case *ast.CallExpr:
		return isProductionDefaultsCall(typed)
	case *ast.Ident:
		rooted := false
		ast.Inspect(enclosing, func(node ast.Node) bool {
			switch statement := node.(type) {
			case *ast.AssignStmt:
				if statement.Tok != token.DEFINE || len(statement.Lhs) != 1 || len(statement.Rhs) != 1 {
					return true
				}
				if lhs, ok := statement.Lhs[0].(*ast.Ident); ok && lhs.Name == typed.Name {
					if call, isCall := statement.Rhs[0].(*ast.CallExpr); isCall && isProductionDefaultsCall(call) {
						rooted = true
					}
				}
			case *ast.ValueSpec:
				if len(statement.Names) != 1 || len(statement.Values) != 1 || statement.Names[0].Name != typed.Name {
					return true
				}
				if call, isCall := statement.Values[0].(*ast.CallExpr); isCall && isProductionDefaultsCall(call) {
					rooted = true
				}
			}
			return true
		})
		return rooted
	default:
		return false
	}
}

func isProductionDefaultsCall(call *ast.CallExpr) bool {
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == "productionOpenDefaults" && len(call.Args) == 0
}

// TestAFailedReclaimIsOneLineOnTheStoreSinkAndSuccessIsSilent: the store owns
// the sink, so the ONE line a failed orphan reclaim earns is written by the
// store's rebuild wrapper, nothing threads a writer down, and a rebuild whose
// reclaim succeeds writes nothing at all.
func TestAFailedReclaimIsOneLineOnTheStoreSinkAndSuccessIsSilent(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	bootstrap, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	_, err = bootstrap.Create("file://tracker-sink-test", "bootstrap", "initialize the persisted ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	require.NoError(t, bootstrap.Close())

	var sink bytes.Buffer
	tracker, err := tasks.OpenTaskTrackerWithOptions(dbPath, tasks.WithDiagnosticSink(&sink))
	require.NoError(t, err)
	defer tracker.Close()
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
	assert.Empty(t, sink.String(), "a rebuild whose reclaim succeeds prints nothing")

	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	concrete, ok := reader.(projection.Reader)
	require.True(t, ok)
	_, err = concrete.DB.Exec(`ALTER TABLE lifecycle_payload_blobs RENAME COLUMN written_at TO written_at_gone`)
	require.NoError(t, err)
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker), "a failed reclaim never fails the rebuild")
	lines := strings.Split(strings.TrimRight(sink.String(), "\n"), "\n")
	require.Len(t, lines, 1, "exactly one line on the store's sink, got %q", sink.String())
	assert.True(t, strings.HasPrefix(lines[0], "pasture: the orphan payload reclaim inside the projection rebuild failed: "), "the line names what failed: %q", lines[0])
	assert.Contains(t, lines[0], "the rebuild still completed and nothing was deleted", "the line says the rebuild completed and nothing was deleted")
	assert.Contains(t, lines[0], "the next read command retries the reclaim", "the line says what happens next")
}
