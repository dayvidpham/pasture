package tasks_test

// free_floating_test.go — Unit / integration tests for the SLICE-9 free-
// floating event recording helpers (RecordGitEvent / RecordSkillEvent /
// RecordSessionEvent).
//
// PROPOSAL-2 §11 Scenario 6 (free-floating git event recording, writer side)
// is the headline scenario; the reader-side CLI assertion lives in S6's
// subprocess CLI tests. Until S6 lands, these tests verify the same end state
// by querying context_edges directly via raw SQL.
//
// Per pasture/CLAUDE.md and IMPL_PLAN §1.2: file-backed `t.TempDir()` only —
// never in-memory SQLite (which bypasses WAL / busy_timeout / fsync, the very
// mechanisms D11 relies on).

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Test helpers ────────────────────────────────────────────────────────────

// openFreeFloatingFixture sets up a TaskTracker + auxiliary auditDB pair
// against a fresh temp file. Returns the tracker, the auditDB (caller must NOT
// close it — t.Cleanup handles it), and the resolved dbPath so tests can
// open a parallel verification handle if they want to check the SQL state
// without going through the tracker.
func openFreeFloatingFixture(t *testing.T) (protocol.TaskTracker, *sql.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker(%q) failed: %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := tracker.Close(); err != nil {
			t.Errorf("tracker.Close: %v", err)
		}
	})

	auditDB, err := tasks.OpenAuditDBForFreeFloating(dbPath)
	if err != nil {
		t.Fatalf("OpenAuditDBForFreeFloating(%q) failed: %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := auditDB.Close(); err != nil {
			t.Errorf("auditDB.Close: %v", err)
		}
	})

	return tracker, auditDB, dbPath
}

// queryContextEdges returns all context_edges rows for the (kind, contextId)
// pair. Used by tests that want to verify the writer-side end state without
// depending on the (not-yet-landed) S6 reader CLI.
func queryContextEdges(t *testing.T, dbPath string, kind protocol.ContextKind, contextId string) []contextEdgeRow {
	t.Helper()
	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open verification handle: %v", err)
	}
	defer verifyDB.Close()

	rows, err := verifyDB.Query(
		`SELECT event_id, context_kind, context_id FROM context_edges WHERE context_kind = ? AND context_id = ? ORDER BY event_id ASC`,
		string(kind), contextId,
	)
	if err != nil {
		t.Fatalf("verify SELECT context_edges: %v", err)
	}
	defer rows.Close()

	var out []contextEdgeRow
	for rows.Next() {
		var r contextEdgeRow
		if err := rows.Scan(&r.eventId, &r.kind, &r.contextId); err != nil {
			t.Fatalf("scan context_edges: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iter context_edges: %v", err)
	}
	return out
}

type contextEdgeRow struct {
	eventId   int64
	kind      string
	contextId string
}

