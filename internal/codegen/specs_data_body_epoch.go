// Body content for the epoch role SKILL.md.
// Ported from aura-plugins/skills/epoch/SKILL.md.
package codegen

var epochBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md)** <- All 12 Phases`,

	Behaviors: []BehaviorSpec{
		{
			Id:        "epoch-verbatim-capture",
			Given:     "user provides request",
			When:      "capturing",
			Then:      "store verbatim without paraphrasing in Phase 1 REQUEST task",
			ShouldNot: "summarize or interpret the user's words",
		},
		{
			Id:        "epoch-dep-chain",
			Given:     "any phase transition",
			When:      "creating new task",
			Then:      "add dependency to previous: [[rec:block]] \"${PARENT_URI}\" --by \"${CHILD_URI}\"",
			ShouldNot: "skip dependency chaining",
		},
		{
			Id:        "epoch-audit-never-delete",
			Given:     "task completion",
			When:      "updating",
			Then:      "add comments and labels only",
			ShouldNot: "close or delete tasks prematurely",
		},
		{
			Id:        "epoch-consensus-required",
			Given:     "review cycle",
			When:      "any REVISE vote",
			Then:      "create PROPOSAL-N+1 and repeat review",
			ShouldNot: "proceed without full ACCEPT consensus from all 3 reviewers",
		},
		{
			Id:        "epoch-followup-trigger",
			Given:     "UAT (Phase 5 or 11) produces one or more user-DEFER'd items",
			When:      "finishing UAT",
			Then:      "Supervisor creates a follow-up epic (label pasture:epic-followup) from the user-DEFER'd UAT items only",
			ShouldNot: "create a follow-up epic from any review severity (BLOCKER/IMPORTANT/MINOR) — the FOLLOWUP epic is fed only by user-DEFER'd UAT items",
		},
		{
			Id:        "epoch-supervisor-not-idle",
			Given:     "a freshly spawned supervisor (Phase 8 IMPL_PLAN)",
			When:      "it dispatches Explore subagents and appears idle",
			Then:      "let it work — an apparently-idle supervisor is usually running Explore subagents to map the codebase",
			ShouldNot: "shut down or restart a supervisor that looks idle at the start of the IMPL_PLAN phase",
		},
		// R7/A1: Phase-10 code review gates each wave on open BLOCKERs and then
		// converges over open findings; on budget exhaustion without convergence,
		// surface outstanding findings to the user at a gate. Resolves to
		// SharedFragmentSpecs[FragReviewCleanExit].
		behaviorRef(FragReviewCleanExit),
		{
			Id:        "epoch-autonomous-progression",
			Given:     "non-user-gated phase completes",
			When:      "transitioning",
			Then:      "proceed autonomously; " + renderUserGates(),
			ShouldNot: "ask 'Should I proceed?' for autonomous phases; add user gates beyond the defined list",
		},
		{
			Id:        "epoch-uat-auto-ratify",
			Given:     "Phase 5 UAT ACCEPT",
			When:      "transitioning to Phase 6",
			Then:      "ratify automatically",
			ShouldNot: "ask user for ratification confirmation",
		},
		{
			Id:        "epoch-frontmatter-refs",
			Given:     "cross-task references",
			When:      "linking related tasks (e.g. URD to REQUEST)",
			Then:      "use description frontmatter references: block",
			ShouldNot: "use peer-reference commands",
		},
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		fragRef(FragTrackerAdapter),
		fragRef(FragSignalCapture),
		{
			Id:    "epoch-core-principles",
			Title: "Core Principles",
			Content: `1. **AUDIT TRAIL PRESERVATION** — Never delete or destroy information, labels, or tasks
