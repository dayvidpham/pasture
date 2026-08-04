package codegen

import (
	"context"
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
// 1.18.10 declares: invoke-skill -> skill, delegate-assignment -> task,
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
	// Discoverable: a default export is required for OpenCode plugin loading.
	if !strings.Contains(module, "export default PastureLifecycle") {
		t.Error("hooks module lacks a default export; OpenCode plugin auto-discovery needs one")
	}
}

func TestOpenCodeHooksModulePreservesNamedAndObservationBoundary(t *testing.T) {
	t.Parallel()

	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, required := range []string{
		`"session.created": false`, `"tool.execute.before": false`,
		`["hook", "lifecycle", "--harness", "opencode", "--event", "session.created", "--host-version", "1.18.10"]`,
		`["hook", "lifecycle", "--harness", "opencode", "--event", "tool.execute.before", "--host-version", "1.18.10"]`,
		`{ input, output: { args } }`, `response.decision !== "proceed"`, `output.args = args`,
	} {
		if !strings.Contains(module, required) {
			t.Errorf("generated plugin lacks %q", required)
		}
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
		`"registrationContract":"opencode/1.18.10"`,
		`"contract":"opencode/opencode@1.18.10"`,
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
