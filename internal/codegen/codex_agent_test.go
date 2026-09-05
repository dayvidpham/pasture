package codegen

import (
	"path/filepath"
	"strings"
	"testing"
)

// countToolRoles returns the number of roles that carry tools (the role set the
// agent emitters cover).
func countToolRoles() int {
	n := 0
	for _, spec := range RoleSpecs {
		if len(spec.Tools) > 0 {
			n++
		}
	}
	return n
}

// TestCodexAgentEmitsStandaloneProfilePerToolRole proves the Codex agent
// emitter produces exactly one `.codex/agents/pasture-<role>.toml` per tool-bearing
// role, sorted by path.
func TestCodexAgentEmitsStandaloneProfilePerToolRole(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	files, err := codexAgentEmitter{}.Emit(root, "", GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("codexAgentEmitter.Emit: %v", err)
	}
	if want := countToolRoles(); len(files) != want {
		t.Fatalf("emitted %d agent profiles, want one per tool-bearing role (%d)", len(files), want)
	}
	prev := ""
	for _, f := range files {
		if !strings.HasSuffix(f.Path, ".toml") {
			t.Fatalf("agent profile %q is not a .toml file", f.Path)
		}
		if !strings.Contains(f.Path, ".codex") {
			t.Fatalf("agent profile %q is not under the .codex agents root", f.Path)
		}
		if !strings.HasPrefix(filepath.Base(f.Path), "pasture-") {
			t.Fatalf("agent profile %q lacks the required pasture- namespace", f.Path)
		}
		if prev != "" && f.Path < prev {
			t.Fatalf("agent profiles are not path-sorted: %q after %q", f.Path, prev)
		}
		prev = f.Path
	}
}

// TestCodexAgentFunctionsAreContractDerived proves every emitted agent profile
// declares exactly the native Codex functions the pinned contract classifies —
// for the recorded Codex version the sole native call `request-input` — and never a
// fabricated skill/spawn function.
func TestCodexAgentFunctionsAreContractDerived(t *testing.T) {
	t.Parallel()

	want := codexNativeFunctions()
	if len(want) == 0 {
		t.Fatal("codexNativeFunctions() returned no native calls; the pinned Codex contract must classify at least request-input")
	}
	// The only native Codex operation is RequestUserDecision -> request-input.
	if len(want) != 1 || want[0] != "request-input" {
		t.Fatalf("pinned Codex native functions = %v, want exactly [request-input]", want)
	}

	root := t.TempDir()
	files, err := codexAgentEmitter{}.Emit(root, "", GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("codexAgentEmitter.Emit: %v", err)
	}
	for _, f := range files {
		got := parseCodexAgentFunctions(f.Content)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("agent %q declares functions %v, want contract-derived %v", f.Path, got, want)
		}
	}
}

// TestCodexAgentHeaderStatesTheMechanismAndClaimsNothingAboutTheHost pins the
// comment block above `functions` in every emitted profile. The text a reader
// meets must say WHERE the list comes from — the pinned runtime contract's
// native operation bindings — and must NOT claim that the Codex host exposes no
// skill or agent-spawn function. That claim was false of the host, and because
// it carried the pinned version it was re-asserted, unchecked, at every bump.
func TestCodexAgentHeaderStatesTheMechanismAndClaimsNothingAboutTheHost(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	files, err := codexAgentEmitter{}.Emit(root, "", GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("codexAgentEmitter.Emit: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("codexAgentEmitter emitted no profile; the header pin would hold nothing and would pass vacuously")
	}
	const mechanism = "`functions` is derived from the pinned runtime contract's native operation"
	const parentMediated = "this profile does not use the host's skill or agent-spawn"
	for _, f := range files {
		if !strings.Contains(f.Content, mechanism) {
			t.Fatalf("agent profile %q does not state where the function list comes from; it must carry %q:\n%s", f.Path, mechanism, f.Content)
		}
		if !strings.Contains(f.Content, parentMediated) {
			t.Fatalf("agent profile %q does not state what this profile uses; it must carry %q:\n%s", f.Path, parentMediated, f.Content)
		}
		if strings.Contains(f.Content, "exposes no skill") || strings.Contains(f.Content, "exposes no self-service spawn") {
			t.Fatalf("agent profile %q claims the Codex host exposes no skill or spawn function; the host ships both, so the header must describe pasture's profile only:\n%s", f.Path, f.Content)
		}
	}
}

// TestCodexAgentRenderIsDeterministic proves the agent renderer is a pure
// function of its inputs: two renders of the same role are byte-identical.
func TestCodexAgentRenderIsDeterministic(t *testing.T) {
	t.Parallel()

	functions := codexNativeFunctions()
	for roleID, spec := range RoleSpecs {
		if len(spec.Tools) == 0 {
			continue
		}
		a, err := renderCodexAgent(roleID, functions)
		if err != nil {
			t.Fatalf("renderCodexAgent(%s): %v", roleID, err)
		}
		b, err := renderCodexAgent(roleID, functions)
		if err != nil {
			t.Fatalf("renderCodexAgent(%s) second call: %v", roleID, err)
		}
		if a != b {
			t.Fatalf("renderCodexAgent(%s) is not deterministic", roleID)
		}
	}
}

// TestCodexModelCoversEveryToolRole proves the Codex model map has an entry for
// every model nickname a tool-bearing role uses, so emission can never fail on
// an unmapped model at generation time.
func TestCodexModelCoversEveryToolRole(t *testing.T) {
	t.Parallel()

	for roleID, spec := range RoleSpecs {
		if len(spec.Tools) == 0 {
			continue
		}
		if _, ok := codexModel[spec.Model]; !ok {
			t.Fatalf("role %q model nickname %q is missing from codexModel", roleID, spec.Model)
		}
		if _, ok := codexRoleClasses[roleID]; !ok {
			t.Fatalf("role %q is missing from codexRoleClasses", roleID)
		}
	}
}

// TestRenderCodexAgentUnknownRoleIsActionable proves an unknown role produces a
// six-part-style actionable error rather than a panic or opaque failure.
func TestRenderCodexAgentUnknownRoleIsActionable(t *testing.T) {
	t.Parallel()

	_, err := renderCodexAgent("no-such-role", codexNativeFunctions())
	if err == nil {
		t.Fatal("renderCodexAgent(unknown role) returned nil error")
	}
	for _, want := range []string{"no-such-role", "RoleSpecs"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

// TestTOMLStringEscaping proves the basic-string escaper protects the emitted
// profile from a quote/backslash/control character in a description.
func TestTOMLStringEscaping(t *testing.T) {
	t.Parallel()

	got := tomlString(`a"b\c` + "\n\t")
	want := `"a\"b\\c\n\t"`
	if got != want {
		t.Fatalf("tomlString escaping = %q, want %q", got, want)
	}
}
