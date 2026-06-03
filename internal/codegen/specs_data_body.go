// Canonical body content for all skill SKILL.md files.
//
// This file consolidates body data for all 7 skills that have hand-authored
// body sections (rendered inside the BEGIN/END marker region by the unified
// templates). Each var encodes the body content for one skill directory.
//
// SkillBodySpecs maps skill directory names to their body content.
// Keys are directory names (not types.RoleId) because sub-skills like
// "supervisor-plan-tasks" have no RoleId equivalent.
package codegen

var SkillBodySpecs = map[string]SkillBody{
	"supervisor":              supervisorBody,
	"supervisor-plan-tasks":   supervisorPlanTasksBody,
	"supervisor-spawn-worker": supervisorSpawnWorkerBody,
	"worker":                  workerBody,
	"architect":               architectBody,
	"reviewer":                reviewerBody,
	"impl-review":             implReviewBody,
	// Newly-ported skill bodies (22 skills from aura-plugins/skills/).
	"architect-handoff":         architectHandoffBody,
	"architect-propose-plan":    architectProposePlanBody,
	"architect-ratify":          architectRatifyBody,
	"architect-request-review":  architectRequestReviewBody,
	"epoch":                     epochBody,
	"explore":                   exploreBody,
	"impl-slice":                implSliceBody,
	"research":                  researchBody,
	"reviewer-comment":          reviewerCommentBody,
	"reviewer-review-code":      reviewerReviewCodeBody,
	"reviewer-review-plan":      reviewerReviewPlanBody,
	"reviewer-vote":             reviewerVoteBody,
	"status":                    statusBody,
	"supervisor-commit":         supervisorCommitBody,
	"supervisor-track-progress": supervisorTrackProgressBody,
	"swarm":                     swarmBody,
	"user-elicit":               userElicitBody,
	"user-request":              userRequestBody,
	"user-uat":                  userUatBody,
	"worker-blocked":            workerBlockedBody,
	"worker-complete":           workerCompleteBody,
	"worker-implement":          workerImplementBody,
}

// ─── supervisorBody ──────────────────────────────────────────────────────────

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
			Then:      "proceed autonomously without asking permission; user-gated phases are: Phase 2 (URE), Phase 5 (Plan UAT), Phase 11 (Impl UAT); all other phases (8 IMPL_PLAN, 9 SLICES, 10 CODE REVIEW, 12 LANDING) progress automatically",
			ShouldNot: "ask 'Should I proceed?' for autonomous phases; only pause for user-facing phases that require human input",
		},
		// R7/A1: code review iterates with NO cycle cap until 0/0/0 clean.
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

// ─── supervisorPlanTasksBody ─────────────────────────────────────────────────

