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
			Then:      "check Beads status at natural intervals (when a worker signals completion or blocker)",
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
			Then:      "consult the URD (" + "`bd show <urd-id>`" + ") as the single source of truth",
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
		{
			Id:      "sup-track-when-to-use",
			Title:   "When to Use",
			Content: `Workers spawned and running — monitoring for completions and blockers until all slices reach ` + "`done`" + ` or a phase transition is warranted.`,
		},
		{
			Id:    "sup-track-beads-queries",
			Title: "Beads Status Queries",
			Content: "```" + `bash
# Check all implementation slices
bd list --labels="pasture:p9-impl:s9-slice" --status=in_progress

# Check for blocked slices
bd list --labels="pasture:p9-impl:s9-slice" --status=blocked

# Check specific task
bd show <task-id>

# Check completed slices
bd list --labels="pasture:p9-impl:s9-slice" --status=done

# Check BLOCKER severity groups (during/after review)
bd list --labels="pasture:severity:blocker" --status=open

# Check follow-up epic
bd list --labels="pasture:epic-followup"
` + "```",
		},
		{
			Id:    "sup-track-coordination",
			Title: "Tracking via Beads",
			Content: `All coordination happens through beads task status and comments:

` + "```" + `bash
# Check for task updates
bd show <task-id>

# Review comments for status updates
bd comments <task-id>

# Add coordination notes
bd comments add <task-id> "All slices complete — proceeding to Phase 10 (code review)"
` + "```",
		},
		{
			Id:    "sup-track-status-patterns",
			Title: "Status Patterns",
			Content: `| Status | Action |
|--------|--------|
| ` + "`done`" + ` | Mark slice progress, check if all slices complete |
| ` + "`blocked`" + ` | Review ` + "`bd show <id>`" + ` for blocker details, resolve or reassign |
| ` + "`in_progress`" + ` | Worker is actively working |`,
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
			Content: "```" + `bash
# Track follow-up lifecycle progress
bd list --labels="pasture:epic-followup"
bd list --labels="pasture:p2-user:s2_1-elicit" --status=open   # FOLLOWUP_URE
bd list --labels="pasture:p3-plan:s3-propose" --status=open     # FOLLOWUP_PROPOSAL
bd list --labels="pasture:p9-impl:s9-slice" --status=in_progress  # FOLLOWUP_SLICE in progress
` + "```",
		},
	},

	Recipes: []RecipeBlock{},
}
