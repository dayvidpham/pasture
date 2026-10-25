---
name: afk-implement
description: Run an unattended implementation session with small PRs and a clean review wave, never merging
---

# AFK Implement

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:afk-implement` — Run an unattended implementation session with small PRs and a clean review wave, never merging

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-8-implementation-plan)** <- Phases 8-10

**[B-afk-no-merge]**
- Given: an unattended session with green CI and a clean review wave
- When: a pull request is ready
- Then: leave it open, record the merge order and evidence, and stop
- Should not: merge, self-approve on the human's behalf, or push to the default branch

**[B-afk-smallest-iteration]**
- Given: an approved direction
- When: scoping work
- Then: ship the smallest independently reviewable and revertable change per iteration
- Should not: batch unrelated changes, add speculative abstraction, or refactor adjacent code

**[B-afk-decide-dont-ask]**
- Given: a reversible design choice
- When: the human is away
- Then: decide from repository conventions, record the rationale and reversal path, and continue
- Should not: block or idle waiting for approval

**[B-afk-clean-wave]**
- Given: a completed iteration
- When: running the review wave
- Then: require three independent ACCEPT votes with zero blockers and zero importants
- Should not: substitute self-review, ignore findings, or fabricate consensus

**[B-afk-stop-at-clean]**
- Given: every scoped iteration has an open PR and the final cross-stack wave is clean
- When: the run is complete
- Then: stop and write the morning handoff
- Should not: start new scope or continue looking for work

**[B-afk-evidence]**
- Given: a claimed quality gate
- When: reporting the result
- Then: name the exact command, the commit it ran against, and the outcome
- Should not: claim a gate that did not run or report green for a job that never executed

## Task Recovery

Recover from live Pasture state at session start and after compaction; never trust cached task rows.

Store selection: an explicit --db overrides PASTURE_DB_PATH; otherwise use XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db. Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

Set PASTURE_NAMESPACE to the repository's canonical namespace URI, for example https://github.com/dayvidpham/pasture. The CLI reads it as the default for --namespace when the flag is omitted; an explicit --namespace always wins. task list, task ready, and task blocked are scoped to that effective namespace, and --all-namespaces ignores both sources. When neither source is set, those reads are unscoped. Creation still falls back to the git-remote-derived namespace, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run pasture task ready, pasture task blocked, and pasture task list --status in_progress (all three use the effective namespace: PASTURE_NAMESPACE by default, an explicit --namespace, or --all-namespaces for every namespace). For each referenced active task, run pasture task show "$TASK_URI", pasture task comments "$TASK_URI", and pasture task timeline "$TASK_URI". All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the actual JSON id returned by create; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create returns an object whose id is the task URI: capture it with --format json and python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'. Persist that URI before label/comment steps. Create-plus-label is non-atomic: on failure retain the URI and retry only the failed label/comment operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

Assignment transfer is not initial allocation: before using pasture task assignment transfer, obtain the existing owner-responsibility assignment and its exact successor assignment ID, registered committing actor, and registered worker occupant from the supervisor. If the task is unassigned, stop and let the supervisor arrange allocation; no assignment-start command is implied. Never repeat an identical transfer as a substitute for allocating different workers.

Parent stays open and is blocked by child: pasture task dep add "$PARENT_URI" --blocked-by "$CHILD_URI". Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

## Task Attribution

Select the current registered author with pasture task agents list and pasture task agents show ACTOR-ID. Never auto-register or guess an identity during recovery. Every comment supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## When to Use

Use this skill when the human hands over a scoped direction and expects to review later: they say they are going AFK or to bed, ask for overnight or unattended implementation, say "don't block on me", ask for progressive adoption, or ask for open pull requests that must not be merged. It also fits an in-flight branch that needs its gates run, a PR opened, and a review wave driven to clean.

Do not use it for an attended session where the human expects the normal live URE/UAT gates, and do not use it to authorize a release, deploy, or merge.

## Invocation Contract

Translate the human's words into explicit defaults, and state the mapping in the handoff.

| The human says | Default |
|---|---|
| Act as the supervisor | Own the implementation and review loop; spawn workers and reviewers instead of serializing every part. |
| Use a named model for all subagents | Resolve the exact provider/model ID with the harness before spawning, pass it on every subagent call, and record any substitution. |
| Scope smaller; minimal code; YAGNI | One smallest end-to-end change per iteration; no speculative flags, config, abstraction, or drive-by edits. |
| Progressive adoption | Each iteration is independently reviewable and revertable; prefer several small PRs to one large one. |
| Stack changes when needed | Base an iteration on its predecessor only when it depends on it; otherwise branch from the default branch. |
| Open PRs, do not merge | Push feature branches and open ready PRs; never merge or push to the default branch. |
| Stop on a clean review wave | Use the exit criteria below; do not keep burning the backlog after clean. |
| Handle design questions sensibly | Decide reversible questions from the repository, record the rationale, and continue. |
| Review in the morning | Write a five-minute handoff with merge order, decisions, risks, and first checks. |

## Operating Rules

These are not negotiable, including when CI is green and every reviewer accepts:

- Never merge. The human merges after review.
- Never push to the default or protected branch; feature branches only.
- Rewrite only your own feature branches, with force-with-lease, and only to rebase a stack.
- No destructive commands, no shared-history rewrites, no branch or data deletion, and no Git hook installation or hook-path changes.
- Preserve the primary checkout, including uncommitted work. Do all implementation in a worktree.
- Reviewers must be independent subagents. Never write their findings or votes yourself.
- Never claim a gate passed unless it ran on the commit you report.

## Iteration Loop

Work one iteration at a time, and open its PR before starting the next.

1. Recover. Read live tracker state, Git status, branches ahead of the base, worktrees, and open PRs. If the human said to work on something specific, inventory the existing work instead of restarting it.
2. Choose the next iteration. Follow the named scope first. Otherwise prefer a failing test or reproduction, a correctness bug, the smallest missing piece of the approved feature, then cleanup that unblocks it. Do not invent product scope.
3. Plan in one paragraph: change, files, test, acceptance case, risk, base branch. If it does not fit, split it.
4. Create or reuse a worktree using the repository naming convention (repository, issue number, commit type, short description); branch and directory share the name.
5. Reproduce or write the failing test first where practical, then make the smallest production change that satisfies it.
6. Run the repository gates for the touched surface, then the full gate when cheap. Record the exact commands and outcomes.
7. Commit atomically with the repository's agent-commit path. Stage only files this iteration owns.
8. Push the branch and open the PR. Then run the review wave, fix findings, re-review, and only then start the next iteration.

## Pull Request Contract

Open a ready PR so CI runs, not a draft, unless the iteration is hard-stopped.

The body carries, in order:

1. What and why, in one paragraph.
2. Scope boundary: what this PR deliberately does not do.
3. Acceptance: the Given/When/Then case, or the observed before and after.
4. Verification: exact commands, results, and CI status.
5. Stack: position, base branch, and merge order, or standalone.
6. Decisions: choice, rationale, alternatives, and how to reverse it.
7. Known gaps: residual findings, flaky tests, unverified paths.

A hard-stopped iteration is a draft PR whose body names the exact blocker and next action. Never present a broken PR as ready, and never merge or close it.

## Stacking

Stack only when the change genuinely depends on its predecessor. Name the base branch in the PR body and the handoff, and order the merge bottom-up. When a base moves, rebase only your own branches and push with force-with-lease, then re-run the gates.

Prefer not stacking. Two iterations that touch the same files are usually one iteration.

## Review Wave

Spawn three independent reviewers in parallel with the model named in the invocation. Give each the same inputs and a distinct axis.

### Axes

| Axis | Ask |
|---|---|
| Correctness | Does the diff do what the PR claims without breaking existing behavior? Check edge cases, error paths, data loss, and concurrency. |
| Tests | Do tests exercise the production path rather than a test-only export? Do they cover failure cases and the acceptance case? Would they catch a regression? Are they flaky? |
| Elegance and YAGNI | Is complexity proportional? Any speculative abstraction, unused flag, duplicated logic, or scope creep? Is there a materially smaller change? |

Require findings as severity (blocker, important, minor), file and line, problem, and a concrete fix. Require a binary vote: ACCEPT or REVISE. Load the impl-review skill for the full severity-group and dual-parent procedure when the run uses tracker severity tasks.

### Clean Wave

A clean wave is three ACCEPT votes with zero blockers and zero importants. Fix blockers and importants in the same branch, commit atomically, push, and re-run the wave. Fix a minor finding if trivial; otherwise record it as follow-up. Cap the loop at three cycles per PR.

If a blocker survives three cycles, mark the PR draft, write the exact blocker and attempted fixes, and move to independent work. Do not merge and do not call the wave clean.

After the final iteration, run one cross-stack wave over all open PRs or the cumulative diff: conflicting assumptions, duplicated helpers, and merge-order breakage. That is the wave that must end clean before stopping.

## Autonomous Decisions

Use this precedence, highest first:

1. The human's explicit request and the repository's documented invariants.
2. Existing repository conventions: naming, error style, test patterns, and gates.
3. The smallest, most reversible option.
4. A prototype behind no public API.

Record every non-obvious decision in the PR body: decision, why, alternatives, and reversal. If two options are plausible and reversible, pick the smaller one and keep moving.

Reasonable autonomy includes naming, file placement, test shape, helper extraction, error wording, choosing between equivalent libraries already present, and splitting or merging iterations.

### Hard Stops

Stop the current line of work, leave a clear note, and continue only with independent iterations when one of these is true:

- Credentials, permissions, a paid resource, or a secret is required.
- The action is irreversible or destructive: rewriting shared history, deleting data or branches, installing or altering Git hooks, or force-pushing a shared branch.
- It reaches production: deploy, release, publish, tag, or migrate live data.
- Product intent is genuinely ambiguous and both options change user-facing behavior irreversibly.
- The change is security-sensitive and there is no safe default.

Not hard stops: failing tests, flaky tests, CI infrastructure outages, merge conflicts inside your own stack, missing documentation, an unavailable model (fall back and record it), or an unpleasant design tradeoff.

## Verification and Evidence

Discover the gates from the repository's agent instructions, Makefile, and CI. Run the narrow gate while iterating and the full gate before calling a PR ready. Docs-only changes still run docs, link, and frontmatter checks.

If CI cannot run, say so explicitly and report local results instead. Keep the tree quiescent while verifying, and record the commit SHA the results apply to.

## Tracking

Create one tracker task per iteration and link it to the parent scope. Add labels and comments; never delete audit history. Comment the branch, commit SHA, PR URL, gate results, and each review round with its votes.

Workers report evidence and never close their own tasks. Close only after the clean wave, and only for work you supervised and verified. If the tracker is unavailable, report the store path, the failed operation, and the repair needed, then continue with Git and PR evidence. A failed query is not proof of no work.

## Morning Handoff

Write the handoff to the repository's untracked agent-handoff directory, for example `.agents.local/afk-<YYYY-MM-DD>.md`, and repeat the essentials in the final message:

1. State: why the run stopped, what is ready, and what is not.
2. PR table: link, one-line summary, base, stack position, CI, review result, and recommended merge order.
3. Decisions: choice and rationale for each non-obvious call.
4. Risks: residual findings, unverified paths, and flaky or skipped gates.
5. Scope boundary: what you deliberately did not do.
6. First three checks: the fastest way for the human to validate the work.

Never describe unmerged work as done or shipped. Say open, review clean, awaiting merge.

## Exit Criteria

Stop the session when all of these are true:

- Every scoped iteration has an open PR, or is a draft with a hard-stop reason.
- The final cross-stack review wave is clean: three ACCEPT, zero blockers, zero importants, or the three-cycle budget is exhausted and residual findings are recorded.
- The morning handoff is written and names the merge order.
- No merge occurred, the default branch is untouched, and no unrelated work was modified.

Then stop and report. Do not start new scope, do not make one more small fix, and do not wait or poll for the human.

## Anti-Patterns

- Merging because CI is green or the human seemed to want speed.
- One giant PR that cannot be adopted progressively.
- Silencing, skipping, or deleting a failing test to reach green.
- Refactoring adjacent code because it was already open.
- Asking a question mid-run and idling, or blocking forever on approval.
- Calling a wave clean without independent reviewers or without reading their findings.
- Expanding the backlog instead of stopping at the approved scope.
- Describing a red or draft PR as ready in the handoff.
<!-- END GENERATED FROM pasture schema -->
