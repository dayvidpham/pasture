// Canonical SharedFragment registry and helper constructors.
//
// This file declares the SharedFragmentSpecs map, fragRef/behaviorRef marker
// constructors, and FragmentToOwnerRefs, which inverts the consumer-keyed
// SkillBodySpecs to produce a map from FragmentId to sorted owner skill-dir
// keys.
//
// Mirror: ConstraintToRoleRefs() at context.go inverts the consumer-keyed
// roleConstraints map; FragmentToOwnerRefs mirrors that pattern exactly,
// operating over SkillBodySpecs instead.
package codegen

import (
	"fmt"
	"sort"
)

// SharedFragmentSpecs is the canonical registry of reusable shared fragments.
// Keyed by FragmentId typed constant (must match the FragRef field used in
// placement marker entries).
//
// SLICE-2 adds: FragRevVoteOptions (green-enabler; resolves the sole
// pre-existing same-id collision between reviewer-vote and reviewer skill
// bodies). Canonical content per D3 (UAT-ratified): ACCEPT row = "All review
// criteria satisfied; no BLOCKER items". Both consumer bodies carry fragRef
// markers. Set keys MUST equal AllFragmentIds (validated by ValidateGlobalIds).
//
// SLICE-3 adds: 6 supervisor review-wave behavior fragments (FragSupReview*,
// FragSupBlockerDualParent, FragSupDeferredFollowup,
// FragSupFollowupEpicTiming) and 2 prose fragments (FragSupSeverityTree,
// FragSupNamingConvention). These replace identical inline definitions in both
// the supervisor and impl-review skill bodies, eliminating the last same-id
// collision group. Canonical content = supervisor text.
//
// SLICE-1 (epoch improvements) adds: FragValidationCases (R6, wired into
// user-elicit/user-uat/worker-implement bodies by SLICE-2) and FragReviewCleanExit
// (R7, wired into supervisor/impl-review bodies by SLICE-3). It also renames
// FragSupImportantMinorFollowup -> FragSupDeferredFollowup (R7/A1): review
// severities no longer feed FOLLOWUP; the FOLLOWUP epic is fed solely by
// user-DEFER'd UAT items.
var SharedFragmentSpecs = map[FragmentId]SharedFragment{
	FragTaskRecovery: {
		Id:   FragTaskRecovery,
		Kind: FragmentKindProse,
		Prose: &ProseSection{
			Id:    string(FragTaskRecovery),
			Title: "Task Recovery",
			Content: `Recover from live Pasture state at session start and after compaction; never trust cached task rows.

Store selection: an explicit --db overrides PASTURE_DB_PATH; otherwise use XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db. Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

Set PASTURE_NAMESPACE to the repository's canonical namespace URI, for example https://github.com/dayvidpham/pasture. Explicit --namespace overrides the git-remote-derived namespace, then file:// of the working directory. List requires an explicit namespace to avoid mixing repositories. ready/blocked have only an exact --label filter, not namespace filtering; inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run pasture task ready, pasture task blocked, and pasture task list --namespace "$PASTURE_NAMESPACE" --status in_progress. For each referenced active task, run pasture task show "$TASK_URI", pasture task comments "$TASK_URI", and pasture task timeline "$TASK_URI". All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the actual JSON id returned by create; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create returns an object whose id is the task URI: capture it with --format json and python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'. Persist that URI before label/comment steps. Create-plus-label is non-atomic: on failure retain the URI and retry only the failed label/comment operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

Assignment transfer is not initial allocation: before using pasture task assignment transfer, obtain the existing owner-responsibility assignment and its exact successor assignment ID, registered committing actor, and registered worker occupant from the supervisor. If the task is unassigned, stop and let the supervisor arrange allocation; no assignment-start command is implied. Never repeat an identical transfer as a substitute for allocating different workers.

Parent stays open and is blocked by child: pasture task dep add "$PARENT_URI" --blocked-by "$CHILD_URI". Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.`,
		},
	},
	FragTaskAuthor: {
		Id:   FragTaskAuthor,
		Kind: FragmentKindProse,
		Prose: &ProseSection{
			Id:      string(FragTaskAuthor),
			Title:   "Task Attribution",
			Content: `Select the current registered author with pasture task agents list and pasture task agents show ACTOR-ID. Never auto-register or guess an identity during recovery. Every comment supplies --author explicitly; machine examples use the pre-registered pasture-system--00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.`,
		},
	},
	FragRevVoteOptions: func() SharedFragment {
		prose := ProseSection{
			Id:    string(FragRevVoteOptions),
			Title: "Vote Options",
			Content: `| Vote | When |
|------|------|
| ACCEPT | All review criteria satisfied; no BLOCKER items |
| REVISE | BLOCKER issues found; must provide actionable feedback |

Binary only. No intermediate levels.`,
		}
		return SharedFragment{
			Id:    FragRevVoteOptions,
			Kind:  FragmentKindProse,
			Prose: &prose,
		}
	}(),

	// ── SLICE-3 behavior fragments ─────────────────────────────────────────────

	FragSupReviewAllSlices: {
		Id:   FragSupReviewAllSlices,
		Kind: FragmentKindBehavior,
		Behavior: &BehaviorSpec{
			Id:        string(FragSupReviewAllSlices),
			Given:     "all slices complete",
			When:      "starting review",
			Then:      "spawn 3 reviewers for ALL slices",
			ShouldNot: "assign reviewers to single slices",
		},
	},

	FragSupReviewCheckEach: {
		Id:   FragSupReviewCheckEach,
		Kind: FragmentKindBehavior,
		Behavior: &BehaviorSpec{
			Id:        string(FragSupReviewCheckEach),
			Given:     "reviewer assigned",
			When:      "reviewing",
			Then:      "check each slice against criteria",
			ShouldNot: "skip any slice",
		},
	},

	FragSupReviewSeverityGroups: {
		Id:   FragSupReviewSeverityGroups,
		Kind: FragmentKindBehavior,
		Behavior: &BehaviorSpec{
			Id:        string(FragSupReviewSeverityGroups),
			Given:     "review round",
			When:      "creating severity groups",
			Then:      "ALWAYS create 3 severity groups (BLOCKER, IMPORTANT, MINOR) per round even if empty",
			ShouldNot: "lazily create groups only when findings exist",
		},
	},

	FragSupBlockerDualParent: {
		Id:   FragSupBlockerDualParent,
		Kind: FragmentKindBehavior,
		Behavior: &BehaviorSpec{
			Id:        string(FragSupBlockerDualParent),
			Given:     "BLOCKER finding",
			When:      "wiring dependencies",
			Then:      "add dual-parent: blocks BOTH the severity group AND the slice",
			ShouldNot: "wire BLOCKER to only one parent",
		},
	},

	FragSupDeferredFollowup: {
		Id:   FragSupDeferredFollowup,
		Kind: FragmentKindBehavior,
		Behavior: &BehaviorSpec{
			Id:        string(FragSupDeferredFollowup),
			Given:     "a review finding (BLOCKER, IMPORTANT, or MINOR)",
			When:      "categorizing",
			Then:      "track it in its severity group; ALL severity groups must reach 0 before wave close — the FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items, never by any review severity",
			ShouldNot: "route any review severity (BLOCKER/IMPORTANT/MINOR) to the FOLLOWUP epic; close a wave with any finding outstanding",
		},
	},

	FragSupFollowupEpicTiming: {
		Id:   FragSupFollowupEpicTiming,
		Kind: FragmentKindBehavior,
		Behavior: &BehaviorSpec{
			Id:        string(FragSupFollowupEpicTiming),
			Given:     "UAT (Phase 5 or 11) produces one or more user-DEFER'd items",
			When:      "finishing UAT",
			Then:      "supervisor creates the FOLLOWUP epic from the user-DEFER'd UAT items only",
			ShouldNot: "create a FOLLOWUP epic from any review severity (BLOCKER/IMPORTANT/MINOR)",
		},
	},

	// ── SLICE-3 prose fragments ────────────────────────────────────────────────

	FragSupSeverityTree: func() SharedFragment {
		// Naming Convention is embedded as an inline Subsection so that
		// impl-review can use a single fragRef(FragSupSeverityTree) at the top-level
		// Sections list and still render ### Naming Convention (H3 via the template's
		// Subsections iteration). The standalone FragSupNamingConvention fragment
		// carries byte-identical content for supervisor's use as a sibling Subsection.
		// See Part 6 of worker-b-SLICE-3-4-implplan.md for the full heading-level analysis.
		namingConvention := ProseSection{
			Id:    string(FragSupNamingConvention),
			Title: "Naming Convention",
			Content: "```" + `
SLICE-{N}-REVIEW-{axis}-{round}
` + "```" + `

Where axis = A (Correctness), B (Test quality), C (Elegance).

Examples:
- ` + "`SLICE-1-REVIEW-A-1`" + ` — Reviewer A (Correctness), Round 1, SLICE-1
- ` + "`SLICE-2-REVIEW-C-2`" + ` — Reviewer C (Elegance), Round 2, SLICE-2

Severity groups:
- ` + "`SLICE-1-REVIEW-A-1 BLOCKER`" + `
- ` + "`SLICE-1-REVIEW-A-1 IMPORTANT`" + `
- ` + "`SLICE-1-REVIEW-A-1 MINOR`",
		}
		prose := ProseSection{
			Id:    string(FragSupSeverityTree),
			Title: "Severity Tree (EAGER Creation)",
			Content: `Per [frag--sup-review-severity-groups], create all 3 severity groups immediately:

` + "```" + `bash
# Step 1: Create all 3 severity groups immediately (EAGER)
BLOCKER_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1 BLOCKER" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_1_ID_URI}"
  review_round: 1
---
BLOCKER findings from Reviewer A (Correctness) on SLICE-1." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$BLOCKER_ID_URI" pasture:severity:blocker
pasture task label add "$BLOCKER_ID_URI" pasture:p10-impl:s10-review

IMPORTANT_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1 IMPORTANT" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_1_ID_URI}"
  review_round: 1
---
IMPORTANT findings from Reviewer A (Correctness) on SLICE-1." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$IMPORTANT_ID_URI" pasture:severity:important
pasture task label add "$IMPORTANT_ID_URI" pasture:p10-impl:s10-review

MINOR_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1 MINOR" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_1_ID_URI}"
  review_round: 1
---
MINOR findings from Reviewer A (Correctness) on SLICE-1." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$MINOR_ID_URI" pasture:severity:minor
pasture task label add "$MINOR_ID_URI" pasture:p10-impl:s10-review

# Step 2: Wire severity groups to the review round task
pasture task dep add "${REVIEW_ROUND_ID_URI}" --blocked-by "$BLOCKER_ID_URI"
pasture task dep add "${REVIEW_ROUND_ID_URI}" --blocked-by "$IMPORTANT_ID_URI"
pasture task dep add "${REVIEW_ROUND_ID_URI}" --blocked-by "$MINOR_ID_URI"
# NEVER wire severity groups to IMPL_PLAN or slices directly.
# BLOCKER findings block slices via dual-parent (see below).
# IMPORTANT/MINOR must ALSO reach 0 before wave close — they are NOT routed to FOLLOWUP.
# The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items (see Follow-up Epic section).

# Step 3: Close empty groups immediately
# If a group has no findings, close it right away
pasture task close "$IMPORTANT_ID_URI"   # if no IMPORTANT findings
pasture task close "$MINOR_ID_URI"        # if no MINOR findings
` + "```" + ``,
			Subsections: []ProseSection{namingConvention},
		}
		return SharedFragment{
			Id:    FragSupSeverityTree,
			Kind:  FragmentKindProse,
			Prose: &prose,
		}
	}(),

	FragSupNamingConvention: func() SharedFragment {
		prose := ProseSection{
			Id:    string(FragSupNamingConvention),
			Title: "Naming Convention",
			Content: "```" + `
SLICE-{N}-REVIEW-{axis}-{round}
` + "```" + `

Where axis = A (Correctness), B (Test quality), C (Elegance).

Examples:
- ` + "`SLICE-1-REVIEW-A-1`" + ` — Reviewer A (Correctness), Round 1, SLICE-1
- ` + "`SLICE-2-REVIEW-C-2`" + ` — Reviewer C (Elegance), Round 2, SLICE-2

Severity groups:
- ` + "`SLICE-1-REVIEW-A-1 BLOCKER`" + `
- ` + "`SLICE-1-REVIEW-A-1 IMPORTANT`" + `
- ` + "`SLICE-1-REVIEW-A-1 MINOR`",
		}
		return SharedFragment{
			Id:    FragSupNamingConvention,
			Kind:  FragmentKindProse,
			Prose: &prose,
		}
	}(),

	// ── SLICE-4 prose fragment ─────────────────────────────────────────────────

	// FragRevPlanVoteOptions is DISTINCT from FragRevVoteOptions (code review) by
	// its final line: "Binary only. No severity tree for plan reviews."
	// (vs code review's "Binary only. No intermediate levels."). The ACCEPT-row
	// wording is shared (unified per UAT-2). The two fragments must NOT be merged
	// (D2). Single-owner (reviewer-review-plan), promoted for registry
	// completeness and validator-enforced distinctness.
	FragRevPlanVoteOptions: func() SharedFragment {
		prose := ProseSection{
			Id:    string(FragRevPlanVoteOptions),
			Title: "Vote Options",
			Content: `| Vote | When |
|------|------|
| ACCEPT | All review criteria satisfied; no BLOCKER items |
| REVISE | BLOCKER issues found; must provide actionable feedback |

Binary only. No severity tree for plan reviews.`,
		}
		return SharedFragment{
			Id:    FragRevPlanVoteOptions,
			Kind:  FragmentKindProse,
			Prose: &prose,
		}
	}(),

	// ── SLICE-1 (epoch improvements) behavior fragments ────────────────────────

	FragValidationCases: {
		Id:   FragValidationCases,
		Kind: FragmentKindBehavior,
		Behavior: &BehaviorSpec{
			Id:        string(FragValidationCases),
			Given:     "any REQUEST (every request, not only fix-intent ones)",
			When:      "eliciting (URE), acceptance-testing (UAT), or implementing",
			Then:      "elicit concrete validation cases — a definition of done plus correct and incorrect behaviours (inputs/behaviors that must pass or must fail), confirm the case set with the user in UAT, evaluate the implementation against them, and store failing real-data cases as test fixtures",
			ShouldNot: "ship without validation cases; treat validation cases as applying to fix-intent requests only; introduce a request-type axis or enum to gate them",
		},
	},

	FragReviewCleanExit: {
		Id:   FragReviewCleanExit,
		Kind: FragmentKindBehavior,
		Behavior: &BehaviorSpec{
			Id:        string(FragReviewCleanExit),
			Given:     "per-slice code review",
			When:      "evaluating review results",
			Then:      "iterate review -> fix -> re-review up to the chosen review-effort budget; clean = 0 BLOCKER + 0 IMPORTANT + 0 MINOR within budget; on budget exhaustion without clean, SURFACE the outstanding findings to the user at a gate for a decision",
			ShouldNot: "hardcode the budget; proceed past the chosen budget without surfacing outstanding findings to the user; loop forever when a finite budget was chosen",
		},
	},
}

