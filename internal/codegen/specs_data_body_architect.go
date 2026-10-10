// Body content for the architect role SKILL.md.
package codegen

var architectBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-3-proposal-n)**`,
	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:    "arch-proposal-naming",
			Title: "PROPOSAL-N Naming",
			Content: `Proposals are numbered incrementally: PROPOSAL-1, PROPOSAL-2, etc. When a revision is needed:
1. Create PROPOSAL-N+1 with fixes
2. Mark PROPOSAL-N as superseded:
   ` + "```" + `bash
   pasture task label add "${OLD_PROPOSAL_ID_URI}" pasture:superseded
   pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${OLD_PROPOSAL_ID_URI}" "Superseded by PROPOSAL-N+1 ("${NEW_PROPOSAL_ID_URI}")"
   ` + "```" + `
3. Re-spawn all 3 reviewers to assess PROPOSAL-N+1`,
		},
		{
			Id:      "arch-state-flow",
			Title:   "State Flow",
			Content: `Idle → Eliciting → Drafting → AwaitingReview → AwaitingUAT → Ratified → HandoffToSupervisor → Idle`,
		},
		{
			Id:      "arch-task-task-creation",
			Title:   "Pasture Task Creation (12-Phase)",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:    "arch-phase1-request",
					Title: "Phase 1: REQUEST Task",
					Content: `Captures the original user prompt verbatim:
