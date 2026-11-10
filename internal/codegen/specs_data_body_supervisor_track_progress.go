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
			Then:      "consult the URD (`[[rec:show]] \"${URD_ID_URI}\"`) as the single source of truth",
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
[[rec:query]] --label="pasture:p9-impl:s9-slice" --status=in_progress

# Check for blocked slices
[[rec:query]] --blocked --label="pasture:p9-impl:s9-slice"

# Check specific task
[[rec:show]] "${TASK_ID_URI}"

# Check completed slices
[[rec:query]] --label="pasture:p9-impl:s9-slice" --status=closed

# Check BLOCKER severity groups (during/after review)
[[rec:query]] --label="pasture:severity:blocker" --status=open

# Check follow-up epic
[[rec:query]] --label="pasture:epic-followup"
` + "```" + ``,
		},
		{
			Id:    "sup-track-coordination",
			Title: "Tracking via Pasture",
			Content: `All coordination happens through Pasture task state and authored comments:

` + "```" + `bash
# Check for task updates
[[rec:show]] "${TASK_ID_URI}"

# Review comments for status updates
[[rec:show]] "${TASK_ID_URI}" --notes

# Add coordination notes
[[rec:note]] "${TASK_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "All slices complete — proceeding to Phase 10 (code review)"
` + "```" + ``,
		},
		{
			Id:    "sup-track-status-patterns",
			Title: "Status Patterns",
			Content: `| Status | Action |
|--------|--------|
| ` + "`" + `closed` + "`" + ` | Mark slice progress, check if all slices complete |
| Open blocked_by child | Review ` + "`" + `[[rec:show]] "${ID_URI}"` + "`" + ` for blocker details, resolve or reassign |
| ` + "`" + `in_progress` + "`" + ` | Worker is actively working |`,
		},
		{
			Id:    "sup-track-severity",
			Title: "Severity Awareness (Phase 10)",
			Content: `When tracking review progress, monitor severity groups:

| Severity | Blocks Slice? | Action |
|----------|---------------|--------|
| BLOCKER | Yes | An open BLOCKER gates the wave (dual-parent: also blocks the slice) |
| IMPORTANT | No (not via dual-parent) | Stays open past the wave gate; converges at the end (never routed to FOLLOWUP) |
| MINOR | No (not via dual-parent) | Stays open past the wave gate; converges at the end (never routed to FOLLOWUP) |`,
		},
		{
			Id:    "sup-track-followup-lifecycle",
			Title: "Follow-up Lifecycle Tracking",
			Content: `` + "```" + `bash
# Track follow-up lifecycle progress
[[rec:query]] --label="pasture:epic-followup"
[[rec:query]] --label="pasture:p2-user:s2_1-elicit" --status=open   # FOLLOWUP_URE
[[rec:query]] --label="pasture:p3-plan:s3-propose" --status=open     # FOLLOWUP_PROPOSAL
[[rec:query]] --label="pasture:p9-impl:s9-slice" --status=in_progress  # FOLLOWUP_SLICE in progress
` + "```" + ``,
		},
	},

	Recipes: []RecipeBlock{},
}
