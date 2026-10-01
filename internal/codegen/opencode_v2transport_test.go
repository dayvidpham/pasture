package codegen

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

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
	contract := runtime.OpenCode2_0_20()
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
	if len(manifest.Activation) != 17 {
		t.Fatalf("activation entries = %d, want the exhaustive 17-event 2.0.20 classification", len(manifest.Activation))
	}
	// No 2.0.20 row carries proofs yet: the capture sitting and the
	// production-path proofs have not landed, so every row is withheld for
	// missing-fixture. An enabled row here would claim evidence that does not
	// exist.
	for _, entry := range manifest.Activation {
		if entry.State != "withheld" {
			t.Fatalf("activation entry %q is %q, want withheld until its capture and production proofs land", entry.Event, entry.State)
		}
		if entry.Reason != "missing-fixture" {
			t.Fatalf("activation entry %q reason = %q, want missing-fixture", entry.Event, entry.Reason)
		}
		if entry.CaptureProof != "" || entry.ProductionProof != "" {
			t.Fatalf("withheld activation entry carries proofs: %#v", entry)
		}
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

func TestOpenCodeHooksModule_UsesOnlyTheHostPluginSpecifier(t *testing.T) {
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// The one import is the host-provided specifier the OpenCode CLI maps to
	// its bundled plugin SDK at load time; a second import would fail
	// isolated loading with siblings absent, and a require() would fail
	// outside CommonJS.
	if got := strings.Count(module, `import { Plugin } from "@opencode/plugin";`); got != 1 {
		t.Errorf("hooks module imports the host specifier %d times, want exactly once", got)
	}
	importRE := regexp.MustCompile(`(?m)^\s*import\s`)
	if found := importRE.FindAllString(module, -1); len(found) != 1 {
		t.Errorf("hooks module has %d import statements, want exactly the host specifier; got:\n%s", len(found), module)
	}
	if strings.Contains(module, "require(") {
		t.Error("hooks module uses require(); it must depend on no npm package beyond the host specifier")
	}
}

// openCodePluginDefaultExport is the one line OpenCode's v2 plugin loader
// reads: a default-exported Plugin.define object carrying the plugin id and
// its setup function, which registers location-scoped hooks on the context.
const openCodePluginDefaultExport = `export default Plugin.define({
  id: "pasture-lifecycle",
  setup: async (ctx) => {`

// TestOpenCodeHooksModule_SatisfiesHostPluginLoaderRule loads the generated
// module the way OpenCode v2 does and applies the host's own acceptance rule
// to it: the default export is the Plugin.define object; it carries a
// non-empty string id and a setup function; setup registers the v2 hook
// surface on the context and returns an async cleanup that disposes every
// registration. The rule is transcribed from the host loader
// (packages/core/src/plugin/module.ts: the default is {id, effect} or
// {id, setup} with setup a function) at the 2.0.20 tag.
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
	writeOpenCodePluginStub(t, dir)
	modulePath := filepath.Join(dir, "pasture-hooks.ts")
	if err := os.WriteFile(modulePath, []byte(module), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	runner := filepath.Join(dir, "loader-rule.ts")
	script := fmt.Sprintf(`
const mod = await import(%q);
const value = mod.default;
if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("the default export is not the Plugin.define object");
if (typeof value.id !== "string" || value.id.trim() === "") throw new Error("a path plugin must export a non-empty string id; OpenCode refuses it otherwise");
if (typeof value.setup !== "function") throw new Error("the default export has no setup() function; OpenCode refuses the plugin");
const disposed = [];
const ctx = {
  session: { hook: async (name, cb) => { if (typeof cb !== "function") throw new Error("session hook callback is not a function"); const r = { dispose: async () => { disposed.push("session." + name); } }; return r; } },
  tool: { hook: async (name, cb) => { if (typeof cb !== "function") throw new Error("tool hook callback is not a function"); const r = { dispose: async () => { disposed.push("tool." + name); } }; return r; } },
  permission: { hook: async (name, cb) => { if (typeof cb !== "function") throw new Error("permission hook callback is not a function"); const r = { dispose: async () => { disposed.push("permission." + name); } }; return r; } },
  shell: { hook: async (name, cb) => { if (typeof cb !== "function") throw new Error("shell hook callback is not a function"); const r = { dispose: async () => { disposed.push("shell." + name); } }; return r; } },
  event: { subscribe: (options) => (async function* () {})() },
};
const cleanup = await value.setup(ctx);
if (typeof cleanup !== "function") throw new Error("setup must return its cleanup function");
await cleanup();
if (disposed.length !== 16) throw new Error("cleanup disposed " + disposed.length + " registrations, want all 16 hooks");
console.log(JSON.stringify({ id: value.id, setup: typeof value.setup }));
`, modulePath)
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatalf("write loader-rule runner: %v", err)
	}
	out, err := exec.Command(bun, runner).CombinedOutput()
	if err != nil {
		t.Fatalf("the generated module does not satisfy OpenCode v2's plugin loader rule: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != `{"id":"pasture-lifecycle","setup":"function"}` {
		t.Fatalf("loader-rule runner reported %q", got)
	}
}

// openCodePluginStubSource is the exact stub of the host-provided
// "@opencode/plugin" specifier every Bun proof writes beside the module under
// test. It mirrors the host's own define, which is the identity function
// (packages/plugin/src/promise/plugin.ts at the 2.0.20 tag:
// `export function define(plugin: Plugin) { return plugin }`), so the shape
// under test is the module's, not the SDK's. The stub keeps proofs hermetic:
// without it Bun resolves the ambient install cache, and a proof would pass
// or fail on machine state instead of the generated bytes.
const openCodePluginStubSource = "export const Plugin = { define: (plugin) => plugin };\n"

// writeOpenCodePluginStub stubs the host-provided "@opencode/plugin"
// specifier beside the module under test. The stub mirrors the host's own
// define, which is the identity function (packages/plugin/src/promise/
// plugin.ts at the 2.0.20 tag: `export function define(plugin: Plugin) {
// return plugin }`), so the shape under test is the module's, not the SDK's.
func writeOpenCodePluginStub(t *testing.T, dir string) {
	t.Helper()
	stub := filepath.Join(dir, "node_modules", "@opencode", "plugin")
	if err := os.MkdirAll(stub, 0o755); err != nil {
		t.Fatalf("create plugin stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stub, "package.json"), []byte(`{"name":"@opencode/plugin","type":"module","main":"index.js"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write plugin stub manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stub, "index.js"), []byte(openCodePluginStubSource), 0o644); err != nil {
		t.Fatalf("write plugin stub: %v", err)
	}
}

// TestOpenCodePluginStubMirrorsHostDefine pins the stub every Bun proof
// depends on and proves the pin load-bearing by mutation: the stub's
// load-bearing property is that define returns its argument, so a mutant
// define that drops the plugin must fail the host loader rule the proofs
// assume. A future edit that changes the stub's semantics turns this red
// instead of silently redefining what every proof loads against.
func TestOpenCodePluginStubMirrorsHostDefine(t *testing.T) {
	if !strings.Contains(openCodePluginStubSource, "define: (plugin) => plugin") {
		t.Fatal("the plugin stub must mirror the host identity define: it returns its argument")
	}
	for _, tc := range []struct {
		name    string
		stub    string
		wantErr string
	}{
		{name: "identity define loads the plugin shape", stub: openCodePluginStubSource},
		{name: "dropping define breaks loading", stub: "export const Plugin = {};\n", wantErr: "Plugin.define is not a function"},
		{name: "non-identity define breaks the shape", stub: "export const Plugin = { define: () => ({}) };\n", wantErr: "non-empty string id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bun, err := exec.LookPath("bun")
			if err != nil {
				t.Fatal("bun is required to pin the plugin stub")
			}
			dir := t.TempDir()
			stubDir := filepath.Join(dir, "node_modules", "@opencode", "plugin")
			if err := os.MkdirAll(stubDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stubDir, "package.json"), []byte(`{"name":"@opencode/plugin","type":"module","main":"index.js"}`+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stubDir, "index.js"), []byte(tc.stub), 0o644); err != nil {
				t.Fatal(err)
			}
			module, err := GenerateOpenCodeHooksModule()
			if err != nil {
				t.Fatal(err)
			}
			modulePath := filepath.Join(dir, "pasture-hooks.ts")
			if err := os.WriteFile(modulePath, []byte(module), 0o644); err != nil {
				t.Fatal(err)
			}
			runner := filepath.Join(dir, "stub-pin.ts")
			script := fmt.Sprintf(`
const mod = await import(%q);
const value = mod.default;
if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("the default export is not the Plugin.define object");
if (typeof value.id !== "string" || value.id.trim() === "") throw new Error("a path plugin must export a non-empty string id; OpenCode refuses it otherwise");
if (typeof value.setup !== "function") throw new Error("the default export has no setup() function; OpenCode refuses the plugin");
console.log("stub pin holds");
`, modulePath)
			if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(bun, runner).CombinedOutput()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("identity stub must load the plugin shape: %v\n%s", err, out)
				}
				return
			}
			if err == nil {
				t.Fatalf("mutant stub loaded the plugin shape; the pin is not load-bearing")
			}
			if !strings.Contains(string(out), tc.wantErr) {
				t.Fatalf("mutant stub failed without the expected diagnostic %q:\n%s", tc.wantErr, out)
			}
		})
	}
}

