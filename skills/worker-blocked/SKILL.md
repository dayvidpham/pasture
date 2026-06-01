---
name: worker-blocked
description: Report a blocker to supervisor via Beads
---

# Worker: Handle Blockers

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:worker:blocked` — Report a blocker to supervisor via Beads

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9

**[wblk-update-status]**
- Given: a blocker
- When: reporting
- Then: update Beads task status and document details
- Should not: guess or work around the blocker

**[wblk-wait-for-response]**
- Given: blocker sent
- When: waiting
- Then: wait for supervisor response
- Should not: continue with incomplete info

## When to Use

Cannot proceed due to missing dependency, unclear requirement, or need changes in another file.

## Steps

1. Identify what's blocking (missing type, unclear requirement, file dependency)

2. Update Beads task:
   ```bash
   bd update <task-id> --status=blocked
   bd update <task-id> --notes="Blocked: <reason>. Missing: <dependency or clarification needed>"
   ```

3. Document the blocker in the task:
   ```bash
   bd comments add <task-id> "BLOCKED: <reason>. Need: <dependency or clarification>"
   ```

4. Wait for supervisor or dependency resolution — check with `bd show <task-id>`

## Common Blockers

- Missing type definition from another file
- Unclear requirement in acceptance_criteria
- Need interface defined in dependent file
- Conflicting constraints in validation_checklist
<!-- END GENERATED FROM pasture schema -->