// queryContextEdgesByEvent returns all context_edges rows for the given
// (eventId, kind) pair via a fresh verification handle. Used to assert
// non-existence of edges for a specific kind without depending on the
// tracker.EventContexts code path (which is independently tested by S5).
func queryContextEdgesByEvent(t *testing.T, dbPath string, eventId int64, kind protocol.ContextKind) []contextEdgeRow {
	t.Helper()
	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open verification handle: %v", err)
	}
	defer verifyDB.Close()

	rows, err := verifyDB.Query(
		`SELECT event_id, context_kind, context_id FROM context_edges WHERE event_id = ? AND context_kind = ? ORDER BY rowid ASC`,
		eventId, string(kind),
	)
	if err != nil {
		t.Fatalf("verify SELECT context_edges by event: %v", err)
	}
	defer rows.Close()

	var out []contextEdgeRow
	for rows.Next() {
		var r contextEdgeRow
		if err := rows.Scan(&r.eventId, &r.kind, &r.contextId); err != nil {
			t.Fatalf("scan context_edges by event: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// queryAuditEvent returns the audit_events row by id; t.Fatal if missing.
//
// Post-S4 (v4 schema): audit_events.epoch_id is gone; epoch attachment is
// recovered via context_edges with kind='EpochContext'. The LEFT JOIN
// keeps row.epochId empty when the event has no epoch attachment (the
// free-floating event case), preserving the assertion semantics this
// helper supports.
func queryAuditEvent(t *testing.T, dbPath string, eventId int64) auditEventRow {
	t.Helper()
	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open verification handle: %v", err)
	}
	defer verifyDB.Close()

	var r auditEventRow
	var epochId sql.NullString
	err = verifyDB.QueryRow(
		`SELECT ae.id, COALESCE(ce.context_id, ''), COALESCE(ae.phase,''), ae.event_type
		 FROM audit_events ae
		 LEFT JOIN context_edges ce
		   ON ce.event_id = ae.id AND ce.context_kind = 'EpochContext'
		 WHERE ae.id = ?`,
		eventId,
	).Scan(&r.id, &epochId, &r.phase, &r.eventType)
	if err != nil {
		t.Fatalf("verify SELECT audit_events id=%d: %v", eventId, err)
	}
	if epochId.Valid {
		r.epochId = epochId.String
	}
	return r
}

type auditEventRow struct {
	id        int64
	epochId   string
	phase     string
	eventType string
}

// ─── BDD Scenario 6: Free-floating git event recording (writer side) ─────────
//
// Given the unified system with no active EpochWorkflow,
// When a git commit hook fires through tasks.RecordGitEvent (which calls
//   tracker.RecordEvent + tracker.AttachContext under the hood),
// Then the audit_events row exists with event_type=GitCommit (no epoch_id
//   required), AND a context_edges row exists with kind=GitContext and
//   context_id=<sha>, AND no context_edges row of kind=EpochContext exists for
//   this event,
// Should not the event require an epoch_id column or fail because no epoch
//   is active.

func TestScenario6_FreeFloatingGitEventRecording(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tracker, auditDB, dbPath := openFreeFloatingFixture(t)

	const sha = "abc123def4567890abcdef1234567890abcdef12"

	// ─── When: fire a git commit "hook" (direct helper call) ───────────
	eventId, err := tasks.RecordGitEvent(
		ctx, tracker, auditDB, sha, tasks.EventGitCommit,
		map[string]any{"sha": sha, "branch": "feat--pasture--initial-golang-port"},
	)
	if err != nil {
		t.Fatalf("RecordGitEvent failed: %v", err)
	}
	if eventId <= 0 {
		t.Fatalf("RecordGitEvent returned non-positive eventId %d", eventId)
	}

	// ─── Then: audit_events row exists with eventType=GitCommit ────────
	row := queryAuditEvent(t, dbPath, eventId)
	if row.eventType != string(tasks.EventGitCommit) {
		t.Errorf("audit_events.event_type = %q, want %q", row.eventType, tasks.EventGitCommit)
	}
	// epoch_id should be empty (not NULL — NOT NULL constraint applies until S4 drops the column)
	if row.epochId != "" {
		t.Errorf("audit_events.epoch_id = %q, want \"\" (free-floating event)", row.epochId)
	}

	// ─── Then: context_edges row exists with (GitContext, sha) ─────────
	gitEdges := queryContextEdges(t, dbPath, protocol.ContextGit, sha)
	if len(gitEdges) != 1 {
		t.Fatalf("context_edges with (GitContext, %q) = %d rows, want 1", sha, len(gitEdges))
	}
	if gitEdges[0].eventId != eventId {
		t.Errorf("context_edges.event_id = %d, want %d", gitEdges[0].eventId, eventId)
	}

	// ─── Then: NO context_edges row of kind=EpochContext exists ────────
	// The slice spec calls this out explicitly: "assert no context_edges row
	// of kind=EpochContext exists for this event." We query the raw table
	// rather than tracker.EventContexts to keep the assertion robust against
	// other parallel slices (S3) that may rewrite SELECTed columns.
	epochEdges := queryContextEdgesByEvent(t, dbPath, eventId, protocol.ContextEpoch)
	if len(epochEdges) != 0 {
		t.Errorf("expected zero ContextEpoch edges for free-floating event %d, got %d", eventId, len(epochEdges))
	}

	// ─── Then: tracker.EventContexts agrees with the raw SQL. ───────────
	contexts, err := tracker.EventContexts(ctx, eventId)
	if err != nil {
		t.Fatalf("EventContexts failed: %v", err)
	}
	if len(contexts) != 1 {
		t.Errorf("EventContexts = %d, want 1 (only ContextGit)", len(contexts))
	}
	if len(contexts) >= 1 && contexts[0].Kind != protocol.ContextGit {
		t.Errorf("EventContexts[0].Kind = %q, want %q", contexts[0].Kind, protocol.ContextGit)
	}

	// Note: tracker.Timeline(GitContext, sha) — the read path that
	// `pasture task events --context-kind=GitContext --context-id=<sha>`
	// will route through once S6's reader CLI lands — is exercised by S5's
	// own test suite (TestScenario7_MultiContextAttachment etc.) and by the
	// S6 worker's CLI subprocess tests. We do NOT re-assert it here because
	// the Timeline SQL projection currently includes audit_events.role,
	// which a parallel S3 migration drops; depending on slice landing order
	// this test would fail for reasons unrelated to S9's writer-side work.
	// The writer-side contract (the audit_events row + the context_edges
	// row) is fully verified above via raw SQL — that is the contract S9
	// owns (per the slice scope: "writer side; reader CLI side in S6").
}

// ─── RecordSkillEvent: ContextSkill end-to-end ───────────────────────────────
//
// Free-floating skill invocation outside an epoch. Same contract as
// RecordGitEvent but with ContextSkill / EventSkillInvoked.

func TestRecordSkillEvent_RecordsContextSkillEdge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tracker, auditDB, dbPath := openFreeFloatingFixture(t)

	const skillRunId = "pasture:user-elicit-2026-04-25-run-001"

	eventId, err := tasks.RecordSkillEvent(
		ctx, tracker, auditDB, skillRunId, tasks.EventSkillInvoked,
		map[string]any{"skill": "pasture:user-elicit", "runId": skillRunId},
	)
	if err != nil {
		t.Fatalf("RecordSkillEvent failed: %v", err)
	}

	skillEdges := queryContextEdges(t, dbPath, protocol.ContextSkill, skillRunId)
	if len(skillEdges) != 1 {
		t.Fatalf("context_edges with (SkillContext, %q) = %d rows, want 1", skillRunId, len(skillEdges))
	}
	if skillEdges[0].eventId != eventId {
		t.Errorf("context_edges.event_id = %d, want %d", skillEdges[0].eventId, eventId)
	}

	// Tracker.EventContexts agrees with the raw SQL.
	contexts, err := tracker.EventContexts(ctx, eventId)
	if err != nil {
		t.Fatalf("EventContexts: %v", err)
	}
	if len(contexts) != 1 || contexts[0].Kind != protocol.ContextSkill {
		t.Errorf("EventContexts = %v, want [{Kind: SkillContext, ContextId: %q}]", contexts, skillRunId)
	}
}

// ─── RecordSessionEvent: ContextSession end-to-end ───────────────────────────

func TestRecordSessionEvent_RecordsContextSessionEdge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tracker, auditDB, dbPath := openFreeFloatingFixture(t)

	const sessionId = "session-2026-04-25-T10-00-00-claude-code-001"

	eventId, err := tasks.RecordSessionEvent(
		ctx, tracker, auditDB, sessionId, tasks.EventSessionRecorded,
		map[string]any{"sessionId": sessionId, "durationSec": 300},
	)
	if err != nil {
		t.Fatalf("RecordSessionEvent failed: %v", err)
	}

	sessionEdges := queryContextEdges(t, dbPath, protocol.ContextSession, sessionId)
	if len(sessionEdges) != 1 {
		t.Fatalf("context_edges with (SessionContext, %q) = %d rows, want 1", sessionId, len(sessionEdges))
	}
	if sessionEdges[0].eventId != eventId {
		t.Errorf("context_edges.event_id = %d, want %d", sessionEdges[0].eventId, eventId)
	}
}