// fragRef returns a ProseSection placement marker that references the given
// fragment. All fields except FragRef are left at their zero values; the
// pre-render resolution pass in skills.go replaces the marker with the actual
// ProseSection payload from SharedFragmentSpecs.
func fragRef(id FragmentId) ProseSection {
	return ProseSection{FragRef: id}
}

// behaviorRef returns a BehaviorSpec placement marker that references the given
// fragment. All fields except FragRef are left at their zero values; the
// pre-render resolution pass in skills.go replaces the marker with the actual
// BehaviorSpec payload from SharedFragmentSpecs.
func behaviorRef(id FragmentId) BehaviorSpec {
	return BehaviorSpec{FragRef: id}
}

// FragmentToOwnerRefs returns a map from FragmentId to the sorted list of
// skill-dir keys (SkillBodySpecs keys) whose body sections or behaviors
// reference that fragment via a placement marker.
//
// It mirrors ConstraintToRoleRefs: iterate the consumer-keyed map, invert to
// fragment-keyed, sort owner slices for deterministic output.
func FragmentToOwnerRefs() map[FragmentId][]string {
	return fragmentToOwnerRefsFrom(SkillBodySpecs)
}

// fragmentToOwnerRefsFrom is the testable inner implementation of
// FragmentToOwnerRefs. It accepts any SkillBody map so tests can pass a
// fixture map without mutating the package-level SkillBodySpecs.
func fragmentToOwnerRefsFrom(bodySpecs map[string]SkillBody) map[FragmentId][]string {
	result := make(map[FragmentId][]string)
	for skillDir, body := range bodySpecs {
		// Collect fragment refs from top-level Sections (and their Subsections).
		for _, sec := range body.Sections {
			if sec.FragRef != "" {
				result[sec.FragRef] = append(result[sec.FragRef], skillDir)
			}
			for _, sub := range sec.Subsections {
				if sub.FragRef != "" {
					result[sub.FragRef] = append(result[sub.FragRef], skillDir)
				}
			}
		}
		// Collect fragment refs from Behaviors.
		for _, beh := range body.Behaviors {
			if beh.FragRef != "" {
				result[beh.FragRef] = append(result[beh.FragRef], skillDir)
			}
		}
	}
	// Sort each owner slice for deterministic output.
	for fragId := range result {
		owners := result[fragId]
		sort.Strings(owners)
		result[fragId] = owners
	}
	return result
}

