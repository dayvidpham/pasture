// Body content for the supervisor-plan-tasks skill SKILL.md.
package codegen

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
		{
			Id:        "sup-plan-review-effort-budget",
			Given:     "the start of Phase 8 (IMPL_PLAN), like the Phase-1 research-depth gate (per C-review-effort-budget)",
			When:      "deciding how much review-and-fix effort to spend per slice",
			Then:      "request a configurable review-effort budget from the user (defaults: 3 rounds, 1 round, 0 rounds, unlimited, custom); the Phase-10 review->fix->re-review loop iterates up to the chosen budget; on budget exhaustion WITHOUT a clean 0/0/0 round, surface the outstanding findings to the user for a decision",
			ShouldNot: "hardcode the review-cycle budget; proceed past the chosen budget without surfacing outstanding findings to the user; loop forever when a finite budget was chosen",
		},
	},
	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:      "sup-plan-when-to-use",
			Title:   "When to Use",
			Content: `Received handoff from architect with RATIFIED_PLAN task ID and placeholder IMPL_PLAN task.`,
		},
		{
			Id:    "sup-plan-review-effort-budget-gate",
			Title: "Request the Review-Effort Budget (Phase 8 user gate)",
			Content: "At the **start of Phase 8** — like the Phase-1 research-depth gate — request a **configurable review-effort budget** from the user (per `C-review-effort-budget`). This is one of the 5 user-gated phases. Present the default choices:\n" +
				"\n" +
				"| Option | Meaning |\n" +
				"|--------|---------|\n" +
				"| **3 rounds** | Up to three review -> fix -> re-review cycles per slice |\n" +
				"| **1 round** | A single review + one fix pass |\n" +
				"| **0 rounds** | No review-fix iteration (review once, surface anything found) |\n" +
				"| **unlimited** | Iterate until a fix-free clean 0/0/0 round (no upper bound) |\n" +
				"| **custom** | A user-specified number of rounds |\n" +
				"\n" +
				"The Phase-10 review->fix->re-review loop iterates **up to the chosen budget** until a fix-free clean round confirms 0 BLOCKER + 0 IMPORTANT + 0 MINOR. On **budget exhaustion WITHOUT a clean round**, SURFACE the outstanding findings to the user for a decision — never proceed dirty, never loop forever, and never hardcode the budget. Record the chosen budget in the IMPL_PLAN so workers and reviewers know the bound.",
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
			Content: `1. **Read RATIFIED_PLAN and URD tasks:**
   ` + "```" + `bash
   pasture task show "${RATIFIED_PLAN_ID_URI}"
   pasture task show "${URD_ID_URI}"
   ` + "```" + `

2. **Identify production code paths** (what end users will actually run):
   - CLI commands: ` + "`" + `cli-tool feature` + "`" + `, ` + "`" + `cli-tool feature list` + "`" + `, ` + "`" + `cli-tool feature detail` + "`" + `
   - API endpoints: ` + "`" + `POST /api/items` + "`" + `, ` + "`" + `GET /api/items/:id` + "`" + `
   - Background jobs: ` + "`" + `sync-daemon` + "`" + `, ` + "`" + `backup-daemon` + "`" + `

3. **Decompose into vertical slices** (one per production code path):
   - Each slice = one command/endpoint/job
   - Each slice owned by ONE worker
   - Each slice goes from types → tests → implementation → wiring

4. **Identify shared infrastructure** (optional Layer 0):
   - Common types used across ALL slices (e.g., base error enums)
   - Shared utilities (not specific to one slice)
   - If significant, create Layer 0 tasks (parallel, no deps)

5. **Identify horizontal Layer Integration Points** (where slices must inter-op):
   - For each pair of slices, ask: "Does slice A need to import/call/consume anything from slice B?"
   - If yes, document the integration point: owning slice, consuming slice(s), and the shared contract
   - Integration points should merge **sooner rather than later** — delaying inter-op causes divergence
   - Common integration points: shared type definitions, event interfaces, registry patterns, DI bindings
   - Each integration point gets an explicit owner (the slice that defines/exports it)

   ` + "```" + `
   ## Integration Points (example)

   | ID | Contract | Owner (exports) | Consumer(s) (imports) | Merge Timing |
   |----|----------|-----------------|-----------------------|--------------|
   | IP-1 | PhaseEnum type | SLICE-1 (foundation) | SLICE-2, SLICE-3, SLICE-4 | L1 (types) |
   | IP-2 | ConstraintContext interface | SLICE-1 (foundation) | SLICE-2 (gen_schema) | L1 (types) |
   | IP-3 | SkillRegistry protocol | SLICE-3 (gen_skills) | SLICE-4 (context_injection) | L3 (impl) |
   ` + "```" + `

   **Interface-first decomposition (R3, Strong SHOULD — see ` + "`" + `C-interface-first-slices` + "`" + `):** when slices share contracts, prefer extracting a horizontal **interface-first FOUNDATION slice** that exports ALL public types/interfaces/contracts and lands FIRST (a barrier). The dependent implementation slices then compile against those contracts and run in **parallel**, instead of being forced into a linear ` + "`" + `A → B → C` + "`" + ` chain whose only real coupling is at the interface boundary. Reserve a linear chain for cases where the runtime dependency genuinely exceeds the interface.

6. **Create vertical slice tasks:**
   ` + "```" + `bash
   SLICE_1_ID_URI=$(pasture task create "SLICE-1: Implement 'cli-tool feature list' command (full vertical)" --phase worker_slices --namespace "$PASTURE_NAMESPACE" --format json --type=task \
     --description="$(cat <<EOF
   ---
   references:
     impl_plan: "${IMPL_PLAN_ID_URI}"
     urd: "${URD_ID_URI}"
   ---
   ## Production Code Path

   **End user runs:** \` + "`" + `./bin/cli-tool feature list\` + "`" + `

   ## Worker Owns (Full Vertical Slice)

   Plan backwards from production code path:
   1. End: CLI entry point \` + "`" + `listCmd (cobra.Command) RunE handler\` + "`" + `
   2. Back: Service call \` + "`" + `feature.NewService(deps).ListItems(opts)\` + "`" + `
   3. Back: Service method \` + "`" + `ListItems(opts ListOptions) ([]ListEntry, error)\` + "`" + `
   4. Back: Types \` + "`" + `ListOptions\` + "`" + `, \` + "`" + `ListEntry\` + "`" + `

   ## Files You Own (Within These Files)

   - pkg/feature/types.go (ListOptions, ListEntry ONLY)
   - cmd/feature/list_test.go (import actual CLI)
   - pkg/feature/service.go (ListItems method ONLY)
   - cmd/feature/list.go (list subcommand wiring ONLY)

   ## Implementation Order (Layers Within Your Slice)

   **Layer 1: Types** (your slice only)
   - Create ListOptions, ListEntry
   - Do NOT add types for other slices (e.g., DetailView)

   **Layer 2: Tests** (importing production code)
   - Import actual CLI: \` + "`" + `import "myproject/cmd/feature"\` + "`" + `
   - Test the actual command users will run
   - Tests will FAIL - expected, no implementation yet

   **Layer 3: Implementation + Wiring**
   - Implement service.ListItems() method
   - Wire cobra command with feature.NewService(realDeps)
   - No TODO placeholders
   - Tests should now PASS

   ## Validation

   Before marking complete:
   - [ ] Production code verified via code inspection (no TODOs, real deps wired)
   - [ ] Tests import actual CLI (not test-only export)
   - [ ] No dual-export anti-pattern
   - [ ] No TODO placeholders
   - [ ] Service wired with real dependencies
   EOF
   )" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
   pasture task label add "$SLICE_1_ID_URI" pasture:p9-impl:s9-slice
   pasture task update "$SLICE_1_ID_URI" --notes "Design: '{
       \"productionCodePath\": \"cli-tool feature list\",
       \"validation_checklist\": [
         \"Type checking passes\",
         \"Tests pass\",
         \"Production code verified via code inspection\",
         \"Tests import production CLI package\",
         \"No TODO placeholders in CLI action\",
         \"Service wired with real dependencies\"
       ],
       \"acceptance_criteria\": [{
         \"given\": \"user runs cli-tool feature list\",
         \"when\": \"command executes\",
         \"then\": \"shows list from actual service\",
         \"should_not\": \"have dual-export (test vs production paths)\"
       }],
       \"ratified_plan\": \"${RATIFIED_PLAN_ID_URI}\"
     }'"

   pasture task dep add "${IMPL_PLAN_ID_URI}" --blocked-by "${SLICE_1_ID_URI}"
   ` + "```" + `

