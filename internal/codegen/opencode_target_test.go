package codegen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// expectedOpenCodeNativeTools is the exact native tool allow-list OpenCode
// the recorded version declares: invoke-skill -> skill, delegate-assignment -> task,
// request-user-decision -> question. Every other core operation is
// semantic-instruction or unsupported and contributes no native tool.
var expectedOpenCodeNativeTools = []string{"question", "skill", "task"}

// foreignNativeCallNames are distinctive native call names declared by OTHER
// harness contracts (Claude Code, Codex). None may appear in any OpenCode
// generated artifact: their presence would mean an invented/borrowed tool name
// survived the projection.
var foreignNativeCallNames = []string{"Agent", "SendMessage", "TaskStop", "AskUserQuestion", "request-input"}

func TestOpenCodeNativeToolNames_MatchPinnedContract(t *testing.T) {
	got, err := deriveOpenCodeNativeToolNames()
	if err != nil {
		t.Fatalf("deriveOpenCodeNativeToolNames: %v", err)
	}
	if !reflect.DeepEqual(got, expectedOpenCodeNativeTools) {
		t.Fatalf("derived native tools = %v, want %v", got, expectedOpenCodeNativeTools)
	}

	// Cross-check every name really is native in the pinned contract, proving the
	// allow-list is the contract's own declared surface, not a hand-copied list.
	contract := runtime.OpenCode1_18_29()
	native := map[string]bool{}
	for _, kind := range ir.AllOperationKinds() {
		desc, ok := runtime.CoreOperationDescriptorFor(kind)
		if !ok {
			t.Fatalf("no descriptor for core kind %q", kind)
		}
		binding, err := runtime.LookupOperationBinding(contract, desc)
		if err != nil {
			continue // unsupported (stop assignment)
		}
		if call, isNative := binding.Native(); isNative {
			native[call.CallName()] = true
		}
	}
	for _, name := range got {
		if !native[name] {
			t.Errorf("derived tool %q is not native in the pinned contract", name)
		}
	}
	if len(native) != len(got) {
		t.Errorf("derived %d tools but contract declares %d native tools", len(got), len(native))
	}
}

func TestGenerateOpenCodeHooksModule_Deterministic(t *testing.T) {
	a, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("first generate: %v", err)
	}
	b, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("second generate: %v", err)
	}
	if a != b {
		t.Fatal("hooks module generation is not byte-identical across runs")
	}
}

func TestOpenCodeTargetManifestPublishesExhaustiveProofGatedActivation(t *testing.T) {
	t.Parallel()
	descriptor, err := NewOpenCodeTargetDescriptor()
	if err != nil {
		t.Fatalf("NewOpenCodeTargetDescriptor: %v", err)
	}
	raw, err := descriptor.Manifest()
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	var manifest openCodeTargetManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		t.Fatalf("decode target manifest: %v", err)
	}
	if len(manifest.Activation) != 47 {
		t.Fatalf("activation entries = %d, want exhaustive 47-event classification", len(manifest.Activation))
	}
	enabled := make([]string, 0, 2)
	for _, entry := range manifest.Activation {
		if entry.State != "enabled" {
			if entry.Reason == "" || entry.CaptureProof != "" || entry.ProductionProof != "" {
				t.Fatalf("withheld activation entry is not fail-closed: %#v", entry)
			}
			continue
		}
		if entry.CaptureProof == "" || entry.ProductionProof == "" {
			t.Fatalf("enabled activation entry lacks both proofs: %#v", entry)
		}
		enabled = append(enabled, entry.Event)
	}
	want := []string{"session.created", "tool.execute.before"}
	if !reflect.DeepEqual(enabled, want) {
		t.Fatalf("enabled events = %v, want %v", enabled, want)
	}
}

func TestOpenCodeHooksModule_ReferencesOnlyDeclaredTools(t *testing.T) {
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// The declared allow-list must appear verbatim as a frozen array.
	if !strings.Contains(module, `Object.freeze(["question", "skill", "task"])`) {
		t.Errorf("hooks module does not freeze the exact derived allow-list; got:\n%s", module)
	}
	for _, foreign := range foreignNativeCallNames {
		if strings.Contains(module, foreign) {
			t.Errorf("hooks module references foreign native call name %q — an invented/borrowed tool survived the projection", foreign)
		}
	}
}