// resolveBodyFragments resolves placement markers in sections and behaviors
// using the provided registry. Entries without FragRef pass through unchanged.
// Entries with FragRef are replaced with the fragment payload from the registry.
// Nested ProseSection.Subsections are resolved recursively.
//
// Parameters:
//   - sections: top-level ProseSection slice (may contain markers)
//   - behaviors: BehaviorSpec slice (may contain markers)
//   - registry: fragment registry (use SharedFragmentSpecs in production)
//   - consumerSkill: skill-dir key for actionable error messages
//   - consumerFile: file path for actionable error messages
//
// Returns an actionable error if a marker references a FragmentId not present
// in the registry.
func resolveBodyFragments(
	sections []ProseSection,
	behaviors []BehaviorSpec,
	registry map[FragmentId]SharedFragment,
	consumerSkill, consumerFile string,
) ([]ProseSection, []BehaviorSpec, error) {
	resolvedSections, err := resolveProseSections(sections, registry, consumerSkill, consumerFile)
	if err != nil {
		return nil, nil, err
	}
	resolvedBehaviors, err := resolveBehaviors(behaviors, registry, consumerSkill, consumerFile)
	if err != nil {
		return nil, nil, err
	}
	return resolvedSections, resolvedBehaviors, nil
}

// resolveProseSections resolves fragment markers in a slice of ProseSection,
// including nested Subsections, using the provided registry.
func resolveProseSections(
	sections []ProseSection,
	registry map[FragmentId]SharedFragment,
	consumerSkill, consumerFile string,
) ([]ProseSection, error) {
	if len(sections) == 0 {
		return sections, nil
	}
	result := make([]ProseSection, 0, len(sections))
	for _, sec := range sections {
		if sec.FragRef == "" {
			// Non-marker: resolve subsections recursively, then pass through.
			resolvedSubs, err := resolveProseSections(sec.Subsections, registry, consumerSkill, consumerFile)
			if err != nil {
				return nil, err
			}
			sec.Subsections = resolvedSubs
			result = append(result, sec)
			continue
		}
		// Marker: look up fragment in registry.
		frag, ok := registry[sec.FragRef]
		if !ok {
			return nil, fmt.Errorf(
				"codegen.resolveBodyFragments: unresolvable prose marker %q in skill %q — "+
					"where: %s — "+
					"when: pre-render fragment resolution — "+
					"what it means: a ProseSection with FragRef=%q references a fragment "+
					"that does not exist in SharedFragmentSpecs — "+
					"fix: add fragment %q to SharedFragmentSpecs in specs_data_fragments.go "+
					"with Kind=FragmentKindProse and a non-nil Prose payload",
				sec.FragRef, consumerSkill, consumerFile, sec.FragRef, sec.FragRef,
			)
		}
		if frag.Kind != FragmentKindProse || frag.Prose == nil {
			return nil, fmt.Errorf(
				"codegen.resolveBodyFragments: fragment %q has Kind=%q (not prose) or nil Prose in skill %q — "+
					"where: %s — "+
					"fix: set Kind=FragmentKindProse and a non-nil Prose pointer in SharedFragmentSpecs[%q]",
				sec.FragRef, frag.Kind, consumerSkill, consumerFile, sec.FragRef,
			)
		}
		result = append(result, *frag.Prose)
	}
	return result, nil
}

