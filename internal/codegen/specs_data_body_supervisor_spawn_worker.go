// Body content for the supervisor-spawn-worker skill SKILL.md.
package codegen

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
			Then:      "iterate review->fix->re-review up to the chosen review-effort budget until a fix-free clean round confirms 0 BLOCKER + 0 IMPORTANT + 0 MINOR within budget; on budget exhaustion without clean, surface outstanding findings to the user at a gate",
			ShouldNot: "hardcode the budget, proceed past the chosen budget without surfacing to the user, close a wave on a fix-applying round, or proceed with any finding silently outstanding",
		},
		{
			Id:        "sup-spawn-important-after-cycles",
			Given:     "IMPORTANT or MINOR findings remain",
			When:      "deciding next step",
			Then:      "keep iterating review->fix->re-review until ALL severity groups reach 0 — every severity must be resolved before the wave closes",
			ShouldNot: "proceed to UAT with non-zero findings or route any review severity (IMPORTANT/MINOR) to the FOLLOWUP epic",
		},
		// R7/A1: code review iterates up to the chosen review-effort budget until
		// 0/0/0 clean; on exhaustion, surface outstanding findings to the user.
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
				"7. REPEAT → Steps 5-6 up to the chosen review-effort budget until a fix-free clean round confirms 0/0/0; on budget exhaustion without clean, surface to the user\n" +
				"8. TRACK → ALL severities (BLOCKER/IMPORTANT/MINOR) must reach 0 — none route to FOLLOWUP\n" +
				"9. NEXT  → When fix-free clean (0 BLOCKER + 0 IMPORTANT + 0 MINOR) → Phase 11 (UAT); escalate to architect only if genuinely stuck\n" +
				"```\n" +
				"\n" +
				"**Key rules:**\n" +
				"- Reviewers are ephemeral (spawned per review cycle via Task tool)\n" +
				"- Slices are **never closed** until reviewed at least once\n" +
				"- **Configurable review-effort budget** (chosen at Phase 8: 3 rounds / 1 round / 0 rounds / unlimited / custom) — iterate review→fix→re-review up to the budget until 0 BLOCKER + 0 IMPORTANT + 0 MINOR on a fix-free round; on budget exhaustion without clean, surface outstanding findings to the user at a gate; escalate to architect only if genuinely stuck\n" +
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
				"7. Repeat steps 4-6 up to the chosen review-effort budget until a fix-free clean round confirms 0/0/0; on budget exhaustion without clean, surface outstanding findings to the user at a gate\n" +
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