// ─── Multi-context attachment piggyback (cross-ref Scenario 7) ───────────────
//
// A post-epoch git commit citing epoch X gets BOTH a ContextGit edge (from
// RecordGitEvent) AND a ContextEpoch edge (from a follow-up AttachContext
// call). The slice description explicitly notes this case. We verify the
// helper returns an event id usable for the follow-up attach.

func TestRecordGitEvent_PiggybackEpochContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tracker, auditDB, dbPath := openFreeFloatingFixture(t)

	const (
		sha     = "deadbeefcafebabe1234567890abcdef12345678"
		epochId = "aura-plugins--01968a3c-ffff-7000-8000-000000000099"
	)

	eventId, err := tasks.RecordGitEvent(
		ctx, tracker, auditDB, sha, tasks.EventGitCommit,
		map[string]any{"sha": sha},
	)
	if err != nil {
		t.Fatalf("RecordGitEvent: %v", err)
	}

	// Caller adds the second context.
	if err := tracker.AttachContext(ctx, eventId, protocol.ContextEpoch, epochId); err != nil {
		t.Fatalf("AttachContext(Epoch): %v", err)
	}

	// Both edges visible.
	contexts, err := tracker.EventContexts(ctx, eventId)
	if err != nil {
		t.Fatalf("EventContexts: %v", err)
	}
	if len(contexts) != 2 {
		t.Fatalf("EventContexts = %d, want 2 (Git + Epoch)", len(contexts))
	}
	gotKinds := map[protocol.ContextKind]bool{}
	for _, c := range contexts {
		gotKinds[c.Kind] = true
	}
	if !gotKinds[protocol.ContextGit] || !gotKinds[protocol.ContextEpoch] {
		t.Errorf("EventContexts kinds = %v, want both ContextGit and ContextEpoch", gotKinds)
	}

	// Both kinds visible at the SQL layer too (raw assertions don't depend
	// on the SELECT projection that S3's WIP migration is reshaping).
	gitRows := queryContextEdges(t, dbPath, protocol.ContextGit, sha)
	if len(gitRows) != 1 || gitRows[0].eventId != eventId {
		t.Errorf("context_edges (Git, %q) = %v, want one row with event_id=%d", sha, gitRows, eventId)
	}
	epochRows := queryContextEdges(t, dbPath, protocol.ContextEpoch, epochId)
	if len(epochRows) != 1 || epochRows[0].eventId != eventId {
		t.Errorf("context_edges (Epoch, %q) = %v, want one row with event_id=%d", epochId, epochRows, eventId)
	}
}

