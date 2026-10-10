// Body content for the supervisor role SKILL.md.
package codegen

var supervisorBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-8-implementation-plan)** <- Phases 7-12`,

	Behaviors: []BehaviorSpec{
		{
			Id:        "sup-assign-slices",
			Given:     "slices created",
			When:      "assigning",
			Then:      "transfer an existing owner-responsibility assignment only after resolving its slot, successor assignment ID, registered committing actor and registered worker occupant; unassigned task allocation remains with the supervisor",
			ShouldNot: "leave slices unassigned",
		},
		{
			Id:        "sup-spawn-workers",
			Given:     "worker assignments",
			When:      "spawning",
			Then:      "use Task tool with `subagent_type: \"general-purpose\"` and `run_in_background: true`, worker MUST call `Skill(/pasture:worker)` at start",
			ShouldNot: "spawn workers sequentially or use specialized agent types",
		},
		{
			Id:        "sup-teamcreate-msg",
			Given:     "teammates spawned via TeamCreate",
			When:      "assigning work via SendMessage",
			Then:      "the message MUST include: (1) explicit instruction to call `Skill(/pasture:worker)`, (2) the Pasture task ID, (3) instruction to run `pasture task show \"${TASK_ID_URI}\"` for full context, and (4) the handoff authored in the Pasture task body",
			ShouldNot: "send bare instructions without Pasture context — teammates have no prior knowledge of the task",
		},
		{
			Id:        "sup-layer-integration-points",
			Given:     "multiple vertical slices",
			When:      "slices share types, interfaces, or data flows",
			Then:      "identify horizontal Layer Integration Points and document them in the IMPL_PLAN (owner, consumers, shared contract, merge timing)",
			ShouldNot: "leave cross-slice dependencies implicit — divergence grows when slices develop in isolation without clear merge points",
		},
		{
			Id:        "sup-followup-deps",
			Given:     "IMPORTANT or MINOR severity groups",
			When:      "linking dependencies",
			Then:      "wire each group to its review round only: `pasture task dep add \"${REVIEW_ROUND_ID_URI}\" --blocked-by \"${IMPORTANT_GROUP_ID_URI}\"` — ALL severity groups must reach 0 before the wave closes",
			ShouldNot: "route IMPORTANT or MINOR severity groups to the FOLLOWUP epic, or wire them as blocking IMPL_PLAN/any slice — only BLOCKER findings block slices, and the FOLLOWUP epic is fed solely by user-DEFER'd UAT items",
		},
		behaviorRef(FragSupReviewAllSlices),
		behaviorRef(FragSupReviewCheckEach),
		behaviorRef(FragSupReviewSeverityGroups),
		behaviorRef(FragSupBlockerDualParent),
		behaviorRef(FragSupDeferredFollowup),
		behaviorRef(FragSupFollowupEpicTiming),
		{
			Id:        "sup-worker-persistence",
			Given:     "worker completes initial implementation",
			When:      "deciding whether to shut down the worker",
			Then:      "keep workers alive for the review-fix cycle; workers notify supervisor via pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 but do NOT shut down",
			ShouldNot: "shut down workers after first implementation pass; workers must stay alive to fix BLOCKERs and IMPORTANT findings",
		},
		{
			Id:        "sup-autonomous-progression",
			Given:     "non-user-gated phase completes",
			When:      "transitioning to next phase",
			Then:      "proceed autonomously without asking permission; the 5 user-gated phases are: Phase 1 s1_1 (research depth), Phase 2 (URE), Phase 5 (Plan UAT), Phase 8 (implementation-effort / review-effort budget request), Phase 11 (Impl UAT); all other phase transitions (9 SLICES, 10 CODE REVIEW, 12 LANDING) progress automatically",
			ShouldNot: "ask 'Should I proceed?' for autonomous phases; add user gates beyond the 5 defined; only pause for user-facing phases that require human input",
		},
		// R7/A1: code review iterates up to the chosen review-effort budget until
		// 0/0/0 clean; on exhaustion, surface outstanding findings to the user.
		// Resolves to SharedFragmentSpecs[FragReviewCleanExit] (SLICE-1).
		behaviorRef(FragReviewCleanExit),
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:    "sup-first-steps",
			Title: "First Steps",
			Content: `The architect creates a placeholder IMPL_PLAN task. Your first job is to fill it in:

1. Read the RATIFIED_PLAN and the **URD** to understand the full scope, user requirements, and **identify production code paths**
   ` + "```" + `bash
   pasture task show "${RATIFIED_PLAN_ID_URI}"
   pasture task show "${URD_ID_URI}"
   ` + "```" + `
2. **Explore the codebase** using ephemeral Explore subagents (see [Exploration](#exploration-ephemeral-explore-subagents) below) — spawn scoped Explore subagents for codebase queries before decomposing into slices.
3. **Prefer vertical slice decomposition** (feature ownership end-to-end) when possible:
   - Vertical slice: Worker owns full feature (types → tests → impl → CLI/API wiring)
   - Horizontal layers: Use when shared infrastructure exists (common types, utilities)
4. Determine layer structure following TDD principles:
   - Layer 1: Types, interfaces, schemas (no deps)
   - Layer 2: Tests for public interfaces (tests first!)
   - Layer 3: Implementation (make tests pass)
   - Layer 4: Integration tests (if needed)
5. **Identify horizontal Layer Integration Points** where slices must inter-op — document in IMPL_PLAN (see [supervisor-plan-tasks](../supervisor-plan-tasks/SKILL.md) step 5)
6. **Create leaf tasks for every slice** (see [Step 3](#step-3-create-leaf-tasks-within-each-slice-critical)) — a slice without leaf tasks is undecomposed and cannot be tracked
7. Bind SLICE_1_ID_URI and SLICE_2_ID_URI from the corresponding slice create outputs first. Update the IMPL_PLAN with the layer breakdown + integration points:
   ` + "```" + `bash
   pasture task update "${IMPL_PLAN_ID_URI}" --description="$(cat <<EOF
   ---
   references:
     request: "${REQUEST_ID_URI}"
     urd: "${URD_ID_URI}"
     proposal: "${RATIFIED_PROPOSAL_ID_URI}"
   ---
   ## Layer Structure (TDD)

   ### Vertical Slices (Preferred)
   - SLICE-1: Feature X command (Worker A owns types → tests → impl → CLI wiring)
   - SLICE-2: Feature Y endpoint (Worker B owns types → tests → impl → API wiring)

   OR

   ### Horizontal Layers (If shared infrastructure)
   - Layer 1: types.go, interfaces.go (no deps)
   - Layer 2: service_test.go (tests first, depend on L1)
   - Layer 3: service.go (implementation, make tests pass)
   - Layer 4: integration_test.go (depends on L3)

   ## Tasks
   - "${SLICE_1_ID_URI}": SLICE-1 ...
   - "${SLICE_2_ID_URI}": SLICE-2 ...
   ...
   EOF
   )"
   ` + "```" + `

See: [../supervisor-plan-tasks/SKILL.md](../supervisor-plan-tasks/SKILL.md) for detailed vertical slice decomposition guidance.`,
		},
		{
			Id:    "sup-exploration",
			Title: "Exploration (Ephemeral Explore Subagents)",
			Content: `Per [C-supervisor-explore-ephemeral], spawn ephemeral Explore subagents (Agent tool, ` + "`subagent_type=Explore`" + `) for scoped codebase queries. These are short-lived — they explore, return findings, and terminate. The supervisor stays lean.

` + "```" + `
// Explore subagent — ephemeral, scoped query
Task({
  subagent_type: "Explore",
  run_in_background: true,
  prompt: ` + "`" + `Call Skill(/pasture:explore) to load your exploration role.

Query: <specific codebase question>
Depth: standard-research

Explore the codebase for the requested topic. Produce structured findings
(entry points, data flow, dependencies, patterns, conflicts). Return findings.` + "`" + `
})
` + "```" + `

Spawn as many Explore subagents as needed — they are cheap and disposable. Use them during Phase 8 (IMPL_PLAN) to understand codebase areas before decomposing into slices.`,
		},
		{
			Id:    "sup-reading-from-task",
			Title: "Reading from Pasture",
			Content: `Get the ratified plan and URD:
` + "```" + `bash
pasture task show "${RATIFIED_PLAN_ID_URI}"
pasture task show "${URD_ID_URI}"
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p6-plan:s6-ratify" --status=open
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:urd"
` + "```" + ``,
		},
		{
			Id:      "sup-impl-task-structure",
			Title:   "Implementation Task Structure",
			Content: "```go\ntype ImplementationTask struct {\n    File            string          // file path\n    TaskId          string          // Pasture task ID (e.g., \"${TASK_A_URI}\")\n    RequirementRef  string\n    Prompt          string\n    Context         struct {\n        RelatedFiles    []struct{ File, Summary string }\n        TaskDescription string\n    }\n    Status          string          // \"Pending\" | \"Claimed\" | \"Complete\" | \"Failed\"\n    // Pasture fields:\n    ValidationChecklist []string              // Items from RATIFIED_PLAN\n    AcceptanceCriteria  []AcceptanceCriterion // {Given, When, Then, ShouldNot}\n    Tradeoffs           []Tradeoff           // {Decision, Rationale}\n    RatifiedPlan        string               // Link to RATIFIED_PLAN task ID\n}\n```",
		},
		{
			Id:      "sup-creating-vertical-slices",
			Title:   "Creating Vertical Slices (Phase 8)",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:    "sup-step1-impl-plan",
					Title: "Step 1: Create the IMPL_PLAN task",
					Content: `` + "```" + `bash
IMPL_PLAN_ID_URI=$(pasture task create "IMPL_PLAN: <feature>" --phase impl_plan --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  request: "${REQUEST_ID_URI}"
  urd: "${URD_ID_URI}"
  proposal: "${RATIFIED_PROPOSAL_ID_URI}"
---
## Horizontal Layers
- L1: Types and schemas
- L2: Tests (import production code)
- L3: Implementation + wiring

## Vertical Slices
- SLICE-1: <description> (files: ...)
- SLICE-2: <description> (files: ...)" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$IMPL_PLAN_ID_URI" pasture:p8-impl:s8-plan
pasture task dep add "${REQUEST_ID_URI}" --blocked-by "${IMPL_PLAN_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "sup-step2-create-slices",
					Title: "Step 2: Create each slice",
					Content: `` + "```" + `bash
SLICE_1_ID_URI=$(pasture task create "SLICE-1: <slice name>" --phase worker_slices --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  impl_plan: "${IMPL_PLAN_ID_URI}"
  urd: "${URD_ID_URI}"
---
## Specification
<detailed spec from ratified plan>

## Files Owned
<list of files>

## Leaf Tasks
- SLICE-1-L1: Types and interfaces
- SLICE-1-L2: Tests (import production code)
- SLICE-1-L3: Implementation + wiring

## Validation Checklist
- [ ] Types defined
- [ ] Tests written (import production code)
- [ ] Implementation complete
- [ ] Production path verified" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$SLICE_1_ID_URI" pasture:p9-impl:s9-slice
pasture task update "$SLICE_1_ID_URI" --notes "Design: '{\"validation_checklist\":[\"Types defined\",\"Tests written (import production code)\",\"Implementation complete\",\"Production path verified\"],\"acceptance_criteria\":[{\"given\":\"X\",\"when\":\"Y\",\"then\":\"Z\"}],\"ratified_plan\":\"${RATIFIED_PLAN_ID_URI}\"}'"
pasture task dep add "${IMPL_PLAN_ID_URI}" --blocked-by "${SLICE_1_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "sup-step3-leaf-tasks",
					Title: "Step 3: Create leaf tasks within each slice (CRITICAL)",
					Content: `Per [C-slice-leaf-tasks], create Pasture tasks for each implementation unit within the slice, then chain them as dependencies. Leaf tasks are what workers actually implement.

` + "```" + `bash
# L1: Types and interfaces for this slice
LEAF_L1=$(pasture task create "SLICE-1-L1: Types — <slice name>" --phase worker_slices --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_1_ID_URI}"
  impl_plan: "${IMPL_PLAN_ID_URI}"
  urd: "${URD_ID_URI}"
---
## Scope
Define types, interfaces, and schemas for this slice.

## Files Owned
- <file-path-1>
- <file-path-2>

## Acceptance Criteria
Given <context> when <action> then <outcome> should never <anti-pattern>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$LEAF_L1" pasture:p9-impl:s9-slice
pasture task dep add "${SLICE_1_ID_URI}" --blocked-by "$LEAF_L1"

# L2: Tests (import production code, will fail until L3)
LEAF_L2=$(pasture task create "SLICE-1-L2: Tests — <slice name>" --phase worker_slices --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_1_ID_URI}"
  impl_plan: "${IMPL_PLAN_ID_URI}"
---
## Scope
Write tests that import from production code paths. Tests MUST fail until L3.

## Files Owned
- <test-file-path-1>

## Acceptance Criteria
Given <context> when <action> then <outcome> should never <anti-pattern>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$LEAF_L2" pasture:p9-impl:s9-slice
pasture task dep add "${SLICE_1_ID_URI}" --blocked-by "$LEAF_L2"
# L2 depends on L1 types being defined first
pasture task dep add "$LEAF_L2" --blocked-by "$LEAF_L1"

# L3: Implementation (makes tests pass)
LEAF_L3=$(pasture task create "SLICE-1-L3: Impl — <slice name>" --phase worker_slices --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_1_ID_URI}"
  impl_plan: "${IMPL_PLAN_ID_URI}"
---
## Scope
Implement production code to make L2 tests pass.

## Files Owned
- <impl-file-path-1>

## Acceptance Criteria
Given <context> when <action> then <outcome> should never <anti-pattern>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$LEAF_L3" pasture:p9-impl:s9-slice
pasture task dep add "${SLICE_1_ID_URI}" --blocked-by "$LEAF_L3"
# L3 depends on L2 tests existing first
pasture task dep add "$LEAF_L3" --blocked-by "$LEAF_L2"
` + "```" + `

The resulting tree per slice:

` + "```" + `
IMPL_PLAN
  └── blocked by SLICE-1
        ├── blocked by SLICE-1-L1: Types
        ├── blocked by SLICE-1-L2: Tests (blocked by L1)
        └── blocked by SLICE-1-L3: Impl  (blocked by L2)
` + "```" + `

Workers are assigned to leaf tasks, not slices. The slice closes when all its leaf tasks close.`,
				},
			},
		},
		{
			Id:    "sup-assigning-slices",
			Title: "Assigning Slices",
			Content: `` + "```" + `bash
# Assign slices to workers
pasture task assignment transfer "${SLICE_1_ID_URI}" --slot owner-responsibility --assignment "${SUCCESSOR_ASSIGNMENT_1_ID}" --actor pasture-system--00000000-0000-0000-0000-000000000000 --occupant "${REGISTERED_WORKER_ACTOR_1}"
pasture task assignment transfer "${SLICE_2_ID_URI}" --slot owner-responsibility --assignment "${SUCCESSOR_ASSIGNMENT_2_ID}" --actor pasture-system--00000000-0000-0000-0000-000000000000 --occupant "${REGISTERED_WORKER_ACTOR_2}"
pasture task assignment transfer "${SLICE_3_ID_URI}" --slot owner-responsibility --assignment "${SUCCESSOR_ASSIGNMENT_3_ID}" --actor pasture-system--00000000-0000-0000-0000-000000000000 --occupant "${REGISTERED_WORKER_ACTOR_3}"
` + "```" + ``,
		},
		{
			Id:      "sup-spawning-workers",
			Title:   "Spawning Workers",
			Content: "Per [C-supervisor-no-impl], all implementation work — no matter how small — is delegated to a worker agent. The supervisor's job is coordination, tracking, and quality control.\n\nWorkers are **general-purpose agents** that call `/pasture:worker` at the start. Select the model based on task complexity:\n\n```\n// Non-trivial work → sonnet model\nTask({\n  subagent_type: \"general-purpose\",\n  model: \"sonnet\",\n  run_in_background: true,\n  prompt: `Call Skill(/pasture:worker) and implement the assigned slice.\\n\\nPasture Task ID: ${taskId}...`\n})\n\n// Trivial work (config tweak, typo fix, single-file edit) → haiku model\nTask({\n  subagent_type: \"general-purpose\",\n  model: \"haiku\",\n  run_in_background: true,\n  prompt: `Call Skill(/pasture:worker) and fix the typo in...\\n\\nPasture Task ID: ${taskId}...`\n})\n\n// WRONG: Supervisor implementing changes directly\nEdit({ file_path: \"src/foo.ts\", ... })  // Supervisors coordinate, they don't implement!\n\n// WRONG: Do not use specialized agent types like \"pasture:worker\" directly\nTask({\n  subagent_type: \"pasture:worker\",  // This doesn't exist!\n  ...\n})\n```",
			Subsections: []ProseSection{
				{
					Id:      "sup-model-selection",
					Title:   "Model Selection Guide",
					Content: "| Complexity | Model | Examples |\n|------------|-------|----------|\n| Trivial | `haiku` | Single-file edit, config change, typo fix, renaming, adding a label |\n| Non-trivial | `sonnet` | Multi-file changes, new features, architectural work, complex logic, test suites |\n\n**Handoff:** Before spawning each worker, author its handoff in the slice (or a dedicated handoff) Pasture task body — the task body IS the handoff (no filesystem path).\n\nSee: [../supervisor-spawn-worker/SKILL.md](../supervisor-spawn-worker/SKILL.md) for handoff template.",
				},
				{
					Id:    "sup-teamcreate-context",
					Title: "TeamCreate Context Requirements",
					Content: `When using TeamCreate instead of the Task tool, teammates have **zero prior context**. Every SendMessage assigning work MUST be self-contained:

` + "```" + `
SendMessage({
  type: "message",
  recipient: "worker-1",
  content: ` + "`" + `You are assigned SLICE-1. Start by calling Skill(/pasture:worker).

Your Pasture task ID: "${SLICE_TASK_ID_URI}"
Run this to get full requirements + handoff: pasture task show "${SLICE_TASK_ID_URI}"

Key context:
- Request: "${REQUEST_ID_URI}" (run: pasture task show "${REQUEST_ID_URI}")
- URD: "${URD_ID_URI}" (run: pasture task show "${URD_ID_URI}")
- IMPL_PLAN: "${IMPL_PLAN_ID_URI}" (run: pasture task show "${IMPL_PLAN_ID_URI}")

Read the handoff doc and your Pasture task before starting implementation.` + "`" + `,
  summary: "SLICE-1 assignment with Pasture context"
})
` + "```" + `

Per [sup-teamcreate-msg], every assignment must include actionable ` + "`" + `pasture task show` + "`" + ` commands. Teammates cannot see your conversation history, the Pasture task tree, or any prior context.

The worker skill provides:
- File ownership validation
- Standard DI patterns
- Completion/blocked signaling via Pasture`,
				},
			},
		},
		{
			Id:      "sup-epic-followup",
			Title:   "EPIC_FOLLOWUP Creation (Phase 5/11)",
			Content: `After UAT, if the user **DEFER'd** one or more items, create a follow-up epic from those DEFER'd items. Per [frag--sup-followup-epic-timing], create immediately after UAT completes. Review severities (BLOCKER/IMPORTANT/MINOR) are **never** routed here — they must all reach 0 before the review wave closes.`,
			Subsections: []ProseSection{
				{
					Id:    "sup-followup-step1",
					Title: "Step 1: Create follow-up epic",
					Content: `` + "```" + `bash
FOLLOWUP_EPIC_ID_URI=$(pasture task create "FOLLOWUP: User-deferred improvements from UAT" --phase unscoped --namespace "$PASTURE_NAMESPACE" --format json --type=epic --priority=3 \
  --description="---
references:
  request: "${REQUEST_ID_URI}"
  urd: "${URD_ID_URI}"
  uat: ${UAT_ID_URI}
---
Aggregated user-DEFER'd items from UAT (Phase 5/11)." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$FOLLOWUP_EPIC_ID_URI" pasture:epic-followup

# Link the DEFER'd UAT items as children of the follow-up epic
pasture task dep add "${FOLLOWUP_EPIC_ID_URI}" --blocked-by "${DEFERRED_ITEM_ID_1_URI}"
pasture task dep add "${FOLLOWUP_EPIC_ID_URI}" --blocked-by "${DEFERRED_ITEM_ID_2_URI}"
` + "```" + `

Severity routing follows [frag--sup-blocker-dual-parent] and [frag--sup-deferred-followup]: all review severities reach 0; the FOLLOWUP epic is DEFER-fed only.`,
				},
				{
					Id:    "sup-followup-step2",
					Title: "Step 2: Follow-up lifecycle (same protocol, FOLLOWUP_* prefix)",
					Content: `The follow-up epic runs the same protocol phases with FOLLOWUP_* prefixed task types. The supervisor creates the initial lifecycle tasks:

` + "```" + `
FOLLOWUP epic (pasture:epic-followup)
  ├── frontmatter reference: original URD
  ├── frontmatter reference: original REVIEW-A/B/C tasks
  └── blocked-by: FOLLOWUP_URE         (Phase 2: scope which DEFER'd items to address)
        └── blocked-by: FOLLOWUP_URD   (Phase 2: requirements for follow-up)
              └── blocked-by: FOLLOWUP_PROPOSAL-1  (Phase 3: proposal for follow-up)
                    └── blocked-by: FOLLOWUP_IMPL_PLAN  (Phase 8: decompose into slices)
                          ├── blocked-by: FOLLOWUP_SLICE-1  (Phase 9)
                          │     ├── blocked-by: deferred-item-leaf-task-...
                          │     └── blocked-by: deferred-item-leaf-task-...
                          └── blocked-by: FOLLOWUP_SLICE-2
` + "```" + `

` + "```" + `bash
# Create FOLLOWUP_URE — user scoping which findings to address
FOLLOWUP_URE_ID=$(pasture task create "FOLLOWUP_URE: Scope follow-up for <feature>" --phase elicit --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  followup_epic: "${FOLLOWUP_EPIC_ID_URI}"
  original_urd: "${ORIGINAL_URD_ID_URI}"
---
Scoping URE: determine which user-DEFER'd UAT items to address." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$FOLLOWUP_URE_ID" pasture:p2-user:s2_1-elicit
pasture task dep add "${FOLLOWUP_EPIC_ID_URI}" --blocked-by "$FOLLOWUP_URE_ID"

# Create FOLLOWUP_URD — requirements for follow-up scope
FOLLOWUP_URD_ID=$(pasture task create "FOLLOWUP_URD: Requirements for <feature> follow-up" --phase elicit --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  followup_epic: "${FOLLOWUP_EPIC_ID_URI}"
  original_urd: "${ORIGINAL_URD_ID_URI}"
---
Follow-up requirements. References original URD." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$FOLLOWUP_URD_ID" pasture:p2-user:s2_2-urd
pasture task label add "$FOLLOWUP_URD_ID" pasture:urd
pasture task dep add "$FOLLOWUP_URE_ID" --blocked-by "$FOLLOWUP_URD_ID"
` + "```" + `

The remaining lifecycle tasks (FOLLOWUP_PROPOSAL, FOLLOWUP_IMPL_PLAN, FOLLOWUP_SLICE) are created as the follow-up epic progresses through the protocol phases.`,
				},
				{
					Id:    "sup-followup-step3",
					Title: "Step 3: DEFER'd-item leaf adoption (dual-parent)",
					Content: `When the supervisor creates FOLLOWUP_SLICE-N tasks during the follow-up implementation phase, the user-DEFER'd UAT-item leaf tasks gain a second parent (dual-parent: leaf blocks BOTH the DEFER'd-items tracking group AND the follow-up slice):

` + "```" + `bash
# Leaf task gets dual-parent: DEFER'd-items tracking group + follow-up slice
pasture task dep add "${FOLLOWUP_SLICE_ID_URI}" --blocked-by "${DEFERRED_ITEM_LEAF_ID_1_URI}"
pasture task dep add "${FOLLOWUP_SLICE_ID_URI}" --blocked-by "${DEFERRED_ITEM_LEAF_ID_2_URI}"
# Leaf task already has: pasture task dep add "${DEFERRED_ITEMS_TRACKING_GROUP_ID_URI}" --blocked-by "${LEAF_TASK_ID_URI}"
` + "```" + ``,
				},
				{
					Id:      "sup-followup-handoff-chain",
					Title:   "Follow-up Handoff Chain",
					Content: "Inside the follow-up lifecycle, the same handoff types (h1-h4) reapply:\n\n| Order | Handoff | Transition |\n|-------|---------|------------|\n| 1 | h5 | Reviewer → Followup: **Starts** the follow-up lifecycle |\n| 2 | *(none)* | Supervisor creates FOLLOWUP_URE (same actor) |\n| 3 | *(none)* | Supervisor creates FOLLOWUP_URD (same actor) |\n| 4 | h6 | Supervisor → Architect: Hands off FOLLOWUP_URE + FOLLOWUP_URD for FOLLOWUP_PROPOSAL |\n| 5 | h1 | Architect → Supervisor: After FOLLOWUP_PROPOSAL ratified |\n| 6 | h2 | Supervisor → Worker: FOLLOWUP_SLICE-N with DEFER'd-item leaf tasks |\n| 7 | h3 | Supervisor → Reviewer: Code review of follow-up slices |\n| 8 | h4 | Worker → Reviewer: Follow-up slice completion |\n\nFollow-up handoff storage: each handoff is authored in its Pasture task body (no filesystem path).\n\nSee `../protocol/HANDOFF_TEMPLATE.md` for full follow-up handoff examples.",
				},
			},
		},
		{
			Id:    "sup-impl-review-severity",
			Title: "Impl-Review Severity Tree Procedure",
			Content: "The severity behaviors for code review (Phase 10) are defined above as structured behaviors " +
				"(frag--sup-review-all-slices through frag--sup-followup-epic-timing). " +
				"The following subsections describe the operational procedures.",
			Subsections: []ProseSection{
				fragRef(FragSupSeverityTree),
				fragRef(FragSupNamingConvention),
			},
		},
		{
			Id:    "sup-tracking-progress",
			Title: "Tracking Progress",
			Content: `` + "```" + `bash
# Check all implementation slices
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=in_progress

# Check for blocked tasks
pasture task blocked --label="pasture:p9-impl:s9-slice"

# Check completed slices
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=closed

# Check specific task
pasture task show "${TASK_ID_URI}"

# Check severity groups from review
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:severity:blocker"
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:severity:important"
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:severity:minor"

# Check follow-up epics
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:epic-followup"
` + "```" + ``,
		},
	},

	Recipes: []RecipeBlock{},
}