func TestOpenCodeHooksModulePreservesV2HookBoundary(t *testing.T) {
	t.Parallel()

	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, required := range []string{
		`["hook", "lifecycle", "--harness", "opencode", "--event", "session.created"]`,
		`["hook", "lifecycle", "--harness", "opencode", "--event", "session.prompt"]`,
		`["hook", "lifecycle", "--harness", "opencode", "--event", "tool.execute.before"]`,
		`["hook", "lifecycle", "--harness", "opencode", "--event", "permission.evaluate"]`,
		`["hook", "lifecycle", "--harness", "opencode", "--event", "shell.create.before"]`,
		`import { Plugin } from "@opencode/plugin";`,
		openCodePluginDefaultExport,
		`ctx.session.hook("prompt"`,
		`ctx.tool.hook("execute.before"`,
		`ctx.permission.hook("evaluate"`,
		`ctx.shell.hook("create.before"`,
		`ctx.event.subscribe({ signal: controller.signal })`,
		`record.decision === "proceed"`,
	} {
		if !strings.Contains(module, required) {
			t.Errorf("generated plugin lacks %q", required)
		}
	}
	for _, forbidden := range []string{
		"PASTURE_ADAPTER_EVENT", "PASTURE_ADAPTER_OPERATION", "PASTURE_ADAPTER_INPUT",
		`"__adapter"`, "invocationIdentity", "sourceValue(",
		"export const PastureLifecycle", "server: PastureLifecycle", "PastureLifecycle =",
		`"tool.execute.before": false`, "PASTURE_NATIVE_TOOLS above",
		// The v1 returned-hooks-object shape must not survive beside the v2
		// Plugin.define shape: a loader that reads the default first ignores
		// anything else, so a second shape would be dead weight at best.
		`async ({ client })`,
	} {
		if strings.Contains(module, forbidden) {
			t.Errorf("generated lifecycle plugin contains forbidden v1 transport %q", forbidden)
		}
	}
	// The permission hook consults the gate but never mutates the host
	// evaluation: wiring the enforceable effect="deny" mutation is a later
	// transport change, and a helper that assigned it here would claim a
	// channel this slice does not prove.
	for _, forbidden := range []string{"event.effect =", "event.effect=", "effect: \"deny\"", "effect: 'deny'"} {
		if strings.Contains(module, forbidden) {
			t.Errorf("generated permission hook performs the unenforced effect mutation %q", forbidden)
		}
	}
}

