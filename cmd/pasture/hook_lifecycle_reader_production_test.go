package main

// This file is the BUILT-CLI half of the gate-reader proof split. The handler
// subjects (internal/handlers) prove typed error identity,
// ErrLifecycleBeforeDurableWrite, and the absence of a receipt. They do NOT
// observe command standard error, host bytes, a fault-record line, or
// fail-closed settlement. Every production-producible row therefore also needs
// a subject here, driven through the real host transport:
//
//   - Claude Code: the built binary as hooks.json invokes it.
//   - Codex: the generated .codex/hooks/events/<Event>.sh runner.
//   - OpenCode: Bun executing the generated .opencode/plugins/pasture-lifecycle.ts.
//
// A Go-only proof does not count for Codex or OpenCode.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/nativeresponse"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/provenance"
)

// ─── the store fixtures every built gate proof starts from ───────────────────

// seedReaderClaim registers the actor a gate proof will claim and writes the
// session claim the gate reads. It writes through the same function a
// session-start event calls, because a fixture that wrote the claim table by any
// other means would prove a store shape the product never produces.
func seedReaderClaim(t *testing.T, dbPath string, harness ir.HarnessID, session string) provenance.ActorID {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, tracker.Close()) }()
	agent, err := tracker.RegisterHumanAgent("gate-reader-fixture", "gate-owner", "gate-owner@example.invalid")
	require.NoError(t, err)
	kind := registration.EventSessionStart
	switch harness {
	case ir.HarnessCodex:
		kind = registration.EventCodexSessionStart
	case ir.HarnessOpenCode:
		kind = registration.EventOpenCodeSessionCreated
	}
	require.NoError(t, tasks.RecordLifecycleSessionClaim(
		context.Background(), tracker, harness, kind,
		[]model.NativeBinding{{Kind: model.BindingSession, Value: session}},
		tasks.ActorClaim(agent.ID.String()), lifecycleCLIClock{},
	))
	return agent.ID
}

// registerReaderAgent registers an additional actor the projection-mismatch row
// needs as the episode's real occupant.
func registerReaderAgent(t *testing.T, dbPath, handle string) provenance.ActorID {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, tracker.Close()) }()
	agent, err := tracker.RegisterHumanAgent(handle, "gate occupant", handle+"@example.invalid")
	require.NoError(t, err)
	return agent.ID
}

// seedReaderEpisode starts one owner-responsibility episode on a task in the
// caller's phase and returns the task identity. withMaterial selects whether the
// assignment-start material the production writer would commit is present; the
// role-source row deliberately omits it.
func seedReaderEpisode(t *testing.T, dbPath string, occupant provenance.ActorID, phase provenance.Phase, withMaterial bool, hugeMaterial bool) provenance.TaskID {
	t.Helper()
	task, err := applyReaderEpisode(t, dbPath, occupant, phase, withMaterial, hugeMaterial)
	require.NoError(t, err)
	return task
}

// applyReaderEpisode is seedReaderEpisode with the store error returned, so the
// byte-limit subject can assert the WRITE-side refusal that keeps the reader's
// byte bound unreachable through any supported writer.
func applyReaderEpisode(t *testing.T, dbPath string, occupant provenance.ActorID, phase provenance.Phase, withMaterial bool, hugeMaterial bool) (provenance.TaskID, error) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return provenance.TaskID{}, err
	}
	defer func() { require.NoError(t, tracker.Close()) }()

	task, err := tracker.Create("file://gate-reader-fixture", "gate reader fixture task", "", provenance.TaskTypeTask, provenance.PriorityMedium, phase)
	require.NoError(t, err)

	genesis, err := tracker.Journal().LookupCommitted(provenance.OperationID("pasture.system.genesis.v1"))
	require.NoError(t, err)
	authority := provenance.JournalID(0)
	found := false
	for _, slot := range genesis.ResultSlots {
		if slot.Slot == provenance.ResultSlotID("auth") {
			authority, found = slot.ProducedJournalID, true
		}
	}
	require.True(t, found, "the system genesis must publish its authority result slot")

	assignment := provenance.AssignmentID("gate-reader-fixture-owner")
	operation := provenance.OperationID("gate-reader-fixture-owner-start")
	effects := []provenance.Effect{{
		Sort: provenance.EffectAssignmentStart, ResultSlot: "authority", TaskID: task.ID,
		AssignmentID: assignment, SlotID: provenance.SlotOwnerResponsibility, Occupant: occupant,
	}}
	if withMaterial {
		started, err := tasks.MapMaterialEvent(tasks.AssignmentStartedEvent{
			Task: task.ID, Assignment: assignment, Role: tasks.RoleOwnerResponsibility, Occupant: occupant,
		})
		require.NoError(t, err)
		started.ResultSlot = "event"
		effects = append(effects, started)
	}
	if hugeMaterial {
		// TEST-ONLY FIXTURE BOUNDARY on a Provenance-owned task event. The
		// payload's size alone would exceed the reader's bound; the write-side
		// canonical-mutation bound refuses it before it is stored.
		pad := strings.Repeat("x", 8<<20)
		payload := `{"assignment":"gate-reader-fixture-huge","role":"owner-responsibility","occupant":"` + occupant.String() + `","pad":"` + pad + `"}`
		effects = append(effects, provenance.Effect{
			Sort: provenance.EffectTaskEvent, ResultSlot: "huge-material", TaskID: task.ID,
			EventKind: tasks.FamilyAssignmentStarted.EventKind(), Payload: []byte(payload),
		})
	}
	_, err = tracker.Journal().Apply(provenance.OperationInput{
		OperationID:        operation,
		ActorID:            occupant,
		AuthorityJournalID: &authority,
		CommandDigest:      []byte(operation),
		Effects:            effects,
	})
	return task.ID, err
}

