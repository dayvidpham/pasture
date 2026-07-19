package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// expectedOpenCodeNativeTools is the exact native tool allow-list OpenCode
// 1.17.18 declares: invoke-skill -> skill, delegate-assignment -> task,
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
	contract := runtime.OpenCode1_17_18()
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
	if !strings.Contains(module, "export default PastureHooks") {
		t.Error("hooks module lacks a default export; OpenCode plugin auto-discovery needs one")
	}
}

// TestOpenCodeHooksModule_ParsesUnderJSEngine is an opportunistic isolated-load
// oracle: when a JavaScript engine is available it confirms the emitted module
// parses on its own (siblings absent). It skips cleanly when no engine is
// installed so the Go suite stays dependency-free and deterministic.
func TestOpenCodeHooksModule_ParsesUnderJSEngine(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on PATH; skipping opportunistic JS parse oracle")
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
	out, err := exec.Command(node, "--check", path).CombinedOutput()
	if err != nil {
		t.Fatalf("node --check rejected the isolated module: %v\n%s", err, out)
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
	extra := []artifact.Source{
		{Path: "skills/worker/SKILL.md", Type: artifact.EntryTypeSkill, Mode: 0o644, Content: []byte("worker skill\n")},
		{Path: "agent/reviewer.md", Type: artifact.EntryTypeAgent, Mode: 0o644, Content: []byte("reviewer\n")},
	}
	bundle, err := desc.Bundle(extra)
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if bundle.ID() != desc.RuntimeContract().String() {
		t.Errorf("bundle id = %q, want the RuntimeContractID %q", bundle.ID(), desc.RuntimeContract().String())
	}

	entries := bundle.Manifest().Entries
	if len(entries) != 4 {
		t.Fatalf("expected 4 bundle entries, got %d", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Path >= entries[i].Path {
			t.Errorf("bundle manifest not lexicographically sorted at %d: %q >= %q", i, entries[i-1].Path, entries[i].Path)
		}
	}
	for _, e := range entries {
		if !digestRE.MatchString(e.Digest) {
			t.Errorf("entry %q digest %q not sha256:<64 hex>", e.Path, e.Digest)
		}
		if e.Mode.Perm() != 0o644 {
			t.Errorf("entry %q mode %o, want 0644", e.Path, e.Mode.Perm())
		}
		// Isolated retrieval: each component's bytes are independently available,
		// so a materializer can write each file with siblings absent.
		if _, ok := bundle.Content(e.Path); !ok {
			t.Errorf("entry %q has no retrievable content", e.Path)
		}
	}

	// The two target-owned components are always present.
	paths := strings.Join(bundle.Paths(), "\n")
	for _, want := range []string{OpenCodeHooksModulePath, OpenCodeTargetManifestPath} {
		if !strings.Contains(paths, want) {
			t.Errorf("bundle missing target-owned component %q", want)
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
	if b1.Manifest().Digest() != b2.Manifest().Digest() {
		t.Fatal("target bundle is not deterministic across descriptor builds")
	}
}

func TestOpenCodeTargetDescriptor_RuntimeContractIdentity(t *testing.T) {
	desc, err := NewOpenCodeTargetDescriptor()
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	want := runtime.OpenCode1_17_18().ID()
	if desc.RuntimeContract() != want {
		t.Errorf("descriptor RuntimeContract = %v, want %v", desc.RuntimeContract(), want)
	}
	if desc.RuntimeContract().Harness() != ir.HarnessOpenCode {
		t.Errorf("descriptor harness = %v, want opencode", desc.RuntimeContract().Harness())
	}
}
