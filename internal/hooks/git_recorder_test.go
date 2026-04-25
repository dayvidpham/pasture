package hooks_test

// git_recorder_test.go — Unit / integration tests for the GitRecorder
// HookHandler (SLICE-9, PROPOSAL-2 §11 Scenario 6 wire side).
//
// The system under test is GitRecorder + the wire to tasks.RecordGitEvent.
// Dependencies (TaskTracker, *sql.DB) are real (file-backed t.TempDir() per
// pasture/CLAUDE.md and IMPL_PLAN §1.2) — we do NOT mock the storage layer
// because Scenario 6's contract includes the SQL-level state.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Test fixtures ───────────────────────────────────────────────────────────

func openRecorderFixture(t *testing.T) (*hooks.GitRecorder, protocol.TaskTracker, *sql.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	t.Cleanup(func() {
		if err := tracker.Close(); err != nil {
			t.Errorf("tracker.Close: %v", err)
		}
	})

	auditDB, err := tasks.OpenAuditDBForFreeFloating(dbPath)
	if err != nil {
		t.Fatalf("OpenAuditDBForFreeFloating: %v", err)
	}
	t.Cleanup(func() {
		if err := auditDB.Close(); err != nil {
			t.Errorf("auditDB.Close: %v", err)
		}
	})

	gr, err := hooks.NewGitRecorder(tracker, auditDB)
	if err != nil {
		t.Fatalf("NewGitRecorder: %v", err)
	}
	return gr, tracker, auditDB, dbPath
}

// countContextEdges returns the count of context_edges rows for the (kind,
// contextID) pair via a fresh verification handle.
func countContextEdges(t *testing.T, dbPath string, kind protocol.ContextKind, contextID string) int {
	t.Helper()
	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open verify: %v", err)
	}
	defer verifyDB.Close()
	var n int
	if err := verifyDB.QueryRow(
		`SELECT COUNT(*) FROM context_edges WHERE context_kind = ? AND context_id = ?`,
		string(kind), contextID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// ─── Constructor validation ──────────────────────────────────────────────────

func TestNewGitRecorder_RejectsNilTracker(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "x.db")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	defer tracker.Close()
	auditDB, err := tasks.OpenAuditDBForFreeFloating(dbPath)
	if err != nil {
		t.Fatalf("OpenAuditDBForFreeFloating: %v", err)
	}
	defer auditDB.Close()

	if _, err := hooks.NewGitRecorder(nil, auditDB); err == nil {
		t.Fatal("NewGitRecorder(nil tracker) returned nil; want validation error")
	} else {
		requireValidationError(t, err)
	}
}

func TestNewGitRecorder_RejectsNilAuditDB(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "x.db")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	defer tracker.Close()

	if _, err := hooks.NewGitRecorder(tracker, nil); err == nil {
		t.Fatal("NewGitRecorder(nil auditDB) returned nil; want validation error")
	} else {
		requireValidationError(t, err)
	}
}

// ─── RecordCommit (direct path) ──────────────────────────────────────────────

// TestGitRecorder_RecordCommitDirect verifies the production-wiring entry
// point: cmd/pastured calls gr.RecordCommit(ctx, sha, payload) when a Claude
// Code Stop hook arrives. The SQL state matches Scenario 6.

