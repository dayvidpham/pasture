package main

// This file is the BUILT-CLI proof for the first-session claim. OpenCode v2
// publishes session.created inside Session.create, before its plugins can
// subscribe, so on a fresh server the first session never delivers that event.
// The claim must therefore bind on the first event the plugin does observe.
//
// The subject drives the built binary directly, exactly as the generated plugin
// spawns it, with NO session.created in the sequence: a session.prompt writes
// the claim, and a following permission.evaluate on the SAME session reads it
// and reaches a real policy denial instead of the UNBOUND fail-open. It is the
// capture sitting's deny flow with the missed session.created removed.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/provadapter"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/provenance"
)

// firstSessionRun is what the built binary returned to the transport.
type firstSessionRun struct {
	exit   int
	stdout string
	stderr string
}

// runFirstSessionBinary spawns the built binary the way the generated OpenCode
// plugin does: the native coordinate flags, the host payload on standard input,
// and the store path in the environment.
func runFirstSessionBinary(t *testing.T, binary, dbPath, actor, event, hostVersion string, payload []byte) firstSessionRun {
	t.Helper()
	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle",
		"--harness", "opencode", "--event", event, "--host-version", hostVersion)
	command.Stdin = bytes.NewReader(payload)
	command.Env = discoveryChildEnv(map[string]*string{
		"PASTURE_ACTOR_ID": &actor, "PASTURE_CAPTURE_DIR": nil, "PASTURE_HOOK_FAIL_CLOSED": nil,
	})
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit, "the binary must exit with a status, not fail to start: %s", stderr.String())
		return firstSessionRun{exit: exit.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
	}
	return firstSessionRun{exit: 0, stdout: stdout.String(), stderr: stderr.String()}
}

// firstSessionConsultations reads every committed gate consultation back through
// the production journal reader, in append order.
func firstSessionConsultations(t *testing.T, dbPath string) []provenance.EvidenceRow {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, tracker.Close()) }()
	return queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)
}