// TestOpenCodeHooksModule_FaultRecordPromiseIsTrue pins the load-bearing
// phrases of the generated plugin's fault-record console line and proves each
// pin load-bearing by mutation: the same predicate run against a mutant
// module with the phrase removed must fail, so a future edit that drops the
// phrase turns this test red instead of passing vacuously. The promise the
// line makes is that a fault record MAY exist but also MAY NOT: standard
// error is where pasture reports every fault including the unwritten-record
// case, and the durable line is only offered, never guaranteed.
func TestOpenCodeHooksModule_FaultRecordPromiseIsTrue(t *testing.T) {
	t.Parallel()
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	loadBearing := []string{
		"including the case where it could not write a durable record",
		"but a fault whose record could not be placed or written leaves none",
	}
	holds := func(candidate string) bool {
		for _, phrase := range loadBearing {
			if !strings.Contains(candidate, phrase) {
				return false
			}
		}
		return true
	}
	if !holds(module) {
		t.Fatalf("the generated plugin does not carry the true fault-record promise")
	}
	for _, phrase := range loadBearing {
		mutant := strings.Replace(module, phrase, "A LINE WAS APPENDED TO THE RECORD", 1)
		if holds(mutant) {
			t.Fatalf("the fault-record pin for %q is not load-bearing: it passes with the phrase removed", phrase)
		}
		if strings.Contains(module, "will be appended to lifecycle-faults.jsonl") ||
			strings.Contains(module, "A line is appended to lifecycle-faults.jsonl") {
			t.Fatalf("the generated plugin promises a record unconditionally")
		}
	}
}