func TestGitRecorder_RecordCommitDirect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	gr, tracker, _, dbPath := openRecorderFixture(t)

	const sha = "f00ba12345678901234567890abcdef0123456789"

	id, err := gr.RecordCommit(ctx, sha, map[string]any{"sha": sha, "branch": "feat-x"})
	if err != nil {
		t.Fatalf("RecordCommit: %v", err)
	}
	if id <= 0 {
		t.Fatalf("RecordCommit returned eventID %d, want > 0", id)
	}

	// SQL state: one ContextGit edge for this sha.
	if got := countContextEdges(t, dbPath, protocol.ContextGit, sha); got != 1 {
		t.Errorf("context_edges (GitContext, %q) count = %d, want 1", sha, got)
	}

	// Tracker.EventContexts agrees with the raw SQL (this read path does
	// NOT depend on the audit_events.role column that S3's WIP migration
	// is reshaping; the cross-slice Timeline read is exercised by S5's
	// own test suite and S6's CLI subprocess tests).
	contexts, err := tracker.EventContexts(ctx, id)
	if err != nil {
		t.Fatalf("EventContexts: %v", err)
	}
	if len(contexts) != 1 {
		t.Fatalf("EventContexts = %d, want 1", len(contexts))
	}
	if contexts[0].Kind != protocol.ContextGit || contexts[0].ContextID != sha {
		t.Errorf("EventContexts[0] = %v, want {ContextGit, %q}", contexts[0], sha)
	}
}

// ─── Handle (HookHandler interface path) ─────────────────────────────────────
//
// When a HookPayload arrives via hooks.Manager.Dispatch and Data["sha"] is
// populated, the GitRecorder's Handle method should record the same way
// RecordCommit does. When Data["sha"] is missing/empty, Handle is a no-op
// (returns nil, no row written).

func TestGitRecorder_Handle_RecordsWhenSHAPresent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	gr, _, _, dbPath := openRecorderFixture(t)

	const sha = "ab1234567890abcdef1234567890abcdef123456"

	payload := hooks.HookPayload{
		Event:   hooks.HookSliceCompleted,
		EpochID: "aura-plugins--01968a3c-1111-7000-8000-000000000123",
		Phase:   protocol.PhaseWorkerSlices,
		Data:    map[string]any{hooks.GitCommitDataKey: sha, "slice": "S9"},
	}

	if err := gr.Handle(ctx, payload); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := countContextEdges(t, dbPath, protocol.ContextGit, sha); got != 1 {
		t.Errorf("context_edges (GitContext, %q) count = %d, want 1", sha, got)
	}
}

func TestGitRecorder_Handle_NoOpWhenSHAAbsent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	gr, _, _, dbPath := openRecorderFixture(t)

	payload := hooks.HookPayload{
		Event:   hooks.HookSliceCompleted,
		EpochID: "epoch-x",
		Data:    map[string]any{"slice": "S9"}, // no "sha" key
	}
	if err := gr.Handle(ctx, payload); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// No context_edges row of any kind should exist for an empty fixture.
	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer verifyDB.Close()
	var n int
	if err := verifyDB.QueryRow(`SELECT COUNT(*) FROM context_edges`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("context_edges count = %d, want 0 (Handle should no-op when sha is absent)", n)
	}
}

func TestGitRecorder_Handle_NoOpWhenSHAEmptyString(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	gr, _, _, dbPath := openRecorderFixture(t)

	payload := hooks.HookPayload{
		Event: hooks.HookSliceCompleted,
		Data:  map[string]any{hooks.GitCommitDataKey: ""}, // empty value
	}
	if err := gr.Handle(ctx, payload); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := countContextEdges(t, dbPath, protocol.ContextGit, ""); got != 0 {
		t.Errorf("context_edges with empty context_id = %d, want 0", got)
	}
}

func TestGitRecorder_Handle_NoOpWhenSHAWrongType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	gr, _, _, dbPath := openRecorderFixture(t)

	payload := hooks.HookPayload{
		Event: hooks.HookSliceCompleted,
		Data:  map[string]any{hooks.GitCommitDataKey: 12345}, // wrong type
	}
	if err := gr.Handle(ctx, payload); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer verifyDB.Close()
	var n int
	if err := verifyDB.QueryRow(`SELECT COUNT(*) FROM context_edges`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("context_edges count = %d, want 0 (Handle should no-op when sha is non-string)", n)
	}
}

