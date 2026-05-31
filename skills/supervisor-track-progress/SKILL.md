# Supervisor: Track Progress

<!-- BEGIN GENERATED FROM aura schema -->
**Command:** `aura:supervisor:track-progress` — Monitor worker status via Beads

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9

**[sup-track-poll-rate]**
- Given: workers running
- When: monitoring
- Then: check Beads status at natural intervals (when a worker signals completion or blocker)
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
- Then: consult the URD (`bd show <urd-id>`) as the single source of truth
- Should not: guess at user intent without checking the URD first

**[sup-track-severity-awareness]**
- Given: all slices complete
- When: transitioning to review
- Then: check for BLOCKER resolution tracking in the review severity groups
- Should not: skip severity awareness when moving to Phase 10

## When to Use

Workers spawned and running — monitoring for completions and blockers until all slices reach `done` or a phase transition is warranted.

## Beads Status Queries

```bash
# Check all implementation slices
bd list --labels="aura:p9-impl:s9-slice" --status=in_progress

# Check for blocked slices
bd list --labels="aura:p9-impl:s9-slice" --status=blocked

# Check specific task
bd show <task-id>

# Check completed slices
bd list --labels="aura:p9-impl:s9-slice" --status=done

# Check BLOCKER severity groups (during/after review)
bd list --labels="aura:severity:blocker" --status=open

# Check follow-up epic
bd list --labels="aura:epic-followup"
```

## Tracking via Beads

All coordination happens through beads task status and comments:

```bash
# Check for task updates
bd show <task-id>

# Review comments for status updates
bd comments <task-id>

# Add coordination notes
bd comments add <task-id> "All slices complete — proceeding to Phase 10 (code review)"
```

## Status Patterns

| Status | Action |
|--------|--------|
| `done` | Mark slice progress, check if all slices complete |
| `blocked` | Review `bd show <id>` for blocker details, resolve or reassign |
| `in_progress` | Worker is actively working |

## Severity Awareness (Phase 10)

When tracking review progress, monitor severity groups:

| Severity | Blocks Slice? | Action |
|----------|---------------|--------|
| BLOCKER | Yes | Must resolve before proceeding to Phase 11 |
| IMPORTANT | No | Goes to follow-up epic (`aura:epic-followup`) |
| MINOR | No | Goes to follow-up epic (`aura:epic-followup`) |

## Follow-up Lifecycle Tracking

```bash
# Track follow-up lifecycle progress
bd list --labels="aura:epic-followup"
bd list --labels="aura:p2-user:s2_1-elicit" --status=open   # FOLLOWUP_URE
bd list --labels="aura:p3-plan:s3-propose" --status=open     # FOLLOWUP_PROPOSAL
bd list --labels="aura:p9-impl:s9-slice" --status=in_progress  # FOLLOWUP_SLICE in progress
```
<!-- END GENERATED FROM aura schema -->
