package codegen

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func TestOpenCodeGeneratedOutputs_NoOperationalBd(t *testing.T) {
	desc, err := NewOpenCodeTargetDescriptor()
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	manifest, err := desc.Manifest()
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	bdRE := regexp.MustCompile(`\bbd\s+(create|update|close|dep|comments|show|ready|list)\b`)
	for name, content := range map[string]string{"hooks": desc.HooksModule(), "manifest": manifest} {
		if bdRE.MatchString(content) {
			t.Errorf("generated %s contains an operational bd command", name)
		}
	}
}

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// runOpenCodeV2Mechanics executes freshly generated production code against a
// real child process. The fake binary is a transport peer, not an encoder or
// a claim about host acceptance. helper names the exported gate helper under
// test; every invocation carries its child behaviour in the test-only __mode
// member of the hook event, read by the fake child out of band.
func runOpenCodeV2Mechanics(t *testing.T, helper, script string) string {
	t.Helper()
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("bun is required for the generated OpenCode child-process proof")
	}
	dir := t.TempDir()
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("generate production plugin: %v", err)
	}
	modulePath := filepath.Join(dir, "plugin.ts")
	if err := os.WriteFile(modulePath, []byte(module), 0o600); err != nil {
		t.Fatalf("write generated plugin: %v", err)
	}
	fakePath := filepath.Join(dir, "fake-pasture")
	fake := "#!" + bun + "\n" + `
import { closeSync } from "node:fs";
if (!process.env.PASTURE_DB_PATH) throw new Error("scratch database path is required");
const value = JSON.parse(await Bun.stdin.text());
const mode = value.__mode ?? "observation-stall";
if (mode === "reply") {
  process.stdout.write(value.__body);
  process.stderr.write(value.__diagnostic ?? "");
  process.exitCode = value.__exitCode ?? 0;
} else {
  // Keep the actual process alive without using a sleep to order the proof.
  Bun.serve({ port: 0, fetch() { return new Response("held"); } });
  if (mode === "exit-stall" || mode === "stderr-stall") closeSync(1);
  if (mode === "exit-stall" || mode === "stdout-stall") closeSync(2);
}
`
	if err := os.WriteFile(fakePath, []byte(fake), 0o700); err != nil {
		t.Fatalf("write actual fake child: %v", err)
	}
	runner := filepath.Join(dir, "proof.ts")
	preamble := fmt.Sprintf(`
import assert from "node:assert/strict";
import { %s } from %q;
const gate = %s;
const children = [];
const originalSpawn = Bun.spawn;
Bun.spawn = (options) => {
  const child = originalSpawn(options);
  const record = { child, kills: 0, reaped: false };
  children.push(record);
  const exited = child.exited.then((code) => {
    record.reaped = true;
    return code;
  });
  return new Proxy(child, {
    get(target, key) {
      if (key === "exited") return exited;
      if (key === "kill") return (...args) => {
        record.kills++;
        return target.kill(...args);
      };
      const value = Reflect.get(target, key, target);
      return typeof value === "function" ? value.bind(target) : value;
    },
  });
};
async function bounded(promise, label) {
  let timer;
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error(label + " exceeded its 12s condition bound")), 12000);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}
async function event(input) {
  const frozen = JSON.stringify(input);
  let failure;
  try {
    await bounded(gate(input), "whole-child completion");
  } catch (error) {
    failure = error;
  }
  assert.equal(JSON.stringify(input), frozen, "host event identity on every outcome");
  return failure;
}
try {
`, helper, modulePath, helper)
	cleanup := `
  console.log("mechanics assertions passed");
} finally {
  Bun.spawn = originalSpawn;
  // Also reap a timer-removal mutant after the named bounded assertion fails.
  for (const { child } of children) {
    if (child.exitCode === null) child.kill("SIGKILL");
    await child.exited;
  }
}
`
	if err := os.WriteFile(runner, []byte(preamble+script+cleanup), 0o600); err != nil {
		t.Fatalf("write Bun mechanics proof: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, runner)
	cmd.Env = append(os.Environ(), "PASTURE_BIN="+fakePath, "PASTURE_DB_PATH="+filepath.Join(dir, "pasture.db"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		t.Fatalf("generated OpenCode mechanics assertion failed: %v (outer bound: %v)\nstdout: %s\nstderr: %s", err, ctx.Err(), stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "mechanics assertions passed") {
		t.Fatalf("Bun did not reach the mechanics assertions: %s", stdout.String())
	}
	return stderr.String()
}

func TestOpenCodeGeneratedPluginClosedResponses(t *testing.T) {
	runOpenCodeV2Mechanics(t, "toolExecuteBefore", `
  const reason = "  role cannot write\nretain this exact reason  ";
  const denied = await event({ sessionID: "constructed", id: "call", __mode: "reply", __body: JSON.stringify({ decision: "deny", reason }) });
  assert(denied instanceof Error, "valid Deny rejects the callback");
  assert.equal(denied.message, reason, "valid Deny is reason-only, not installation advice");
  const invalid = [
    "not-json", "null", "[]", "[1]", "true", "7", '"proceed"', "{}",
    '{"decision":"unknown"}', '{"decision":null}', '{"decision":1}',
    '{"decision":"proceed","reason":"extra"}', '{"decision":"proceed","extra":true}',
    '{"decision":"deny"}', '{"decision":"deny","reason":""}',
    '{"decision":"deny","reason":null}', '{"decision":"deny","reason":7}',
    '{"decision":"deny","reason":true}', '{"decision":"deny","reason":[]}',
    '{"decision":"deny","reason":["x"]}', '{"decision":"deny","reason":{"length":1}}',
    '{"decision":"deny","reason":{}}', '{"decision":"deny","reason":"x","extra":0}',
    '{"reason":"missing decision"}',
  ];
  for (const body of invalid) {
    const failure = await event({ sessionID: "constructed", id: "call", __mode: "reply", __body: body });
    assert(failure instanceof Error, "invalid response accepted: " + body);
    assert.match(failure.message, /pasture hook lifecycle response/, "response fault, not a policy Deny: " + body);
    assert.match(failure.message, /tool.execute.before/, "fault names event: " + body);
    assert.match(failure.message, /verify PASTURE_BIN and the generated OpenCode/, "response fault configuration advice: " + body);
  }
  assert.equal(await event({ sessionID: "constructed", id: "call", __mode: "reply", __body: '{"decision":"proceed"}' }), undefined, "exact Proceed accepted");
  const nonzero = await event({ sessionID: "constructed", id: "call", __mode: "reply", __body: '{"decision":"deny","reason":"not a decision at nonzero"}', __diagnostic: "synthetic lifecycle diagnostic", __exitCode: 7 });
  assert.match(nonzero.message, /exited 7: synthetic lifecycle diagnostic; verify PASTURE_BIN and the generated OpenCode/, "nonzero remains invocation fault");
`)
}

func TestOpenCodeGeneratedPluginBoundsWholeChildAndReaps(t *testing.T) {
	runOpenCodeV2Mechanics(t, "toolExecuteBefore", `
  await Promise.all([...["stdout-stall", "stderr-stall", "exit-stall"].map(async (mode) => {
        const start = performance.now();
        const failure = await event({ sessionID: "constructed", id: "call", __mode: mode });
        assert(failure instanceof Error, mode + " must fault");
        assert.match(failure.message, /timed out after 8000 ms/, mode + " must hit the plugin timer, not the test bound");
        assert.match(failure.message, /verify PASTURE_BIN and the generated OpenCode/, mode + " retains configuration advice");
        assert(performance.now() - start >= 7900, "actual 8s timer was not shortened");
      })]);
  assert.equal(children.length, 3, "all stalled real children were invoked");
  for (const record of children) {
    assert(record.kills > 0, "stalled actual child was killed");
    assert(record.reaped, "callback waits for child reaping");
    assert.equal(record.child.signalCode, "SIGKILL", "force kill, not a cooperative exit");
    assert.throws(() => process.kill(record.child.pid, 0), { code: "ESRCH" }, "no surviving process or zombie");
  }
`)
}

func TestOpenCodeGeneratedPluginClearsTimerAfterCompletion(t *testing.T) {
	runOpenCodeV2Mechanics(t, "toolExecuteBefore", `
  const originalSet = globalThis.setTimeout;
  const originalClear = globalThis.clearTimeout;
  const pending = new Map();
  let scheduled = 0;
  globalThis.setTimeout = (callback, ms, ...args) => {
    const handle = originalSet(callback, ms, ...args);
    if (ms === 8000) {
      scheduled++;
      pending.set(handle, () => callback(...args));
    }
    return handle;
  };
  globalThis.clearTimeout = (handle) => {
    pending.delete(handle);
    originalClear(handle);
  };
  try {
    for (const exitCode of [0, 7]) {
      const failure = await event({ sessionID: "constructed", id: "call", __mode: "reply", __body: '{"decision":"proceed"}', __exitCode: exitCode });
      assert.equal(Boolean(failure), exitCode !== 0, "healthy/nonzero outcome before timer cleanup check");
      const record = children.at(-1);
      assert(record.reaped, "completed child is reaped");
      // Advance only still-live timer callbacks after completion. This is a
      // deterministic late-kill negative control, not a sleep-based ordering.
      for (const [handle, fire] of pending) {
        originalClear(handle);
        fire();
      }
      assert.equal(record.kills, 0, "timer must not kill after healthy or nonzero completion");
      assert.equal(pending.size, 0, "whole-child timer cleared on resolution and rejection");
    }
    assert.equal(scheduled, 2, "one real 8s timer per child");
  } finally {
    for (const handle of pending.keys()) originalClear(handle);
    globalThis.setTimeout = originalSet;
    globalThis.clearTimeout = originalClear;
  }
`)
}

func TestOpenCodeGeneratedPluginDrainsBothPipesVerbatim(t *testing.T) {
	stderr := runOpenCodeV2Mechanics(t, "toolExecuteBefore", `
  // Both streams exceed ordinary pipe capacity. Sequential draining would
  // deadlock a writer that fills stderr before closing stdout.
  const diagnostic = "  diagnostic α\n".repeat(32768);
  const failure = await event({
    sessionID: "constructed", id: "call",
    __mode: "reply",
    __body: " ".repeat(262144) + '{"decision":"proceed"}',
    __diagnostic: diagnostic,
  });
  assert.equal(failure, undefined, "both large streams drain before the 8s bound");
  assert(children[0].reaped, "healthy child reaped before return");
  assert.equal(children[0].kills, 0, "healthy child was not killed");
`)
	want := strings.Repeat("  diagnostic α\n", 32768)
	if stderr != want {
		t.Fatalf("child diagnostic forwarding changed bytes: got %d bytes, want exact %d bytes", len(stderr), len(want))
	}
}

func TestOpenCodeGeneratedPluginForwardingFailureCleansListeners(t *testing.T) {
	runOpenCodeV2Mechanics(t, "toolExecuteBefore", `
  const { Writable } = await import("node:stream");
  const original = Object.getOwnPropertyDescriptor(process, "stderr");
  for (const mode of ["failed-write", "closed", "close-during-write"]) {
    const sink = new Writable({
      write(chunk, encoding, callback) {
        if (mode === "close-during-write") {
          this.destroy();
          return;
        }
        callback(new Error("controlled diagnostic sink failure"));
      },
    });
    if (mode === "closed") {
      await new Promise((resolve) => {
        sink.once("close", resolve);
        sink.destroy();
      });
    }
    let failure;
    let listeners;
    try {
      Object.defineProperty(process, "stderr", { configurable: true, value: sink });
      failure = await event({ sessionID: "constructed", id: "call", __mode: "reply", __body: '{"decision":"proceed"}', __diagnostic: "must not disappear" });
      listeners = [sink.listenerCount("error"), sink.listenerCount("close")];
    } finally {
      Object.defineProperty(process, "stderr", original);
      sink.destroy();
    }
    assert(failure instanceof Error, mode + " must reject, not silently proceed");
    assert.match(failure.message, /diagnostic forwarding for tool.execute.before failed/, mode + " identifies forwarding fault");
    assert.match(failure.message, /restore the OpenCode standard-error sink and retry/, mode + " gives sink repair advice");
    assert.deepEqual(listeners, [0, 0], mode + " leaves no error or close listeners");
    assert(children.at(-1).reaped, mode + " has already reaped the healthy child");
    assert.equal(children.at(-1).kills, 0, mode + " does not kill a completed child");
  }
`)
}

func TestOpenCodeGeneratedPluginEmptyBodyDiagnostic(t *testing.T) {
	stderr := runOpenCodeV2Mechanics(t, "toolExecuteBefore", `
  const logged = [];
  const originalError = console.error;
  console.error = (...values) => logged.push(values.join(" "));
  let failure;
  try {
    failure = await event({ sessionID: "constructed", id: "call", __mode: "reply", __body: " \n\t", __diagnostic: "  old binary diagnostic α" });
  } finally {
    console.error = originalError;
  }
  assert.equal(failure, undefined, "empty-body belt continues unevaluated");
  assert.equal(logged.length, 1, "empty-body belt logs once");
  assert.match(logged[0], /did not evaluate tool.execute.before/, "empty-body diagnostic names event");
  assert.match(logged[0], /Read the pasture diagnostic on standard error first/, "empty-body diagnostic directs operator to forwarded bytes");
`)
	if stderr != "  old binary diagnostic α\n" {
		t.Fatalf("empty-body diagnostic = %q, want exact child bytes plus the existing terminal newline", stderr)
	}
}

func TestOpenCodeGeneratedPluginBoundsPipesAfterExit(t *testing.T) {
	runOpenCodeV2Mechanics(t, "toolExecuteBefore", `
  const spawn = Bun.spawn;
  const canceled = [];
  Bun.spawn = (options) => {
    const child = spawn(options);
    const index = children.length - 1;
    const pipe = index === 0 ? "stdout" : "stderr";
    // An externally held pipe after a real child exits. Stream cancellation
    // is observable independently of child.exited; no descendant is orphaned.
    const held = new ReadableStream({ cancel() { canceled.push(pipe); } });
    return new Proxy(child, {
      get(target, key) {
        if (key === pipe) return held;
        return Reflect.get(target, key);
      },
    });
  };
  await Promise.all([0, 1].map(async () => {
    const failure = await event({ sessionID: "constructed", id: "call", __mode: "reply", __body: '{"decision":"proceed"}' });
    assert.match(failure?.message ?? "", /timed out after 8000 ms/, "drains remain bounded after actual exit");
  }));
  assert.deepEqual(canceled.sort(), ["stderr", "stdout"], "both stuck readers canceled");
  for (const record of children) {
    assert(record.reaped, "already exited child was still reaped");
    assert.equal(record.child.exitCode, 0, "direct child actually exited normally before pipe timeout");
  }
`)
}

func TestOpenCodeGeneratedPluginCleansUpReadFailure(t *testing.T) {
	runOpenCodeV2Mechanics(t, "toolExecuteBefore", `
  const originalSet = globalThis.setTimeout;
  const originalClear = globalThis.clearTimeout;
  const timers = new Set();
  globalThis.setTimeout = (callback, ms, ...args) => {
    const handle = originalSet(callback, ms, ...args);
    if (ms === 8000) timers.add(handle);
    return handle;
  };
  globalThis.clearTimeout = (handle) => {
    timers.delete(handle);
    originalClear(handle);
  };
  const spawn = Bun.spawn;
  let canceled = false;
  Bun.spawn = (options) => {
    const child = spawn(options);
    const broken = new ReadableStream({ start(controller) { controller.error(new Error("synthetic read failure")); } });
    const held = new ReadableStream({ cancel() { canceled = true; } });
    return new Proxy(child, {
      get(target, key) {
        if (key === "stdout") return broken;
        if (key === "stderr") return held;
        return Reflect.get(target, key);
      },
    });
  };
  try {
    const failure = await event({ sessionID: "constructed", id: "call", __mode: "exit-stall" });
    assert.match(failure?.message ?? "", /synthetic read failure; verify PASTURE_BIN/, "read failure is an invocation fault");
    assert(canceled, "other pending reader canceled on rejection");
    assert(children[0].reaped, "read failure reaps actual child");
    assert.equal(children[0].child.signalCode, "SIGKILL", "read failure kills actual child");
    assert.equal(timers.size, 0, "rejected drain clears timer");
  } finally {
    for (const handle of timers) originalClear(handle);
    globalThis.setTimeout = originalSet;
    globalThis.clearTimeout = originalClear;
  }
`)
}

// TestOpenCodeV2SwallowingHelpersReportAndContinue proves the v2 throw
// discipline for every gate helper but tool.execute.before and the v2
// enforcement discipline for the permission hook: an invocation fault is
// reported on the console and continued, never thrown, because those hooks'
// failure channel is never. The four non-permission helpers below also log
// an unenforced denial and continue without touching their host event,
// because their hooks carry no typed refusal channel. The permission helper
// instead enforces a Denial by assigning the host evaluation's effect and
// message, and leaves every other answer untouched.
func TestOpenCodeV2SwallowingHelpersReportAndContinue(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("bun is required for the generated OpenCode failure-path proof; enter the flake dev shell")
	}
	dir := t.TempDir()
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	modulePath := filepath.Join(dir, "plugin.ts")
	if err := os.WriteFile(modulePath, []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeBinary := filepath.Join(dir, "fake-pasture")
	fake := `#!/bin/sh
payload=$(cat)
case "$payload" in
  *'"__mode":"malformed"'*) printf '%s' 'not-json' ;;
  *'"__mode":"extra"'*) printf '%s' '{"decision":"proceed","extra":true}' ;;
  *'"__mode":"wrong-decision"'*) printf '%s' '{"decision":"block"}' ;;
  *'"__mode":"nonzero"'*) printf '%s' 'synthetic lifecycle diagnostic' >&2; exit 7 ;;
  *'"__mode":"deny"'*) printf '%s' '{"decision":"deny","reason":"role cannot write"}' ;;
  *) printf '%s' '{"decision":"proceed"}' ;;
esac
`
	if err := os.WriteFile(fakeBinary, []byte(fake), 0o700); err != nil {
		t.Fatalf("write bounded fake PASTURE_BIN: %v", err)
	}
	runner := filepath.Join(dir, "swallow-proof.ts")
	script := fmt.Sprintf(`
import { sessionPrompt, sessionContext, toolExecuteAfter, permissionEvaluate, shellCreateBefore, sessionCreated } from %q;

const logged = [];
const originalError = console.error;
console.error = (...values) => logged.push(values.join(" "));

const frozen = (value) => {
  const before = JSON.stringify(value);
  return () => {
    if (JSON.stringify(value) !== before) throw new Error("a consultation mutated its host event: " + before);
  };
};

for (const helper of [sessionPrompt, sessionContext, toolExecuteAfter, shellCreateBefore]) {
  for (const mode of ["malformed", "extra", "wrong-decision", "nonzero", "deny"]) {
    const hookEvent = { sessionID: "constructed", id: "call", __mode: mode };
    const check = frozen(hookEvent);
    await helper(hookEvent);
    check();
  }
}

// A non-denial leaves the permission evaluation exactly as the host set
// it: effect stays allow and no message member appears.
for (const mode of ["malformed", "extra", "wrong-decision", "nonzero"]) {
  const evaluation = { sessionID: "constructed", action: "edit", resources: ["file"], effect: "allow", __mode: mode };
  const check = frozen(evaluation);
  await permissionEvaluate(evaluation);
  check();
  if (evaluation.effect !== "allow" || "message" in evaluation) {
    throw new Error(mode + " mutated the host evaluation");
  }
}

// A pasture Denial is enforced through the host's typed channel: effect
// becomes deny and message carries the durable reason verbatim. The fake
// binary's reason is asserted exactly so a rewrite turns red.
{
  const evaluation = { sessionID: "constructed", action: "edit", resources: ["file"], effect: "allow", __mode: "deny" };
  await permissionEvaluate(evaluation);
  if (evaluation.effect !== "deny") {
    throw new Error("a pasture denial left the host effect at " + JSON.stringify(evaluation.effect));
  }
  if (evaluation.message !== "role cannot write") {
    throw new Error("a pasture denial carried message " + JSON.stringify(evaluation.message));
  }
}

// An observation failure is swallowed and logged like any other fault here.
await sessionCreated({ type: "session.created", data: { sessionID: "constructed" }, __mode: "nonzero" });

console.error = originalError;
const gateFaults = logged.filter((line) => line.includes("gate consultation failed"));
const unenforced = logged.filter((line) => line.includes("does not enforce"));
const observations = logged.filter((line) => line.includes("observation failed for session.created"));
if (gateFaults.length !== 20) throw new Error("expected 20 gate fault reports, got " + gateFaults.length + ": " + JSON.stringify(logged));
if (unenforced.length !== 4) throw new Error("expected 4 unenforced-denial reports (the permission denial is enforced, not logged), got " + unenforced.length);
if (observations.length !== 1) throw new Error("expected 1 observation report, got " + observations.length);
console.log(JSON.stringify({ swallowed: true }));
`, "file://"+modulePath)
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatalf("write Bun swallow-proof runner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	proof := exec.CommandContext(ctx, bun, runner)
	proof.Env = append(os.Environ(), "PASTURE_BIN="+fakeBinary, "PASTURE_DB_PATH="+filepath.Join(dir, "pasture.db"))
	output, err := proof.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("Bun swallow-path proof exceeded its 20s bound: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("execute generated OpenCode swallow paths under Bun: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != `{"swallowed":true}` {
		t.Fatalf("Bun swallow-path proof output = %q, want all report-and-continue assertions", output)
	}
}

// TestOpenCodePermissionEvaluateDenyMatrix is the Bun-driven deny proof for
// the enforceable permission channel: it drives the generated
// permissionEvaluate helper through a stub gate binary across the full
// answer matrix and pins the host-object semantics of each arm. A Denial
// assigns effect deny and the durable reason verbatim, replacing any draft
// the host carried; a proceed, the empty-body unevaluated belt, and an
// invocation fault all leave the evaluation exactly as the host set it; and
// a Denial against an unwritable object reports inside the guarded region
// instead of escaping as a throw.
func TestOpenCodePermissionEvaluateDenyMatrix(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("bun is required for the generated OpenCode deny proof; enter the flake dev shell")
	}
	dir := t.TempDir()
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	modulePath := filepath.Join(dir, "plugin.ts")
	if err := os.WriteFile(modulePath, []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeBinary := filepath.Join(dir, "fake-pasture")
	fake := `#!/bin/sh
payload=$(cat)
case "$payload" in
  *'"__mode":"deny"'*) printf '%s' '{"decision":"deny","reason":"role cannot write"}' ;;
  *'"__mode":"empty"'*) printf '%s' '' ;;
  *'"__mode":"nonzero"'*) printf '%s' 'synthetic lifecycle diagnostic' >&2; exit 7 ;;
  *) printf '%s' '{"decision":"proceed"}' ;;
esac
`
	if err := os.WriteFile(fakeBinary, []byte(fake), 0o700); err != nil {
		t.Fatalf("write bounded fake PASTURE_BIN: %v", err)
	}
	runner := filepath.Join(dir, "deny-matrix.ts")
	script := fmt.Sprintf(`
import { permissionEvaluate } from %q;
import assert from "node:assert/strict";

const logged = [];
const originalError = console.error;
console.error = (...values) => logged.push(values.join(" "));

// A Denial assigns the typed channel members with the durable reason verbatim.
{
  const evaluation = { sessionID: "constructed", action: "edit", resources: ["file"], effect: "allow", __mode: "deny" };
  await permissionEvaluate(evaluation);
  assert.equal(evaluation.effect, "deny");
  assert.equal(evaluation.message, "role cannot write");
}

// A Denial replaces the host's own draft: the gate verdict, not the
// incoming effect or message, decides.
{
  const evaluation = { sessionID: "constructed", action: "edit", resources: ["file"], effect: "ask", message: "host draft", __mode: "deny" };
  await permissionEvaluate(evaluation);
  assert.equal(evaluation.effect, "deny");
  assert.equal(evaluation.message, "role cannot write");
}

// A proceed assigns nothing: the object round-trips deep-equal.
{
  const evaluation = { sessionID: "constructed", action: "edit", resources: ["file"], effect: "allow", __mode: "proceed" };
  const before = JSON.stringify(evaluation);
  await permissionEvaluate(evaluation);
  assert.equal(JSON.stringify(evaluation), before);
  assert.equal("message" in evaluation, false);
}

// The empty-body unevaluated belt assigns nothing and reports on the console.
{
  const evaluation = { sessionID: "constructed", action: "edit", resources: ["file"], effect: "allow", __mode: "empty" };
  const before = JSON.stringify(evaluation);
  await permissionEvaluate(evaluation);
  assert.equal(JSON.stringify(evaluation), before);
  assert.equal("message" in evaluation, false);
}

// An invocation fault assigns nothing and reports on the console.
{
  const evaluation = { sessionID: "constructed", action: "edit", resources: ["file"], effect: "allow", __mode: "nonzero" };
  const before = JSON.stringify(evaluation);
  await permissionEvaluate(evaluation);
  assert.equal(JSON.stringify(evaluation), before);
  assert.equal("message" in evaluation, false);
}

// An unwritable host evaluation must not escape as a throw: the deny
// assignment lives inside the guarded region, so a frozen object reports and
// continues with its effect untouched. A mutant that moves the assignment
// outside the try turns this arm into a rejection.
{
  const evaluation = Object.freeze({ sessionID: "constructed", action: "edit", resources: ["file"], effect: "allow", __mode: "deny" });
  await permissionEvaluate(evaluation);
  assert.equal(evaluation.effect, "allow");
  assert.equal("message" in evaluation, false);
}

console.error = originalError;
const faults = logged.filter((line) => line.includes("gate consultation failed for permission.evaluate"));
assert.equal(faults.length, 2, "the nonzero invocation and the frozen deny fault, got " + JSON.stringify(logged));
console.log(JSON.stringify({ denied: true }));
`, "file://"+modulePath)
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatalf("write Bun deny-matrix runner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	proof := exec.CommandContext(ctx, bun, runner)
	proof.Env = append(os.Environ(), "PASTURE_BIN="+fakeBinary, "PASTURE_DB_PATH="+filepath.Join(dir, "pasture.db"))
	output, err := proof.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("Bun deny-matrix proof exceeded its 20s bound: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("execute generated OpenCode permission deny matrix under Bun: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != `{"denied":true}` {
		t.Fatalf("Bun deny-matrix proof output = %q, want the full mutation matrix", output)
	}
}

// TestOpenCodeGeneratedLifecycleCallbacks_RejectInvalidGateResponsesAndSwallowObservationFailure
// proves the throwing gate rejects every non-conforming response with an
// actionable diagnostic while the observation swallows and logs its failure.
func TestOpenCodeGeneratedLifecycleCallbacks_RejectInvalidGateResponsesAndSwallowObservationFailure(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("bun is required for the generated OpenCode failure-path proof; enter the flake dev shell")
	}
	dir := t.TempDir()
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	modulePath := filepath.Join(dir, "plugin.ts")
	if err := os.WriteFile(modulePath, []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeBinary := filepath.Join(dir, "fake-pasture")
	fake := `#!/bin/sh
payload=$(cat)
case "$payload" in
  *'"__mode":"malformed"'*) printf '%s' 'not-json' ;;
  *'"__mode":"extra"'*) printf '%s' '{"decision":"proceed","extra":true}' ;;
  *'"__mode":"wrong-decision"'*) printf '%s' '{"decision":"block"}' ;;
  *'"__mode":"nonzero"'*) printf '%s' 'synthetic lifecycle diagnostic' >&2; exit 7 ;;
  *) printf '%s' '{"decision":"proceed"}' ;;
esac
`
	if err := os.WriteFile(fakeBinary, []byte(fake), 0o700); err != nil {
		t.Fatalf("write bounded fake PASTURE_BIN: %v", err)
	}

	runner := filepath.Join(dir, "failure-proof.ts")
	script := fmt.Sprintf(`
import { sessionCreated, toolExecuteBefore } from %q;

const cases = [
  { mode: "malformed", diagnostic: "response is not JSON" },
  { mode: "extra", diagnostic: 'response must be exactly {"decision":"proceed"}' },
  { mode: "wrong-decision", diagnostic: 'response must be exactly {"decision":"proceed"}' },
  { mode: "nonzero", diagnostic: "exited 7: synthetic lifecycle diagnostic" },
];
for (const testCase of cases) {
  const hookEvent = { sessionID: "constructed", id: "call", __mode: testCase.mode };
  const before = JSON.stringify(hookEvent);
  let diagnostic = "";
  try {
    await toolExecuteBefore(hookEvent);
  } catch (error) {
    diagnostic = String(error);
  }
  if (!diagnostic.includes(testCase.diagnostic)) {
    throw new Error(testCase.mode + " did not reject actionably; got: " + diagnostic);
  }
  if (JSON.stringify(hookEvent) !== before) {
    throw new Error(testCase.mode + " mutated the host event on rejection");
  }
}

const logged = [];
const originalError = console.error;
console.error = (...values) => logged.push(values.join(" "));
try {
  await sessionCreated({ type: "session.created", data: { sessionID: "constructed" }, __mode: "nonzero" });
} finally {
  console.error = originalError;
}
if (logged.length !== 1 || !logged[0].includes("observation failed for session.created") ||
    !logged[0].includes("exited 7: synthetic lifecycle diagnostic")) {
  throw new Error("session.created did not swallow and log its observation failure: " + JSON.stringify(logged));
}
console.log(JSON.stringify({ rejected: cases.length, observationLogged: true }));
`, "file://"+modulePath)
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatalf("write Bun failure-proof runner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	proof := exec.CommandContext(ctx, bun, runner)
	proof.Env = append(os.Environ(), "PASTURE_BIN="+fakeBinary, "PASTURE_DB_PATH="+filepath.Join(dir, "pasture.db"))
	output, err := proof.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("Bun failure-path proof exceeded its 20s bound: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("execute generated OpenCode failure paths under Bun: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != `{"rejected":4,"observationLogged":true}` {
		t.Fatalf("Bun failure-path proof output = %q, want all rejection and observation assertions", output)
	}
}

// TestOpenCodeHooksModule_ParsesUnderBun makes Bun a required gate dependency.
func TestOpenCodeHooksModule_ParsesUnderBun(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("bun is required to validate the generated OpenCode lifecycle plugin; enter the flake dev shell or install the flake-locked Bun package")
	}
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// The module lives under a plugin/ directory alone; write only itself so the
	// parse exercises isolated loading with no sibling present.
	path := filepath.Join(t.TempDir(), "pasture-hooks.ts")
	if err := os.WriteFile(path, []byte(module), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	out, err := exec.Command(bun, "--check", path).CombinedOutput()
	if err != nil {
		t.Fatalf("bun --check rejected the isolated module: %v\n%s", err, out)
	}
}

// TestOpenCodeV2TransportThroughBuiltCLIIsUnevaluatedBeforeProofs drives the
// generated v2 callbacks through the real built binary with constructed v2
// payloads. shell.create.before is the only coordinate still without proofs,
// so the handler refuses it as withheld before reading a byte: the host
// receives its continue bytes with exit 0 and a diagnostic. The enabled
// tool.execute.before gate faults open on the uninitialized store. No receipt
// exists afterwards: a row without proofs evaluates nothing.
func TestOpenCodeV2TransportThroughBuiltCLIIsUnevaluatedBeforeProofs(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("bun is required for the generated OpenCode production proof; enter the flake dev shell")
	}
	root := testModuleRoot(t)
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	build := exec.Command("go", "build", "-o", binary, "./cmd/pasture")
	build.Dir = root
	build.Env = append(build.Environ(), "CGO_ENABLED=0")
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build production pasture CLI: %v\n%s", buildErr, output)
	}

	moduleURL := (&url.URL{Scheme: "file", Path: copyCommittedModuleToTemp(t, dir)}).String()
	runner := filepath.Join(dir, "production-proof.ts")
	script := fmt.Sprintf(`
import { shellCreateBefore, toolExecuteBefore } from %q;
await shellCreateBefore({ sessionID: "constructed-2.0.20" });
const hookEvent = { tool: "task", sessionID: "constructed-2.0.20", agent: "agent", messageID: "message", id: "call-2.0.20", input: { path: "unchanged" } };
const before = JSON.stringify(hookEvent);
await toolExecuteBefore(hookEvent);
if (JSON.stringify(hookEvent) !== before) throw new Error("an unevaluated event mutated its host event");
console.log(JSON.stringify({ forwarded: true }));
`, moduleURL)
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatalf("write Bun production-proof runner: %v", err)
	}
	dbPath := filepath.Join(dir, "pasture.db")
	versionPath := filepath.Join(dir, "bin")
	if err := os.Mkdir(versionPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionPath, "opencode"), []byte("#!/bin/sh\nprintf '2.0.20\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var proofOut, proofErr bytes.Buffer
	proof := exec.Command(bun, runner)
	proof.Env = append(os.Environ(), "PASTURE_BIN="+binary, "PASTURE_DB_PATH="+dbPath,
		"PATH="+versionPath+":"+os.Getenv("PATH"), "PASTURE_CAPTURE_DIR=", "PASTURE_ACTOR_ID=", "PASTURE_HOOK_FAIL_CLOSED=")
	proof.Stdout = &proofOut
	proof.Stderr = &proofErr
	if err := proof.Run(); err != nil {
		t.Fatalf("execute generated OpenCode callbacks through built CLI: %v\nstdout: %s\nstderr: %s", err, proofOut.String(), proofErr.String())
	}
	if strings.TrimSpace(proofOut.String()) != `{"forwarded":true}` {
		t.Fatalf("Bun proof stdout = %q, want the forward confirmation and nothing else", proofOut.String())
	}
	// The withheld gate's refusal arrives as the host's continue bytes with a
	// diagnostic naming the withheld reason; the enabled gate faults on the
	// uninitialized store and also continues with a diagnostic.
	for _, diagnostic := range []string{
		`withheld (reason unclearable-payload)`,
		`tool.execute.before`,
		`shell.create.before`,
	} {
		if !strings.Contains(proofErr.String(), diagnostic) {
			t.Errorf("withheld diagnostic lacks %q: %s", diagnostic, proofErr.String())
		}
	}
	// Refused before a byte was read: no occurrence exists for either event.
	readback := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "list", "--format", "json")
	readbackOutput, err := readback.CombinedOutput()
	if err != nil {
		t.Fatalf("read back receipts through production CLI: %v\n%s", readbackOutput, err)
	}
	if strings.Contains(string(readbackOutput), "constructed-2.0.20") {
		t.Fatalf("a withheld event left a receipt: %s", readbackOutput)
	}
}

// TestOpenCodeV2GateSurvivesRealFaultsWithoutEvaluation is the fail-open
// proof, end to end: the REAL built binary, REAL faults, and the REAL
// generated plugin under Bun. No 2.0.20 row is admitted, so the deepest fault
// the gate path can reach before proofs land is version resolution with no
// host executable and no caller-supplied version; the observation leg reaches
// its withheld refusal through its occurrence-local version. Under the
// fail-open default the host continues in both cases, and under the
// fail-closed opt-in it ALSO continues on this harness, because that opt-in
// refuses through the process exit code and OpenCode's named callbacks do not
// refuse that way. Both are asserted here so neither can regress silently.
func TestOpenCodeV2GateSurvivesRealFaultsWithoutEvaluation(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("bun is required for the generated OpenCode fail-open proof; enter the flake dev shell")
	}
	root := testModuleRoot(t)
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	build := exec.Command("go", "build", "-o", binary, "./cmd/pasture")
	build.Dir = root
	build.Env = append(build.Environ(), "CGO_ENABLED=0")
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build production pasture CLI: %v\n%s", buildErr, output)
	}

	moduleURL := (&url.URL{Scheme: "file", Path: copyCommittedModuleToTemp(t, dir)}).String()
	runner := filepath.Join(dir, "fail-open-proof.ts")
	script := fmt.Sprintf(`
import { sessionCreated, toolExecuteBefore } from %q;
// No host executable is on PATH and no version flag is passed: version
// resolution fails before admission, capture or storage.
const hookEvent = { tool: "task", sessionID: "constructed", agent: "agent", messageID: "message", id: "call", input: { path: "unchanged" } };
const before = JSON.stringify(hookEvent);
await toolExecuteBefore(hookEvent);
if (JSON.stringify(hookEvent) !== before) {
  throw new Error("a pasture fault changed the host event");
}
// The observation carries its own occurrence-local version and reaches the
// withheld refusal instead.
await sessionCreated({ type: "session.created", data: { sessionID: "constructed", version: "2.0.20" } });
console.log(JSON.stringify({ hostContinued: true }));
`, moduleURL)
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatalf("write Bun fail-open proof runner: %v", err)
	}

	emptyPath := filepath.Join(dir, "empty-bin")
	if err := os.Mkdir(emptyPath, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []struct {
		name string
		env  []string
	}{
		{name: "the fail-open default", env: nil},
		{name: "the fail-closed opt-in, which has no channel on this harness", env: []string{"PASTURE_HOOK_FAIL_CLOSED=1"}},
	} {
		policy := policy
		t.Run(policy.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			proof := exec.CommandContext(ctx, bun, runner)
			proof.Env = append(os.Environ(), "PASTURE_BIN="+binary, "PASTURE_DB_PATH="+filepath.Join(dir, "pasture.db"),
				"PATH="+emptyPath, "PASTURE_CAPTURE_DIR=", "PASTURE_ACTOR_ID=", "PASTURE_HOOK_FAIL_CLOSED=")
			proof.Env = append(proof.Env, policy.env...)
			var proofOut, proofErr bytes.Buffer
			proof.Stdout = &proofOut
			proof.Stderr = &proofErr
			err := proof.Run()
			if ctx.Err() != nil {
				t.Fatalf("Bun fail-open proof exceeded its 60s bound: %v\nstdout: %s\nstderr: %s", ctx.Err(), proofOut.String(), proofErr.String())
			}
			if err != nil {
				t.Fatalf("a real pasture fault stopped the generated gate callback: %v\nstdout: %s\nstderr: %s", err, proofOut.String(), proofErr.String())
			}
			if strings.TrimSpace(proofOut.String()) != `{"hostContinued":true}` {
				t.Fatalf("Bun fail-open stdout = %q, want the continue confirmation and nothing else", proofOut.String())
			}
			if !strings.Contains(proofErr.String(), "host version resolution failed") || !strings.Contains(proofErr.String(), "session.created") {
				t.Fatalf("the fault diagnostics must carry the version fault and the withheld observation: %s", proofErr.String())
			}
		})
	}
}

// TestOpenCodeGeneratedPluginContinuesOnAnEmptyBody is the GENERATOR BELT.
//
// The defect: a pasture fault used to exit 0 with an EMPTY standard output, and
// the generated plugin ran JSON.parse("") on a NAMED callback, which throws. A
// throw inside tool.execute.before is the OpenCode blocking channel, so a
// pasture internal fault stopped the user's tool call — the exact opposite of
// the fail-open default.
//
// The Go fix emits the host's proceed bytes, so a CURRENT binary no longer
// produces an empty body. This belt covers the OTHER half: an OLD binary, or
// any future path that returns nothing, must not abort a tool call either. The
// plugin therefore reads "exit 0 with an empty body" as "not evaluated,
// continue", and keeps its throw for a NON-EMPTY body it cannot accept.
func TestOpenCodeGeneratedPluginContinuesOnAnEmptyBody(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("bun is required for the generated OpenCode fail-open belt proof; enter the flake dev shell")
	}
	dir := t.TempDir()
	fakeBinary := filepath.Join(dir, "fake-pasture")
	// An OLD pasture: exit 0, a diagnostic on stderr, and NOTHING on stdout.
	fake := `#!/bin/sh
cat >/dev/null
printf '%s' 'pasture could not evaluate this lifecycle hook event' >&2
exit 0
`
	if err := os.WriteFile(fakeBinary, []byte(fake), 0o700); err != nil {
		t.Fatalf("write bounded fake PASTURE_BIN: %v", err)
	}

	moduleURL := (&url.URL{Scheme: "file", Path: copyCommittedModuleToTemp(t, dir)}).String()
	runner := filepath.Join(dir, "fail-open-belt.ts")
	script := fmt.Sprintf(`
import { toolExecuteBefore } from %q;

const hookEvent = { sessionID: "constructed", id: "call", __mode: "empty-body" };
const before = JSON.stringify(hookEvent);
const logged = [];
const originalError = console.error;
console.error = (...values) => logged.push(values.join(" "));
try {
  await toolExecuteBefore(hookEvent);
} finally {
  console.error = originalError;
}
if (JSON.stringify(hookEvent) !== before) {
  throw new Error("an unevaluated event mutated its host event");
}
if (logged.length !== 1 || !logged[0].includes("did not evaluate tool.execute.before")) {
  throw new Error("the callback did not report the unevaluated event: " + JSON.stringify(logged));
}
console.log(JSON.stringify({ continued: true }));
`, moduleURL)
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatalf("write Bun fail-open belt runner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	proof := exec.CommandContext(ctx, bun, runner)
	proof.Env = append(os.Environ(), "PASTURE_BIN="+fakeBinary, "PASTURE_DB_PATH="+filepath.Join(dir, "pasture.db"))
	// THE STREAMS ARE READ APART, AND THIS USED TO BE CombinedOutput. The
	// assertion below required the combined bytes to EQUAL the confirmation, so
	// the contract it stated was "the plugin emits nothing on any stream except
	// its own stdout confirmation" — and that contract was the defect. The
	// plugin spawns pasture with stderr piped rather than inherited, so the
	// child's diagnostic reaches no stream unless the plugin forwards it, while
	// the belt line it prints tells the operator to READ that diagnostic on
	// standard error. Combined bytes cannot tell a confirmation from a
	// diagnostic, so this proof could not have distinguished the behaviour that
	// is wanted from the behaviour that was there.
	//
	// The stdout requirement is UNCHANGED and still exact: the host-facing
	// confirmation is the whole of it and nothing may join it there.
	var proofOut, proofErr bytes.Buffer
	proof.Stdout = &proofOut
	proof.Stderr = &proofErr
	err = proof.Run()
	if ctx.Err() != nil {
		t.Fatalf("Bun fail-open belt proof exceeded its 20s bound: %v\nstdout: %s\nstderr: %s",
			ctx.Err(), proofOut.String(), proofErr.String())
	}
	if err != nil {
		t.Fatalf("an empty body aborted the generated named callback: %v\nstdout: %s\nstderr: %s",
			err, proofOut.String(), proofErr.String())
	}
	if strings.TrimSpace(proofOut.String()) != `{"continued":true}` {
		t.Fatalf("Bun fail-open belt stdout = %q, want the continue confirmation and nothing else",
			proofOut.String())
	}
	// THE DIAGNOSTIC THE BELT SENDS THE OPERATOR TO MUST BE ON THE STREAM IT
	// NAMES. The fake writes it on standard error at exit 0, which is the exact
	// shape the belt exists for; the plugin captures it through the pipe and it
	// is gone unless invokeLifecycle puts it back on a stream. Without the
	// forward, this proof stayed green while an operator who followed the
	// printed instruction found nothing and could not tell a record-written
	// fault from a record-lost one.
	if !strings.Contains(proofErr.String(), "pasture could not evaluate this lifecycle hook event") {
		t.Fatalf("the generated plugin must FORWARD the child's diagnostic to standard error, "+
			"because it pipes fd 2 rather than inheriting it and the line it prints on this route "+
			"tells the operator to read that diagnostic there; stderr = %q", proofErr.String())
	}
}

func TestOpenCodeTargetDescriptor_BundleManifestOracle(t *testing.T) {
	desc, err := NewOpenCodeTargetDescriptor()
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	// Integration layer folds in an emitted skill file and an agent file.
	extra := []OpenCodeComponentFile{
		{Path: "skills/worker/SKILL.md", Content: []byte("worker skill\n")},
		{Path: "agent/reviewer.md", Content: []byte("reviewer\n")},
	}
	bundle, err := desc.Bundle(extra)
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	// The shared artifact.Bundle is content-addressed: its ID is a canonical
	// bundle content address, not the RuntimeContractID. Attribution to the
	// producing contract lives on the descriptor (RuntimeContract), not on the
	// neutral bundle value.
	if _, err := artifact.ParseBundleID(bundle.ID().String()); err != nil {
		t.Errorf("bundle id %q is not a canonical artifact bundle id: %v", bundle.ID(), err)
	}

	entries := bundle.Manifest().Entries()
	// Lexicographic order over every entry (files and declared directories).
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Path().String() >= entries[i].Path().String() {
			t.Errorf("bundle manifest not lexicographically sorted at %d: %q >= %q",
				i, entries[i-1].Path(), entries[i].Path())
		}
	}

	regularPaths := make(map[string]struct{})
	for _, e := range entries {
		p := e.Path().String()
		if e.IsDirectory() {
			// Declared parent directories carry mode 0755 and no content digest.
			if e.Mode().Bits() != 0o755 {
				t.Errorf("directory entry %q mode %o, want 0755", p, e.Mode().Bits())
			}
			continue
		}
		regularPaths[p] = struct{}{}
		if !digestRE.MatchString(e.Digest().String()) {
			t.Errorf("entry %q digest %q not sha256:<64 hex>", p, e.Digest())
		}
		if e.Mode().Bits() != 0o644 {
			t.Errorf("entry %q mode %o, want 0644", p, e.Mode().Bits())
		}
		// Isolated retrieval: each component's bytes are independently available,
		// so a materializer can write each file with siblings absent.
		file, openErr := bundle.Open(p)
		if openErr != nil {
			t.Errorf("entry %q has no retrievable content: %v", p, openErr)
			continue
		}
		_ = file.Close()
	}

	// Exactly four regular files: the two target-owned components plus the two
	// folded-in emitted files.
	wantRegular := []string{
		OpenCodeHooksModulePath,
		OpenCodeTargetManifestPath,
		"skills/worker/SKILL.md",
		"agent/reviewer.md",
	}
	if len(regularPaths) != len(wantRegular) {
		t.Fatalf("expected %d regular-file entries, got %d (%v)", len(wantRegular), len(regularPaths), regularPaths)
	}
	for _, want := range wantRegular {
		if _, ok := regularPaths[want]; !ok {
			t.Errorf("bundle missing regular-file component %q", want)
		}
	}
}

func TestOpenCodeTargetDescriptor_Deterministic(t *testing.T) {
	d1, err := NewOpenCodeTargetDescriptor()
	if err != nil {
		t.Fatalf("descriptor 1: %v", err)
	}
	d2, err := NewOpenCodeTargetDescriptor()
	if err != nil {
		t.Fatalf("descriptor 2: %v", err)
	}
	b1, err := d1.Bundle(nil)
	if err != nil {
		t.Fatalf("bundle 1: %v", err)
	}
	b2, err := d2.Bundle(nil)
	if err != nil {
		t.Fatalf("bundle 2: %v", err)
	}
	// The shared bundle is content-addressed, so identical inputs yield an
	// identical bundle id and an equal manifest.
	if b1.ID() != b2.ID() {
		t.Fatalf("target bundle is not deterministic across descriptor builds: %q != %q", b1.ID(), b2.ID())
	}
	if !b1.Equal(b2) {
		t.Fatal("target bundles with identical inputs are not Equal")
	}
}

func TestOpenCodeTargetDescriptor_RuntimeContractIdentity(t *testing.T) {
	desc, err := NewOpenCodeTargetDescriptor()
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	want := runtime.OpenCode2_0_20().ID()
	if desc.RuntimeContract() != want {
		t.Errorf("descriptor RuntimeContract = %v, want %v", desc.RuntimeContract(), want)
	}
	if desc.RuntimeContract().Harness() != ir.HarnessOpenCode {
		t.Errorf("descriptor harness = %v, want opencode", desc.RuntimeContract().Harness())
	}
}
