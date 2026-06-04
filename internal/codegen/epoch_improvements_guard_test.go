// Package codegen_test — epoch-improvements guards (G2–G7 + M2–M3).
//
// These guards enforce the invariants introduced by the pasture epoch-protocol
// improvements epoch (RATIFIED PROPOSAL-5, aura-plugins-v1xg8). They are
// authored by SLICE-1 (the interface-first FOUNDATION barrier). Two of them
// (G2, G4) have a documented SLICE-5 widening point: the body-prose path
// removals (SLICE-3) and protocol-doc removals (SLICE-4) land after this
// barrier, at which point SLICE-5 extends the file/target scope below.
//
//   - G2 (TestG2_NoHandoffStoragePaths): no `.git/.aura/handoff` filesystem path
//     remains in ANY swept surface — schema.xml + agents/*.md (SLICE-1) +
//     skills/*/SKILL.md (SLICE-5 widening) + skills/protocol/*.md (UAT-2
//     widening). R8/A3 retired the StoragePattern entirely — there is NO
//     allowlist and NO exclusion. The protocol-docs exclusion was dropped at
//     UAT-2 once the last retired-pattern migration callouts were stripped, so
//     zero-tolerance is now durable everywhere.
//   - G3 (TestG3_SliceLeafTasksFlexibleCount): the C-slice-leaf-tasks constraint
//     does not mandate a fixed L1/L2/L3 leaf triple (R9 — any number of leaves).
//   - G4 (TestG4_FollowupRoutesDeferOnly): the FOLLOWUP-routing surface owned by
//     SLICE-1 (FragSupDeferredFollowup, FragSupFollowupEpicTiming,
//     C-followup-timing) routes the FOLLOWUP epic from user-DEFER'd UAT items
//     ONLY and names NO review severity (BLOCKER/IMPORTANT/MINOR) as a
//     FOLLOWUP source. SLICE-5 widens the target set to epoch-followup-trigger
//     and the supervisor-body routing sites once SLICE-3 rewords them.
//   - G4 (TestG4_SupervisorBodyNeverRoutesSeverityToFollowup): the SLICE-5
//     widening of G4 — the supervisor-body + epoch-followup-trigger routing sites
//     route the FOLLOWUP epic from user-DEFER'd UAT items ONLY and name NO review
//     severity as a FOLLOWUP source.
//   - G5 (TestG5_InlineFragmentTokensResolve): every inline `[frag--<kebab>]`
//     token in any generated SKILL.md resolves to a live SharedFragmentSpecs
//     kebab, and the retired token `frag--sup-important-minor-followup` appears
//     in ZERO generated outputs (closes the inline-reference gap that structural
//     fragRef/behaviorRef parity does not cover).
//   - G6 (TestG6_NoStaleCycleCapOrSeverityRoutingProse): no retired pre-R7/A1
//     HARDCODED-regime prose (an unconditional numeric cycle cap, or a review
//     severity routed to a follow-up epic) remains in any generated
//     skills/*/SKILL.md or agents/*.md. v2-2 retune: the cap-* patterns are now
//     budget-exempt — a numeric value stated alongside configurable review-effort
//     budget vocabulary is allowed (a user-chosen budget, not a baked-in cap);
//     the severity-routing half is unchanged.
//   - G7 (TestG7_ReviewLoopReferencesConfigurableBudget): positive complement to
//     G6 — the review-loop prose in the surfaces that own it DOES reference the
//     configurable review-effort budget AND a surface-to-user-on-exhaustion
//     fallback. Must-fails on a hardcoded-unlimited-only mutation (v2-2 V1).
//   - M2 (TestR6ValidationCasesRenders): the R6 FragValidationCases instruction
//     (universal validation cases, v2-2 V3) actually renders into the generated
//     worker-implement and reviewer-review-code SKILL.md (positive render-assertion).
//   - M3 (TestV2DeferralRaisedAtNextGateRenders): the V2 expanded-deferral model
//     (agent-proposed deferrals; ALL deferred items raised to the user at the next
//     user gate) renders into both user-elicit and user-uat SKILL.md (v2-2 V2).
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