// Constructed rows below test generator mechanics, not authentic host payloads
// or activation evidence. The public generator still evaluates the real corpus.
func TestOpenCodeV2SetupRegistersTheWholeSurface(t *testing.T) {
	manifest := registration.OpenCode2_0_20().Entries()
	entries, err := openCodeActivationEntries()
	if err != nil {
		t.Fatal(err)
	}
	surfaces := map[string]runtime.HookSurface{}
	contract := runtime.OpenCode2_0_20Lifecycle()
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		if err != nil {
			t.Fatal(err)
		}
		surfaces[mapping.NativeName()] = mapping.Surface()
	}
	// Additional supplied registrations must not need a hook hand-list edit,
	// and the domains they use must be real plugin domains.
	manifest = append(manifest,
		registration.Event{Kind: 250, NativeName: "session.mechanics"},
		registration.Event{Kind: 251, NativeName: "tool.mechanics.gate"},
		registration.Event{Kind: 252, NativeName: "permission.mechanics"},
		registration.Event{Kind: 253, NativeName: "shell.mechanics"},
		registration.Event{Kind: 254, NativeName: "mechanics.bus"},
	)
	entries = append(entries,
		activation.Entry{Event: 250, State: activation.Enabled},
		activation.Entry{Event: 251, State: activation.Enabled},
		activation.Entry{Event: 252, State: activation.Enabled},
		activation.Entry{Event: 253, State: activation.Enabled},
		activation.Entry{Event: 254, State: activation.Enabled},
	)
	surfaces["session.mechanics"] = runtime.SurfaceOpenCodeNamedOutput
	surfaces["tool.mechanics.gate"] = runtime.SurfaceOpenCodeNamedOutput
	surfaces["permission.mechanics"] = runtime.SurfaceOpenCodeNamedOutput
	surfaces["shell.mechanics"] = runtime.SurfaceOpenCodeNamedOutput
	surfaces["mechanics.bus"] = runtime.SurfaceOpenCodeCatchAllSSE
	module, err := generateOpenCodeHooksModule(manifest, entries, surfaces)
	if err != nil {
		t.Fatal(err)
	}
	busScript := `[
  { type: "session.deleted", data: { sessionID: "constructed" } },
  { type: "session.created", data: { sessionID: "constructed", version: "2.0.20" } },
  null,
  { type: "unknown.bus.event" },
]`
	runOpenCodeV2SetupModule(t, module, busScript, `
const registered = Object.keys(hooks).sort();
assert.deepEqual(registered, [
  "permission.evaluate", "permission.mechanics",
  "session.compaction", "session.context", "session.experimental.ws.handshake",
  "session.experimental.ws.receive", "session.experimental.ws.send", "session.generate",
  "session.http.request", "session.http.response", "session.mechanics", "session.model.request",
  "session.prompt", "session.retry", "session.title",
  "shell.create.before", "shell.mechanics",
  "tool.execute.after", "tool.execute.before", "tool.mechanics.gate",
].sort());
// The transport registers the whole surface, not the enabled subset: every
// row above is registered even though the committed activation withholds all
// of them. Admission is enforced by the lifecycle handler; the fail-open
// continuation bytes make an unadmitted row safe. The two bus observations
// ride the event subscription instead of a hook.
assert.equal(subscribed.length, 1, "one bus subscription carries the observations");
const byEvent = (name) => calls.find((call) => call.argv[5] === name);
await waitForCalls(1);
assert.equal(byEvent("session.created").argv.slice(6).join(" "), "--host-version 2.0.20", "the bus version rides the occurrence-local flag");
assert.deepEqual(byEvent("session.created").payload, { type: "session.created", data: { sessionID: "constructed", version: "2.0.20" } });
assert.equal(calls.length, 1, "only the session.created bus event spawns");
await hooks["tool.execute.before"]({ tool: "task", sessionID: "constructed", agent: "agent", messageID: "message", id: "call", input: { path: "unchanged" } });
assert.deepEqual(byEvent("tool.execute.before").payload, { tool: "task", sessionID: "constructed", agent: "agent", messageID: "message", id: "call", input: { path: "unchanged" } });
// A gate payload is forwarded verbatim: the v2 transport performs no
// args-only projection and no input/output shape validation.
await hooks["session.prompt"]({ sessionID: "constructed", messageID: "m", prompt: { text: "hello" }, delivery: "steer" });
assert.deepEqual(byEvent("session.prompt").payload, { sessionID: "constructed", messageID: "m", prompt: { text: "hello" }, delivery: "steer" });
// The permission hook consults but never mutates the host evaluation.
const evaluation = Object.freeze({ sessionID: "constructed", action: "edit", resources: ["file"], effect: "allow" });
await hooks["permission.evaluate"](evaluation);
assert.deepEqual(byEvent("permission.evaluate").payload, evaluation);
assert.equal(evaluation.effect, "allow", "the gate consultation must not mutate the host evaluation");
assert.equal("message" in evaluation, false, "the gate consultation must not add a host message");
`)
}

