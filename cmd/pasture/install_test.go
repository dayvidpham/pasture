package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_InstallPlan_NormalizesGlobalChoices(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	// opencode enabled; skills+agents+hooks global; claude/codex disabled.
	body := `install:
  harnesses:
    claude-code: false
    opencode: true
    codex: false
  extensions:
    skills: true
    agents: true
    hooks: true
`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runCLI(t, "--format", "json", "install", "plan", "--config", cfg)
	if out.exitCode != 0 {
		t.Fatalf("plan exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	var decoded struct {
		Cells map[string]bool `json:"cells"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &decoded); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.stdout)
	}
	// opencode cells effective; disabled harnesses get nothing.
	if !decoded.Cells["opencode.skills"] || !decoded.Cells["opencode.hooks"] {
		t.Errorf("opencode cells should be effective: %+v", decoded.Cells)
	}
	if decoded.Cells["claude-code.skills"] || decoded.Cells["codex.agents"] {
		t.Errorf("disabled harness cells must be off: %+v", decoded.Cells)
	}
}

func TestCLI_InstallPlan_MissingConfigUsesDefaults(t *testing.T) {
	t.Parallel()
	out := runCLI(t, "install", "plan", "--config", filepath.Join(t.TempDir(), "none.yaml"))
	if out.exitCode != 0 {
		t.Fatalf("plan exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	// First-run defaults: everything effective-false (no harness enabled).
	if !strings.Contains(out.stdout, "pasture.install.effective-selection/v1") {
		t.Errorf("missing schema in default plan: %s", out.stdout)
	}
	if strings.Contains(out.stdout, "true") {
		t.Errorf("first-run plan should have no effective cells: %s", out.stdout)
	}
}

func TestCLI_InstallPlan_RejectsUnknownHarness(t *testing.T) {
	t.Parallel()
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	_ = os.WriteFile(cfg, []byte("install:\n  harnesses:\n    gemini: true\n"), 0o644)
	out := runCLI(t, "install", "plan", "--config", cfg)
	if out.exitCode != 1 {
		t.Fatalf("expected exit 1 for unknown harness; got %d; stderr=%s", out.exitCode, out.stderr)
	}
	if !strings.Contains(out.stdout+out.stderr, "gemini") {
		t.Errorf("error should name the unknown harness: %s", out.stdout+out.stderr)
	}
}

func TestCLI_InstallStatus_EmptyStateReported(t *testing.T) {
	t.Parallel()
	out := runCLI(t, "install", "status", "--state", filepath.Join(t.TempDir(), "installations.yaml"))
	if out.exitCode != 0 {
		t.Fatalf("status exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	if !strings.Contains(out.stdout, "nothing has been installed") {
		t.Errorf("empty state should be reported: %s", out.stdout)
	}
}

func mixedScopeStatusDocument(project string) string {
	return fmt.Sprintf(`schema: pasture.install.registry/v1
global_installations:
  - cell: opencode.hooks
    source: installer
    strategy: direct-file
    managed: true
    observation: installed
    trust: pending
    last_operation: ensure
    last_outcome: completed
    diagnostic: global hooks await native trust
  - cell: codex.hooks
    source: home-manager
    strategy: native-plugin-pending-trust
    managed: false
    observation: unknown
    trust: not-applicable
    last_operation: inspect
    last_outcome: failed
    diagnostic: global codex inspection failed
project_installations:
  - canonical_project_root: %s
    cell: opencode.hooks
    source: home-manager
    strategy: native-plugin
    managed: false
    observation: absent
    trust: trusted
    last_operation: remove
    last_outcome: failed
    diagnostic: project hook removal needs retry
`, project)
}

func TestCLI_InstallStatus_ReportsRecordedCellsJSON(t *testing.T) {
	t.Parallel()
	state := filepath.Join(t.TempDir(), "installations.yaml")
	project := t.TempDir()
	body := mixedScopeStatusDocument(project)
	if err := os.WriteFile(state, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runCLI(t, "--format", "json", "install", "status", "--state", state)
	if out.exitCode != 0 {
		t.Fatalf("status exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	var decoded struct {
		Cells []struct {
			Scope       string `json:"scope"`
			ProjectRoot string `json:"project_root"`
			Cell        string `json:"cell"`
			Observation string `json:"observation"`
			Strategy    string `json:"strategy"`
			Source      string `json:"source"`
			Managed     bool   `json:"managed"`
			Trust       string `json:"trust"`
			LastAction  string `json:"last_action"`
			LastOutcome string `json:"last_outcome"`
			Diagnostic  string `json:"diagnostic"`
		} `json:"cells"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &decoded); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.stdout)
	}
	if len(decoded.Cells) != 3 {
		t.Fatalf("cells = %d, want 3", len(decoded.Cells))
	}
	// canonical order: opencode.hooks before codex.hooks
	if decoded.Cells[0].Cell != "opencode.hooks" || decoded.Cells[1].Cell != "codex.hooks" || decoded.Cells[2].Cell != "opencode.hooks" {
		t.Errorf("canonical order broken: %+v", decoded.Cells)
	}
	want := []struct {
		scope, root, cell, observation, strategy, source string
		managed                                          bool
		trust, action, outcome, diagnostic               string
	}{
		{"global", "", "opencode.hooks", "installed", "direct-file", "installer", true, "pending", "ensure", "completed", "global hooks await native trust"},
		{"global", "", "codex.hooks", "unknown", "native-plugin-pending-trust", "home-manager", false, "not-applicable", "inspect", "failed", "global codex inspection failed"},
		{"project", project, "opencode.hooks", "absent", "native-plugin", "home-manager", false, "trusted", "remove", "failed", "project hook removal needs retry"},
	}
	for i, expected := range want {
		got := decoded.Cells[i]
		if got.Scope != expected.scope || got.ProjectRoot != expected.root || got.Cell != expected.cell || got.Observation != expected.observation || got.Strategy != expected.strategy || got.Source != expected.source || got.Managed != expected.managed || got.Trust != expected.trust || got.LastAction != expected.action || got.LastOutcome != expected.outcome || got.Diagnostic != expected.diagnostic {
			t.Errorf("cells[%d]=%+v, want %+v", i, got, expected)
		}
	}
}