// rawBoundary opens the unified store's SQLite file for the labelled test-only
// fixture writes the fault rows need. Every caller's write is a boundary a
// supported writer cannot produce, which is the point of the row.
func rawBoundary(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ─── the three real transports ───────────────────────────────────────────────

// readerRun is what a built transport returned to its host.
type readerRun struct {
	ExitCode     int
	Stdout       string
	Stderr       string
	FaultDir     string
	Thrown       string // OpenCode only: the plugin callback's thrown message, if any.
	ChildStdout  string // OpenCode only: the spawned child's standard output bytes.
	Continuation string // The bytes the host reads as "continue" on this transport.
}

// readerHarnessCase is one (harness, gate event, fixture, transport) cell.
type readerHarnessCase struct {
	label   string
	harness ir.HarnessID
	native  string
	fixture string
	mapping runtime.LifecycleEventMapping
	run     func(t *testing.T, binary, dbPath string, raw []byte, failClosed bool) readerRun
}

func readerHarnessCases(t *testing.T) []readerHarnessCase {
	t.Helper()
	claude, err := runtime.ClaudeCode2_1_261Lifecycle().Mapping(runtime.ClaudeEventPreToolUse)
	require.NoError(t, err)
	codex, err := runtime.Codex0_153_0Lifecycle().Mapping(runtime.CodexEventPreToolUse)
	require.NoError(t, err)
	openCode, err := runtime.OpenCode1_18_29Lifecycle().Mapping(runtime.OpenCodeEventToolExecuteBefore)
	require.NoError(t, err)
	return []readerHarnessCase{
		{
			label: "ClaudeCode", harness: ir.HarnessClaudeCode, native: "PreToolUse",
			fixture: filepath.Join("internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "pre_tool_use_2_1_261.json"),
			mapping: claude, run: runReaderClaude,
		},
		{
			label: "Codex", harness: ir.HarnessCodex, native: "PreToolUse",
			fixture: filepath.Join("internal", "lifecycle", "ingress", "codex", "testdata", "fixtures", "pre_tool_use_0_153_0.json"),
			mapping: codex, run: runReaderCodex,
		},
		{
			label: "OpenCode", harness: ir.HarnessOpenCode, native: "tool.execute.before",
			fixture: filepath.Join("internal", "lifecycle", "ingress", "opencode", "testdata", "fixtures", "tool_execute_before_1_18_29.json"),
			mapping: openCode, run: runReaderOpenCode,
		},
	}
}

func readerFixture(t *testing.T, relative string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", relative))
	require.NoError(t, err)
	return raw
}

// runReaderClaude drives the built binary exactly as hooks/hooks.json invokes
// it: the generated command string, the host payload on standard input, the
// store path and binary in the environment.
func runReaderClaude(t *testing.T, binary, dbPath string, raw []byte, failClosed bool) readerRun {
	t.Helper()
	shell, err := exec.LookPath("sh")
	require.NoError(t, err)
	path, _ := discoveryPathExecutable(t, "printf '2.1.261 (Claude Code)\\n'")
	env := map[string]*string{
		"PASTURE_BIN": &binary, "PASTURE_DB_PATH": &dbPath, "PATH": &path,
		"CLAUDE_CODE_EXECPATH": nil, "PASTURE_CAPTURE_DIR": nil, "PASTURE_ACTOR_ID": nil,
		"PASTURE_HOOK_FAIL_CLOSED": nil,
	}
	if failClosed {
		closed := "1"
		env["PASTURE_HOOK_FAIL_CLOSED"] = &closed
	}
	command := exec.Command(shell, "-c", generatedClaudeLifecycleCommand(t, "PreToolUse"))
	command.Stdin = bytes.NewReader(raw)
	command.Env = discoveryChildEnv(env)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	return finishTransport(t, command, &stdout, &stderr, dbPath)
}

// runReaderCodex drives the generated Codex runner, which execs the built
// binary. The runner queries the installed host executable for its version, so
// a controlled codex stand-in is placed first on PATH.
func runReaderCodex(t *testing.T, binary, dbPath string, raw []byte, failClosed bool) readerRun {
	t.Helper()
	shell, err := exec.LookPath("sh")
	require.NoError(t, err)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	path := t.TempDir()
	stub := filepath.Join(path, "codex")
	script := "#!/bin/sh\n[ \"$#\" = 1 ] && [ \"$1\" = --version ] || exit 91\nprintf 'codex-cli 0.153.0\\n'\n"
	require.NoError(t, os.WriteFile(stub, []byte(script), 0o700))
	env := map[string]*string{
		"PASTURE_BIN": &binary, "PASTURE_DB_PATH": &dbPath, "PATH": &path,
		"PASTURE_CAPTURE_DIR": nil, "PASTURE_ACTOR_ID": nil, "PASTURE_HOOK_FAIL_CLOSED": nil,
	}
	if failClosed {
		closed := "1"
		env["PASTURE_HOOK_FAIL_CLOSED"] = &closed
	}
	runner := filepath.Join(root, ".codex", "hooks", "events", "PreToolUse.sh")
	command := exec.Command(shell, runner)
	command.Dir = root
	command.Stdin = bytes.NewReader(raw)
	command.Env = discoveryChildEnv(env)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	return finishTransport(t, command, &stdout, &stderr, dbPath)
}

// runReaderOpenCode drives Bun executing the generated plugin. The plugin spawns
// the built binary and forwards its diagnostic; the runner observes the child's
// bytes by teeing the real spawn, never by replacing it.
func runReaderOpenCode(t *testing.T, binary, dbPath string, raw []byte, failClosed bool) readerRun {
	t.Helper()
	bun, err := exec.LookPath("bun")
	require.NoError(t, err, "Bun is required for the generated OpenCode production proof; enter the flake dev shell")
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	moduleURL := (&url.URL{Scheme: "file", Path: filepath.Join(root, filepath.FromSlash(codegen.OpenCodeHooksModulePath))}).String()
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "fixture.json")
	require.NoError(t, os.WriteFile(fixturePath, raw, 0o600))
	runner := filepath.Join(dir, "reader-runner.ts")
	require.NoError(t, os.WriteFile(runner, []byte(fmt.Sprintf(openCodeRunnerScript, moduleURL, fixturePath)), 0o600))

	path := t.TempDir()
	stub := filepath.Join(path, "opencode")
	require.NoError(t, os.WriteFile(stub, []byte("#!/bin/sh\nprintf '1.19.0\\n'\n"), 0o700))
	env := map[string]*string{
		"PASTURE_BIN": &binary, "PASTURE_DB_PATH": &dbPath, "PATH": &path,
		"PASTURE_CAPTURE_DIR": nil, "PASTURE_ACTOR_ID": nil, "PASTURE_HOOK_FAIL_CLOSED": nil,
	}
	if failClosed {
		closed := "1"
		env["PASTURE_HOOK_FAIL_CLOSED"] = &closed
	}
	command := exec.Command(bun, runner)
	command.Env = discoveryChildEnv(env)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	run := finishTransport(t, command, &stdout, &stderr, dbPath)
	var envelope struct {
		Thrown string `json:"thrown"`
		Calls  []struct {
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
			Exit   int    `json:"exit"`
		} `json:"calls"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &envelope), stdout.String())
	require.Len(t, envelope.Calls, 1, "one gate callback must spawn exactly one child")
	run.Thrown = envelope.Thrown
	run.ChildStdout = envelope.Calls[0].Stdout
	run.Continuation = envelope.Calls[0].Stdout
	return run
}

// openCodeRunnerScript imports the generated plugin, tees the real child spawn
// (never replacing it), invokes the gate callback, and reports the observed
// bytes plus any thrown refusal as one JSON object.
const openCodeRunnerScript = `
import plugin from %q;
const fixture = await Bun.file(%q).json();
const input = fixture.input, output = fixture.output;
const realSpawn = Bun.spawn;
const calls = [];
Bun.spawn = options => {
  const child = realSpawn(options);
  const [stdout, observedOut] = child.stdout.tee();
  const [stderr, observedErr] = child.stderr.tee();
  calls.push(Promise.all([new Response(observedOut).text(), new Response(observedErr).text(), child.exited]));
  return {stdout, stderr, exited: child.exited, get exitCode(){return child.exitCode;}, kill: signal => child.kill(signal)};
};
let thrown = null;
try {
  const hooks = await plugin.server({client: {}}, {});
  await hooks["tool.execute.before"](input, output);
} catch (error) {
  thrown = String(error && error.message ? error.message : error);
}
Bun.spawn = realSpawn;
const observed = await Promise.all(calls);
console.log(JSON.stringify({thrown, calls: observed.map(([stdout, stderr, exit]) => ({stdout, stderr, exit}))}));
`

// finishTransport runs a prepared host command and reports what the host saw.
func finishTransport(t *testing.T, command *exec.Cmd, stdout, stderr *bytes.Buffer, dbPath string) readerRun {
	t.Helper()
	code := 0
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit, "the transport must exit with a status, not fail to start: %s", stderr.String())
		code = exit.ExitCode()
	}
	return readerRun{
		ExitCode: code, Stdout: stdout.String(), Stderr: stderr.String(),
		FaultDir: filepath.Dir(dbPath), Continuation: stdout.String(),
	}
}

// ─── the outcome cells ───────────────────────────────────────────────────────

// seedBoundActorOwningAMappedTask writes the durable facts an EVALUATED Proceed
// needs: a registered actor, the session claim that binds the fixture's session
// to it, and one owner-responsibility assignment start on a task whose phase the
// policy tables map. It is the PRESERVE seed for the interpreted V2 subjects.
func seedBoundActorOwningAMappedTask(t *testing.T, dbPath string, harness ir.HarnessID, raw []byte) {
	t.Helper()
	session := captureSessionIDOf(t, raw, harness)
	actor := seedReaderClaim(t, dbPath, harness, session)
	seedReaderEpisode(t, dbPath, actor, provenance.PhaseUnscoped, true, false)
}

type readerConsultationDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// readerConsultation returns the committed consultation payload, or nil when the
// invocation committed none.
func readerConsultation(t *testing.T, dbPath string) []byte {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, tracker.Close()) }()
	rows := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)
	if len(rows) == 0 {
		return nil
	}
	require.Len(t, rows, 1, "one gate invocation must commit exactly one consultation")
	return rows[0].Payload
}

func readerConsultationDecisionOf(t *testing.T, payload []byte) readerConsultationDecision {
	t.Helper()
	members := decodeJSONObject(t, payload)
	var decision readerConsultationDecision
	require.NoError(t, json.Unmarshal(members["decision"], &decision))
	return decision
}

// readerNormalizedAndOutcome computes, at run time, the durable decision and the
// host outcome the production encoder produces for one policy verdict. The test
// never hard-codes a per-harness outcome: it derives both from the pinned
// runtime row's response capability.
func readerNormalizedAndOutcome(t *testing.T, harness ir.HarnessID, mapping runtime.LifecycleEventMapping, decision backend.Decision) (backend.Decision, hostexit.Outcome) {
	t.Helper()
	normalized, err := nativeresponse.NormalizeDecision(mapping, decision)
	require.NoError(t, err)
	response, err := backend.NewHostResponse(normalized)
	require.NoError(t, err)
	var outcome hostexit.Outcome
	switch harness {
	case ir.HarnessClaudeCode:
		outcome, err = nativeresponse.EncodeClaude(mapping, response)
	case ir.HarnessCodex:
		outcome, err = nativeresponse.EncodeCodex(mapping, response)
	case ir.HarnessOpenCode:
		outcome, err = nativeresponse.EncodeOpenCode(mapping, response)
	default:
		t.Fatalf("unknown harness %q", harness)
	}
	require.NoError(t, err)
	return normalized, outcome
}

func readerExitCode(t *testing.T, status hostexit.ExitStatus) int {
	t.Helper()
	code, ok := status.Code()
	require.True(t, ok, "a decided host outcome must carry an exit code")
	return code
}

func readerFreshStore(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	return dbPath
}

// TestReaderGateOutcomesOnEveryHarness is the nine literal outcome locators. For
// each harness's gate event it drives the real transport through an allow, a
// deny, and a fault, and agrees with the runtime row on the host bytes and the
// durable reason.
//
// WHAT IT VISITS: the three pinned gate events and their three real transports.
// WHAT IT DOES NOT READ: whether a transport stays current with regenerated
// output, or any event other than the pinned gate row.
func TestReaderGateOutcomesOnEveryHarness(t *testing.T) {
	binary := lifecycleBinary(t)
	for _, harness := range readerHarnessCases(t) {
		for _, cell := range []string{"allow", "deny", "fault"} {
			t.Run(harness.native+"/"+harness.label+"/"+cell, func(t *testing.T) {
				dbPath := readerFreshStore(t)
				raw := readerFixture(t, harness.fixture)
				switch cell {
				case "allow":
					seedBoundActorOwningAMappedTask(t, dbPath, harness.harness, raw)
				case "deny":
					seedBoundActorWithoutAssignment(t, dbPath, harness.harness, raw)
				case "fault":
					// A real store fault the built transport reaches: an episode
					// whose role no accepted record writes. The store opens
					// (unlike the integrity rows, which open-time replay refuses),
					// so the gate read is what faults.
					seedReaderRoleSource(t, dbPath, harness.harness, captureSessionIDOf(t, raw, harness.harness))
				}

				run := harness.run(t, binary, dbPath, raw, false)

				if cell == "fault" {
					continuation, err := nativeresponse.FaultContinuation(harness.harness, harness.mapping.Semantic())
					require.NoError(t, err)
					assert.Equal(t, string(continuation.Bytes()), run.Continuation, "a fail-open fault continues with the harness continuation")
					assert.Equal(t, 0, run.ExitCode, "a fail-open fault never refuses the host")
					assert.Contains(t, run.Stderr, "could not establish the role", "the diagnostic must name the gate read that faulted")
					records := readFaultRecords(t, run.FaultDir)
					require.Len(t, records, 1, "a fault writes exactly one record line")
					assert.Equal(t, "fault", records[0]["outcomeClass"])
					assert.Empty(t, readerConsultation(t, dbPath), "a fault commits no consultation")
					return
				}

				decision, err := backend.NewDecision(backend.DecisionProceed, backend.ReasonLegal)
				if cell == "deny" {
					decision, err = backend.NewDecision(backend.DecisionDeny, backend.ReasonNoActiveAssignment)
				}
				require.NoError(t, err)
				normalized, outcome := readerNormalizedAndOutcome(t, harness.harness, harness.mapping, decision)
				assert.Equal(t, readerExitCode(t, outcome.Exit), run.ExitCode)
				assert.Equal(t, string(outcome.Stdout), run.Continuation, "the host continuation must match the encoder's bytes")
				assert.Equal(t, outcome.Stderr, strings.TrimRight(run.Stderr, "\n"), "the host diagnostic must match the encoder's refusal")
				payload := readerConsultation(t, dbPath)
				require.NotNil(t, payload, "an evaluated gate commits a consultation")
				committed := readerConsultationDecisionOf(t, payload)
				assert.Equal(t, normalized.Kind().String(), committed.Decision)
				assert.Equal(t, normalized.Reason().String(), committed.Reason)
				if cell == "deny" {
					assert.Equal(t, backend.ReasonNoActiveAssignment, decision.Reason(), "the policy verdict is a denial of the no-active-assignment rule")
				}
			})
		}
	}
}

// ─── the per-row store faults ────────────────────────────────────────────────

// readerFaultRow names one production-producible gate-read fault, the diagnostic
// token its stderr line must carry, and the real store fixture that produces it.
type readerFaultRow struct {
	name  string
	token string
	seed  func(t *testing.T, dbPath string, harness ir.HarnessID, session string)
}

// readerFaultRows are the production-producible gate-read faults whose damaged
// state a REAL store can hold and a BUILT transport can reach. Three rows the
// design's mapping lists in its built column are NOT here, and the reason is
// pinned by TestReaderIntegrityRowsAreHandlerOnlyBecauseTheStoreRefusesTheDamage:
// the pinned Provenance open-time replay convergence check refuses the damage
// (an out-of-range phase, an owner projection that disagrees with the folded
// journal) before any gate read runs, and the write-side canonical-mutation bound
// refuses a material large enough to trip the reader's byte bound. Those rows
// stay handler-only.
func readerFaultRows() []readerFaultRow {
	return []readerFaultRow{
		{name: "malformed-claim", token: "gate claim read", seed: seedReaderMalformedClaim},
		{name: "role-source", token: "could not establish the role", seed: seedReaderRoleSource},
	}
}

func seedReaderMalformedClaim(t *testing.T, dbPath string, harness ir.HarnessID, session string) {
	t.Helper()
	// TEST-ONLY FIXTURE BOUNDARY on the Pasture-owned claim table. The row is the
	// one shape no supported writer produces: a claim whose actor is not an actor.
	db := rawBoundary(t, dbPath)
	_, err := db.Exec(
		`INSERT INTO pasture_session_claim (harness, session, actor, claimed_at) VALUES (?, ?, ?, ?)`,
		string(harness), session, "not-an-actor-id", int64(1),
	)
	require.NoError(t, err)
}

func seedReaderRoleSource(t *testing.T, dbPath string, harness ir.HarnessID, session string) {
	t.Helper()
	actor := seedReaderClaim(t, dbPath, harness, session)
	seedReaderEpisode(t, dbPath, actor, provenance.PhaseUnscoped, false, false)
}

func seedReaderUnmappedPhase(t *testing.T, dbPath string, harness ir.HarnessID, session string) {
	t.Helper()
	actor := seedReaderClaim(t, dbPath, harness, session)
	task := seedReaderEpisode(t, dbPath, actor, provenance.PhaseUnscoped, true, false)
	// TEST-ONLY FIXTURE BOUNDARY on Provenance-owned tables. The insert exists
	// because tasks.phase_id really references phases.id, so the update would
	// otherwise violate a live foreign key.
	db := rawBoundary(t, dbPath)
	_, err := db.Exec(`INSERT INTO phases(id,name) VALUES (99,'test-unmapped-phase')`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE tasks SET phase_id=99 WHERE id=?`, task.String())
	require.NoError(t, err)
}

func seedReaderProjectionMismatch(t *testing.T, dbPath string, harness ir.HarnessID, session string) {
	t.Helper()
	claimActor := seedReaderClaim(t, dbPath, harness, session)
	occupant := registerReaderAgent(t, dbPath, "gate-reader-occupant")
	task := seedReaderEpisode(t, dbPath, occupant, provenance.PhaseUnscoped, true, false)
	// TEST-ONLY FIXTURE BOUNDARY on the Provenance-owned task projection. The
	// owner column is written with the episode in one transaction, so a value
	// that disagrees with the active winner is the store damage this row names.
	db := rawBoundary(t, dbPath)
	_, err := db.Exec(`UPDATE tasks SET owner_id=? WHERE id=?`, claimActor.String(), task.String())
	require.NoError(t, err)
}

func seedReaderByteLimit(t *testing.T, dbPath string, harness ir.HarnessID, session string) {
	t.Helper()
	actor := seedReaderClaim(t, dbPath, harness, session)
	seedReaderEpisode(t, dbPath, actor, provenance.PhaseUnscoped, false, true)
}

// TestReaderGateFaultRowsReachTheHostFaultPath drives every BUILT row in the
// fault mapping through each harness's real transport and asserts the fail-open
// host settlement: exact continuation bytes, one stage-naming diagnostic, one
// fault record, no consultation, and no Deny.
//
// WHAT IT VISITS: every row readerFaultRows declares, plus the held-writer
// deadline row, on each pinned harness.
// WHAT IT DOES NOT READ: the rows pinned handler-only by
// TestReaderIntegrityRowsAreHandlerOnlyBecauseTheStoreRefusesTheDamage, or
// whether a transport stays current with regenerated output.
func TestReaderGateFaultRowsReachTheHostFaultPath(t *testing.T) {
	binary := lifecycleBinary(t)
	for _, harness := range readerHarnessCases(t) {
		for _, row := range readerFaultRows() {
			t.Run(harness.native+"/"+harness.label+"/"+row.name, func(t *testing.T) {
				dbPath := readerFreshStore(t)
				raw := readerFixture(t, harness.fixture)
				row.seed(t, dbPath, harness.harness, captureSessionIDOf(t, raw, harness.harness))
				run := harness.run(t, binary, dbPath, raw, false)

				continuation, err := nativeresponse.FaultContinuation(harness.harness, harness.mapping.Semantic())
				require.NoError(t, err)
				assert.Equal(t, string(continuation.Bytes()), run.Continuation)
				assert.Equal(t, 0, run.ExitCode, "a fail-open fault never refuses the host")
				assert.Contains(t, run.Stderr, row.token, "the one diagnostic must name this row's stage")
				records := readFaultRecords(t, run.FaultDir)
				require.Len(t, records, 1, "a fault writes exactly one record line")
				assert.Equal(t, "fault", records[0]["outcomeClass"])
				assert.Contains(t, fmt.Sprint(records[0]["cause"]), row.token, "the durable record must agree with the host-facing diagnostic")
				assert.Empty(t, readerConsultation(t, dbPath), "a fault commits no consultation")
			})
		}
		t.Run(harness.native+"/"+harness.label+"/deadline", func(t *testing.T) {
			runReaderDeadlineRow(t, binary, harness, false)
		})
	}
}

// TestReaderGateFaultRowsFailClosedOnlyWhereEvidenced runs the same built
// fixtures under PASTURE_HOOK_FAIL_CLOSED=1. The expected refusal is COMPUTED at
// run time from the pinned row: a block only when the effective mode blocks by
// exit code AND the failure evidence is present.
//
// WHAT IT VISITS: every row readerFaultRows declares, plus the held-writer
// deadline row, on each pinned harness.
// WHAT IT DOES NOT READ: any harness verdict not recomputed from
// LookupLifecycleFailure, or whether a transport stays current with regenerated
// output.
func TestReaderGateFaultRowsFailClosedOnlyWhereEvidenced(t *testing.T) {
	binary := lifecycleBinary(t)
	for _, harness := range readerHarnessCases(t) {
		policy, found := runtime.LookupLifecycleFailure(harness.harness, harness.native)
		require.True(t, found, "the pinned profile must declare this harness's gate event")
		blocked := policy.Mode.BlocksByExitCode() && policy.Evidence.IsPresent()
		for _, row := range readerFaultRows() {
			t.Run(harness.native+"/"+harness.label+"/"+row.name+"/fail-closed", func(t *testing.T) {
				dbPath := readerFreshStore(t)
				raw := readerFixture(t, harness.fixture)
				row.seed(t, dbPath, harness.harness, captureSessionIDOf(t, raw, harness.harness))
				run := harness.run(t, binary, dbPath, raw, true)

				continuation, err := nativeresponse.FaultContinuation(harness.harness, harness.mapping.Semantic())
				require.NoError(t, err)
				wantContinuation := string(continuation.Bytes())
				wantExit := 0
				if blocked {
					wantContinuation, wantExit = "", 2
				}
				assert.Equal(t, wantContinuation, run.Continuation)
				assert.Equal(t, wantExit, run.ExitCode)
				assert.Contains(t, run.Stderr, row.token)
				records := readFaultRecords(t, run.FaultDir)
				require.Len(t, records, 1)
				assert.Empty(t, readerConsultation(t, dbPath), "a fault commits no consultation")
			})
		}
		t.Run(harness.native+"/"+harness.label+"/deadline/fail-closed", func(t *testing.T) {
			policy, found := runtime.LookupLifecycleFailure(harness.harness, harness.native)
			require.True(t, found)
			runReaderDeadlineRow(t, binary, harness, policy.Mode.BlocksByExitCode() && policy.Evidence.IsPresent())
		})
	}
}

// runReaderDeadlineRow holds the SQLite write lock while the real transport runs
// the gate, so the invocation reaches its hook-invocation deadline with the
// ownership read unanswerable. It is the built /deadline settlement proof.
func runReaderDeadlineRow(t *testing.T, binary string, harness readerHarnessCase, failClosed bool) {
	t.Helper()
	dbPath := readerFreshStore(t)
	raw := readerFixture(t, harness.fixture)

	locked := make(chan struct{})
	release := make(chan struct{})
	lockFailed := make(chan error, 1)
	var once sync.Once
	unlock := sync.OnceFunc(func() { close(release) })
	go func() {
		db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
		if err != nil {
			lockFailed <- err
			return
		}
		defer db.Close()
		transaction, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			lockFailed <- err
			return
		}
		if _, err := transaction.Exec(`CREATE TABLE IF NOT EXISTS lifecycle_reader_deadline_probe (id INTEGER PRIMARY KEY)`); err != nil {
			lockFailed <- err
			return
		}
		once.Do(func() { close(locked) })
		<-release
		_ = transaction.Rollback()
	}()

	select {
	case <-locked:
	case err := <-lockFailed:
		require.NoError(t, err, "could not hold the SQLite write lock")
	case <-time.After(30 * time.Second):
		unlock()
		t.Fatal("the SQLite write lock was never taken, so the deadline could not be exercised")
	}

	run := harness.run(t, binary, dbPath, raw, failClosed)
	unlock()

	continuation, err := nativeresponse.FaultContinuation(harness.harness, harness.mapping.Semantic())
	require.NoError(t, err)
	wantContinuation, wantExit := string(continuation.Bytes()), 0
	if failClosed {
		wantContinuation, wantExit = "", 2
	}
	assert.Equal(t, wantContinuation, run.Continuation)
	assert.Equal(t, wantExit, run.ExitCode)
	assert.Contains(t, run.Stderr, "hook-invocation deadline", "the diagnostic must name the deadline path")
	records := readFaultRecords(t, run.FaultDir)
	require.Len(t, records, 1, "the fault must be recorded durably")
	assert.Equal(t, "fault", records[0]["outcomeClass"])
	assert.Contains(t, fmt.Sprint(records[0]["cause"]), "hook-invocation deadline")
	assert.Empty(t, readerConsultation(t, dbPath), "an abandoned invocation commits no consultation")
}