7. **Update IMPL_PLAN with vertical slice breakdown + integration points:** First create each remaining slice using the Step 6 pattern and bind SLICE_2_ID_URI, SLICE_3_ID_URI and SLICE_4_ID_URI from their respective create outputs.
   ` + "```" + `bash
   pasture task update "${IMPL_PLAN_ID_URI}" --description="$(cat <<EOF
   ---
   references:
     request: "${REQUEST_ID_URI}"
     urd: "${URD_ID_URI}"
     proposal: "${RATIFIED_PROPOSAL_ID_URI}"
   ---
   ## Vertical Slice Decomposition

   Each worker owns ONE production code path (full vertical slice from CLI → service → types).

   ### Shared Infrastructure (Layer 0 - optional)
   - Common types: SortOrder, OutputFormat, ErrorCode enums
   - Implemented first, parallel

   ### Vertical Slices (parallel, after Layer 0)

   **SLICE-1: "cli-tool feature" (default command)**
   - Worker: A
   - Production path: \` + "`" + `./bin/cli-tool feature\` + "`" + `
   - Owns: default action, recent items logic
    - Task: "${SLICE_1_ID_URI}"

   **SLICE-2: "cli-tool feature list"**
   - Worker: B
   - Production path: \` + "`" + `./bin/cli-tool feature list\` + "`" + `
   - Owns: ListOptions types, list tests, listItems() method, list CLI wiring
    - Task: "${SLICE_2_ID_URI}"

   **SLICE-3: "cli-tool feature detail"**
   - Worker: C
    - Production path: \` + "`" + `./bin/cli-tool feature detail FEATURE_ID\` + "`" + `
   - Owns: DetailView types, detail tests, getItemDetail() method, detail CLI wiring
    - Task: ${SLICE_3_ID_URI}

   **SLICE-4: "cli-tool feature search"**
   - Worker: D
   - Production path: \` + "`" + `./bin/cli-tool feature search\` + "`" + `
   - Owns: SearchQuery types, search tests, searchItems() method, search CLI wiring
    - Task: ${SLICE_4_ID_URI}

   ## Horizontal Layer Integration Points

   Where slices must inter-op. Merge sooner, not later — divergence grows with delay.

   | ID | Contract | Owner (exports) | Consumer(s) (imports) | Merge Timing |
   |----|----------|-----------------|-----------------------|--------------|
   | IP-1 | FeatureError enum | SLICE-1 | SLICE-2, SLICE-3, SLICE-4 | L1 (types) |
   | IP-2 | BaseService interface | SLICE-1 | SLICE-2, SLICE-3 | L1 (types) |

   ## Execution Order

   1. Layer 0 (if needed): Shared infrastructure (parallel)
   2. SLICE-1 through SLICE-4: Each worker implements their vertical slice (parallel)
      - Within each slice: Types (L1) → Tests (L2) → Impl+Wiring (L3)
   3. Integration points merge at documented timing (L1 contracts first, L3 wiring last)

   ## Validation

   All production code paths verified via code inspection:
   - ./bin/cli-tool feature
   - ./bin/cli-tool feature list
   - ./bin/cli-tool feature detail "${ID_URI}"
   - ./bin/cli-tool feature search
   - All integration points verified: contracts match between owner and consumers
   EOF
   )"
   ` + "```" + ``,
		},
		{
			Id:    "sup-plan-vertical-slice-task-structure",
			Title: "Vertical Slice Task Structure",
			Content: `` + "```" + `json
{
  "slice": "feature-list",
  "productionCodePath": "cli-tool feature list",
  "taskId": "${TASK_A_URI}",
  "workerOwns": {
    "endPoint": "listCmd (cobra.Command) RunE handler",
    "types": ["ListOptions", "ListEntry"],
    "tests": ["cmd/feature/list_test.go"],
    "implementation": [
      "(*FeatureService).ListItems() method",
      "listCmd wired with feature.NewService(realDeps)"
    ]
  },
  "planningApproach": "Backwards from production code path",
  "validation_checklist": [
    "Type checking passes",
    "Tests pass",
    "Production code works: ./bin/aura sessions list",
    "Tests import production CLI (not test-only export)",
    "No TODO placeholders",
    "Service wired with real dependencies"
  ],
  "acceptance_criteria": [{
    "given": "user runs aura sessions list",
    "when": "command executes",
    "then": "shows session list from actual service",
    "should_not": "have dual-export or TODO placeholders"
  }],
  "ratified_plan": "${RATIFIED_PLAN_ID_URI}",
  "urd": "<urd-id>"
}
` + "```" + ``,
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
			Content: `When planning for a follow-up epic (after receiving h1 from architect post-FOLLOWUP_PROPOSAL ratification), the same vertical slice decomposition applies:

` + "```" + `bash
# Create FOLLOWUP_IMPL_PLAN
FOLLOWUP_IMPL_PLAN_ID_URI=$(pasture task create "FOLLOWUP_IMPL_PLAN: <follow-up feature>" --phase impl_plan --namespace "$PASTURE_NAMESPACE" --format json --type=epic --priority=2 \
  --description="---
references:
  followup_epic: "${FOLLOWUP_EPIC_ID_URI}"
  original_request: "${REQUEST_ID_URI}"
  original_urd: "${URD_ID_URI}"
  followup_urd: "${FOLLOWUP_URD_ID_URI}"
  followup_proposal: "${FOLLOWUP_PROPOSAL_ID_URI}"
---
Vertical slice decomposition for follow-up epic." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$FOLLOWUP_IMPL_PLAN_ID_URI" pasture:p8-impl:s8-plan

# Create FOLLOWUP_SLICE-N with DEFER'd-item leaf tasks
FOLLOWUP_SLICE_ID_URI=$(pasture task create "FOLLOWUP_SLICE-1: <description>" --phase worker_slices --namespace "$PASTURE_NAMESPACE" --format json --type=task \
  --description="---
references:
  followup_impl_plan: "${FOLLOWUP_IMPL_PLAN_ID_URI}"
  followup_urd: "${FOLLOWUP_URD_ID_URI}"
---
## DEFER'd-Item Leaf Tasks
| Leaf Task ID | Source UAT | DEFER'd Item | Description |
|---|---|---|---|
| "${LEAF_ID_1_URI}" | "${UAT_ID_URI}" | "${DEFERRED_ITEM_ID_URI}" | <description> |
| "${LEAF_ID_2_URI}" | "${UAT_ID_URI}" | "${DEFERRED_ITEM_ID_URI}" | <description> |

## Specification
<detailed spec>

## Validation Checklist
- [ ] All DEFER'd-item leaf tasks resolved
- [ ] Tests pass
- [ ] Production code path verified" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$FOLLOWUP_SLICE_ID_URI" pasture:p9-impl:s9-slice

# Wire dual-parent: leaf blocks BOTH the DEFER'd-items tracking group AND the follow-up slice
pasture task dep add "${FOLLOWUP_SLICE_ID_URI}" --blocked-by "${LEAF_TASK_ID_1_URI}"
pasture task dep add "${FOLLOWUP_SLICE_ID_URI}" --blocked-by "${LEAF_TASK_ID_2_URI}"
` + "```" + ``,
		},
	},
}