func TestOpenCodeHooksModule_SelfContainedAndDiscoverable(t *testing.T) {
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Self-contained: no sibling/npm import, no CommonJS require. A leading
	// import would fail isolated loading with siblings absent.
	importRE := regexp.MustCompile(`(?m)^\s*import\s`)
	if importRE.MatchString(module) {
		t.Error("hooks module has an import statement; it must be self-contained for isolated loading")
	}
	if strings.Contains(module, "require(") {
		t.Error("hooks module uses require(); it must depend on no npm package")
	}
	// Discoverable: OpenCode reads the default export first and uses it when it
	// is an object with server(); a bare function default falls back to a scan
	// of every export that throws on the first non-function export.
	if !strings.Contains(module, openCodePluginDefaultExport) {
		t.Errorf("hooks module lacks the default export OpenCode's loader reads first, %q; without it the loader scans every export and throws on PASTURE_NATIVE_TOOLS", openCodePluginDefaultExport)
	}
}

// openCodePluginDefaultExport is the one line OpenCode's plugin loader reads
// first: a default-exported object whose server() is the plugin function.
const openCodePluginDefaultExport = `export default { id: "pasture-lifecycle", server: PastureLifecycle };`

// TestOpenCodeHooksModule_SatisfiesHostPluginLoaderRule loads the generated
// module the way OpenCode does and applies OpenCode's own acceptance rule to
// it: the default export is an object; it carries id, server or tui; server
// is a function; tui is absent; a path plugin carries a non-empty string id.
// When the default export passes that rule the loader reads nothing else, so
// the legacy scan of every export (which throws on a non-function export) is
// never reached. The rule is transcribed from OpenCode's loader
// (packages/opencode/src/plugin/shared.ts readV1Plugin, readPluginId,
// resolvePluginId; packages/opencode/src/plugin/index.ts applyPlugin,
// getLegacyPlugins), identical at host versions 1.18.10 and 1.18.29.
func TestOpenCodeHooksModule_SatisfiesHostPluginLoaderRule(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("bun is required to load the generated OpenCode lifecycle plugin the way the host does; enter the flake dev shell or install the flake-locked Bun package")
	}
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	dir := t.TempDir()
	modulePath := filepath.Join(dir, "pasture-hooks.ts")
	if err := os.WriteFile(modulePath, []byte(module), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	runner := filepath.Join(dir, "loader-rule.ts")
	script := fmt.Sprintf(`
const mod = await import(%q);
const value = mod.default;
const isRecord = (v) => v !== null && typeof v === "object" && !Array.isArray(v);
if (!isRecord(value) || (!("id" in value) && !("server" in value) && !("tui" in value))) {
  throw new Error("the default export is not an object with server(): OpenCode falls back to scanning every export and throws \"Plugin export is not a function\" on the first non-function export");
}
if (typeof value.server !== "function") throw new Error("the default export has no server() function; OpenCode refuses the plugin");
if (value.tui !== undefined) throw new Error("the default export also carries tui(); OpenCode refuses a plugin that exports both");
if (typeof value.id !== "string" || value.id.trim() === "") throw new Error("a path plugin must export a non-empty string id; OpenCode refuses it otherwise");
console.log(JSON.stringify({ id: value.id, server: typeof value.server }));
`, modulePath)
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatalf("write loader-rule runner: %v", err)
	}
	out, err := exec.Command(bun, runner).CombinedOutput()
	if err != nil {
		t.Fatalf("the generated module does not satisfy OpenCode's plugin loader rule: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != `{"id":"pasture-lifecycle","server":"function"}` {
		t.Fatalf("loader-rule runner reported %q", got)
	}
}

func TestOpenCodeHooksModulePreservesNamedAndObservationBoundary(t *testing.T) {
	t.Parallel()

	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, required := range []string{
		`["hook", "lifecycle", "--harness", "opencode", "--event", "session.created"]`,
		`["hook", "lifecycle", "--harness", "opencode", "--event", "tool.execute.before"]`,
		`{ input, output: { args: output.args } }`, `record.decision === "proceed"`,
	} {
		if !strings.Contains(module, required) {
			t.Errorf("generated plugin lacks %q", required)
		}
	}
	if strings.Contains(module, `"session.created": false`) || strings.Contains(module, `"tool.execute.before": false`) {
		t.Error("generated plugin retained a duplicate hand-flipped activation boolean table")
	}
	for _, forbidden := range []string{"PASTURE_ADAPTER_EVENT", "PASTURE_ADAPTER_OPERATION", "PASTURE_ADAPTER_INPUT", `"__adapter"`, "invocationIdentity", "sourceValue("} {
		if strings.Contains(module, forbidden) {
			t.Errorf("generated lifecycle plugin contains forbidden semantic transport %q", forbidden)
		}
	}
}