// resolveBehaviors resolves fragment markers in a slice of BehaviorSpec
// using the provided registry.
func resolveBehaviors(
	behaviors []BehaviorSpec,
	registry map[FragmentId]SharedFragment,
	consumerSkill, consumerFile string,
) ([]BehaviorSpec, error) {
	if len(behaviors) == 0 {
		return behaviors, nil
	}
	result := make([]BehaviorSpec, 0, len(behaviors))
	for _, beh := range behaviors {
		if beh.FragRef == "" {
			result = append(result, beh)
			continue
		}
		// Marker: look up fragment in registry.
		frag, ok := registry[beh.FragRef]
		if !ok {
			return nil, fmt.Errorf(
				"codegen.resolveBodyFragments: unresolvable behavior marker %q in skill %q — "+
					"where: %s — "+
					"when: pre-render fragment resolution — "+
					"what it means: a BehaviorSpec with FragRef=%q references a fragment "+
					"that does not exist in SharedFragmentSpecs — "+
					"fix: add fragment %q to SharedFragmentSpecs in specs_data_fragments.go "+
					"with Kind=FragmentKindBehavior and a non-nil Behavior payload",
				beh.FragRef, consumerSkill, consumerFile, beh.FragRef, beh.FragRef,
			)
		}
		if frag.Kind != FragmentKindBehavior || frag.Behavior == nil {
			return nil, fmt.Errorf(
				"codegen.resolveBodyFragments: fragment %q has Kind=%q (not behavior) or nil Behavior in skill %q — "+
					"where: %s — "+
					"fix: set Kind=FragmentKindBehavior and a non-nil Behavior pointer in SharedFragmentSpecs[%q]",
				beh.FragRef, frag.Kind, consumerSkill, consumerFile, beh.FragRef,
			)
		}
		result = append(result, *frag.Behavior)
	}
	return result, nil
}