// g2ScopedFiles returns the full surface swept by G2: the root schema.xml,
// every agents/*.md (SLICE-1), every skills/*/SKILL.md (SLICE-5 widening), and
// every skills/protocol/*.md (UAT-2 widening).
//
// SLICE-1 authored this guard against the schema.xml + agents/*.md it owned and
// documented the widening point (A3: "G2 widened [SLICE-1 guard; SLICE-5 final
// sweep]"). SLICE-5 widened the scope to skills/*/SKILL.md once SLICE-3 (bodies)
// and SLICE-4 (protocol docs) dropped their handoff-path prose.
//
// UAT-2 dropped the LAST exclusion: the hand-authored skills/protocol/*.md docs
// (CLAUDE.md, CONSTRAINTS.md, AGENTS.md, PROCESS.md, HANDOFF_TEMPLATE.md) were
// previously left out because they carried legitimate retired-pattern migration
// callouts (e.g. "R8/A3 retired the .git/.aura/handoff pattern"). The user chose
// absolute cleanliness (UAT-2 FIX-NOW): the path must appear NOWHERE, so those
// callouts were stripped and the protocol docs are now swept too. There is NO
// allowlist and NO exclusion anywhere — zero-tolerance is durable across the
// entire generated + hand-authored surface.
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

	// UAT-2 widening: the hand-authored protocol docs (skills/protocol/*.md) were
	// previously excluded because they carried legitimate retired-pattern migration
	// callouts. The user chose absolute cleanliness (UAT-2 FIX-NOW): the path must
	// appear NOWHERE, so the exclusion is dropped and these are now swept too.
	protocolGlob := filepath.Join(root, "skills", "protocol", "*.md")
	protocolFiles, err := filepath.Glob(protocolGlob)
	require.NoError(t, err, "G2: globbing %q failed", protocolGlob)
	require.NotEmpty(t, protocolFiles,
		"G2: no skills/protocol/*.md found under %q — the UAT-2 widening would vacuously pass", root)

	files := append([]string{}, agentFiles...)
	files = append(files, skillFiles...)
	files = append(files, protocolFiles...)
	files = append(files, filepath.Join(root, "schema.xml"))
	return files
}

// TestG2_NoHandoffStoragePaths asserts that the retired `.git/.aura/handoff`
// filesystem path appears NOWHERE across the full swept surface — schema.xml +
// agents/*.md + skills/*/SKILL.md + skills/protocol/*.md (see g2ScopedFiles).
// No allowlist and no exclusion (R8/A3; protocol-docs exclusion dropped at UAT-2).
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

// ─── G6: recurrence guard — no stale cap / severity-routing prose ─────────────
//
// B3 (review round 1): no guard swept free-text figure/ASCII/table surfaces, so
// the pre-R7/A1 regime (3-cycle cap; review IMPORTANT/MINOR fed into FOLLOWUP)
// survived in rendered outputs (ride-the-wave figure, supervisor body tables)
// while structural guards G2-G5 stayed green. G6 sweeps every generated
// skills/*/SKILL.md and agents/*.md for that retired prose.
//
// Each forbidden pattern is paired with a documented stale exemplar (the actual
// pre-fix phrasing). TestG6 first asserts every pattern matches its own
// exemplar — a non-vacuity check so a typo that silently disables a pattern is
// itself caught (the same mutation discipline Reviewer B applied to G2-G5).

// staleRegimePattern pairs a forbidden regex with the pre-fix phrasing it must catch.
//
// budgetExempt marks the cap-* patterns whose numeric match is legitimate when
// it appears alongside configurable-budget vocabulary (budgetAllowRe). The
// route-* patterns are never exempt — review severities still never feed
// FOLLOWUP.
type staleRegimePattern struct {
	name         string
	re           *regexp.Regexp
	exemplar     string
	budgetExempt bool
}

