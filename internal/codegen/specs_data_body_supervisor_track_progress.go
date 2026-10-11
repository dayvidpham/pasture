// Body content for the supervisor-track-progress skill SKILL.md.
// Ported from aura-plugins/skills/supervisor-track-progress/SKILL.md.
package codegen

var supervisorTrackProgressBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9`,

	Behaviors: []BehaviorSpec{
		{
			Id:        "sup-track-poll-rate",
			Given:     "workers running",
			When:      "monitoring",
			Then:      "check Pasture status at natural intervals (when a worker signals completion or blocker)",
			ShouldNot: "poll aggressively or busy-wait in a tight loop",
		},
		{
			Id:        "sup-track-partial-commit",
			Given:     "worker complete",
			When:      "all slices for a phase are done",
			Then:      "proceed to code review or commit",
			ShouldNot: "commit partial work — wait for all slices in the layer to complete",
		},
		{
			Id:        "sup-track-resolve-blockers",
			Given:     "worker blocked",
			When:      "handling",
			Then:      "resolve or reassign immediately",
			ShouldNot: "leave workers waiting on a blocker without action",
		},
		{
			Id:        "sup-track-urd-source-of-truth",
			Given:     "requirements question arises",
			When:      "resolving",
			Then:      "consult the URD (`pasture task show \"${URD_ID_URI}\"`) as the single source of truth",
			ShouldNot: "guess at user intent without checking the URD first",
		},
		{
			Id:        "sup-track-severity-awareness",
			Given:     "all slices complete",
			When:      "transitioning to review",
			Then:      "check for BLOCKER resolution tracking in the review severity groups",
			ShouldNot: "skip severity awareness when moving to Phase 10",
		},
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:      "sup-track-when-to-use",
			Title:   "When to Use",
			Content: "Workers spawned and running — monitoring for completions and blockers until all slices reach `closed` or a phase transition is warranted.",
		},
		{
			Id:    "sup-track-task-queries",
			Title: "Pasture Status Queries",
			Content: `` + "```" + `bash
# Check all implementation slices
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=in_progress

# Check for blocked slices
pasture task blocked --label="pasture:p9-impl:s9-slice"

# Check specific task
pasture task show "${TASK_ID_URI}"

# Check completed slices
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=closed

# Check BLOCKER severity groups (during/after review)
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:severity:blocker" --status=open

# Check follow-up epic
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:epic-followup"
` + "```" + ``,
		},
		{
			Id:    "sup-track-coordination",
			Title: "Tracking via Pasture",
			Content: `All coordination happens through Pasture task state and authored comments:

` + "```" + `bash
# Check for task updates
pasture task show "${TASK_ID_URI}"

# Review comments for status updates
pasture task comments "${TASK_ID_URI}"

# Add coordination notes
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${TASK_ID_URI}" "All slices complete — proceeding to Phase 10 (code review)"
` + "```" + ``,
		},
		{
			Id:    "sup-track-status-patterns",
			Title: "Status Patterns",
			Content: `| Status | Action |
|--------|--------|
| ` + "`" + `closed` + "`" + ` | Mark slice progress, check if all slices complete |
| Open blocked_by child | Review ` + "`" + `pasture task show "${ID_URI}"` + "`" + ` for blocker details, resolve or reassign |
| ` + "`" + `in_progress` + "`" + ` | Worker is actively working |`,
		},
		{
			Id:    "sup-track-severity",
			Title: "Severity Awareness (Phase 10)",
			Content: `When tracking review progress, monitor severity groups:

| Severity | Blocks Slice? | Action |
|----------|---------------|--------|
| BLOCKER | Yes | Must reach 0 before wave close (dual-parent: also blocks the slice) |
| IMPORTANT | No (not via dual-parent) | Must reach 0 before wave close (never routed to FOLLOWUP) |
| MINOR | No (not via dual-parent) | Must reach 0 before wave close (never routed to FOLLOWUP) |`,
		},
		{
			Id:    "sup-track-followup-lifecycle",
			Title: "Follow-up Lifecycle Tracking",
			Content: `` + "```" + `bash
# Track follow-up lifecycle progress
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:epic-followup"
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p2-user:s2_1-elicit" --status=open   # FOLLOWUP_URE
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p3-plan:s3-propose" --status=open     # FOLLOWUP_PROPOSAL
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=in_progress  # FOLLOWUP_SLICE in progress
` + "```" + ``,
		},
	},

	Recipes: []RecipeBlock{},
}