// TestOpenCodeV2WithheldRowsStayRegistered pins the v2 divergence from the v1
// emitter: marking rows withheld must NOT remove their hook registrations.
// The committed 2.0.20 activation withholds every row (no proofs yet) and the
// generated transport still registers all of them; the lifecycle handler is
// the admission authority, and its fail-open continuation answers an
// unadmitted row with the host's continue bytes.
func TestOpenCodeV2WithheldRowsStayRegistered(t *testing.T) {
	manifest := registration.OpenCode2_0_20().Entries()
	withheld, err := openCodeActivationEntries()
	if err != nil {
		t.Fatal(err)
	}
	for i := range withheld {
		withheld[i].State = activation.Withheld
	}
	surfaces := map[string]runtime.HookSurface{}
	contract := runtime.OpenCode2_0_20Lifecycle()
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		if err != nil {
			t.Fatal(err)
		}
		surfaces[mapping.NativeName()] = mapping.Surface()
	}
	module, err := generateOpenCodeHooksModule(manifest, withheld, surfaces)
	if err != nil {
		t.Fatal(err)
	}
	runOpenCodeV2SetupModule(t, module, `[]`, `
assert.equal(Object.keys(hooks).length, 16, "all-withheld activation still registers the whole 16-hook surface");
assert.ok(hooks["session.prompt"], "withheld session hook stays registered");
assert.ok(hooks["tool.execute.before"], "withheld tool gate stays registered");
assert.ok(hooks["permission.evaluate"], "withheld permission hook stays registered");
assert.equal(subscribed.length, 1, "withheld bus observation stays subscribed");
assert.equal(calls.length, 0, "an empty bus script spawns nothing");
`)
}

