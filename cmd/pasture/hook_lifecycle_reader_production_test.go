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
func seedReaderEpisode(t *testing.T, dbPath string, occupant provenance.ActorID, phase provenance.Phase, withMaterial bool) provenance.TaskID {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, tracker.Close()) }()

	task, err := tracker.Create("file://gate-reader-fixture", "gate reader fixture task", "", provenance.TaskTypeTask, provenance.PriorityMedium, phase)
	require.NoError(t, err)

	genesis := readerGenesisAuthority(t, tracker)

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
	_, err = tracker.Journal().Apply(provenance.OperationInput{
		OperationID:        operation,
		ActorID:            occupant,
		AuthorityJournalID: &genesis,
		CommandDigest:      []byte(operation),
		Effects:            effects,
	})
	require.NoError(t, err)
	return task.ID
}

// readerGenesisAuthority resolves the one bootstrap authority every reader
// fixture cites, so neither seeder can drift from the other on the genesis
// operation ID or its result slot.
func readerGenesisAuthority(t *testing.T, tracker interface {
	Journal() provenance.Journal
}) provenance.JournalID {
	t.Helper()
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
	return authority
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
// bytes by teeing the real spawn, never by replacing it. The module is copied
// beside a stub of the host-provided specifier (the host maps it to its
// bundled SDK at load time; the stub mirrors its identity define), because
// the committed artifact carries that import and Bun resolves it from the
// importing file upward. The v2 helper forwards the whole fixture object;
// the stubbed 1.19.0 host version routes the 1.18.29 row, whose struct
// decoder ignores the members the retired args-only projection used to drop.
func runReaderOpenCode(t *testing.T, binary, dbPath string, raw []byte, failClosed bool) readerRun {
	t.Helper()
	bun, err := exec.LookPath("bun")
	require.NoError(t, err, "Bun is required for the generated OpenCode production proof; enter the flake dev shell")
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	dir := t.TempDir()
	module, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(codegen.OpenCodeHooksModulePath)))
	require.NoError(t, err)
	modulePath := filepath.Join(dir, "pasture-hooks.ts")
	require.NoError(t, os.WriteFile(modulePath, module, 0o600))
	writeOpenCodePluginStubFiles(t, dir)
	moduleURL := (&url.URL{Scheme: "file", Path: modulePath}).String()
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