// TestReaderIntegrityRowsAreHandlerOnlyBecauseTheStoreRefusesTheDamage pins WHY
// three rows the fault mapping lists in its built column have no built subject.
//
// It is not a convenience. Each row's damage is a store state no supported
// writer produces, and the two ways to reach it both refuse before the gate read:
//
//   - An out-of-range phase, and an owner projection that disagrees with the
//     folded journal, are rejected by Provenance's OPEN-TIME full replay
//     convergence check. The hook never reaches its read; it reports a
//     connection fault instead of a gate fault.
//   - A material large enough to trip the reader's 8 MiB byte bound cannot be
//     written at all: the canonical-mutation bound refuses any single payload
//     above 1 MiB, so the stored result the reader would reject is unreachable
//     through Journal.Apply.
//
// The reader still owns those arms, and the handler subjects prove them with a
// wrapping API that can return the typed cause. This subject keeps the built
// column honest instead of publishing locators that cannot resolve.
//
// WHAT IT VISITS: the three integrity rows the design lists in its built column.
// WHAT IT DOES NOT READ: the gate reader's own error mapping; the handler
// subjects cover that, and this subject asserts only that the store refuses the
// damaged state first.
func TestReaderIntegrityRowsAreHandlerOnlyBecauseTheStoreRefusesTheDamage(t *testing.T) {
	t.Run("unmapped-phase", func(t *testing.T) {
		dbPath := readerFreshStore(t)
		seedReaderUnmappedPhase(t, dbPath, ir.HarnessClaudeCode, "integrity-unmapped-phase")
		tracker, err := tasks.OpenTaskTracker(dbPath)
		if tracker != nil {
			_ = tracker.Close()
		}
		require.Error(t, err, "the store must refuse the out-of-range phase before the gate reads it")
		require.Contains(t, err.Error(), "journal replay")
		require.Contains(t, err.Error(), "field phase")
	})
	t.Run("projection-mismatch", func(t *testing.T) {
		dbPath := readerFreshStore(t)
		seedReaderProjectionMismatch(t, dbPath, ir.HarnessClaudeCode, "integrity-projection-mismatch")
		tracker, err := tasks.OpenTaskTracker(dbPath)
		if tracker != nil {
			_ = tracker.Close()
		}
		require.Error(t, err, "the store must refuse the owner divergence before the gate reads it")
		require.Contains(t, err.Error(), "journal replay")
		require.Contains(t, err.Error(), "field owner")
	})
	t.Run("byte-limit", func(t *testing.T) {
		dbPath := readerFreshStore(t)
		actor := seedReaderClaim(t, dbPath, ir.HarnessClaudeCode, "integrity-byte-limit")
		_, err := applyReaderEpisode(t, dbPath, actor, provenance.PhaseUnscoped, false, true)
		require.Error(t, err, "the canonical-mutation bound must refuse the oversized material before it is stored")
		require.Contains(t, err.Error(), "exceeds maximum 1048576")
	})
}
