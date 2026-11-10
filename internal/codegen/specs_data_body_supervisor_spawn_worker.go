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
			Then:      "use [[op:delegate_assignment]] tool with `run_in_background: true`",
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
			Then:      "include Pasture task ID, full context, and handoff document",
			ShouldNot: "omit checklist or criteria",
		},
		{
			Id:        "sup-spawn-handoff-doc",
			Given:     "worker handoff",
			When:      "creating",
			Then:      "author the supervisor→worker handoff in the slice (or a dedicated handoff) Pasture task body",
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
			Then:      "iterate review->fix->re-review up to the chosen review-effort budget; only an open BLOCKER gates a wave, IMPORTANT and MINOR findings stay open, and convergence at the end targets 0/0/0 within maxRounds; on budget exhaustion without convergence, surface outstanding findings to the user at a gate",
			ShouldNot: "hardcode the budget, proceed past the chosen budget without surfacing to the user, require a zeroed round to close a wave, or close a finding outside `C-finding-lifecycle`",
		},
		{
			Id:        "sup-spawn-important-after-cycles",
			Given:     "IMPORTANT or MINOR findings remain",
			When:      "deciding next step",
			Then:      "keep them open past the wave gate and converge over every open `pasture:finding` at the end — only an open BLOCKER blocks the wave",
			ShouldNot: "proceed to UAT with an open BLOCKER or route any review severity (IMPORTANT/MINOR) to the FOLLOWUP epic",
		},
		// R7/A1: code review gates each wave on open BLOCKERs and converges over
		// open findings at the end; on exhaustion, surface outstanding findings to
		// the user. Resolves to SharedFragmentSpecs[FragReviewCleanExit] (SLICE-1).
		behaviorRef(FragReviewCleanExit),
	},
	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:      "sup-spawn-when-to-use",
			Title:   "When to Use",
			Content: `Implementation tasks ready. Ephemeral reviewers will be spawned for the wave during review phase.`,
		},
		{
			Id:    "sup-spawn-ride-the-wave-overview",
			Title: "Ride the Wave — Overview",
			Content: "The supervisor executes Phases 8-10 as a single coordinated cycle called **Ride the Wave**:\n" +
				"\n" +
				"```\n" +
				"1. PLAN  → supervisor-plan-tasks: decompose into slices + integration points\n" +
				"2. EXPLORE → Ephemeral Explore subagents ([[op:delegate_assignment]] tool): map codebase, short-lived\n" +
				"3. BUILD → N Workers: implement slices in parallel\n" +
				"4. REVIEW → Ephemeral reviewers ([[op:delegate_assignment]] tool): review the whole wave\n" +
				"5. FIX   → Workers fix BLOCKERs + IMPORTANTs with atomic commits\n" +
				"6. RE-REVIEW → Spawn new ephemeral reviewers for re-review\n" +
				"7. REPEAT → Steps 5-6 up to the chosen review-effort budget; only an open BLOCKER gates the wave, and convergence at the end targets 0/0/0 within maxRounds; on budget exhaustion without convergence, surface to the user\n" +
				"8. TRACK → Open findings converge at the end per `C-finding-lifecycle`; none route to FOLLOWUP\n" +
				"9. NEXT  → When convergence finds no open `pasture:finding` → Phase 11 (UAT); escalate to architect only if genuinely stuck\n" +
				"```\n" +
				"\n" +
				"**Key rules:**\n" +
				"- Reviewers are ephemeral (spawned per review cycle with the [[op:delegate_assignment]] tool)\n" +
				"- Slices are **never closed** until reviewed at least once\n" +
				"- **Configurable review-effort budget** (chosen at Phase 8: 3 rounds / 1 round / 0 rounds / unlimited / custom) — iterate review→fix→re-review up to the budget; only an open BLOCKER gates a wave, and convergence at the end queries open `pasture:finding` toward 0/0/0 within maxRounds; on budget exhaustion without convergence, surface outstanding findings to the user at a gate; escalate to architect only if genuinely stuck\n" +
				"- The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items, never by review severities",
		},
		{
			Id:    "sup-spawn-handoff-template",
			Title: "Handoff Template (Supervisor → Worker)",
			Content: `Before spawning each worker, author its handoff in the slice (or a dedicated handoff) Pasture task body:

**Storage:** the Pasture task body IS the handoff — no filesystem path.

` + "```" + `markdown
# Handoff: Supervisor → Worker <N>

## Context
- Request: ${REQUEST_ID_URI}
- URD: ${URD_ID_URI}
- IMPL_PLAN: ${IMPL_PLAN_ID_URI}
- Ratified Proposal: ${PROPOSAL_ID_URI}

## Your Slice
- Slice: SLICE-<N>
- Task ID: ${SLICE_TASK_ID_URI}
- Production Code Path: <what end users run>

## Key Files
| File | What You Own |
|------|-------------|
| pkg/feature/types.go | ListOptions, ListEntry types |
| cmd/feature/list_test.go | List command tests |
| pkg/feature/service.go | ListItems() method |
| cmd/feature/list.go | list subcommand wiring |

## Implementation Order
1. Layer 1: Types (your slice only)
2. Layer 2: Tests (import production code — will FAIL, expected)
3. Layer 3: Implementation + Wiring (make tests PASS)

## Validation Checklist
- [ ] Production code verified via code inspection
- [ ] Tests import actual CLI (not test-only export)
- [ ] No dual-export anti-pattern
- [ ] No TODO placeholders
- [ ] Service wired with real dependencies

## Persistence
Do NOT shut down after implementation. You will receive review feedback
and may need to fix BLOCKERs and IMPORTANT findings. Stay alive for the
full Ride the Wave cycle.
` + "```" + ``,
		},
		{
			Id:    "sup-spawn-task-call",
			Title: "Task Call",
			Content: `` + "```" + `
delegation: {
  description: "Worker: implement SLICE-{W}-{N}",
  prompt: ` + "`" + `Call [[op:invoke_skill]](/pasture:worker) and implement the assigned slice.

Pasture Task ID: "${TASK_ID_URI}"
Read full requirements + handoff: [[rec:show]] "${TASK_ID_URI}"

Do NOT shut down after implementation. You will receive review feedback and may need to fix issues.` + "`" + `,
  subagent_type: "general-purpose",
  run_in_background: true
}
` + "```" + `

Per [sup-spawn-workers], use ` + "`" + `subagent_type: "general-purpose"` + "`" + `, not a custom agent type. The worker skill is invoked inside the agent via ` + "`" + `[[op:invoke_skill]](/pasture:worker)` + "`" + `.`,
		},
		{
			Id:    "sup-spawn-teamcreate-sendmessage",
			Title: "Delegated Assignment Message",
			Content: `When a worker is delegated an assignment, the assignment message is its only context. The message MUST be self-contained — the worker has **no prior context**:

` + "```" + `
[[op:send_assignment_message]]({
  type: "message",
  recipient: "worker-1",
  content: ` + "`" + `You are assigned SLICE-{W}-{N}. Start by calling [[op:invoke_skill]](/pasture:worker).

Your Pasture task ID: "${SLICE_TASK_ID_URI}"
Run this to get full requirements + handoff: [[rec:show]] "${SLICE_TASK_ID_URI}"

Key references (run [[rec:show]] on each for full context):
- Request: "${REQUEST_ID_URI}"
- URD: "${URD_ID_URI}"
- IMPL_PLAN: "${IMPL_PLAN_ID_URI}"
- Ratified Proposal: "${PROPOSAL_ID_URI}"

Read the handoff doc and your Pasture task before starting implementation.

IMPORTANT: Do NOT shut down after completing implementation. You will receive
review feedback from ephemeral reviewers and may need to fix BLOCKERs and IMPORTANT
findings. Stay alive for the full Ride the Wave cycle.` + "`" + `,
  summary: "SLICE-{W}-{N} assignment with Pasture context"
})
` + "```" + `

Per [sup-teamcreate-msg], include Pasture task IDs and ` + "`" + `[[rec:show]]` + "`" + ` commands in every assignment. Teammates cannot see your conversation or task tree.`,
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
				"7. Repeat steps 4-6 up to the chosen review-effort budget; only an open BLOCKER gates the wave, and convergence at the end targets 0/0/0 within maxRounds; on budget exhaustion without convergence, surface outstanding findings to the user at a gate\n" +
				"8. After convergence finds no open `pasture:finding`: supervisor shuts down worker",
			Subsections: []ProseSection{
				{
					Id:    "sup-spawn-fix-assignment-template",
					Title: "Fix Assignment Message Template",
					Content: `` + "```" + `
[[op:send_assignment_message]]({
  type: "message",
  recipient: "worker-1",
  content: ` + "`" + `Review cycle <N> found issues in your slice (SLICE-{W}-{N}).

BLOCKERs (must fix — blocks slice closure):
- "${FINDING_ID_URI}": <description> ([[rec:show]] "${FINDING_ID_URI}")

IMPORTANT (must fix — closes only per C-finding-lifecycle):
- "${FINDING_ID_URI}": <description> ([[rec:show]] "${FINDING_ID_URI}")

MINOR (must fix — closes only per C-finding-lifecycle):
- "${FINDING_ID_URI}": <description> ([[rec:show]] "${FINDING_ID_URI}")

After fixing all items:
  [[rec:note]] "${SLICE_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Fixes applied for review cycle <N>"

Do NOT shut down. Ephemeral reviewers will re-review.` + "`" + `,
  summary: "Review cycle <N> fixes for SLICE-{W}-{N}"
})
` + "```" + ``,
				},
			},
		},
		{
			Id:    "sup-spawn-task-status",
			Title: "Worker Should Update Pasture Status",
			Content: `- On start: ` + "`" + `[[rec:status]] "${TASK_ID_URI}" --set in_progress` + "`" + `
- On implementation complete (NOT slice close): ` + "`" + `[[rec:note]] "${TASK_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Implementation complete, awaiting review"` + "`" + `
- On blocked: ` + "`" + `[[rec:note]] "${TASK_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body="Blocked: <reason>"` + "`" + `
- Slice closure: **only the supervisor** closes slices after review passes`,
		},
		{
			Id:    "sup-spawn-assign-via-task",
			Title: "Assign via Pasture",
			Content: `` + "```" + `bash
[[rec:label]] "${TASK_ID_URI}" owner:"${REGISTERED_WORKER_ACTOR}"
[[rec:status]] "${TASK_ID_URI}" --set in_progress
` + "```" + ``,
		},
		{
			Id:    "sup-spawn-followup-slice-handoff",
			Title: "Follow-up Slice Handoff (FOLLOWUP_SLICE-N)",
			Content: `For follow-up slices, the handoff (authored in the Pasture task body — no filesystem path) extends with additional fields:

` + "```" + `markdown
# Handoff: Supervisor → Worker <N> (Follow-up)

## Context
- Original Request: "${REQUEST_ID_URI}"
- Follow-up Epic: "${FOLLOWUP_EPIC_ID_URI}"
- FOLLOWUP_URD: "${FOLLOWUP_URD_ID_URI}"
- FOLLOWUP_IMPL_PLAN: "${FOLLOWUP_IMPL_PLAN_ID_URI}"

## Your Slice
- Slice: FOLLOWUP_SLICE-<N>
- Task ID: "${SLICE_TASK_ID_URI}"

## DEFER'd Items (from UAT)
| Item Task ID | Source UAT | Description |
|---|---|---|
| "${ITEM_ID_1_URI}" | "${UAT_ID_URI}" | <user-DEFER'd item description> |
| "${ITEM_ID_2_URI}" | "${UAT_ID_URI}" | <user-DEFER'd item description> |

## Acceptance Criteria
- All DEFER'd items in this slice resolved (tests pass, production code path verified)
- See [[rec:show]] "${SLICE_TASK_ID_URI}" for full validation_checklist
` + "```" + ``,
		},
	},
}