// Constructed rows below test generator mechanics, not authentic host payloads
// or activation evidence. The public generator still evaluates the real corpus.
func TestOpenCodeEnabledDispatchThroughDefaultFactory(t *testing.T) {
	manifest := registration.OpenCode1_18_29().Entries()
	entries, err := openCodeActivationEntries()
	if err != nil {
		t.Fatal(err)
	}
	surfaces := map[string]runtime.HookSurface{}
	contract := runtime.OpenCode1_18_29Lifecycle()
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		if err != nil {
			t.Fatal(err)
		}
		surfaces[mapping.NativeName()] = mapping.Surface()
	}
	// Additional supplied registrations must not need a callback hand-list edit.
	manifest = append(manifest, registration.Event{Kind: 250, NativeName: "mechanics.observed"}, registration.Event{Kind: 251, NativeName: "mechanics.named"})
	entries = append(entries, activation.Entry{Event: 250, State: activation.Enabled}, activation.Entry{Event: 251, State: activation.Enabled})
	surfaces["mechanics.observed"] = runtime.SurfaceOpenCodeCatchAllSSE
	surfaces["mechanics.named"] = runtime.SurfaceOpenCodeNamedOutput
	for _, reduced := range []bool{false, true} {
		t.Run(fmt.Sprintf("reduced=%v", reduced), func(t *testing.T) {
			selected := append([]activation.Entry(nil), entries...)
			if reduced {
				for i := range selected {
					if selected[i].Event != 251 {
						selected[i].State = activation.Withheld
					}
				}
			}
			module, err := generateOpenCodeHooksModule(manifest, selected, surfaces)
			if err != nil {
				t.Fatal(err)
			}
			runOpenCodeDispatchModule(t, module, fmt.Sprintf(`
const reduced = %v;
assert.deepEqual(Object.keys(hooks).sort(), reduced ? ["mechanics.named"] : ["event", "mechanics.named", "tool.execute.before"]);
assert.equal(hooks["chat.message"], undefined, "withheld named key");
const input = Object.freeze({ sessionID: "constructed", tool: "task" });
const args = Object.freeze({ nested: Object.freeze([1, null, true]) });
const output = Object.freeze({ args, extra: "host-owned" });
await hooks["mechanics.named"](input, output);
assert.strictEqual(output.args, args);
assert.deepEqual(calls[0].payload, { input, output });
assert.equal(calls[0].argv[5], "mechanics.named");
if (!reduced) {
  const callback = Object.freeze({ event: Object.freeze({ type: "mechanics.observed", properties: Object.freeze({ id: "constructed" }) }) });
  await hooks.event(callback);
  assert.deepEqual(calls[1].payload, callback);
  assert.equal(calls[1].argv[5], "mechanics.observed");
  await hooks["tool.execute.before"](input, output);
  assert.deepEqual(calls[2].payload, { input, output: { args } });
  assert.strictEqual(output.args, args);
  const count = calls.length;
  for (const type of ["session.updated", "unknown.observation", "chat.message", "toString", "__proto__"]) await hooks.event({event:{type}});
  await hooks.event({});
  assert.equal(calls.length, count, "unknown and withheld observations must not spawn");
}
const count = calls.length;
await assert.rejects(hooks["mechanics.named"](null, output), /requires input and output objects/);
await assert.rejects(hooks["mechanics.named"](input, []), /requires input and output objects/);
assert.equal(calls.length, count, "unsupported callback shape must not spawn");
`, reduced))
		})
	}
}

