// Package codegen_test — namespace renamespace guard.
//
// This file contains the SLICE-2 guard test that enforces the pasture
// renamespace: after the aura:->pasture: sweep + marker rebrand + structural
// rebrand, NO `aura:` colon-anchored namespace token may remain in the on-disk
// generated/skill surface, and the pasture-branded marker pair must be intact
// in every marker-bounded SKILL.md.
//
// The guard is colon-anchored: it matches the literal token prefix `aura:`
// (skill commands like `aura:worker`, labels like `aura:p9-impl:s9-slice`,
// severity tags like `aura:severity:blocker`). It deliberately does NOT match
// the deferred, allowlisted tool/path references `aura-swarm`, `aura-parallel`,
// or `.git/.aura/...` — those use a hyphen or a dot after `aura`, not a colon,
// so the colon anchor already excludes them. The explicit allowlist below is
// belt-and-suspenders documentation of that intent (D6: those renames are
// deferred until the pasture tooling reaches parity).
package codegen_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// auraNamespaceToken is the colon-anchored namespace prefix the guard forbids
// in generated output. Matching this literal substring excludes the deferred
// hyphenated tool refs (aura-swarm, aura-parallel) and the dotted handoff path
// (.git/.aura) by construction.
const auraNamespaceToken = "aura:"

// allowlistedAuraRefs are the deferred, non-colon tool/path references that are
// intentionally NOT renamed in this slice (D6). They are listed here for
// documentation and as a defensive filter: a line is exempt only if every
// `aura:`-looking hit on it is actually part of one of these tokens. In
// practice the colon anchor already excludes them (none contains "aura:"), so
// this list is a guard against future drift, not a current escape hatch.
var allowlistedAuraRefs = []string{
	"aura-swarm",
	"aura-parallel",
	".git/.aura",
}

// strayRef is a single forbidden `aura:` occurrence located by the guard.
type strayRef struct {
	File string // path relative to repo root, e.g. "skills/worker/SKILL.md"
	Line int    // 1-based line number
	Text string // trimmed line text, for the failure report
}

// guardScopedFiles returns the on-disk files the namespace guard scans:
//   - every skills/*/SKILL.md
//   - every agents/*.md
//   - the ROOT schema.xml
//
// Paths are absolute; the returned relFor closure maps them back to repo-root
// relative paths for readable failure output.
func guardScopedFiles(t *testing.T, root string) (files []string, relFor func(string) string) {
	t.Helper()

	skillGlob := filepath.Join(root, "skills", "*", "SKILL.md")
	skillFiles, err := filepath.Glob(skillGlob)
	require.NoError(t, err, "namespace guard: globbing %q failed", skillGlob)

	agentGlob := filepath.Join(root, "agents", "*.md")
	agentFiles, err := filepath.Glob(agentGlob)
	require.NoError(t, err, "namespace guard: globbing %q failed", agentGlob)

	files = append(files, skillFiles...)
	files = append(files, agentFiles...)
	files = append(files, filepath.Join(root, "schema.xml"))

	require.NotEmpty(t, skillFiles,
		"namespace guard: no skills/*/SKILL.md found under %q — "+
			"the guard would vacuously pass; ensure the skills tree exists", root)
	require.NotEmpty(t, agentFiles,
		"namespace guard: no agents/*.md found under %q — "+
			"the guard would vacuously pass; ensure the agents tree exists", root)

	rootClean := filepath.Clean(root)
	relFor = func(abs string) string {
		if rel, err := filepath.Rel(rootClean, abs); err == nil {
			return rel
		}
		return abs
	}
	return files, relFor
}

// lineHasOnlyAllowlistedAura reports whether every `aura`-rooted hyphen/dot
// reference on the line is allowlisted. It is only consulted when the line
// contains the forbidden colon token, as a defensive check; a line carrying a
// genuine `aura:` namespace token is never exempted.
func lineHasOnlyAllowlistedAura(line string) bool {
	// A genuine namespace token is never allowlisted.
	if strings.Contains(line, auraNamespaceToken) {
		return false
	}
	for _, ok := range allowlistedAuraRefs {
		if strings.Contains(line, ok) {
			return true
		}
	}
	return false
}

// scanStrayAuraRefs reads a single file and returns every line carrying a
// forbidden `aura:` colon token (allowlisted refs excluded).
func scanStrayAuraRefs(t *testing.T, path, relPath string) []strayRef {
	t.Helper()

	f, err := os.Open(path)
	require.NoError(t, err,
		"namespace guard: cannot open %q for scanning — "+
			"ensure the file exists and is readable: %v", relPath, err)
	defer f.Close()

	var strays []strayRef
	scanner := bufio.NewScanner(f)
	// SKILL.md/schema.xml lines can be long (embedded ASCII figures, label
	// tables); raise the buffer ceiling so long lines are not truncated.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if !strings.Contains(line, auraNamespaceToken) {
			continue
		}
		if lineHasOnlyAllowlistedAura(line) {
			continue
		}
		strays = append(strays, strayRef{
			File: relPath,
			Line: lineNo,
			Text: strings.TrimSpace(line),
		})
	}
	require.NoError(t, scanner.Err(),
		"namespace guard: error while reading %q: %v", relPath, scanner.Err())
	return strays
}

