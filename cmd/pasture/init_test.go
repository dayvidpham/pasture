package main_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
	_ "modernc.org/sqlite"
)

func TestCLI_InitCreatesCompleteDatabaseAndIsIdempotent(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "nested", "pasture.db")
	first := runCLI(t, "--db", dbPath, "--format", "json", "init")
	if first.exitCode != 0 {
		t.Fatalf("first init exit %d; stdout=%s stderr=%s", first.exitCode, first.stdout, first.stderr)
	}

	var result struct {
		DBPath        string `json:"dbPath"`
		BuiltInAgents int    `json:"builtInAgents"`
	}
	if err := json.Unmarshal([]byte(first.stdout), &result); err != nil {
		t.Fatalf("decode init result: %v\nbody: %s", err, first.stdout)
	}
	if result.DBPath != dbPath || result.BuiltInAgents != tasks.WellKnownAgentCount {
		t.Fatalf("init result = %+v, want path %q and %d built-in agents", result, dbPath, tasks.WellKnownAgentCount)
	}

	firstState := readInitializationState(t, dbPath)
	for table, count := range firstState.Counts {
		if count != tasks.WellKnownAgentCount {
			t.Errorf("%s rows after first init = %d, want %d", table, count, tasks.WellKnownAgentCount)
		}
	}
	assertCanonicalAgents(t, firstState.Agents)
	if firstState.SchemaVersion != audit.MaxKnownSchemaVersion {
		t.Errorf("schema version after init = %d, want %d", firstState.SchemaVersion, audit.MaxKnownSchemaVersion)
	}
	for table, count := range firstState.RuntimeCounts {
		if count != 0 {
			t.Errorf("%s rows after init = %d, want 0; init must not launch or run the durable executor", table, count)
		}
	}

	second := runCLI(t, "--db", dbPath, "init")
	if second.exitCode != 0 {
		t.Fatalf("second init exit %d; stdout=%s stderr=%s", second.exitCode, second.stdout, second.stderr)
	}
	if !strings.Contains(second.stdout, dbPath) {
		t.Errorf("second init output does not identify %q: %s", dbPath, second.stdout)
	}
	secondState := readInitializationState(t, dbPath)
	for table, firstCount := range firstState.Counts {
		if secondState.Counts[table] != firstCount {
			t.Errorf("%s rows changed across repeated init: first=%d second=%d", table, firstCount, secondState.Counts[table])
		}
	}
	if !maps.Equal(firstState.Agents, secondState.Agents) {
		t.Errorf("built-in agent mappings changed across repeated init:\nfirst:  %+v\nsecond: %+v", firstState.Agents, secondState.Agents)
	}

	status := runCLI(t, "--db", dbPath, "--format", "json", "status")
	if status.exitCode != 0 {
		t.Fatalf("status after init exit %d; stdout=%s stderr=%s", status.exitCode, status.stdout, status.stderr)
	}
	if strings.TrimSpace(status.stdout) != "[]" {
		t.Fatalf("status after init = %q, want empty epoch list", status.stdout)
	}
}

func TestCLI_StatusMissingDatabaseRecommendsInitWithoutCreatingFile(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	status := runCLI(t, "--db", dbPath, "status")
	if status.exitCode != 2 {
		t.Fatalf("status exit = %d, want 2; stdout=%s stderr=%s", status.exitCode, status.stdout, status.stderr)
	}
	expectedInit := `pasture --db "` + dbPath + `" init`
	if !strings.Contains(status.stderr, expectedInit) {
		t.Errorf("missing-database remediation does not preserve the requested path with %q:\n%s", expectedInit, status.stderr)
	}
	if strings.Contains(status.stderr, "pastured") {
		t.Errorf("missing-database remediation still delegates initialization to pastured:\n%s", status.stderr)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("read-only status created %q: stat error=%v", dbPath, err)
	}
}

func TestCLI_InitUsesEnvironmentDatabasePath(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "from-env", "pasture.db")
	env := map[string]string{tasks.DBPathEnv: dbPath}
	initialized := runCLIWithEnvironment(t, env, "--format", "json", "init")
	if initialized.exitCode != 0 {
		t.Fatalf("environment init exit %d; stdout=%s stderr=%s", initialized.exitCode, initialized.stdout, initialized.stderr)
	}
	var result struct {
		DBPath string `json:"dbPath"`
	}
	if err := json.Unmarshal([]byte(initialized.stdout), &result); err != nil {
		t.Fatalf("decode environment init: %v\nbody: %s", err, initialized.stdout)
	}
	if result.DBPath != dbPath {
		t.Fatalf("environment init path = %q, want %q", result.DBPath, dbPath)
	}
	status := runCLIWithEnvironment(t, env, "--format", "json", "status")
	if status.exitCode != 0 || strings.TrimSpace(status.stdout) != "[]" {
		t.Fatalf("environment status exit=%d stdout=%s stderr=%s", status.exitCode, status.stdout, status.stderr)
	}
}