// This tests command construction, not Go executable discovery or durable receipts.
// The version controls are constructed values, not new host captures.
func TestOpenCodeCreationVersionIsOccurrenceLocal(t *testing.T) {
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatal(err)
	}
	runOpenCodeDispatchModule(t, module, `
const base = ["hook", "lifecycle", "--harness", "opencode", "--event"];
const input = Object.freeze({sessionID:"constructed", callID:"call", tool:"task"});
const args = Object.freeze({nested:Object.freeze([1, null, true])});
const output = Object.freeze({args});
for (const version of ["1.19.0", " local ", "1.20.0+build", undefined, "", " \t\n", null, 42, false, {}, []]) {
  const info = Object.freeze(version === undefined ? {id:"constructed"} : {id:"constructed", version});
  const callback = Object.freeze({event:Object.freeze({type:"session.created", properties:Object.freeze({info})})});
  const before = JSON.stringify(callback);
  await hooks.event(callback);
  const expected = [...base, "session.created"];
  if (typeof version === "string" && version.trim() !== "") expected.push("--host-version", version);
  assert.deepEqual(calls.at(-1).argv, expected, "creation occurrence selects original usable metadata only");
  assert.deepEqual(calls.at(-1).payload, callback);
  assert.equal(JSON.stringify(callback), before);
  assert.strictEqual(callback.event.properties.info, info);
  await hooks["tool.execute.before"](input, output);
  assert.deepEqual(calls.at(-1).argv, [...base, "tool.execute.before"], "later tool must omit all version arguments, never cache creation metadata");
  assert.deepEqual(calls.at(-1).payload, {input, output:{args}});
  assert.strictEqual(output.args, args);
}
for (const event of [{type:"session.created"}, {type:"session.created", properties:{}}, {type:"session.created", properties:{info:null}}]) {
  await hooks.event({event});
  assert.deepEqual(calls.at(-1).argv, [...base, "session.created"]);
}
assert.equal(calls.length, 25, "all metadata and later-callback controls must run");
`)
}

