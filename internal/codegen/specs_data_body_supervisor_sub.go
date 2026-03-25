// Canonical body content for supervisor sub-skill SKILL.md files.
//
// Encodes the hand-authored body sections (everything after the
// <!-- END GENERATED FROM aura schema --> marker) from:
//   - skills/supervisor-plan-tasks/SKILL.md (body from line 39)
//   - skills/supervisor-spawn-worker/SKILL.md (body from line 44)
//
// Registered into SkillBodySpecs via init() to avoid merge conflicts
// with parallel workers editing specs_data.go.
package codegen

func init() {
	SkillBodySpecs["supervisor-plan-tasks"] = supervisorPlanTasksBody
	SkillBodySpecs["supervisor-spawn-worker"] = supervisorSpawnWorkerBody
}

// supervisorPlanTasksBody encodes skills/supervisor-plan-tasks/SKILL.md
// body content (lines 39–409).
var supervisorPlanTasksBody = SkillBody{
	Preamble: "Break RATIFIED_PLAN into vertical slice Implementation tasks for workers.\n\n" +
		"**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-8-implementation-plan)** <- Phase 8",
	Behaviors: []BehaviorSpec{
		{
			ID:        "sup-plan-impl-plan-decompose",
			Given:     "IMPL_PLAN placeholder",
			When:      "planning",
			Then:      "decompose into vertical slices (production code paths)",
			ShouldNot: "decompose into horizontal layers (files)",
		},
		{
			ID:        "sup-plan-ratified-plan-tasks",
			Given:     "RATIFIED_PLAN features/commands",
			When:      "creating tasks",
			Then:      "assign one vertical slice per worker (full end-to-end)",
			ShouldNot: "assign horizontal layers (types worker, tests worker, impl worker)",
		},
		{
			ID:        "sup-plan-vertical-slice-define",
			Given:     "vertical slice",
			When:      "defining",
			Then:      "specify production code path and backward planning approach",
			ShouldNot: "leave workers guessing what end users will run",
		},
		{
			ID:        "sup-plan-validation-checklist",
			Given:     "validation_checklist",
			When:      "distributing",
			Then:      "include production code verification",
			ShouldNot: "allow test-only validation",
		},
		{
			ID:        "sup-plan-integration-points-identify",
			Given:     "multiple vertical slices",
			When:      "slices share types, interfaces, or data flows",
			Then:      "identify horizontal Layer Integration Points where slices must inter-op and document them in the IMPL_PLAN with owning slice, consuming slices, and the shared contract (type, interface, or protocol)",
			ShouldNot: "leave cross-slice dependencies implicit — divergence grows when slices develop in isolation without clear merge points",
		},
		{
			ID:        "sup-plan-integration-points-include",
			Given:     "integration points identified",
			When:      "creating slice tasks",
			Then:      "include each integration point in the relevant slice descriptions so workers know what they must export and what they may import",
			ShouldNot: "assume workers will discover cross-slice contracts on their own",
		},
	},
	Sections: []ProseSection{
		{
			ID:    "sup-plan-when-to-use",
			Title: "When to Use",
			Content: `Received handoff from architect with RATIFIED_PLAN task ID and placeholder IMPL_PLAN task.`,
		},
		{
			ID:    "sup-plan-critical-vertical-slices",
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
			ID:    "sup-plan-steps",
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
				"6. **Create vertical slice tasks:**\n" +
				"   ```bash\n" +
				"   bd create --type=task \\\n" +
				"     --labels=\"aura:p9-impl:s9-slice\" \\\n" +
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
			ID:    "sup-plan-vertical-slice-task-structure",
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
			ID:    "sup-plan-layer-cake",
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
			ID:    "sup-plan-red-green-flags",
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
			ID:    "sup-plan-shared-infrastructure",
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
			ID:    "sup-plan-followup-impl-plan",
			Title: "Follow-up Implementation Plan (FOLLOWUP_IMPL_PLAN)",
			Content: "When planning for a follow-up epic (after receiving h1 from architect post-FOLLOWUP_PROPOSAL ratification), the same vertical slice decomposition applies:\n" +
				"\n" +
				"```bash\n" +
				"# Create FOLLOWUP_IMPL_PLAN\n" +
				"bd create --type=epic --priority=2 \\\n" +
				"  --labels=\"aura:p8-impl:s8-plan\" \\\n" +
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
				"# Create FOLLOWUP_SLICE-N with adopted leaf tasks\n" +
				"bd create --type=task \\\n" +
				"  --labels=\"aura:p9-impl:s9-slice\" \\\n" +
				"  --title=\"FOLLOWUP_SLICE-1: <description>\" \\\n" +
				"  --description=\"---\n" +
				"references:\n" +
				"  followup_impl_plan: <followup-impl-plan-id>\n" +
				"  followup_urd: <followup-urd-id>\n" +
				"---\n" +
				"## Adopted Leaf Tasks\n" +
				"| Leaf Task ID | Severity | Original Slice | Description |\n" +
				"|---|---|---|---|\n" +
				"| <leaf-id-1> | IMPORTANT | SLICE-1 | <description> |\n" +
				"| <leaf-id-2> | MINOR | SLICE-2 | <description> |\n" +
				"\n" +
				"## Specification\n" +
				"<detailed spec>\n" +
				"\n" +
				"## Validation Checklist\n" +
				"- [ ] All adopted leaf tasks resolved\n" +
				"- [ ] Tests pass\n" +
				"- [ ] Production code path verified\"\n" +
				"\n" +
				"# Wire dual-parent for adopted leaf tasks\n" +
				"bd dep add <followup-slice-id> --blocked-by <leaf-task-id-1>\n" +
				"bd dep add <followup-slice-id> --blocked-by <leaf-task-id-2>\n" +
				"```",
		},
	},
}

