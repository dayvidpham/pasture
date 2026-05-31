// Body content for the worker-blocked skill SKILL.md.
// Ported from aura-plugins/skills/worker-blocked/SKILL.md.
package codegen

var workerBlockedBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9`,

	Behaviors: []BehaviorSpec{
		{
			ID:        "wblk-update-status",
			Given:     "a blocker",
			When:      "reporting",
			Then:      "update Beads task status and document details",
			ShouldNot: "guess or work around the blocker",
		},
		{
			ID:        "wblk-wait-for-response",
			Given:     "blocker sent",
			When:      "waiting",
			Then:      "wait for supervisor response",
			ShouldNot: "continue with incomplete info",
		},
	},

	Sections: []ProseSection{
		{
			ID:      "wblk-when-to-use",
			Title:   "When to Use",
			Content: `Cannot proceed due to missing dependency, unclear requirement, or need changes in another file.`,
		},
		{
			ID:    "wblk-steps",
			Title: "Steps",
			Content: `1. Identify what's blocking (missing type, unclear requirement, file dependency)

2. Update Beads task:
   ` + "```bash" + `
   bd update <task-id> --status=blocked
   bd update <task-id> --notes="Blocked: <reason>. Missing: <dependency or clarification needed>"
   ` + "```" + `

3. Document the blocker in the task:
   ` + "```bash" + `
   bd comments add <task-id> "BLOCKED: <reason>. Need: <dependency or clarification>"
   ` + "```" + `

4. Wait for supervisor or dependency resolution — check with ` + "`bd show <task-id>`",
		},
		{
			ID:    "wblk-common-blockers",
			Title: "Common Blockers",
			Content: `- Missing type definition from another file
- Unclear requirement in acceptance_criteria
- Need interface defined in dependent file
- Conflicting constraints in validation_checklist`,
		},
	},

	Recipes: []RecipeBlock{},
}
