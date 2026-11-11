---
name: supervisor-track-progress
description: Monitor worker status via Pasture
---

# Supervisor: Track Progress

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.33 artifact v1 -->
**Command:** `pasture:supervisor:track-progress` — Monitor worker status via Pasture

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9

**[sup-track-poll-rate]**
- Given: workers running
- When: monitoring
- Then: check Pasture status at natural intervals (when a worker signals completion or blocker)
- Should not: poll aggressively or busy-wait in a tight loop

**[sup-track-partial-commit]**
- Given: worker complete
- When: all slices for a phase are done
- Then: proceed to code review or commit
- Should not: commit partial work — wait for all slices in the layer to complete

**[sup-track-resolve-blockers]**
- Given: worker blocked
- When: handling
- Then: resolve or reassign immediately
- Should not: leave workers waiting on a blocker without action

**[sup-track-urd-source-of-truth]**
- Given: requirements question arises
- When: resolving
- Then: consult the URD (`"${EPOCH_REC}" show "${URD_ID_URI}"`) as the single source of truth
- Should not: guess at user intent without checking the URD first

**[sup-track-severity-awareness]**
- Given: all slices complete
- When: transitioning to review
- Then: check for BLOCKER resolution tracking in the review severity groups
- Should not: skip severity awareness when moving to Phase 10

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

## When to Use

Workers spawned and running — monitoring for completions and blockers until all slices reach `closed` or a phase transition is warranted.

## Pasture Status Queries

```bash
# Check all implementation slices
"${EPOCH_REC}" query --phase worker_slices --status=in_progress

# Check for blocked slices
"${EPOCH_REC}" query --blocked --phase worker_slices

# Check specific task
"${EPOCH_REC}" show "${TASK_ID_URI}"

# Check completed slices
"${EPOCH_REC}" query --phase worker_slices --status=closed

# Check BLOCKER severity groups (during/after review)
"${EPOCH_REC}" query --label="pasture:severity:blocker" --status=open

# Check follow-up epic
"${EPOCH_REC}" query --label="pasture:epic-followup"
```

## Tracking via Pasture

All coordination happens through Pasture task state and authored comments:

```bash
# Check for task updates
"${EPOCH_REC}" show "${TASK_ID_URI}"

# Review comments for status updates
"${EPOCH_REC}" show "${TASK_ID_URI}" --notes

# Add coordination notes
"${EPOCH_REC}" note "${TASK_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "All slices complete — proceeding to Phase 10 (code review)"
```

## Status Patterns

| Status | Action |
|--------|--------|
| `closed` | Mark slice progress, check if all slices complete |
| Open blocked_by child | Review `"${EPOCH_REC}" show "${ID_URI}"` for blocker details, resolve or reassign |
| `in_progress` | Worker is actively working |

## Severity Awareness (Phase 10)

When tracking review progress, monitor severity groups:

| Severity | Blocks Slice? | Action |
|----------|---------------|--------|
| BLOCKER | Yes | An open BLOCKER gates the wave (dual-parent: also blocks the slice) |
| IMPORTANT | No (not via dual-parent) | Stays open past the wave gate; converges at the end (never routed to FOLLOWUP) |
| MINOR | No (not via dual-parent) | Stays open past the wave gate; converges at the end (never routed to FOLLOWUP) |

## Follow-up Lifecycle Tracking

```bash
# Track follow-up lifecycle progress
"${EPOCH_REC}" query --label="pasture:epic-followup"
"${EPOCH_REC}" query --label="pasture:p2-user:s2_1-elicit" --status=open   # FOLLOWUP_URE
"${EPOCH_REC}" query --label="pasture:p3-plan:s3-propose" --status=open     # FOLLOWUP_PROPOSAL
"${EPOCH_REC}" query --phase worker_slices --status=in_progress  # FOLLOWUP_SLICE in progress
```
<!-- END GENERATED FROM pasture schema -->
