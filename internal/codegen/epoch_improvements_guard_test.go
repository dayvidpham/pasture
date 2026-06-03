// Package codegen_test — epoch-improvements guards (G2–G5).
//
// These guards enforce the invariants introduced by the pasture epoch-protocol
// improvements epoch (RATIFIED PROPOSAL-5, aura-plugins-v1xg8). They are
// authored by SLICE-1 (the interface-first FOUNDATION barrier). Two of them
// (G2, G4) have a documented SLICE-5 widening point: the body-prose path
// removals (SLICE-3) and protocol-doc removals (SLICE-4) land after this
// barrier, at which point SLICE-5 extends the file/target scope below.
//
//   - G2 (TestG2_NoHandoffStoragePaths): no `.git/.aura/handoff` filesystem path
//     remains in the SLICE-1-owned generated surface (schema.xml + agents/*.md).
//     R8/A3 retired the StoragePattern entirely — there is NO allowlist. SLICE-5
//     widens the scope to skills/*/SKILL.md once SLICE-3 (bodies) and SLICE-4
//     (protocol docs) drop their handoff-path prose.
//   - G3 (TestG3_SliceLeafTasksFlexibleCount): the C-slice-leaf-tasks constraint
//     does not mandate a fixed L1/L2/L3 leaf triple (R9 — any number of leaves).
//   - G4 (TestG4_FollowupRoutesDeferOnly): the FOLLOWUP-routing surface owned by
//     SLICE-1 (FragSupDeferredFollowup, FragSupFollowupEpicTiming,
//     C-followup-timing) routes the FOLLOWUP epic from user-DEFER'd UAT items
//     ONLY and names NO review severity (BLOCKER/IMPORTANT/MINOR) as a
//     FOLLOWUP source. SLICE-5 widens the target set to epoch-followup-trigger
//     and the supervisor-body routing sites once SLICE-3 rewords them.
//   - G5 (TestG5_InlineFragmentTokensResolve): every inline `[frag--<kebab>]`
//     token in any generated SKILL.md resolves to a live SharedFragmentSpecs
//     kebab, and the retired token `frag--sup-important-minor-followup` appears
//     in ZERO generated outputs (closes the inline-reference gap that structural
//     fragRef/behaviorRef parity does not cover).
package codegen_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// handoffStoragePathToken is the retired filesystem handoff-storage path prefix
// (R8/A3). Zero occurrences may remain — there is NO allowlist.
const handoffStoragePathToken = ".git/.aura/handoff"

// retiredFragmentToken is the kebab of the renamed fragment
// (FragSupImportantMinorFollowup -> FragSupDeferredFollowup, R7/A1). It must
// appear in ZERO generated outputs.
const retiredFragmentToken = "frag--sup-important-minor-followup"

// inlineFragTokenRe matches an inline fragment cross-reference embedded in prose
// content, e.g. "[frag--sup-blocker-dual-parent]". Capture group 1 is the kebab.
var inlineFragTokenRe = regexp.MustCompile(`\[(frag--[a-z0-9-]+)\]`)

// ─── G2: handoff storage path absence ────────────────────────────────────────

// g2ScopedFiles returns the SLICE-1-owned generated surface swept by G2:
// the root schema.xml and every agents/*.md. SLICE-5 widens this to include
// skills/*/SKILL.md once SLICE-3 (bodies) and SLICE-4 (protocol docs) remove
// their handoff-path prose (A3: "G2 widened [SLICE-1 guard; SLICE-5 final sweep]").
func g2ScopedFiles(t *testing.T, root string) []string {
	t.Helper()

	agentGlob := filepath.Join(root, "agents", "*.md")
	agentFiles, err := filepath.Glob(agentGlob)
	require.NoError(t, err, "G2: globbing %q failed", agentGlob)
	require.NotEmpty(t, agentFiles,
		"G2: no agents/*.md found under %q — the guard would vacuously pass", root)

	files := append([]string{}, agentFiles...)
	files = append(files, filepath.Join(root, "schema.xml"))
	return files
}

// TestG2_NoHandoffStoragePaths asserts that the retired `.git/.aura/handoff`
// filesystem path appears NOWHERE in the SLICE-1-owned generated surface
// (schema.xml + agents/*.md). No allowlist (R8/A3).
func TestG2_NoHandoffStoragePaths(t *testing.T) {
	root := repoRoot(t)

	for _, file := range g2ScopedFiles(t, root) {
		data, err := os.ReadFile(file)
		require.NoError(t, err, "G2: reading %q failed", file)

		rel, _ := filepath.Rel(root, file)
		assert.NotContains(t, string(data), handoffStoragePathToken,
			"G2: generated file %q still contains the retired handoff storage path %q — "+
				"R8/A3 retired the .git/.aura/handoff filesystem pattern; every handoff is "+
				"authored in its Beads task body. Fix: replace the path reference with the "+
				"task-body convention (sentinel %q in HandoffsSection.StoragePattern).",
			rel, handoffStoragePathToken, "beads-task-body")
	}
}

// ─── G3: flexible slice-leaf count ───────────────────────────────────────────