func runOpenCodeDispatchModule(t *testing.T, module, assertions string) {
	t.Helper()
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("Bun is required for the generated default-factory dispatch proof")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.ts")
	if err := os.WriteFile(path, []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(dir, "dispatch.ts")
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
const {default: plugin} = await import(%q);
assert.equal(plugin.id, "pasture-lifecycle");
const hooks = await plugin.server({client:{}});
const calls = [];
const originalSpawn = Bun.spawn;
Bun.spawn = options => {
  const record = {argv: options.cmd.slice(1), payload: undefined};
  calls.push(record);
  const exited = options.stdin.text().then(text => {
    record.payload = JSON.parse(text);
    const expected = ["hook", "lifecycle", "--harness", "opencode", "--event", record.argv[5]];
    const version = record.payload.event?.properties?.info?.version;
    if (record.argv[5] === "session.created" && typeof version === "string" && version.trim() !== "") expected.push("--host-version", version);
    assert.deepEqual(record.argv, expected);
    return 0;
  });
  return {stdout: new Blob(['{"decision":"proceed"}']).stream(), stderr: new Blob([]).stream(), exited, exitCode: 0, kill() {throw new Error("unexpected kill");}};
};
try {
%s
  console.log("dispatch assertions passed");
} finally { Bun.spawn = originalSpawn; }
`, path, assertions)
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, runner)
	cmd.Env = append(os.Environ(), "PASTURE_DB_PATH="+filepath.Join(dir, "scratch.db"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("default factory dispatch: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "dispatch assertions passed") {
		t.Fatalf("runner did not finish: %s", out)
	}
}

func TestOpenCodeGeneratorRejectsUnsupportedSurface(t *testing.T) {
	for _, surface := range []runtime.HookSurface{0, runtime.SurfaceClaudeCommandJSON, runtime.SurfaceCodexStrictCommandJSON} {
		_, err := generateOpenCodeHooksModule([]registration.Event{{Kind: 250, NativeName: "mechanics.observed"}}, []activation.Entry{{Event: 250, State: activation.Enabled}}, map[string]runtime.HookSurface{"mechanics.observed": surface})
		if err == nil || !strings.Contains(err.Error(), "unsupported runtime surface") {
			t.Fatalf("surface %v: %v", surface, err)
		}
	}
}

func TestOpenCodeRegisteredSurfaceEmissionMechanics(t *testing.T) {
	// This is not an activation evaluator: all rows here are constructed enabled
	// inputs so emission covers native spellings that remain withheld in product.
	manifest := registration.OpenCode1_18_29().Entries()
	var entries []activation.Entry
	surfaces := map[string]runtime.HookSurface{}
	contract := runtime.OpenCode1_18_29Lifecycle()
	var observations, named []string
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		if err != nil {
			t.Fatal(err)
		}
		surfaces[mapping.NativeName()] = mapping.Surface()
	}
	for _, event := range manifest {
		entries = append(entries, activation.Entry{Event: event.Kind, State: activation.Enabled})
		switch surfaces[event.NativeName] {
		case runtime.SurfaceOpenCodeCatchAllSSE:
			observations = append(observations, event.NativeName)
		case runtime.SurfaceOpenCodeNamedOutput:
			named = append(named, event.NativeName)
		default:
			t.Fatalf("unsupported registered surface: %s", event.NativeName)
		}
	}
	module, err := generateOpenCodeHooksModule(manifest, entries, surfaces)
	if err != nil {
		t.Fatal(err)
	}
	observationJSON, err := json.Marshal(observations)
	if err != nil {
		t.Fatal(err)
	}
	namedJSON, err := json.Marshal(named)
	if err != nil {
		t.Fatal(err)
	}
	runOpenCodeDispatchModule(t, module, fmt.Sprintf(`
const observations = %s, named = %s;
assert.deepEqual(Object.keys(hooks).sort(), ["event", ...named].sort());
for (const type of observations) {
  const payload = {event:{type, properties:{id:"constructed"}}};
  await hooks.event(payload);
  assert.equal(calls.at(-1).argv[5], type);
  assert.deepEqual(calls.at(-1).payload, payload);
}
for (const name of named) {
  const input = Object.freeze({id:"constructed"});
  const output = Object.freeze({args:Object.freeze({id:"constructed"})});
  await hooks[name](input, output);
  assert.equal(calls.at(-1).argv[5], name);
  assert.deepEqual(calls.at(-1).payload, {input, output});
}
assert.equal(calls.length, observations.length + named.length);
`, observationJSON, namedJSON))
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

func TestOpenCodeGeneratedLifecycleCallbacks_RunBuiltCLIWithAuthenticFixtures(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("bun is required for the generated OpenCode production proof; enter the flake dev shell")
	}
	root := testModuleRoot(t)
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	build := exec.Command("go", "build", "-race", "-o", binary, "./cmd/pasture")
	build.Dir = root
	// The repository's standard test target keeps the outer suite CGO-free.
	// This child build intentionally uses the race detector, which requires CGO.
	build.Env = append(build.Environ(), "CGO_ENABLED=1")
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build production pasture CLI with race instrumentation: %v\n%s", buildErr, output)
	}

	moduleURL := (&url.URL{Scheme: "file", Path: filepath.Join(root, filepath.FromSlash(OpenCodeHooksModulePath))}).String()
	fixtureDir := filepath.Join(root, "internal", "lifecycle", "ingress", "opencode", "testdata", "fixtures")
	runner := filepath.Join(dir, "production-proof.ts")
	script := fmt.Sprintf(`
import { sessionCreated, toolExecuteBefore } from %q;
const sessionCapture = await Bun.file(%q).json();
const toolCapture = await Bun.file(%q).json();
await sessionCreated(sessionCapture);
const output = toolCapture.output;
const before = JSON.stringify(output.args);
await toolExecuteBefore(toolCapture.input, output);
if (JSON.stringify(output.args) !== before) throw new Error("generated tool.execute.before callback changed output.args");
console.log(JSON.stringify({ argsUnchanged: true }));
`, moduleURL,
		filepath.Join(fixtureDir, "session_created_1_18_29.json"),
		filepath.Join(fixtureDir, "tool_execute_before_1_18_29.json"))
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatalf("write Bun production-proof runner: %v", err)
	}
	dbPath := filepath.Join(dir, "pasture.db")
	bootstrap := exec.Command(binary, "--db", dbPath, "--namespace", "file://opencode-production-proof", "task", "create", "initialize lifecycle identity")
	if bootstrapOutput, bootstrapErr := bootstrap.CombinedOutput(); bootstrapErr != nil {
		t.Fatalf("initialize real temporary Pasture store through production CLI: %v\n%s", bootstrapErr, bootstrapOutput)
	}
	proof := exec.Command(bun, runner)
	versionPath := filepath.Join(dir, "bin")
	if err := os.Mkdir(versionPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionPath, "opencode"), []byte("#!/bin/sh\nprintf '1.19.0\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	proof.Env = append(os.Environ(), "PASTURE_BIN="+binary, "PASTURE_DB_PATH="+dbPath,
		"PATH="+versionPath, "PASTURE_CAPTURE_DIR=", "PASTURE_ACTOR_ID=", "PASTURE_HOOK_FAIL_CLOSED=")
	output, err := proof.CombinedOutput()
	if err != nil {
		t.Fatalf("execute generated OpenCode callbacks through built CLI: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != `{"argsUnchanged":true}` {
		t.Fatalf("Bun proof output = %q, want unchanged-args confirmation", output)
	}

	readback := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "list", "--format", "json")
	readbackOutput, err := readback.CombinedOutput()
	if err != nil {
		t.Fatalf("read back generated callback receipts through production CLI: %v\n%s", err, readbackOutput)
	}
	for _, required := range []string{
		fmt.Sprintf(`"registrationContract":"opencode/%s"`, openCodeHostVersion()),
		fmt.Sprintf(`"contract":%q`, runtime.OpenCode1_18_29().ID().String()),
		`"semantic":1`,
		`"semantic":2`,
	} {
		if !strings.Contains(string(readbackOutput), required) {
			t.Errorf("production lifecycle read-back lacks %s: %s", required, readbackOutput)
		}
	}
}

func TestOpenCodeGeneratedLifecycleCallbacks_RejectInvalidGateResponsesAndSwallowObservationFailure(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("bun is required for the generated OpenCode failure-path proof; enter the flake dev shell")
	}
	root := testModuleRoot(t)
	dir := t.TempDir()
	fakeBinary := filepath.Join(dir, "fake-pasture")
	fake := `#!/bin/sh
payload=$(cat)
case "$payload" in
  *'"tool":"malformed"'*) printf '%s' 'not-json' ;;
  *'"tool":"extra"'*) printf '%s' '{"decision":"proceed","extra":true}' ;;
  *'"tool":"wrong-decision"'*) printf '%s' '{"decision":"block"}' ;;
  *'"tool":"nonzero"'*|*'"type":"session.created"'*) printf '%s' 'synthetic lifecycle diagnostic' >&2; exit 7 ;;
  *) printf '%s' '{"decision":"proceed"}' ;;