// supervisorPlanTasksBody encodes skills/supervisor-plan-tasks/SKILL.md
// body content (lines 39–409).
var supervisorPlanTasksBody = SkillBody{
	Preamble: "Break RATIFIED_PLAN into vertical slice Implementation tasks for workers.\n\n" +
		"**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-8-implementation-plan)** <- Phase 8",
	Behaviors: []BehaviorSpec{
		{
			Id:        "sup-plan-impl-plan-decompose",
			Given:     "IMPL_PLAN placeholder",
			When:      "planning",
			Then:      "decompose into vertical slices (production code paths)",
			ShouldNot: "decompose into horizontal layers (files)",
		},
		{
			Id:        "sup-plan-ratified-plan-tasks",
			Given:     "RATIFIED_PLAN features/commands",
			When:      "creating tasks",
			Then:      "assign one vertical slice per worker (full end-to-end)",
			ShouldNot: "assign horizontal layers (types worker, tests worker, impl worker)",
		},
		{
			Id:        "sup-plan-vertical-slice-define",
			Given:     "vertical slice",
			When:      "defining",
			Then:      "specify production code path and backward planning approach",
			ShouldNot: "leave workers guessing what end users will run",
		},
		{
			Id:        "sup-plan-validation-checklist",
			Given:     "validation_checklist",
			When:      "distributing",
			Then:      "include production code verification",
			ShouldNot: "allow test-only validation",
		},
		{
			Id:        "sup-plan-integration-points-identify",
			Given:     "multiple vertical slices",
			When:      "slices share types, interfaces, or data flows",
			Then:      "identify horizontal Layer Integration Points where slices must inter-op and document them in the IMPL_PLAN with owning slice, consuming slices, and the shared contract (type, interface, or protocol)",
			ShouldNot: "leave cross-slice dependencies implicit — divergence grows when slices develop in isolation without clear merge points",
		},
		{
			Id:        "sup-plan-integration-points-include",
			Given:     "integration points identified",
			When:      "creating slice tasks",
			Then:      "include each integration point in the relevant slice descriptions so workers know what they must export and what they may import",
			ShouldNot: "assume workers will discover cross-slice contracts on their own",
		},
		{
			Id:        "sup-plan-interface-first",
			Given:     "slices that share types, interfaces, or contracts (R3, per C-interface-first-slices)",
			When:      "deciding decomposition order",
			Then:      "prefer extracting a horizontal interface-first FOUNDATION slice (all public types/interfaces/contracts) that lands first, so the dependent implementation slices can compile against the contracts and run in PARALLEL",
			ShouldNot: "force a linear slice chain (A->B->C) when the runtime dependency is only on interfaces that could be exported up front",
		},
	},
	Sections: []ProseSection{
		{
			Id:      "sup-plan-when-to-use",
			Title:   "When to Use",
			Content: `Received handoff from architect with RATIFIED_PLAN task ID and placeholder IMPL_PLAN task.`,
		},
		{
			Id:    "sup-plan-critical-vertical-slices",
			Title: "Critical: Vertical Slices, Not Horizontal Layers",
			Content: "**ANTI-PATTERN (causes dual-export problem):**\n" +
				"```\n" +
				"Task A: Layer 1 - types.go (all types)\n" +
				"Task B: Layer 2 - service_test.go (all tests)\n" +
				"Task C: Layer 3 - service.go (all implementation)\n" +
				"Task D: Layer 4 - CLI wiring\n" +
				"```\n" +
				"\n" +
				"**Problem:** No worker owns full production code path → dual-export anti-pattern\n" +
				"\n" +
				"**CORRECT PATTERN:**\n" +
				"```\n" +
				"SLICE-1: \"feature list command\" (Worker A owns full vertical)\n" +
				"  - ListOptions, ListEntry types (L1)\n" +
				"  - Tests importing `cli-tool feature list` CLI (L2)\n" +
				"  - service.ListItems() implementation (L3)\n" +
				"  - listCmd (cobra) RunE handler wiring (L3)\n" +
				"\n" +
				"SLICE-2: \"feature detail command\" (Worker B owns full vertical)\n" +
				"  - DetailView types (L1)\n" +
				"  - Tests importing `cli-tool feature detail` CLI (L2)\n" +
				"  - service.GetItemDetail() implementation (L3)\n" +
				"  - detailCmd (cobra) RunE handler wiring (L3)\n" +
				"```",
		},
		{
			Id:    "sup-plan-steps",
			Title: "Steps",
			Content: "1. **Read RATIFIED_PLAN and URD tasks:**\n" +
				"   ```bash\n" +
				"   bd show <ratified-plan-id>\n" +
				"   bd show <urd-id>\n" +
				"   ```\n" +
				"\n" +
				"2. **Identify production code paths** (what end users will actually run):\n" +
				"   - CLI commands: `cli-tool feature`, `cli-tool feature list`, `cli-tool feature detail`\n" +
				"   - API endpoints: `POST /api/items`, `GET /api/items/:id`\n" +
				"   - Background jobs: `sync-daemon`, `backup-daemon`\n" +
				"\n" +
				"3. **Decompose into vertical slices** (one per production code path):\n" +
				"   - Each slice = one command/endpoint/job\n" +
				"   - Each slice owned by ONE worker\n" +
				"   - Each slice goes from types → tests → implementation → wiring\n" +
				"\n" +
				"4. **Identify shared infrastructure** (optional Layer 0):\n" +
				"   - Common types used across ALL slices (e.g., base error enums)\n" +
				"   - Shared utilities (not specific to one slice)\n" +
				"   - If significant, create Layer 0 tasks (parallel, no deps)\n" +
				"\n" +
				"5. **Identify horizontal Layer Integration Points** (where slices must inter-op):\n" +
				"   - For each pair of slices, ask: \"Does slice A need to import/call/consume anything from slice B?\"\n" +
				"   - If yes, document the integration point: owning slice, consuming slice(s), and the shared contract\n" +
				"   - Integration points should merge **sooner rather than later** — delaying inter-op causes divergence\n" +
				"   - Common integration points: shared type definitions, event interfaces, registry patterns, DI bindings\n" +
				"   - Each integration point gets an explicit owner (the slice that defines/exports it)\n" +
				"\n" +
				"   ```\n" +
				"   ## Integration Points (example)\n" +
				"\n" +
				"   | ID | Contract | Owner (exports) | Consumer(s) (imports) | Merge Timing |\n" +
				"   |----|----------|-----------------|-----------------------|--------------|\n" +
				"   | IP-1 | PhaseEnum type | SLICE-1 (foundation) | SLICE-2, SLICE-3, SLICE-4 | L1 (types) |\n" +
				"   | IP-2 | ConstraintContext interface | SLICE-1 (foundation) | SLICE-2 (gen_schema) | L1 (types) |\n" +
				"   | IP-3 | SkillRegistry protocol | SLICE-3 (gen_skills) | SLICE-4 (context_injection) | L3 (impl) |\n" +
				"   ```\n" +
				"\n" +
				"   **Interface-first decomposition (R3, Strong SHOULD — see `C-interface-first-slices`):** when slices share contracts, prefer extracting a horizontal **interface-first FOUNDATION slice** that exports ALL public types/interfaces/contracts and lands FIRST (a barrier). The dependent implementation slices then compile against those contracts and run in **parallel**, instead of being forced into a linear `A → B → C` chain whose only real coupling is at the interface boundary. Reserve a linear chain for cases where the runtime dependency genuinely exceeds the interface.\n" +
				"\n" +
				"6. **Create vertical slice tasks:**\n" +
				"   ```bash\n" +
				"   bd create --type=task \\\n" +
				"     --labels=\"pasture:p9-impl:s9-slice\" \\\n" +
				"     --title=\"SLICE-1: Implement 'cli-tool feature list' command (full vertical)\" \\\n" +
				"     --description=\"$(cat <<'EOF'\n" +
				"   ---\n" +
				"   references:\n" +
				"     impl_plan: <impl-plan-task-id>\n" +
				"     urd: <urd-task-id>\n" +
				"   ---\n" +
				"   ## Production Code Path\n" +
				"\n" +
				"   **End user runs:** `./bin/cli-tool feature list`\n" +
				"\n" +
				"   ## Worker Owns (Full Vertical Slice)\n" +
				"\n" +
				"   Plan backwards from production code path:\n" +
				"   1. End: CLI entry point `listCmd (cobra.Command) RunE handler`\n" +
				"   2. Back: Service call `feature.NewService(deps).ListItems(opts)`\n" +
				"   3. Back: Service method `ListItems(opts ListOptions) ([]ListEntry, error)`\n" +
				"   4. Back: Types `ListOptions`, `ListEntry`\n" +
				"\n" +
				"   ## Files You Own (Within These Files)\n" +
				"\n" +
				"   - pkg/feature/types.go (ListOptions, ListEntry ONLY)\n" +
				"   - cmd/feature/list_test.go (import actual CLI)\n" +
				"   - pkg/feature/service.go (ListItems method ONLY)\n" +
				"   - cmd/feature/list.go (list subcommand wiring ONLY)\n" +
				"\n" +
				"   ## Implementation Order (Layers Within Your Slice)\n" +
				"\n" +
				"   **Layer 1: Types** (your slice only)\n" +
				"   - Create ListOptions, ListEntry\n" +
				"   - Do NOT add types for other slices (e.g., DetailView)\n" +
				"\n" +
				"   **Layer 2: Tests** (importing production code)\n" +
				"   - Import actual CLI: `import \"myproject/cmd/feature\"`\n" +
				"   - Test the actual command users will run\n" +
				"   - Tests will FAIL - expected, no implementation yet\n" +
				"\n" +
				"   **Layer 3: Implementation + Wiring**\n" +
				"   - Implement service.ListItems() method\n" +
				"   - Wire cobra command with feature.NewService(realDeps)\n" +
				"   - No TODO placeholders\n" +
				"   - Tests should now PASS\n" +
				"\n" +
				"   ## Validation\n" +
				"\n" +
				"   Before marking complete:\n" +
				"   - [ ] Production code verified via code inspection (no TODOs, real deps wired)\n" +
				"   - [ ] Tests import actual CLI (not test-only export)\n" +
				"   - [ ] No dual-export anti-pattern\n" +
				"   - [ ] No TODO placeholders\n" +
				"   - [ ] Service wired with real dependencies\n" +
				"   EOF\n" +
				"   )\" \\\n" +
				"     --design='{\n" +
				"       \"productionCodePath\": \"cli-tool feature list\",\n" +
				"       \"validation_checklist\": [\n" +
				"         \"Type checking passes\",\n" +
				"         \"Tests pass\",\n" +
				"         \"Production code verified via code inspection\",\n" +
				"         \"Tests import production CLI package\",\n" +
				"         \"No TODO placeholders in CLI action\",\n" +
				"         \"Service wired with real dependencies\"\n" +
				"       ],\n" +
				"       \"acceptance_criteria\": [{\n" +
				"         \"given\": \"user runs cli-tool feature list\",\n" +
				"         \"when\": \"command executes\",\n" +
				"         \"then\": \"shows list from actual service\",\n" +
				"         \"should_not\": \"have dual-export (test vs production paths)\"\n" +
				"       }],\n" +
				"       \"ratified_plan\": \"<ratified-plan-id>\"\n" +
				"     }'\n" +
				"\n" +
				"   bd dep add <impl-plan-id> --blocked-by <slice-task-id>\n" +
				"   ```\n" +
				"\n" +
				"7. **Update IMPL_PLAN with vertical slice breakdown + integration points:**\n" +
				"   ```bash\n" +
				"   bd update <impl-plan-id> --description=\"$(cat <<'EOF'\n" +
				"   ---\n" +
				"   references:\n" +
				"     request: <request-task-id>\n" +
				"     urd: <urd-task-id>\n" +
				"     proposal: <ratified-proposal-id>\n" +
				"   ---\n" +
				"   ## Vertical Slice Decomposition\n" +
				"\n" +
				"   Each worker owns ONE production code path (full vertical slice from CLI → service → types).\n" +
				"\n" +
				"   ### Shared Infrastructure (Layer 0 - optional)\n" +
				"   - Common types: SortOrder, OutputFormat, ErrorCode enums\n" +
				"   - Implemented first, parallel\n" +
				"\n" +
				"   ### Vertical Slices (parallel, after Layer 0)\n" +
				"\n" +
				"   **SLICE-1: \"cli-tool feature\" (default command)**\n" +
				"   - Worker: A\n" +
				"   - Production path: `./bin/cli-tool feature`\n" +
				"   - Owns: default action, recent items logic\n" +
				"   - Task: aura-xxx\n" +
				"\n" +
				"   **SLICE-2: \"cli-tool feature list\"**\n" +
				"   - Worker: B\n" +
				"   - Production path: `./bin/cli-tool feature list`\n" +
				"   - Owns: ListOptions types, list tests, listItems() method, list CLI wiring\n" +
				"   - Task: aura-yyy\n" +
				"\n" +
				"   **SLICE-3: \"cli-tool feature detail\"**\n" +
				"   - Worker: C\n" +
				"   - Production path: `./bin/cli-tool feature detail <id>`\n" +
				"   - Owns: DetailView types, detail tests, getItemDetail() method, detail CLI wiring\n" +
				"   - Task: aura-zzz\n" +
				"\n" +
				"   **SLICE-4: \"cli-tool feature search\"**\n" +
				"   - Worker: D\n" +
				"   - Production path: `./bin/cli-tool feature search`\n" +
				"   - Owns: SearchQuery types, search tests, searchItems() method, search CLI wiring\n" +
				"   - Task: aura-www\n" +
				"\n" +
				"   ## Horizontal Layer Integration Points\n" +
				"\n" +
				"   Where slices must inter-op. Merge sooner, not later — divergence grows with delay.\n" +
				"\n" +
				"   | ID | Contract | Owner (exports) | Consumer(s) (imports) | Merge Timing |\n" +
				"   |----|----------|-----------------|-----------------------|--------------|\n" +
				"   | IP-1 | FeatureError enum | SLICE-1 | SLICE-2, SLICE-3, SLICE-4 | L1 (types) |\n" +
				"   | IP-2 | BaseService interface | SLICE-1 | SLICE-2, SLICE-3 | L1 (types) |\n" +
				"\n" +
				"   ## Execution Order\n" +
				"\n" +
				"   1. Layer 0 (if needed): Shared infrastructure (parallel)\n" +
				"   2. SLICE-1 through SLICE-4: Each worker implements their vertical slice (parallel)\n" +
				"      - Within each slice: Types (L1) → Tests (L2) → Impl+Wiring (L3)\n" +
				"   3. Integration points merge at documented timing (L1 contracts first, L3 wiring last)\n" +
				"\n" +
				"   ## Validation\n" +
				"\n" +
				"   All production code paths verified via code inspection:\n" +
				"   - ./bin/cli-tool feature\n" +
				"   - ./bin/cli-tool feature list\n" +
				"   - ./bin/cli-tool feature detail <id>\n" +
				"   - ./bin/cli-tool feature search\n" +
				"   - All integration points verified: contracts match between owner and consumers\n" +
				"   EOF\n" +
				"   )\"\n" +
				"   ```",
		},
		{
			Id:    "sup-plan-vertical-slice-task-structure",
			Title: "Vertical Slice Task Structure",
			Content: "```json\n" +
				"{\n" +
				"  \"slice\": \"feature-list\",\n" +
				"  \"productionCodePath\": \"cli-tool feature list\",\n" +
				"  \"taskId\": \"aura-xxx\",\n" +
				"  \"workerOwns\": {\n" +
				"    \"endPoint\": \"listCmd (cobra.Command) RunE handler\",\n" +
				"    \"types\": [\"ListOptions\", \"ListEntry\"],\n" +
				"    \"tests\": [\"cmd/feature/list_test.go\"],\n" +
				"    \"implementation\": [\n" +
				"      \"(*FeatureService).ListItems() method\",\n" +
				"      \"listCmd wired with feature.NewService(realDeps)\"\n" +
				"    ]\n" +
				"  },\n" +
				"  \"planningApproach\": \"Backwards from production code path\",\n" +
				"  \"validation_checklist\": [\n" +
				"    \"Type checking passes\",\n" +
				"    \"Tests pass\",\n" +
				"    \"Production code works: ./bin/aura sessions list\",\n" +
				"    \"Tests import production CLI (not test-only export)\",\n" +
				"    \"No TODO placeholders\",\n" +
				"    \"Service wired with real dependencies\"\n" +
				"  ],\n" +
				"  \"acceptance_criteria\": [{\n" +
				"    \"given\": \"user runs aura sessions list\",\n" +
				"    \"when\": \"command executes\",\n" +
				"    \"then\": \"shows session list from actual service\",\n" +
				"    \"should_not\": \"have dual-export or TODO placeholders\"\n" +
				"  }],\n" +
				"  \"ratified_plan\": \"<ratified-plan-id>\",\n" +
				"  \"urd\": \"<urd-id>\"\n" +
				"}\n" +
				"```",
		},
		{
			Id:    "sup-plan-layer-cake",
			Title: "Layer Cake Within Each Vertical Slice",
			Content: "Each worker implements their slice in layers (TDD approach):\n" +
				"\n" +
				"```\n" +
				"Worker A's Slice: \"aura sessions list\"\n" +
				"  Layer 1: Types (ListOptions, SessionListEntry only)\n" +
				"  Layer 2: Tests (import sessions package, test list action)\n" +
				"           → Tests will FAIL (expected - no impl yet)\n" +
				"  Layer 3: Implementation + Wiring\n" +
				"           - (*SessionsService).ListSessions() method\n" +
				"           - listCmd wired with sessions.NewService(deps)\n" +
				"           - Wire action to call service\n" +
				"           → Tests should now PASS\n" +
				"```\n" +
				"\n" +
				"**Important:** Layer 2 tests failing is expected. Worker knows tests define the contract, implementation comes in Layer 3.",
		},
		{
			Id:    "sup-plan-red-green-flags",
			Title: "Red Flags vs Green Flags",
			Content: "**Red flags (horizontal layer decomposition):**\n" +
				"- Tasks organized by layer: \"Layer 1 all types\", \"Layer 2 all tests\"\n" +
				"- Worker assigned \"all types\" or \"all tests\" instead of feature slice\n" +
				"- No production code path specified per task\n" +
				"- Tasks describe \"file to modify\" not \"production code path to deliver\"\n" +
				"\n" +
				"**Green flags (vertical slice decomposition):**\n" +
				"- Each task specifies production code path (e.g., \"aura sessions list\")\n" +
				"- Worker owns full vertical (types → tests → impl → wiring)\n" +
				"- Task description says \"plan backwards from end point\"\n" +
				"- Validation checklist includes \"production code works: ./bin/aura <command>\"\n" +
				"- Workers can execute independently (parallel slices)",
		},
		{
			Id:    "sup-plan-shared-infrastructure",
			Title: "Shared Infrastructure (Layer 0)",
			Content: "If multiple slices share common infrastructure:\n" +
				"\n" +
				"```\n" +
				"Layer 0 Tasks (parallel, implemented first):\n" +
				"- Common enums: SortOrder, OutputFormat, SessionsErrorCode\n" +
				"- Common types: ParseHealth (used by all slices)\n" +
				"- Shared utilities: isSidechainSession(), getGitBranch()\n" +
				"```\n" +
				"\n" +
				"Then vertical slices proceed in parallel, depending on Layer 0.\n" +
				"\n" +
				"**Key insight:** Shared infrastructure is the exception, not the rule. Most types/logic belong to specific slices.",
		},
		{
			Id:    "sup-plan-followup-impl-plan",
			Title: "Follow-up Implementation Plan (FOLLOWUP_IMPL_PLAN)",
			Content: "When planning for a follow-up epic (after receiving h1 from architect post-FOLLOWUP_PROPOSAL ratification), the same vertical slice decomposition applies:\n" +
				"\n" +
				"```bash\n" +
				"# Create FOLLOWUP_IMPL_PLAN\n" +
				"bd create --type=epic --priority=2 \\\n" +
				"  --labels=\"pasture:p8-impl:s8-plan\" \\\n" +
				"  --title=\"FOLLOWUP_IMPL_PLAN: <follow-up feature>\" \\\n" +
				"  --description=\"---\n" +
				"references:\n" +
				"  followup_epic: <followup-epic-id>\n" +
				"  original_request: <request-task-id>\n" +
				"  original_urd: <urd-task-id>\n" +
				"  followup_urd: <followup-urd-id>\n" +
				"  followup_proposal: <followup-proposal-id>\n" +
				"---\n" +
				"Vertical slice decomposition for follow-up epic.\"\n" +
				"\n" +
				"# Create FOLLOWUP_SLICE-N with DEFER'd-item leaf tasks\n" +
				"bd create --type=task \\\n" +
				"  --labels=\"pasture:p9-impl:s9-slice\" \\\n" +
				"  --title=\"FOLLOWUP_SLICE-1: <description>\" \\\n" +
				"  --description=\"---\n" +
				"references:\n" +
				"  followup_impl_plan: <followup-impl-plan-id>\n" +
				"  followup_urd: <followup-urd-id>\n" +
				"---\n" +
				"## DEFER'd-Item Leaf Tasks\n" +
				"| Leaf Task ID | Source UAT | DEFER'd Item | Description |\n" +
				"|---|---|---|---|\n" +
				"| <leaf-id-1> | <uat-id> | <deferred-item-id> | <description> |\n" +
				"| <leaf-id-2> | <uat-id> | <deferred-item-id> | <description> |\n" +
				"\n" +
				"## Specification\n" +
				"<detailed spec>\n" +
				"\n" +
				"## Validation Checklist\n" +
				"- [ ] All DEFER'd-item leaf tasks resolved\n" +
				"- [ ] Tests pass\n" +
				"- [ ] Production code path verified\"\n" +
				"\n" +
				"# Wire dual-parent: leaf blocks BOTH the DEFER'd-items tracking group AND the follow-up slice\n" +
				"bd dep add <followup-slice-id> --blocked-by <leaf-task-id-1>\n" +
				"bd dep add <followup-slice-id> --blocked-by <leaf-task-id-2>\n" +
				"```",
		},
	},
}

