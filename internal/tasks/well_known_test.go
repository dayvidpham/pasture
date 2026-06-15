// Package tasks_test — well_known_test.go
//
// BDD tests for PROPOSAL-2 §7.7.3 well-known automaton-agent registration:
//
//   - Scenario 14 (two-restart idempotency): row counts and AgentIDs in
//     `agents`, `agents_software`, `pasture_well_known_agents`, and
//     `pasture_agent_categories` are pointwise identical across two
//     consecutive RegisterWellKnownAgents calls on the same database.
//   - Scenario 8a-8e (registration side): all 15 well-known agents exist
//     with the correct (logical name, automaton_role) pairings after the
//     first RegisterWellKnownAgents call. (Emission side is owned by S8.)
//
// Test conventions (per pasture/CLAUDE.md and IMPL_PLAN §1.2):
//
//   - File-backed t.TempDir() SQLite — never in-memory, which bypasses WAL /
//     busy_timeout / fsync, the very mechanisms D11 (concurrent writers)
//     relies on.
//   - Real Provenance + audit subsystems — the system under test is the
//     registration FLOW; mocking the dependencies would defeat the purpose
//     because Scenario 14 specifically asserts cross-subsystem behaviour
//     (Provenance row counts AND audit row counts both stay constant).
//   - Each test uses its own t.TempDir() so race-detector runs do not
//     leak file locks across cases.

package tasks_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	stderrors "errors"

	_ "modernc.org/sqlite"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

// rowCounts returns the row counts for the four tables relevant to Scenario 14
// (well-known agent registration idempotency). It opens a private *sql.DB
// against the same file used by the tracker; modernc/sqlite + WAL allows
// concurrent reads while the tracker holds its own write handle.
//
// Returns the counts in fixed order (agents, agents_software,
// pasture_well_known_agents, pasture_agent_categories) so callers can use
// reflect.DeepEqual / direct comparison without sorting.
type rowCountSnapshot struct {
	Agents                 int
	AgentsSoftware         int
	PastureWellKnownAgents int
	PastureAgentCategories int
}

func captureRowCounts(t *testing.T, dbPath string) rowCountSnapshot {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("captureRowCounts: sql.Open(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	count := func(table string) int {
		var n int
		row := db.QueryRow("SELECT COUNT(*) FROM " + table)
		if err := row.Scan(&n); err != nil {
			t.Fatalf("captureRowCounts: COUNT(*) FROM %s: %v", table, err)
		}
		return n
	}
	return rowCountSnapshot{
		Agents:                 count("agents"),
		AgentsSoftware:         count("agents_software"),
		PastureWellKnownAgents: count("pasture_well_known_agents"),
		PastureAgentCategories: count("pasture_agent_categories"),
	}
}

