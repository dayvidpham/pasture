// Worker and architect skill body content for the Pasture codegen system.
//
// These vars encode the hand-authored body sections of:
//   - skills/worker/SKILL.md   (content after END GENERATED marker, ~line 253)
//   - skills/architect/SKILL.md (content after END GENERATED marker, ~line 309)
//
// Registered into SkillBodySpecs via init() to avoid merge conflicts with
// other slice files that register other roles.
package codegen

func init() {
	SkillBodySpecs["worker"] = workerBody
	SkillBodySpecs["architect"] = architectBody
}

var workerBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9`,
	Sections: []ProseSection{
		{
			ID:    "wrk-what-you-own",
			Title: "What You Own",
			Content: `**NOT:** A single file or horizontal layer (e.g., "all types" or "all tests")
**YES:** A full vertical slice (complete production code path end-to-end)

**Example vertical slice: "CLI command with list subcommand"**
- **Production code path:** ` + "`" + `./bin/cli-tool command list` + "`" + ` (what end users run)
- **You own (within each file):**
  - Types: ` + "`" + `ListOptions` + "`" + `, ` + "`" + `ListEntry` + "`" + ` (in pkg/feature/types.go)
  - Tests: list_test.go (importing actual CLI command package)
  - Service: ` + "`" + `ListItems()` + "`" + ` method (in pkg/feature/service.go)
  - CLI wiring: ` + "`" + `listCmd` + "`" + ` cobra command RunE handler (in cmd/feature/list.go)

**Key insight:** You own the FEATURE end-to-end, not a layer or file.`,
		},
		{
			ID:    "wrk-planning-backwards",
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
			ID:    "wrk-implementation-order",
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

**CRITICAL:** Tests must import production code, not test-only export:
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

**No TODO placeholders. No test-only exports. Production code wired and working.**`,
		},
		{
			ID:    "wrk-tdd-layer-awareness",
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
			ID:    "wrk-reading-from-beads",
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
			ID:    "wrk-vertical-slice-fields",
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
			ID:    "wrk-followup-slices",
			Title: "Follow-up Slices (FOLLOWUP_SLICE-N)",
			Content: `You may be assigned a ` + "`FOLLOWUP_SLICE-N`" + ` task instead of a ` + "`SLICE-N`" + ` task. The implementation procedure is identical, with these additions:

- **Adopted leaf tasks**: Your slice task will list specific IMPORTANT/MINOR leaf tasks from the original code review that you must resolve. Check ` + "`bd show <task-id>`" + ` for an "Adopted Leaf Tasks" section.
- **Dual-parent resolution**: The adopted leaf tasks are children of both the original severity group AND your FOLLOWUP_SLICE-N. Resolving the leaf task satisfies both parents.
- **Completion handoff (h4)**: When completing a follow-up slice, your handoff to the reviewer must list which original leaf tasks were resolved.

` + "```bash" + `
# Completion comment for follow-up slices should include:
bd comments add <task-id> "Implementation complete. Resolved leaf tasks: <leaf-task-id-1>, <leaf-task-id-2>"
` + "```",
		},
		{
			ID:    "wrk-updating-beads-status",
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
}

var architectBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-3-proposal-n)**`,
	Sections: []ProseSection{
		{
			ID:    "arch-proposal-naming",
			Title: "PROPOSAL-N Naming",
			Content: `Proposals are numbered incrementally: PROPOSAL-1, PROPOSAL-2, etc. When a revision is needed:
1. Create PROPOSAL-N+1 with fixes
2. Mark PROPOSAL-N as superseded:
   ` + "```bash" + `
   bd label add <old-proposal-id> aura:superseded
   bd comments add <old-proposal-id> "Superseded by PROPOSAL-N+1 (<new-proposal-id>)"
   ` + "```" + `
