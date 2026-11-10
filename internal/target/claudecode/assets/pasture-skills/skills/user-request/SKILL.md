---
name: user-request
description: Capture user feature request verbatim (Phase 1)
---

# User Request (Phase 1)

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
**Command:** `pasture:user:request` — Capture user feature request verbatim (Phase 1)

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-1-request-aurap1-user)** <- Phase 1

**[user-req-verbatim-capture]**
- Given: user provides request
- When: capturing
- Then: store verbatim without paraphrasing
- Should not: summarize or interpret

**[user-req-classify-label]**
- Given: request captured
- When: classifying
- Then: use `pasture:p1-user:s1_1-classify` label
- Should not: use other labels for the initial capture

**[user-req-research-depth]**
- Given: classification complete
- When: user confirms research depth
- Then: run s1_2-research and s1_3-explore in parallel
- Should not: skip research depth confirmation

**[user-req-proceed-to-elicit]**
- Given: Phase 1 complete
- When: proceeding
- Then: invoke `/pasture:user-elicit` for Phase 2
- Should not: skip to proposal

**[user-req-fix-intent]**
- Given: a request whose user intent is to FIX existing behavior (a bug, regression, or incorrect output)
- When: classifying in Phase 1
- Then: recognize the fix-intent SEMANTICALLY during classification (record it in the classification comment) so the downstream URE/UAT/impl validation cases capture the currently-failing behaviours; validation cases themselves are elicited for EVERY request regardless
- Should not: introduce a request-type axis or enum to detect fix-intent — recognition is semantic, not a fifth classification axis; gate validation cases on fix-intent

**[frag--validation-cases]**
- Given: any REQUEST (every request, not only fix-intent ones)
- When: eliciting (URE), acceptance-testing (UAT), or implementing
- Then: elicit concrete validation cases — a definition of done plus correct and incorrect behaviours (inputs/behaviors that must pass or must fail), confirm the case set with the user in UAT, evaluate the implementation against them, and store failing real-data cases as test fixtures
- Should not: ship without validation cases; treat validation cases as applying to fix-intent requests only; introduce a request-type axis or enum to gate them

## Task Recovery

The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami, signal.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

The adapter passes `--namespace` only when EPOCH.md records `namespace:`. That pin wins on both backends. Otherwise it omits the flag. Pasture then derives the namespace itself: PASTURE_NAMESPACE, else the git remote, then file:// of the working directory. The ledger calls derive_namespace for the header and for every write that has no pin. That function is the one list of remote forms; do not restate it. With no origin, both backends use file:// of the working directory, so invocations from different subdirectories use different namespaces. Queries without a pin follow that backend's derivation; pass `--all-namespaces` to opt out. When neither a pin nor PASTURE_NAMESPACE is set, pasture reads are unscoped and creation falls back to the git remote, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run `"${EPOCH_REC}" ready`, `"${EPOCH_REC}" query --blocked`, and `"${EPOCH_REC}" query --status in_progress`. For each referenced active task, run `"${EPOCH_REC}" show "$TASK_URI"`, `"${EPOCH_REC}" show "$TASK_URI" --notes`, and `"${EPOCH_REC}" show "$TASK_URI" --history`. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with `"${EPOCH_REC}" label "$TASK_URI" owner:<role>`. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: `"${EPOCH_REC}" block "$PARENT_URI" --by "$CHILD_URI"`. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records `backend: error`, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

## Task Attribution

Select the current registered author with "${EPOCH_REC}" whoami. It prints the author recorded in the header or PASTURE_AUTHOR, and on the pasture backend it confirms that actor with agents show. It does not list agents and it does not invent one. If neither source names an author, it stops: set author: in EPOCH.md or export PASTURE_AUTHOR. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## Model Policy and Fallback Chain

A role names a capability tier. A tier carries a neutral default model nickname, an effort level, and its default roles.

<!-- pasture:model-chain-ask -->

| Tier | Name | Default model | Effort | Effective roles |
|------|------|---------------|--------|----------------|
| T1 | frontier | frontier | high | architect, epoch, supervisor |
| T2 | standard | standard | medium | reviewer, worker |
| T3 | mini | mini | low | (runtime choice only) |

The reviewer's effective default is T2 standard, so the table lists reviewer under T2, not under T3. T3 mini has no effective default role; it is a phase-8 runtime choice. Record that choice as `reviewer_tier: T3` in `.agents.local/pasture/EPOCH.md` and pass that tier's native model id when reviewers are spawned. Each harness publishes every tier's native id in its emitted agent files (`capability-tier` lines). On a harness with no smaller model than T2, T3's id equals T2's; Codex is that harness, so a cheap review there uses the standard profile.