// This tests command construction, not Go executable discovery or durable receipts.
// The version controls are constructed values, not new host captures.
func TestOpenCodeCreationVersionIsOccurrenceLocal(t *testing.T) {
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatal(err)
	}
	runOpenCodeV2SetupModule(t, module, `[
  { type: "session.created", data: { sessionID: "constructed", version: "2.0.20" } },
  { type: "session.created", data: { sessionID: "constructed", version: " local " } },
  { type: "session.created", data: { sessionID: "constructed", version: "2.1.0+build" } },
  { type: "session.created", data: { sessionID: "constructed" } },
  { type: "session.created", data: { sessionID: "constructed", version: "" } },
  { type: "session.created", data: { sessionID: "constructed", version: " \t\n" } },
  { type: "session.created", data: { sessionID: "constructed", version: null } },
  { type: "session.created", data: { sessionID: "constructed", version: 42 } },
  { type: "session.created", data: { sessionID: "constructed", version: false } },
  { type: "session.created", data: { sessionID: "constructed", version: {} } },
  { type: "session.created", data: { sessionID: "constructed", version: [] } },
  { type: "session.created", sessionID: "constructed", version: "2.0.20" },
]`, `
const base = ["hook", "lifecycle", "--harness", "opencode", "--event"];
await waitForCalls(12);
const usable = (version) => typeof version === "string" && version.trim() !== "";
const created = calls.filter((call) => call.argv[5] === "session.created");
assert.equal(created.length, 12, "every bus script entry spawns; only the last carries a flat decoy version the helper must ignore");
for (const call of created) {
  const version = call.payload.data?.version;
  const expected = [...base, "session.created"];
  if (usable(version)) expected.push("--host-version", version);
  assert.deepEqual(call.argv, expected, "creation occurrence selects original usable metadata only");
}
const decoy = created[created.length - 1];
assert.equal(decoy.payload.version, "2.0.20", "the flat decoy version rides top-level");
assert.deepEqual(decoy.argv, [...base, "session.created"], "a top-level version beside data must not become a flag");
const hookEvent = Object.freeze({ tool: "task", sessionID: "constructed", agent: "agent", messageID: "m", id: "call", input: Object.freeze({ path: "unchanged" }) });
const before = JSON.stringify(hookEvent);
await hooks["tool.execute.before"](hookEvent);
assert.deepEqual(calls.at(-1).argv, [...base, "tool.execute.before"], "hook callbacks never carry a version flag; the binary resolves its own host version");
assert.deepEqual(calls.at(-1).payload, hookEvent);
assert.equal(JSON.stringify(hookEvent), before, "the hook event is forwarded without mutation");
await hooks["session.context"](Object.freeze({ sessionID: "constructed" }));
assert.deepEqual(calls.at(-1).argv, [...base, "session.context"], "session hooks never carry a version flag either");
`)
}

