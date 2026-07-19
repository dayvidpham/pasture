package codegen

import (
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
// emitter produces exactly one `.codex/agents/<role>.toml` per tool-bearing
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
		if prev != "" && f.Path < prev {
			t.Fatalf("agent profiles are not path-sorted: %q after %q", f.Path, prev)
		}
		prev = f.Path
	}
}

// TestCodexAgentFunctionsAreContractDerived proves every emitted agent profile
// declares exactly the native Codex functions the pinned contract classifies —
// for Codex 0.144.1 the sole native call `request-input` — and never a
// fabricated skill/spawn function.
func TestCodexAgentFunctionsAreContractDerived(t *testing.T) {
	t.Parallel()

	want := codexNativeFunctions()
	if len(want) == 0 {
		t.Fatal("codexNativeFunctions() returned no native calls; the pinned Codex contract must classify at least request-input")
	}
	// The only native Codex 0.144.1 operation is RequestUserDecision -> request-input.
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