// ─── supervisorSpawnWorkerBody ───────────────────────────────────────────────

// supervisorSpawnWorkerBody encodes skills/supervisor-spawn-worker/SKILL.md
// body content (lines 44–266).
var supervisorSpawnWorkerBody = SkillBody{
	Preamble: "Launch the wave of workers for parallel vertical slice implementation, reviewed by ephemeral reviewers.\n\n" +
		"**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9",
	Behaviors: []BehaviorSpec{
		{
			Id:        "sup-spawn-task-tool",
			Given:     "implementation tasks",
			When:      "spawning",
			Then:      "use Task tool with `run_in_background: true`",
			ShouldNot: "block on worker completion",
		},
		{
			Id:        "sup-spawn-parallel-wave",
			Given:     "multiple workers",
			When:      "launching",
			Then:      "spawn all slices in parallel as a single wave",
			ShouldNot: "spawn sequentially",
		},
		{
			Id:        "sup-spawn-worker-context",
			Given:     "worker assignment",
			When:      "providing context",
			Then:      "include Beads task ID, full context, and handoff document",
			ShouldNot: "omit checklist or criteria",
		},
		{
			Id:        "sup-spawn-handoff-doc",
			Given:     "worker handoff",
			When:      "creating",
			Then:      "author the supervisor→worker handoff in the slice (or a dedicated handoff) Beads task body",
			ShouldNot: "skip the handoff or store it as a filesystem path",
		},
		{
			Id:        "sup-spawn-no-close-before-review",
			Given:     "workers complete their slices",
			When:      "first wave finishes",
			Then:      "do NOT close slices — ephemeral reviewers must review ALL slices first",
			ShouldNot: "close a slice that has not been reviewed at least once",
		},
		{
			Id:        "sup-spawn-fix-and-rereview",
			Given:     "reviewers finish reviewing",
			When:      "BLOCKERs or IMPORTANT findings exist",
			Then:      "send findings to workers for fixing, then spawn new ephemeral reviewers for re-review",
			ShouldNot: "skip re-review after fixes",
		},
		{
			Id:        "sup-spawn-max-cycles",
			Given:     "worker-reviewer cycle",
			When:      "counting iterations",
			Then:      "iterate review->fix->re-review with NO cycle cap until a fix-free clean round confirms 0 BLOCKER + 0 IMPORTANT + 0 MINOR",
			ShouldNot: "impose a maximum cycle cap, close a wave on a fix-applying round, or proceed with any finding outstanding",
		},
		{
			Id:        "sup-spawn-important-after-cycles",
			Given:     "IMPORTANT or MINOR findings remain",
			When:      "deciding next step",
			Then:      "keep iterating review->fix->re-review until ALL severity groups reach 0 — every severity must be resolved before the wave closes",
			ShouldNot: "proceed to UAT with non-zero findings or route any review severity (IMPORTANT/MINOR) to the FOLLOWUP epic",
		},
		// R7/A1: code review has no cycle cap; iterate until 0/0/0 clean.
		// Resolves to SharedFragmentSpecs[FragReviewCleanExit] (SLICE-1).
		behaviorRef(FragReviewCleanExit),
	},
	Sections: []ProseSection{
		{
			Id:      "sup-spawn-when-to-use",
			Title:   "When to Use",
			Content: `Implementation tasks ready. Ephemeral reviewers will be spawned per-slice during review phase.`,
		},
		{
			Id:    "sup-spawn-ride-the-wave-overview",
			Title: "Ride the Wave — Overview",
			Content: "The supervisor executes Phases 8-10 as a single coordinated cycle called **Ride the Wave**:\n" +
				"\n" +
				"```\n" +
				"1. PLAN  → supervisor-plan-tasks: decompose into slices + integration points\n" +
				"2. EXPLORE → Ephemeral Explore subagents (Task tool): map codebase, short-lived\n" +
				"3. BUILD → N Workers: implement slices in parallel\n" +
				"4. REVIEW → Ephemeral reviewers (Task tool): review per-slice\n" +
				"5. FIX   → Workers fix BLOCKERs + IMPORTANTs with atomic commits\n" +
				"6. RE-REVIEW → Spawn new ephemeral reviewers for re-review\n" +
				"7. REPEAT → Steps 5-6 with NO cycle cap until a fix-free clean round confirms 0/0/0\n" +
				"8. TRACK → ALL severities (BLOCKER/IMPORTANT/MINOR) must reach 0 — none route to FOLLOWUP\n" +
				"9. NEXT  → When fix-free clean (0 BLOCKER + 0 IMPORTANT + 0 MINOR) → Phase 11 (UAT); escalate to architect only if genuinely stuck\n" +
				"```\n" +
				"\n" +
				"**Key rules:**\n" +
				"- Reviewers are ephemeral (spawned per review cycle via Task tool)\n" +
				"- Slices are **never closed** until reviewed at least once\n" +
				"- **No cycle cap** — iterate review→fix→re-review until 0 BLOCKER + 0 IMPORTANT + 0 MINOR on a fix-free round; escalate to architect only if genuinely stuck\n" +
				"- The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items, never by review severities",
		},
		{
			Id:    "sup-spawn-handoff-template",
			Title: "Handoff Template (Supervisor → Worker)",
			Content: "Before spawning each worker, author its handoff in the slice (or a dedicated handoff) Beads task body:\n" +
				"\n" +
				"**Storage:** the Beads task body IS the handoff — no filesystem path.\n" +
				"\n" +
				"```markdown\n" +
				"# Handoff: Supervisor → Worker <N>\n" +
				"\n" +
				"## Context\n" +
				"- Request: <request-task-id>\n" +
				"- URD: <urd-task-id>\n" +
				"- IMPL_PLAN: <impl-plan-task-id>\n" +
				"- Ratified Proposal: <proposal-task-id>\n" +
				"\n" +
				"## Your Slice\n" +
				"- Slice: SLICE-<N>\n" +
				"- Task ID: <slice-task-id>\n" +
				"- Production Code Path: <what end users run>\n" +
				"\n" +
				"## Key Files\n" +
				"| File | What You Own |\n" +
				"|------|-------------|\n" +
				"| pkg/feature/types.go | ListOptions, ListEntry types |\n" +
				"| cmd/feature/list_test.go | List command tests |\n" +
				"| pkg/feature/service.go | ListItems() method |\n" +
				"| cmd/feature/list.go | list subcommand wiring |\n" +
				"\n" +
				"## Implementation Order\n" +
				"1. Layer 1: Types (your slice only)\n" +
				"2. Layer 2: Tests (import production code — will FAIL, expected)\n" +
				"3. Layer 3: Implementation + Wiring (make tests PASS)\n" +
				"\n" +
				"## Validation Checklist\n" +
				"- [ ] Production code verified via code inspection\n" +
				"- [ ] Tests import actual CLI (not test-only export)\n" +
				"- [ ] No dual-export anti-pattern\n" +
				"- [ ] No TODO placeholders\n" +
				"- [ ] Service wired with real dependencies\n" +
				"\n" +
				"## Persistence\n" +
				"Do NOT shut down after implementation. You will receive review feedback\n" +
				"and may need to fix BLOCKERs and IMPORTANT findings. Stay alive for the\n" +
				"full Ride the Wave cycle.\n" +
				"```",
		},
		{
			Id:    "sup-spawn-task-call",
			Title: "Task Call",
			Content: "```\n" +
				"Task({\n" +
				"  description: \"Worker: implement SLICE-N\",\n" +
				"  prompt: `Call Skill(/pasture:worker) and implement the assigned slice.\n" +
				"\n" +
				"Beads Task ID: <task-id>\n" +
				"Read full requirements + handoff: bd show <task-id>\n" +
				"\n" +
				"Do NOT shut down after implementation. You will receive review feedback and may need to fix issues.`,\n" +
				"  subagent_type: \"general-purpose\",\n" +
				"  run_in_background: true\n" +
				"})\n" +
				"```\n" +
				"\n" +
				"Per [sup-spawn-workers], use `subagent_type: \"general-purpose\"`, not a custom agent type. The worker skill is invoked inside the agent via `Skill(/pasture:worker)`.",
		},
		{
			Id:    "sup-spawn-teamcreate-sendmessage",
			Title: "TeamCreate: SendMessage Assignment",
			Content: "When workers are spawned via TeamCreate, they receive context through SendMessage instead of a Task prompt. The message MUST be self-contained — teammates have **no prior context**:\n" +
				"\n" +
				"```\n" +
				"SendMessage({\n" +
				"  type: \"message\",\n" +
				"  recipient: \"worker-1\",\n" +
				"  content: `You are assigned SLICE-1. Start by calling Skill(/pasture:worker).\n" +
				"\n" +
				"Your Beads task ID: <slice-task-id>\n" +
				"Run this to get full requirements + handoff: bd show <slice-task-id>\n" +
				"\n" +
				"Key references (run bd show on each for full context):\n" +
				"- Request: <request-task-id>\n" +
				"- URD: <urd-task-id>\n" +
				"- IMPL_PLAN: <impl-plan-task-id>\n" +
				"- Ratified Proposal: <proposal-task-id>\n" +
				"\n" +
				"Read the handoff doc and your Beads task before starting implementation.\n" +
				"\n" +
				"IMPORTANT: Do NOT shut down after completing implementation. You will receive\n" +
				"review feedback from ephemeral reviewers and may need to fix BLOCKERs and IMPORTANT\n" +
				"findings. Stay alive for the full Ride the Wave cycle.`,\n" +
				"  summary: \"SLICE-1 assignment with Beads context\"\n" +
				"})\n" +
				"```\n" +
				"\n" +
				"Per [sup-teamcreate-msg], include Beads task IDs and `bd show` commands in every assignment. Teammates cannot see your conversation or task tree.",
		},
		{
			Id:    "sup-spawn-worker-persistence",
			Title: "Worker Persistence (Ride the Wave)",
			Content: "Per [sup-worker-persistence], workers stay alive for the full review-fix cycle:\n" +
				"\n" +
				"1. Worker completes slice → notifies supervisor\n" +
				"2. Supervisor does **NOT** close the slice or shut down the worker\n" +
				"3. Ephemeral reviewers review the slice\n" +
				"4. If ANY findings (BLOCKER/IMPORTANT/MINOR): supervisor sends fix assignment to worker\n" +
				"5. Worker fixes issues → notifies supervisor\n" +
				"6. New ephemeral reviewers re-review\n" +
				"7. Repeat steps 4-6 with NO cycle cap until a fix-free clean round confirms 0/0/0\n" +
				"8. After a fix-free clean round (0 BLOCKER + 0 IMPORTANT + 0 MINOR): supervisor shuts down worker",
			Subsections: []ProseSection{
				{
					Id:    "sup-spawn-fix-assignment-template",
					Title: "Fix Assignment Message Template",
					Content: "```\n" +
						"SendMessage({\n" +
						"  type: \"message\",\n" +
						"  recipient: \"worker-1\",\n" +
						"  content: `Review cycle <N> found issues in your slice (SLICE-1).\n" +
						"\n" +
						"BLOCKERs (must fix — blocks slice closure):\n" +
						"- <finding-id>: <description> (bd show <finding-id>)\n" +
						"\n" +
						"IMPORTANT (must fix — must reach 0 before wave close):\n" +
						"- <finding-id>: <description> (bd show <finding-id>)\n" +
						"\n" +
						"MINOR (must fix — must reach 0 before wave close):\n" +
						"- <finding-id>: <description> (bd show <finding-id>)\n" +
						"\n" +
						"After fixing all items:\n" +
						"  bd comments add <slice-id> \"Fixes applied for review cycle <N>\"\n" +
						"\n" +
						"Do NOT shut down. Ephemeral reviewers will re-review.`,\n" +
						"  summary: \"Review cycle <N> fixes for SLICE-1\"\n" +
						"})\n" +
						"```",
				},
			},
		},
		{
			Id:    "sup-spawn-beads-status",
			Title: "Worker Should Update Beads Status",
			Content: "- On start: `bd update <task-id> --status=in_progress`\n" +
				"- On implementation complete (NOT slice close): `bd comments add <task-id> \"Implementation complete, awaiting review\"`\n" +
				"- On blocked: `bd update <task-id> --notes=\"Blocked: <reason>\"`\n" +
				"- Slice closure: **only the supervisor** closes slices after review passes",
		},
		{
			Id:    "sup-spawn-assign-via-beads",
			Title: "Assign via Beads",
			Content: "```bash\n" +
				"bd update <task-id> --assignee=\"<worker-agent-name>\"\n" +
				"bd update <task-id> --status=in_progress\n" +
				"```",
		},
		{
			Id:    "sup-spawn-followup-slice-handoff",
			Title: "Follow-up Slice Handoff (FOLLOWUP_SLICE-N)",
			Content: "For follow-up slices, the handoff (authored in the Beads task body — no filesystem path) extends with additional fields:\n" +
				"\n" +
				"```markdown\n" +
				"# Handoff: Supervisor → Worker <N> (Follow-up)\n" +
				"\n" +
				"## Context\n" +
				"- Original Request: <request-task-id>\n" +
				"- Follow-up Epic: <followup-epic-id>\n" +
				"- FOLLOWUP_URD: <followup-urd-id>\n" +
				"- FOLLOWUP_IMPL_PLAN: <followup-impl-plan-id>\n" +
				"\n" +
				"## Your Slice\n" +
				"- Slice: FOLLOWUP_SLICE-<N>\n" +
				"- Task ID: <slice-task-id>\n" +
				"\n" +
				"## DEFER'd Items (from UAT)\n" +
				"| Item Task ID | Source UAT | Description |\n" +
				"|---|---|---|\n" +
				"| <item-id-1> | <uat-id> | <user-DEFER'd item description> |\n" +
				"| <item-id-2> | <uat-id> | <user-DEFER'd item description> |\n" +
				"\n" +
				"## Acceptance Criteria\n" +
				"- All DEFER'd items in this slice resolved (tests pass, production code path verified)\n" +
				"- See bd task <slice-task-id> for full validation_checklist\n" +
				"```",
		},
	},
}

