// Body content for the impl-slice command SKILL.md.
// Ported from aura-plugins/skills/impl-slice/SKILL.md.
package codegen

var implSliceBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9",

	Behaviors: []BehaviorSpec{
		{
			Id:        "impl-slice-full-specs",
			Given:     "IMPL_PLAN complete",
			When:      "assigning slices",
			Then:      "create the slice tasks with full specs",
			ShouldNot: "leave specs vague",
		},
		{
			Id:        "impl-slice-dep-chain",
			Given:     "slice assigned",
			When:      "creating task",
			Then:      "chain dependency to IMPL_PLAN: [[rec:block]] \"${IMPL_PLAN_ID_URI}\" --by \"${SLICE_ID_URI}\"",
			ShouldNot: "create orphan slices",
		},
		{
			Id:        "impl-slice-track-status",
			Given:     "worker starts",
			When:      "tracking",
			Then:      "update task to in_progress",
			ShouldNot: "leave status as open",
		},
		{
			Id:        "impl-slice-complete-label",
			Given:     "slice complete",
			When:      "verifying",
			Then:      "record a completion note",
			ShouldNot: "close the task prematurely",
		},
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:    "impl-slice-structure",
			Title: "Slice Structure",
			Content: `Each vertical slice contains:
- **slice_id**: A name for the work, not an internal code
- **slice_name**: Human-readable name
- **slice_spec**: Detailed implementation specification
- **slice_files**: Files owned by this slice`,
		},
		{
			Id:    "impl-slice-creating",
			Title: "Creating Slices",
			Content: `After supervisor decomposes the ratified plan:

` + "```" + `bash
# Create the first slice
SLICE_1_ID_URI=$([[rec:create]] --kind task --title "<slice name>" --phase worker_slices \
  --body "---
references:
  impl_plan: "${IMPL_PLAN_ID_URI}"
  urd: "${URD_ID_URI}"
---
## Specification
<detailed implementation spec>

## Files Owned
<list of files this slice owns>

## Acceptance Criteria
<criteria from ratified plan>

## Validation Checklist
- [ ] Types defined
- [ ] Tests written (import production code)
- [ ] Implementation complete
- [ ] Wiring complete
- [ ] Production code path verified")
[[rec:note]] "$SLICE_1_ID_URI" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Design: '{\"validation_checklist\":[\"Types defined\",\"Tests written (import production code)\",\"Implementation complete\",\"Wiring complete\",\"Production code path verified\"],\"acceptance_criteria\":[{\"given\":\"X\",\"when\":\"Y\",\"then\":\"Z\"}],\"ratified_plan\":\"${RATIFIED_PLAN_ID_URI}\"}'; Requested role (not an assignment): worker-1"

[[rec:block]] "${IMPL_PLAN_ID_URI}" --by "${SLICE_1_ID_URI}"
` + "```" + ``,
		},
		{
			Id:    "impl-slice-assigning",
			Title: "Assigning Workers",
			Content: `` + "```" + `bash
[[rec:label]] "${SLICE_1_ID_URI}" owner:"${REGISTERED_WORKER_ACTOR_1}"
[[rec:label]] "${SLICE_2_ID_URI}" owner:"${REGISTERED_WORKER_ACTOR_2}"
[[rec:label]] "${SLICE_3_ID_URI}" owner:"${REGISTERED_WORKER_ACTOR_3}"
` + "```" + ``,
		},
		{
			Id:    "impl-slice-tracking",
			Title: "Tracking Progress",
			Content: `` + "```" + `bash
# Worker starts
[[rec:status]] "${SLICE_ID_URI}" --set in_progress

# Check all slice status
[[rec:query]] --phase worker_slices --status=open
[[rec:query]] --phase worker_slices --status=in_progress

# Worker completes (add comment and label)
[[rec:note]] "${SLICE_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "COMPLETE: All checklist items verified. Production code path working."
` + "```" + ``,
		},
		{
			Id:    "impl-slice-dependencies",
			Title: "Slice Dependencies",
			Content: `Slices can have dependencies on each other (sync points):

` + "```" + `bash
# the second slice depends on the first slice completing first
[[rec:block]] "${SLICE_2_ID_URI}" --by "${SLICE_1_ID_URI}"
` + "```" + `

Minimize inter-slice dependencies when possible.`,
		},
		{
			Id:    "impl-slice-aggregation",
			Title: "Aggregation",
			Content: `The aggregation step waits for every slice to record completion before code review:

` + "```" + `bash
# Slices in this phase, including those still open
[[rec:query]] --phase worker_slices
` + "```" + ``,
		},
		{
			Id:    "impl-slice-followup",
			Title: "Follow-up slices",
			Content: `Follow-up slices use the same structure and tracking, with additional fields:
- **Title:** name the work, for example ` + "`" + `Add correlation` + "`" + `
- **Adopted leaf tasks:** User-DEFER'd UAT-item leaf tasks become dual-parent children (original severity group + follow-up slice)
- **Tracking:** ` + "`" + `[[rec:query]] --phase worker_slices` + "`" + ` includes both ordinary slices and follow-up slices`,
		},
	},

	Recipes: []RecipeBlock{},
}