` + "```" + `bash
REQUEST_ID_URI=$(pasture task create "REQUEST: <summary>" --phase request --namespace "$PASTURE_NAMESPACE" --format json \
  --description "<verbatim user prompt - do not paraphrase>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$REQUEST_ID_URI" pasture:p1-user:s1_1-classify
` + "```" + ``,
				},
				{
					Id:    "arch-phase2-elicit",
					Title: "Phase 2: ELICIT Task",
					Content: `Run ` + "`" + `/pasture:user-elicit` + "`" + ` first, then capture results:
` + "```" + `bash
ELICIT_ID_URI=$(pasture task create "ELICIT: <feature>" --phase elicit --namespace "$PASTURE_NAMESPACE" --format json \
  --description "<questions and user responses verbatim>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$ELICIT_ID_URI" pasture:p2-user:s2_1-elicit
pasture task dep add "${REQUEST_ID_URI}" --blocked-by "${ELICIT_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "arch-phase2-5-urd",
					Title: "Phase 2.5: URD (User Requirements Document)",
					Content: `Create the URD as the single source of truth after elicitation:
` + "```" + `bash
URD_ID_URI=$(pasture task create "URD: <feature>" --phase elicit --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  request: "${REQUEST_ID_URI}"
  elicit: "${ELICIT_ID_URI}"
---
<structured requirements, priorities, design choices, MVP goals, end-vision>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$URD_ID_URI" pasture:urd
pasture task label add "$URD_ID_URI" pasture:p2-user:s2_2-urd
` + "```" + ``,
				},
				{
					Id:    "arch-phase3-proposal",
					Title: "Phase 3: PROPOSAL-N Task",
					Content: `Contains full plan with validation checklist and acceptance criteria:
` + "```" + `bash
PROPOSAL_ID_URI=$(pasture task create "PROPOSAL-1: <feature>" --phase propose --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  request: "${REQUEST_ID_URI}"
  urd: "${URD_ID_URI}"
---
<plan content in markdown>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$PROPOSAL_ID_URI" pasture:p3-plan:s3-propose
pasture task update "$PROPOSAL_ID_URI" --notes "Design: '{\"validation_checklist\":[\"item1\",\"item2\"],\"acceptance_criteria\":[{\"given\":\"X\",\"when\":\"Y\",\"then\":\"Z\"}],\"tradeoffs\":[{\"decision\":\"X\",\"rationale\":\"Y\"}]}'"
pasture task dep add "${ELICIT_ID_URI}" --blocked-by "${PROPOSAL_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "arch-phase4-review",
					Title: "Phase 4: REVIEW Tasks",
					Content: `Each reviewer creates their own task:
` + "```" + `bash
REVIEW_ID_URI=$(pasture task create "PROPOSAL-1-REVIEW-A-1: <feature>" --phase review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "VOTE: <ACCEPT|REVISE> - <justification>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$REVIEW_ID_URI" pasture:p4-plan:s4-review
pasture task dep add "${PROPOSAL_ID_URI}" --blocked-by "${REVIEW_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "arch-phase5-uat",
					Title: "Phase 5: UAT Task",
					Content: `After all 3 reviewers ACCEPT, run ` + "`" + `/pasture:user-uat` + "`" + `:
` + "```" + `bash
UAT_ID_URI=$(pasture task create "UAT-1: <feature>" --phase plan_uat --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  proposal: "${PROPOSAL_ID_URI}"
  urd: "${URD_ID_URI}"
---
<demonstrative examples and user responses>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$UAT_ID_URI" pasture:p5-user:s5-uat
pasture task dep add "${PROPOSAL_ID_URI}" --blocked-by "${UAT_ID_URI}"

# Update URD with UAT results
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${URD_ID_URI}" "UAT results: <summary of user acceptance/feedback>"
` + "```" + ``,
				},
				{
					Id:    "arch-phase6-ratify",
					Title: "Phase 6: RATIFY",
					Content: `Add label to proposal (DO NOT close, delete, or create new task):
` + "```" + `bash
pasture task label add "${PROPOSAL_ID_URI}" pasture:p6-plan:s6-ratify
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${PROPOSAL_ID_URI}" "RATIFIED: All 3 reviewers ACCEPT, UAT passed ("${UAT_ID_URI}")"

# Mark all previous proposals as superseded
pasture task label add "${OLD_PROPOSAL_ID_URI}" pasture:superseded
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${OLD_PROPOSAL_ID_URI}" "Superseded by PROPOSAL-N ("${RATIFIED_PROPOSAL_ID_URI}")"

# Update URD with ratification
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${URD_ID_URI}" "Ratified: scope confirmed as <summary>"
` + "```" + ``,
				},
				{
					Id:    "arch-phase7-handoff",
					Title: "Phase 7: HANDOFF",
					Content: `Create the HANDOFF task — its body IS the handoff document:
` + "```" + `bash
HANDOFF_ID_URI=$(pasture task create "HANDOFF: Architect → Supervisor for REQUEST" --phase handoff --namespace "$PASTURE_NAMESPACE" --format json --type=task --priority=2 \
  --description "---
references:
  request: "${REQUEST_ID_URI}"
  urd: "${URD_ID_URI}"
  proposal: "${RATIFIED_PROPOSAL_ID_URI}"
---
# Handoff: Architect → Supervisor
<full handoff body — the task body IS the handoff>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$HANDOFF_ID_URI" pasture:p7-plan:s7-handoff
` + "```" + `

Storage: the handoff is authored in this HANDOFF Pasture task body (no filesystem path).`,
				},
			},
		},
		{
			Id:      "arch-plan-structure",
			Title:   "Plan Structure",
			Content: "```markdown\n## Problem Space\n**Axes:** parallelism, distribution, reliability\n**Has-a / Is-a:** relationships\n\n## Engineering Tradeoffs\n| Option | Pros | Cons | Decision |\n\n## MVP Milestone\nScope with tradeoff rationale\n\n## Public Interfaces\n```go\ntype Example interface { /* ... */ }\n```\n\n## Validation Checklist\n- [ ] Item 1\n- [ ] Item 2\n\n## BDD Acceptance Criteria\n**Given** X **When** Y **Then** Z **Should Not** W\n```",
		},
		{
			Id:    "arch-followup-lifecycle",
			Title: "Follow-up Lifecycle (Receiving h6)",
			Content: `In the follow-up lifecycle, the architect receives a handoff (h6) from the supervisor containing FOLLOWUP_URE + FOLLOWUP_URD, and creates FOLLOWUP_PROPOSAL-N:

` + "```" + `bash
# After receiving h6 from supervisor:
FOLLOWUP_PROPOSAL_ID_URI=$(pasture task create "FOLLOWUP_PROPOSAL-1: <follow-up feature>" --phase propose --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  request: "${ORIGINAL_REQUEST_ID_URI}"
  original_urd: "${ORIGINAL_URD_ID_URI}"
  followup_urd: "${FOLLOWUP_URD_ID_URI}"
  followup_epic: "${FOLLOWUP_EPIC_ID_URI}"
---
<proposal content addressing the scoped user-DEFER'd UAT items>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$FOLLOWUP_PROPOSAL_ID_URI" pasture:p3-plan:s3-propose
` + "```" + `

The same review/ratify/UAT/handoff cycle (Phases 3-7) applies. After FOLLOWUP_PROPOSAL is ratified, hand off to supervisor via h1 for FOLLOWUP_IMPL_PLAN creation.`,
		},
		{
			Id:      "arch-spawning-reviewers",
			Title:   "Spawning Reviewers",
			Content: "Spawn 3 axis-specific reviewers (A=Correctness, B=Test quality, C=Elegance) as `general-purpose` subagents. Each reviewer must invoke the `/pasture:reviewer` skill (via the Skill tool) to load its role instructions — `/pasture:reviewer` is a **Skill**, not a subagent type.\n\n```\nTask(description: \"Reviewer A: correctness\", prompt: \"You are Reviewer A (Correctness). First invoke `/pasture:reviewer` to load your role. Then review PROPOSAL-1 task <id>. URD: <urd-id>...\", subagent_type: \"general-purpose\")\nTask(description: \"Reviewer B: test quality\", prompt: \"You are Reviewer B (Test quality). First invoke `/pasture:reviewer` to load your role. Then review PROPOSAL-1 task <id>. URD: <urd-id>...\", subagent_type: \"general-purpose\")\nTask(description: \"Reviewer C: elegance\", prompt: \"You are Reviewer C (Elegance). First invoke `/pasture:reviewer` to load your role. Then review PROPOSAL-1 task <id>. URD: <urd-id>...\", subagent_type: \"general-purpose\")\n```",
		},
		{
			Id:      "arch-supervisor-handoff",
			Title:   "Supervisor Handoff",
			Content: "**DO NOT** spawn the supervisor as a Task tool subagent or via `aura-swarm` for the IMPL_PLAN phase. Instead, invoke:\n\n```\nSkill(skill: \"pasture:architect-handoff\")\n```\n\nThe handoff skill guides you through:\n1. Authoring the handoff in a HANDOFF Pasture task body (no filesystem path)\n2. Launching the supervisor (and workers) as **Opus** teammates via TeamCreate, then assigning work via SendMessage\n\n**CRITICAL:** The supervisor assignment MUST:\n1. **Start with `Skill(/pasture:supervisor)`** — this loads the supervisor's role instructions, including leaf task creation\n2. Include all Pasture task IDs (REQUEST, URD, RATIFIED PROPOSAL, HANDOFF)\n3. Reference the HANDOFF Pasture task ID — the handoff is in that task body\n\nA supervisor that appears idle right after spawn is usually running Explore subagents — do **not** shut it down pre-emptively.\n\n**DO NOT** create implementation tasks yourself - the supervisor creates vertical slice tasks from the ratified plan.",
		},
	},
	Behaviors: []BehaviorSpec{
		{
			Id:        "arch-followup-h6",
			Given:     "h6 handoff received (FOLLOWUP_URE + FOLLOWUP_URD)",
			When:      "starting follow-up proposal",
			Then:      "create FOLLOWUP_PROPOSAL-N referencing both original URD and FOLLOWUP_URD",
			ShouldNot: "create FOLLOWUP_PROPOSAL without reading the original URD",
		},
	},
}
