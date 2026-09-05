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
	contract := runtime.OpenCode1_18_10()
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
		fmt.Sprintf(`["hook", "lifecycle", "--harness", "opencode", "--event", "session.created", "--host-version", %q]`, openCodeHostVersion()),
		fmt.Sprintf(`["hook", "lifecycle", "--harness", "opencode", "--event", "tool.execute.before", "--host-version", %q]`, openCodeHostVersion()),
		`{ input, output: { args } }`, `response.decision !== "proceed"`, `output.args = args`,
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
	path := filepath.Join(t.TempDir(), "pasture-hooks.mjs")
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
await sessionCreated(sessionCapture.value);
const output = toolCapture.value.output;
const before = JSON.stringify(output.args);
await toolExecuteBefore(toolCapture.value.input, output);
if (JSON.stringify(output.args) !== before) throw new Error("generated tool.execute.before callback changed output.args");
console.log(JSON.stringify({ argsUnchanged: true }));
`, moduleURL,
		filepath.Join(fixtureDir, "session_created_1_18_10.capture.json"),
		filepath.Join(fixtureDir, "tool_execute_before_1_18_10.capture.json"))
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatalf("write Bun production-proof runner: %v", err)
	}
	dbPath := filepath.Join(dir, "pasture.db")
	bootstrap := exec.Command(binary, "--db", dbPath, "--namespace", "file://opencode-production-proof", "task", "create", "initialize lifecycle identity")
	if bootstrapOutput, bootstrapErr := bootstrap.CombinedOutput(); bootstrapErr != nil {
		t.Fatalf("initialize real temporary Pasture store through production CLI: %v\n%s", bootstrapErr, bootstrapOutput)
	}
	proof := exec.Command(bun, runner)
	proof.Env = append(os.Environ(), "PASTURE_BIN="+binary, "PASTURE_DB_PATH="+dbPath)
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
		fmt.Sprintf(`"contract":%q`, runtime.OpenCode1_18_10().ID().String()),
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
	proof.Env = append(os.Environ(), "PASTURE_BIN="+fakeBinary)
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
	want := runtime.OpenCode1_18_10().ID()
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
	proof.Env = append(os.Environ(), "PASTURE_BIN="+fakeBinary)
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

const output = toolCapture.value.output;
const before = JSON.stringify(output.args);
await toolExecuteBefore(toolCapture.value.input, output);
if (JSON.stringify(output.args) !== before) {
  throw new Error("a pasture fault changed output.args");
}
console.log(JSON.stringify({ toolCallProceeded: true }));
`, moduleURL, filepath.Join(fixtureDir, "tool_execute_before_1_18_10.capture.json"))
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
