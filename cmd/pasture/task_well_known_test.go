package main_test

import (
	"database/sql"
	"encoding/json"
	"sync"
	"testing"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/provadapter"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

func assertCLIBuiltIns(t *testing.T, path string) map[string]string {
	t.Helper()
	out := runCLI(t, "--db", path, "--format", "json", "task", "agents", "list")
	require.Zero(t, out.exitCode, out.stderr)
	var entries []struct {
		AgentID       string `json:"agentId"`
		Name          string `json:"name"`
		WellKnownName string `json:"wellKnownName"`
		AutomatonRole string `json:"automatonRole"`
		PastureRole   string `json:"pastureRole"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.stdout), &entries))
	byName := make(map[string]int)
	for i, entry := range entries {
		if entry.WellKnownName != "" {
			byName[entry.WellKnownName] = i
		}
	}
	require.Len(t, byName, len(tasks.WellKnownAgents()))
	db, err := dbconn.OpenReadOnlyDB(path)
	require.NoError(t, err)
	defer db.Close()
	bindings := make(map[string]string)
	for _, spec := range tasks.WellKnownAgents() {
		i, ok := byName[spec.Name]
		require.True(t, ok, spec.Name)
		entry := entries[i]
		require.Equal(t, spec.Name, entry.Name)
		require.Equal(t, string(spec.Role), entry.AutomatonRole)
		require.Equal(t, "None", entry.PastureRole)
		var id, role, pasture string
		require.NoError(t, db.QueryRow(`SELECT w.agent_id, c.automaton_role, c.pasture_role FROM pasture_well_known_agents w
			JOIN pasture_agent_categories c ON w.agent_id = c.agent_id WHERE w.name = ?`, spec.Name).Scan(&id, &role, &pasture))
		require.Equal(t, entry.AgentID, id)
		require.Equal(t, entry.AutomatonRole, role)
		require.Equal(t, entry.PastureRole, pasture)
		bindings[spec.Name] = id
	}
	foundSystem := false
	for _, entry := range entries {
		if entry.AgentID == provadapter.PastureSystemDefaultActorID().String() {
			require.Equal(t, provadapter.PastureSystemDefaultName, entry.Name)
			foundSystem = true
		}
	}
	require.True(t, foundSystem, "CLI construction must retain its system actor")
	return bindings
}

func TestCLIOnlyFirstTaskEnsuresPersistedWellKnownAgents(t *testing.T) {
	t.Parallel()
	path := absentDB(t)
	out := runCLI(t, "--db", path, "--format", "json", "task", "create", "CLI-only initialization", "--namespace", "cli-only")
	require.Zero(t, out.exitCode, out.stderr)
	var task struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.stdout), &task))
	require.NotEmpty(t, task.ID)
	bindings := assertCLIBuiltIns(t, path)
	require.Equal(t, bindings, assertCLIBuiltIns(t, path))
}

func TestCLIProcessesRacingDurableOpenConverge(t *testing.T) {
	t.Parallel()
	path := absentDB(t)
	trail, err := audit.NewSqliteAuditTrail(path)
	require.NoError(t, err)
	require.NoError(t, trail.Close())
	db, err := sql.Open("sqlite", dbconn.SharedDSN(path))
	require.NoError(t, err)
	prov, err := provenance.OpenBorrowedSQLite(db)
	require.NoError(t, err)
	require.NoError(t, prov.Close())
	require.NoError(t, db.Close())
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]runOutcome, 2)
	for i := range results {
		wg.Go(func() {
			<-start
			results[i] = runCLI(t, "--db", path, "--format", "json", "task", "create", "racing CLI", "--namespace", "cli-race")
		})
	}
	close(start)
	wg.Wait()
	for _, out := range results {
		require.Zero(t, out.exitCode, out.stderr)
	}
	bindings := assertCLIBuiltIns(t, path)
	db, err = dbconn.OpenReadOnlyDB(path)
	require.NoError(t, err)
	defer db.Close()
	var software, categories, canonical int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM agents_software`).Scan(&software))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pasture_agent_categories`).Scan(&categories))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pasture_well_known_agents`).Scan(&canonical))
	require.Equal(t, len(tasks.WellKnownAgents()), categories)
	require.Equal(t, len(tasks.WellKnownAgents()), canonical)
	t.Logf("cross-process software-agent count (losing mints permitted): %d", software)
	out := runCLI(t, "--db", path, "task", "create", "warm CLI", "--namespace", "cli-race")
	require.Zero(t, out.exitCode, out.stderr)
	require.Equal(t, bindings, assertCLIBuiltIns(t, path))
	var warmCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM agents_software`).Scan(&warmCount))
	require.Equal(t, software, warmCount)
}