// ─── Validation: nil tracker / nil auditDB / empty contextId / empty event ──

func TestRecordGitEvent_RejectsNilTracker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, auditDB, _ := openFreeFloatingFixture(t)

	_, err := tasks.RecordGitEvent(ctx, nil, auditDB, "abc", tasks.EventGitCommit, nil)
	requireValidationError(t, err)
}

// TestRecordGitEvent_AcceptsNilAuditDB verifies that passing nil for the
// auditDB parameter does not cause a validation error — the parameter is
// retained only for API compatibility and is no longer used by the
// implementation (tracker.RecordEventReturningId bundles write + id recovery).
func TestRecordGitEvent_AcceptsNilAuditDB(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, _, dbPath := openFreeFloatingFixture(t)

	const sha = "nilauditdb1234567890abcdef1234567890abcd"
	eventId, err := tasks.RecordGitEvent(ctx, tracker, nil, sha, tasks.EventGitCommit, nil)
	if err != nil {
		t.Fatalf("RecordGitEvent with nil auditDB returned unexpected error: %v", err)
	}
	if eventId <= 0 {
		t.Fatalf("RecordGitEvent with nil auditDB returned non-positive eventId %d", eventId)
	}

	// The event and context edge should still be persisted correctly.
	gitEdges := queryContextEdges(t, dbPath, protocol.ContextGit, sha)
	if len(gitEdges) != 1 || gitEdges[0].eventId != eventId {
		t.Errorf("context_edges (GitContext, %q) = %v, want one row with event_id=%d", sha, gitEdges, eventId)
	}
}

