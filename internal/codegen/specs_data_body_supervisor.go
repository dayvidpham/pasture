package codegen

func init() {
	SkillBodySpecs["supervisor"] = supervisorBody
}

var supervisorBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-8-implementation-plan)** <- Phases 7-12`,

	Behaviors: []BehaviorSpec{
		{
			ID:        "sup-assign-slices",
			Given:     "slices created",
			When:      "assigning",
			Then:      "use `bd update <slice-id> --assignee=\"worker-N\"` for assignment",
			ShouldNot: "leave slices unassigned",
		},
		{
			ID:        "sup-spawn-workers",
			Given:     "worker assignments",
			When:      "spawning",
			Then:      "use Task tool with `subagent_type: \"general-purpose\"` and `run_in_background: true`, worker MUST call `Skill(/aura:worker)` at start",
			ShouldNot: "spawn workers sequentially or use specialized agent types",
		},
		{
			ID:        "sup-teamcreate-msg",
			Given:     "teammates spawned via TeamCreate",
			When:      "assigning work via SendMessage",
			Then:      "the message MUST include: (1) explicit instruction to call `Skill(/aura:worker)`, (2) the Beads task ID, (3) instruction to run `bd show <task-id>` for full context, and (4) the handoff document path",
			ShouldNot: "send bare instructions without Beads context — teammates have no prior knowledge of the task",
		},
		{
			ID:        "sup-layer-integration-points",
			Given:     "multiple vertical slices",
			When:      "slices share types, interfaces, or data flows",
			Then:      "identify horizontal Layer Integration Points and document them in the IMPL_PLAN (owner, consumers, shared contract, merge timing)",
			ShouldNot: "leave cross-slice dependencies implicit — divergence grows when slices develop in isolation without clear merge points",
		},
		{
			ID:        "sup-followup-deps",
			Given:     "IMPORTANT or MINOR severity groups",
			When:      "linking dependencies",
			Then:      "link them to the FOLLOWUP epic only: `bd dep add <followup-epic-id> --blocked-by <important-group-id>`",
			ShouldNot: "link IMPORTANT or MINOR severity groups as blocking IMPL_PLAN or any slice — only BLOCKER findings block slices",
		},
		{
			ID:        "sup-review-all-slices",
			Given:     "all slices complete",
			When:      "starting review",
			Then:      "spawn 3 reviewers for ALL slices",
			ShouldNot: "assign reviewers to single slices",
		},
		{
			ID:        "sup-review-check-each",
			Given:     "reviewer assigned",
			When:      "reviewing",
			Then:      "check each slice against criteria",
			ShouldNot: "skip any slice",
		},
		{
			ID:        "sup-review-severity-groups",
			Given:     "review round",
			When:      "creating severity groups",
			Then:      "ALWAYS create 3 severity groups (BLOCKER, IMPORTANT, MINOR) per round even if empty",
			ShouldNot: "lazily create groups only when findings exist",
		},
		{
			ID:        "sup-blocker-dual-parent",
			Given:     "BLOCKER finding",
			When:      "wiring dependencies",
			Then:      "add dual-parent: blocks BOTH severity group AND slice",
			ShouldNot: "wire BLOCKER to only one parent",
		},
		{
			ID:        "sup-important-minor-followup",
			Given:     "IMPORTANT or MINOR finding",
			When:      "categorizing",
			Then:      "add to severity group only (NOT to slice) — these go to follow-up epic",
			ShouldNot: "block slices on non-BLOCKER findings",
		},
		{
			ID:        "sup-followup-epic-timing",
			Given:     "review complete with IMPORTANT/MINOR",
			When:      "finishing",
			Then:      "supervisor creates EPIC_FOLLOWUP immediately (NOT gated on BLOCKER resolution)",
			ShouldNot: "wait for BLOCKERs to resolve before creating follow-up",
		},
	},

	Sections: []ProseSection{
		{
			ID:      "sup-ride-the-wave",
			Title:   "Ride the Wave — Operational Detail",
			Content: "",
			Subsections: []ProseSection{
				{
					ID:    "sup-stage1-plan",
					Title: "Stage 1: Plan _(sequential)_",
					Content: `- Read RATIFIED_PLAN and URD via ` + "`bd show`" + `
- Spawn ephemeral Explore subagents (Agent tool, ` + "`subagent_type=Explore`" + `) for scoped codebase queries — NOT standing teams
- Decompose into vertical slices with integration points
- Create leaf tasks (L1/L2/L3) for every slice`,
				},
				{
					ID:    "sup-stage2-build",
					Title: "Spawning the Wave — Stage 2: Build _(parallel)_",
					Content: `- Spawn workers as Agent tool subagents by default (` + "`subagent_type: \"general-purpose\"`" + `, ` + "`run_in_background: true`" + `)
- Use TeamCreate only for >=3 slices with shared integration points requiring SendMessage coordination
- Supervisor commits at integration points (atomic commits) — commit small and often
- Integrate early and often`,
				},
				{
					ID:    "sup-stage3-review",
					Title: "Stage 3: Review _(conditional-loop, per-slice)_",
					Content: `- Spawn 3 ephemeral reviewer subagents per round (same pattern as Phase 4 plan review)
- **CLEAN REVIEW** = 0 BLOCKERs + 0 IMPORTANTs from ALL reviewers
- Per-slice fix+review with independent cycle counters per slice
- Fix flow: Stage 3 (dirty review) -> Stage 2 (worker fixes) -> Stage 3 (re-review)
- Max 3 cycles per slice, then escalate to architect for re-planning
- **MUST end on a review wave** — cannot proceed after a worker wave without review

` + "```" + `text
Stage 3 Flow (per-slice):

  ┌─────────────────────────────────────────┐
  │ Spawn 3 ephemeral reviewers             │
  │ Review slice (severity: BLOCKER/IMP/MIN)│
  └──────────────┬──────────────────────────┘
                 │
          CLEAN? ├── YES → slice passes, proceed
                 │
                 └── NO (cycle < 3)
                       │
                       ▼
              ┌────────────────────┐
              │ Stage 2: worker    │
              │ fixes BLOCKERs +   │
              │ IMPORTANTs         │
              └────────┬───────────┘
                       │
                       ▼
              ┌────────────────────┐
              │ Stage 3: re-review │
              │ (new ephemeral     │
              │  reviewers)        │
              └────────┬───────────┘
                       │
                 cycle++ → loop
                       │
          3 cycles exhausted → escalate to architect
` + "```",
				},
			},
		},
		{
			ID:    "sup-first-steps",
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
			ID:    "sup-exploration",
			Title: "Exploration (Ephemeral Explore Subagents)",
			Content: `**The supervisor MUST NOT perform deep codebase exploration directly.** Instead, spawn ephemeral Explore subagents (Agent tool, ` + "`subagent_type=Explore`" + `) for scoped codebase queries. These are short-lived — they explore, return findings, and terminate. The supervisor stays lean.

` + "```" + `
// Explore subagent — ephemeral, scoped query
Task({
  subagent_type: "Explore",
  run_in_background: true,
  prompt: ` + "`" + `Call Skill(/aura:explore) to load your exploration role.

Query: <specific codebase question>
Depth: standard-research

Explore the codebase for the requested topic. Produce structured findings
(entry points, data flow, dependencies, patterns, conflicts). Return findings.` + "`" + `
})
` + "```" + `

Spawn as many Explore subagents as needed — they are cheap and disposable. Use them during Phase 8 (IMPL_PLAN) to understand codebase areas before decomposing into slices.`,
		},
		{
			ID:    "sup-reading-from-beads",
			Title: "Reading from Beads",
			Content: `Get the ratified plan and URD:
` + "```" + `bash
bd show <ratified-plan-id>
bd show <urd-id>
bd list --labels="aura:p6-plan:s6-ratify" --status=open
bd list --labels="aura:urd"
` + "```",
		},
		{
			ID:    "sup-impl-task-structure",
			Title: "Implementation Task Structure",
			Content: "```" + `go
type ImplementationTask struct {
    File            string          // file path
    TaskID          string          // Beads task ID (e.g., "aura-xxx")
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
			ID:      "sup-creating-vertical-slices",
			Title:   "Creating Vertical Slices (Phase 8)",
			Content: "",
			Subsections: []ProseSection{
				{
					ID:    "sup-step1-impl-plan",
					Title: "Step 1: Create the IMPL_PLAN task",
					Content: "```" + `bash
bd create --labels "aura:p8-impl:s8-plan" \
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
					ID:    "sup-step2-create-slices",
					Title: "Step 2: Create each slice",
					Content: "```" + `bash
bd create --labels "aura:p9-impl:s9-slice" \
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
					ID:    "sup-step3-leaf-tasks",
					Title: "Step 3: Create leaf tasks within each slice (CRITICAL)",
					Content: `**A slice without leaf tasks is undecomposed.** The supervisor MUST create Beads tasks for each implementation unit within the slice, then chain them as dependencies. Leaf tasks are what workers actually implement.

` + "```" + `bash
# L1: Types and interfaces for this slice
LEAF_L1=$(bd create --labels "aura:p9-impl:s9-slice" \
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
LEAF_L2=$(bd create --labels "aura:p9-impl:s9-slice" \
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
LEAF_L3=$(bd create --labels "aura:p9-impl:s9-slice" \
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
			ID:    "sup-assigning-slices",
			Title: "Assigning Slices",
			Content: "```" + `bash
# Assign slices to workers
bd update <slice-1-id> --assignee="worker-1"
bd update <slice-2-id> --assignee="worker-2"
bd update <slice-3-id> --assignee="worker-3"
` + "```",
		},
		{
			ID:    "sup-spawning-workers",
			Title: "Spawning Workers",
			Content: `**The supervisor NEVER implements changes directly.** All implementation work — no matter how small — is delegated to a worker agent. The supervisor's job is coordination, tracking, and quality control.

Workers are **general-purpose agents** that call ` + "`/aura:worker`" + ` at the start. Select the model based on task complexity:

` + "```" + `
// Non-trivial work → sonnet model
Task({
  subagent_type: "general-purpose",
  model: "sonnet",
  run_in_background: true,
  prompt: ` + "`" + `Call Skill(/aura:worker) and implement the assigned slice.\n\nBeads Task ID: ${taskId}...` + "`" + `
})

// Trivial work (config tweak, typo fix, single-file edit) → haiku model
Task({
  subagent_type: "general-purpose",
  model: "haiku",
  run_in_background: true,
  prompt: ` + "`" + `Call Skill(/aura:worker) and fix the typo in...\n\nBeads Task ID: ${taskId}...` + "`" + `
})

// WRONG: Supervisor implementing changes directly
Edit({ file_path: "src/foo.ts", ... })  // Supervisors coordinate, they don't implement!

// WRONG: Do not use specialized agent types like "aura:worker" directly
Task({
  subagent_type: "aura:worker",  // This doesn't exist!
  ...
})
` + "```",
			Subsections: []ProseSection{
				{
					ID:    "sup-model-selection",
					Title: "Model Selection Guide",
					Content: `| Complexity | Model | Examples |
|------------|-------|----------|
| Trivial | ` + "`haiku`" + ` | Single-file edit, config change, typo fix, renaming, adding a label |
| Non-trivial | ` + "`sonnet`" + ` | Multi-file changes, new features, architectural work, complex logic, test suites |

**Handoff:** Before spawning each worker, create a handoff document:
` + "```" + `
.git/.aura/handoff/<request-task-id>/supervisor-to-worker-<N>.md
` + "```" + `

See: [../supervisor-spawn-worker/SKILL.md](../supervisor-spawn-worker/SKILL.md) for handoff template.`,
				},
				{
					ID:    "sup-teamcreate-context",
					Title: "TeamCreate Context Requirements",
					Content: `When using TeamCreate instead of the Task tool, teammates have **zero prior context**. Every SendMessage assigning work MUST be self-contained:

` + "```" + `
SendMessage({
  type: "message",
  recipient: "worker-1",
  content: ` + "`" + `You are assigned SLICE-1. Start by calling Skill(/aura:worker).

Your Beads task ID: <slice-task-id>
Run this to get full requirements: bd show <slice-task-id>
Handoff document: .git/.aura/handoff/<request-task-id>/supervisor-to-worker-1.md

Key context:
- Request: <request-task-id> (run: bd show <request-task-id>)
- URD: <urd-task-id> (run: bd show <urd-task-id>)
- IMPL_PLAN: <impl-plan-task-id> (run: bd show <impl-plan-task-id>)

Read the handoff doc and your Beads task before starting implementation.` + "`" + `,
  summary: "SLICE-1 assignment with Beads context"
})
` + "```" + `

**Never assume teammates know anything.** They cannot see your conversation history, the Beads task tree, or any prior context. Every assignment must include actionable ` + "`bd show`" + ` commands.

The worker skill provides:
- File ownership validation
- Standard DI patterns
- Completion/blocked signaling via Beads`,
				},
			},
		},
		{
			ID:      "sup-epic-followup",
			Title:   "EPIC_FOLLOWUP Creation (Phase 10)",
			Content: `After code review completes, if ANY IMPORTANT or MINOR findings exist, create a follow-up epic.

**Trigger:** Review round completion + ANY IMPORTANT or MINOR findings exist.
**NOT gated on BLOCKER resolution.** Create as soon as review completes.`,
			Subsections: []ProseSection{
				{
					ID:    "sup-followup-step1",
					Title: "Step 1: Create follow-up epic",
					Content: "```" + `bash
bd create --type=epic --priority=3 \
  --title="FOLLOWUP: Non-blocking improvements from code review" \
  --description="---
references:
  request: <request-task-id>
  urd: <urd-task-id>
  review_round: <review-task-ids>
---
Aggregated IMPORTANT and MINOR findings from code review." \
  --add-label "aura:epic-followup"

# Link IMPORTANT/MINOR severity groups as children
bd dep add <followup-epic-id> --blocked-by <important-group-id>
bd dep add <followup-epic-id> --blocked-by <minor-group-id>
` + "```" + `

**Severity routing rules (CRITICAL):**
- BLOCKER severity groups → block the **slice** they apply to: ` + "`bd dep add <slice-id> --blocked-by <blocker-group-id>`" + `
- IMPORTANT severity groups → block the **FOLLOWUP epic** only: ` + "`bd dep add <followup-epic-id> --blocked-by <important-group-id>`" + `
- MINOR severity groups → block the **FOLLOWUP epic** only: ` + "`bd dep add <followup-epic-id> --blocked-by <minor-group-id>`" + `

**NEVER link IMPORTANT or MINOR severity groups as blocking IMPL_PLAN or any slice.** Only BLOCKER findings block the implementation path.`,
				},
				{
					ID:    "sup-followup-step2",
					Title: "Step 2: Follow-up lifecycle (same protocol, FOLLOWUP_* prefix)",
					Content: `The follow-up epic runs the same protocol phases with FOLLOWUP_* prefixed task types. The supervisor creates the initial lifecycle tasks:

` + "```" + `
FOLLOWUP epic (aura:epic-followup)
  ├── relates_to: original URD
  ├── relates_to: original REVIEW-A/B/C tasks
  └── blocked-by: FOLLOWUP_URE         (Phase 2: scope which findings to address)
        └── blocked-by: FOLLOWUP_URD   (Phase 2: requirements for follow-up)
              └── blocked-by: FOLLOWUP_PROPOSAL-1  (Phase 3: proposal for follow-up)
                    └── blocked-by: FOLLOWUP_IMPL_PLAN  (Phase 8: decompose into slices)
                          ├── blocked-by: FOLLOWUP_SLICE-1  (Phase 9)
                          │     ├── blocked-by: important-leaf-task-...
                          │     └── blocked-by: minor-leaf-task-...
                          └── blocked-by: FOLLOWUP_SLICE-2
` + "```" + `

` + "```" + `bash
# Create FOLLOWUP_URE — user scoping which findings to address
FOLLOWUP_URE_ID=$(bd create \
  --title "FOLLOWUP_URE: Scope follow-up for <feature>" \
  --labels "aura:p2-user:s2_1-elicit" \
  --description "---
references:
  followup_epic: <followup-epic-id>
  original_urd: <original-urd-id>
---
Scoping URE: determine which IMPORTANT/MINOR findings to address.")
bd dep add <followup-epic-id> --blocked-by $FOLLOWUP_URE_ID

# Create FOLLOWUP_URD — requirements for follow-up scope
FOLLOWUP_URD_ID=$(bd create \
  --title "FOLLOWUP_URD: Requirements for <feature> follow-up" \
  --labels "aura:p2-user:s2_2-urd,aura:urd" \
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
					ID:    "sup-followup-step3",
					Title: "Step 3: Leaf task adoption (dual-parent)",
					Content: `When the supervisor creates FOLLOWUP_SLICE-N tasks during the follow-up implementation phase, the IMPORTANT/MINOR leaf tasks from the original review gain a second parent:

` + "```" + `bash
# Leaf task gets dual-parent: original severity group + follow-up slice
bd dep add <followup-slice-id> --blocked-by <important-leaf-task-id>
bd dep add <followup-slice-id> --blocked-by <minor-leaf-task-id>
# Leaf task already has: bd dep add <severity-group-id> --blocked-by <leaf-task-id>
` + "```",
				},
				{
					ID:    "sup-followup-handoff-chain",
					Title: "Follow-up Handoff Chain",
					Content: `Inside the follow-up lifecycle, the same handoff types (h1-h4) reapply:

| Order | Handoff | Transition |
|-------|---------|------------|
| 1 | h5 | Reviewer → Followup: **Starts** the follow-up lifecycle |
| 2 | *(none)* | Supervisor creates FOLLOWUP_URE (same actor) |
| 3 | *(none)* | Supervisor creates FOLLOWUP_URD (same actor) |
| 4 | h6 | Supervisor → Architect: Hands off FOLLOWUP_URE + FOLLOWUP_URD for FOLLOWUP_PROPOSAL |
| 5 | h1 | Architect → Supervisor: After FOLLOWUP_PROPOSAL ratified |
| 6 | h2 | Supervisor → Worker: FOLLOWUP_SLICE-N with adopted leaf task IDs |
| 7 | h3 | Supervisor → Reviewer: Code review of follow-up slices |
| 8 | h4 | Worker → Reviewer: Follow-up slice completion |

Follow-up handoff storage: ` + "`.git/.aura/handoff/{followup-epic-id}/{source}-to-{target}.md`" + `

See ` + "`../protocol/HANDOFF_TEMPLATE.md`" + ` for full follow-up handoff examples, including Supervisor → Worker with adopted leaf task IDs.`,
				},
			},
		},
		{
			ID:      "sup-impl-review-severity",
			Title:   "Impl-Review Severity Tree Procedure",
			Content: "The following describes the full severity tree procedure for code review (Phase 10).",
			Subsections: []ProseSection{
				{
					ID:    "sup-severity-gwts",
					Title: "Given/When/Then/Should",
					Content: `**Given** all slices complete **when** starting review **then** spawn 3 reviewers for ALL slices **should never** assign reviewers to single slices

**Given** reviewer assigned **when** reviewing **then** check each slice against criteria **should never** skip any slice

**Given** review round **when** creating severity groups **then** ALWAYS create 3 severity groups (BLOCKER, IMPORTANT, MINOR) per round even if empty **should never** lazily create groups only when findings exist

**Given** BLOCKER finding **when** wiring dependencies **then** add dual-parent: blocks BOTH severity group AND slice **should never** wire BLOCKER to only one parent

**Given** IMPORTANT or MINOR finding **when** categorizing **then** add to severity group only (NOT to slice) — these go to follow-up epic **should never** block slices on non-BLOCKER findings

**Given** review complete with IMPORTANT/MINOR **when** finishing **then** supervisor creates EPIC_FOLLOWUP immediately (NOT gated on BLOCKER resolution) **should never** wait for BLOCKERs to resolve before creating follow-up`,
				},
				{
					ID:    "sup-severity-tree",
					Title: "Severity Tree (EAGER Creation)",
					Content: `**ALWAYS create 3 severity group tasks per review round**, even if some groups have no findings:

` + "```" + `bash
# Step 1: Create all 3 severity groups immediately (EAGER)
BLOCKER_ID=$(bd create --title "SLICE-1-REVIEW-A-1 BLOCKER" \
  --labels "aura:severity:blocker,aura:p10-impl:s10-review" \
  --description "---
references:
  slice: <slice-1-id>
  review_round: 1
---
BLOCKER findings from Reviewer A (Correctness) on SLICE-1.")

IMPORTANT_ID=$(bd create --title "SLICE-1-REVIEW-A-1 IMPORTANT" \
  --labels "aura:severity:important,aura:p10-impl:s10-review" \
  --description "---
references:
  slice: <slice-1-id>
  review_round: 1
---
IMPORTANT findings from Reviewer A (Correctness) on SLICE-1.")

MINOR_ID=$(bd create --title "SLICE-1-REVIEW-A-1 MINOR" \
  --labels "aura:severity:minor,aura:p10-impl:s10-review" \
  --description "---
references:
  slice: <slice-1-id>
  review_round: 1
---
MINOR findings from Reviewer A (Correctness) on SLICE-1.")

# Step 2: Wire severity groups to the review round task
bd dep add <review-round-id> --blocked-by $BLOCKER_ID
bd dep add <review-round-id> --blocked-by $IMPORTANT_ID
bd dep add <review-round-id> --blocked-by $MINOR_ID
# NEVER wire severity groups to IMPL_PLAN or slices directly.
# BLOCKER findings block slices via dual-parent (see below).
# IMPORTANT/MINOR route to FOLLOWUP epic only (see Follow-up Epic section).

# Step 3: Close empty groups immediately
# If a group has no findings, close it right away
bd close $IMPORTANT_ID   # if no IMPORTANT findings
bd close $MINOR_ID        # if no MINOR findings
` + "```",
				},
				{
					ID:    "sup-naming-convention",
					Title: "Naming Convention",
					Content: "```" + `
SLICE-{N}-REVIEW-{axis}-{round}
` + "```" + `

Where axis = A (Correctness), B (Test quality), C (Elegance).

Examples:
- ` + "`SLICE-1-REVIEW-A-1`" + ` — Reviewer A (Correctness), Round 1, SLICE-1
- ` + "`SLICE-2-REVIEW-C-2`" + ` — Reviewer C (Elegance), Round 2, SLICE-2

Severity groups:
- ` + "`SLICE-1-REVIEW-A-1 BLOCKER`" + `
- ` + "`SLICE-1-REVIEW-A-1 IMPORTANT`" + `
- ` + "`SLICE-1-REVIEW-A-1 MINOR`",
				},
			},
		},
		{
			ID:    "sup-tracking-progress",
			Title: "Tracking Progress",
			Content: "```" + `bash
# Check all implementation slices
bd list --labels="aura:p9-impl:s9-slice" --status=in_progress

# Check for blocked tasks
bd list --labels="aura:p9-impl:s9-slice" --status=blocked

# Check completed slices
bd list --labels="aura:p9-impl:s9-slice" --status=done

# Check specific task
bd show <task-id>

# Check severity groups from review
bd list --labels="aura:severity:blocker"
bd list --labels="aura:severity:important"
bd list --labels="aura:severity:minor"

# Check follow-up epics
bd list --labels="aura:epic-followup"
` + "```",
		},
	},

	Recipes: []RecipeBlock{},
}