// ─── workerBody ──────────────────────────────────────────────────────────────

var workerBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9`,
	Sections: []ProseSection{
		{
			Id:    "wrk-what-you-own",
			Title: "Vertical Slice Ownership in Practice",
			Content: `**Example vertical slice: "CLI command with list subcommand"**
- **Production code path:** ` + "`" + `./bin/cli-tool command list` + "`" + ` (what end users run)
- **You own (within each file):**
  - Types: ` + "`" + `ListOptions` + "`" + `, ` + "`" + `ListEntry` + "`" + ` (in pkg/feature/types.go)
  - Tests: list_test.go (importing actual CLI command package)
  - Service: ` + "`" + `ListItems()` + "`" + ` method (in pkg/feature/service.go)
  - CLI wiring: ` + "`" + `listCmd` + "`" + ` cobra command RunE handler (in cmd/feature/list.go)

**Key insight:** You own the FEATURE end-to-end, not a layer or file.`,
		},
		{
			Id:    "wrk-planning-backwards",
			Title: "Planning Backwards from Production Code Path",
			Content: `**Start from the end, plan backwards:**

1. **Identify your production code path:**
   ` + "```bash" + `
   bd show <task-id>  # Look for "productionCodePath" field
   # Example: "cli-tool command list"
   # This is what end users will actually run
   ` + "```" + `

