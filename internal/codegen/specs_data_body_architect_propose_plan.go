// Body content for the architect-propose-plan skill SKILL.md.
// Ported from aura-plugins/skills/architect-propose-plan/SKILL.md.
package codegen

var architectProposePlanBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-3-proposal-n)** <- Phase 3",

	Behaviors: []BehaviorSpec{
		{
			Id:        "arch-propose-bdd-format",
			Given:     "feature request",
			When:      "proposing",
			Then:      "use BDD Given/When/Then format with acceptance criteria",
			ShouldNot: "write vague requirements",
		},
		{
			Id:        "arch-propose-checklist-required",
			Given:     "plan",
			When:      "creating task",
			Then:      "include validation_checklist and tradeoffs in design field",
			ShouldNot: "leave checklist empty",
		},
		{
			Id:        "arch-propose-revision-history",
			Given:     "existing plan",
			When:      "revising",
			Then:      "create PROPOSAL-N+1 task and mark old as `pasture:superseded`",
			ShouldNot: "lose history",
		},
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:      "arch-propose-when-to-use",
			Title:   "When to Use",
			Content: "Starting new feature design; creating formal plan for review.",
		},
		{
			Id:    "arch-propose-naming",
			Title: "PROPOSAL-N Naming",
			Content: "Proposals are numbered incrementally: PROPOSAL-1, PROPOSAL-2, etc. Each revision increments N. " +
				"Old proposals are marked `pasture:superseded` with a comment explaining why.",
		},
		{
			Id:    "arch-propose-task-task",
			Title: "Pasture Task Creation",
			Content: `` + "```" + `bash
PROPOSAL_ID_URI=$(pasture task create "PROPOSAL-1: <feature name>" --phase propose --namespace "$PASTURE_NAMESPACE" --format json --type=feature \
  --description="$(cat <<EOF
---
references:
  request: "${REQUEST_ID_URI}"
  urd: "${URD_ID_URI}"
---

## Problem Space

**Axes of the problem:**
- Parallelism: ...
- Distribution: ...

**Has-a / Is-a:**
- X HAS-A Y
- Z IS-A W

## Engineering Tradeoffs

| Option | Pros | Cons | Decision |
|--------|------|------|----------|
| A | ... | ... | Selected |
| B | ... | ... | Rejected |

## MVP Milestone

<scope with tradeoff rationale>

## Public Interfaces

\\` + "`" + `\\` + "`" + `\\` + "`" + `go
type Example interface { /* ... */ }
\\` + "`" + `\\` + "`" + `\\` + "`" + `

## Types & Enums

\\` + "`" + `\\` + "`" + `\\` + "`" + `go
type ExampleType int

const (
    ExampleTypeA ExampleType = iota
    ExampleTypeB
)
\\` + "`" + `\\` + "`" + `\\` + "`" + `

## Validation Checklist

### Phase 1
- [ ] Item 1
- [ ] Item 2

### Phase 2
- [ ] Item 3

## BDD Acceptance Criteria

**Given** precondition
**When** action
**Then** outcome
**Should Not** negative case

## Files Affected
- pkg/path/file1.go (create)
- pkg/path/file2.go (modify)
EOF
)" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$PROPOSAL_ID_URI" pasture:p3-plan:s3-propose
pasture task update "$PROPOSAL_ID_URI" --notes "Design: '{\"validation_checklist\":[\"Item 1\",\"Item 2\",\"Item 3\"],\"tradeoffs\":[{\"decision\":\"Use A\",\"rationale\":\"Because...\"}],\"acceptance_criteria\":[{\"given\":\"X\",\"when\":\"Y\",\"then\":\"Z\",\"should_not\":\"W\"}]}'"

# Link to request
pasture task dep add "${REQUEST_ID_URI}" --blocked-by "${PROPOSAL_ID_URI}"
` + "```" + ``,
		},
		{
			Id:    "arch-propose-before-creating",
			Title: "Before Creating the Proposal",
			Content: `Read the URD and Phase 1 outputs to understand full context before drafting:
` + "```" + `bash
pasture task show "${URD_ID_URI}"
pasture task show "${REQUEST_ID_URI}"   # includes classification, research findings, explore findings as comments
` + "```" + `

The URD contains the structured requirements, priorities, design choices, and MVP goals from the URE survey. The REQUEST task comments contain Phase 1 outputs: classification (4 axes), domain research findings (prior art, standards), and codebase exploration findings (entry points, related types, dependencies). Your proposal must:
- Trace back to URD requirements
- Incorporate research findings (prior art, domain standards) into engineering tradeoffs
- Reference explore findings (entry points, existing patterns) in the files affected section`,
		},
		{
			Id:    "arch-propose-plan-structure",
			Title: "Plan Structure",
			Content: "- **Requirements Traceability: URD:** `<urd-id>`\n" +
				"- Problem Space (axes, has-a/is-a)\n" +
				"- Engineering Tradeoffs (table with decisions)\n" +
				"- MVP Milestone (scope with tradeoff rationale)\n" +
				"- Public Interfaces (Go)\n" +
				"- Types & Enums\n" +
				"- Validation Checklist (per phase)\n" +
				"- BDD Acceptance Criteria\n" +
				"- Files Affected",
		},
		{
			Id:    "arch-propose-next-steps",
			Title: "Next Steps",
			Content: "After creating PROPOSAL-N task:\n" +
				"1. Run `/pasture:architect-request-review` to spawn 3 reviewers\n" +
				"2. Wait for all 3 reviewers to vote ACCEPT\n" +
				"3. Run `/pasture:architect-ratify` to add ratify label to PROPOSAL-N",
		},
		{
			Id:    "arch-propose-followup",
			Title: "Follow-up Proposals (FOLLOWUP_PROPOSAL-N)",
			Content: "When creating proposals for a follow-up epic (received via h6 from supervisor):\n" +
				"- **Title prefix:** `FOLLOWUP_PROPOSAL-N:` (e.g., `FOLLOWUP_PROPOSAL-1: Add request-id correlation`)\n" +
				"- **References:** Include both `original_urd: <id>` and `followup_urd: <id>` in frontmatter\n" +
				"- **Content:** Address specific IMPORTANT/MINOR findings scoped in FOLLOWUP_URE/URD\n" +
				"- Same review/ratify/UAT lifecycle applies (3 reviewers, ACCEPT/REVISE, UAT, ratify, handoff)",
		},
	},

	Recipes: []RecipeBlock{},
}
