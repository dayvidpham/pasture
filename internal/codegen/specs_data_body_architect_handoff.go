// Body content for the architect-handoff skill SKILL.md.
// Ported from aura-plugins/skills/architect-handoff/SKILL.md.
package codegen

import "strings"

var architectHandoffBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-7-handoff)** <- Phase 7",

	Behaviors: []BehaviorSpec{
		{
			Id:        "arch-handoff-link-proposal",
			Given:     "ratified PROPOSAL-N task",
			When:      "handing off",
			Then:      "author the handoff in a HANDOFF Pasture task body, linking to the ratified proposal",
			ShouldNot: "hand off without linking to ratified proposal",
		},
		{
			Id:        "arch-handoff-spawn-supervisor",
			Given:     "handoff for the IMPL_PLAN phase",
			When:      "spawning the supervisor",
			Then:      "delegate the supervisor as the implementation-plan owner (workers also on the frontier tier), then give it a self-contained assignment",
			ShouldNot: "spawn the supervisor without a self-contained assignment for the IMPL_PLAN phase",
		},
		{
			Id:        "arch-handoff-supervisor-not-idle",
			Given:     "a freshly spawned supervisor",
			When:      "it dispatches Explore subagents and appears idle",
			Then:      "let it work — an apparently-idle supervisor is usually running Explore subagents to map the codebase before decomposing slices",
			ShouldNot: "shut down or restart a supervisor that looks idle at the start of the IMPL_PLAN phase",
		},
		{
			Id:        "arch-handoff-no-impl-tasks",
			Given:     "implementation planning",
			When:      "handing off",
			Then:      "let supervisor create vertical slice tasks",
			ShouldNot: "create implementation tasks as architect",
		},
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		fragRef(FragWritingStandard),
		fragRef(FragWritingQuestions),
		{
			Id:      "arch-handoff-when-to-use",
			Title:   "When to Use",
			Content: "Plan ratified and user has approved proceeding with implementation.",
		},
		{
			Id:      "arch-handoff-template",
			Title:   "Handoff Template",
			Content: archHandoffTemplate(),
		},
		{
			Id:    "arch-handoff-steps",
			Title: "Steps",
			Content: `1. Create the HANDOFF Pasture task — its body IS the handoff document (use the template above):
   ` + "```" + `bash
   HANDOFF_ID_URI=$([[rec:create]] --kind task --title "HANDOFF: Architect → Supervisor for REQUEST" --phase handoff --priority 2 \
     --body="---
   ` + handoffReferenceFrontmatter("     ") + `   ---
   # Handoff: Architect → Supervisor
   <full handoff body per the template above>")
   [[rec:label]] "$HANDOFF_ID_URI" pasture:p7-plan:s7-handoff

   ` + "```" + `

2. Delegate the supervisor with the [[op:delegate_assignment]] operation (the IMPL_PLAN phase runs under the supervisor):
   ` + "```" + `
   delegate the implementation-plan owner, then give it the self-contained assignment below
   ` + "```" + `

3. Monitor supervisor progress:
   ` + "```" + `bash
   # Check pasture status
   [[rec:query]] --status=in_progress
   ` + "```" + `

   A supervisor that looks idle right after spawn is usually running Explore subagents — do **not** shut it down pre-emptively.`,
		},
		{
			Id:    "arch-handoff-example-prompt",
			Title: "Example Prompt",
			Content: `**CRITICAL:** The [[op:send_assignment_message]] assignment MUST instruct the supervisor to invoke ` + "`" + `/pasture:supervisor` + "`" + ` as its first action. Without this, the supervisor agent starts without its role instructions and skips leaf task creation, ephemeral exploration, and other critical procedures.

` + "```" + `
Start by calling ` + "`" + `[[op:invoke_skill]](/pasture:supervisor)` + "`" + ` to load your role instructions.

Implement the ratified plan for <feature name>.

## Context
- REQUEST: "${REQUEST_ID_URI}"
- URD: "${URD_ID_URI}" (read with ` + "`" + `[[rec:show]] "${URD_ID_URI}"` + "`" + ` for user requirements)
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}"
- HANDOFF: "${HANDOFF_ID_URI}" (the handoff body — read with ` + "`" + `[[rec:show]] "${HANDOFF_ID_URI}"` + "`" + `)

## Summary
<1-2 sentence summary of what needs to be implemented>

## Key Files
<list main files to be created/modified from the ratified plan>

## Acceptance Criteria
<Given/When/Then criteria from the ratified plan>

## Reminders
1. Call ` + "`" + `[[op:invoke_skill]](/pasture:supervisor)` + "`" + ` FIRST — do not proceed without loading your role
2. Spawn ephemeral Explore subagents with the [[op:delegate_assignment]] tool when codebase exploration is needed
3. Every vertical slice MUST have leaf tasks — any number, named after the real work units (the L1/L2/L3 triple is only illustrative); a slice without leaf tasks is undecomposed
4. Read the ratified plan with ` + "`" + `[[rec:show]] "${RATIFIED_PROPOSAL_ID_URI}"` + "`" + ` and the URD with ` + "`" + `[[rec:show]] "${URD_ID_URI}"` + "`" + `
` + "```" + `

Deliver this assignment to the supervisor when you delegate it:

` + "```" + `
[[op:send_assignment_message]]({
  to: "supervisor",
  message: ` + "`" + `Start by calling [[op:invoke_skill]](/pasture:supervisor) to load your role instructions.

Implement the ratified plan for User Authentication.

## Context
- REQUEST: ${REQUEST_ID_URI}
- URD: "${URD_ID_URI}"
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}"
- HANDOFF: "${HANDOFF_ID_URI}" (read with [[rec:show]] "${HANDOFF_ID_URI}")

## Summary
Add JWT-based authentication with login/logout endpoints and middleware.

## Key Files
- pkg/auth/jwt.go
- pkg/auth/middleware.go
- cmd/api/auth.go

## Acceptance Criteria
Given a valid JWT token when accessing protected routes then allow access
Given an expired token when accessing protected routes then return 401

## Reminders
1. Call [[op:invoke_skill]](/pasture:supervisor) FIRST
2. Spawn ephemeral Explore subagents with the [[op:delegate_assignment]] tool when codebase exploration is needed
3. Every slice MUST have leaf tasks (any number; L1/L2/L3 is only illustrative)
4. Read ratified plan: [[rec:show]] "${RATIFIED_PROPOSAL_ID_URI}" and URD: [[rec:show]] "${URD_ID_URI}"` + "`" + `,
  summary: "IMPL_PLAN assignment with Pasture context"
})
` + "```" + ``,
		},
		{
			Id:    "arch-handoff-delegation-notes",
			Title: "Delegation Notes",
			Content: `- Delegate the supervisor (and the workers it will coordinate) as **T1 frontier** assignments — the IMPL_PLAN phase benefits from the frontier tier for decomposition and review.
- A delegated assignment has **zero prior context**: every assignment MUST be self-contained (call ` + "`" + `[[op:invoke_skill]](/pasture:supervisor)` + "`" + `, the Pasture task IDs, and ` + "`" + `[[rec:show]]` + "`" + ` commands to fetch full requirements).
- Do not spawn the supervisor for the IMPL_PLAN phase except through this handoff.`,
		},
		{
			Id:    "arch-handoff-important",
			Title: "IMPORTANT",
			Content: "- **DO NOT** skip the handoff and spawn the supervisor for the IMPL_PLAN phase — delegate it with the [[op:delegate_assignment]] operation\n" +
				"- **DO NOT** create implementation tasks yourself - the supervisor creates vertical slice tasks\n" +
				"- **DO NOT** implement the plan yourself - your role is handoff and monitoring\n" +
				"- **DO NOT** shut down a supervisor that appears idle right after spawn — it is usually running Explore subagents\n" +
				"- The supervisor reads the ratified plan and determines vertical slice structure\n" +
				"- Architect monitors for blockers or escalations",
		},
		{
			Id:      "arch-handoff-followup-lifecycle",
			Title:   "Follow-up Lifecycle (h1 Reuse)",
			Content: "This handoff (h1: Architect → Supervisor) also occurs after FOLLOWUP_PROPOSAL is ratified. In follow-up context:\n\n- **Storage:** the follow-up handoff is authored in its own HANDOFF Pasture task body (no filesystem path)\n- **References:** keep `urd: \"${URD_ID_URI}\"` and add `followup_urd: \"${FOLLOWUP_URD_ID_URI}\"`, then read each with `\"${EPOCH_REC}\" show`\n- **Context:** Summary of FOLLOWUP_PROPOSAL ratification and the user-DEFER'd UAT items the follow-up addresses\n- **Next step:** Supervisor creates the follow-up plan and the follow-up slices for that scope",
		},
	},

	Recipes: []RecipeBlock{},
}

