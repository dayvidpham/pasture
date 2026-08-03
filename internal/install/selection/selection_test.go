package selection_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/selection"
)

const validDoc = `schema: pasture.install.effective-selection/v1
cells:
  claude-code: {skills: true, agents: false, hooks: false}
  opencode: {skills: false, agents: true, hooks: false}
  codex: {skills: false, agents: false, hooks: true}
`

func TestParseValidAndAccessors(t *testing.T) {
	sel, err := selection.Parse([]byte(validDoc))
	if err != nil {
		t.Fatalf("Parse valid doc: %v", err)
	}
	claudeSkills, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	if !sel.Enabled(claudeSkills) {
		t.Error("claude-code.skills should be enabled")
	}
	codexHooks, _ := cell.New(ir.HarnessCodex, cell.HooksAxis())
	if !sel.Enabled(codexHooks) {
		t.Error("codex.hooks should be enabled")
	}
	ordered := sel.Ordered()
	if len(ordered) != 9 {
		t.Fatalf("Ordered len = %d, want 9", len(ordered))
	}
	if ordered[0].Cell.String() != "claude-code.skills" || ordered[8].Cell.String() != "codex.hooks" {
		t.Errorf("canonical order broken: first=%s last=%s", ordered[0].Cell, ordered[8].Cell)
	}
}

func TestParseRejectsExtraAxis(t *testing.T) {
	doc := strings.Replace(validDoc,
		"claude-code: {skills: true, agents: false, hooks: false}",
		"claude-code: {skills: true, agents: false, hooks: false, plugins: true}", 1)
	if _, err := selection.Parse([]byte(doc)); err == nil {
		t.Fatal("Parse with extra axis = nil error, want rejection")
	}
}

func TestParseRejectsMissingAxis(t *testing.T) {
	doc := strings.Replace(validDoc,
		"codex: {skills: false, agents: false, hooks: true}",
		"codex: {skills: false, agents: false}", 1)
	if _, err := selection.Parse([]byte(doc)); err == nil {
		t.Fatal("Parse with missing hooks axis = nil error, want rejection")
	}
}

func TestParseRejectsMissingHarness(t *testing.T) {
	doc := `schema: pasture.install.effective-selection/v1
cells:
  claude-code: {skills: true, agents: false, hooks: false}
  opencode: {skills: false, agents: true, hooks: false}
`
	if _, err := selection.Parse([]byte(doc)); err == nil {
		t.Fatal("Parse with missing codex harness = nil error, want rejection")
	}
}

func TestParseRejectsWrongSchema(t *testing.T) {
	doc := strings.Replace(validDoc,
		"pasture.install.effective-selection/v1",
		"pasture.install.effective-selection/v2", 1)
	if _, err := selection.Parse([]byte(doc)); err == nil {
		t.Fatal("Parse with wrong schema = nil error, want rejection")
	}
}

func TestMarshalRoundTripCanonicalAndNormalizesEquivalent(t *testing.T) {
	sel, err := selection.Parse([]byte(validDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	encoded, err := sel.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	reparsed, err := selection.Parse(encoded)
	if err != nil {
		t.Fatalf("re-Parse: %v", err)
	}
	// An installer document and an equivalent Home Manager document (axes given
	// in a different key order) must normalize to identical cells.
	shuffled := `schema: pasture.install.effective-selection/v1
cells:
  codex: {hooks: true, skills: false, agents: false}
  opencode: {agents: true, hooks: false, skills: false}
  claude-code: {hooks: false, agents: false, skills: true}
`
	fromShuffled, err := selection.Parse([]byte(shuffled))
	if err != nil {
		t.Fatalf("Parse shuffled: %v", err)
	}
	for _, c := range cell.CanonicalCells() {
		if reparsed.Enabled(c) != sel.Enabled(c) {
			t.Errorf("round-trip diverged at %s", c)
		}
		if fromShuffled.Enabled(c) != sel.Enabled(c) {
			t.Errorf("key-order variant diverged at %s", c)
		}
	}
}

func TestZeroSelectionMarshalRejected(t *testing.T) {
	var zero selection.Selection
	if _, err := zero.Marshal(); err == nil {
		t.Error("Marshal(zero) = nil error, want rejection")
	}
}
