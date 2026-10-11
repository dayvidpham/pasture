---
name: user-request
description: Capture user feature request verbatim (Phase 1)
---

# User Request (Phase 1)

<!-- BEGIN GENERATED FROM pasture schema -->
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

Recover from live Pasture state at session start and after compaction; never trust cached task rows.

Store selection: an explicit --db overrides PASTURE_DB_PATH; otherwise use XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db. Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

Set PASTURE_NAMESPACE to the repository's canonical namespace URI, for example https://github.com/dayvidpham/pasture. Explicit --namespace overrides the git-remote-derived namespace, then file:// of the working directory. List requires an explicit namespace to avoid mixing repositories. ready/blocked have only an exact --label filter, not namespace filtering; inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run pasture task ready, pasture task blocked, and pasture task list --namespace "$PASTURE_NAMESPACE" --status in_progress. For each referenced active task, run pasture task show "$TASK_URI", pasture task comments "$TASK_URI", and pasture task timeline "$TASK_URI". All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the actual JSON id returned by create; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create returns an object whose id is the task URI: capture it with --format json and python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'. Persist that URI before label/comment steps. Create-plus-label is non-atomic: on failure retain the URI and retry only the failed label/comment operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

Assignment transfer is not initial allocation: before using pasture task assignment transfer, obtain the existing owner-responsibility assignment and its exact successor assignment ID, registered committing actor, and registered worker occupant from the supervisor. If the task is unassigned, stop and let the supervisor arrange allocation; no assignment-start command is implied. Never repeat an identical transfer as a substitute for allocating different workers.

Parent stays open and is blocked by child: pasture task dep add "$PARENT_URI" --blocked-by "$CHILD_URI". Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

## Task Attribution

Select the current registered author with pasture task agents list and pasture task agents show ACTOR-ID. Never auto-register or guess an identity during recovery. Every comment supplies --author explicitly; machine examples use the pre-registered pasture-system--00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

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
   REQUEST_ID_URI=$(pasture task create "REQUEST: {{short summary}}" --phase request --namespace "$PASTURE_NAMESPACE" --format json \
     --description "{{VERBATIM user request - do not edit}}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
   pasture task label add "$REQUEST_ID_URI" pasture:p1-user:s1_1-classify
   pasture task update "$REQUEST_ID_URI" --notes "Requested role (not an assignment): architect"
   ```

3. **Classify along 4 axes:**
   - **Scope:** Single file, module, cross-cutting
   - **Complexity:** Low, medium, high
   - **Risk:** Breaking changes, new API, internal-only
   - **Domain novelty:** Familiar patterns vs new territory

4. **Record classification** via comment on the request task:
   ```bash
   pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
     "Classification: scope={{scope}}, complexity={{complexity}}, risk={{risk}}, novelty={{novelty}}"
   ```

### Recognize fix-intent (semantic, NOT a classification axis)

Separately from the 4 axes above, judge **semantically** whether the user's intent is to **FIX existing behavior** (a bug, regression, or wrong output) versus build something new. This is a recognition step, **not a fifth axis and not a `request-type` enum** — do not add a typed field for it.

When the intent is to fix existing behavior, the **validation-case lifecycle** applies for the rest of the epoch: concrete failing/expected cases are elicited in URE (`/pasture:user-elicit`), confirmed with the user in UAT (`/pasture:user-uat`), evaluated against the fix, and the failing real-data cases are stored as test fixtures.

Record the recognition in the same classification comment so downstream phases pick it up:
```bash
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
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
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
  "Research depth: {{depth}} (user confirmed)"
```

Spawn both agents in parallel (via Task tool with `run_in_background: true`). Each agent invokes its dedicated skill.

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
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
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
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
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
REQUEST_ID_URI=$(pasture task create "REQUEST: Add logout button to header" --phase request --namespace "$PASTURE_NAMESPACE" --format json \
  --description "I want to add a logout button to the header that clears the session and redirects to the login page" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$REQUEST_ID_URI" pasture:p1-user:s1_1-classify
pasture task update "$REQUEST_ID_URI" --notes "Requested role (not an assignment): architect"

pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
  "Classification: scope=module, complexity=low, risk=internal-only, novelty=familiar"
```

## Next Phase

After Phase 1 completes, invoke `/pasture:user-elicit` to begin requirements elicitation (Phase 2).

The elicit task will block this request task:
```bash
pasture task dep add "${REQUEST_ID_URI}" --blocked-by "${ELICIT_ID_URI}"
```
<!-- END GENERATED FROM pasture schema -->