// budgetAllowRe recognizes configurable-budget vocabulary (v2-2 V1 regime). A
// cap-* match on a line that also carries this vocabulary is the user-chosen
// review-effort budget, not a hardcoded cap, and is allowed.
var budgetAllowRe = regexp.MustCompile(`(?i)review-effort budget|chosen .{0,30}budget|configurable .{0,20}budget|budget .{0,30}(chosen|exhaust|surface|user)|up to the chosen|surface .{0,40}to the user`)

// staleRegimePatterns enumerates the retired pre-R7/A1 prose forms. Patterns
// require an affirmative routing target ("follow-up epic") or a numeric cycle
// cap, so legitimate negated text ("NOT routed to FOLLOWUP", "no cycle cap")
// and the DEFER'd-items tracking-group wording do NOT match.
//
// v2-2 retune (V1 reconciliation): R7 replaced the hardcoded "unlimited" review
// regime with a CONFIGURABLE review-effort budget ({3/1/0/unlimited/custom}
// requested at Phase 8). G6 must forbid only HARDCODED/stale-regime caps (an
// unconditional "maximum of 3 cycles per slice", "3 cycles exhausted") while
// ALLOWING the configurable-budget vocabulary. The cap-* patterns are therefore
// budgetExempt: a match is permitted when its line also carries budget-context
// vocabulary (budgetAllowRe) — the numeric value is then a user-chosen budget,
// not a baked-in cap. The route-* (severity-routing) half is NOT exempt.
var staleRegimePatterns = []staleRegimePattern{
	// Cycle-cap forms (a digit bound to cycles / per-slice). budgetExempt: allowed
	// when the line carries configurable-budget vocabulary.
	{"cap-max-n", regexp.MustCompile(`(?i)max(imum)?(\s+of)?\s+\d+\s+(review\s+)?(cycles?|per slice)`), "Phase 10: REVIEW + FIX CYCLES (max 3 per slice)", true},
	{"cap-n-cycles", regexp.MustCompile(`(?i)\b\d+\s+(review\s+)?cycles?\s+(per slice|exhausted|total)`), "Repeat steps 4-6 up to 3 cycles total", true},
	{"cap-after-n", regexp.MustCompile(`(?i)after\s+\d+\s+cycles?`), "After 3 cycles per slice: escalate to architect", true},
	{"cap-exhausted", regexp.MustCompile(`(?i)cycles?\s+exhausted`), "3 cycles exhausted, IMPORTANT remain", true},
	// Review-severity-routed-to-FOLLOWUP forms (affirmative routing to a follow-up epic). Never budget-exempt.
	{"route-tracked-epic", regexp.MustCompile(`(?i)track(ed|s)?[^.\n]{0,40}follow-?up epic`), "findings are tracked on the existing follow-up epic", false},
	{"route-goes-to-epic", regexp.MustCompile(`(?i)goes to (the )?follow-?up epic`), "IMPORTANT | No | Goes to follow-up epic", false},
	{"route-epic-if-any", regexp.MustCompile(`(?i)follow-?up epic if any`), "Create FOLLOWUP epic if ANY IMPORTANT/MINOR findings", false},
	{"route-track-in-followup", regexp.MustCompile(`(?i)track in follow-?up`), "3 cycles exhausted, IMPORTANT remain -> Track in FOLLOWUP", false},
}

// g6ScopedFiles returns every generated skills/*/SKILL.md and agents/*.md.
func g6ScopedFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for _, glob := range []string{
		filepath.Join(root, "skills", "*", "SKILL.md"),
		filepath.Join(root, "agents", "*.md"),
	} {
		matched, err := filepath.Glob(glob)
		require.NoError(t, err, "G6: globbing %q failed", glob)
		files = append(files, matched...)
	}
	require.NotEmpty(t, files,
		"G6: no generated skills/*/SKILL.md or agents/*.md found under %q — the guard would vacuously pass", root)
	return files
}

