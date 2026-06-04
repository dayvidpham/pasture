// Package codegen_test — namespace renamespace guard.
//
// This file contains the SLICE-2 guard tests that enforce the pasture
// renamespace: after the aura:->pasture: sweep + marker rebrand + structural
// rebrand, NO `aura:` colon-anchored namespace token may remain in the on-disk
// generated/skill surface or in the protocol documentation surface, and the
// pasture-branded marker pair must be intact in every marker-bounded SKILL.md.
//
// Scope 1 (TestNamespaceGuard_NoStrayAuraRefs):
//
//	skills/*/SKILL.md + agents/*.md + ROOT schema.xml
//
// Scope 2 (TestNamespaceGuard_ProtocolDocs_NoStrayAuraRefs):
//
//	skills/protocol/*.md
//
// # Colon anchor
//
// Both guards match the literal token prefix "aura:" (skill commands like
// `aura:worker`, labels like `aura:p9-impl:s9-slice`, severity tags). This
// excludes the deferred tool/path references (aura-swarm, aura-parallel,
// .git/.aura) by construction — those use a hyphen or a dot after "aura".
//
// # Allowlist
//
// The explicit allowlists document lines that are intentionally preserved as
// "aura" references for cross-project or historical reasons (not pasture
// identity). Current allowlist for protocol docs:
//
//   - MIGRATION_v1_to_v2.md:3 — "migrate existing Aura protocol usage from v1" —
//     the word "Aura" (no colon) describes what is being migrated FROM; no `aura:`
//     token, so the colon-anchored guard does not flag it.
//   - user-request-revamp-v2.md:39-43 — task titles from the unified-schema-*
//     beads project ("Aura ingest CLI command"); these reference a SEPARATE project
//     with pre-existing task IDs. No `aura:` colon token; not flagged.
//   - HANDOFF_EXAMPLE-*.md — "Aura web dashboard", `aura web` command from an
//     example handoff of a separate project. No `aura:` colon token; not flagged.
//   - CLAUDE.md references to `aura-swarm`, `aura-parallel` (deferred tools, D6).
//
// (The former `.git/.aura/handoff/...` storage-path allowlist entry was removed:
// R8/A3 retired that filesystem pattern entirely — handoffs are authored in the
// Beads task body — so no such path should remain in any generated output. The
// G2 guard (TestG2_NoHandoffStoragePaths) enforces its absence.)
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
// intentionally NOT renamed (D6). They are listed here for documentation and as
// a defensive filter. In practice the colon anchor already excludes them (none
// contains "aura:"), so this list is a guard against future drift.
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

// guardScopedFiles returns the on-disk files the primary namespace guard scans:
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

	return files, relForRoot(root)
}

// guardProtocolDocFiles returns the on-disk files for the protocol docs guard:
// every skills/protocol/*.md.
//
// This scope was added in cycle-1 of the SLICE-2 fix loop to enforce the
// full-identity renamespace across the hand-authored protocol documentation.
func guardProtocolDocFiles(t *testing.T, root string) (files []string, relFor func(string) string) {
	t.Helper()

	docGlob := filepath.Join(root, "skills", "protocol", "*.md")
	docFiles, err := filepath.Glob(docGlob)
	require.NoError(t, err, "namespace guard: globbing %q failed", docGlob)

	require.NotEmpty(t, docFiles,
		"namespace guard: no skills/protocol/*.md found under %q — "+
			"the guard would vacuously pass; ensure the protocol docs tree exists", root)

	return docFiles, relForRoot(root)
}

// relForRoot returns a closure that maps an absolute path to a path relative
// to root (for readable error messages).
func relForRoot(root string) func(string) string {
	rootClean := filepath.Clean(root)
	return func(abs string) string {
		if rel, err := filepath.Rel(rootClean, abs); err == nil {
			return rel
		}
		return abs
	}
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
	// SKILL.md/schema.xml/protocol-doc lines can be long (embedded ASCII
	// figures, label tables); raise the buffer ceiling so long lines are not
	// truncated.
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

// runGuardScan is the shared scan loop for both guard tests: it collects
// stray refs across all files in scope and t.Fatal()s with a full file:line
// report if any are found.
func runGuardScan(t *testing.T, files []string, relFor func(string) string, scope string) {
	t.Helper()

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
			"namespace guard: found %d stray `aura:` namespace reference(s) in %s.\n"+
				"The pasture renamespace requires zero `aura:` colon tokens (allowlist: %s).\n"+
				"Fix: sweep the source of each line from `aura:` to `pasture:` — for\n"+
				"generated-output files run `go generate ./internal/codegen/...` afterward;\n"+
				"for hand-authored docs edit directly. Allowlisted: %v.\n"+
				"Stray references:\n",
			len(strays), scope, strings.Join(allowlistedAuraRefs, ", "), allowlistedAuraRefs)
		for _, s := range strays {
			fmt.Fprintf(&b, "  %s:%d: %s\n", s.File, s.Line, s.Text)
		}
		t.Fatal(b.String())
	}
}

// TestNamespaceGuard_NoStrayAuraRefs is the primary SLICE-2 renamespace guard.
//
// It scans the on-disk generated/skill surface (skills/*/SKILL.md, agents/*.md,
// ROOT schema.xml) for any `aura:` colon-anchored namespace token. The expected
// count of stray references is derived dynamically: the guard passes iff the
// scan finds ZERO strays. On failure it prints every offending file:line so the
// fix is a direct sweep, not a hunt.
func TestNamespaceGuard_NoStrayAuraRefs(t *testing.T) {
	root := repoRoot(t)
	files, relFor := guardScopedFiles(t, root)
	runGuardScan(t, files, relFor, "skills/*/SKILL.md, agents/*.md, schema.xml")
}

// TestNamespaceGuard_ProtocolDocs_NoStrayAuraRefs extends the primary guard to
// the hand-authored protocol documentation (skills/protocol/*.md).
//
// These docs describe the pasture protocol and were swept in SLICE-2 cycle-1.
// The allowlist is the same as the primary guard (aura-swarm, aura-parallel,
// .git/.aura — all non-colon refs that the colon anchor already excludes).
//
// Preserved cross-project references in these docs (NOT flagged — no aura: colon):
//   - MIGRATION_v1_to_v2.md: "migrate existing Aura protocol usage from v1"
//     (word "Aura" with no colon; describes what is being migrated away from)
//   - user-request-revamp-v2.md: "Aura ingest CLI command" task titles from a
//     separate beads project (unified-schema-*; no aura: colon)
//   - HANDOFF_EXAMPLE-*.md: "Aura web dashboard" / "aura web" command from an
//     example handoff of a separate project (no aura: colon)
func TestNamespaceGuard_ProtocolDocs_NoStrayAuraRefs(t *testing.T) {
	root := repoRoot(t)
	files, relFor := guardProtocolDocFiles(t, root)
	runGuardScan(t, files, relFor, "skills/protocol/*.md")
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