2. **DEPENDENCY CHAINING** — Each task blocks its predecessor: ` + "`" + `[[rec:block]] "${PARENT_URI}" --by "${CHILD_URI}"` + "`" + `
3. **USER ENGAGEMENT** — URE and UAT at multiple checkpoints
4. **CONSENSUS REQUIRED** — All 3 reviewers must ACCEPT before proceeding
5. **EAGER SEVERITY TREE** — Code reviews (Phase 10) always create 3 severity groups (BLOCKER, IMPORTANT, MINOR); empty groups closed immediately. Only an open BLOCKER gates a review wave; open findings converge at the end
6. **FOLLOW-UP EPIC** — Fed ONLY by user-DEFER'd UAT items (Phase 5/11), never by any review severity; the Supervisor creates it from those DEFER'd items
7. **RIDE THE WAVE** — Phases 8-10 form one continuous cycle: Explore subagents (P8), workers implement (P9), ephemeral reviewers review (P10), iterating review→fix→re-review up to the chosen review-effort budget; only an open BLOCKER gates a wave, and after the last wave convergence queries every open pasture:finding toward 0/0/0 within maxRounds; on exhaustion, surface outstanding findings to the user at a gate; workers persist throughout`,
		},
		{
			Id:    "epoch-12-phase-workflow",
			Title: "The 12-Phase Workflow",
			Content: "```" + `
Phase 1:  pasture:p1-user       -> REQUEST (classify, research, explore)
            s1_1-classify -> s1_2-research || s1_3-explore
Phase 2:  pasture:p2-user       -> ELICIT (URE survey) + URD (single source of truth)
            s2_1-elicit -> s2_2-urd
Phase 3:  pasture:p3-plan       -> PROPOSAL-N (architect proposes)
Phase 4:  pasture:p4-plan       -> REVIEW (3 parallel reviewers, ACCEPT/REVISE)
Phase 5:  pasture:p5-user       -> Plan UAT (user acceptance test)
Phase 6:  pasture:p6-plan       -> Ratification (supersede old proposals)
Phase 7:  pasture:p7-plan       -> Handoff (architect -> supervisor)
Phase 8:  implementation plan -> IMPL_PLAN (supervisor decomposes into slices; Explore subagents)
Phase 9:  worker slices       -> one slice per worker (parallel workers; Ride the Wave — workers persist for review)
Phase 10: code review         -> reviewers review every slice in the wave; only an open BLOCKER gates a wave, then convergence at the end, else surface to the user
Phase 11: pasture:p11-user    -> Implementation UAT
Phase 12: landing             -> commit, push, hand off
` + "```" + `

### Phase 1 Expanded: REQUEST

Phase 1 has 3 sub-steps:

| Sub-step | Label | Description | Parallel? |
|----------|-------|-------------|-----------|
| s1_1-classify | ` + "`pasture:p1-user:s1_1-classify`" + ` | Capture and classify request along 4 axes (scope, complexity, risk, domain novelty) | Sequential (first) |
| s1_2-research | ` + "`pasture:p1-user:s1_2-research`" + ` | Find domain standards, prior art | Parallel with s1_3 |
| s1_3-explore | ` + "`pasture:p1-user:s1_3-explore`" + ` | Codebase exploration for integration points | Parallel with s1_2 |

After classification, user confirms research depth. Then s1_2 and s1_3 run in parallel.`,
		},
		{
			Id:    "epoch-starting",
			Title: "Starting an Epoch",
			Content: `**Step 0: detect the tracker backend before the first write.**

Write the probe and the adapter by running the installer below against this skill file. It writes ` + "`" + `.agents.local/pasture/probe.sh` + "`" + ` and ` + "`" + `.agents.local/pasture/rec` + "`" + ` from the shipped fences in the Tracker Adapter section. Set ` + "`SKILL_FILE`" + ` to this file, run the installer, then the probe. Write ` + "`EPOCH.md`" + ` from the probe output before any task is created. The orchestrator runs this probe once; subagents read the header and never re-probe. A missing git origin is not an error backend. For pasture, the probe does not derive a namespace: record ` + "`namespace:`" + ` only to pin one, otherwise the adapter omits ` + "`--namespace`" + ` and Pasture derives it. For ledger, the probe does derive a namespace: it prints NS from derive_namespace, and every unpinned ledger write calls that same function. Copy NS to ` + "`namespace:`" + ` only to override it. A missing or unregistered author is a warning (` + "`WARN=no-author`" + `), not an error backend.

