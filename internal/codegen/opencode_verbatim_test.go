package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// emitOpenCodeVerbatim runs the FULL OpenCode harness against the real module
// tree with Write:false (b2 — never mutate the repo) and returns every emitted
// file that lands under .opencode/skill/<dir>/, keyed by its path RELATIVE to
// .opencode/skill (e.g. "protocol/PROCESS.md", "protocol/figures/layer-cake.yaml").
// Using the full harness (not copyVerbatimSkill directly) proves OpenCodeTarget
// is actually WIRED to the verbatim dirs — a stub openCodeVerbatimDirs would
// make this map empty and fail every assertion below.
func emitOpenCodeVerbatim(t *testing.T) (root string, byRel map[string]GeneratedFile) {
	t.Helper()

	root = testModuleRoot(t)
	figuresDir := filepath.Join(root, "skills", "protocol", "figures")

	files, err := EmitHarness(root, OpenCodeTarget, figuresDir, GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("EmitHarness(opencode): %v", err)
	}

	skillRoot := filepath.Join(root, ".opencode", "skill")
	byRel = make(map[string]GeneratedFile)
	for _, f := range files {
		rel, err := filepath.Rel(skillRoot, f.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue // not under .opencode/skill (e.g. agents, manifest)
		}
		byRel[filepath.ToSlash(rel)] = f
	}
	return root, byRel
}

// assertVerbatim asserts the emitted file at relPath (relative to .opencode/skill)
// exists and is byte-identical to its source under skills/<relPath>.
func assertVerbatim(t *testing.T, root string, byRel map[string]GeneratedFile, relPath string) {
	t.Helper()
	got, ok := byRel[relPath]
	if !ok {
		t.Errorf("verbatim emit MISSING .opencode/skill/%s — recursive copy must reproduce the whole source skill dir, not just SKILL.md", relPath)
		return
	}
	srcPath := filepath.Join(root, "skills", filepath.FromSlash(relPath))
	want, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read verbatim source %q: %v", srcPath, err)
	}
	if got.Content != string(want) {
		t.Errorf(".opencode/skill/%s is NOT byte-verbatim with source %s — verbatim dirs must be copied raw (no template rendering)", relPath, srcPath)
	}
}

// TestOpenCodeVerbatimCopiesProtocolRecursively asserts the protocol skill dir
// is copied RECURSIVELY: SKILL.md + every sibling .md doc the role skills link
// to + the figures/ subtree, all byte-verbatim. A SKILL.md-only copy MUST FAIL
// this test (the linkedDocs assertions would error on the missing siblings),
// because the generated per-role OpenCode skills link to ../protocol/<doc> and a
// SKILL.md-only copy would ship dangling links.
func TestOpenCodeVerbatimCopiesProtocolRecursively(t *testing.T) {
	t.Parallel()

	root, byRel := emitOpenCodeVerbatim(t)

	// (a) SKILL.md present + verbatim.
	assertVerbatim(t, root, byRel, "protocol/SKILL.md")

	// (b) The sibling docs SKILL.md / the generated role skills link to. The
	// canonical doc set (PROCESS/AGENTS/CONSTRAINTS/CLAUDE/SKILLS/README) plus
	// the HANDOFF_*/MR_* templates that role skills reference via ../protocol/.
	// Asserting the LINKED SET (not just one) is what makes a SKILL.md-only copy
	// fail this test.
	linkedDocs := []string{
		"protocol/PROCESS.md",
		"protocol/AGENTS.md",
		"protocol/CONSTRAINTS.md",
		"protocol/CLAUDE.md",
		"protocol/SKILLS.md",
		"protocol/README.md",
		"protocol/HANDOFF_TEMPLATE.md",
		"protocol/MR_TEMPLATE.md",
	}
	for _, doc := range linkedDocs {
		assertVerbatim(t, root, byRel, doc)
	}

	// (c) The figures/ subdir must be present (proves the copy recurses into
	// subdirectories, not just the top level).
	var sawFigure bool
	for rel := range byRel {
		if strings.HasPrefix(rel, "protocol/figures/") {
			sawFigure = true
			assertVerbatim(t, root, byRel, rel)
		}
	}
	if !sawFigure {
		t.Errorf("verbatim emit copied NO files under protocol/figures/ — recursive copy must include the figures/ subtree")
	}
}

// TestOpenCodeVerbatimResolvesGeneratedSkillLinks is the integration assertion:
// the generated per-role OpenCode skills link to ../protocol/<doc>; emitting the
// verbatim protocol/ dir is what makes those links resolve. For every distinct
// ../protocol/<doc> link the generated .opencode/skill/**/SKILL.md files carry,
// assert the corresponding verbatim file was emitted.
func TestOpenCodeVerbatimResolvesGeneratedSkillLinks(t *testing.T) {
	t.Parallel()

	_, byRel := emitOpenCodeVerbatim(t)

	const linkPrefix = "../protocol/"
	links := make(map[string]struct{})
	for rel, f := range byRel {
		if filepath.Base(rel) != "SKILL.md" || strings.HasPrefix(rel, "protocol/") {
			continue // only inspect generated (non-protocol) role/command skills
		}
		for _, tok := range tokenizeLinks(f.Content) {
			if strings.HasPrefix(tok, linkPrefix) {
				// Drop any "#anchor" fragment — links resolve to the FILE.
				tok, _, _ = strings.Cut(tok, "#")
				links[tok] = struct{}{}
			}
		}
	}
	if len(links) == 0 {
		t.Fatalf("no ../protocol/ links found in generated OpenCode skills — expected at least PROCESS.md/CONSTRAINTS.md references")
	}
	for link := range links {
		target := "protocol/" + strings.TrimPrefix(link, linkPrefix)
		if _, ok := byRel[target]; !ok {
			t.Errorf("generated OpenCode skill links to %q but verbatim emit produced no %q — the link would dangle", link, target)
		}
	}
}

// tokenizeLinks extracts whitespace/paren/bracket-delimited tokens that contain
// a "../protocol/" path so the link-resolution test can scan markdown bodies
// without a full markdown parser.
func tokenizeLinks(content string) []string {
	fields := strings.FieldsFunc(content, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', '(', ')', '[', ']', '<', '>', '"', '\'', '`', ',':
			return true
		default:
			return false
		}
	})
	return fields
}