// TestG6_NoStaleCycleCapOrSeverityRoutingProse asserts no retired pre-R7/A1
// regime prose (cycle cap, or review severity routed to a follow-up epic)
// remains in any generated skill/agent output.
func TestG6_NoStaleCycleCapOrSeverityRoutingProse(t *testing.T) {
	root := repoRoot(t)

	// Non-vacuity: every pattern must match its own documented stale exemplar.
	for _, p := range staleRegimePatterns {
		require.Truef(t, p.re.MatchString(p.exemplar),
			"G6: pattern %q does not match its own stale exemplar %q — the regex is broken/vacuous; fix the pattern before relying on the sweep",
			p.name, p.exemplar)
	}

	for _, file := range g6ScopedFiles(t, root) {
		data, err := os.ReadFile(file)
		require.NoError(t, err, "G6: reading %q failed", file)
		rel, _ := filepath.Rel(root, file)

		// Scan line-by-line so the configurable-budget exemption is scoped to the
		// matched line (a hardcoded cap and a budget mention never share a line).
		for _, line := range strings.Split(string(data), "\n") {
			for _, p := range staleRegimePatterns {
				loc := p.re.FindStringIndex(line)
				if loc == nil {
					continue
				}
				// v2-2: a cap-* match is allowed when its line carries
				// configurable-budget vocabulary (a user-chosen budget value, not a
				// baked-in cap). route-* matches are never exempt.
				if p.budgetExempt && budgetAllowRe.MatchString(line) {
					continue
				}
				assert.Failf(t, "G6: stale pre-R7/A1 regime prose found",
					"G6: generated file %q matches retired-regime pattern %q (matched text: %q) on line %q — "+
						"R7/A1 retired the HARDCODED cycle-cap + severity-fed-FOLLOWUP regime. Fix: code review iterates "+
						"review->fix->re-review up to the CONFIGURABLE review-effort budget until a fix-free clean round confirms 0/0/0 "+
						"(on budget exhaustion without clean, surface to the user), and the FOLLOWUP epic is fed ONLY by "+
						"user-DEFER'd UAT items (never review severities). A numeric cap is allowed only as a chosen budget value "+
						"(state it alongside budget vocabulary). Update the source spec/figure (e.g. "+
						"skills/protocol/figures/ride-the-wave.yaml or specs_data_body*.go) and regenerate.",
					rel, p.name, line[loc[0]:loc[1]], line)
			}
		}
	}
}

// ─── G7: positive — review-loop prose references the configurable budget ──────
//
// v2-2 (V1 reconciliation): R7 replaced the hardcoded "unlimited" review regime
// with a CONFIGURABLE review-effort budget that surfaces to the user on
// exhaustion. G7 is the positive complement to the retuned G6: it asserts the
// review-loop prose in the role/command surfaces that own it DOES reference a
// configurable budget AND a surface-to-user fallback. It must-fail if that prose
// is mutated back to a hardcoded-unlimited-only form (the budget/surface needles
// disappear).

// surfaceToUserRe recognizes the surface-on-exhaustion fallback prose.
var surfaceToUserRe = regexp.MustCompile(`(?i)surface[sd]?\s+.{0,60}user`)

// TestG7_ReviewLoopReferencesConfigurableBudget asserts every generated surface
// that describes the Phase-10 review loop references the configurable
// review-effort budget AND the surface-to-user-on-exhaustion fallback.
func TestG7_ReviewLoopReferencesConfigurableBudget(t *testing.T) {
	root := repoRoot(t)

	// Non-vacuity: the allow/needle regexes must match their own canonical phrasing.
	require.True(t, budgetAllowRe.MatchString("iterate up to the chosen review-effort budget"),
		"G7: budgetAllowRe does not match its own canonical phrasing — fix the regex before relying on it")
	require.True(t, surfaceToUserRe.MatchString("on budget exhaustion, surface outstanding findings to the user"),
		"G7: surfaceToUserRe does not match its own canonical phrasing — fix the regex before relying on it")

	// Generated surfaces that own review-loop prose and must reference the budget.
	for _, rel := range []string{
		"skills/supervisor-spawn-worker/SKILL.md",
		"skills/supervisor-plan-tasks/SKILL.md",
		"skills/epoch/SKILL.md",
		"skills/reviewer-review-code/SKILL.md",
		"skills/worker-implement/SKILL.md",
	} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		require.NoErrorf(t, err, "G7: reading %q failed", rel)
		content := string(data)

		assert.Truef(t, budgetAllowRe.MatchString(content),
			"G7: generated %q does not reference the configurable review-effort budget — "+
				"R7/A1 (v2-2) replaced the hardcoded 'unlimited' regime with a configurable budget; "+
				"the review-loop prose must say it iterates up to the chosen review-effort budget. "+
				"Re-add the budget vocabulary in the source body spec and regenerate.", rel)
		assert.Truef(t, surfaceToUserRe.MatchString(content),
			"G7: generated %q does not reference the surface-to-user-on-exhaustion fallback — "+
				"on budget exhaustion without a clean 0/0/0 round, outstanding findings must be surfaced to the user "+
				"(never proceed-dirty, never loop forever). Re-add the surface-to-user prose and regenerate.", rel)
	}
}

