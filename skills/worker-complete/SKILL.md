# Worker: Signal Completion

<!-- BEGIN GENERATED FROM aura schema -->
**Command:** `aura:worker:complete` — Signal slice completion after quality gates pass

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9

**[wcomp-quality-gates]**
- Given: implementation done
- When: signaling
- Then: verify the project's quality gates pass
- Should not: report done with failing checks

**[wcomp-checklist]**
- Given: validation_checklist
- When: completing
- Then: confirm all items satisfied
- Should not: complete with unchecked items

**[wcomp-beads-update]**
- Given: completion
- When: reporting
- Then: update Beads task status
- Should not: omit Beads update

**[wcomp-handoff-doc]**
- Given: completion
- When: handing off to reviewer
- Then: create handoff document at `.git/.aura/handoff/<request-task-id>/worker-<N>-to-reviewer.md`
- Should not: skip handoff for actor transitions

## When to Use

Implementation complete and all checks pass.

## Steps

1. Run the project's quality gates (type checking + tests) - must pass
2. **Verify production code path via code inspection:**
   - [ ] Tests import production code (not test-only export)
   - [ ] No dual-export anti-pattern
   - [ ] No TODO placeholders in production code
   - [ ] Service wired with real dependencies (not mocks in production)
3. Verify all validation_checklist items satisfied:
   ```bash
   bd show <task-id>  # Review checklist items
   ```
4. Update Beads task:
   ```bash
   bd update <task-id> --status=done
   bd update <task-id> --notes="Implementation complete. Production code verified working."
   ```
5. Create handoff document for reviewer transition

## Handoff Template (Worker → Reviewer)



### Storage

Path: `.git/.aura/handoff/<request-task-id>/worker-<N>-to-reviewer.md`

### Template

```markdown
# Handoff: Worker <N> → Reviewer

## Context
- Request: <request-task-id>
- URD: <urd-task-id>
- Slice: SLICE-<N>
- Task ID: <slice-task-id>

## What Was Implemented
- Production Code Path: <what end users run>
- Files Changed: <list of files>

## Key Decisions
- <decision 1>: <rationale>
- <decision 2>: <rationale>

## Quality Gates
- Type checking: PASS
- Tests: PASS
- Production code inspection: PASS (no TODOs, real deps wired)

## Areas of Concern
- <any areas the reviewer should pay special attention to>
```

## Report Completion

```bash
# Close the task and add completion notes
bd close <task-id>
bd comments add <task-id> "Implementation complete. Quality gates pass. Production code verified."
```

## Follow-up Slice Completion (FOLLOWUP_SLICE-N)

When completing a FOLLOWUP_SLICE-N, additionally report which original leaf tasks were resolved:

```bash
bd comments add <task-id> "Implementation complete. Resolved leaf tasks: <leaf-task-id-1>, <leaf-task-id-2>"
```

The handoff to the reviewer (h4) must include which original leaf tasks were resolved so reviewers can verify.
<!-- END GENERATED FROM aura schema -->