func TestRecordGitEvent_RejectsEmptySHA(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, auditDB, _ := openFreeFloatingFixture(t)

	_, err := tasks.RecordGitEvent(ctx, tracker, auditDB, "", tasks.EventGitCommit, nil)
	requireValidationError(t, err)
}

func TestRecordGitEvent_RejectsEmptyEventType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, auditDB, _ := openFreeFloatingFixture(t)

	_, err := tasks.RecordGitEvent(ctx, tracker, auditDB, "abc", "", nil)
	requireValidationError(t, err)
}

func TestRecordSkillEvent_RejectsEmptyRunId(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, auditDB, _ := openFreeFloatingFixture(t)

	_, err := tasks.RecordSkillEvent(ctx, tracker, auditDB, "", tasks.EventSkillInvoked, nil)
	requireValidationError(t, err)
}

func TestRecordSessionEvent_RejectsEmptySessionId(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, auditDB, _ := openFreeFloatingFixture(t)

	_, err := tasks.RecordSessionEvent(ctx, tracker, auditDB, "", tasks.EventSessionRecorded, nil)
	requireValidationError(t, err)
}

// ─── Concurrent recording: per-statement LastInsertId is race-safe ──────────
//
// A concurrent burst of N goroutines must produce exactly N rows in
// audit_events and N rows in context_edges without errors AND each goroutine
// must receive a UNIQUE event id. The unique-ID-per-goroutine invariant is
// the property that distinguishes a correct context_edges attachment (the
// edge for goroutine A is attached to A's audit event) from the broken
// SELECT MAX(id) workaround (the edge could be attached to a different
// goroutine's higher-id row, silently misattributing context).
//
// History: Phase 10 W3's free_floating.go fix had to drop this assertion
// because tracker.RecordEventReturningId recovered the id via SELECT MAX(id)
// after the INSERT, and concurrent writes really did return duplicate ids
// to two callers (D11-bounded race, observable under -race in CI). Phase 11
// R1-B (commit cf6c1a9) extended audit.Trail with RecordEventReturningId
// that uses sql.Result.LastInsertId from the SAME INSERT statement, so the
// returned id is now race-safe under any concurrency level. The
// per-statement LastInsertId guarantee is regression-tested at the storage
// boundary by TestSqliteAuditTrail_RecordEventReturningId_ConcurrentUnique
// (sqlite_test.go) and TestInMemoryAuditTrail_RecordEventReturningId_ConcurrentUnique
// (memory_test.go); this test re-verifies the same property surfaces
// through the public tasks.RecordGitEvent boundary.
//
// What we assert:
//  1. No errors occurred (all N writes+attaches succeeded).
//  2. Exactly N audit_events rows and N context_edges rows exist on disk.
//  3. Every returned id is a positive, distinct event id that exists in
//     audit_events. Distinctness is the property that guarantees each
//     goroutine's AttachContext call landed on its OWN event row (not a
//     peer's row that happened to commit at a higher id).