// archHandoffTemplate renders the handoff body from the h1 HandoffSpec: the
// bound fields become URIs with a read command, the rest become sections, and
// the create step's frontmatter uses the same Bindings. The architect skill's
// table renders from RequiredFields, so the field list has one home.
func archHandoffTemplate() string {
	spec := HandoffSpecs["h1"]
	var b strings.Builder
	b.WriteString("Use the required handoff fields in [architect](../architect/SKILL.md). ")
	b.WriteString("That skill renders the list from the handoff registry; this template renders from the same registry.\n\n")
	b.WriteString("Storage: the handoff is authored directly in the **HANDOFF Pasture task body** (no filesystem path; the task body IS the handoff).\n\n")
	b.WriteString("```markdown\n# Handoff: Architect → Supervisor\n\n")
	b.WriteString("## Supervisor Startup\n")
	b.WriteString("1. Call `[[op:invoke_skill]](/pasture:supervisor)` to load your role instructions\n")
	b.WriteString("2. Spawn ephemeral Explore subagents with the [[op:delegate_assignment]] tool when codebase exploration is needed\n")
	b.WriteString("3. Read each reference under References with the command beside it, before you plan\n")
	b.WriteString("4. Every vertical slice MUST have leaf tasks — any number, named after the real work units\n")
	b.WriteString("\n## References\n")
	for _, binding := range spec.Bindings {
		b.WriteString("- " + handoffFieldHeading(binding.Field) + ": \"${" + binding.Var + "}\" — read with `\"${EPOCH_REC}\" show \"${" + binding.Var + "}\"`\n")
	}
	bound := map[string]struct{}{}
	for _, binding := range spec.Bindings {
		bound[binding.Field] = struct{}{}
	}
	for _, field := range spec.RequiredFields {
		if _, ok := bound[field]; ok {
			continue
		}
		b.WriteString("\n## " + handoffFieldHeading(field) + "\n<" + field + ">\n")
	}
	b.WriteString("```\n")
	return b.String()
}

// handoffFieldHeading turns a registry field name into a section heading.
func handoffFieldHeading(field string) string {
	if field == "urd" {
		return "URD"
	}
	words := strings.Split(field, "-")
	for i, word := range words {
		if word == "" {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

// handoffReferenceFrontmatter renders the task body's references: block from
// HandoffSpecs["h1"].Bindings, so every create step binds exactly the fields the
// handoff registry requires and none is left unbound.
func handoffReferenceFrontmatter(indent string) string {
	var b strings.Builder
	b.WriteString("references:\n")
	for _, binding := range HandoffSpecs["h1"].Bindings {
		b.WriteString(indent + strings.ReplaceAll(binding.Field, "-", "_") + ": \"${" + binding.Var + "}\"\n")
	}
	return b.String()
}
