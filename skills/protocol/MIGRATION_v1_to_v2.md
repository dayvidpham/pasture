# Migration Procedure: Pasture Protocol v1 → v2

This document describes how to migrate existing Aura protocol usage from v1 labels and conventions to v2.

## Principles

1. **Additive approach:** Old labels are NOT removed. New labels are ADDED alongside existing ones.
2. **Old title conventions recognized:** During the transition period, both old-format titles (e.g., `REQUEST_PLAN:`, `PROPOSE_PLAN:`) and new-format titles (e.g., `REQUEST:`, `PROPOSAL-N:`) are recognized.
3. **No retroactive restructuring:** Existing dependency trees are NOT modified. New labels and conventions apply to new tasks only.
4. **Completion criterion:** Migration is complete when no tasks with ONLY old-format labels remain open. Closed tasks are left as-is.

---

## Label Mapping

| v1 Label | v2 Label | Notes |
|----------|----------|-------|
| `pasture:plan:request` | `pasture:p1-user:s1_1-classify` | Phase 1 now has 3 sub-steps |
| — (new) | `pasture:p1-user:s1_2-research` | New: parallel research step |
| — (new) | `pasture:p1-user:s1_3-explore` | New: parallel explore step |
| `pasture:plan:request` (elicit) | `pasture:p2-user:s2_1-elicit` | Elicitation now Phase 2 |
| `pasture:urd` | `pasture:urd` | Unchanged (also `pasture:p2-user:s2_2-urd`) |
| `pasture:plan:propose` | `pasture:p3-plan:s3-propose` | Title: PROPOSAL-N (not PROPOSE_PLAN) |
| `pasture:review` | `pasture:p4-plan:s4-review` | Title: PROPOSAL-N-REVIEW-{axis}-{round} |
| `pasture:plan:revision` | `pasture:p3-plan:s3-propose` | Revisions are new PROPOSAL-N (incremented N) |
| — (new) | `pasture:p5-user:s5-uat` | Plan UAT now explicit phase |
| `pasture:plan:ratified` | `pasture:p6-plan:s6-ratify` | Ratification now Phase 6 |
| `pasture:plan:ratify` | `pasture:p6-plan:s6-ratify` | Same as above |
| — (new) | `pasture:p7-plan:s7-handoff` | Handoff now explicit phase |
| `pasture:impl` (plan) | `pasture:p8-impl:s8-plan` | IMPL_PLAN title unchanged |
| `pasture:impl` (slice) | `pasture:p9-impl:s9-slice` | Title: SLICE-N (not [SLICE]) |
| — (new) | `pasture:p10-impl:s10-review` | Code review now explicit phase |
| — (new) | `pasture:p11-user:s11-uat` | Impl UAT now explicit phase |
| — (new) | `pasture:p12-impl:s12-landing` | Landing now explicit phase |
| — (new) | `pasture:superseded` | Marks superseded proposals |
| — (new) | `pasture:severity:blocker` | Severity group (was: no formal label) |
| — (new) | `pasture:severity:important` | Severity group (was: no formal label) |
| — (new) | `pasture:severity:minor` | Severity group (was: no formal label) |
| — (new) | `pasture:epic-followup` | Follow-up epic |

---

## Title Mapping

| v1 Title Convention | v2 Title Convention |
|---------------------|---------------------|
| `REQUEST_PLAN: Description` | `REQUEST: Description` |
| `PROPOSE_PLAN: Description` | `PROPOSAL-N: Description` |
| `REVISION_1: Description` | `PROPOSAL-N: Description` (N incremented) |
| `REVIEW_1/2/3: Description` | `PROPOSAL-N-REVIEW-{axis}-{round}: Description` |
| `RATIFIED_PLAN: Description` | (ratified via `pasture:p6-plan:s6-ratify` label; old proposals marked `pasture:superseded`) |
| `[SLICE] Implement 'X'` | `SLICE-N: Description` |
| `IMPLEMENTATION_PLAN: Description` | `IMPL_PLAN: Description` |
| `URD: Description` | `URD: Description` (unchanged) |

---

## Vocabulary Mapping

### Votes

