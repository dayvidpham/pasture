// Body content for the architect role SKILL.md.
package codegen

var architectBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-3-proposal-n)**`,
	Sections: []ProseSection{
		{
			Id:    "arch-proposal-naming",
			Title: "PROPOSAL-N Naming",
			Content: `Proposals are numbered incrementally: PROPOSAL-1, PROPOSAL-2, etc. When a revision is needed:
1. Create PROPOSAL-N+1 with fixes
2. Mark PROPOSAL-N as superseded:
   ` + "```bash" + `
   bd label add <old-proposal-id> pasture:superseded
   bd comments add <old-proposal-id> "Superseded by PROPOSAL-N+1 (<new-proposal-id>)"
   ` + "```" + `
3. Re-spawn all 3 reviewers to assess PROPOSAL-N+1`,
		},
		{
			Id:      "arch-state-flow",
			Title:   "State Flow",
			Content: `Idle → Eliciting → Drafting → AwaitingReview → AwaitingUAT → Ratified → HandoffToSupervisor → Idle`,
		},
		{
			Id:      "arch-beads-task-creation",
			Title:   "Beads Task Creation (12-Phase)",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:    "arch-phase1-request",
					Title: "Phase 1: REQUEST Task",
					Content: `Captures the original user prompt verbatim:
` + "```bash" + `
bd create --labels "pasture:p1-user:s1_1-classify" \
  --title "REQUEST: <summary>" \
  --description "<verbatim user prompt - do not paraphrase>"
# Result: task-req
` + "```",
				},
				{
					Id:    "arch-phase2-elicit",
					Title: "Phase 2: ELICIT Task",
					Content: `Run ` + "`/pasture:user-elicit`" + ` first, then capture results:
` + "```bash" + `
bd create --labels "pasture:p2-user:s2_1-elicit" \
  --title "ELICIT: <feature>" \
  --description "<questions and user responses verbatim>"
bd dep add <request-id> --blocked-by <elicit-id>
# Result: task-eli
` + "```",
				},
				{
					Id:    "arch-phase2-5-urd",
					Title: "Phase 2.5: URD (User Requirements Document)",
					Content: `Create the URD as the single source of truth after elicitation:
` + "```bash" + `
bd create --labels "pasture:urd,pasture:p2-user:s2_2-urd" \
  --title "URD: <feature>" \
  --description "---
references:
  request: <request-id>
  elicit: <elicit-id>
---
<structured requirements, priorities, design choices, MVP goals, end-vision>"
# Result: task-urd
` + "```",
				},
				{
					Id:    "arch-phase3-proposal",
					Title: "Phase 3: PROPOSAL-N Task",
					Content: `Contains full plan with validation checklist and acceptance criteria:
` + "```bash" + `
bd create --labels "pasture:p3-plan:s3-propose" \
  --title "PROPOSAL-1: <feature>" \
  --description "---
references:
  request: <request-id>
  urd: <urd-id>
---
<plan content in markdown>" \
  --design='{"validation_checklist":["item1","item2"],"acceptance_criteria":[{"given":"X","when":"Y","then":"Z"}],"tradeoffs":[{"decision":"X","rationale":"Y"}]}'
bd dep add <request-id> --blocked-by <proposal-id>
# Result: task-prop
` + "```",
				},
				{
					Id:    "arch-phase4-review",
					Title: "Phase 4: REVIEW Tasks",
					Content: `Each reviewer creates their own task:
` + "```bash" + `
bd create --labels "pasture:p4-plan:s4-review" \
  --title "PROPOSAL-1-REVIEW-A-1: <feature>" \
  --description "VOTE: <ACCEPT|REVISE> - <justification>"
bd dep add <proposal-id> --blocked-by <review-id>
` + "```",
				},
				{
					Id:    "arch-phase5-uat",
					Title: "Phase 5: UAT Task",
					Content: `After all 3 reviewers ACCEPT, run ` + "`/pasture:user-uat`" + `:
` + "```bash" + `
bd create --labels "pasture:p5-user:s5-uat" \
  --title "UAT-1: <feature>" \
  --description "---
references:
  proposal: <proposal-id>
  urd: <urd-id>
---
<demonstrative examples and user responses>"
bd dep add <proposal-id> --blocked-by <uat-id>

# Update URD with UAT results
bd comments add <urd-id> "UAT results: <summary of user acceptance/feedback>"
` + "```",
				},
				{
					Id:    "arch-phase6-ratify",
					Title: "Phase 6: RATIFY",
					Content: `Add label to proposal (DO NOT close, delete, or create new task):
` + "```bash" + `
bd label add <proposal-id> pasture:p6-plan:s6-ratify
bd comments add <proposal-id> "RATIFIED: All 3 reviewers ACCEPT, UAT passed (<uat-task-id>)"

# Mark all previous proposals as superseded
bd label add <old-proposal-id> pasture:superseded
bd comments add <old-proposal-id> "Superseded by PROPOSAL-N (<ratified-proposal-id>)"

# Update URD with ratification
bd comments add <urd-id> "Ratified: scope confirmed as <summary>"
` + "```",
				},
				{
					Id:    "arch-phase7-handoff",
					Title: "Phase 7: HANDOFF",
					Content: `Create the HANDOFF task — its body IS the handoff document:
` + "```bash" + `
bd create --type=task --priority=2 \
  --title "HANDOFF: Architect → Supervisor for REQUEST" \
  --description "---
references:
  request: <request-id>
  urd: <urd-id>
  proposal: <ratified-proposal-id>
---
# Handoff: Architect → Supervisor
<full handoff body — the task body IS the handoff>" \
  --add-label "pasture:p7-plan:s7-handoff"
` + "```" + `

Storage: the handoff is authored in this HANDOFF Beads task body (no filesystem path).`,
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

` + "```bash" + `
# After receiving h6 from supervisor:
bd create --labels "pasture:p3-plan:s3-propose" \
  --title "FOLLOWUP_PROPOSAL-1: <follow-up feature>" \
  --description "---
references:
  request: <original-request-id>
  original_urd: <original-urd-id>
  followup_urd: <followup-urd-id>
  followup_epic: <followup-epic-id>
---
<proposal content addressing the scoped user-DEFER'd UAT items>"
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
			Content: "**DO NOT** spawn the supervisor as a Task tool subagent or via `aura-swarm` for the IMPL_PLAN phase. Instead, invoke:\n\n```\nSkill(skill: \"pasture:architect-handoff\")\n```\n\nThe handoff skill guides you through:\n1. Authoring the handoff in a HANDOFF Beads task body (no filesystem path)\n2. Launching the supervisor (and workers) as **Opus** teammates via TeamCreate, then assigning work via SendMessage\n\n**CRITICAL:** The supervisor assignment MUST:\n1. **Start with `Skill(/pasture:supervisor)`** — this loads the supervisor's role instructions, including leaf task creation\n2. Include all Beads task IDs (REQUEST, URD, RATIFIED PROPOSAL, HANDOFF)\n3. Reference the HANDOFF Beads task ID — the handoff is in that task body\n\nA supervisor that appears idle right after spawn is usually running Explore subagents — do **not** shut it down pre-emptively.\n\n**DO NOT** create implementation tasks yourself - the supervisor creates vertical slice tasks from the ratified plan.",
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