func TestCLI_InitAndStatusUseDefaultDatabasePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	xdgDataHome := filepath.Join(root, "xdg")
	expectedPath := filepath.Join(xdgDataHome, "pasture", tasks.DefaultDBFilename)
	env := map[string]string{
		tasks.DBPathEnv: "",
		"HOME":          filepath.Join(root, "home"),
		"XDG_DATA_HOME": xdgDataHome,
	}

	initialized := runCLIWithEnvironment(t, env, "--format", "json", "init")
	if initialized.exitCode != 0 {
		t.Fatalf("default-path init exit %d; stdout=%s stderr=%s", initialized.exitCode, initialized.stdout, initialized.stderr)
	}
	var result struct {
		DBPath string `json:"dbPath"`
	}
	if err := json.Unmarshal([]byte(initialized.stdout), &result); err != nil {
		t.Fatalf("decode default-path init: %v\nbody: %s", err, initialized.stdout)
	}
	if result.DBPath != expectedPath {
		t.Fatalf("default init path = %q, want %q", result.DBPath, expectedPath)
	}
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("stat default database %q: %v", expectedPath, err)
	}

	status := runCLIWithEnvironment(t, env, "--format", "json", "status")
	if status.exitCode != 0 || strings.TrimSpace(status.stdout) != "[]" {
		t.Fatalf("default-path status exit=%d stdout=%s stderr=%s", status.exitCode, status.stdout, status.stderr)
	}
}

func TestCLI_InitRejectsDatabaseBelowRegularFile(t *testing.T) {
	t.Parallel()

	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("occupied"), 0o600); err != nil {
		t.Fatalf("write blocking parent: %v", err)
	}
	dbPath := filepath.Join(parent, "pasture.db")
	initialized := runCLI(t, "--db", dbPath, "init")
	if initialized.exitCode == 0 {
		t.Fatalf("init unexpectedly succeeded; stdout=%s stderr=%s", initialized.stdout, initialized.stderr)
	}
	if initialized.stdout != "" || !strings.Contains(initialized.stderr, "database") || !strings.Contains(initialized.stderr, "directory") {
		t.Fatalf("init failure was not actionable; stdout=%s stderr=%s", initialized.stdout, initialized.stderr)
	}
}

func TestCLI_InitConcurrentFreshDatabaseConverges(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "concurrent", "pasture.db")
	type invocation struct {
		cmd            *exec.Cmd
		stdout, stderr bytes.Buffer
	}
	invocations := make([]invocation, 2)
	for i := range invocations {
		invocations[i].cmd = exec.Command(binaryPath, "--db", dbPath, "init")
		invocations[i].cmd.Stdout = &invocations[i].stdout
		invocations[i].cmd.Stderr = &invocations[i].stderr
		if err := invocations[i].cmd.Start(); err != nil {
			t.Fatalf("start concurrent init %d: %v", i, err)
		}
	}
	for i := range invocations {
		if err := invocations[i].cmd.Wait(); err != nil {
			t.Errorf("concurrent init %d failed: %v\nstdout=%s\nstderr=%s", i, err, invocations[i].stdout.String(), invocations[i].stderr.String())
		}
	}

	state := readInitializationState(t, dbPath)
	assertCanonicalAgents(t, state.Agents)
	if state.Counts["pasture_well_known_agents"] != tasks.WellKnownAgentCount || state.Counts["pasture_agent_categories"] != tasks.WellKnownAgentCount {
		t.Fatalf("concurrent init did not converge to %d mapped agents: %+v", tasks.WellKnownAgentCount, state.Counts)
	}
}

type builtInAgentState struct {
	ID            string
	AutomatonRole string
	PastureRole   string
}

type initializationState struct {
	Counts        map[string]int
	RuntimeCounts map[string]int
	Agents        map[string]builtInAgentState
	SchemaVersion int
}

var initializedSchemaTables = []string{
	"activities",
	"actor_namespace_claims",
	"agent_kinds",
	"agents",
	"agents_human",
	"agents_ml",
	"agents_software",
	"application_versions",
	"assignment_slots",
	"assignment_transitions",
	"audit_events",
	"audit_schema_meta",
	"authority_kinds",
	"comments",
	"context_edges",
	"dbos_migrations",
	"edge_kinds",
	"edges",
	"epoch_state_projection",
	"event_dispatch_kv",
	"fixed_actor_manifest_entries",
	"governed_allocation_genesis",
	"governed_allocation_operations",
	"governed_composed_supplement_owners",
	"governed_operation_effect_rows",
	"journal",
	"journal_activity_creations",
	"journal_authorities",
	"journal_authority_assignment_episodes",
	"journal_authority_assignment_transitions",
	"journal_authority_bootstraps",
	"journal_decision_contexts",
	"journal_decisions",
	"journal_evidence",
	"journal_evidence_contexts",
	"journal_kinds",
	"journal_operation_result_slots",
	"journal_operations",
	"journal_task_event_contexts",
	"journal_task_events",
	"labels",
	"lifecycle_occurrence_bindings",
	"lifecycle_occurrences",
	"lifecycle_payload_blobs",
	"ml_models",
	"notifications",
	"operation_outputs",
	"pasture_agent_categories",
	"pasture_governed_allocation_audit",
	"pasture_system_identity",
	"pasture_well_known_agents",
	"phases",
	"priorities",
	"providers",
	"queues",
	"roles",
	"session_entries",
	"stages",
	"statuses",
	"streams",
	"task_attributions",
	"task_types",
	"tasks",
	"workflow_events",
	"workflow_events_history",
	"workflow_schedules",
	"workflow_status",
}