func TestRecordGitEvent_ConcurrentBurst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, auditDB, dbPath := openFreeFloatingFixture(t)

	const N = 16
	shas := make([]string, N)
	for i := range shas {
		shas[i] = "concurrent-sha-" + string(rune('a'+i)) + "0123456789abcdef0123456789abcdef"
	}

	var wg sync.WaitGroup
	errCh := make(chan error, N)
	idCh := make(chan int64, N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(sha string) {
			defer wg.Done()
			id, err := tasks.RecordGitEvent(ctx, tracker, auditDB, sha, tasks.EventGitCommit, map[string]any{"sha": sha})
			if err != nil {
				errCh <- err
				return
			}
			idCh <- id
		}(shas[i])
	}
	wg.Wait()
	close(errCh)
	close(idCh)

	for err := range errCh {
		t.Errorf("concurrent RecordGitEvent: %v", err)
	}

	// Drain the id channel into a slice so we can iterate it for both the
	// uniqueness check and the on-disk existence check below.
	ids := make([]int64, 0, N)
	for id := range idCh {
		ids = append(ids, id)
	}

	// (1) Per-call uniqueness — every goroutine must have received a distinct
	// event_id. This is the Phase 11 R1-C re-tightened assertion: pre-R1-B
	// (when tracker.RecordEventReturningId used SELECT MAX(id) under the hood)
	// this would intermittently fail because two goroutines could observe the
	// same MAX(id) between their INSERT and SELECT. Post-R1-B (cf6c1a9) the
	// per-statement sql.Result.LastInsertId guarantee makes this race-free.
	//
	// If this assertion fails, the per-statement LastInsertId guarantee from
	// R1-B has regressed — first check audit.SqliteAuditTrail.RecordEventReturningId
	// (sqlite.go) and its regression test
	// TestSqliteAuditTrail_RecordEventReturningId_ConcurrentUnique.
	seen := make(map[int64]int, N)
	for _, id := range ids {
		seen[id]++
	}
	for id, count := range seen {
		if count > 1 {
			t.Errorf("RecordGitEvent returned duplicate event_id %d to %d concurrent callers; "+
				"the per-statement LastInsertId guarantee from Phase 11 R1-B (commit cf6c1a9) appears broken — "+
				"re-check audit.SqliteAuditTrail.RecordEventReturningId and its regression test "+
				"TestSqliteAuditTrail_RecordEventReturningId_ConcurrentUnique", id, count)
		}
	}
	if len(seen) != len(ids) {
		t.Errorf("expected %d distinct event_ids from %d concurrent RecordGitEvent calls, got %d distinct",
			len(ids), len(ids), len(seen))
	}

	// (2) Verify the on-disk state agrees: N rows in audit_events, N rows in
	// context_edges with kind=GitContext.
	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open verification handle: %v", err)
	}
	defer verifyDB.Close()

	var auditCount, edgeCount int
	if err := verifyDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE event_type = ?`, string(tasks.EventGitCommit)).Scan(&auditCount); err != nil {
		t.Fatalf("count audit_events: %v", err)
	}
	if auditCount != N {
		t.Errorf("audit_events count = %d, want %d", auditCount, N)
	}
	if err := verifyDB.QueryRow(`SELECT COUNT(*) FROM context_edges WHERE context_kind = ?`, string(protocol.ContextGit)).Scan(&edgeCount); err != nil {
		t.Fatalf("count context_edges: %v", err)
	}
	if edgeCount != N {
		t.Errorf("context_edges (GitContext) count = %d, want %d", edgeCount, N)
	}

	// (3) Every returned id must be a valid positive event id that exists in audit_events.
	for _, id := range ids {
		if id <= 0 {
			t.Errorf("RecordGitEvent returned non-positive eventId %d", id)
			continue
		}
		var exists int
		if err := verifyDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE id = ?`, id).Scan(&exists); err != nil {
			t.Fatalf("verify event id %d exists: %v", id, err)
		}
		if exists == 0 {
			t.Errorf("returned eventId %d does not exist in audit_events", id)
		}
	}
}

// ─── OpenAuditDBForFreeFloating: empty path resolves to default ──────────────

func TestOpenAuditDBForFreeFloating_OpensFile(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "custom-pasture.db")

	// Pre-create the file via a TaskTracker open so the schema is in place.
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

	// The handle should be usable: a SELECT 1 should succeed.
	var got int
	if err := auditDB.QueryRow(`SELECT 1`).Scan(&got); err != nil {
		t.Fatalf("auditDB SELECT 1: %v", err)
	}
	if got != 1 {
		t.Errorf("SELECT 1 returned %d, want 1", got)
	}
}

// ─── Helper: assert a validation-category StructuredError ────────────────────

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