// TestG3_SliceLeafTasksFlexibleCount asserts that C-slice-leaf-tasks does NOT
// mandate a fixed L1/L2/L3 leaf triple (R9): a slice may have any number of
// leaves, named after the real work units.
func TestG3_SliceLeafTasksFlexibleCount(t *testing.T) {
	spec, ok := codegen.ConstraintSpecs["C-slice-leaf-tasks"]
	require.True(t, ok,
		"G3: C-slice-leaf-tasks not found in ConstraintSpecs — R9 reword target is missing")

	then := strings.ToLower(spec.Then)
	shouldNot := strings.ToLower(spec.ShouldNot)

	assert.Contains(t, then, "any number",
		"G3: C-slice-leaf-tasks.Then must state a slice may have ANY number of leaves (R9) — got %q", spec.Then)
	assert.Contains(t, then, "illustrative",
		"G3: C-slice-leaf-tasks.Then must mark the L1/L2/L3 triple as illustrative-only (R9) — got %q", spec.Then)
	assert.Contains(t, shouldNot, "fixed",
		"G3: C-slice-leaf-tasks.ShouldNot must forbid forcing a fixed leaf triple (R9) — got %q", spec.ShouldNot)
}

// ─── G4: FOLLOWUP routes DEFER-only ──────────────────────────────────────────

// g4FollowupThens returns the resolved FOLLOWUP-routing texts owned by SLICE-1,
// keyed by a human label for failure messages.
func g4FollowupThens(t *testing.T) map[string]string {
	t.Helper()

	out := map[string]string{}

	deferred, ok := codegen.SharedFragmentSpecs[codegen.FragSupDeferredFollowup]
	require.True(t, ok && deferred.Behavior != nil,
		"G4: FragSupDeferredFollowup behavior fragment missing")
	out["FragSupDeferredFollowup"] = deferred.Behavior.Then

	timing, ok := codegen.SharedFragmentSpecs[codegen.FragSupFollowupEpicTiming]
	require.True(t, ok && timing.Behavior != nil,
		"G4: FragSupFollowupEpicTiming behavior fragment missing")
	out["FragSupFollowupEpicTiming"] = timing.Behavior.Then

	timingC, ok := codegen.ConstraintSpecs["C-followup-timing"]
	require.True(t, ok, "G4: C-followup-timing constraint missing")
	out["C-followup-timing"] = timingC.Then

	return out
}

// TestG4_FollowupRoutesDeferOnly asserts that every SLICE-1-owned FOLLOWUP-routing
// text routes the FOLLOWUP epic from user-DEFER'd UAT items ONLY (mentions DEFER)
// and names NO review severity (IMPORTANT/MINOR) as a FOLLOWUP source in its
// Then clause (R7/A1). SLICE-5 widens the target set to epoch-followup-trigger
// and the supervisor-body routing sites after SLICE-3 rewords them.
func TestG4_FollowupRoutesDeferOnly(t *testing.T) {
	for label, then := range g4FollowupThens(t) {
		upper := strings.ToUpper(then)
		assert.Contains(t, upper, "DEFER",
			"G4: %s.Then must route FOLLOWUP from user-DEFER'd UAT items (R7/A1) — got %q", label, then)
		assert.NotContains(t, upper, "IMPORTANT",
			"G4: %s.Then must NOT name IMPORTANT as a FOLLOWUP source (R7/A1: all review severities reach 0; FOLLOWUP is DEFER-fed) — got %q", label, then)
		assert.NotContains(t, upper, "MINOR",
			"G4: %s.Then must NOT name MINOR as a FOLLOWUP source (R7/A1: all review severities reach 0; FOLLOWUP is DEFER-fed) — got %q", label, then)
	}
}

// ─── G5: inline fragment-token resolvability + retired-token absence ──────────

// liveFragmentKebabs returns the set of kebab values currently registered in
// SharedFragmentSpecs (the resolvable inline-reference targets).
func liveFragmentKebabs() map[string]bool {
	live := map[string]bool{}
	for id := range codegen.SharedFragmentSpecs {
		live[string(id)] = true
	}
	return live
}

// TestG5_InlineFragmentTokensResolve sweeps every generated skills/*/SKILL.md and
// asserts (a) the retired token frag--sup-important-minor-followup appears in ZERO
// files, and (b) every inline [frag--<kebab>] reference resolves to a live
// SharedFragmentSpecs kebab. Closes the inline-reference gap that structural
// fragRef/behaviorRef parity (ValidateGlobalIds) does not cover.
func TestG5_InlineFragmentTokensResolve(t *testing.T) {
	root := repoRoot(t)
	live := liveFragmentKebabs()

	skillGlob := filepath.Join(root, "skills", "*", "SKILL.md")
	skillFiles, err := filepath.Glob(skillGlob)
	require.NoError(t, err, "G5: globbing %q failed", skillGlob)
	require.NotEmpty(t, skillFiles,
		"G5: no skills/*/SKILL.md found under %q — the guard would vacuously pass", root)

	for _, file := range skillFiles {
		data, err := os.ReadFile(file)
		require.NoError(t, err, "G5: reading %q failed", file)
		content := string(data)
		rel, _ := filepath.Rel(root, file)

		// (a) retired token must be entirely absent.
		assert.NotContains(t, content, retiredFragmentToken,
			"G5: generated file %q still contains the retired fragment token %q — "+
				"R7/A1 renamed it to frag--sup-deferred-followup. Fix: update the inline "+
				"reference (and regenerate) so no dangling cross-reference remains.",
			rel, retiredFragmentToken)

		// (b) every inline [frag--*] token must resolve to a live fragment.
		for _, m := range inlineFragTokenRe.FindAllStringSubmatch(content, -1) {
			kebab := m[1]
			assert.Truef(t, live[kebab],
				"G5: generated file %q contains inline fragment reference [%s] that does not "+
					"resolve to any fragment in SharedFragmentSpecs — this is a dangling "+
					"user-visible cross-reference. Fix: add the fragment to SharedFragmentSpecs "+
					"(and AllFragmentIds) or correct the inline token.",
				rel, kebab)
		}
	}
}
