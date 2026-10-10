package tasks_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

// Initialize only the schema via the existing low-level constructors. This
// deliberately does not call a durable tracker opener or register identities.
func unregisteredDurableDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	trail, err := audit.NewSqliteAuditTrail(path)
	require.NoError(t, err)
	require.NoError(t, trail.Close())
	db, err := sql.Open("sqlite", dbconn.SharedDSNWithProfile(path, timeouts.TestProfile()))
	require.NoError(t, err)
	prov, err := provenance.OpenBorrowedSQLite(db)
	require.NoError(t, err)
	require.NoError(t, prov.Close())
	require.NoError(t, db.Close())
	return path
}

func assertBuiltInBindings(t *testing.T, path string, tracker protocol.TaskTracker) map[string]string {
	t.Helper()
	bindings := captureWellKnownMap(t, path)
	require.Len(t, bindings, len(tasks.WellKnownAgents()))
	for _, spec := range tasks.WellKnownAgents() {
		id := bindings[spec.Name]
		require.NotEmpty(t, id, spec.Name)
		parsed, err := provenance.ParseAgentID(id)
		require.NoError(t, err)
		agent, err := tracker.SoftwareAgent(parsed)
		require.NoError(t, err)
		require.Equal(t, spec.Name, agent.Name)
		automaton, pasture, err := tracker.AgentCategories(parsed)
		require.NoError(t, err)
		require.Equal(t, spec.Role, automaton, spec.Name)
		require.Equal(t, protocol.PastureRoleNone, pasture, spec.Name)
	}
	return bindings
}

func TestDurableOpenEnsuresBuiltInsAndWarmOpensPreserveIdentity(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pasture.db")
	start := time.Now()
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	defer tracker.Close()
	t.Logf("fresh durable open: %s", time.Since(start))
	bindings := assertBuiltInBindings(t, path, tracker)
	counts := captureRowCounts(t, path)
	require.NoError(t, tracker.Close())

	openers := map[string]func(string) (protocol.TaskTracker, error){
		"default": tasks.OpenTaskTracker,
		"options": func(path string) (protocol.TaskTracker, error) { return tasks.OpenTaskTrackerWithOptions(path) },
		"skip migrations": func(path string) (protocol.TaskTracker, error) {
			return tasks.OpenTaskTrackerWithOptions(path, tasks.WithSkipMigrations())
		},
		"public facade": protocol.OpenTaskTracker,
	}
	for name, open := range openers {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			tracker, err := open(path)
			require.NoError(t, err)
			defer tracker.Close()
			t.Logf("warm durable open: %s", time.Since(start))
			require.Equal(t, bindings, assertBuiltInBindings(t, path, tracker))
			cache := tasks.NewWellKnownAgentCache()
			require.NoError(t, tasks.RegisterWellKnownAgents(context.Background(), tracker, cache))
			for name, id := range bindings {
				cached, ok := cache.Get(name)
				require.True(t, ok)
				require.Equal(t, id, cached.String())
			}
			require.Equal(t, counts, captureRowCounts(t, path))
		})
	}
}

