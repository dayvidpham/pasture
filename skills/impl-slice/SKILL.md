# Implementation Slice (Phase 9)

<!-- BEGIN GENERATED FROM aura schema -->
**Command:** `aura:impl:slice` — Vertical slice assignment and tracking

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9

**[impl-slice-full-specs]**
- Given: IMPL_PLAN complete
- When: assigning slices
- Then: create SLICE-N tasks with full specs
- Should not: leave specs vague

**[impl-slice-dep-chain]**
- Given: slice assigned
- When: creating task
- Then: chain dependency to IMPL_PLAN: bd dep add <impl-plan-id> --blocked-by <slice-id>
- Should not: create orphan slices

**[impl-slice-track-status]**
- Given: worker starts
- When: tracking
- Then: update task to in_progress
- Should not: leave status as open

**[impl-slice-complete-label]**
- Given: slice complete
- When: verifying
- Then: add completion label and comments
- Should not: close the task prematurely

## Slice Structure

Each vertical slice contains:
- **slice_id**: Identifier (SLICE-1, SLICE-2, SLICE-3, ...)
- **slice_name**: Human-readable name
- **slice_spec**: Detailed implementation specification
- **slice_files**: Files owned by this slice

## Creating Slices

After supervisor decomposes the ratified plan:

```bash
# Create SLICE-1
bd create --labels "aura:p9-impl:s9-slice" \
  --title "SLICE-1: <slice name>" \
  --description "---
references:
  impl_plan: <impl-plan-task-id>
  urd: <urd-task-id>
---
## Specification
<detailed implementation spec>

## Files Owned
<list of files this slice owns>

## Acceptance Criteria
<criteria from ratified plan>

## Validation Checklist
- [ ] Types defined
- [ ] Tests written (import production code)
- [ ] Implementation complete
- [ ] Wiring complete
- [ ] Production code path verified" \
  --design='{"validation_checklist":["Types defined","Tests written (import production code)","Implementation complete","Wiring complete","Production code path verified"],"acceptance_criteria":[{"given":"X","when":"Y","then":"Z"}],"ratified_plan":"<ratified-plan-id>"}' \
  --assignee worker-1

bd dep add <impl-plan-id> --blocked-by <slice-1-id>
```

## Assigning Workers

```bash
bd update <slice-1-id> --assignee="worker-1"
bd update <slice-2-id> --assignee="worker-2"
bd update <slice-3-id> --assignee="worker-3"
```

## Tracking Progress

```bash
# Worker starts
bd update <slice-id> --status in_progress

# Check all slice status
bd list --labels="aura:p9-impl:s9-slice" --status=open
bd list --labels="aura:p9-impl:s9-slice" --status=in_progress

# Worker completes (add comment and label)
bd comments add <slice-id> "COMPLETE: All checklist items verified. Production code path working."
bd label add <slice-id> aura:p9-impl:slice-complete
```

## Slice Dependencies

Slices can have dependencies on each other (sync points):

```bash
# SLICE-2 depends on SLICE-1 completing first
bd dep add <slice-2-id> --blocked-by <slice-1-id>
```

Minimize inter-slice dependencies when possible.

## Aggregation

The aggregation step waits for all slices to complete before code review:

```bash
# Check if all slices have complete label
bd list --labels="aura:p9-impl:slice-complete"

# Compare to total slices
bd list --labels="aura:p9-impl:s9-slice"
```

## Follow-up Slices (FOLLOWUP_SLICE-N)

Follow-up slices use the same structure and tracking, with additional fields:
- **Title prefix:** `FOLLOWUP_SLICE-N:` (e.g., `FOLLOWUP_SLICE-1: Add request-id correlation`)
- **Adopted leaf tasks:** Original IMPORTANT/MINOR leaf tasks from review become dual-parent children (original severity group + follow-up slice)
- **Tracking:** Same `bd list --labels="aura:p9-impl:s9-slice"` queries include both regular and follow-up slices
<!-- END GENERATED FROM aura schema -->