// TestFirstObservedSessionEventBindsTheClaimAndTheNextGateDenies is the
// motivating proof. It never sends session.created. The first observed event,
// session.prompt, carries a session binding and an actor, so it must write the
// claim; the gate on that first event still reads an unbound session because the
// claim is written after the gate. The following permission.evaluate on the same
// session must then read the claim and reach Deny(NoActiveAssignment), which is
// impossible for an unclaimed session.
//
// RED when: the claim trigger stays session-start-only (no row is written and
// the second gate records UNBOUND), when the write is skipped for a non-start
// event, or when a later event errors on the already-present claim.
func TestFirstObservedSessionEventBindsTheClaimAndTheNextGateDenies(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)

	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	agent, err := tracker.RegisterHumanAgent("first-session-owner", "first session owner", "first-session@example.invalid")
	require.NoError(t, err)
	require.NoError(t, tracker.Close())
	actor := agent.ID.String()

	// Model an existing CLI-only store from before built-in initialization:
	// native ingress identity and gate prerequisites exist, but no well-known
	// bindings do. The hook's real durable open must fill the missing registry.
	db := rawBoundary(t, dbPath)
	_, err = db.Exec(`DELETE FROM pasture_agent_categories WHERE agent_id IN (SELECT agent_id FROM pasture_well_known_agents)`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM pasture_well_known_agents`)
	require.NoError(t, err)

	fixtureDir := filepath.Join("..", "..", "internal", "lifecycle", "ingress", "opencode", "testdata", "fixtures")
	prompt, err := os.ReadFile(filepath.Join(fixtureDir, "opencode_session_prompt_2_0_20.1.json"))
	require.NoError(t, err)
	var promptPayload map[string]any
	require.NoError(t, json.Unmarshal(prompt, &promptPayload))
	session, ok := promptPayload["sessionID"].(string)
	require.True(t, ok, "the session.prompt capture must carry sessionID")
	require.NotEmpty(t, session)

	// EVENT 1 — session.prompt, the first event the plugin observes. No
	// session.created is ever sent. The gate runs before the claim write, so
	// this event proceeds as UNBOUND and then binds the session.
	started := time.Now()
	promptRun := runFirstSessionBinary(t, binary, dbPath, actor, "session.prompt", "2.0.21", prompt)
	t.Logf("native hook with all built-in bindings missing: %s", time.Since(started))
	require.Equal(t, 0, promptRun.exit, promptRun.stderr)
	require.JSONEq(t, `{"decision":"proceed"}`, promptRun.stdout)

	var count int
	var claimed string
	require.NoError(t, db.QueryRow(`SELECT COUNT(*), actor FROM pasture_session_claim WHERE harness = ? AND session = ?`, "opencode", session).Scan(&count, &claimed))
	require.Equal(t, 1, count, "the first observed event must write the claim even without session.created")
	require.Equal(t, actor, claimed)

	promptConsultations := firstSessionConsultations(t, dbPath)
	require.Len(t, promptConsultations, 1)
	promptDecision := readerConsultationDecisionOf(t, promptConsultations[0].Payload)
	require.Equal(t, backend.ReasonUnboundSession.String(), promptDecision.Reason,
		"the first event's gate runs before the claim write, so it is honestly unbound")

	// EVENT 2 — permission.evaluate on the SAME session. The claim is now
	// present, so the gate reads it and reaches Deny(NoActiveAssignment). The
	// permission fixture is re-keyed to the prompt session so both events name
	// one session, which is the sequence a single real run produces.
	permission, err := os.ReadFile(filepath.Join(fixtureDir, "opencode_permission_evaluate_2_0_21.3.json"))
	require.NoError(t, err)
	var permissionPayload map[string]any
	require.NoError(t, json.Unmarshal(permission, &permissionPayload))
	permissionPayload["sessionID"] = session
	permission, err = json.Marshal(permissionPayload)
	require.NoError(t, err)

	permissionRun := runFirstSessionBinary(t, binary, dbPath, actor, "permission.evaluate", "2.0.21", permission)
	require.Equal(t, 0, permissionRun.exit, permissionRun.stderr)
	require.JSONEq(t, `{"decision":"deny","reason":"no-active-assignment"}`, permissionRun.stdout)

	permissionConsultations := firstSessionConsultations(t, dbPath)
	require.Len(t, permissionConsultations, 2)
	permissionDecision := readerConsultationDecisionOf(t, permissionConsultations[1].Payload)
	require.Equal(t, "deny", permissionDecision.Decision)
	require.Equal(t, backend.ReasonNoActiveAssignment.String(), permissionDecision.Reason)

	assertNativeReceiptAuthorsAndBuiltIns(t, db, permissionConsultations)
}

func assertNativeReceiptAuthorsAndBuiltIns(t *testing.T, db *sql.DB, receipts []provenance.EvidenceRow) {
	t.Helper()
	// Native receipts still commit as the system actor, not as a legacy hook
	// automaton. Inspect only receipt operations, not bootstrap registrations.
	for _, evidence := range receipts {
		var committer string
		require.NoError(t, db.QueryRow(`SELECT actor_id FROM journal WHERE journal_id = ?`, evidence.ProducingOperationJournalID).Scan(&committer))
		require.Equal(t, provadapter.PastureSystemDefaultActorID().String(), committer)
	}
	var canonical int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pasture_well_known_agents`).Scan(&canonical))
	require.Equal(t, len(tasks.WellKnownAgents()), canonical)
	for _, spec := range tasks.WellKnownAgents() {
		var role string
		require.NoError(t, db.QueryRow(`SELECT c.automaton_role FROM pasture_well_known_agents w
			JOIN pasture_agent_categories c ON c.agent_id = w.agent_id WHERE w.name = ?`, spec.Name).Scan(&role))
		require.Equal(t, string(spec.Role), role)
	}
}