2. **Plan backwards from that end point:**
   ` + "```" + `
   End: User runs ./bin/cli-tool command list
     ↓ (what code handles this?)
   Entry: commandCli.command('list').action(async (options) => { ... })
     ↓ (what service does this call?)
   Service: createFeatureService({ fs, logger, parser, ... })
     ↓ (what method?)
   Method: await service.listItems(options)
     ↓ (what types does method need?)
   Types: ListOptions (input), ListEntry[] (output)
   ` + "```" + `

3. **Identify what you own in each layer:**
   - **L1 Types:** Which types does your slice need?
   - **L2 Tests:** How will you test the production code path?
   - **L3 Implementation + Wiring:** What service methods + CLI wiring needed?

4. **Verify no dual-export anti-pattern:**
   - Your tests must import the same code users run
   - Not a separate test-only function
   - When tests pass, production must work (same code path)`,
		},
		{
			Id:    "wrk-implementation-order",
			Title: "Implementation Order (Layers Within Your Slice)",
			Content: `You implement your vertical slice in layers (TDD approach):

**Layer 1: Types** (only what your slice needs)
` + "```go" + `
// pkg/feature/types.go
// Only add types for YOUR slice (e.g., list command)
type ListOptions struct { /* ... */ }
type ListEntry struct { /* ... */ }
// Don't add types for other slices (e.g., DetailView for other commands)
` + "```" + `