// ─── Events() / hooks.Manager dispatch wiring ────────────────────────────────
//
// The recorder subscribes to HookSliceCompleted by default. When registered
// with a hooks.Manager and the manager dispatches a matching payload, the
// recorder receives it via the Manager (not just via direct call).

func TestGitRecorder_DispatchesViaManager(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	gr, _, _, dbPath := openRecorderFixture(t)

	mgr := hooks.NewManager()
	mgr.Register(gr)

	const sha = "managerdispatch1234567890abcdef0123456789"

	payload := hooks.HookPayload{
		Event: hooks.HookSliceCompleted,
		Data:  map[string]any{hooks.GitCommitDataKey: sha},
	}
	if err := mgr.Dispatch(ctx, payload); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got := countContextEdges(t, dbPath, protocol.ContextGit, sha); got != 1 {
		t.Errorf("after Manager.Dispatch, context_edges (GitContext, %q) = %d, want 1", sha, got)
	}
}

// WithSubscribedEvents lets future S7 wiring redirect the recorder to a new
// event type without touching the recorder's internals. Verify the option
// replaces (not appends to) the default subscription.

func TestGitRecorder_WithSubscribedEvents_Replaces(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "x.db")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	defer tracker.Close()
	auditDB, err := tasks.OpenAuditDBForFreeFloating(dbPath)
	if err != nil {
		t.Fatalf("OpenAuditDBForFreeFloating: %v", err)
	}
	defer auditDB.Close()

	gr, err := hooks.NewGitRecorder(
		tracker, auditDB,
		hooks.WithSubscribedEvents(hooks.HookEpochCompleted),
	)
	if err != nil {
		t.Fatalf("NewGitRecorder: %v", err)
	}
	events := gr.Events()
	if len(events) != 1 || events[0] != hooks.HookEpochCompleted {
		t.Errorf("Events() = %v, want [HookEpochCompleted] (option should REPLACE default)", events)
	}
}

// ─── RegisterDefaultRecorders helper (cmd/pastured wiring) ──────────────────

func TestRegisterDefaultRecorders_HappyPath(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "x.db")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	defer tracker.Close()
	auditDB, err := tasks.OpenAuditDBForFreeFloating(dbPath)
	if err != nil {
		t.Fatalf("OpenAuditDBForFreeFloating: %v", err)
	}
	defer auditDB.Close()

	mgr := hooks.NewManager()
	gr, err := hooks.RegisterDefaultRecorders(mgr, tracker, auditDB)
	if err != nil {
		t.Fatalf("RegisterDefaultRecorders: %v", err)
	}
	if gr == nil {
		t.Fatal("RegisterDefaultRecorders returned nil GitRecorder")
	}

	// Dispatch a matching payload — the registered recorder should pick it up.
	const sha = "regdef1234567890abcdef1234567890abcdef12"
	if err := mgr.Dispatch(context.Background(), hooks.HookPayload{
		Event: hooks.HookSliceCompleted,
		Data:  map[string]any{hooks.GitCommitDataKey: sha},
	}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if got := countContextEdges(t, dbPath, protocol.ContextGit, sha); got != 1 {
		t.Errorf("after RegisterDefaultRecorders + Dispatch, context_edges (GitContext, %q) = %d, want 1", sha, got)
	}
}

func TestRegisterDefaultRecorders_RejectsNilManager(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "x.db")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	defer tracker.Close()
	auditDB, err := tasks.OpenAuditDBForFreeFloating(dbPath)
	if err != nil {
		t.Fatalf("OpenAuditDBForFreeFloating: %v", err)
	}
	defer auditDB.Close()

	if _, err := hooks.RegisterDefaultRecorders(nil, tracker, auditDB); err == nil {
		t.Fatal("RegisterDefaultRecorders(nil mgr) returned nil; want validation error")
	} else {
		requireValidationError(t, err)
	}
}

// ─── Helper ──────────────────────────────────────────────────────────────────

func requireValidationError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("error is not *StructuredError: %v", err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryValidation)
	}
}