**Ask here, and at the matching gate in the other phase.** At phase 1 and again at phase 8, ask the user for three models: a primary, a secondary, and a tertiary. One gate, two asks, not five. The three form one fallback chain: when a provider is exhausted, use the next model in the chain; never fail the epoch. Record the chain on the request or IMPL_PLAN task and repeat it in every handoff. Advance the chain in the architect and supervisor surfaces too: they spawn the reviewers and workers, so a provider exhaustion reaches them first.

**Reviewer override.** The reviewer tier is the reviewer role's TierOverride, not a separate review-budget type. The default is T2 standard (full review); treat T3 mini as an epoch-recorded runtime choice, never a compile-time one.

**Concurrency cap.** Run at most 4 delegated agents at once in one stage. Phase 9 spawns one worker per slice and has no slice bound; when a plan has more slices than the cap, start them in batches of that size. The cap is a batching policy, not a protocol maximum. It bounds concurrency only; maxRounds remains the only bound on review rounds.

## Phase 1 Sub-steps

| Sub-step | Label | Description | Parallel? |
|----------|-------|-------------|----------|
| s1_1-classify | `pasture:p1-user:s1_1-classify` | Capture verbatim + classify along 4 axes | Sequential (first) |
| s1_2-research | `pasture:p1-user:s1_2-research` | Find domain standards, prior art | Parallel with s1_3 |
| s1_3-explore | `pasture:p1-user:s1_3-explore` | Codebase exploration for integration points | Parallel with s1_2 |

## Step 1: Capture and Classify (s1_1)



### Capture verbatim and create the request task

1. **Get the user's request verbatim:**
   ```
   AskUserQuestion: "What feature or change would you like to request?"
   ```

2. **Create the request task:**
   ```bash
   REQUEST_ID_URI=$("${EPOCH_REC}" create --kind task --title "REQUEST: {{short summary}}" --phase request \
     --body "{{VERBATIM user request - do not edit}}")
   "${EPOCH_REC}" label "$REQUEST_ID_URI" pasture:p1-user:s1_1-classify
   "${EPOCH_REC}" note "$REQUEST_ID_URI" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Requested role (not an assignment): architect"
   ```

3. **Classify along 4 axes:**
   - **Scope:** Single file, module, cross-cutting
   - **Complexity:** Low, medium, high
   - **Risk:** Breaking changes, new API, internal-only
   - **Domain novelty:** Familiar patterns vs new territory

4. **Record classification** via comment on the request task:
   ```bash
   "${EPOCH_REC}" note "${REQUEST_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body \
     "Classification: scope={{scope}}, complexity={{complexity}}, risk={{risk}}, novelty={{novelty}}"
   ```

### Recognize fix-intent (semantic, NOT a classification axis)

Separately from the 4 axes above, judge **semantically** whether the user's intent is to **FIX existing behavior** (a bug, regression, or wrong output) versus build something new. This is a recognition step, **not a fifth axis and not a `request-type` enum** — do not add a typed field for it.

When the intent is to fix existing behavior, the **validation-case lifecycle** applies for the rest of the epoch: concrete failing/expected cases are elicited in URE (`/pasture:user-elicit`), confirmed with the user in UAT (`/pasture:user-uat`), evaluated against the fix, and the failing real-data cases are stored as test fixtures.

Record the recognition in the same classification comment so downstream phases pick it up:
```bash
"${EPOCH_REC}" note "${REQUEST_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body \
  "Fix-intent: yes — validation-case lifecycle applies (elicit cases in URE, confirm in UAT, store as fixtures)"
```

## Step 2: Research Depth Confirmation

After classification, confirm research depth with the user:

```
AskUserQuestion:
  question: "Based on classification ({{scope}}, {{complexity}}, {{risk}}, {{novelty}}), how deep should research go?"
  header: "Research Depth"
  options:
    - label: "Quick scan"
      description: "Familiar domain, low complexity — brief prior art check"
    - label: "Standard research"
      description: "Moderate complexity or some novelty — find existing patterns and standards"
    - label: "Deep dive"
      description: "High complexity, new territory, or high risk — thorough domain analysis"
```

## Step 3: Record Depth + Spawn Parallel Agents (s1_2 || s1_3)



### Record depth and spawn agents

Record the user's depth choice, then spawn two parallel agents:

```bash
"${EPOCH_REC}" note "${REQUEST_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body \
  "Research depth: {{depth}} (user confirmed)"
```

Spawn both agents in parallel (with the Agent tool and `run_in_background: true`). Each agent invokes its dedicated skill.