var durableRuntimeTables = []string{
	"application_versions",
	"epoch_state_projection",
	"event_dispatch_kv",
	"notifications",
	"operation_outputs",
	"queues",
	"streams",
	"workflow_events",
	"workflow_events_history",
	"workflow_schedules",
	"workflow_status",
}

func readInitializationState(t *testing.T, dbPath string) initializationState {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open initialized database: %v", err)
	}
	defer db.Close()

	state := initializationState{
		Counts:        make(map[string]int, 4),
		RuntimeCounts: make(map[string]int, len(durableRuntimeTables)),
		Agents:        make(map[string]builtInAgentState, tasks.WellKnownAgentCount),
	}
	for _, table := range []string{"agents", "agents_software", "pasture_well_known_agents", "pasture_agent_categories"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		state.Counts[table] = count
	}
	tableRows, err := db.Query(`SELECT name FROM sqlite_master
		WHERE type='table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name`)
	if err != nil {
		t.Fatalf("read initialized schema inventory: %v", err)
	}
	var actualTables []string
	for tableRows.Next() {
		var table string
		if err := tableRows.Scan(&table); err != nil {
			t.Fatalf("scan initialized schema inventory: %v", err)
		}
		actualTables = append(actualTables, table)
	}
	if err := tableRows.Close(); err != nil {
		t.Fatalf("close initialized schema inventory: %v", err)
	}
	if err := tableRows.Err(); err != nil {
		t.Fatalf("iterate initialized schema inventory: %v", err)
	}
	if !slices.Equal(actualTables, initializedSchemaTables) {
		t.Errorf("initialized schema tables differ:\n got: %v\nwant: %v", actualTables, initializedSchemaTables)
	}
	if err := db.QueryRow("SELECT MAX(version) FROM audit_schema_meta").Scan(&state.SchemaVersion); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	// DBOS Launch writes application_versions before starting its queue runner,
	// scheduler, and recovery sweep. The remaining tables catch runtime work that
	// could outlive that startup marker.
	for _, table := range durableRuntimeTables {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("read initialized table %s: %v", table, err)
		}
		state.RuntimeCounts[table] = count
	}

	rows, err := db.Query(`SELECT w.name, w.agent_id, c.automaton_role, c.pasture_role
		FROM pasture_well_known_agents w
		JOIN pasture_agent_categories c ON c.agent_id = w.agent_id
		ORDER BY w.name`)
	if err != nil {
		t.Fatalf("read built-in agents: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var agent builtInAgentState
		if err := rows.Scan(&name, &agent.ID, &agent.AutomatonRole, &agent.PastureRole); err != nil {
			t.Fatalf("scan built-in agent: %v", err)
		}
		state.Agents[name] = agent
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate built-in agents: %v", err)
	}
	return state
}

func assertCanonicalAgents(t *testing.T, agents map[string]builtInAgentState) {
	t.Helper()

	if len(agents) != tasks.WellKnownAgentCount {
		t.Fatalf("built-in agent mappings = %d, want %d", len(agents), tasks.WellKnownAgentCount)
	}
	for _, spec := range tasks.WellKnownAgents() {
		agent, ok := agents[spec.Name]
		if !ok {
			t.Errorf("missing built-in agent %q", spec.Name)
			continue
		}
		if _, err := provenance.ParseAgentID(agent.ID); err != nil {
			t.Errorf("built-in agent %q has invalid id %q: %v", spec.Name, agent.ID, err)
		}
		if agent.AutomatonRole != string(spec.Role) || agent.PastureRole != string(protocol.PastureRoleNone) {
			t.Errorf("built-in agent %q categories = (%q, %q), want (%q, %q)", spec.Name, agent.AutomatonRole, agent.PastureRole, spec.Role, protocol.PastureRoleNone)
		}
	}
}

func runCLIWithEnvironment(t *testing.T, overrides map[string]string, args ...string) runOutcome {
	t.Helper()

	overridden := make(map[string]struct{}, len(overrides))
	for key := range overrides {
		overridden[key] = struct{}{}
	}
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overridden[key]; !replaced {
			env = append(env, entry)
		}
	}
	for key, value := range overrides {
		if value != "" {
			env = append(env, key+"="+value)
		}
	}

	cmd := exec.Command(binaryPath, args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			exitCode = exit.ExitCode()
		} else {
			t.Fatalf("unexpected command error: %v", err)
		}
	}
	return runOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitCode}
}