**Layer 2: Tests** (importing production code)
` + "```go" + `
// cmd/feature/list_test.go
package feature_test

import (
    "testing"
    "myproject/cmd/feature"
)

func TestFeatureList(t *testing.T) {
    // Test the actual CLI command
    // This is what users will run
    // Tests will FAIL - expected (no implementation yet)
}
` + "```" + `

Per [B-worker-test-production-code]:
` + "```go" + `
// ✅ CORRECT: Import actual CLI package
import "myproject/cmd/feature"

// ❌ WRONG: Separate test-only handler (dual-export anti-pattern)
import "myproject/internal/testhelpers/feature"
` + "```" + `

**Layer 3: Implementation + Wiring** (make tests pass)
` + "```go" + `
// pkg/feature/service.go
type FeatureServiceDeps struct {
    FS     afero.Fs
    Logger *slog.Logger
}

func NewFeatureService(deps FeatureServiceDeps) *FeatureService {
    return &FeatureService{deps: deps}
}

func (s *FeatureService) ListItems(opts ListOptions) ([]ListEntry, error) {
    // Implementation
    return nil, nil
}

// cmd/feature/list.go
var listCmd = &cobra.Command{
    Use:   "list",
    Short: "List items",
    RunE: func(cmd *cobra.Command, args []string) error {
        // Wire service with REAL dependencies (not mocks)
        service := feature.NewFeatureService(feature.FeatureServiceDeps{
            FS:     osFS{},
            Logger: slog.Default(),
        })

        limit, _ := cmd.Flags().GetInt("limit")
        format, _ := cmd.Flags().GetString("format")
        result, err := service.ListItems(feature.ListOptions{
            Limit:  limit,
            Format: format,
        })
        if err != nil {
            return err
        }

        fmt.Println(formatList(result, format))
        return nil
    },
}
` + "```" + `

Per [wrk-no-stubs], deliver fully wired production code.`,
		},
		{
			Id:    "wrk-tdd-layer-awareness",
			Title: "TDD Layer Awareness (Within Your Slice)",
			Content: `**Layer 2 (your tests):**
- Your tests WILL fail - implementation doesn't exist yet
- This is correct and expected
- Tests import actual production code (CLI command)
- Test failure is OK in Layer 2; typecheck must pass

**Layer 3 (your implementation + wiring):**
- Failing tests from Layer 2 are your specification
- Your job is to make those tests pass
- Wire production code with real dependencies
- Run tests - your tests should now PASS
- If tests fail for unrelated code (other workers' slices), that's OK

**Key insight:** A failing test for unimplemented code is NOT a blocker - it's the specification you're implementing against.`,
		},
		{
			Id:    "wrk-reading-from-beads",
			Title: "Reading from Beads",
			Content: `Get your task details:
` + "```bash" + `
bd show <task-id>
` + "```" + `

Look for:
- ` + "`productionCodePath`" + `: What end users will run (e.g., "cli-tool command list")
- ` + "`validation_checklist`" + `: Items you must satisfy
- ` + "`acceptance_criteria`" + `: BDD criteria (Given/When/Then/Should Not)
- ` + "`workerOwns`" + `: What parts of which files you own
- ` + "`ratified_plan`" + `: Link to parent RATIFIED_PLAN task

Update status on start:
` + "```bash" + `
bd update <task-id> --status=in_progress
` + "```",
		},
		{
			Id:    "wrk-vertical-slice-fields",
			Title: "Vertical Slice Fields (From Beads Task)",
			Content: `- ` + "`slice`" + `: Your slice identifier (e.g., "feature-list")
- ` + "`productionCodePath`" + `: What users run (e.g., "cli-tool command list")
- ` + "`workerOwns.types`" + `: Which types you create
- ` + "`workerOwns.tests`" + `: Which test files you write
- ` + "`workerOwns.implementation`" + `: Which methods/actions you implement
- ` + "`validation_checklist`" + `: Items you must verify (includes production code works)
- ` + "`acceptance_criteria`" + `: BDD criteria for your slice
- ` + "`ratified_plan`" + `: Link to parent plan`,
		},
		{
			Id:    "wrk-followup-slices",
			Title: "Follow-up Slices (FOLLOWUP_SLICE-N)",
			Content: `You may be assigned a ` + "`FOLLOWUP_SLICE-N`" + ` task instead of a ` + "`SLICE-N`" + ` task. The implementation procedure is identical, with these additions:

- **DEFER'd-item leaf tasks**: Your slice task will list specific user-DEFER'd UAT-item leaf tasks that you must resolve. Check ` + "`bd show <task-id>`" + ` for a "DEFER'd-Item Leaf Tasks" section.
- **Dual-parent resolution**: Each leaf task is a child of both the DEFER'd-items tracking group AND your FOLLOWUP_SLICE-N. Resolving the leaf task satisfies both parents.
- **Completion handoff (h4)**: When completing a follow-up slice, your handoff to the reviewer must list which DEFER'd-item leaf tasks were resolved.

` + "```bash" + `
# Completion comment for follow-up slices should include:
bd comments add <task-id> "Implementation complete. Resolved DEFER'd-item leaf tasks: <leaf-task-id-1>, <leaf-task-id-2>"
` + "```",
		},
		{
			Id:    "wrk-updating-beads-status",
			Title: "Updating Beads Status",
			Content: `On start:
` + "```bash" + `
bd update <task-id> --status=in_progress
` + "```" + `

On complete:
` + "```bash" + `
bd update <task-id> --status=done
bd update <task-id> --notes="Implementation complete. Production code verified working via code inspection."
` + "```" + `

On blocked:
` + "```bash" + `
bd update <task-id> --status=blocked
bd update <task-id> --notes="Blocked: <reason>. Need: <dependency or clarification>"
` + "```",
		},
	},
	Behaviors: []BehaviorSpec{
		{
			Id:        "wrk-no-stubs",
			Given:     "completing Layer 3 (implementation + wiring)",
			When:      "finishing a vertical slice",
			Then:      "deliver production code that is fully wired and working end-to-end",
			ShouldNot: "leave TODO placeholders, test-only exports, or unimplemented stubs",
		},
	},
}

// ─── architectBody ───────────────────────────────────────────────────────────

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

// ─── reviewerBody ────────────────────────────────────────────────────────────

var reviewerBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)**",
	Sections: []ProseSection{
		{
			Id:    "rev-plan-vs-code",
			Title: "Plan Review vs Code Review",
			Content: `| Aspect | Plan Review (Phase 4) | Code Review (Phase 10) |
|--------|-----------------------|------------------------|
| Label | ` + "`pasture:p4-plan:s4-review`" + ` | ` + "`pasture:p10-impl:s10-review`" + ` |
| Vote | ACCEPT / REVISE (binary) | ACCEPT / REVISE (binary) |
| Severity tree | **NO** — no severity groups | **YES** — EAGER creation (always 3 groups) |
| Naming | PROPOSAL-N-REVIEW-{axis}-{round} | SLICE-N-REVIEW-{axis}-{round} |
| Focus | End-user alignment, MVP scope | Production code paths, severity findings |`,
		},
		{
			Id:    "rev-end-user-alignment",
			Title: "End-User Alignment Criteria",
			Content: `All reviewers also apply these general questions:

1. **Who are the end-users?**
2. **What would end-users want?**
3. **How would this affect them?**
4. **Are there implementation gaps?**
5. **Does MVP scope make sense?**
6. **Is validation checklist complete and correct?**`,
		},
		fragRef(FragRevVoteOptions),
		{
			Id:    "rev-severity-vocab",
			Title: "Severity Vocabulary (Code Review Only)",
			Content: `| Severity | When to Use | Blocks Slice? |
|----------|-------------|---------------|
| BLOCKER | Security, type errors, test failures, broken production code paths | Yes |
| IMPORTANT | Performance, missing validation, architectural concerns | No (follow-up epic) |
| MINOR | Style, optional optimizations, naming improvements | No (follow-up epic) |`,
		},
		{
			Id:    "rev-followup-lifecycle",
			Title: "Follow-up Lifecycle Reviews",
			Content: `Reviewers also participate in the follow-up lifecycle:

- **FOLLOWUP_PROPOSAL review (Phase 4):** Same procedure as standard plan review. Task naming: ` + "`FOLLOWUP_PROPOSAL-N-REVIEW-{axis}-{round}`" + `. Binary ACCEPT/REVISE, no severity tree.
- **FOLLOWUP_SLICE code review (Phase 10):** Same procedure as standard code review. Task naming: ` + "`FOLLOWUP_SLICE-N-REVIEW-{axis}-{round}`" + `. Full EAGER severity tree (BLOCKER/IMPORTANT/MINOR).
- **All severities reach 0 (no followup-of-followup):** ALL findings (BLOCKER/IMPORTANT/MINOR) from a FOLLOWUP_SLICE code review must reach 0 before the follow-up wave closes — they are never re-routed to a follow-up epic. The FOLLOWUP epic is fed only by user-DEFER'd UAT items.`,
		},
		{
			Id:    "rev-beads-process",
			Title: "Beads Review Process",
			Content: `Read the plan and URD:
` + "```bash\n" +
				`bd show <task-id>
bd show <urd-id>   # Read URD for user requirements context
` + "```" + `

Add review comment with vote:
` + "```bash\n" +
				`# If accepting:
bd comments add <task-id> "VOTE: ACCEPT - End-user impact clear. MVP scope appropriate. Checklist items verifiable."

# If requesting revision:
bd comments add <task-id> "VOTE: REVISE - Missing: what happens if X fails? Suggestion: add error handling to checklist."
` + "```",
		},
		{
			Id:    "rev-consensus",
			Title: "Consensus",
			Content: `All 3 reviewers must vote ACCEPT for plan to be ratified. If any reviewer votes REVISE:
1. Architect creates PROPOSAL-N+1 addressing feedback
2. Old proposal marked ` + "`pasture:superseded`" + `
3. Reviewers re-review new proposal
4. Repeat until all ACCEPT`,
		},
	},
	Behaviors: []BehaviorSpec{
		{
			Id:        "rev-review-task-creation",
			Given:     "review complete",
			When:      "documenting findings",
			Then:      "create review task with dependency chain linking findings to the reviewed artifact",
			ShouldNot: "vote without creating a review task",
		},
	},
}

// ─── implReviewBody ──────────────────────────────────────────────────────────