esac
`
	if err := os.WriteFile(fakeBinary, []byte(fake), 0o700); err != nil {
		t.Fatalf("write bounded fake PASTURE_BIN: %v", err)
	}

	moduleURL := (&url.URL{Scheme: "file", Path: filepath.Join(root, filepath.FromSlash(OpenCodeHooksModulePath))}).String()
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
  const output = { args: { path: "unchanged", nested: [1, true, null] } };
  const before = JSON.stringify(output.args);
  let diagnostic = "";
  try {
    await toolExecuteBefore({ tool: testCase.mode }, output);
  } catch (error) {
    diagnostic = String(error);
  }
  if (!diagnostic.includes(testCase.diagnostic)) {
    throw new Error(testCase.mode + " did not reject actionably; got: " + diagnostic);
  }
  if (JSON.stringify(output.args) !== before) {
    throw new Error(testCase.mode + " changed output.args bytes on rejection");
  }
}

const logged = [];
const originalError = console.error;
console.error = (...values) => logged.push(values.join(" "));
try {
  await sessionCreated({ event: { type: "session.created" } });
} finally {
  console.error = originalError;
}
if (logged.length !== 1 || !logged[0].includes("observation failed for session.created") ||
    !logged[0].includes("exited 7: synthetic lifecycle diagnostic")) {
  throw new Error("session.created did not swallow and log its observation failure: " + JSON.stringify(logged));
}
console.log(JSON.stringify({ rejected: cases.length, observationLogged: true }));
`, moduleURL)
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

