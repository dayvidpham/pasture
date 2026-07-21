package promotion_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dayvidpham/pasture/internal/promotion"
)

func candidateTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"pasture-agents", "pasture-hooks", "pasture-skills"} {
		source := filepath.Join("..", "target", "claudecode", "assets", name, ".claude-plugin", "plugin.json")
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		destination := filepath.Join(root, "internal", "target", "claudecode", "assets", name, ".claude-plugin", "plugin.json")
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func projectCandidateTree(t *testing.T) promotion.Projection {
	t.Helper()
	projection, err := promotion.ProjectClaudeCodeTree(candidateTree(t), "aura-plugins", testPastureCommit)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	return projection
}

func TestProjectClaudeCodeTreePinsExactDistinctSources(t *testing.T) {
	projection := projectCandidateTree(t)
	want := map[string]string{
		"pasture-agents": "internal/target/claudecode/assets/pasture-agents",
		"pasture-hooks":  "internal/target/claudecode/assets/pasture-hooks",
		"pasture-skills": "internal/target/claudecode/assets/pasture-skills",
	}
	if len(projection.Entries) != len(want) {
		t.Fatalf("entries = %d, want %d", len(projection.Entries), len(want))
	}
	paths := map[string]struct{}{}
	for _, entry := range projection.Entries {
		if entry.Source.Source != promotion.SourceGitSubdir || entry.Source.URL != "https://github.com/dayvidpham/pasture.git" || entry.Source.SHA != testPastureCommit || entry.Source.Path != want[entry.Name] {
			t.Errorf("%s source = %+v, want canonical exact tuple", entry.Name, entry.Source)
		}
		if _, duplicate := paths[entry.Source.Path]; duplicate {
			t.Errorf("source path %q is not pairwise distinct", entry.Source.Path)
		}
		paths[entry.Source.Path] = struct{}{}
	}
}

func TestProjectClaudeCodeTreeSelectorsAndDeterminism(t *testing.T) {
	root := candidateTree(t)
	a, err := promotion.ProjectClaudeCodeTree(root, "aura-plugins", testPastureCommit)
	if err != nil {
		t.Fatal(err)
	}
	b, err := promotion.ProjectClaudeCodeTree(root, "aura-plugins", testPastureCommit)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("projection differs: a=%+v b=%+v", a, b)
	}
	want := []string{"claude-code/agents", "claude-code/hooks", "claude-code/skills"}
	if !reflect.DeepEqual(a.Selectors(), want) {
		t.Fatalf("selectors = %v, want %v", a.Selectors(), want)
	}
}

func TestProjectClaudeCodeTreeRejectsMissingCanonicalPath(t *testing.T) {
	root := candidateTree(t)
	if err := os.Remove(filepath.Join(root, "internal", "target", "claudecode", "assets", "pasture-hooks", ".claude-plugin", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := promotion.ProjectClaudeCodeTree(root, "aura-plugins", testPastureCommit); err == nil {
		t.Fatal("expected missing canonical manifest to fail")
	}
}

func TestFindEntry(t *testing.T) {
	projection := projectCandidateTree(t)
	entry, ok := projection.FindEntry("pasture-skills")
	if !ok || entry.ComponentID != "claude-code/skills" {
		t.Fatalf("pasture-skills entry = %+v, present=%v", entry, ok)
	}
}
