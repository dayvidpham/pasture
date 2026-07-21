package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func normalizeInstallCLIForHarnessParity(t *testing.T, content string) string {
	t.Helper()
	const bodyHeading = "## Behavior\n"
	index := strings.Index(content, bodyHeading)
	if index < 0 {
		t.Fatalf("install-cli output has no canonical body boundary %q", bodyHeading)
	}
	return content[index:]
}

func TestInstallCLICanonicalRendererPreservesClaudeAndNormalizesTargetParity(t *testing.T) {
	t.Parallel()

	claude, err := renderInstallCLISkill(HarnessClaudeCode)
	if err != nil {
		t.Fatalf("render Claude install-cli: %v", err)
	}
	openCode, err := renderInstallCLISkill(HarnessOpenCode)
	if err != nil {
		t.Fatalf("render OpenCode install-cli: %v", err)
	}
	wantClaude, err := os.ReadFile(filepath.Join(testModuleRoot(t), "skills", "install-cli", "SKILL.md"))
	if err != nil {
		t.Fatalf("read shipped Claude install-cli: %v", err)
	}
	if claude != string(wantClaude) {
		t.Error("canonical renderer changed the shipped Claude install-cli behavior")
	}
	if got, want := normalizeInstallCLIForHarnessParity(t, openCode), normalizeInstallCLIForHarnessParity(t, claude); got != want {
		t.Error("install-cli canonical body differs across harness render contexts")
	}
}

func TestOpenCodeInstallCLIRejectsClaudeIdentityPathsAndInvocationWording(t *testing.T) {
	t.Parallel()

	content, err := renderInstallCLISkill(HarnessOpenCode)
	if err != nil {
		t.Fatalf("render OpenCode install-cli: %v", err)
	}
	for _, forbidden := range []string{"Claude Code", "~/.claude", "CLAUDE.md", "Invoke via Claude"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("OpenCode install-cli contains forbidden Claude wording %q", forbidden)
		}
	}
	for _, required := range []string{"OpenCode skill for automated Pasture binary installation.", "Invoke via OpenCode:"} {
		if !strings.Contains(content, required) {
			t.Errorf("OpenCode install-cli is missing target guidance %q", required)
		}
	}
}

func TestInstallCLIRendererRejectsTargetWithoutTypedContext(t *testing.T) {
	t.Parallel()

	_, err := renderInstallCLISkill(HarnessName("unregistered"))
	if err == nil || !strings.Contains(err.Error(), "no install-cli render context") {
		t.Fatalf("renderInstallCLISkill error = %v, want missing typed-context error", err)
	}
}
