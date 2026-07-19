package promotion_test

import (
	"reflect"
	"testing"

	"github.com/dayvidpham/pasture/internal/promotion"
	"github.com/dayvidpham/pasture/internal/target/claudecode"
)

func mustDescriptor(t *testing.T) claudecode.TargetDescriptor {
	t.Helper()
	d, err := claudecode.Descriptor()
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	return d
}

func TestProjectClaudeCodeProducesOneEntryPerComponent(t *testing.T) {
	proj, err := promotion.ProjectClaudeCode(mustDescriptor(t), "aura-plugins", "dayvidpham/pasture", promotion.DefaultStableRef)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(proj.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(proj.Entries))
	}
	names := map[string]promotion.MarketplaceEntry{}
	for _, e := range proj.Entries {
		names[e.Name] = e
		if e.Source.Source != promotion.SourceGitHub {
			t.Errorf("%s source = %q, want github", e.Name, e.Source.Source)
		}
		if e.Source.Repo != "dayvidpham/pasture" {
			t.Errorf("%s repo = %q", e.Name, e.Source.Repo)
		}
		if e.Version == "" {
			t.Errorf("%s has empty version", e.Name)
		}
	}
	for _, want := range []string{"pasture-skills", "pasture-agents", "pasture-hooks"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing projected plugin %q", want)
		}
	}
}

func TestProjectClaudeCodeSelectorsMatchComponentIDs(t *testing.T) {
	proj, err := promotion.ProjectClaudeCode(mustDescriptor(t), "aura-plugins", "dayvidpham/pasture", promotion.DefaultStableRef)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	got := proj.Selectors()
	// Selectors are the stable activation identities, in canonical (name-sorted)
	// order: agents, hooks, skills.
	want := []string{"claude-code/agents", "claude-code/hooks", "claude-code/skills"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selectors = %v, want %v", got, want)
	}
}

func TestProjectClaudeCodeIsDeterministic(t *testing.T) {
	a, err := promotion.ProjectClaudeCode(mustDescriptor(t), "aura-plugins", "dayvidpham/pasture", promotion.DefaultStableRef)
	if err != nil {
		t.Fatalf("project a: %v", err)
	}
	b, err := promotion.ProjectClaudeCode(mustDescriptor(t), "aura-plugins", "dayvidpham/pasture", promotion.DefaultStableRef)
	if err != nil {
		t.Fatalf("project b: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("projection is not deterministic:\n a=%+v\n b=%+v", a, b)
	}
}

func TestProjectClaudeCodeRejectsEmptyOperands(t *testing.T) {
	d := mustDescriptor(t)
	cases := []struct {
		name                    string
		market, repo, sourceRef string
	}{
		{"empty market", "", "dayvidpham/pasture", promotion.DefaultStableRef},
		{"empty repo", "aura-plugins", "", promotion.DefaultStableRef},
		{"empty ref", "aura-plugins", "dayvidpham/pasture", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := promotion.ProjectClaudeCode(d, tc.market, tc.repo, tc.sourceRef); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestProjectClaudeCodeRejectsInvalidDescriptor(t *testing.T) {
	if _, err := promotion.ProjectClaudeCode(claudecode.TargetDescriptor{}, "aura-plugins", "dayvidpham/pasture", promotion.DefaultStableRef); err == nil {
		t.Fatal("expected error for zero descriptor")
	}
}

func TestFindEntry(t *testing.T) {
	proj, err := promotion.ProjectClaudeCode(mustDescriptor(t), "aura-plugins", "dayvidpham/pasture", promotion.DefaultStableRef)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	e, ok := proj.FindEntry("pasture-skills")
	if !ok {
		t.Fatal("pasture-skills not found")
	}
	if e.ComponentID != "claude-code/skills" {
		t.Fatalf("component id = %q", e.ComponentID)
	}
	if _, ok := proj.FindEntry("nonexistent"); ok {
		t.Fatal("unexpectedly found nonexistent entry")
	}
}