var implReviewBody = SkillBody{
	Preamble: `Conduct code review across ALL implementation slices. Each of 3 reviewers reviews every slice.

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-10-code-review)** <- Phase 10

See ` + "`../protocol/CONSTRAINTS.md`" + ` for coding standards and severity definitions.`,
	// Behaviors shared with supervisor body via SharedFragmentSpecs (SLICE-3):
	// all 6 review-wave behaviors are now fragments so the canonical text is
	// single-sourced and both skill bodies render byte-identical content.
	Behaviors: []BehaviorSpec{
		behaviorRef(FragSupReviewAllSlices),
		behaviorRef(FragSupReviewCheckEach),
		behaviorRef(FragSupReviewSeverityGroups),
		behaviorRef(FragSupBlockerDualParent),
		behaviorRef(FragSupDeferredFollowup),
		behaviorRef(FragSupFollowupEpicTiming),
	},
	Sections: []ProseSection{
		// fragRef resolves to severity-tree prose; naming-convention is its embedded Subsection
		// (renders as H3 via the template's Subsections iteration — see Part 6 of worker-b impl-plan).
		fragRef(FragSupSeverityTree),
		{
			Id:    "impl-rev-dual-parent",
			Title: "Dual-Parent BLOCKER Relationship",
			Content: `BLOCKER findings have **two parents**:
1. The severity group task (` + "`pasture:severity:blocker`" + `) — for categorization
2. The slice they block — for dependency tracking

` + "```bash\n" +
				`# Create a BLOCKER finding
FINDING_ID=$(bd create --title "BLOCKER: Missing error handling in auth flow" \
  --labels "pasture:p10-impl:s10-review" \
  --description "---
references:
  slice: <slice-1-id>
  reviewer: reviewer-A
  round: 1
---
Missing error handling causes silent failure in auth flow.")

# Wire dual-parent: finding blocks BOTH severity group AND slice
bd dep add $BLOCKER_ID --blocked-by $FINDING_ID
bd dep add <slice-1-id> --blocked-by $FINDING_ID
` + "```\n" +
				`
Per [frag--sup-deferred-followup], IMPORTANT/MINOR findings attach to their severity group only (they do **not** block the slice via dual-parent), but ALL severity groups (BLOCKER/IMPORTANT/MINOR) must reach 0 before the review wave closes — they are **never** routed to the FOLLOWUP epic. The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items.

` + "```bash\n" +
				`# IMPORTANT finding — attaches to the IMPORTANT severity group (NOT the slice)
IMPORTANT_FINDING_ID=$(bd create --title "IMPORTANT: Add request timeout" \
  --labels "pasture:p10-impl:s10-review" \
  --description "---
references:
  slice: <slice-1-id>
  reviewer: reviewer-A
  round: 1
---
API calls should have configurable timeouts.")

# Attaches to the IMPORTANT severity group (NOT the slice); the group must still reach 0
bd dep add $IMPORTANT_ID --blocked-by $IMPORTANT_FINDING_ID
` + "```",
		},
		{
			Id:    "impl-rev-review-structure",
			Title: "Review Structure",
			Content: `Each reviewer (A, B, C) reviews EVERY slice:

` + "```\n" +
				`Reviewer A (Correctness): Reviews SLICE-1, SLICE-2, SLICE-3 →
  Creates: SLICE-1-REVIEW-A-1, SLICE-2-REVIEW-A-1, SLICE-3-REVIEW-A-1
  Each review has 3 severity groups (BLOCKER/IMPORTANT/MINOR)

Reviewer B (Test quality): Reviews SLICE-1, SLICE-2, SLICE-3 →
  Creates: SLICE-1-REVIEW-B-1, SLICE-2-REVIEW-B-1, SLICE-3-REVIEW-B-1

Reviewer C (Elegance): Reviews SLICE-1, SLICE-2, SLICE-3 →
  Creates: SLICE-1-REVIEW-C-1, SLICE-2-REVIEW-C-1, SLICE-3-REVIEW-C-1
` + "```",
		},
		{
			Id:    "impl-rev-spawning",
			Title: "Spawning Reviewers",
			Content: `Supervisor spawns 3 parallel reviewers as **subagents** (via the Task tool) or via **TeamCreate**. Reviewers are short-lived — keep them in-session.

` + "```\n" +
				`// Spawn 3 reviewers (one per axis)
Task({
  subagent_type: "general-purpose",
  run_in_background: true,
  prompt: ` + "`" + `You are Reviewer A (Correctness).
URD: <urd-id> (read with bd show <urd-id> for user requirements context)
Focus: Does implementation faithfully serve the user? Are technical decisions consistent with rationale?
Review ALL slices: <slice-1-id>, <slice-2-id>, <slice-3-id>
For each slice, run: bd show <slice-id>
Create severity groups (BLOCKER/IMPORTANT/MINOR) for each slice. Title: SLICE-N-REVIEW-A-1
Call Skill(/pasture:reviewer-review-code) for the review procedure.` + "`" + `
})
` + "```\n" +
				`
**Handoff:** Before spawning each reviewer, author its handoff in a Beads task body (the task body IS the handoff — no filesystem path).`,
			Subsections: []ProseSection{
				{
					Id:    "impl-rev-handoff-template",
					Title: "Supervisor → Reviewer Handoff Template",
					Content: "```markdown\n" +
						`# Handoff: Supervisor → Reviewer <N>

## Context
- Request: <request-task-id>
- URD: <urd-task-id>
- IMPL_PLAN: <impl-plan-task-id>
- Ratified Proposal: <proposal-task-id>

## Slices to Review
| Slice | Task ID | Description | Worker |
|-------|---------|-------------|--------|
| SLICE-1 | <id> | <description> | worker-1 |
| SLICE-2 | <id> | <description> | worker-2 |

## Review Procedure
1. For each slice: ` + "`bd show <slice-id>`" + `
2. Create 3 severity groups per slice (EAGER)
3. Add findings as children of severity groups
4. BLOCKER findings: dual-parent (severity group + slice)
5. Close empty severity groups immediately
6. Vote ACCEPT or REVISE per slice
` + "```",
				},
			},
		},
		{
			Id:    "impl-rev-criteria",
			Title: "Review Criteria",
			Content: `Each reviewer checks each slice for:

1. **Requirements Alignment (check URD)**
   - Does implementation match ratified plan?
   - Are all acceptance criteria met?
   - Read URD (` + "`bd show <urd-id>`" + `) for requirements traceability

2. **User Vision (check URD)**
   - Does it fulfill the user's original request (as documented in URD)?
   - Does it match UAT expectations?

3. **MVP Scope**
   - Is scope appropriate (not over/under engineered)?

4. **Codebase Quality**
   - Follows project style/constraints?
   - No TODO placeholders?
   - Tests import production code?

5. **Validation Checklist**
   - All items from slice checklist verified?`,
		},
		{
			Id:    "impl-rev-voting",
			Title: "Voting: ACCEPT vs REVISE (Binary Only)",
			Content: `| Vote | Requirement |
|------|-------------|
| **ACCEPT** | All 5 criteria satisfied; no BLOCKER items |
| **REVISE** | BLOCKER issues found; must provide actionable feedback |

**Documentation (via Beads comments):**
` + "```bash\n" +
				`bd comments add <slice-id> "VOTE: ACCEPT - [reason]"
# OR
bd comments add <slice-id> "VOTE: REVISE - [specific issue]. Suggest: [fix]"
` + "```",
		},
		{
			Id:    "impl-rev-consensus",
			Title: "Consensus Check",
			Content: `All reviews across all slices must be ACCEPT:

` + "```bash\n" +
				`# Check for any REVISE votes
bd list --labels="pasture:p10-impl:s10-review" --desc-contains "VOTE: REVISE"

# Check for unresolved BLOCKERs
bd list --labels="pasture:severity:blocker" --status=open

# If any REVISE or open BLOCKERs, return to implementation
# If all ACCEPT and BLOCKERs resolved, proceed to Phase 11 (UAT)
` + "```",
		},
		{
			Id:    "impl-rev-handling-revise",
			Title: "Handling REVISE",
			Content: `If any reviewer votes REVISE on any slice:

1. **Document issues** in the review task description
2. **Return slice to worker** for fixes
3. **Re-review** after fixes complete (new review round)

` + "```bash\n" +
				`# Mark slice as needing revision
bd comments add <slice-id> "REVISION NEEDED: <specific issues>"

# After worker fixes, start new review round
# New severity groups are created fresh for the new round
` + "```",
		},
		{
			Id:      "impl-rev-followup-epic",
			Title:   "Follow-up Epic (EPIC_FOLLOWUP)",
			Content: `Per [frag--sup-followup-epic-timing], create immediately after review completes.`,
			Subsections: []ProseSection{
				{
					Id:    "impl-rev-followup-step1",
					Title: "Step 1: Create the follow-up epic",
					Content: "```bash\n" +
						`bd create --type=epic --priority=3 \
  --title="FOLLOWUP: Non-blocking improvements from code review" \
  --description="---
references:
  request: <request-task-id>
  urd: <urd-task-id>
  review_round: <review-round-ids>
---
Aggregated IMPORTANT and MINOR findings from code review." \
  --add-label "pasture:epic-followup"

# Link IMPORTANT/MINOR severity groups
bd dep add <followup-epic-id> --blocked-by <important-group-id>
bd dep add <followup-epic-id> --blocked-by <minor-group-id>
` + "```",
				},
				{
					Id:    "impl-rev-followup-step2",
					Title: "Step 2: Follow-up lifecycle (same protocol, FOLLOWUP_* prefix)",
					Content: `The follow-up epic runs the same protocol phases with FOLLOWUP_* prefixed task types:

` + "```\n" +
						`FOLLOWUP epic (pasture:epic-followup)
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
` + "```\n" +
						`
` + "```bash\n" +
						`# Create follow-up lifecycle tasks
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
` + "```",
				},
				{
					Id:    "impl-rev-followup-step3",
					Title: "Step 3: DEFER'd-item leaf adoption (dual-parent)",
					Content: `When the supervisor creates FOLLOWUP_SLICE-N tasks, the user-DEFER'd UAT-item leaf tasks gain a second parent (dual-parent: leaf blocks BOTH the DEFER'd-items tracking group AND the follow-up slice):

` + "```bash\n" +
						`# Leaf task gets dual-parent: DEFER'd-items tracking group + follow-up slice
bd dep add <followup-slice-id> --blocked-by <deferred-item-leaf-id-1>
bd dep add <followup-slice-id> --blocked-by <deferred-item-leaf-id-2>
# Leaf task already has: bd dep add <deferred-items-tracking-group-id> --blocked-by <leaf-task-id>
` + "```",
				},
				{
					Id:    "impl-rev-followup-handoff",
					Title: "Followup Handoff (h5)",
					Content: `The h5 handoff (Reviewer → Supervisor, summary-with-ids) closes out the review wave. The FOLLOWUP epic itself is created later, at UAT, from the user-DEFER'd UAT items — **not** from review findings (all review severities reach 0 before the wave closes). Author this handoff in its Beads task body (no filesystem path):

` + "```markdown\n" +
						`# Handoff: Reviewer → Supervisor (review wave complete)

## Context
- Request: <request-task-id>
- URD: <urd-task-id>
- Ratified Proposal: <proposal-task-id>

## Review Outcome
- All slices reviewed; ALL severity groups (BLOCKER/IMPORTANT/MINOR) reached 0 on a fix-free clean round.

## Open Items
- None for this wave. Any user-DEFER'd UAT items feed the FOLLOWUP epic at Phase 11.
` + "```",
				},
				{
					Id:    "impl-rev-followup-chain",
					Title: "Follow-up Handoff Chain",
					Content: `Inside the follow-up lifecycle, the same handoff types (h1-h4) apply but scoped to the follow-up epic:

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

See ` + "`../protocol/HANDOFF_TEMPLATE.md`" + ` for full follow-up handoff examples and field requirements.`,
				},
			},
		},
		{
			Id:    "impl-rev-proceed-uat",
			Title: "Proceeding to UAT",
			Content: `Only when ALL reviews are ACCEPT and all BLOCKERs are resolved:

` + "```bash\n" +
				`# Verify consensus — no open BLOCKERs
bd list --labels="pasture:severity:blocker" --status=open
# Should return 0 results

# Proceed to Phase 11 (Implementation UAT)
Skill(/pasture:user-uat)
` + "```",
		},
	},
}
