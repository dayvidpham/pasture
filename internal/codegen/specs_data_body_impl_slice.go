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
			Then:      "create SLICE-N tasks with full specs",
			ShouldNot: "leave specs vague",
		},
		{
			Id:        "impl-slice-dep-chain",
			Given:     "slice assigned",
			When:      "creating task",
			Then:      "chain dependency to IMPL_PLAN: pasture task dep add \"${IMPL_PLAN_ID_URI}\" --blocked-by \"${SLICE_ID_URI}\"",
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
			Then:      "add completion label and comments",
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
- **slice_id**: Identifier (SLICE-1, SLICE-2, SLICE-3, ...)
- **slice_name**: Human-readable name
- **slice_spec**: Detailed implementation specification
- **slice_files**: Files owned by this slice`,
		},
		{
			Id:    "impl-slice-creating",
			Title: "Creating Slices",
			Content: `After supervisor decomposes the ratified plan:

` + "```" + `bash
# Create SLICE-1
SLICE_1_ID_URI=$(pasture task create "SLICE-1: <slice name>" --phase worker_slices --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
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
- [ ] Production code path verified" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$SLICE_1_ID_URI" pasture:p9-impl:s9-slice
pasture task update "$SLICE_1_ID_URI" --notes "Design: '{\"validation_checklist\":[\"Types defined\",\"Tests written (import production code)\",\"Implementation complete\",\"Wiring complete\",\"Production code path verified\"],\"acceptance_criteria\":[{\"given\":\"X\",\"when\":\"Y\",\"then\":\"Z\"}],\"ratified_plan\":\"${RATIFIED_PLAN_ID_URI}\"}'; Requested role (not an assignment): worker-1"

pasture task dep add "${IMPL_PLAN_ID_URI}" --blocked-by "${SLICE_1_ID_URI}"
` + "```" + ``,
		},
		{
			Id:    "impl-slice-assigning",
			Title: "Assigning Workers",
			Content: `` + "```" + `bash
pasture task assignment transfer "${SLICE_1_ID_URI}" --slot owner-responsibility --assignment "${SUCCESSOR_ASSIGNMENT_1_ID}" --actor pasture-system--00000000-0000-0000-0000-000000000000 --occupant "${REGISTERED_WORKER_ACTOR_1}"
pasture task assignment transfer "${SLICE_2_ID_URI}" --slot owner-responsibility --assignment "${SUCCESSOR_ASSIGNMENT_2_ID}" --actor pasture-system--00000000-0000-0000-0000-000000000000 --occupant "${REGISTERED_WORKER_ACTOR_2}"
pasture task assignment transfer "${SLICE_3_ID_URI}" --slot owner-responsibility --assignment "${SUCCESSOR_ASSIGNMENT_3_ID}" --actor pasture-system--00000000-0000-0000-0000-000000000000 --occupant "${REGISTERED_WORKER_ACTOR_3}"
` + "```" + ``,
		},
		{
			Id:    "impl-slice-tracking",
			Title: "Tracking Progress",
			Content: `` + "```" + `bash
# Worker starts
pasture task update "${SLICE_ID_URI}" --status in_progress

# Check all slice status
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=open
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=in_progress

# Worker completes (add comment and label)
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${SLICE_ID_URI}" "COMPLETE: All checklist items verified. Production code path working."
pasture task label add "${SLICE_ID_URI}" pasture:p9-impl:slice-complete
` + "```" + ``,
		},
		{
			Id:    "impl-slice-dependencies",
			Title: "Slice Dependencies",
			Content: `Slices can have dependencies on each other (sync points):

` + "```" + `bash
# SLICE-2 depends on SLICE-1 completing first
pasture task dep add "${SLICE_2_ID_URI}" --blocked-by "${SLICE_1_ID_URI}"
` + "```" + `

Minimize inter-slice dependencies when possible.`,
		},
		{
			Id:    "impl-slice-aggregation",
			Title: "Aggregation",
			Content: `The aggregation step waits for all slices to complete before code review:

` + "```" + `bash
# Check if all slices have complete label
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:slice-complete"

# Compare to total slices
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice"
` + "```" + ``,
		},
		{
			Id:    "impl-slice-followup",
			Title: "Follow-up Slices (FOLLOWUP_SLICE-N)",
			Content: `Follow-up slices use the same structure and tracking, with additional fields:
- **Title prefix:** ` + "`" + `FOLLOWUP_SLICE-N:` + "`" + ` (e.g., ` + "`" + `FOLLOWUP_SLICE-1: Add "${REQUEST_ID_URI}" correlation` + "`" + `)
- **Adopted leaf tasks:** User-DEFER'd UAT-item leaf tasks become dual-parent children (original severity group + follow-up slice)
- **Tracking:** Same ` + "`" + `pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice"` + "`" + ` queries include both regular and follow-up slices`,
		},
	},

	Recipes: []RecipeBlock{},
}