// ─── M2: positive render-assertion for the R6 FragValidationCases step ─────

// TestR6ValidationCasesRenders asserts the FragValidationCases (R6)
// instruction actually renders into the generated worker-implement and
// reviewer-review-code SKILL.md (the behaviorRef wiring is otherwise only
// transitively covered).
func TestR6ValidationCasesRenders(t *testing.T) {
	root := repoRoot(t)

	frag, ok := codegen.SharedFragmentSpecs[codegen.FragValidationCases]
	require.Truef(t, ok && frag.Behavior != nil,
		"M2: FragValidationCases behavior fragment missing from SharedFragmentSpecs")

	// Stable substring of the canonical Then that must render verbatim wherever
	// the fragment is behaviorRef'd.
	const needle = "store failing real-data cases as test fixtures"
	require.Containsf(t, frag.Behavior.Then, needle,
		"M2: FragValidationCases.Then no longer contains the assertion needle %q — update the needle to a current stable substring", needle)

	for _, rel := range []string{
		"skills/worker-implement/SKILL.md",
		"skills/reviewer-review-code/SKILL.md",
	} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		require.NoErrorf(t, err, "M2: reading %q failed", rel)
		assert.Containsf(t, string(data), needle,
			"M2: generated %q does not render the FragValidationCases (R6) instruction %q — "+
				"the behaviorRef(FragValidationCases) wiring is missing or the fragment was not embedded; "+
				"re-add the behaviorRef in the body spec and regenerate.", rel, needle)
	}
}

// ─── M3: positive render-assertion for the V2 expanded-deferral model ─────────
//
// v2-2 V2 (B-4): deferrals may be proposed by the architect/supervisor (not only
// flagged by the user), and ALL deferred items must be raised to the user at the
// next user gate. M3 asserts that prose renders into BOTH user-gate skills
// (user-elicit and user-uat), so a regression that drops the agent-proposed or
// raise-at-next-gate semantics is caught.
func TestV2DeferralRaisedAtNextGateRenders(t *testing.T) {
	root := repoRoot(t)

	// Stable substrings of the V2 behavior that must render in both user gates.
	const (
		raiseNeedle = "raised to the user at the next user gate (URE, Plan UAT, or Impl UAT)"
		agentNeedle = "proposed by the architect/supervisor"
	)

	for _, rel := range []string{
		"skills/user-elicit/SKILL.md",
		"skills/user-uat/SKILL.md",
	} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		require.NoErrorf(t, err, "M3: reading %q failed", rel)
		content := string(data)
		assert.Containsf(t, content, raiseNeedle,
			"M3: generated %q does not render the raise-deferred-items-at-next-gate needle %q — "+
				"V2 requires ALL deferred items be raised to the user at the next user gate; "+
				"re-add the behavior in the body spec and regenerate.", rel, raiseNeedle)
		assert.Containsf(t, content, agentNeedle,
			"M3: generated %q does not render the agent-proposed-deferral needle %q — "+
				"V2 allows the architect/supervisor to propose deferrals (not only the user); "+
				"re-add the behavior in the body spec and regenerate.", rel, agentNeedle)
	}
}