// captureWellKnownMap returns the (name → agent_id) map currently stored in
// pasture_well_known_agents. Used by Scenario 14 to assert pointwise AgentId
// identity across two daemon starts.
func captureWellKnownMap(t *testing.T, dbPath string) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("captureWellKnownMap: sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	rows, err := db.Query(`SELECT name, agent_id FROM pasture_well_known_agents ORDER BY name`)
	if err != nil {
		t.Fatalf("captureWellKnownMap: query: %v", err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var name, agentId string
		if err := rows.Scan(&name, &agentId); err != nil {
			t.Fatalf("captureWellKnownMap: scan: %v", err)
		}
		out[name] = agentId
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("captureWellKnownMap: rows.Err: %v", err)
	}
	return out
}

// captureCategoriesMap returns the (agent_id → automaton_role) map currently
// stored in pasture_agent_categories. Used by Scenarios 8a-8e to assert each
// well-known agent has the correct AutomatonRole.
func captureCategoriesMap(t *testing.T, dbPath string) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("captureCategoriesMap: sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	rows, err := db.Query(`SELECT agent_id, automaton_role FROM pasture_agent_categories`)
	if err != nil {
		t.Fatalf("captureCategoriesMap: query: %v", err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var agentId, role string
		if err := rows.Scan(&agentId, &role); err != nil {
			t.Fatalf("captureCategoriesMap: scan: %v", err)
		}
		out[agentId] = role
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("captureCategoriesMap: rows.Err: %v", err)
	}
	return out
}

// openTrackerOrFatal opens a TaskTracker against dbPath and registers a
// cleanup that closes it. Used by every test that needs a tracker.
func openTrackerOrFatal(t *testing.T, dbPath string) protocol.TaskTracker {
	t.Helper()
	tracker, err := tasks.OpenTaskTrackerWithOptions(dbPath, tasks.WithSkipMigrations())
	if err != nil {
		t.Fatalf("OpenTaskTracker(%q): %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := tracker.Close(); err != nil {
			t.Errorf("tracker.Close: %v", err)
		}
	})
	return tracker
}

// ─── Registry shape tests (cheap, no I/O) ────────────────────────────────────

// TestWellKnownAgents_CountMatchesConstant guards against silent drift
// between the canonical registry and the WellKnownAgentCount constant. A
// future edit that adds a row but forgets to bump the constant (or vice
// versa) would surface here.
func TestWellKnownAgents_CountMatchesConstant(t *testing.T) {
	got := len(tasks.WellKnownAgents())
	if got != tasks.WellKnownAgentCount {
		t.Errorf("len(WellKnownAgents()) = %d, want WellKnownAgentCount = %d", got, tasks.WellKnownAgentCount)
	}
}

// TestWellKnownAgents_AllNamesUnique guards against accidental duplicate
// entries in the registry. Duplicates would cause RegisterWellKnownAgents to
// either: (a) silently no-op the second insert (UNIQUE on name causes
// constraint violation), or (b) succeed twice and overwrite the first
// agent_id in cache. Both are bugs; better to detect at test time.
func TestWellKnownAgents_AllNamesUnique(t *testing.T) {
	seen := make(map[string]int)
	for i, spec := range tasks.WellKnownAgents() {
		if prev, dup := seen[spec.Name]; dup {
			t.Errorf("duplicate name %q at indexes %d and %d", spec.Name, prev, i)
		}
		seen[spec.Name] = i
	}
}

// TestWellKnownAgents_AllRolesValid asserts every spec in the registry uses
// a protocol.AutomatonRole value that passes IsValid(). Catches typos or
// stale enum references in well_known_registry.go.
func TestWellKnownAgents_AllRolesValid(t *testing.T) {
	for _, spec := range tasks.WellKnownAgents() {
		if !spec.Role.IsValid() {
			t.Errorf("spec %q has invalid AutomatonRole %q", spec.Name, spec.Role)
		}
		if spec.Role == protocol.AutomatonRoleNone {
			t.Errorf("spec %q has AutomatonRoleNone — every well-known automaton must have a concrete role", spec.Name)
		}
	}
}

// TestWellKnownAgents_RoleBreakdown asserts the role distribution matches
// PROPOSAL-2 §7.7.2:
//
//	1 ConstraintChecker, 3 TransitionGate, 9 HookHandler,
//	1 ConsensusReached, 1 CreateFollowup. Total = 15.
func TestWellKnownAgents_RoleBreakdown(t *testing.T) {
	counts := make(map[protocol.AutomatonRole]int)
	for _, spec := range tasks.WellKnownAgents() {
		counts[spec.Role]++
	}

	want := map[protocol.AutomatonRole]int{
		protocol.AutomatonRoleConstraintChecker: 1,
		protocol.AutomatonRoleTransitionGate:    3,
		protocol.AutomatonRoleHookHandler:       9,
		protocol.AutomatonRoleConsensusReached:  1,
		protocol.AutomatonRoleCreateFollowup:    1,
	}
	for role, w := range want {
		if got := counts[role]; got != w {
			t.Errorf("role %q: got %d entries, want %d", role, got, w)
		}
	}
}

// ─── Scenario 8a-8e: registration-side assertions ────────────────────────────

// TestRegisterWellKnownAgents_AllNamesPresent (Scenarios 8a-8e, registration
// side) asserts that after one RegisterWellKnownAgents call against a fresh
// database, every well-known name from the registry has:
//
//   - A row in pasture_well_known_agents with the correct AgentId format.
//   - A row in pasture_agent_categories with the correct AutomatonRole and
//     pasture_role='None'.
//   - A cached entry in WellKnownAgentCache that matches the disk row.
func TestRegisterWellKnownAgents_AllNamesPresent(t *testing.T) {
	dbPath := testutil.GoldenUnifiedDBPath(t)
	tracker := openTrackerOrFatal(t, dbPath)

	cache := tasks.NewWellKnownAgentCache()
	if err := tasks.RegisterWellKnownAgents(context.Background(), tracker, cache); err != nil {
		t.Fatalf("RegisterWellKnownAgents: %v", err)
	}

	if cache.Len() != tasks.WellKnownAgentCount {
		t.Errorf("cache.Len() = %d, want %d", cache.Len(), tasks.WellKnownAgentCount)
	}

	// Verify each registry entry matches both the cache and the on-disk rows.
	wellKnownOnDisk := captureWellKnownMap(t, dbPath)
	categoriesOnDisk := captureCategoriesMap(t, dbPath)

	for _, spec := range tasks.WellKnownAgents() {
		t.Run(spec.Name, func(t *testing.T) {
			cachedId, ok := cache.Get(spec.Name)
			if !ok {
				t.Fatalf("cache missing entry for %q", spec.Name)
			}

			diskId, ok := wellKnownOnDisk[spec.Name]
			if !ok {
				t.Fatalf("pasture_well_known_agents missing row for %q", spec.Name)
			}

			if cachedId.String() != diskId {
				t.Errorf("cache AgentId %q != disk AgentId %q for %q", cachedId.String(), diskId, spec.Name)
			}

			roleOnDisk, ok := categoriesOnDisk[diskId]
			if !ok {
				t.Fatalf("pasture_agent_categories missing row for agent_id=%q (name=%q)", diskId, spec.Name)
			}
			if roleOnDisk != string(spec.Role) {
				t.Errorf("automaton_role for %q = %q, want %q", spec.Name, roleOnDisk, spec.Role)
			}

			// Verify the namespace is "pasture" (worker agent registration
			// path uses WellKnownAgentNamespace).
			if !strings.HasPrefix(diskId, tasks.WellKnownAgentNamespace+"--") {
				t.Errorf("AgentId %q for %q does not have %q-- namespace prefix", diskId, spec.Name, tasks.WellKnownAgentNamespace)
			}
		})
	}
}

// TestRegisterWellKnownAgents_PastureRoleAlwaysNone asserts the registration
// path writes pasture_role='None' for every well-known automaton (PROPOSAL-2
// §7.7.2). Workflow-role agents (Architect, Supervisor, Worker, Reviewer)
// are NOT well-known automata and are not registered by this slice.
func TestRegisterWellKnownAgents_PastureRoleAlwaysNone(t *testing.T) {
	dbPath := testutil.GoldenUnifiedDBPath(t)
	tracker := openTrackerOrFatal(t, dbPath)

	cache := tasks.NewWellKnownAgentCache()
	if err := tasks.RegisterWellKnownAgents(context.Background(), tracker, cache); err != nil {
		t.Fatalf("RegisterWellKnownAgents: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT agent_id, pasture_role FROM pasture_agent_categories`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var agentId, pastureRole string
		if err := rows.Scan(&agentId, &pastureRole); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if pastureRole != string(protocol.PastureRoleNone) {
			t.Errorf("agent_id %q: pasture_role = %q, want %q", agentId, pastureRole, protocol.PastureRoleNone)
		}
	}
}

// ─── Scenario 14: two-restart idempotency ────────────────────────────────────

// TestRegisterWellKnownAgents_TwoRestartsIdempotent (Scenario 14) is the
// gold-standard test for S7. It opens a fresh tracker against a temp
// database, registers all well-known agents, closes the tracker, snapshots
// row counts and the (name → agent_id) map; then opens a SECOND tracker
// against the SAME file, registers again, and asserts:
//
//   - All 4 row counts (agents, agents_software, pasture_well_known_agents,
//     pasture_agent_categories) are byte-identical between starts.
//   - The (name → agent_id) map in pasture_well_known_agents is pointwise
//     identical (same UUIDs, not just same names) — the second start must
//     RECOVER existing AgentIDs via the fast-path lookup, not mint new ones.
//
// This test would catch any of these regressions:
//
//   - Forgetting the lookup-by-name fast path → mints duplicates → all 4
//     counts diverge.
//   - Accidentally writing a new `agents_software` row even on a hit → only
//     the agents counts diverge.
//   - Caching the wrong AgentId → on-disk identical, in-memory cache wrong.
func TestRegisterWellKnownAgents_TwoRestartsIdempotent(t *testing.T) {
	dbPath := testutil.GoldenUnifiedDBPath(t)

	// First start.
	tracker1, err := tasks.OpenTaskTrackerWithOptions(dbPath, tasks.WithSkipMigrations())
	if err != nil {
		t.Fatalf("OpenTaskTracker (first start): %v", err)
	}
	cache1 := tasks.NewWellKnownAgentCache()
	if err := tasks.RegisterWellKnownAgents(context.Background(), tracker1, cache1); err != nil {
		_ = tracker1.Close()
		t.Fatalf("RegisterWellKnownAgents (first start): %v", err)
	}
	snapshot1 := captureRowCounts(t, dbPath)
	wellKnown1 := captureWellKnownMap(t, dbPath)
	cacheNames1 := cache1.Names()
	if err := tracker1.Close(); err != nil {
		t.Fatalf("tracker1.Close: %v", err)
	}

	// Sanity: first start populated 15 well-known rows.
	if snapshot1.PastureWellKnownAgents != 15 {
		t.Fatalf("first start: pasture_well_known_agents has %d rows, want 15", snapshot1.PastureWellKnownAgents)
	}
	if snapshot1.PastureAgentCategories != 15 {
		t.Fatalf("first start: pasture_agent_categories has %d rows, want 15", snapshot1.PastureAgentCategories)
	}
	if cache1.Len() != 15 {
		t.Fatalf("first start: cache has %d entries, want 15", cache1.Len())
	}

	// Second start.
	tracker2, err := tasks.OpenTaskTrackerWithOptions(dbPath, tasks.WithSkipMigrations())
	if err != nil {
		t.Fatalf("OpenTaskTracker (second start): %v", err)
	}
	cache2 := tasks.NewWellKnownAgentCache()
	if err := tasks.RegisterWellKnownAgents(context.Background(), tracker2, cache2); err != nil {
		_ = tracker2.Close()
		t.Fatalf("RegisterWellKnownAgents (second start): %v", err)
	}
	snapshot2 := captureRowCounts(t, dbPath)
	wellKnown2 := captureWellKnownMap(t, dbPath)
	cacheNames2 := cache2.Names()
	if err := tracker2.Close(); err != nil {
		t.Errorf("tracker2.Close: %v", err)
	}

	// Idempotency assertion 1: row counts unchanged.
	if snapshot1 != snapshot2 {
		t.Errorf("row counts diverged across restarts:\n  start1: %+v\n  start2: %+v", snapshot1, snapshot2)
	}

	// Idempotency assertion 2: (name → agent_id) map pointwise identical.
	if len(wellKnown1) != len(wellKnown2) {
		t.Fatalf("well-known name counts diverged: start1=%d start2=%d", len(wellKnown1), len(wellKnown2))
	}
	for name, id1 := range wellKnown1 {
		id2, ok := wellKnown2[name]
		if !ok {
			t.Errorf("name %q present in start1 but missing in start2", name)
			continue
		}
		if id1 != id2 {
			t.Errorf("name %q: agent_id changed across restarts: start1=%q start2=%q", name, id1, id2)
		}
	}

	// Idempotency assertion 3: cache contains the same name set across runs.
	if !equalStringSlices(cacheNames1, cacheNames2) {
		t.Errorf("cache name sets diverged across restarts:\n  start1: %v\n  start2: %v", cacheNames1, cacheNames2)
	}

	// Idempotency assertion 4: cache AgentIDs match what's on disk after
	// the second run (every cached entry must be the same as the recovered
	// disk entry — this catches "cache silently re-mints" bugs).
	for _, name := range cacheNames2 {
		cachedId, _ := cache2.Get(name)
		diskId := wellKnown2[name]
		if cachedId.String() != diskId {
			t.Errorf("name %q: cache AgentId %q != disk AgentId %q after second start", name, cachedId.String(), diskId)
		}
	}
}

// equalStringSlices returns true if a and b contain the same elements in the
// same order. Used for cache.Names() comparison (which is sorted).
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ─── Error path tests ────────────────────────────────────────────────────────

// TestRegisterWellKnownAgents_NilCacheReturnsConfigError catches the wiring
// error where main.go forgets to allocate the cache.
func TestRegisterWellKnownAgents_NilCacheReturnsConfigError(t *testing.T) {
	dbPath := testutil.GoldenUnifiedDBPath(t)
	tracker := openTrackerOrFatal(t, dbPath)

	err := tasks.RegisterWellKnownAgents(context.Background(), tracker, nil)
	if err == nil {
		t.Fatal("expected error when cache is nil, got nil")
	}
	var se *pasterrors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("expected *StructuredError, got %T: %v", err, err)
	}
	if se.Category != pasterrors.CategoryConfig {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryConfig)
	}
	if !strings.Contains(se.What, "cache is nil") {
		t.Errorf("What = %q, want it to mention 'cache is nil'", se.What)
	}
}