` + "```" + `bash
# pasture:install-tracker
install_tracker_assets() {
  skill=$1
  dest=${2:-.agents.local/pasture}
  if [ -z "$skill" ] || [ ! -f "$skill" ]; then
    printf 'install: no skill file\n  why: install_tracker_assets needs the rendered skill path\n  where: Starting an Epoch install step\n  fix: set SKILL_FILE to this skill and pass it as the first argument\n' >&2
    return 1
  fi
  mkdir -p "$dest" || return 1
  awk -v dest="$dest" '
    $0 == "<!-- pasture:asset probe -->" { kind = "probe"; next }
    $0 == "<!-- pasture:asset rec -->" { kind = "rec"; next }
    $0 ~ /^<!-- \/pasture:asset / { kind = ""; grab = 0; next }
    kind != "" && $0 == "~~~sh" { grab = 1; next }
    kind != "" && grab == 1 && $0 == "~~~" { grab = 0; next }
    grab == 1 && kind == "probe" { print > (dest "/probe.sh"); next }
    grab == 1 && kind == "rec" { print > (dest "/rec"); next }
  ' "$skill"
  if [ ! -s "$dest/probe.sh" ] || [ ! -s "$dest/rec" ]; then
    printf 'install: the skill did not emit probe.sh and rec\n  why: the Tracker Adapter fences were missing or empty\n  where: %s\n  fix: regenerate the skill so it contains pasture:asset probe and pasture:asset rec\n' "$skill" >&2
    return 1
  fi
  chmod +x "$dest/probe.sh" "$dest/rec"
}
# /pasture:install-tracker
install_tracker_assets "$SKILL_FILE" .agents.local/pasture
sh .agents.local/pasture/probe.sh
# Then write .agents.local/pasture/EPOCH.md from the keys named below. Do not
# copy a derived NS into namespace: unless you mean to pin it. In every shell
# session resolve the entry point:
EPOCH_REC=$(sed -n 's/^rec: //p' .agents.local/pasture/EPOCH.md | tail -1)
` + "```" + `

If the probe prints ` + "`BACKEND=error`" + `, stop and report before the first mutation: the store exists but cannot be used. If it prints ` + "`BACKEND=ledger`" + `, record ` + "`backend:`" + `, ` + "`ledger:`" + `, ` + "`rec:`" + ` and ` + "`probed_at:`" + ` from the output and continue; the adapter writes one JSONL line per mutation under that ledger and derives the namespace with the same function that printed NS. Offer a gitignore entry; never require one. If it prints ` + "`BACKEND=pasture`" + `, record ` + "`backend:`" + `, ` + "`db:`" + `, ` + "`author:`" + `, ` + "`rec:`" + ` and ` + "`probed_at:`" + ` from the output. Record ` + "`namespace:`" + ` only to pin one; the adapter then passes ` + "`--namespace`" + `, and that pin wins over Pasture's derivation.

**Step 0b: create the epoch feedback task once, and only on the pasture backend.** This is the only create. On the pasture backend, create the task once and record ` + "`feedback:`" + `. On the ledger backend, create nothing and record nothing. The lookup rule, including who may create the task, is in Signal Capture.

` + "```" + `bash
BACKEND=$(sed -n 's/^backend: //p' .agents.local/pasture/EPOCH.md | tail -1)
if [ "$BACKEND" = pasture ]; then
  FEEDBACK_ID_URI=$(sed -n 's/^feedback: //p' .agents.local/pasture/EPOCH.md | tail -1)
  if [ -z "$FEEDBACK_ID_URI" ]; then
    FEEDBACK_ID_URI=$([[rec:create]] --kind task --title "FEEDBACK: epoch signals" --phase unscoped)
    printf 'feedback: %s\n' "$FEEDBACK_ID_URI" >> .agents.local/pasture/EPOCH.md
  fi
fi
` + "```" + `

**Option 1: Manual Task Creation**
` + "```" + `bash
# Phase 1: Capture user request
REQUEST_ID_URI=$([[rec:create]] --kind task --title "REQUEST: {{feature}}" --phase request \
  --body "{{verbatim user request}}")
[[rec:label]] "$REQUEST_ID_URI" pasture:p1-user:s1_1-classify
[[rec:note]] "$REQUEST_ID_URI" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Requested role (not an assignment): architect"

# Then proceed through phases manually
` + "```" + `

Use the explicit task graph; no molecule subsystem is required.`,
		},
		{
			Id:    "epoch-phase-transitions",
			Title: "Phase Transitions",
			Content: `Each phase creates a task and chains dependencies. Cross-references use description frontmatter instead of peer-reference commands.

