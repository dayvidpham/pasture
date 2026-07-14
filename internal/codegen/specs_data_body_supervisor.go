// Body content for the supervisor role SKILL.md.
package codegen

var supervisorBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-8-implementation-plan)** <- Phases 7-12`,

	Behaviors: []BehaviorSpec{
		{
			Id:        "sup-assign-slices",
			Given:     "slices created",
			When:      "assigning",
			Then:      "use `bd update <slice-id> --assignee=\"worker-N\"` for assignment",
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
			Then:      "the message MUST include: (1) explicit instruction to call `Skill(/pasture:worker)`, (2) the Beads task ID, (3) instruction to run `bd show <task-id>` for full context, and (4) the handoff document path",
			ShouldNot: "send bare instructions without Beads context — teammates have no prior knowledge of the task",
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
			Then:      "wire each group to its review round only: `bd dep add <review-round-id> --blocked-by <important-group-id>` — ALL severity groups must reach 0 before the wave closes",
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
			Then:      "keep workers alive for the review-fix cycle; workers notify supervisor via bd comments add but do NOT shut down",
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
		{
			Id:    "sup-first-steps",
			Title: "First Steps",
			Content: `The architect creates a placeholder IMPL_PLAN task. Your first job is to fill it in:

1. Read the RATIFIED_PLAN and the **URD** to understand the full scope, user requirements, and **identify production code paths**
   ` + "```" + `bash
   bd show <ratified-plan-id>
   bd show <urd-id>
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
7. Update the IMPL_PLAN with the layer breakdown + integration points:
   ` + "```" + `bash
   bd update <impl-plan-id> --description="$(cat <<'EOF'
   ---
   references:
     request: <request-task-id>
     urd: <urd-task-id>
     proposal: <ratified-proposal-id>
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
   - <task-id-1>: SLICE-1 ...
   - <task-id-2>: SLICE-2 ...
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
			Id:    "sup-reading-from-beads",
			Title: "Reading from Beads",
			Content: `Get the ratified plan and URD:
` + "```" + `bash
bd show <ratified-plan-id>
bd show <urd-id>
bd list --labels="pasture:p6-plan:s6-ratify" --status=open
bd list --labels="pasture:urd"
` + "```",
		},
		{
			Id:    "sup-impl-task-structure",
			Title: "Implementation Task Structure",
			Content: "```" + `go
type ImplementationTask struct {
    File            string          // file path
    TaskId          string          // Beads task ID (e.g., "aura-xxx")
    RequirementRef  string
    Prompt          string
    Context         struct {
        RelatedFiles    []struct{ File, Summary string }
        TaskDescription string
    }
    Status          string          // "Pending" | "Claimed" | "Complete" | "Failed"
    // Beads fields:
    ValidationChecklist []string              // Items from RATIFIED_PLAN
    AcceptanceCriteria  []AcceptanceCriterion // {Given, When, Then, ShouldNot}
    Tradeoffs           []Tradeoff           // {Decision, Rationale}
    RatifiedPlan        string               // Link to RATIFIED_PLAN task ID
}
` + "```",
		},
		{
			Id:      "sup-creating-vertical-slices",
			Title:   "Creating Vertical Slices (Phase 8)",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:    "sup-step1-impl-plan",
					Title: "Step 1: Create the IMPL_PLAN task",
					Content: "```" + `bash
bd create --labels "pasture:p8-impl:s8-plan" \
  --title "IMPL_PLAN: <feature>" \
  --description "---
references:
  request: <request-task-id>
  urd: <urd-task-id>
  proposal: <ratified-proposal-id>
---
## Horizontal Layers
- L1: Types and schemas
- L2: Tests (import production code)
- L3: Implementation + wiring

## Vertical Slices
- SLICE-1: <description> (files: ...)
- SLICE-2: <description> (files: ...)"
bd dep add <request-id> --blocked-by <impl-plan-id>
` + "```",
				},
				{
					Id:    "sup-step2-create-slices",
					Title: "Step 2: Create each slice",
					Content: "```" + `bash
bd create --labels "pasture:p9-impl:s9-slice" \
  --title "SLICE-1: <slice name>" \
  --description "---
references:
  impl_plan: <impl-plan-task-id>
  urd: <urd-task-id>
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
- [ ] Production path verified" \
  --design='{"validation_checklist":["Types defined","Tests written (import production code)","Implementation complete","Production path verified"],"acceptance_criteria":[{"given":"X","when":"Y","then":"Z"}],"ratified_plan":"<ratified-plan-id>"}'
bd dep add <impl-plan-id> --blocked-by <slice-1-id>
` + "```",
				},
				{
					Id:    "sup-step3-leaf-tasks",
					Title: "Step 3: Create leaf tasks within each slice (CRITICAL)",
					Content: `Per [C-slice-leaf-tasks], create Beads tasks for each implementation unit within the slice, then chain them as dependencies. Leaf tasks are what workers actually implement.

` + "```" + `bash
# L1: Types and interfaces for this slice
LEAF_L1=$(bd create --labels "pasture:p9-impl:s9-slice" \
  --title "SLICE-1-L1: Types — <slice name>" \
  --description "---
references:
  slice: <slice-1-id>
  impl_plan: <impl-plan-task-id>
  urd: <urd-task-id>
---
## Scope
Define types, interfaces, and schemas for this slice.

## Files Owned
- <file-path-1>
- <file-path-2>

## Acceptance Criteria
Given <context> when <action> then <outcome> should never <anti-pattern>")
bd dep add <slice-1-id> --blocked-by $LEAF_L1

# L2: Tests (import production code, will fail until L3)
LEAF_L2=$(bd create --labels "pasture:p9-impl:s9-slice" \
  --title "SLICE-1-L2: Tests — <slice name>" \
  --description "---
references:
  slice: <slice-1-id>
  impl_plan: <impl-plan-task-id>
---
## Scope
Write tests that import from production code paths. Tests MUST fail until L3.

## Files Owned
- <test-file-path-1>

## Acceptance Criteria
Given <context> when <action> then <outcome> should never <anti-pattern>")
bd dep add <slice-1-id> --blocked-by $LEAF_L2
# L2 depends on L1 types being defined first
bd dep add $LEAF_L2 --blocked-by $LEAF_L1

# L3: Implementation (makes tests pass)
LEAF_L3=$(bd create --labels "pasture:p9-impl:s9-slice" \
  --title "SLICE-1-L3: Impl — <slice name>" \
  --description "---
references:
  slice: <slice-1-id>
  impl_plan: <impl-plan-task-id>
---
## Scope
Implement production code to make L2 tests pass.

## Files Owned
- <impl-file-path-1>

## Acceptance Criteria
Given <context> when <action> then <outcome> should never <anti-pattern>")
bd dep add <slice-1-id> --blocked-by $LEAF_L3
# L3 depends on L2 tests existing first
bd dep add $LEAF_L3 --blocked-by $LEAF_L2
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
			Content: "```" + `bash
# Assign slices to workers
bd update <slice-1-id> --assignee="worker-1"
bd update <slice-2-id> --assignee="worker-2"
bd update <slice-3-id> --assignee="worker-3"
` + "```",
		},
		{
			Id:    "sup-spawning-workers",
			Title: "Spawning Workers",
			Content: `Per [C-supervisor-no-impl], all implementation work — no matter how small — is delegated to a worker agent. The supervisor's job is coordination, tracking, and quality control.

Workers are **general-purpose agents** that call ` + "`/pasture:worker`" + ` at the start. Select the model based on task complexity:

` + "```" + `
// Non-trivial work → sonnet model
Task({
  subagent_type: "general-purpose",
  model: "sonnet",
  run_in_background: true,
  prompt: ` + "`" + `Call Skill(/pasture:worker) and implement the assigned slice.\n\nBeads Task ID: ${taskId}...` + "`" + `
})

// Trivial work (config tweak, typo fix, single-file edit) → haiku model
Task({
  subagent_type: "general-purpose",
  model: "haiku",
  run_in_background: true,
  prompt: ` + "`" + `Call Skill(/pasture:worker) and fix the typo in...\n\nBeads Task ID: ${taskId}...` + "`" + `
})

// WRONG: Supervisor implementing changes directly
Edit({ file_path: "src/foo.ts", ... })  // Supervisors coordinate, they don't implement!

// WRONG: Do not use specialized agent types like "pasture:worker" directly
Task({
  subagent_type: "pasture:worker",  // This doesn't exist!
  ...
})
` + "```",
			Subsections: []ProseSection{
				{
					Id:    "sup-model-selection",
					Title: "Model Selection Guide",
					Content: `| Complexity | Model | Examples |
|------------|-------|----------|
| Trivial | ` + "`haiku`" + ` | Single-file edit, config change, typo fix, renaming, adding a label |
| Non-trivial | ` + "`sonnet`" + ` | Multi-file changes, new features, architectural work, complex logic, test suites |

**Handoff:** Before spawning each worker, author its handoff in the slice (or a dedicated handoff) Beads task body — the task body IS the handoff (no filesystem path).

See: [../supervisor-spawn-worker/SKILL.md](../supervisor-spawn-worker/SKILL.md) for handoff template.`,
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

Your Beads task ID: <slice-task-id>
Run this to get full requirements + handoff: bd show <slice-task-id>

Key context:
- Request: <request-task-id> (run: bd show <request-task-id>)
- URD: <urd-task-id> (run: bd show <urd-task-id>)
- IMPL_PLAN: <impl-plan-task-id> (run: bd show <impl-plan-task-id>)

Read the handoff doc and your Beads task before starting implementation.` + "`" + `,
  summary: "SLICE-1 assignment with Beads context"
})
` + "```" + `

Per [sup-teamcreate-msg], every assignment must include actionable ` + "`bd show`" + ` commands. Teammates cannot see your conversation history, the Beads task tree, or any prior context.

The worker skill provides:
- File ownership validation
- Standard DI patterns
- Completion/blocked signaling via Beads`,
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
					Content: "```" + `bash
bd create --type=epic --priority=3 \
  --title="FOLLOWUP: User-deferred improvements from UAT" \
  --description="---
references:
  request: <request-task-id>
  urd: <urd-task-id>
  uat: <uat-task-ids>
---
Aggregated user-DEFER'd items from UAT (Phase 5/11)." \
  --add-label "pasture:epic-followup"

# Link the DEFER'd UAT items as children of the follow-up epic
bd dep add <followup-epic-id> --blocked-by <deferred-item-id-1>
bd dep add <followup-epic-id> --blocked-by <deferred-item-id-2>
` + "```" + `

Severity routing follows [frag--sup-blocker-dual-parent] and [frag--sup-deferred-followup]: all review severities reach 0; the FOLLOWUP epic is DEFER-fed only.`,
				},
				{
					Id:    "sup-followup-step2",
					Title: "Step 2: Follow-up lifecycle (same protocol, FOLLOWUP_* prefix)",
					Content: `The follow-up epic runs the same protocol phases with FOLLOWUP_* prefixed task types. The supervisor creates the initial lifecycle tasks:

` + "```" + `
FOLLOWUP epic (pasture:epic-followup)
  ├── relates_to: original URD
  ├── relates_to: original REVIEW-A/B/C tasks
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
FOLLOWUP_URE_ID=$(bd create \
  --title "FOLLOWUP_URE: Scope follow-up for <feature>" \
  --labels "pasture:p2-user:s2_1-elicit" \
  --description "---
references:
  followup_epic: <followup-epic-id>
  original_urd: <original-urd-id>
---
Scoping URE: determine which user-DEFER'd UAT items to address.")
bd dep add <followup-epic-id> --blocked-by $FOLLOWUP_URE_ID

# Create FOLLOWUP_URD — requirements for follow-up scope
FOLLOWUP_URD_ID=$(bd create \
  --title "FOLLOWUP_URD: Requirements for <feature> follow-up" \
  --labels "pasture:p2-user:s2_2-urd,pasture:urd" \
  --description "---
references:
  followup_epic: <followup-epic-id>
  original_urd: <original-urd-id>
---
Follow-up requirements. References original URD.")
bd dep add $FOLLOWUP_URE_ID --blocked-by $FOLLOWUP_URD_ID
` + "```" + `

The remaining lifecycle tasks (FOLLOWUP_PROPOSAL, FOLLOWUP_IMPL_PLAN, FOLLOWUP_SLICE) are created as the follow-up epic progresses through the protocol phases.`,
				},
				{
					Id:    "sup-followup-step3",
					Title: "Step 3: DEFER'd-item leaf adoption (dual-parent)",
					Content: `When the supervisor creates FOLLOWUP_SLICE-N tasks during the follow-up implementation phase, the user-DEFER'd UAT-item leaf tasks gain a second parent (dual-parent: leaf blocks BOTH the DEFER'd-items tracking group AND the follow-up slice):

` + "```" + `bash
# Leaf task gets dual-parent: DEFER'd-items tracking group + follow-up slice
bd dep add <followup-slice-id> --blocked-by <deferred-item-leaf-id-1>
bd dep add <followup-slice-id> --blocked-by <deferred-item-leaf-id-2>
# Leaf task already has: bd dep add <deferred-items-tracking-group-id> --blocked-by <leaf-task-id>
` + "```",
				},
				{
					Id:    "sup-followup-handoff-chain",
					Title: "Follow-up Handoff Chain",
					Content: `Inside the follow-up lifecycle, the same handoff types (h1-h4) reapply:

| Order | Handoff | Transition |
|-------|---------|------------|
| 1 | h5 | Reviewer → Followup: **Starts** the follow-up lifecycle |
| 2 | *(none)* | Supervisor creates FOLLOWUP_URE (same actor) |
| 3 | *(none)* | Supervisor creates FOLLOWUP_URD (same actor) |
| 4 | h6 | Supervisor → Architect: Hands off FOLLOWUP_URE + FOLLOWUP_URD for FOLLOWUP_PROPOSAL |
| 5 | h1 | Architect → Supervisor: After FOLLOWUP_PROPOSAL ratified |
| 6 | h2 | Supervisor → Worker: FOLLOWUP_SLICE-N with DEFER'd-item leaf tasks |
| 7 | h3 | Supervisor → Reviewer: Code review of follow-up slices |
| 8 | h4 | Worker → Reviewer: Follow-up slice completion |

Follow-up handoff storage: each handoff is authored in its Beads task body (no filesystem path).

See ` + "`../protocol/HANDOFF_TEMPLATE.md`" + ` for full follow-up handoff examples.`,
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
			Content: "```" + `bash
# Check all implementation slices
bd list --labels="pasture:p9-impl:s9-slice" --status=in_progress

# Check for blocked tasks
bd list --labels="pasture:p9-impl:s9-slice" --status=blocked

# Check completed slices
bd list --labels="pasture:p9-impl:s9-slice" --status=done

# Check specific task
bd show <task-id>

# Check severity groups from review
bd list --labels="pasture:severity:blocker"
bd list --labels="pasture:severity:important"
bd list --labels="pasture:severity:minor"

# Check follow-up epics
bd list --labels="pasture:epic-followup"
` + "```",
		},
	},

	Recipes: []RecipeBlock{},
}