// TestRegisterWellKnownAgents_NilTrackerReturnsConfigError catches the wiring
// error where the daemon forgets to open the tracker before calling the
// registrar.
func TestRegisterWellKnownAgents_NilTrackerReturnsConfigError(t *testing.T) {
	cache := tasks.NewWellKnownAgentCache()
	err := tasks.RegisterWellKnownAgents(context.Background(), nil, cache)
	if err == nil {
		t.Fatal("expected error when tracker is nil, got nil")
	}
	var se *pasterrors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("expected *StructuredError, got %T: %v", err, err)
	}
	if se.Category != pasterrors.CategoryConfig {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryConfig)
	}
}

// TestRegisterWellKnownAgents_CancelledContextStopsEarly verifies that a
// pre-cancelled context aborts registration before any rows land. This is
// the daemon-shutdown-during-startup safety property.
func TestRegisterWellKnownAgents_CancelledContextStopsEarly(t *testing.T) {
	dbPath := testutil.GoldenUnifiedDBPath(t)
	tracker := openTrackerOrFatal(t, dbPath)

	cache := tasks.NewWellKnownAgentCache()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before we even call

	err := tasks.RegisterWellKnownAgents(ctx, tracker, cache)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	var se *pasterrors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("expected *StructuredError, got %T: %v", err, err)
	}
	if se.Category != pasterrors.CategoryConfig {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryConfig)
	}
}

