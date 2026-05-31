// Body content for the impl-slice command SKILL.md.
// Ported from aura-plugins/skills/impl-slice/SKILL.md.
package codegen

var implSliceBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9",

	Behaviors: []BehaviorSpec{
		{
			ID:        "impl-slice-full-specs",
			Given:     "IMPL_PLAN complete",
			When:      "assigning slices",
			Then:      "create SLICE-N tasks with full specs",
			ShouldNot: "leave specs vague",
		},
		{
			ID:        "impl-slice-dep-chain",
			Given:     "slice assigned",
			When:      "creating task",
			Then:      "chain dependency to IMPL_PLAN: bd dep add <impl-plan-id> --blocked-by <slice-id>",
			ShouldNot: "create orphan slices",
		},
		{
			ID:        "impl-slice-track-status",
			Given:     "worker starts",
			When:      "tracking",
			Then:      "update task to in_progress",
			ShouldNot: "leave status as open",
		},
		{
			ID:        "impl-slice-complete-label",
			Given:     "slice complete",
			When:      "verifying",
			Then:      "add completion label and comments",
			ShouldNot: "close the task prematurely",
		},
	},

	Sections: []ProseSection{
		{
			ID:    "impl-slice-structure",
			Title: "Slice Structure",
			Content: `Each vertical slice contains:
- **slice_id**: Identifier (SLICE-1, SLICE-2, SLICE-3, ...)
- **slice_name**: Human-readable name
- **slice_spec**: Detailed implementation specification
- **slice_files**: Files owned by this slice`,
		},
		{
			ID:    "impl-slice-creating",
			Title: "Creating Slices",
			Content: "After supervisor decomposes the ratified plan:\n\n" +
				"```bash\n" +
				"# Create SLICE-1\n" +
				"bd create --labels \"aura:p9-impl:s9-slice\" \\\n" +
				"  --title \"SLICE-1: <slice name>\" \\\n" +
				"  --description \"---\n" +
				"references:\n" +
				"  impl_plan: <impl-plan-task-id>\n" +
				"  urd: <urd-task-id>\n" +
				"---\n" +
				"## Specification\n" +
				"<detailed implementation spec>\n\n" +
				"## Files Owned\n" +
				"<list of files this slice owns>\n\n" +
				"## Acceptance Criteria\n" +
				"<criteria from ratified plan>\n\n" +
				"## Validation Checklist\n" +
				"- [ ] Types defined\n" +
				"- [ ] Tests written (import production code)\n" +
				"- [ ] Implementation complete\n" +
				"- [ ] Wiring complete\n" +
				"- [ ] Production code path verified\" \\\n" +
				"  --design='{\"validation_checklist\":[\"Types defined\",\"Tests written (import production code)\",\"Implementation complete\",\"Wiring complete\",\"Production code path verified\"],\"acceptance_criteria\":[{\"given\":\"X\",\"when\":\"Y\",\"then\":\"Z\"}],\"ratified_plan\":\"<ratified-plan-id>\"}' \\\n" +
				"  --assignee worker-1\n\n" +
				"bd dep add <impl-plan-id> --blocked-by <slice-1-id>\n" +
				"```",
		},
		{
			ID:    "impl-slice-assigning",
			Title: "Assigning Workers",
			Content: "```bash\n" +
				"bd update <slice-1-id> --assignee=\"worker-1\"\n" +
				"bd update <slice-2-id> --assignee=\"worker-2\"\n" +
				"bd update <slice-3-id> --assignee=\"worker-3\"\n" +
				"```",
		},
		{
			ID:    "impl-slice-tracking",
			Title: "Tracking Progress",
			Content: "```bash\n" +
				"# Worker starts\n" +
				"bd update <slice-id> --status in_progress\n\n" +
				"# Check all slice status\n" +
				"bd list --labels=\"aura:p9-impl:s9-slice\" --status=open\n" +
				"bd list --labels=\"aura:p9-impl:s9-slice\" --status=in_progress\n\n" +
				"# Worker completes (add comment and label)\n" +
				"bd comments add <slice-id> \"COMPLETE: All checklist items verified. Production code path working.\"\n" +
				"bd label add <slice-id> aura:p9-impl:slice-complete\n" +
				"```",
		},
		{
			ID:    "impl-slice-dependencies",
			Title: "Slice Dependencies",
			Content: "Slices can have dependencies on each other (sync points):\n\n" +
				"```bash\n" +
				"# SLICE-2 depends on SLICE-1 completing first\n" +
				"bd dep add <slice-2-id> --blocked-by <slice-1-id>\n" +
				"```\n\n" +
				"Minimize inter-slice dependencies when possible.",
		},
		{
			ID:    "impl-slice-aggregation",
			Title: "Aggregation",
			Content: "The aggregation step waits for all slices to complete before code review:\n\n" +
				"```bash\n" +
				"# Check if all slices have complete label\n" +
				"bd list --labels=\"aura:p9-impl:slice-complete\"\n\n" +
				"# Compare to total slices\n" +
				"bd list --labels=\"aura:p9-impl:s9-slice\"\n" +
				"```",
		},
		{
			ID:    "impl-slice-followup",
			Title: "Follow-up Slices (FOLLOWUP_SLICE-N)",
			Content: `Follow-up slices use the same structure and tracking, with additional fields:
- **Title prefix:** ` + "`FOLLOWUP_SLICE-N:`" + ` (e.g., ` + "`FOLLOWUP_SLICE-1: Add request-id correlation`" + `)
- **Adopted leaf tasks:** Original IMPORTANT/MINOR leaf tasks from review become dual-parent children (original severity group + follow-up slice)
- **Tracking:** Same ` + "`bd list --labels=\"aura:p9-impl:s9-slice\"`" + ` queries include both regular and follow-up slices`,
		},
	},

	Recipes: []RecipeBlock{},
}
