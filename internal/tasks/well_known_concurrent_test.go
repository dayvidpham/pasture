package tasks

// well_known_concurrent_test.go — built-in agent registration when two
// processes register against one file at the same moment.
//
// Both processes miss the fast-path lookup, both mint an agent, and both try
// to bind their own id to the same UNIQUE name. The write lock orders them;
// the second must adopt the first's id and write nothing. The first test
// drives that collision deterministically, by binding two minted ids to one
// name in the order the lock imposes. The second runs two real trackers
// concurrently on one file and holds the outcome to one row per name and one
// id per name in both caches.

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"
)

// openFreshTracker opens the production tracker on a new file under t.TempDir
// and returns it with its audit handle. The file is migrated by the open, as
// a first daemon start migrates it.
func openFreshTracker(t *testing.T, dbPath string) (*trackerImpl, *sql.DB) {
	t.Helper()
	tracker, err := OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker(%q): %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := tracker.Close(); err != nil {
			t.Errorf("tracker.Close: %v", err)
		}
	})
	impl, ok := tracker.(*trackerImpl)
	if !ok {
		t.Fatalf("OpenTaskTracker returned %T, want *trackerImpl", tracker)
	}
	return impl, impl.auditDBHandle()
}

func mintedId() provenance.AgentID {
	return provenance.AgentID{Namespace: WellKnownAgentNamespace, UUID: uuid.Must(uuid.NewV7())}
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// The second binding of a name must not fail, must return the id the first
// binding wrote, and must leave one mapping row and one role row — the first
// one's.
//
// MUTATION: restore the bare INSERT in bindWellKnownAgent (drop the ON
// CONFLICT clause). The second call then fails with "UNIQUE constraint
// failed: pasture_well_known_agents.name" and this test fails on it.
func TestBindingASecondMintedIdToABoundNameKeepsTheFirstAndWritesNoSecondRoleRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, auditDB := openFreshTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	spec := WellKnownAgents()[0]
	first, second := mintedId(), mintedId()

	got, err := bindWellKnownAgent(ctx, auditDB, first, spec)
	if err != nil {
		t.Fatalf("first binding of %q: %v", spec.Name, err)
	}
	if got != first {
		t.Fatalf("first binding returned %s, want the minted id %s", got, first)
	}

	got, err = bindWellKnownAgent(ctx, auditDB, second, spec)
	if err != nil {
		t.Fatalf("the second binding of %q, as the process that lost the write lock makes it, failed: %v", spec.Name, err)
	}
	if got != first {
		t.Errorf("the second binding returned %s, want the id the first binding wrote, %s", got, first)
	}

	if n := countRows(t, auditDB, `SELECT COUNT(*) FROM pasture_well_known_agents WHERE name = ?`, spec.Name); n != 1 {
		t.Errorf("pasture_well_known_agents rows for %q = %d, want 1", spec.Name, n)
	}
	var bound string
	if err := auditDB.QueryRow(`SELECT agent_id FROM pasture_well_known_agents WHERE name = ?`, spec.Name).Scan(&bound); err != nil {
		t.Fatalf("read the bound id: %v", err)
	}
	if bound != first.String() {
		t.Errorf("the name is bound to %s, want the first id %s", bound, first)
	}
	if n := countRows(t, auditDB, `SELECT COUNT(*) FROM pasture_agent_categories WHERE agent_id = ?`, first.String()); n != 1 {
		t.Errorf("role rows for the first id = %d, want 1", n)
	}
	if n := countRows(t, auditDB, `SELECT COUNT(*) FROM pasture_agent_categories WHERE agent_id = ?`, second.String()); n != 0 {
		t.Errorf("role rows for the second id = %d, want 0: the loser must write no role row", n)
	}
}

// Two trackers on one file register every built-in agent at the same moment.
// Both must succeed, the file must hold exactly one mapping row and one role
// row per name, and both caches must hold the same id for every name.
func TestTwoProcessesRegisteringTheBuiltInAgentsAtOnceAgreeOnEveryId(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	trackerA, auditDB := openFreshTracker(t, dbPath)
	trackerB, _ := openFreshTracker(t, dbPath)
	cacheA, cacheB := NewWellKnownAgentCache(), NewWellKnownAgentCache()

	start := make(chan struct{})
	var wg sync.WaitGroup
	var errA, errB error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errA = RegisterWellKnownAgents(context.Background(), trackerA, cacheA)
	}()
	go func() {
		defer wg.Done()
		<-start
		errB = RegisterWellKnownAgents(context.Background(), trackerB, cacheB)
	}()
	close(start)
	wg.Wait()
	if errA != nil {
		t.Fatalf("registration A failed while B registered the same names: %v", errA)
	}
	if errB != nil {
		t.Fatalf("registration B failed while A registered the same names: %v", errB)
	}

	if n := countRows(t, auditDB, `SELECT COUNT(*) FROM pasture_well_known_agents`); n != WellKnownAgentCount {
		t.Errorf("pasture_well_known_agents rows = %d, want %d (one per name)", n, WellKnownAgentCount)
	}
	if n := countRows(t, auditDB, `SELECT COUNT(DISTINCT name) FROM pasture_well_known_agents`); n != WellKnownAgentCount {
		t.Errorf("distinct names = %d, want %d", n, WellKnownAgentCount)
	}
	if n := countRows(t, auditDB,
		`SELECT COUNT(*) FROM pasture_agent_categories c JOIN pasture_well_known_agents w ON w.agent_id = c.agent_id`,
	); n != WellKnownAgentCount {
		t.Errorf("role rows bound to a well-known name = %d, want %d", n, WellKnownAgentCount)
	}
	for _, spec := range WellKnownAgents() {
		idA, err := cacheA.MustGet(spec.Name)
		if err != nil {
			t.Fatalf("cache A has no entry for %q: %v", spec.Name, err)
		}
		idB, err := cacheB.MustGet(spec.Name)
		if err != nil {
			t.Fatalf("cache B has no entry for %q: %v", spec.Name, err)
		}
		if idA != idB {
			t.Errorf("%q: cache A holds %s and cache B holds %s; both processes must agree on the id", spec.Name, idA, idB)
		}
		var bound string
		if err := auditDB.QueryRow(`SELECT agent_id FROM pasture_well_known_agents WHERE name = ?`, spec.Name).Scan(&bound); err != nil {
			t.Fatalf("read the bound id for %q: %v", spec.Name, err)
		}
		if bound != idA.String() {
			t.Errorf("%q: the file binds %s but the caches hold %s", spec.Name, bound, idA)
		}
	}
}
