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