3. Re-spawn all 3 reviewers to assess PROPOSAL-N+1`,
		},
		{
			ID:      "arch-state-flow",
			Title:   "State Flow",
			Content: `Idle → Eliciting → Drafting → AwaitingReview → AwaitingUAT → Ratified → HandoffToSupervisor → Idle`,
		},
		{
			ID:      "arch-beads-task-creation",
			Title:   "Beads Task Creation (12-Phase)",
			Content: "",
			Subsections: []ProseSection{
				{
					ID:    "arch-phase1-request",
					Title: "Phase 1: REQUEST Task",
					Content: `Captures the original user prompt verbatim:
` + "```bash" + `
bd create --labels "aura:p1-user:s1_1-classify" \
  --title "REQUEST: <summary>" \
  --description "<verbatim user prompt - do not paraphrase>"
# Result: task-req
` + "```",
				},
				{
					ID:    "arch-phase2-elicit",
					Title: "Phase 2: ELICIT Task",
					Content: `Run ` + "`/aura:user-elicit`" + ` first, then capture results:
` + "```bash" + `
bd create --labels "aura:p2-user:s2_1-elicit" \
  --title "ELICIT: <feature>" \
  --description "<questions and user responses verbatim>"
bd dep add <request-id> --blocked-by <elicit-id>
# Result: task-eli
` + "```",
				},
				{
					ID:    "arch-phase2-5-urd",
					Title: "Phase 2.5: URD (User Requirements Document)",
					Content: `Create the URD as the single source of truth after elicitation:
` + "```bash" + `
bd create --labels "aura:urd,aura:p2-user:s2_2-urd" \
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
					ID:    "arch-phase3-proposal",
					Title: "Phase 3: PROPOSAL-N Task",
					Content: `Contains full plan with validation checklist and acceptance criteria:
` + "```bash" + `
bd create --labels "aura:p3-plan:s3-propose" \
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
					ID:    "arch-phase4-review",
					Title: "Phase 4: REVIEW Tasks",
					Content: `Each reviewer creates their own task:
` + "```bash" + `
bd create --labels "aura:p4-plan:s4-review" \
  --title "PROPOSAL-1-REVIEW-A-1: <feature>" \
  --description "VOTE: <ACCEPT|REVISE> - <justification>"
bd dep add <proposal-id> --blocked-by <review-id>
` + "```",
				},
				{
					ID:    "arch-phase5-uat",
					Title: "Phase 5: UAT Task",
					Content: `After all 3 reviewers ACCEPT, run ` + "`/aura:user-uat`" + `:
` + "```bash" + `
bd create --labels "aura:p5-user:s5-uat" \
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
					ID:    "arch-phase6-ratify",
					Title: "Phase 6: RATIFY",
					Content: `Add label to proposal (DO NOT close, delete, or create new task):
` + "```bash" + `
bd label add <proposal-id> aura:p6-plan:s6-ratify
bd comments add <proposal-id> "RATIFIED: All 3 reviewers ACCEPT, UAT passed (<uat-task-id>)"

# Mark all previous proposals as superseded
bd label add <old-proposal-id> aura:superseded
bd comments add <old-proposal-id> "Superseded by PROPOSAL-N (<ratified-proposal-id>)"

# Update URD with ratification
bd comments add <urd-id> "Ratified: scope confirmed as <summary>"
` + "```",
				},
				{
					ID:    "arch-phase7-handoff",
					Title: "Phase 7: HANDOFF",
					Content: `Create handoff document and task:
` + "```bash" + `
bd create --type=task --priority=2 \
  --title "HANDOFF: Architect → Supervisor for REQUEST" \
  --description "---
references:
  request: <request-id>
  urd: <urd-id>
  proposal: <ratified-proposal-id>
---
Handoff from architect to supervisor. See handoff document at
.git/.aura/handoff/<request-id>/architect-to-supervisor.md" \
  --add-label "aura:p7-plan:s7-handoff"
` + "```" + `

Storage: ` + "`.git/.aura/handoff/{request-task-id}/architect-to-supervisor.md`",
				},
			},
		},
		{
			ID:    "arch-plan-structure",
			Title: "Plan Structure",
			Content: "```markdown\n## Problem Space\n**Axes:** parallelism, distribution, reliability\n**Has-a / Is-a:** relationships\n\n## Engineering Tradeoffs\n| Option | Pros | Cons | Decision |\n\n## MVP Milestone\nScope with tradeoff rationale\n\n## Public Interfaces\n```go\ntype Example interface { /* ... */ }\n```\n\n## Validation Checklist\n- [ ] Item 1\n- [ ] Item 2\n\n## BDD Acceptance Criteria\n**Given** X **When** Y **Then** Z **Should Not** W\n```",
		},
		{
			ID:    "arch-followup-lifecycle",
			Title: "Follow-up Lifecycle (Receiving h6)",
			Content: `In the follow-up lifecycle, the architect receives a handoff (h6) from the supervisor containing FOLLOWUP_URE + FOLLOWUP_URD, and creates FOLLOWUP_PROPOSAL-N:

**Given** h6 handoff received (FOLLOWUP_URE + FOLLOWUP_URD) **when** starting follow-up proposal **then** create FOLLOWUP_PROPOSAL-N referencing both original URD and FOLLOWUP_URD **should never** create FOLLOWUP_PROPOSAL without reading the original URD

` + "```bash" + `
# After receiving h6 from supervisor:
bd create --labels "aura:p3-plan:s3-propose" \
  --title "FOLLOWUP_PROPOSAL-1: <follow-up feature>" \
  --description "---
references:
  request: <original-request-id>
  original_urd: <original-urd-id>
  followup_urd: <followup-urd-id>
  followup_epic: <followup-epic-id>
---
<proposal content addressing scoped IMPORTANT/MINOR findings>"
` + "```" + `

The same review/ratify/UAT/handoff cycle (Phases 3-7) applies. After FOLLOWUP_PROPOSAL is ratified, hand off to supervisor via h1 for FOLLOWUP_IMPL_PLAN creation.`,
		},
		{
			ID:    "arch-spawning-reviewers",
			Title: "Spawning Reviewers",
			Content: "Spawn 3 axis-specific reviewers (A=Correctness, B=Test quality, C=Elegance) as `general-purpose` subagents. Each reviewer must invoke the `/aura:reviewer` skill (via the Skill tool) to load its role instructions — `/aura:reviewer` is a **Skill**, not a subagent type.\n\n```\nTask(description: \"Reviewer A: correctness\", prompt: \"You are Reviewer A (Correctness). First invoke `/aura:reviewer` to load your role. Then review PROPOSAL-1 task <id>. URD: <urd-id>...\", subagent_type: \"general-purpose\")\nTask(description: \"Reviewer B: test quality\", prompt: \"You are Reviewer B (Test quality). First invoke `/aura:reviewer` to load your role. Then review PROPOSAL-1 task <id>. URD: <urd-id>...\", subagent_type: \"general-purpose\")\nTask(description: \"Reviewer C: elegance\", prompt: \"You are Reviewer C (Elegance). First invoke `/aura:reviewer` to load your role. Then review PROPOSAL-1 task <id>. URD: <urd-id>...\", subagent_type: \"general-purpose\")\n```",
		},
		{
			ID:    "arch-supervisor-handoff",
			Title: "Supervisor Handoff",
			Content: "**DO NOT** spawn supervisor as a Task tool subagent. Instead, invoke:\n\n```\nSkill(skill: \"aura:architect-handoff\")\n```\n\nThe handoff skill guides you through:\n1. Creating the handoff document at `.git/.aura/handoff/{request-task-id}/architect-to-supervisor.md`\n2. Launching supervisor via `aura-swarm start --swarm-mode intree --role supervisor -n 1` or `aura-swarm start --epic <id>`\n\n**CRITICAL:** The supervisor launch prompt MUST:\n1. **Start with `Skill(/aura:supervisor)`** — this loads the supervisor's role instructions, including leaf task creation\n2. Include all Beads task IDs (REQUEST, URD, RATIFIED PROPOSAL, HANDOFF)\n3. Include the handoff document path\n\n**DO NOT** create implementation tasks yourself - the supervisor creates vertical slice tasks from the ratified plan.",
		},
	},
}