// TestNamespaceGuard_NoStrayAuraRefs is the SLICE-2 renamespace guard.
//
// It scans the on-disk generated/skill surface (skills/*/SKILL.md, agents/*.md,
// ROOT schema.xml) for any `aura:` colon-anchored namespace token. The expected
// count of stray references is derived dynamically: the guard passes iff the
// scan finds ZERO strays. On failure it prints every offending file:line so the
// fix is a direct sweep, not a hunt.
func TestNamespaceGuard_NoStrayAuraRefs(t *testing.T) {
	root := repoRoot(t)
	files, relFor := guardScopedFiles(t, root)

	var strays []strayRef
	for _, abs := range files {
		strays = append(strays, scanStrayAuraRefs(t, abs, relFor(abs))...)
	}

	// Dynamic count: failure is keyed on the actual number of strays found,
	// not a hardcoded magic number. Zero is the only acceptable value.
	if len(strays) > 0 {
		sort.Slice(strays, func(i, j int) bool {
			if strays[i].File != strays[j].File {
				return strays[i].File < strays[j].File
			}
			return strays[i].Line < strays[j].Line
		})
		var b strings.Builder
		fmt.Fprintf(&b,
			"namespace guard: found %d stray `aura:` namespace reference(s) in the "+
				"generated/skill surface (skills/*/SKILL.md, agents/*.md, schema.xml).\n"+
				"The pasture renamespace requires zero `aura:` colon tokens (allowlist: %s).\n"+
				"Fix: sweep the source that produces each line (specs_data*.go, figure YAML, "+
				"or the hand-authored body) from `aura:` to `pasture:`, then re-run "+
				"`go generate ./internal/codegen/...`.\nStray references:\n",
			len(strays), strings.Join(allowlistedAuraRefs, ", "))
		for _, s := range strays {
			fmt.Fprintf(&b, "  %s:%d: %s\n", s.File, s.Line, s.Text)
		}
		t.Fatal(b.String())
	}
}

// TestNamespaceGuard_PastureMarkerIntact asserts that the pasture-branded
// marker pair is present and well-formed in every marker-bounded SKILL.md, and
// that the OLD `aura schema` marker has been fully retired from the on-disk
// surface.
//
// Marker-bounded files are those that carry a BEGIN marker (the 29 generated
// role + sub-skill SKILL.md). Hand-authored, marker-less skills (protocol,
// install-cli) and the fully-generated agents/*.md are exempt from the
// pair-intact check but are still scanned for the retired old marker.
func TestNamespaceGuard_PastureMarkerIntact(t *testing.T) {
	root := repoRoot(t)
	files, relFor := guardScopedFiles(t, root)

	const oldBegin = "<!-- BEGIN GENERATED FROM aura schema -->"
	const oldEnd = "<!-- END GENERATED FROM aura schema -->"

	// Sanity: the rebranded constants must carry the pasture branding. This
	// pins the marker rebrand (D3) alongside the on-disk assertions below.
	require.Contains(t, codegen.GeneratedBegin, "pasture schema",
		"namespace guard: codegen.GeneratedBegin must be the pasture marker; got %q",
		codegen.GeneratedBegin)
	require.Contains(t, codegen.GeneratedEnd, "pasture schema",
		"namespace guard: codegen.GeneratedEnd must be the pasture marker; got %q",
		codegen.GeneratedEnd)

	markerBoundedCount := 0
	for _, abs := range files {
		rel := relFor(abs)
		content, err := os.ReadFile(abs)
		require.NoError(t, err,
			"namespace guard: cannot read %q for marker check: %v", rel, err)
		text := string(content)

		// The retired aura marker must not survive anywhere in scope.
		assert.NotContains(t, text, oldBegin,
			"namespace guard: %s still contains the retired BEGIN marker %q — "+
				"run the one-time on-disk transition + `go generate` to rebrand it to %q",
			rel, oldBegin, codegen.GeneratedBegin)
		assert.NotContains(t, text, oldEnd,
			"namespace guard: %s still contains the retired END marker %q — "+
				"run the one-time on-disk transition + `go generate` to rebrand it to %q",
			rel, oldEnd, codegen.GeneratedEnd)

		// Only marker-bounded files are required to have an intact pair.
		if !strings.Contains(text, codegen.GeneratedBegin) {
			continue
		}
		markerBoundedCount++

		lines := strings.SplitAfter(text, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		begin, end, err := codegen.FindMarkerPositions(lines, rel)
		require.NoError(t, err,
			"namespace guard: %s has a BEGIN marker but the pasture marker pair is "+
				"malformed — %v", rel, err)
		assert.Less(t, begin, end,
			"namespace guard: %s BEGIN marker (line %d) must precede END marker (line %d)",
			rel, begin+1, end+1)
	}

	// Dynamic expectation: the count of marker-bounded files is derived from
	// the scan, not hardcoded. There must be at least one (the role +
	// sub-skill SKILL.md), otherwise the rebrand silently dropped all markers.
	assert.Positive(t, markerBoundedCount,
		"namespace guard: no marker-bounded SKILL.md found — the pasture marker "+
			"appears to have been dropped from every generated skill; expected the "+
			"role and sub-skill SKILL.md to carry %q", codegen.GeneratedBegin)
}
