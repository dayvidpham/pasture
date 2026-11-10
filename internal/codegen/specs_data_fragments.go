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
	"strings"
)

// SharedFragmentSpecs is the canonical registry of reusable shared fragments.
// Keyed by FragmentId typed constant (must match the FragRef field used in
// placement marker entries).
//
// adds: FragRevVoteOptions (green-enabler; resolves the sole
// pre-existing same-id collision between reviewer-vote and reviewer skill
// bodies). Canonical content per D3 (UAT-ratified): ACCEPT row = "All review
// criteria satisfied; no BLOCKER items". Both consumer bodies carry fragRef
// markers. Set keys MUST equal AllFragmentIds (validated by ValidateGlobalIds).
//
// adds: 6 supervisor review-wave behavior fragments (FragSupReview*,
// FragSupBlockerDualParent, FragSupDeferredFollowup,
// FragSupFollowupEpicTiming) and 2 prose fragments (FragSupSeverityTree,
// FragSupNamingConvention). These replace identical inline definitions in both
// the supervisor and impl-review skill bodies, eliminating the last same-id
// collision group. Canonical content = supervisor text.
//
// (epoch improvements) adds: FragValidationCases (R6, wired into
// user-elicit/user-uat/worker-implement bodies) and FragReviewCleanExit
// (R7, wired into supervisor/impl-review bodies). It also renames
// FragSupImportantMinorFollowup -> FragSupDeferredFollowup (R7/A1): review
// severities no longer feed FOLLOWUP; the FOLLOWUP epic is fed solely by
// user-DEFER'd UAT items.
var SharedFragmentSpecs = map[FragmentId]SharedFragment{
	FragTaskRecovery: taskRecoveryFragment(),
	FragTaskAuthor: {
		Id:   FragTaskAuthor,
		Kind: FragmentKindProse,
		Prose: &ProseSection{
			Id:      string(FragTaskAuthor),
			Title:   "Task Attribution",
			Content: `Select the current registered author with [[rec:whoami]]. It prints the author recorded in the header or PASTURE_AUTHOR, and on the pasture backend it confirms that actor with agents show. It does not list agents and it does not invent one. If neither source names an author, it stops: set author: in EPOCH.md or export PASTURE_AUTHOR. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.`,
		},
	},
	FragTrackerAdapter: trackerAdapterFragment(),
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

	// ── supervisor review-wave behavior fragments ──────────────────────────────

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
			Given:     "a wave review round",
			When:      "creating severity groups",
			Then:      "ALWAYS create 3 severity groups (BLOCKER, IMPORTANT, MINOR) per wave, per axis, per round, even if empty",
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
			Then:      "track it in its severity group as an open finding; open findings do not block a wave unless they are BLOCKERs, and the FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items, never by any review severity",
			ShouldNot: "route any review severity (BLOCKER/IMPORTANT/MINOR) to the FOLLOWUP epic; close a finding outside `C-finding-lifecycle`",
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

	// ── supervisor review-wave prose fragments ─────────────────────────────────

	FragSupSeverityTree: func() SharedFragment {
		// Naming Convention is embedded as an inline Subsection so that
		// impl-review can use a single fragRef(FragSupSeverityTree) at the top-level
		// Sections list and still render ### Naming Convention (H3 via the template's
		// Subsections iteration). The standalone FragSupNamingConvention fragment
		// carries byte-identical content for supervisor's use as a sibling Subsection.
		// The heading-level analysis is why the naming section is a subsection here.
		namingConvention := ProseSection{
			Id:      string(FragSupNamingConvention),
			Title:   "Naming Convention",
			Content: "Name the review for one wave, one axis, and one round. Axis A is correctness, B is test quality, and C is elegance. List every slice of that wave on the review. Create one severity trio under it: blocker, important, and minor. Do not create one review per slice.\n\nThe first wave's correctness review, first round, has one blocker group, one important group, and one minor group. A later wave gets its own review.",
		}
		prose := ProseSection{
			Id:    string(FragSupSeverityTree),
			Title: "Severity Tree (EAGER Creation)",
			Content: `Per [frag--sup-review-severity-groups], create ONE severity trio per wave, per axis, per round — not one per slice:

` + "```" + `bash
# Wave 1 contains the first slice, the second slice, and the third slice.
# Step 1: Create all 3 severity groups immediately (EAGER)
BLOCKER_ID_URI=$([[rec:create]] --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} BLOCKER" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  review_round: 1
---
BLOCKER findings from Reviewer A (Correctness) on wave 1.")
[[rec:label]] "$BLOCKER_ID_URI" pasture:severity:blocker

IMPORTANT_ID_URI=$([[rec:create]] --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} IMPORTANT" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  review_round: 1
---
IMPORTANT findings from Reviewer A (Correctness) on wave 1.")
[[rec:label]] "$IMPORTANT_ID_URI" pasture:severity:important

MINOR_ID_URI=$([[rec:create]] --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} MINOR" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  review_round: 1
---
MINOR findings from Reviewer A (Correctness) on wave 1.")
[[rec:label]] "$MINOR_ID_URI" pasture:severity:minor

# Step 2: Wire the one severity trio to the wave's review round task
[[rec:block]] "${REVIEW_ROUND_ID_URI}" --by "$BLOCKER_ID_URI"
[[rec:block]] "${REVIEW_ROUND_ID_URI}" --by "$IMPORTANT_ID_URI"
[[rec:block]] "${REVIEW_ROUND_ID_URI}" --by "$MINOR_ID_URI"
# NEVER wire severity groups to IMPL_PLAN or slices directly.
# BLOCKER findings block slices via dual-parent (see below).
# Open findings converge at the end; only an open BLOCKER gates the wave. IMPORTANT/MINOR are NOT routed to FOLLOWUP.
# The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items (see Follow-up Epic section).

# Step 3: Close empty groups immediately
# If a group has no findings, close it right away
[[rec:close]] "$IMPORTANT_ID_URI"   # if no IMPORTANT findings
[[rec:close]] "$MINOR_ID_URI"        # if no MINOR findings
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
			Id:      string(FragSupNamingConvention),
			Title:   "Naming Convention",
			Content: "Name the review for one wave, one axis, and one round. Axis A is correctness, B is test quality, and C is elegance. List every slice of that wave on the review. Create one severity trio under it: blocker, important, and minor. Do not create one review per slice.\n\nThe first wave's correctness review, first round, has one blocker group, one important group, and one minor group. A later wave gets its own review.",
		}
		return SharedFragment{
			Id:    FragSupNamingConvention,
			Kind:  FragmentKindProse,
			Prose: &prose,
		}
	}(),

	// ── plan-review vote fragment ──────────────────────────────────────────────

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

	// ── validation and review-exit behavior fragments ──────────────────────────

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
			Given:     "per-wave code review and end-of-epoch convergence",
			When:      "evaluating review results",
			Then:      "iterate review -> fix -> re-review up to the chosen review-effort budget; during a wave only an open BLOCKER finding gates progress, while IMPORTANT and MINOR findings stay open; after the last wave, converge over open `pasture:finding` tasks across all waves toward 0/0/0 within maxRounds; a finding closes only per `C-finding-lifecycle`; on budget exhaustion without convergence, SURFACE the outstanding findings to the user at a gate for a decision",
			ShouldNot: "hardcode the budget; proceed past the chosen budget without surfacing outstanding findings to the user; require a zeroed round at a wave boundary; close a finding outside `C-finding-lifecycle`; loop forever when a finite budget was chosen",
		},
	},

	FragModelPolicy: modelPolicyFragment(),

	// ── writing standard and signal capture ────────────────────────────────────

	// FragWritingStandard is the wording half of the writing standard.
	// Consumers reference it through fragRef; none copies the text.
	FragWritingStandard: writingStandardFragment(),

	// FragWritingQuestions is the question-and-option half. Status does not
	// reference it. The union of the two fragments is the full rule set.
	FragWritingQuestions: writingQuestionFragment(),

	// FragSignalCapture is the one authored home of the signal capture.
	// Consumers reference it through fragRef; none copies the text.
	FragSignalCapture: signalCaptureFragment(),
}

// modelPolicyAskMarker is the structural signal that a rendered document
// carries the model-chain ask. A test keys on this marker instead of a
// sentence, so appending a paraphrase of the ask elsewhere cannot pass for a
// second gate and rewording the ask cannot hide the real ones.
const modelPolicyAskMarker = "<!-- pasture:model-chain-ask -->"

// modelPolicyFragment builds the model-policy prose from the capability-tier
// registry. The table is rendered from CapabilityTierSpecs rather than
// hand-maintained, so the shipped table cannot disagree with the tiers the
// emitters use (tenet 4: name it once).
func modelPolicyFragment() SharedFragment {
	var table strings.Builder
	table.WriteString("| Tier | Name | Default model | Effort | Effective roles |\n")
	table.WriteString("|------|------|---------------|--------|----------------|\n")
	for _, tier := range AllCapabilityTiers() {
		spec := mustCapabilityTierSpec(tier)
		roles := effectiveRoleNames(tier)
		shown := strings.Join(roles, ", ")
		if shown == "" {
			shown = "(runtime choice only)"
		}
		fmt.Fprintf(&table, "| %s | %s | %s | %s | %s |\n",
			tier, spec.Name, spec.DefaultModel, spec.Effort, shown)
	}

	prose := ProseSection{
		Id:    string(FragModelPolicy),
		Title: "Model Policy and Fallback Chain",
		Content: `A role names a capability tier. A tier carries a neutral default model nickname, an effort level, and its default roles.

` + modelPolicyAskMarker + `

` + table.String() + `
The reviewer's effective default is T2 standard, so the table lists reviewer under T2, not under T3. T3 mini has no effective default role; it is a phase-8 runtime choice. Record that choice as ` + "`reviewer_tier: T3`" + ` in ` + "`.agents.local/pasture/EPOCH.md`" + ` and pass that tier's native model id when reviewers are spawned. Each harness publishes every tier's native id in its emitted agent files (` + "`capability-tier`" + ` lines). On a harness with no smaller model than T2, T3's id equals T2's; Codex is that harness, so a cheap review there uses the standard profile.

**Ask here, and at the matching gate in the other phase.** At phase 1 and again at phase 8, ask the user for three models: a primary, a secondary, and a tertiary. One gate, two asks, not five. The three form one fallback chain: when a provider is exhausted, use the next model in the chain; never fail the epoch. Record the chain on the request or IMPL_PLAN task and repeat it in every handoff. Advance the chain in the architect and supervisor surfaces too: they spawn the reviewers and workers, so a provider exhaustion reaches them first.

**Reviewer override.** The reviewer tier is the reviewer role's TierOverride, not a separate review-budget type. The default is T2 standard (full review); treat T3 mini as an epoch-recorded runtime choice, never a compile-time one.

**Concurrency cap.** Run at most ` + fmt.Sprintf("%d", MaxConcurrentAgents) + ` delegated agents at once in one stage. Phase 9 spawns one worker per slice and has no slice bound; when a plan has more slices than the cap, start them in batches of that size. The cap is a batching policy, not a protocol maximum. It bounds concurrency only; maxRounds remains the only bound on review rounds.`,
	}
	return SharedFragment{
		Id:    FragModelPolicy,
		Kind:  FragmentKindProse,
		Prose: &prose,
	}
}

// taskRecoveryFragment builds the task-recovery prose from the tracker
// contract. The closed operation set and the mid-epoch write-failure rule are
// rendered from their one authored home (AllTrackerOpIds and
// TrackerWriteFailureRule), so every role that carries recovery instructions
// also carries the rule that applies when a write fails.
func taskRecoveryFragment() SharedFragment {
	ids := AllTrackerOpIds()
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, string(id))
	}
	prose := ProseSection{
		Id:    string(FragTaskRecovery),
		Title: "Task Recovery",
		Content: `The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the ` + "`rec:`" + ` path from the header and use it as the one entry point: ` + "`\"${EPOCH_REC}\" <op>`" + `. The operation set is closed: ` + strings.Join(names, ", ") + `.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's ` + "`db:`" + `; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

The adapter passes ` + "`--namespace`" + ` only when EPOCH.md records ` + "`namespace:`" + `. That pin wins on both backends. Otherwise it omits the flag. Pasture then derives the namespace itself: PASTURE_NAMESPACE, else the git remote, then file:// of the working directory. The ledger calls derive_namespace for the header and for every write that has no pin. That function is the one list of remote forms; do not restate it. A ledger with no origin uses file:// of the git work-tree root, so a subdirectory does not split the namespace. Queries without a pin follow that backend's derivation; pass ` + "`--all-namespaces`" + ` to opt out. When neither a pin nor PASTURE_NAMESPACE is set, pasture reads are unscoped and creation falls back to the git remote, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run ` + "`[[rec:ready]]`" + `, ` + "`[[rec:query]] --blocked`" + `, and ` + "`[[rec:query]] --status in_progress`" + `. For each referenced active task, run ` + "`[[rec:show]] \"$TASK_URI\"`" + `, ` + "`[[rec:show]] \"$TASK_URI\" --notes`" + `, and ` + "`[[rec:show]] \"$TASK_URI\" --history`" + `. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with ` + "`[[rec:label]] \"$TASK_URI\" owner:<role>`" + `. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: ` + "`[[rec:block]] \"$PARENT_URI\" --by \"$CHILD_URI\"`" + `. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records ` + "`backend: error`" + `, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. ` + TrackerWriteFailureRule,
	}
	return SharedFragment{
		Id:    FragTaskRecovery,
		Kind:  FragmentKindProse,
		Prose: &prose,
	}
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