` + "```" + `bash
# After Phase 1 creates "${REQUEST_ID_URI}"
[[rec:block]] "${REQUEST_ID_URI}" --by "${ELICIT_ID_URI}"    # REQUEST blocked by ELICIT

# After Phase 2 creates "${ELICIT_ID_URI}" and URD
[[rec:block]] "${ELICIT_ID_URI}" --by "${PROPOSAL_ID_URI}"   # ELICIT blocked by PROPOSAL
# URD linked via frontmatter in its description:
#   references:
#     request: "${REQUEST_ID_URI}"
#     elicit: "${ELICIT_ID_URI}"

# After Phase 5 (UAT) and Phase 6 (ratify), update URD
[[rec:note]] "${URD_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "UAT results: {{summary}}"
[[rec:note]] "${URD_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Ratified: scope confirmed as {{summary}}"
` + "```" + ``,
		},
		{
			Id:    "epoch-followup-epic",
			Title: "Follow-up Epic",
			Content: `**Trigger:** UAT (Phase 5 or 11) produces one or more **user-DEFER'd items**.
The FOLLOWUP epic is fed ONLY by those DEFER'd UAT items — **never** by any review severity (review findings converge at the end; close rule: C-finding-lifecycle).
**Owner:** Supervisor creates the follow-up epic.

` + "```" + `bash
FOLLOWUP_EPIC_ID_URI=$([[rec:create]] --kind epic --title "FOLLOWUP: User-deferred improvements from UAT" --phase unscoped --priority 3 \
  --body "---
references:
  request: "${REQUEST_ID_URI}"
  uat: "${UAT_ID_URI}"
---
Aggregated user-DEFER'd items from UAT (Phase 5/11).")
[[rec:label]] "$FOLLOWUP_EPIC_ID_URI" pasture:epic-followup
` + "```" + `

### Follow-up lifecycle (same protocol, FOLLOWUP_* prefix)

The follow-up epic runs the same protocol phases with FOLLOWUP_* prefixed task types:

` + "```" + `
FOLLOWUP → FOLLOWUP_URE → FOLLOWUP_URD → FOLLOWUP_PROPOSAL-1 → FOLLOWUP_IMPL_PLAN → the follow-up slice
` + "```" + `

- **FOLLOWUP_URE**: Scoping URE with user to determine which DEFER'd items to address
- **FOLLOWUP_URD**: Requirements doc for follow-up scope (references original URD)
- **FOLLOWUP_PROPOSAL-{N}**: Proposal accounting for original URD + FOLLOWUP_URD + the DEFER'd items
- **FOLLOWUP_IMPL_PLAN**: Supervisor decomposes follow-up into slices
- **the follow-up slice**: Each slice implements the DEFER'd-item work decomposed into leaf tasks

See ` + "`" + `/pasture:supervisor` + "`" + ` and ` + "`" + `/pasture:impl-review` + "`" + ` for full creation commands.`,
		},
		{
			Id:    "epoch-eager-severity",
			Title: "EAGER Severity Tree (Phase 10)",
			Content: `Code reviews ALWAYS create ONE severity trio (3 group tasks) per wave, per axis, per round, even if empty:

` + "```" + `bash
# Create all 3 severity groups immediately (EAGER, not lazy)
BLOCKER_ID_URI=$([[rec:create]] --kind task --title "BLOCKER" --phase code_review \
   --body "Severity group for this review round; close only if empty")
[[rec:label]] "$BLOCKER_ID_URI" pasture:severity:blocker
IMPORTANT_ID_URI=$([[rec:create]] --kind task --title "IMPORTANT" --phase code_review \
   --body "Severity group for this review round; close only if empty")
[[rec:label]] "$IMPORTANT_ID_URI" pasture:severity:important
MINOR_ID_URI=$([[rec:create]] --kind task --title "MINOR" --phase code_review \
   --body "Severity group for this review round; close only if empty")
[[rec:label]] "$MINOR_ID_URI" pasture:severity:minor

# Empty groups are closed immediately
[[rec:close]] "${IMPORTANT_ID_URI}"
[[rec:close]] "${MINOR_ID_URI}"
` + "```" + `

**Dual-parent BLOCKER:** Bind BLOCKER_FINDING_ID_URI from the finding task's create output before running this fence. BLOCKER findings block both the severity group AND the slice:
` + "```" + `bash
[[rec:block]] "${BLOCKER_ID_URI}" --by "${BLOCKER_FINDING_ID_URI}"
[[rec:block]] "${SLICE_ID_URI}" --by "${BLOCKER_FINDING_ID_URI}"
` + "```" + `

See [reviewer-review-code](../reviewer-review-code/SKILL.md) for severity definitions.`,
		},
		{
			Id:    "epoch-tracking",
			Title: "Tracking Progress",
			Content: `` + "```" + `bash
# View dependency chain
[[rec:query]] --tree "${LATEST_TASK_ID_URI}"

# Check blocked work
[[rec:query]] --blocked

# See all epoch tasks by phase
[[rec:query]] --label="pasture:p1-user:s1_1-classify"    # REQUEST tasks
[[rec:query]] --label="pasture:p2-user:s2_1-elicit"      # ELICIT tasks
[[rec:query]] --label="pasture:p3-plan:s3-propose"        # PROPOSAL tasks
[[rec:query]] --phase worker_slices          # Implementation slices
` + "```" + ``,
		},
		{
			Id:      "epoch-skills-table",
			Title:   "Skills to Invoke",
			Content: "Each phase transition MUST include an explicit `[[op:invoke_skill]](...)` invocation directive. When launching agents for a phase, the prompt MUST tell the agent to call the corresponding skill as its first action.\n\n| Phase | Skill | Invocation Directive |\n|-------|-------|---------------------|\n| 1 (REQUEST: classify, research, explore) | `/pasture:user-request` | `[[op:invoke_skill]](/pasture:user-request)` |\n| 2 (ELICIT + URD) | `/pasture:user-elicit` | `[[op:invoke_skill]](/pasture:user-elicit)` |\n| 3-6 (PROPOSAL, REVIEW, UAT, RATIFY) | `/pasture:architect` | `[[op:invoke_skill]](/pasture:architect)` |\n| 5, 11 (UAT) | `/pasture:user-uat` | `[[op:invoke_skill]](/pasture:user-uat)` |\n| 7 (HANDOFF) | `/pasture:architect-handoff` | Architect calls `[[op:invoke_skill]](/pasture:architect-handoff)` after ratification |\n| 8-10 (IMPL_PLAN, SLICES, CODE REVIEW) | `/pasture:supervisor` | Supervisor prompt MUST start with `[[op:invoke_skill]](/pasture:supervisor)` |\n| 12 (LANDING) | Manual git commit and push | N/A |\n\n**CRITICAL — interviewing phases:** The interviewing phases MUST explicitly invoke their skill. Do **not** improvise interview questions:\n- **Phase 2 (URE):** invoke `[[op:invoke_skill]](/pasture:user-elicit)` — skipping it produces low-quality elicitation.\n- **Phases 5 & 11 (UAT):** invoke `[[op:invoke_skill]](/pasture:user-uat)` — it drives the FIX-NOW, DEFER, or WON'T-DO disposition and demonstrative examples.\n\n**CRITICAL:** When the architect hands off to the supervisor (Phase 7 → 8), the supervisor launch prompt MUST:\n1. Start with `[[op:invoke_skill]](/pasture:supervisor)` — without this, the supervisor skips role-critical procedures\n2. Include all Pasture task IDs (REQUEST, URD, RATIFIED PROPOSAL, HANDOFF)\n3. Include the HANDOFF Pasture task ID — the handoff is authored in that task body (no filesystem path)",
		},
		{
			Id:    "epoch-never-delete",
			Title: "Never Delete Policy",
			Content: `**DO:** Add labels, add comments, update status
**DON'T:** Close tasks prematurely, delete tasks, remove labels

` + "```" + `bash
# Correct: Add ratify label
[[rec:label]] "${PROPOSAL_ID_URI}" pasture:p6-plan:s6-ratify
[[rec:note]] "${PROPOSAL_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "RATIFIED: All reviewers ACCEPT"

# Wrong: Don't close
# [[rec:close]] "${PROPOSAL_ID_URI}"  # NEVER DO THIS
` + "```" + ``,
		},
	},

	Recipes: []RecipeBlock{},
}
