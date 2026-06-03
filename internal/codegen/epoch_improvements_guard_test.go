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

// g2ScopedFiles returns the generated surface swept by G2: the root schema.xml,
// every agents/*.md, and (SLICE-5 widening) every skills/*/SKILL.md.
//
// SLICE-1 authored this guard against the schema.xml + agents/*.md it owned and
// documented the widening point (A3: "G2 widened [SLICE-1 guard; SLICE-5 final
// sweep]"). SLICE-5 widens the scope to skills/*/SKILL.md now that SLICE-3
// (bodies) and SLICE-4 (protocol docs) have dropped their handoff-path prose —
// the generated SKILL.md outputs must be verified handoff-path-free too.
//
// The skills/*/SKILL.md glob deliberately matches ONLY the generated SKILL.md
// in each skill directory (including skills/protocol/SKILL.md, which is clean).
// It does NOT match the other hand-authored skills/protocol/*.md docs
// (CLAUDE.md, CONSTRAINTS.md, AGENTS.md, PROCESS.md, HANDOFF_TEMPLATE.md), which
// legitimately mention ".git/.aura/handoff" in EXPLANATORY "R8/A3 retired the
// ... pattern" migration text — those are intentional and out of scope for G2.
func g2ScopedFiles(t *testing.T, root string) []string {
	t.Helper()

	agentGlob := filepath.Join(root, "agents", "*.md")
	agentFiles, err := filepath.Glob(agentGlob)
	require.NoError(t, err, "G2: globbing %q failed", agentGlob)
	require.NotEmpty(t, agentFiles,
		"G2: no agents/*.md found under %q — the guard would vacuously pass", root)

	skillGlob := filepath.Join(root, "skills", "*", "SKILL.md")
	skillFiles, err := filepath.Glob(skillGlob)
	require.NoError(t, err, "G2: globbing %q failed", skillGlob)
	require.NotEmpty(t, skillFiles,
		"G2: no skills/*/SKILL.md found under %q — the SLICE-5 widening would vacuously pass", root)

	files := append([]string{}, agentFiles...)
	files = append(files, skillFiles...)
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

// bodyBehaviorById returns the BehaviorSpec with the given Id from the named
// SkillBodySpecs body, failing the test if either the body or the behavior is
// absent. Used to reach SLICE-3-owned body-local routing behaviors that are not
// shared fragments (e.g. epoch-followup-trigger, sup-followup-deps).
func bodyBehaviorById(t *testing.T, skillKey, behaviorId string) codegen.BehaviorSpec {
	t.Helper()

	body, ok := codegen.SkillBodySpecs[skillKey]
	require.Truef(t, ok, "G4: SkillBodySpecs[%q] body missing", skillKey)
	for _, beh := range body.Behaviors {
		if beh.Id == behaviorId {
			return beh
		}
	}
	t.Fatalf("G4: behavior %q not found in SkillBodySpecs[%q] — SLICE-3 reword target is missing", behaviorId, skillKey)
	return codegen.BehaviorSpec{}
}

// g4FollowupThens returns the resolved positive FOLLOWUP-source routing texts,
// keyed by a human label for failure messages. Each Then clause is a positive
// statement of where the FOLLOWUP epic is sourced from, so it must name DEFER
// and must NOT name any review severity.
//
// SLICE-1 owns the three shared fragments; SLICE-5 widens the set to include the
// SLICE-3-reworded body-local routing behavior epoch-followup-trigger (epoch
// body), whose Then clause is itself a positive DEFER-only source statement.
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

	// SLICE-5 widening: epoch body's FOLLOWUP trigger (SLICE-3 reworded DEFER-only).
	epochTrigger := bodyBehaviorById(t, "epoch", "epoch-followup-trigger")
	out["epoch-followup-trigger"] = epochTrigger.Then

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

// TestG4_SupervisorBodyNeverRoutesSeverityToFollowup asserts the supervisor-body
// severity-routing behavior (sup-followup-deps, SLICE-3-owned) keeps review
// severities OUT of the FOLLOWUP epic. Unlike the positive DEFER-source texts in
// g4FollowupThens, this behavior discusses IMPORTANT/MINOR explicitly — but only
// to route them to the review round (Then) and to forbid routing them to the
// FOLLOWUP epic (ShouldNot). The strict "no severity token" check therefore does
// NOT apply; instead this asserts the spec invariant directly (R7/A1):
//
//   - the POSITIVE instruction (Then) routes severities to the review round and
//     must NOT route any severity INTO a FOLLOWUP epic, so it must not mention
//     "follow-up"/"followup"; and
//   - the behavior must affirm that the FOLLOWUP epic is DEFER-fed and that
//     severities are explicitly forbidden from it (ShouldNot names both DEFER
//     and FOLLOWUP in a forbidding clause).
//
// This is the supervisor-body counterpart to epoch-followup-trigger (which is a
// positive DEFER-source text and is checked by TestG4_FollowupRoutesDeferOnly).
func TestG4_SupervisorBodyNeverRoutesSeverityToFollowup(t *testing.T) {
	beh := bodyBehaviorById(t, "supervisor", "sup-followup-deps")

	thenUpper := strings.ToUpper(beh.Then)
	// The positive routing instruction sends severities to the review round,
	// never to a follow-up epic — so it must not mention follow-up at all.
	assert.NotContains(t, thenUpper, "FOLLOW",
		"G4: sup-followup-deps.Then must route IMPORTANT/MINOR severity groups to their REVIEW ROUND only, "+
			"never into a FOLLOWUP epic (R7/A1: all review severities reach 0 before wave close) — got %q", beh.Then)

	shouldNotUpper := strings.ToUpper(beh.ShouldNot)
	assert.Contains(t, shouldNotUpper, "DEFER",
		"G4: sup-followup-deps.ShouldNot must affirm the FOLLOWUP epic is fed solely by user-DEFER'd UAT items (R7/A1) — got %q", beh.ShouldNot)
	assert.Contains(t, shouldNotUpper, "FOLLOWUP",
		"G4: sup-followup-deps.ShouldNot must explicitly forbid routing IMPORTANT/MINOR severity groups to the FOLLOWUP epic (R7/A1) — got %q", beh.ShouldNot)
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