func TestCLI_InstallStatus_MixedScopeTextKeepsFactsWithSameCellRows(t *testing.T) {
	t.Parallel()
	state := filepath.Join(t.TempDir(), "installations.yaml")
	project := t.TempDir()
	body := mixedScopeStatusDocument(project)
	if err := os.WriteFile(state, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runCLI(t, "install", "status", "--state", state)
	if out.exitCode != 0 {
		t.Fatalf("status exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	globalStart := strings.Index(out.stdout, "opencode.hooks")
	projectStart := strings.LastIndex(out.stdout, "opencode.hooks")
	if globalStart < 0 || projectStart <= globalStart {
		t.Fatalf("mixed same-cell rows missing: %s", out.stdout)
	}
	globalEnd := strings.Index(out.stdout[globalStart:], "codex.hooks") + globalStart
	globalBlock := out.stdout[globalStart:globalEnd]
	projectBlock := out.stdout[projectStart:]
	for _, want := range []string{"installed", "direct-file", "global", "installer/pasture-managed", "pending", "last: ensure -> completed", "note: global hooks await native trust"} {
		if !strings.Contains(globalBlock, want) {
			t.Errorf("global same-cell block omitted %q:\n%s", want, globalBlock)
		}
	}
	for _, want := range []string{"absent", "native-plugin", "project:" + project, "home-manager/external", "trusted", "last: remove -> failed", "note: project hook removal needs retry"} {
		if !strings.Contains(projectBlock, want) {
			t.Errorf("project same-cell block omitted %q:\n%s", want, projectBlock)
		}
	}
}

func TestCLI_InstallStatusJSONOmitsAbsentLastAction(t *testing.T) {
	t.Parallel()
	state := filepath.Join(t.TempDir(), "installations.yaml")
	body := "schema: pasture.install.registry/v1\nglobal_installations:\n  - cell: claude-code.skills\n    source: installer\n    strategy: native-plugin\n    managed: false\n    observation: absent\n    trust: not-applicable\n    last_operation: none\n    last_outcome: none\nproject_installations: []\n"
	if err := os.WriteFile(state, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runCLI(t, "--format", "json", "install", "status", "--state", state)
	if out.exitCode != 0 {
		t.Fatalf("status exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	var decoded struct {
		Cells []map[string]any `json:"cells"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Cells) != 1 {
		t.Fatalf("cells=%v", decoded.Cells)
	}
	if _, ok := decoded.Cells[0]["last_action"]; ok {
		t.Fatalf("absent last_action was serialized: %v", decoded.Cells[0])
	}
	if _, ok := decoded.Cells[0]["last_outcome"]; ok {
		t.Fatalf("absent last_outcome was serialized: %v", decoded.Cells[0])
	}
}

func TestCLI_InstallStatus_RejectsSymlinkStateFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	real := filepath.Join(dir, "real.yaml")
	_ = os.WriteFile(real, []byte("schema: pasture.install.state/v1\ncells: []\n"), 0o600)
	link := filepath.Join(dir, "installations.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	out := runCLI(t, "install", "status", "--state", link)
	if out.exitCode != 1 {
		t.Fatalf("expected exit 1 for symlinked state; got %d; stderr=%s", out.exitCode, out.stderr)
	}
}