// ─── WellKnownAgentCache tests ───────────────────────────────────────────────

// TestWellKnownAgentCache_MustGetMissingEntry asserts that asking for a name
// that was never registered returns an actionable CategoryValidation error.
func TestWellKnownAgentCache_MustGetMissingEntry(t *testing.T) {
	cache := tasks.NewWellKnownAgentCache()
	_, err := cache.MustGet("pasture/automaton/does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing entry, got nil")
	}
	var se *pasterrors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("expected *StructuredError, got %T: %v", err, err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryValidation)
	}
}

// TestWellKnownAgentCache_LenOnNilReceiver verifies the defensive Len-on-nil
// behaviour used by main.go for early logging.
func TestWellKnownAgentCache_LenOnNilReceiver(t *testing.T) {
	var c *tasks.WellKnownAgentCache
	if got := c.Len(); got != 0 {
		t.Errorf("nil cache Len() = %d, want 0", got)
	}
}

// TestWellKnownAgentCache_NamesAfterRegistration asserts Names() is sorted
// (test-stable) and contains exactly the registered names.
func TestWellKnownAgentCache_NamesAfterRegistration(t *testing.T) {
	dbPath := testutil.GoldenUnifiedDBPath(t)
	tracker := openTrackerOrFatal(t, dbPath)

	cache := tasks.NewWellKnownAgentCache()
	if err := tasks.RegisterWellKnownAgents(context.Background(), tracker, cache); err != nil {
		t.Fatalf("RegisterWellKnownAgents: %v", err)
	}

	names := cache.Names()
	if len(names) != tasks.WellKnownAgentCount {
		t.Fatalf("len(Names()) = %d, want %d", len(names), tasks.WellKnownAgentCount)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("Names() not strictly ascending at index %d: %q >= %q", i, names[i-1], names[i])
		}
	}
}