// runOpenCodeMechanics executes freshly generated production code. The fake
// binary is a transport peer, not an encoder or a claim about host acceptance.
func runOpenCodeMechanics(t *testing.T, script string) string {
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
const mode = value.input?.tool ?? "observation-stall";
if (mode === "reply") {
  process.stdout.write(value.input.body);
  process.stderr.write(value.input.diagnostic ?? "");
  process.exitCode = value.input.exitCode ?? 0;
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
const { default: plugin } = await import(%q);
const hooks = await plugin.server({ client: {} });
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
async function gate(input) {
  const args = { path: "unchanged", nested: [1, true, null] };
  const output = { args };
  let failure;
  try {
    await bounded(hooks["tool.execute.before"](input, output), "whole-child completion");
  } catch (error) {
    failure = error;
  }
  assert.strictEqual(output.args, args, "native args identity on every outcome");
  assert.deepEqual(args, { path: "unchanged", nested: [1, true, null] }, "native args content");
  return failure;
}
try {
`, modulePath)
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
	runOpenCodeMechanics(t, `
  const reason = "  role cannot write\nretain this exact reason  ";
  const denied = await gate({ tool: "reply", body: JSON.stringify({ decision: "deny", reason }) });
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
    const failure = await gate({ tool: "reply", body });
    assert(failure instanceof Error, "invalid response accepted: " + body);
    assert.match(failure.message, /pasture hook lifecycle response/, "response fault, not a policy Deny: " + body);
    assert.match(failure.message, /tool.execute.before/, "fault names event: " + body);
    assert.match(failure.message, /verify PASTURE_BIN and the generated OpenCode/, "response fault configuration advice: " + body);
  }
  assert.equal(await gate({ tool: "reply", body: '{"decision":"proceed"}' }), undefined, "exact Proceed accepted");
  const nonzero = await gate({ tool: "reply", body: '{"decision":"deny","reason":"not a decision at nonzero"}', diagnostic: "synthetic lifecycle diagnostic", exitCode: 7 });
  assert.match(nonzero.message, /exited 7: synthetic lifecycle diagnostic; verify PASTURE_BIN and the generated OpenCode/, "nonzero remains invocation fault");
`)
}

func TestOpenCodeGeneratedPluginBoundsWholeChildAndReaps(t *testing.T) {
	runOpenCodeMechanics(t, `
  const logged = [];
  const originalError = console.error;
  console.error = (...args) => logged.push(args.join(" "));
  try {
    await Promise.all([
      ...["stdout-stall", "stderr-stall", "exit-stall"].map(async (tool) => {
        const start = performance.now();
        const failure = await gate({ tool });
        assert(failure instanceof Error, tool + " must fault");
        assert.match(failure.message, /timed out after 8000 ms/, tool + " must hit the plugin timer, not the test bound");
        assert.match(failure.message, /verify PASTURE_BIN and the generated OpenCode/, tool + " retains configuration advice");
        assert(performance.now() - start >= 7900, "actual 8s timer was not shortened");
      }),
      bounded(hooks.event({ event: { type: "session.created" } }), "observation completion"),
    ]);
  } finally {
    console.error = originalError;
  }
  assert.equal(logged.length, 1, "observation logs once and continues");
  assert.match(logged[0], /observation failed for session.created:.*timed out after 8000 ms/);
  assert.equal(children.length, 4, "all stalled real children were invoked");
  for (const record of children) {
    assert(record.kills > 0, "stalled actual child was killed");
    assert(record.reaped, "callback waits for child reaping");
    assert.equal(record.child.signalCode, "SIGKILL", "force kill, not a cooperative exit");
    assert.throws(() => process.kill(record.child.pid, 0), { code: "ESRCH" }, "no surviving process or zombie");
  }
`)
}

func TestOpenCodeGeneratedPluginClearsTimerAfterCompletion(t *testing.T) {
	runOpenCodeMechanics(t, `
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
      const failure = await gate({ tool: "reply", body: '{"decision":"proceed"}', exitCode });
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
	stderr := runOpenCodeMechanics(t, `
  // Both streams exceed ordinary pipe capacity. Sequential draining would
  // deadlock a writer that fills stderr before closing stdout.
  const diagnostic = "  diagnostic α\n".repeat(32768);
  const failure = await gate({
    tool: "reply",
    body: " ".repeat(262144) + '{"decision":"proceed"}',
    diagnostic,
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
	runOpenCodeMechanics(t, `
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
      failure = await gate({ tool: "reply", body: '{"decision":"proceed"}', diagnostic: "must not disappear" });
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
	stderr := runOpenCodeMechanics(t, `
  const logged = [];
  const originalError = console.error;
  console.error = (...values) => logged.push(values.join(" "));
  let failure;
  try {
    failure = await gate({ tool: "reply", body: " \n\t", diagnostic: "  old binary diagnostic α" });
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
	runOpenCodeMechanics(t, `
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
    const failure = await gate({ tool: "reply", body: '{"decision":"proceed"}' });
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
	runOpenCodeMechanics(t, `
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
    const failure = await gate({ tool: "exit-stall" });
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
	want := runtime.OpenCode1_18_29().ID()
	if desc.RuntimeContract() != want {
		t.Errorf("descriptor RuntimeContract = %v, want %v", desc.RuntimeContract(), want)
	}
	if desc.RuntimeContract().Harness() != ir.HarnessOpenCode {
		t.Errorf("descriptor harness = %v, want opencode", desc.RuntimeContract().Harness())
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
	root := testModuleRoot(t)
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

	moduleURL := (&url.URL{Scheme: "file", Path: filepath.Join(root, filepath.FromSlash(OpenCodeHooksModulePath))}).String()
	runner := filepath.Join(dir, "fail-open-belt.ts")
	script := fmt.Sprintf(`
import { toolExecuteBefore } from %q;

const output = { args: { path: "unchanged", nested: [1, true, null] } };
const before = JSON.stringify(output.args);
const logged = [];
const originalError = console.error;
console.error = (...values) => logged.push(values.join(" "));
try {
  await toolExecuteBefore({ tool: "empty-body" }, output);
} finally {
  console.error = originalError;
}
if (JSON.stringify(output.args) !== before) {
  throw new Error("an unevaluated event changed output.args");
}
if (logged.length !== 1 || !logged[0].includes("did not evaluate tool.execute.before")) {
  throw new Error("the callback did not report the unevaluated event: " + JSON.stringify(logged));
}
console.log(JSON.stringify({ continued: true, argsUnchanged: true }));
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
	if strings.TrimSpace(proofOut.String()) != `{"continued":true,"argsUnchanged":true}` {
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

// TestOpenCodeGeneratedGateSurvivesARealPastureFault is the BLOCKER proof, end
// to end: the REAL built binary, a REAL fault, and the REAL generated plugin
// under Bun.
//
// The fault is the commonest one a user meets: the pasture store cannot be
// opened. Under the fail-open default the user's tool call must proceed with
// its arguments untouched, and under the fail-closed opt-in it must ALSO
// proceed on this harness, because that opt-in refuses through the process exit
// code and OpenCode's named callbacks do not refuse that way. Both are asserted
// here so neither can regress silently.
func TestOpenCodeGeneratedGateSurvivesARealPastureFault(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("bun is required for the generated OpenCode fail-open proof; enter the flake dev shell")
	}
	root := testModuleRoot(t)
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	build := exec.Command("go", "build", "-race", "-o", binary, "./cmd/pasture")
	build.Dir = root
	build.Env = append(build.Environ(), "CGO_ENABLED=1")
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build production pasture CLI with race instrumentation: %v\n%s", buildErr, output)
	}

	// A DIRECTORY where the database file belongs. Every attempt to open the
	// pasture store fails with a real storage error; nothing is simulated.
	unopenable := filepath.Join(dir, "not-a-database")
	if err := os.Mkdir(unopenable, 0o755); err != nil {
		t.Fatalf("create the unopenable store path: %v", err)
	}

	moduleURL := (&url.URL{Scheme: "file", Path: filepath.Join(root, filepath.FromSlash(OpenCodeHooksModulePath))}).String()
	fixtureDir := filepath.Join(root, "internal", "lifecycle", "ingress", "opencode", "testdata", "fixtures")
	runner := filepath.Join(dir, "fail-open-proof.ts")
	script := fmt.Sprintf(`
import { toolExecuteBefore } from %q;
const toolCapture = await Bun.file(%q).json();

const output = toolCapture.output;
const before = JSON.stringify(output.args);
await toolExecuteBefore(toolCapture.input, output);
if (JSON.stringify(output.args) !== before) {
  throw new Error("a pasture fault changed output.args");
}
console.log(JSON.stringify({ toolCallProceeded: true }));
`, moduleURL, filepath.Join(fixtureDir, "tool_execute_before_1_18_29.json"))
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatalf("write Bun fail-open proof runner: %v", err)
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
			proof.Env = append(os.Environ(), "PASTURE_BIN="+binary, "PASTURE_DB_PATH="+unopenable)
			proof.Env = append(proof.Env, policy.env...)
			output, err := proof.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("the Bun fail-open proof exceeded its bound: %v\n%s", ctx.Err(), output)
			}
			if err != nil {
				t.Fatalf("a pasture fault STOPPED the user's tool call under %s: %v\n%s", policy.name, err, output)
			}
			if !strings.Contains(string(output), `{"toolCallProceeded":true}`) {
				t.Fatalf("Bun fail-open proof output = %q, want the proceed confirmation", output)
			}
		})
	}

	// The fault is reported and recorded as a FAULT, not as a decision. The
	// record sits beside the database path the invocation used.
	records, err := os.ReadFile(filepath.Join(dir, "lifecycle-faults.jsonl"))
	if err != nil {
		t.Fatalf("read the durable lifecycle fault record: %v", err)
	}
	for _, required := range []string{
		`"outcomeClass":"fault"`,
		`"harness":"opencode"`,
		`"event":"tool.execute.before"`,
		`"hostExit":"continue"`,
		`"hostContinuation":"{\"decision\":\"proceed\"}"`,
	} {
		if !strings.Contains(string(records), required) {
			t.Errorf("the fault record lacks %s, so a reader cannot tell an unevaluated proceed from a decision: %s", required, records)
		}
	}
	if strings.Contains(string(records), `"outcomeClass":"decision"`) {
		t.Error("a fault must never be recorded as a decision")
	}
}
