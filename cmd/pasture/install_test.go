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

func TestCLI_InstallStatus_ReportsRecordedCellsJSON(t *testing.T) {
	t.Parallel()
	state := filepath.Join(t.TempDir(), "installations.yaml")
	project := t.TempDir()
	body := fmt.Sprintf(`schema: pasture.install.registry/v1
global_installations:
  - cell: opencode.hooks
    source: installer
    strategy: direct-file
    managed: true
    observation: installed
    trust: not-applicable
    last_operation: ensure
    last_outcome: completed
  - cell: codex.hooks
    source: installer
    strategy: native-plugin-pending-trust
    managed: true
    observation: installed
    trust: pending
    last_operation: ensure
    last_outcome: completed
project_installations:
  - canonical_project_root: %s
    cell: opencode.hooks
    source: installer
    strategy: direct-file
    managed: false
    observation: absent
    trust: not-applicable
    last_operation: remove
    last_outcome: completed
`, project)
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
			Trust       string `json:"trust"`
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
	if decoded.Cells[1].Trust != "pending" {
		t.Errorf("codex hooks trust should be pending: %+v", decoded.Cells[1])
	}
	if decoded.Cells[0].Scope != "global" || decoded.Cells[0].ProjectRoot != "" || decoded.Cells[2].Scope != "project" || decoded.Cells[2].ProjectRoot != project {
		t.Fatalf("scoped rows=%+v", decoded.Cells)
	}
}

func TestCLI_InstallStatus_ProjectOnlyTextNamesScopeAndRoot(t *testing.T) {
	t.Parallel()
	state := filepath.Join(t.TempDir(), "installations.yaml")
	project := t.TempDir()
	body := fmt.Sprintf("schema: pasture.install.registry/v1\nglobal_installations: []\nproject_installations:\n  - canonical_project_root: %s\n    cell: codex.agents\n    source: installer\n    strategy: direct-file\n    managed: true\n    observation: installed\n    trust: not-applicable\n    last_operation: ensure\n    last_outcome: completed\n", project)
	if err := os.WriteFile(state, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runCLI(t, "install", "status", "--state", state)
	if out.exitCode != 0 {
		t.Fatalf("status exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	if !strings.Contains(out.stdout, "project:"+project) || !strings.Contains(out.stdout, "codex.agents") || strings.Contains(out.stdout, "nothing has been installed") {
		t.Fatalf("project-only text omitted scope/root: %s", out.stdout)
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
