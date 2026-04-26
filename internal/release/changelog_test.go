package release_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/release"
)

// ─── ParseConventionalCommit ─────────────────────────────────────────────────

func TestParseConventionalCommit(t *testing.T) {
	tests := []struct {
		input     string
		wantErr   bool
		wantType  string
		wantScope string
		wantDesc  string
	}{
		{"feat: add login", false, "feat", "", "add login"},
		{"fix(auth): resolve token expiry", false, "fix", "auth", "resolve token expiry"},
		{"chore(deps): bump go version", false, "chore", "deps", "bump go version"},
		{"refactor: simplify handler", false, "refactor", "", "simplify handler"},
		{"not a conventional commit", true, "", "", ""},
		{"", true, "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			cc, err := release.ParseConventionalCommit(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for %q, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cc.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", cc.Type, tc.wantType)
			}
			if cc.Scope != tc.wantScope {
				t.Errorf("Scope = %q, want %q", cc.Scope, tc.wantScope)
			}
			if cc.Description != tc.wantDesc {
				t.Errorf("Description = %q, want %q", cc.Description, tc.wantDesc)
			}
			if cc.Raw != tc.input {
				t.Errorf("Raw = %q, want %q", cc.Raw, tc.input)
			}
		})
	}
}

// ─── GenerateChangelog ───────────────────────────────────────────────────────

func TestGenerateChangelog_Sections(t *testing.T) {
	commits := []release.ConventionalCommit{
		{Type: "feat", Description: "add thing", Raw: "feat: add thing"},
		{Type: "fix", Description: "resolve bug", Raw: "fix: resolve bug"},
		{Type: "refactor", Description: "cleanup", Raw: "refactor: cleanup"},
	}
	version := release.SemVer{Major: 1, Minor: 0, Patch: 0}
	result := release.GenerateChangelog(commits, version)

	if !strings.Contains(result, "## [1.0.0]") {
		t.Errorf("missing version header in changelog:\n%s", result)
	}
	if !strings.Contains(result, "### Added") {
		t.Error("missing Added section")
	}
	if !strings.Contains(result, "### Fixed") {
		t.Error("missing Fixed section")
	}
	if !strings.Contains(result, "### Changed") {
		t.Error("missing Changed section")
	}
	if !strings.Contains(result, "- feat: add thing") {
		t.Error("missing feat entry")
	}
	if !strings.Contains(result, "- fix: resolve bug") {
		t.Error("missing fix entry")
	}
}

func TestGenerateChangelog_EmptyCommits(t *testing.T) {
	result := release.GenerateChangelog(nil, release.SemVer{Major: 0, Minor: 1, Patch: 0})
	if !strings.Contains(result, "## [0.1.0]") {
		t.Errorf("missing version header:\n%s", result)
	}
}

func TestGenerateChangelog_SectionOrder(t *testing.T) {
	commits := []release.ConventionalCommit{
		{Type: "fix", Raw: "fix: b"},
		{Type: "feat", Raw: "feat: a"},
	}
	result := release.GenerateChangelog(commits, release.SemVer{Major: 1, Minor: 0, Patch: 0})

	addedIdx := strings.Index(result, "### Added")
	fixedIdx := strings.Index(result, "### Fixed")
	if addedIdx == -1 || fixedIdx == -1 {
		t.Fatalf("missing sections:\n%s", result)
	}
	// Added should come before Fixed per sectionOrder.
	if addedIdx > fixedIdx {
		t.Errorf("expected Added before Fixed in changelog")
	}
}