| v1 Vote | v2 Vote | Notes |
|---------|---------|-------|
| APPROVE | ACCEPT | Renamed |
| APPROVE_WITH_COMMENTS | — (removed) | Use ACCEPT + severity tree for findings |
| REQUEST_CHANGES | REVISE | Renamed |
| REJECT | — (removed) | Use REVISE with BLOCKER findings |

### Severity

| v1 Severity | v2 Severity | Notes |
|-------------|-------------|-------|
| BLOCKING | BLOCKER | Renamed |
| MAJOR | IMPORTANT | Renamed |
| MINOR | MINOR | Unchanged |

### Commands

| v1 Command | v2 Replacement | Notes |
|------------|---------------|-------|
| `bd dep relate <a> <b>` | Frontmatter `references:` in description | Peer references via description frontmatter |
| `bd label add <id> pasture:plan:propose` | `bd label add <id> pasture:p3-plan:s3-propose` | New label format |
| `bd label add <id> pasture:plan:ratify` | `bd label add <id> pasture:p6-plan:s6-ratify` | New label format |

---

## Step-by-Step Migration Procedure for In-Flight Epics

### Step 1: Audit Open Tasks

```bash
# List all open tasks with old-format labels
bd list --status=open --labels="pasture:plan:request"
bd list --status=open --labels="pasture:plan:propose"
bd list --status=open --labels="pasture:plan:revision"
bd list --status=open --labels="pasture:review"
bd list --status=open --labels="pasture:plan:ratified"
bd list --status=open --labels="pasture:impl"
```

### Step 2: Add New Labels (Additive)

For each open task, add the corresponding v2 label WITHOUT removing the v1 label:

```bash
# Example: REQUEST_PLAN task
bd label add <task-id> pasture:p1-user:s1_1-classify
# Old pasture:plan:request label remains

# Example: PROPOSE_PLAN task
bd label add <task-id> pasture:p3-plan:s3-propose
# Old pasture:plan:propose label remains

# Example: Implementation slice
bd label add <task-id> pasture:p9-impl:s9-slice
# Old pasture:impl label remains
```

### Step 3: Add Frontmatter References

For tasks that previously used `bd dep relate`, add frontmatter references to the description:

```bash
# Instead of: bd dep relate <urd-id> <request-id>
# Update URD description to include frontmatter:
bd update <urd-id> --description "---
references:
  request: <request-id>
  elicit: <elicit-id>
---
$(bd show <urd-id> --field=description)"
```

### Step 4: Update Titles (Optional)

Optionally rename task titles to match new conventions. This is cosmetic and not required for migration:

```bash
# Example: REQUEST_PLAN → REQUEST
bd update <task-id> --title "REQUEST: Description"

# Example: PROPOSE_PLAN → PROPOSAL-1
bd update <task-id> --title "PROPOSAL-1: Description"
```

### Step 5: Create Missing Phase Tasks

If the in-flight epic predates v2, create tasks for phases that didn't exist in v1:

- **Handoff task** (Phase 7) — if architect-to-supervisor transition already happened, create retroactively and close
- **Code review tasks** (Phase 10) — if reviews already happened, no action needed
- **Severity groups** — only needed for future review rounds

### Step 6: Verify Completion

```bash
# Check: no open tasks with ONLY old labels (no corresponding v2 label)
bd list --status=open --labels="pasture:plan:request" | while read id; do
  bd show $id --field=labels | grep -q "pasture:p1-user" || echo "NEEDS UPDATE: $id"
done

# Repeat for each old label...
```

### Step 7: Mark Migration Complete

Once all open tasks have v2 labels alongside their v1 labels:

```bash
bd comments add <request-id> "Migration v1→v2 complete. All open tasks have v2 labels."
```

---

## What NOT to Do

1. **DO NOT** remove old labels from existing tasks — they serve as historical record
2. **DO NOT** restructure existing dependency trees — dependencies using `bd dep add --blocked-by` are correct in both v1 and v2
3. **DO NOT** retroactively create severity groups for past reviews — only apply to new review rounds
4. **DO NOT** delete or modify closed tasks — they are immutable historical records
5. **DO NOT** rename closed tasks — title mapping applies only to open/new tasks