// runOpenCodeV2SetupModule drives one generated v2 plugin through its real
// Plugin.define shape: setup(ctx) on a capturing fake context. hooks maps
// every registered native coordinate to its callback, busScript replays
// through the subscription the setup opens, and calls records every spawned
// lifecycle invocation. The fake PASTURE_BIN answers proceed with empty
// diagnostics. The subscription loop drains concurrently with setup, so the
// assertions wait for their expected calls with a bounded poll.
func runOpenCodeV2SetupModule(t *testing.T, module, busScript, assertions string) {
	t.Helper()
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("Bun is required for the generated OpenCode v2 setup proof")
	}
	dir := t.TempDir()
	writeOpenCodePluginStub(t, dir)
	path := filepath.Join(dir, "plugin.ts")
	if err := os.WriteFile(path, []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(dir, "setup.ts")
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
const {default: plugin} = await import(%q);
assert.equal(plugin.id, "pasture-lifecycle");
const hooks = {};
const registrations = [];
const subscribed = [];
const ctx = {
  session: { hook: async (name, cb) => { hooks["session." + name] = cb; const r = { dispose: async () => {} }; registrations.push(r); return r; } },
  tool: { hook: async (name, cb) => { hooks["tool." + name] = cb; const r = { dispose: async () => {} }; registrations.push(r); return r; } },
  permission: { hook: async (name, cb) => { hooks["permission." + name] = cb; const r = { dispose: async () => {} }; registrations.push(r); return r; } },
  shell: { hook: async (name, cb) => { hooks["shell." + name] = cb; const r = { dispose: async () => {} }; registrations.push(r); return r; } },
  event: { subscribe: (options) => { subscribed.push(options); return (async function* () { for (const busEvent of BUS_SCRIPT) yield busEvent; })(); } },
};
const BUS_SCRIPT = %s;
async function waitForCalls(n) {
  const start = Date.now();
  while (calls.length < n && Date.now() - start < 5000) await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(calls.length, n, "expected lifecycle invocations did not arrive");
}
const calls = [];
const originalSpawn = Bun.spawn;
Bun.spawn = options => {
  const record = {argv: options.cmd.slice(1), payload: undefined};
  calls.push(record);
  const exited = options.stdin.text().then(text => {
    record.payload = JSON.parse(text);
    const expected = ["hook", "lifecycle", "--harness", "opencode", "--event", record.argv[5]];
    assert.deepEqual(record.argv.slice(0, 6), expected);
    return 0;
  });
  return {stdout: new Blob(['{"decision":"proceed"}']).stream(), stderr: new Blob([]).stream(), exited, exitCode: 0, kill() {throw new Error("unexpected kill");}};
};
try {
  const cleanup = await plugin.setup(ctx);
  assert.equal(typeof cleanup, "function", "setup returns its cleanup function");
  %s
  await cleanup();
  assert.equal(registrations.length, Object.keys(hooks).length, "one registration per hook");
  console.log("setup assertions passed");
} finally { Bun.spawn = originalSpawn; }
`, path, busScript, assertions)
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, runner)
	cmd.Env = append(os.Environ(), "PASTURE_DB_PATH="+filepath.Join(dir, "scratch.db"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("v2 setup dispatch: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "setup assertions passed") {
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
	// A dotted coordinate outside the session/tool/permission/shell domains
	// is not a v2 hook registration.
	_, err := generateOpenCodeHooksModule(
		[]registration.Event{{Kind: 250, NativeName: "bogus.hook"}},
		[]activation.Entry{{Event: 250, State: activation.Enabled}},
		map[string]runtime.HookSurface{"bogus.hook": runtime.SurfaceOpenCodeNamedOutput},
	)
	if err == nil || !strings.Contains(err.Error(), "unknown plugin domain") {
		t.Fatalf("unknown domain: %v", err)
	}
}

func TestOpenCodeRegisteredSurfaceEmissionMechanics(t *testing.T) {
	// This is not an activation evaluator: all rows here are constructed enabled
	// inputs so emission covers native spellings that remain withheld in product.
	manifest := registration.OpenCode2_0_20().Entries()
	var entries []activation.Entry
	surfaces := map[string]runtime.HookSurface{}
	contract := runtime.OpenCode2_0_20Lifecycle()
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
	runOpenCodeV2SetupModule(t, module, `[]`, fmt.Sprintf(`
const observations = %s, named = %s;
assert.deepEqual(Object.keys(hooks).sort(), named.sort());
assert.equal(subscribed.length, 1, "observations ride one bus subscription");
for (const name of named) {
  const hookEvent = Object.freeze({ sessionID: "constructed", id: "constructed", marker: name });
  const before = JSON.stringify(hookEvent);
  await hooks[name](hookEvent);
  assert.equal(calls.at(-1).argv[5], name);
  assert.deepEqual(calls.at(-1).payload, hookEvent);
  assert.equal(JSON.stringify(hookEvent), before, "a gate consultation never mutates its host event");
}
assert.equal(calls.length, named.length);
`, observationJSON, namedJSON))
}

// copyCommittedModuleToTemp stages the committed plugin artifact beside a
// stub of the host specifier, so Bun proofs load the exact shipped bytes
// hermetically instead of depending on the ambient install cache. Byte
// equality between the committed file and the generator output is held
// separately by the embedded-asset drift guard and the transport parity
// checks; what these proofs assert is the behaviour of the shipped bytes.
func copyCommittedModuleToTemp(t *testing.T, dir string) string {
	t.Helper()
	root := testModuleRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(OpenCodeHooksModulePath)))
	if err != nil {
		t.Fatalf("read committed plugin artifact: %v", err)
	}
	writeOpenCodePluginStub(t, dir)
	modulePath := filepath.Join(dir, "pasture-hooks.ts")
	if err := os.WriteFile(modulePath, raw, 0o600); err != nil {
		t.Fatalf("stage committed plugin artifact: %v", err)
	}
	return modulePath
}
