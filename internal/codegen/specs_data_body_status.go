// Body content for the status skill SKILL.md.
// Ported from aura-plugins/skills/status/SKILL.md.
package codegen

var statusBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md)**",

	Sections: []ProseSection{
		{
			ID:    "status-steps",
			Title: "Steps",
			Subsections: []ProseSection{
				{
					ID:    "status-step1-plans",
					Title: "1. Check for active plans",
					Content: "```" + `bash
bd list --labels="aura:p3-plan:s3-propose" --status=open
bd list --labels="aura:p6-plan:s6-ratify" --status=open
` + "```",
				},
				{
					ID:    "status-step2-impl",
					Title: "2. Check implementation progress",
					Content: "```" + `bash
bd list --labels="aura:p8-impl:s8-plan" --status=open
bd list --labels="aura:p9-impl:s9-slice" --status=in_progress
bd list --labels="aura:p9-impl:s9-slice" --status=blocked
bd list --labels="aura:p9-impl:s9-slice" --status=done
` + "```",
				},
				{
					ID:    "status-step3-stats",
					Title: "3. Get project stats",
					Content: "```" + `bash
bd stats
` + "```",
				},
				{
					ID:      "status-step4-report",
					Title:   "4. Report status",
					Content: "Summarize findings across plans, implementation, and blocked tasks in the output format below.",
				},
			},
		},
		{
			ID:    "status-output-format",
			Title: "Output Format",
			Content: "```" + `
## Aura Protocol Status

**Phase:** {Phase 1: Request | Phase 3: Propose | Phase 4: Review | Phase 6: Ratified | Phase 9: Implementation}
**Active Plan:** {task-id or "None"}

### Plans
- [proposal-id] Status: {open|closed}
- [ratified-id] Status: {open|closed}

### Implementation Progress
- IMPL_PLAN: {task-id}
- Layer 1: {N}/{M} complete
- Layer 2: {N}/{M} complete (blocked: {count})

### Blocked Tasks
- {task-id}: {blocker reason}

### Recent Activity
bd list --limit=5
` + "```",
		},
		{
			ID:    "status-quick-status",
			Title: "Quick Status",
			Content: "```" + `bash
bd stats
bd ready
bd blocked
` + "```",
		},
	},
}