### s1_2-research: Domain Research

Invoke `/pasture:research` with:
- **topic:** derived from the user's request
- **depth:** the user-confirmed research depth
- **request-task-id:** the REQUEST pasture task ID

The `/pasture:research` skill handles the full research workflow: depth-scoped checklist, structured report written to `docs/research/<topic>.md`, and summary comment on the REQUEST task.

See [skills/research/SKILL.md](../research/SKILL.md) for full procedure, output format, and examples.

**Depth determines scope:**

| Depth | Local | Web | Deliverable |
|-------|-------|-----|-------------|
| **Quick scan** | Grep project for related patterns, check README/docs | None | 1-paragraph summary of local findings |
| **Standard research** | Local scan + check project dependencies, related repos | Search for domain standards, established patterns | List of prior art with relevance notes |
| **Deep dive** | Full local analysis + dependency tree | Search for competing solutions, RFCs, academic papers, well-regarded projects | Structured report: standards found, competing approaches, recommended direction |

**Research checklist:**
1. What domain standards exist? (RFCs, specs, community conventions)
2. What well-regarded projects solve similar problems? (prior art)
3. What patterns are established in this domain? (idioms, best practices)
4. Are there existing solutions that could be reused or adapted?

**Record findings** as a comment on the REQUEST task:
```bash
"${EPOCH_REC}" note "${REQUEST_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body \
  "Research findings ({{depth}}):
  - Standards: {{list or 'none found'}}
  - Prior art: {{list of projects/solutions}}
  - Patterns: {{established approaches}}
  - Recommendation: {{brief direction}}
  - Full report: docs/research/{{topic}}.md"
```

### s1_3-explore: Codebase Exploration

Invoke `/pasture:explore` with:
- **topic:** derived from the user's request
- **depth:** the user-confirmed research depth (same depth applies)
- **request-task-id:** the REQUEST pasture task ID

The `/pasture:explore` skill handles the full exploration workflow: depth-scoped checklist, structured findings, and summary comment on the REQUEST task.

See [skills/explore/SKILL.md](../explore/SKILL.md) for full procedure, output format, and examples.

**Exploration checklist:**
1. **Entry points:** Where would this feature plug in? (CLI commands, API routes, event handlers)
2. **Data flow:** What existing data structures, types, or schemas are relevant?
3. **Dependencies:** What modules/packages would this feature depend on or extend?
4. **Existing patterns:** How do similar features work in this codebase? (conventions, DI patterns, test structure)
5. **Conflicts:** Are there existing implementations that would need modification or could conflict?

**Depth determines thoroughness:**

| Depth | Scope | Tools |
|-------|-------|-------|
| **Quick scan** | Grep for keywords, check obvious entry points | Glob, Grep |
| **Standard research** | Trace data flow, map dependencies, read related modules | Glob, Grep, Read |
| **Deep dive** | Full dependency graph, architectural analysis, identify all touchpoints | Glob, Grep, Read, Bash (for build/dep tools) |

**Record findings** as a comment on the REQUEST task:
```bash
"${EPOCH_REC}" note "${REQUEST_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body \
  "Explore findings ({{depth}}):
  - Entry points: {{list of files/functions}}
  - Related types: {{existing types/schemas}}
  - Dependencies: {{modules this would use}}
  - Patterns: {{how similar features work here}}
  - Conflicts: {{potential issues or 'none'}}"
```

### Completion

Both agents must complete before proceeding to Phase 2. Their findings are recorded as comments on the REQUEST task, available for the elicitation survey and proposal phases.

## Example

User says: "I want to add a logout button to the header that clears the session and redirects to the login page"

```bash
REQUEST_ID_URI=$("${EPOCH_REC}" create --kind task --title "REQUEST: Add logout button to header" --phase request \
  --body "I want to add a logout button to the header that clears the session and redirects to the login page")
"${EPOCH_REC}" label "$REQUEST_ID_URI" pasture:p1-user:s1_1-classify
"${EPOCH_REC}" note "$REQUEST_ID_URI" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Requested role (not an assignment): architect"

"${EPOCH_REC}" note "${REQUEST_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body \
  "Classification: scope=module, complexity=low, risk=internal-only, novelty=familiar"
```

## Next Phase

After Phase 1 completes, invoke `/pasture:user-elicit` to begin requirements elicitation (Phase 2).

The elicit task will block this request task:
```bash
"${EPOCH_REC}" block "${REQUEST_ID_URI}" --by "${ELICIT_ID_URI}"
```
<!-- END GENERATED FROM pasture schema -->
