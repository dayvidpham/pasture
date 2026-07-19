package artifact_test

import (
	"io/fs"
	"regexp"
	"testing"
	"testing/fstest"

	"github.com/dayvidpham/pasture/internal/artifact"
)

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func mustBundle(t *testing.T, id string, sources []artifact.Source) artifact.Bundle {
	t.Helper()
	b, err := artifact.NewBundle(id, sources)
	if err != nil {
		t.Fatalf("NewBundle(%q): unexpected error: %v", id, err)
	}
	return b
}

func sampleSources() []artifact.Source {
	// Deliberately supplied out of lexicographic order to prove the manifest sorts.
	return []artifact.Source{
		{Path: "skills/worker/SKILL.md", Type: artifact.EntryTypeSkill, Mode: 0o644, Content: []byte("worker skill\n")},
		{Path: "agent/reviewer.md", Type: artifact.EntryTypeAgent, Mode: 0o644, Content: []byte("reviewer agent\n")},
		{Path: "pasture-hooks/pasture-hooks.ts", Type: artifact.EntryTypeHook, Mode: 0o644, Content: []byte("export default () => ({})\n")},
		{Path: "opencode.json", Type: artifact.EntryTypeManifest, Mode: 0o644, Content: []byte("{}\n")},
	}
}

func TestNewBundle_ManifestSortedLexicographically(t *testing.T) {
	b := mustBundle(t, "opencode@1.17.18", sampleSources())
	entries := b.Manifest().Entries
	if len(entries) != 4 {
		t.Fatalf("expected 4 manifest entries, got %d", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Path >= entries[i].Path {
			t.Errorf("manifest not lexicographically sorted: %q >= %q at index %d",
				entries[i-1].Path, entries[i].Path, i)
		}
	}
}

func TestNewBundle_FreezesTypeModeDigest(t *testing.T) {
	b := mustBundle(t, "opencode@1.17.18", sampleSources())
	for _, e := range b.Manifest().Entries {
		if !e.Type.IsValid() {
			t.Errorf("entry %q has invalid type %q", e.Path, e.Type)
		}
		if e.Mode.Perm() != 0o644 {
			t.Errorf("entry %q froze mode %o, want 0644", e.Path, e.Mode.Perm())
		}
		if !digestRE.MatchString(e.Digest) {
			t.Errorf("entry %q digest %q is not sha256:<64 lowercase hex>", e.Path, e.Digest)
		}
	}
}

func TestNewBundle_Deterministic_SupplyOrderIndependent(t *testing.T) {
	forward := sampleSources()
	reversed := make([]artifact.Source, len(forward))
	for i := range forward {
		reversed[len(forward)-1-i] = forward[i]
	}
	a := mustBundle(t, "opencode@1.17.18", forward)
	b := mustBundle(t, "opencode@1.17.18", reversed)
	if a.Manifest().Digest() != b.Manifest().Digest() {
		t.Fatalf("manifest digest differs by supply order: %q vs %q",
			a.Manifest().Digest(), b.Manifest().Digest())
	}
	pa, pb := a.Paths(), b.Paths()
	if len(pa) != len(pb) {
		t.Fatalf("path count differs: %d vs %d", len(pa), len(pb))
	}
	for i := range pa {
		if pa[i] != pb[i] {
			t.Errorf("path %d differs: %q vs %q", i, pa[i], pb[i])
		}
	}
}

func TestBundle_ContentRoundTripAndIsolation(t *testing.T) {
	b := mustBundle(t, "opencode@1.17.18", sampleSources())
	got, ok := b.Content("skills/worker/SKILL.md")
	if !ok {
		t.Fatal("expected content for skills/worker/SKILL.md")
	}
	if string(got) != "worker skill\n" {
		t.Errorf("content = %q, want %q", got, "worker skill\n")
	}
	// Mutating the returned slice must not alter the bundle's frozen bytes.
	got[0] = 'X'
	again, _ := b.Content("skills/worker/SKILL.md")
	if string(again) != "worker skill\n" {
		t.Errorf("bundle content mutated through returned slice: %q", again)
	}
	if _, ok := b.Content("does/not/exist"); ok {
		t.Error("expected missing content to report ok=false")
	}
}

func TestNewBundle_RejectsBadInput(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		sources []artifact.Source
	}{
		{"empty id", "", sampleSources()},
		{"absolute path", "id", []artifact.Source{{Path: "/etc/passwd", Type: artifact.EntryTypeSkill, Mode: 0o644}}},
		{"traversal path", "id", []artifact.Source{{Path: "../escape", Type: artifact.EntryTypeSkill, Mode: 0o644}}},
		{"non-canonical path", "id", []artifact.Source{{Path: "a//b", Type: artifact.EntryTypeSkill, Mode: 0o644}}},
		{"invalid type", "id", []artifact.Source{{Path: "a", Type: artifact.EntryType("bogus"), Mode: 0o644}}},
		{"duplicate path", "id", []artifact.Source{
			{Path: "a", Type: artifact.EntryTypeSkill, Mode: 0o644},
			{Path: "a", Type: artifact.EntryTypeAgent, Mode: 0o644},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := artifact.NewBundle(tc.id, tc.sources); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestFromFS_EmbedBacked(t *testing.T) {
	fsys := fstest.MapFS{
		"skills/worker/SKILL.md":         {Data: []byte("worker\n")},
		"agent/reviewer.md":              {Data: []byte("reviewer\n")},
		"pasture-hooks/pasture-hooks.ts": {Data: []byte("export default () => ({})\n")},
	}
	b, err := artifact.FromFS("opencode@1.17.18", fsys, func(p string) (artifact.EntryType, fs.FileMode, error) {
		return artifact.EntryTypeSkill, 0o644, nil
	})
	if err != nil {
		t.Fatalf("FromFS: %v", err)
	}
	if len(b.Paths()) != 3 {
		t.Fatalf("expected 3 entries from FS, got %d", len(b.Paths()))
	}
}