// supervisorSpawnWorkerBody encodes skills/supervisor-spawn-worker/SKILL.md
// body content (lines 44–266).
var supervisorSpawnWorkerBody = SkillBody{
	Preamble: "Launch the wave of workers for parallel vertical slice implementation, reviewed by ephemeral reviewers.\n\n" +
		"**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9",
	Behaviors: []BehaviorSpec{
		{
			ID:        "sup-spawn-task-tool",
			Given:     "implementation tasks",
			When:      "spawning",
			Then:      "use Task tool with `run_in_background: true`",
			ShouldNot: "block on worker completion",
		},
		{
			ID:        "sup-spawn-parallel-wave",
			Given:     "multiple workers",
			When:      "launching",
			Then:      "spawn all slices in parallel as a single wave",
			ShouldNot: "spawn sequentially",
		},
		{
			ID:        "sup-spawn-worker-context",
			Given:     "worker assignment",
			When:      "providing context",
			Then:      "include Beads task ID, full context, and handoff document",
			ShouldNot: "omit checklist or criteria",
		},
		{
			ID:        "sup-spawn-handoff-doc",
			Given:     "worker handoff",
			When:      "creating",
			Then:      "store at `.git/.aura/handoff/<request-task-id>/supervisor-to-worker-<N>.md`",
			ShouldNot: "skip handoff document",
		},
		{
			ID:        "sup-spawn-no-close-before-review",
			Given:     "workers complete their slices",
			When:      "first wave finishes",
			Then:      "do NOT close slices — ephemeral reviewers must review ALL slices first",
			ShouldNot: "close a slice that has not been reviewed at least once",
		},
		{
			ID:        "sup-spawn-fix-and-rereview",
			Given:     "reviewers finish reviewing",
			When:      "BLOCKERs or IMPORTANT findings exist",
			Then:      "send findings to workers for fixing, then spawn new ephemeral reviewers for re-review",
			ShouldNot: "skip re-review after fixes",
		},
		{
			ID:        "sup-spawn-max-cycles",
			Given:     "worker-reviewer cycle",
			When:      "counting iterations",
			Then:      "limit to a MAXIMUM of 3 cycles",
			ShouldNot: "exceed 3 cycles — if IMPORTANT findings remain after cycle 3, move to UAT and track remaining in FOLLOWUP epic",
		},
		{
			ID:        "sup-spawn-important-after-cycles",
			Given:     "IMPORTANT findings remain after 3 cycles",
			When:      "deciding next step",
			Then:      "proceed to Phase 11 (UAT) — all remaining IMPORTANT and MINOR findings must be tracked in the FOLLOWUP Beads epic",
			ShouldNot: "block UAT on non-BLOCKER findings after 3 cycles",
		},
	},
	Sections: []ProseSection{
		{
			ID:    "sup-spawn-when-to-use",
			Title: "When to Use",
			Content: `Implementation tasks ready. Ephemeral reviewers will be spawned per-slice during review phase.`,
		},
		{
			ID:    "sup-spawn-ride-the-wave-overview",
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
				"7. REPEAT → Steps 5-6 up to MAX 3 cycles per slice\n" +
				"8. TRACK → IMPORTANT/MINOR findings → FOLLOWUP epic\n" +
				"9. NEXT  → If clean or 3 cycles exhausted → Phase 11 (UAT) or escalate to architect\n" +
				"```\n" +
				"\n" +
				"**Key rules:**\n" +
				"- Reviewers are ephemeral (spawned per review cycle via Task tool)\n" +
				"- Slices are **never closed** until reviewed at least once\n" +
				"- Max **3 review cycles per slice** — escalate to architect after cycle 3 if BLOCKERs remain",
		},
		{
			ID:    "sup-spawn-handoff-template",
			Title: "Handoff Template (Supervisor → Worker)",
			Content: "Before spawning each worker, create a handoff document:\n" +
				"\n" +
				"**Storage:** `.git/.aura/handoff/<request-task-id>/supervisor-to-worker-<N>.md`\n" +
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
			ID:    "sup-spawn-task-call",
			Title: "Task Call",
			Content: "```\n" +
				"Task({\n" +
				"  description: \"Worker: implement SLICE-N\",\n" +
				"  prompt: `Call Skill(/aura:worker) and implement the assigned slice.\n" +
				"\n" +
				"Beads Task ID: <task-id>\n" +
				"Read full requirements: bd show <task-id>\n" +
				"Handoff doc: .git/.aura/handoff/<request-task-id>/supervisor-to-worker-<N>.md\n" +
				"\n" +
				"Do NOT shut down after implementation. You will receive review feedback and may need to fix issues.`,\n" +
				"  subagent_type: \"general-purpose\",\n" +
				"  run_in_background: true\n" +
				"})\n" +
				"```\n" +
				"\n" +
				"**Important:** Use `subagent_type: \"general-purpose\"`, not a custom agent type. The worker skill is invoked inside the agent via `Skill(/aura:worker)`.",
		},
		{
			ID:    "sup-spawn-teamcreate-sendmessage",
			Title: "TeamCreate: SendMessage Assignment",
			Content: "When workers are spawned via TeamCreate, they receive context through SendMessage instead of a Task prompt. The message MUST be self-contained — teammates have **no prior context**:\n" +
				"\n" +
				"```\n" +
				"SendMessage({\n" +
				"  type: \"message\",\n" +
				"  recipient: \"worker-1\",\n" +
				"  content: `You are assigned SLICE-1. Start by calling Skill(/aura:worker).\n" +
				"\n" +
				"Your Beads task ID: <slice-task-id>\n" +
				"Run this to get full requirements: bd show <slice-task-id>\n" +
				"Handoff document: .git/.aura/handoff/<request-task-id>/supervisor-to-worker-1.md\n" +
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
				"**Critical:** Never send bare instructions like \"implement SLICE-1\" without Beads task IDs and `bd show` commands. Teammates cannot see your conversation or task tree.",
		},
		{
			ID:    "sup-spawn-worker-persistence",
			Title: "Worker Persistence (Ride the Wave)",
			Content: "Workers are **never shut down** after completing their first implementation pass. They stay alive for the review-fix cycle:\n" +
				"\n" +
				"1. Worker completes slice → notifies supervisor\n" +
				"2. Supervisor does **NOT** close the slice or shut down the worker\n" +
				"3. Ephemeral reviewers review the slice\n" +
				"4. If BLOCKERs or IMPORTANT findings: supervisor sends fix assignment to worker\n" +
				"5. Worker fixes issues → notifies supervisor\n" +
				"6. New ephemeral reviewers re-review\n" +
				"7. Repeat steps 4-6 up to MAX 3 cycles total\n" +
				"8. After 3 cycles or all clean: supervisor shuts down worker",
			Subsections: []ProseSection{
				{
					ID:    "sup-spawn-fix-assignment-template",
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
						"IMPORTANT (must fix this cycle):\n" +
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
			ID:    "sup-spawn-beads-status",
			Title: "Worker Should Update Beads Status",
			Content: "- On start: `bd update <task-id> --status=in_progress`\n" +
				"- On implementation complete (NOT slice close): `bd comments add <task-id> \"Implementation complete, awaiting review\"`\n" +
				"- On blocked: `bd update <task-id> --notes=\"Blocked: <reason>\"`\n" +
				"- Slice closure: **only the supervisor** closes slices after review passes",
		},
		{
			ID:    "sup-spawn-assign-via-beads",
			Title: "Assign via Beads",
			Content: "```bash\n" +
				"bd update <task-id> --assignee=\"<worker-agent-name>\"\n" +
				"bd update <task-id> --status=in_progress\n" +
				"```",
		},
		{
			ID:    "sup-spawn-followup-slice-handoff",
			Title: "Follow-up Slice Handoff (FOLLOWUP_SLICE-N)",
			Content: "For follow-up slices, the handoff template extends with additional fields:\n" +
				"\n" +
				"**Storage:** `.git/.aura/handoff/{followup-epic-id}/supervisor-to-worker-<N>.md`\n" +
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
				"## Adopted Leaf Tasks\n" +
				"| Leaf Task ID | Severity | Original Slice | Description |\n" +
				"|---|---|---|---|\n" +
				"| <leaf-id-1> | IMPORTANT | SLICE-1 | <description> |\n" +
				"| <leaf-id-2> | MINOR | SLICE-2 | <description> |\n" +
				"\n" +
				"## Acceptance Criteria\n" +
				"- Both adopted leaf tasks resolved (tests pass, production code path verified)\n" +
				"- See bd task <slice-task-id> for full validation_checklist\n" +
				"```",
		},
	},
}
