package preferences_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/preferences"
)

func TestDefaultFirstRun(t *testing.T) {
	p := preferences.Default()
	for _, h := range cell.CanonicalHarnesses() {
		if p.HarnessEnabled(h) {
			t.Errorf("harness %s should default disabled", h)
		}
	}
	if !p.ExtensionEnabled(cell.SkillsAxis()) || !p.ExtensionEnabled(cell.AgentsAxis()) {
		t.Error("skills and agents should default enabled")
	}
	if p.ExtensionEnabled(cell.HooksAxis()) {
		t.Error("hooks should default disabled")
	}
}

func TestEffectiveSelectionDisabledHarnessGetsNoExtensions(t *testing.T) {
	p := preferences.Default() // skills+agents on, all harnesses off
	sel, err := p.EffectiveSelection()
	if err != nil {
		t.Fatalf("EffectiveSelection: %v", err)
	}
	for _, c := range cell.CanonicalCells() {
		if sel.Enabled(c) {
			t.Errorf("cell %s enabled with all harnesses off", c)
		}
	}
	// Enable claude-code; skills/agents cells become effective, hooks stays off.
	p, _ = p.WithHarness(ir.HarnessClaudeCode, true)
	sel, _ = p.EffectiveSelection()
	skills, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	hooks, _ := cell.New(ir.HarnessClaudeCode, cell.HooksAxis())
	opencodeSkills, _ := cell.New(ir.HarnessOpenCode, cell.SkillsAxis())
	if !sel.Enabled(skills) {
		t.Error("claude-code.skills should be effective")
	}
	if sel.Enabled(hooks) {
		t.Error("claude-code.hooks should stay off without explicit hooks opt-in")
	}
	if sel.Enabled(opencodeSkills) {
		t.Error("opencode.skills should stay off while opencode harness disabled")
	}
}

func TestHooksNeverEffectiveWithoutOptIn(t *testing.T) {
	p, _ := preferences.Default().WithHarness(ir.HarnessCodex, true)
	sel, _ := p.EffectiveSelection()
	hooks, _ := cell.New(ir.HarnessCodex, cell.HooksAxis())
	if sel.Enabled(hooks) {
		t.Fatal("hooks effective without explicit opt-in")
	}
	p, _ = p.WithExtension(cell.HooksAxis(), true)
	sel, _ = p.EffectiveSelection()
	if !sel.Enabled(hooks) {
		t.Fatal("hooks not effective after explicit opt-in on an enabled harness")
	}
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	p, err := preferences.Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if p.HarnessEnabled(ir.HarnessClaudeCode) || !p.ExtensionEnabled(cell.SkillsAxis()) {
		t.Error("missing-file load did not return defaults")
	}
}

func TestSaveLoadRoundTripRestoresExplicitHookOptIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := preferences.Default()
	p, _ = p.WithHarness(ir.HarnessOpenCode, true)
	p, _ = p.WithExtension(cell.HooksAxis(), true)
	if err := preferences.Save(path, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := preferences.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.HarnessEnabled(ir.HarnessOpenCode) {
		t.Error("opencode harness not restored")
	}
	if !loaded.ExtensionEnabled(cell.HooksAxis()) {
		t.Error("explicit hooks opt-in not restored")
	}
}

func TestSavePreservesUnrelatedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `provenance:
  db_path: /custom/provenance.db
  enabled: true
telemetry:
  level: verbose
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := preferences.Default().WithHarness(ir.HarnessCodex, true)
	if err := preferences.Save(path, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, _ := os.ReadFile(path)
	text := string(out)
	for _, want := range []string{"/custom/provenance.db", "telemetry:", "level: verbose"} {
		if !strings.Contains(text, want) {
			t.Errorf("unrelated config lost: %q not in\n%s", want, text)
		}
	}
	loaded, _ := preferences.Load(path)
	if !loaded.HarnessEnabled(ir.HarnessCodex) {
		t.Error("codex preference not persisted alongside unrelated keys")
	}
}

func TestLoadFollowsSymlinkedConfig(t *testing.T) {
	// A symlinked config.yaml (the dotfile-manager / Home Manager layout) is read
	// THROUGH, on purpose. Pin the intentional asymmetry versus Save.
	dir := t.TempDir()
	real := filepath.Join(dir, "source-config.yaml")
	doc := `install:
  harnesses:
    opencode: true
  extensions:
    hooks: true
`
	if err := os.WriteFile(real, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	loaded, err := preferences.Load(link)
	if err != nil {
		t.Fatalf("Load through symlink = %v, want success (managed-config workflow)", err)
	}
	if !loaded.HarnessEnabled(ir.HarnessOpenCode) || !loaded.ExtensionEnabled(cell.HooksAxis()) {
		t.Error("symlinked config content not read through the link")
	}
}

func TestSaveRefusesSymlinkedConfigAndLeavesTargetUntouched(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "source-config.yaml")
	original := "telemetry:\n  level: verbose\n"
	if err := os.WriteFile(real, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	p, _ := preferences.Default().WithHarness(ir.HarnessCodex, true)
	err := preferences.Save(link, p)
	if err == nil {
		t.Fatal("Save through a symlinked config = nil error, want rejection")
	}
	// The fix must point the user at the dotfiles source, not at clobbering.
	if !strings.Contains(err.Error(), "symlink's source") {
		t.Errorf("Save fault does not direct the user to the symlink source: %v", err)
	}
	// The real target must be untouched (no atomic replace ran through the link).
	got, _ := os.ReadFile(real)
	if string(got) != original {
		t.Errorf("symlink target was modified: %q", got)
	}
	// The link must still be a link, not replaced by a regular file.
	info, _ := os.Lstat(link)
	if info.Mode().Type()&os.ModeSymlink == 0 {
		t.Error("config symlink was replaced by a regular file")
	}
}

func TestLoadRejectsUnknownHarness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	bad := `install:
  harnesses:
    gemini: true
  extensions:
    skills: true
`
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := preferences.Load(path); err == nil {
		t.Fatal("Load with unknown harness = nil error, want actionable rejection")
	}
}

func TestLoadRejectsUnknownAxis(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	bad := `install:
  extensions:
    plugins: true
`
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := preferences.Load(path); err == nil {
		t.Fatal("Load with unknown axis = nil error, want actionable rejection")
	}
}