func TestDurableOpenFillsPartialRegistry(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	defer tracker.Close()
	bindings := assertBuiltInBindings(t, path, tracker)
	require.NoError(t, tracker.Close())
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	removed := tasks.WellKnownAgents()[len(tasks.WellKnownAgents())-1]
	_, err = db.Exec(`DELETE FROM pasture_agent_categories WHERE agent_id = ?`, bindings[removed.Name])
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM pasture_well_known_agents WHERE name = ?`, removed.Name)
	require.NoError(t, err)
	tracker, err = tasks.OpenTaskTrackerWithOptions(path, tasks.WithSkipMigrations())
	require.NoError(t, err)
	defer tracker.Close()
	filled := assertBuiltInBindings(t, path, tracker)
	for name, id := range bindings {
		if name != removed.Name {
			require.Equal(t, id, filled[name], name)
		}
	}
}

func TestIndependentDurableOpensRaceToCanonicalBindings(t *testing.T) {
	t.Parallel()
	path := unregisteredDurableDB(t)
	// Use the bounded contention profile already used by the activation race
	// in internal/lifecycle/receipt/definition_test.go. Race instrumentation and
	// shared-runner CPU load can hold a SQLite write beyond TestProfile's 500ms
	// lease window. This proof tests identity convergence, not production lock
	// deadlines; production and deadline regression profiles stay unchanged.
	profile, err := timeouts.New(timeouts.Test, 5*time.Second, 10*time.Second, 15*time.Second, 20*time.Second, 60*time.Second)
	require.NoError(t, err)
	start := make(chan struct{})
	var wg sync.WaitGroup
	trackers := make([]protocol.TaskTracker, 2)
	errs := make([]error, 2)
	for i := range trackers {
		wg.Go(func() {
			<-start
			trackers[i], errs[i] = tasks.OpenTaskTrackerWithOptions(path, tasks.WithSkipMigrations(), tasks.WithTimeoutProfile(profile))
		})
	}
	close(start)
	wg.Wait()
	for i, tracker := range trackers {
		if tracker != nil {
			t.Cleanup(func() { _ = tracker.Close() })
		}
		require.NoError(t, errs[i])
	}
	bindings := assertBuiltInBindings(t, path, trackers[0])
	require.Equal(t, bindings, assertBuiltInBindings(t, path, trackers[1]))
	counts := captureRowCounts(t, path)
	require.Equal(t, len(tasks.WellKnownAgents()), counts.PastureAgentCategories)
	t.Logf("racing durable opens software-agent count (losing mints permitted): %d", counts.AgentsSoftware)
	for _, tracker := range trackers {
		cache := tasks.NewWellKnownAgentCache()
		require.NoError(t, tasks.RegisterWellKnownAgents(context.Background(), tracker, cache))
		for name, id := range bindings {
			cached, ok := cache.Get(name)
			require.True(t, ok)
			require.Equal(t, id, cached.String())
		}
	}
	require.Equal(t, counts, captureRowCounts(t, path))
	reopened, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	defer reopened.Close()
	require.Equal(t, bindings, assertBuiltInBindings(t, path, reopened))
	require.Equal(t, counts, captureRowCounts(t, path))
}

func TestReadOnlyStatusDoesNotEnsureBuiltIns(t *testing.T) {
	t.Parallel()
	path := unregisteredDurableDB(t)
	for _, initialized := range []bool{false, true} {
		if initialized {
			tracker, err := tasks.OpenTaskTracker(path)
			require.NoError(t, err)
			require.NoError(t, tracker.Close())
		}
		counts := captureRowCounts(t, path)
		if !initialized {
			require.Zero(t, counts.PastureWellKnownAgents)
		}
		db, err := dbconn.OpenReadOnlyDB(path)
		require.NoError(t, err)
		require.NoError(t, tasks.CheckSchemaVersion(db, path))
		reader := tasks.NewStatusReaderFromDB(db)
		_, err = reader.QueryEvents(context.Background(), "absent", nil, nil)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.NoError(t, db.Close())
		require.Equal(t, counts, captureRowCounts(t, path))
	}
}

func TestCLIOnlyLegacyHookAuditUsesBoundAgent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	defer tracker.Close()
	const role = "pasture/automaton/hook/SessionStart"
	require.NoError(t, tracker.RecordEvent(context.Background(), protocol.AuditEvent{
		EpochId: "cli-only", Role: role, EventType: protocol.EventPhaseTransition, Timestamp: time.Now().UTC(),
	}))
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	var author, bound, category string
	require.NoError(t, db.QueryRow(`SELECT e.agent_id, w.agent_id, c.automaton_role
		FROM audit_events e JOIN pasture_well_known_agents w ON w.name = ?
		JOIN pasture_agent_categories c ON c.agent_id = e.agent_id
		JOIN context_edges x ON x.event_id = e.id WHERE x.context_id = ?`, role, "cli-only").Scan(&author, &bound, &category))
	require.Equal(t, bound, author)
	require.Equal(t, string(protocol.AutomatonRoleHookHandler), category)
	var shadows int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM agents_software WHERE name = ?`, "pasture/legacy-role/"+role).Scan(&shadows))
	require.Zero(t, shadows)
}