// openCodeRunnerScript imports the generated plugin's throwing gate helper,
// tees the real child spawn (never replacing it), invokes the callback with
// the whole fixture object, and reports the observed bytes plus any thrown
// refusal as one JSON object.
const openCodeRunnerScript = `
import { toolExecuteBefore } from %q;
const fixture = await Bun.file(%q).json();
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
  await toolExecuteBefore(fixture);
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
		ExitCode: code, Stderr: stderr.String(),
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
	seedReaderEpisode(t, dbPath, actor, provenance.PhaseUnscoped, true)
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

// readerOutcomeLocators are the nine literal public locators this file publishes.
// The loop asserts the composed subtest name is one of them, so a harness-label
// rename turns that cell RED instead of silently renaming a published locator.
var readerOutcomeLocators = map[string]struct{}{
	"PreToolUse/ClaudeCode/allow":        {},
	"PreToolUse/ClaudeCode/deny":         {},
	"PreToolUse/ClaudeCode/fault":        {},
	"PreToolUse/Codex/allow":             {},
	"PreToolUse/Codex/deny":              {},
	"PreToolUse/Codex/fault":             {},
	"tool.execute.before/OpenCode/allow": {},
	"tool.execute.before/OpenCode/deny":  {},
	"tool.execute.before/OpenCode/fault": {},
}

// readerAssertTransportProperties pins the properties a transport writes but a
// caller could forget to read: on OpenCode the generated plugin spawns the child
// and observes its bytes, and it must NOT throw for a gate that is not a policy
// Deny (the plugin throws only when the child returns a denial). It does NOT
// compare run.ChildStdout to run.Continuation: both are the same tee'd byte
// stream, so such a comparison could never fail.
func readerAssertTransportProperties(t *testing.T, harness readerHarnessCase, run readerRun) {
	t.Helper()
	if harness.harness != ir.HarnessOpenCode {
		return
	}
	require.Empty(t, run.Thrown, "the generated OpenCode plugin must not throw for a gate that is not a policy Deny")
	require.NotEmpty(t, run.ChildStdout, "the OpenCode transport must observe the spawned child's bytes")
}

// TestReaderGateOutcomesOnEveryHarness is the nine literal outcome locators. For
// each harness's gate event it drives the real transport through an allow, a
// deny, and a fault, and agrees with the runtime row on the host bytes and the
// durable reason.
//
// THE /deny CELL IS THE POLICY-DENY CELL, and on a row whose response capability
// does not allow a Deny the same policy verdict is committed as an UNENFORCED
// Deny (Proceed/ReasonUnenforcedDeny) and the host continues. The cell asserts
// whatever the pinned row's capability computes, never a refusal the row cannot
// express; the locator keeps the literal /deny name the outcome contract publishes, so the contract is
// readable from it.
//
// WHAT IT VISITS: the three pinned gate events and their three real transports.
// WHAT IT DOES NOT READ: whether a transport stays current with regenerated
// output, or any event other than the pinned gate row.
func TestReaderGateOutcomesOnEveryHarness(t *testing.T) {
	binary := lifecycleBinary(t)
	visited := map[string]bool{}
	for _, harness := range readerHarnessCases(t) {
		for _, cell := range []string{"allow", "deny", "fault"} {
			composed := harness.native + "/" + harness.label + "/" + cell
			t.Run(composed, func(t *testing.T) {
				require.Contains(t, readerOutcomeLocators, composed,
					"the composed locator must be one of the nine published names")
				assert.Equal(t, "TestReaderGateOutcomesOnEveryHarness/"+composed, t.Name(),
					"the published locator must match the literal name, so renaming the test function reddens every cell")
				visited[composed] = true

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
				readerAssertTransportProperties(t, harness, run)

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
				// The committed decision is written by waist.Decision.MarshalJSON,
				// which serializes only a constructor-validated kind/reason pair.
				// In the waist a refusal reason is admissible with BOTH Deny and
				// RequireHuman (waist/decision.go), so a reason does not name
				// exactly one kind in general. It does on the reachable paths
				// here: no production path emits RequireHuman for this branch,
				// the normalizer downgrades any the row cannot express, and the
				// encoders reject it. A refusal reason therefore reaches the
				// committed record only as Deny, so this reason equality already
				// pins the kind and a separate kind equality is not restated.
				assert.Equal(t, normalized.Reason().String(), committed.Reason)

				if cell == "allow" {
					// TEETH. The grant must be genuinely evaluated: the SAME
					// fixture on a store with no claim must record a DIFFERENT
					// reason. A hard-coded Proceed/Legal grant would make this
					// control legal too, and the allow cell would go RED.
					controlPath := readerFreshStore(t)
					controlRun := harness.run(t, binary, controlPath, raw, false)
					readerAssertTransportProperties(t, harness, controlRun)
					controlPayload := readerConsultation(t, controlPath)
					require.NotNil(t, controlPayload, "the unbound control must still commit a consultation")
					control := readerConsultationDecisionOf(t, controlPayload)
					assert.Equal(t, backend.ReasonUnboundSession.String(), control.Reason,
						"the same fixture with no claim must record the unbound reason; a hard-coded Proceed/Legal grant cannot satisfy this cell")
				}
			})
		}
	}
	// THE REVERSE DIRECTION: the forward Contains above pins emitted ⊆ published,
	// so a renamed label cannot publish a stranger. This pins published ⊆
	// emitted, so dropping a harness cannot silently UNPUBLISH one of the nine.
	for locator := range readerOutcomeLocators {
		assert.True(t, visited[locator],
			"the published locator %q was never emitted; a dropped harness silently unpublished it", locator)
	}
}

// ─── the per-row store faults ────────────────────────────────────────────────

// readerFaultRow names one production-producible gate-read fault, the diagnostic
// token its stderr line must carry, and the real store fixture that produces
// it.
type readerFaultRow struct {
	name  string
	token string
	seed  func(t *testing.T, dbPath string, harness ir.HarnessID, session string)
}

// readerFaultRows are the production-producible gate-read faults whose damaged
// state a REAL store can hold and a BUILT transport can reach within the fixed
// 5s hook-invocation budget. Two rows the
// built-column contract lists are NOT here, and the reason is
// pinned by TestReaderIntegrityRowsAreHandlerOnlyBecauseTheStoreRefusesTheDamage:
// the pinned Provenance open-time replay convergence check refuses the damage
// (an out-of-range phase, an owner projection that disagrees with the folded
// journal) before any gate read runs. The byte-limit row is NOT here either,
// for a different, honest reason recorded beside that subject: the byte-limit
// fault is produced by the READER (a store/reader property, not a transport
// property), and at the built layer the mandatory 8+ MiB read plus store open
// cannot be made deterministic inside the fixed 5s hook-invocation budget on a
// loaded runner; it is therefore pinned at the handler layer where the budget
// is injectable, by TestGateByteLimitOverRealStoreFaultsBeforeDurableWrite.
// What the built column gives up by that removal: NO built subject exercises
// an ownership-read fault anymore. The byte-limit row was the only row
// carrying "gate ownership read", the only built GateReadIntegrityError, and
// the only built fail-closed cell for one.
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
	seedReaderEpisode(t, dbPath, actor, provenance.PhaseUnscoped, false)
}

func seedReaderUnmappedPhase(t *testing.T, dbPath string, harness ir.HarnessID, session string) {
	t.Helper()
	actor := seedReaderClaim(t, dbPath, harness, session)
	task := seedReaderEpisode(t, dbPath, actor, provenance.PhaseUnscoped, true)
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
	task := seedReaderEpisode(t, dbPath, occupant, provenance.PhaseUnscoped, true)
	// TEST-ONLY FIXTURE BOUNDARY on the Provenance-owned task projection. The
	// owner column is written with the episode in one transaction, so a value
	// that disagrees with the active winner is the store damage this row names.
	db := rawBoundary(t, dbPath)
	_, err := db.Exec(`UPDATE tasks SET owner_id=? WHERE id=?`, claimActor.String(), task.String())
	require.NoError(t, err)
}

// TestReaderGateFaultRowsReachTheHostFaultPath drives every BUILT row in the
// fault mapping through each harness's real transport and asserts the fail-open
// host settlement: exact continuation bytes, one stage-naming diagnostic, one
// fault record, no consultation, and no Deny.
//
// WHAT IT VISITS: every row readerFaultRows declares, plus the held-writer
// deadline row, on each pinned harness.
// WHAT IT DOES NOT READ: the rows pinned handler-only by
// TestReaderIntegrityRowsAreHandlerOnlyBecauseTheStoreRefusesTheDamage, the
// byte-limit aggregate bound pinned at the handler layer by
// TestGateByteLimitOverRealStoreFaultsBeforeDurableWrite, or
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
				readerAssertTransportProperties(t, harness, run)

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
			runReaderDeadlineRow(t, binary, harness, false, false)
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
				readerAssertTransportProperties(t, harness, run)

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
			runReaderDeadlineRow(t, binary, harness, true, policy.Mode.BlocksByExitCode() && policy.Evidence.IsPresent())
		})
	}
}

// runReaderDeadlineRow holds the SQLite write lock while the real transport runs
// the gate, so the invocation reaches its hook-invocation deadline with the
// ownership read unanswerable. It is the built /deadline settlement proof.
//
// closed and expectBlock are SEPARATE because they answer different questions:
// closed sets PASTURE_HOOK_FAIL_CLOSED in the child, and expectBlock is what the
// runtime row says the child must do about it. Coupling them would make every
// non-blocking harness's fail-closed cell byte-identical to its default cell, so
// the cell would assert nothing.
func runReaderDeadlineRow(t *testing.T, binary string, harness readerHarnessCase, closed, expectBlock bool) {
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

	run := harness.run(t, binary, dbPath, raw, closed)
	unlock()
	readerAssertTransportProperties(t, harness, run)

	continuation, err := nativeresponse.FaultContinuation(harness.harness, harness.mapping.Semantic())
	require.NoError(t, err)
	wantContinuation, wantExit := string(continuation.Bytes()), 0
	if expectBlock {
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
// two rows the fault mapping lists in its built column have no built subject.
//
// It is not a convenience. Both rows' damage is a store state no supported
// writer produces, and Provenance's OPEN-TIME full replay convergence check
// (internal/sqlite/db.go:613,789; internal/sqlite/replay.go:480) refuses it
// before the hook opens the store: the data cannot exist in a store that opens.
// The hook reports a connection fault, not a gate fault, so no built subject can
// reach the reader's arm:
//
//   - an out-of-range phase travels with "provenance: projection divergence ...
//     field phase";
//   - an owner projection that disagrees with the folded journal travels with
//     "... field owner".
//
// The reader still owns those arms, and the handler subjects prove them with a
// wrapping API that can return the typed cause. This subject keeps the built
// column honest instead of publishing locators that cannot resolve.
//
// byte-limit is NOT here, for a different reason that is NOT about damage the
// store refuses. The byte-limit fault is produced by the READER (a store/reader
// property, not a transport property): ten legal ~900 KiB assignment materials
// cross the reader's AGGREGATE 8 MiB wire bound on a store the product's own
// writer produces. At the built layer the mandatory 8+ MiB read plus store open
// cannot be made deterministic inside the fixed 5s hook-invocation budget on a
// loaded runner; it is therefore pinned at the handler layer where the budget
// is injectable, by TestGateByteLimitOverRealStoreFaultsBeforeDurableWrite.
// What the built column gives up by that removal: NO built subject exercises
// an ownership-read fault anymore. The byte-limit row was the only row
// carrying "gate ownership read", the only built GateReadIntegrityError, and
// the only built fail-closed cell for one.
//
// WHAT IT VISITS: the two integrity rows whose damaged state cannot persist in a
// store that opens.
// WHAT IT DOES NOT READ: the gate reader's own error mapping, or the byte-limit
// aggregate bound; the handler
// subjects cover those, and this subject asserts only that the store refuses the
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
}

// TestReaderGateInvocationCostIsMeasuredWithoutACeiling records the measured
// elapsed of a real gate invocation on each built transport, split into the
// classes that ship. It makes NO wall-clock assertion: the cost contract reports
// measured elapsed only, because a ceiling would turn a loaded runner into a
// false defect.
//
// THE PER-CLASS RECONCILIATION IS HERE IN THE REPO, not only in a handoff. The
// classes that ship after this change, which supersede the earlier finer-grained
// class framing:
//
//  1. session-claim read — one indexed point SELECT on pasture_session_claim
//     (primary key harness,session) under the SQLiteBusy tier. Condition: any
//     gate event carrying a session identity; a sessionless event short-circuits
//     to unbound with no SQL. Measured: the `unbound gate` line below (claim
//     read + receipt, no ownership transaction).
//  2. ownership transaction — one deferred-BEGIN read snapshot with at most 5
//     statements, no paging loop and NO episode cap. Condition: a BOUND session
//     only. Measured: `bound gate` minus `unbound gate` below, i.e. the
//     ownership-transaction contribution.
//  3. session-start claim write — one INSERT ... ON CONFLICT DO NOTHING on
//     pasture_session_claim. Condition: the host's own session-start event with
//     an actor, once per session; never on a gate path. Measured separately
//     in-process with distinct sessions (the `session-start-claim-write` line).
//  4. first-open v8->v9 migration — one-time per store, on the first command
//     that opens a v8 file, reported on its own line and never averaged into
//     hook cost. Measured by the built-binary subjects in
//     cmd/pasture/migrate_v8_v9_cli_test.go, not here.
//
// The retired 64-episode measurement is replaced: the read has NO
// episode cap, so a bound session costs O(owned tasks), an unbound one O(1), and
// the only wire bound is MaxActorOwnershipResultBytes (8 MiB).
//
// WHAT IT VISITS: the three pinned gate events and their transports, plus the
// one-time claim write.
// WHAT IT DOES NOT READ: a wall-clock ceiling, or the v8->v9 migration (covered
// by its own built-binary subjects).
func TestReaderGateInvocationCostIsMeasuredWithoutACeiling(t *testing.T) {
	binary := lifecycleBinary(t)
	for _, harness := range readerHarnessCases(t) {
		t.Run(harness.label, func(t *testing.T) {
			raw := readerFixture(t, harness.fixture)
			boundPath := readerFreshStore(t)
			seedBoundActorOwningAMappedTask(t, boundPath, harness.harness, raw)
			unboundPath := readerFreshStore(t)

			measure := func(label, path string) time.Duration {
				const samples = 5
				var total time.Duration
				for sample := 0; sample < samples; sample++ {
					started := time.Now()
					run := harness.run(t, binary, path, raw, false)
					require.Equal(t, 0, run.ExitCode, run.Stderr)
					total += time.Since(started)
				}
				mean := total / samples
				t.Logf("cost: %s %s mean=%s over %d samples", harness.label, label, mean, samples)
				return mean
			}
			bound := measure("bound gate (claim read + ownership transaction + receipt)", boundPath)
			unbound := measure("unbound gate (claim read + receipt, no ownership)", unboundPath)
			t.Logf("cost: %s ownership-transaction contribution ~%s (bound - unbound)", harness.label, bound-unbound)
		})
	}
	t.Run("session-start-claim-write", func(t *testing.T) {
		dbPath := readerFreshStore(t)
		tracker, err := tasks.OpenTaskTracker(dbPath)
		require.NoError(t, err)
		defer func() { require.NoError(t, tracker.Close()) }()
		agent, err := tracker.RegisterHumanAgent("claim-write-cost", "claim writer", "claim@example.invalid")
		require.NoError(t, err)
		const samples = 25
		started := time.Now()
		for sample := 0; sample < samples; sample++ {
			require.NoError(t, tasks.RecordLifecycleSessionClaim(
				context.Background(), tracker, ir.HarnessClaudeCode, registration.EventSessionStart,
				[]model.NativeBinding{{Kind: model.BindingSession, Value: fmt.Sprintf("cost-session-%02d", sample)}},
				tasks.ActorClaim(agent.ID.String()), lifecycleCLIClock{}))
		}
		t.Logf("cost: session-start claim write (in-process, distinct sessions) mean=%s over %d samples",
			time.Since(started)/samples, samples)
	})
}
