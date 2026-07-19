package cell_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/cell"
)

func TestCanonicalCellsFrozenOrder(t *testing.T) {
	got := cell.CanonicalCells()
	want := []string{
		"claude-code.skills", "claude-code.agents", "claude-code.hooks",
		"opencode.skills", "opencode.agents", "opencode.hooks",
		"codex.skills", "codex.agents", "codex.hooks",
	}
	if len(got) != len(want) {
		t.Fatalf("cell count = %d, want %d", len(got), len(want))
	}
	for i, c := range got {
		if c.String() != want[i] {
			t.Errorf("cell[%d] = %q, want %q", i, c.String(), want[i])
		}
		if c.Index() != i {
			t.Errorf("cell[%d].Index() = %d, want %d", i, c.Index(), i)
		}
	}
}

func TestParseExtensionRejectsUnknown(t *testing.T) {
	if _, err := cell.ParseExtension("plugins"); err == nil {
		t.Fatal("ParseExtension(plugins) = nil error, want actionable rejection")
	}
	for _, name := range []string{"skills", "agents", "hooks"} {
		ext, err := cell.ParseExtension(name)
		if err != nil {
			t.Fatalf("ParseExtension(%q) unexpected error: %v", name, err)
		}
		if ext.String() != name {
			t.Errorf("ParseExtension(%q).String() = %q", name, ext.String())
		}
	}
}

func TestNewRejectsUnknownHarnessAndZeroExtension(t *testing.T) {
	if _, err := cell.New(ir.HarnessID("gemini"), cell.SkillsAxis()); err == nil {
		t.Error("New with unknown harness = nil error, want rejection")
	}
	if _, err := cell.New(ir.HarnessClaudeCode, cell.Extension{}); err == nil {
		t.Error("New with zero extension = nil error, want rejection")
	}
}

func TestParseCellRoundTrip(t *testing.T) {
	for _, c := range cell.CanonicalCells() {
		parsed, err := cell.ParseCell(c.String())
		if err != nil {
			t.Fatalf("ParseCell(%q) error: %v", c.String(), err)
		}
		if parsed.Index() != c.Index() {
			t.Errorf("ParseCell(%q).Index() = %d, want %d", c.String(), parsed.Index(), c.Index())
		}
	}
	if _, err := cell.ParseCell("codexhooks"); err == nil {
		t.Error("ParseCell without dot = nil error, want rejection")
	}
}

func TestExtensionTextMarshalRoundTrip(t *testing.T) {
	for _, ext := range cell.CanonicalExtensions() {
		encoded, err := ext.MarshalText()
		if err != nil {
			t.Fatalf("MarshalText(%v) error: %v", ext, err)
		}
		var decoded cell.Extension
		if err := decoded.UnmarshalText(encoded); err != nil {
			t.Fatalf("UnmarshalText(%q) error: %v", encoded, err)
		}
		if decoded.String() != ext.String() {
			t.Errorf("round trip = %q, want %q", decoded.String(), ext.String())
		}
	}
	var zero cell.Extension
	if _, err := zero.MarshalText(); err == nil {
		t.Error("MarshalText(zero) = nil error, want rejection")
	}
}
