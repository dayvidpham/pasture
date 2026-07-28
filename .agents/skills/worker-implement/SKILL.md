---
name: worker-implement
description: Implement assigned vertical slice following TDD layers
---

# Worker: Implement Vertical Slice

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:worker:implement` — Implement assigned vertical slice following TDD layers

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9

**[wimpl-plan-backwards]**
- Given: vertical slice task
- When: implementing
- Then: plan backwards from production code path
- Should not: start with types without knowing the end

**[wimpl-vertical-ownership]**
- Given: production code path
- When: implementing
- Then: own full vertical (types → tests → impl → wiring)
- Should not: implement only horizontal layer

**[wimpl-import-production]**
- Given: tests
- When: writing
- Then: import actual production code
- Should not: create test-only export or dual code paths

**[wimpl-verify-production]**
- Given: implementation complete
- When: verifying
- Then: confirm production code path is wired (via code inspection or safe testing)
- Should not: rely only on unit tests passing

**[wimpl-inject-deps]**
- Given: dependencies
- When: designing
- Then: inject all deps for testability
- Should not: hard-code `new`

**[wimpl-validate-input]**
- Given: external input
- When: processing
- Then: validate with schema/validation tooling
- Should not: trust raw input

**[frag--validation-cases]**
- Given: any REQUEST (every request, not only fix-intent ones)
- When: eliciting (URE), acceptance-testing (UAT), or implementing
- Then: elicit concrete validation cases — a definition of done plus correct and incorrect behaviours (inputs/behaviors that must pass or must fail), confirm the case set with the user in UAT, evaluate the implementation against them, and store failing real-data cases as test fixtures
- Should not: ship without validation cases; treat validation cases as applying to fix-intent requests only; introduce a request-type axis or enum to gate them

## When to Use

You have a Beads task ID for a vertical slice and are ready to implement end-to-end.

## Steps



### Step 0: Plan backwards from production code path (before implementing)

**Given** Beads task **when** starting **then** identify production code path first

```bash
bd show <task-id>
# Look for: "productionCodePath": "cli-command subcommand" or "api-endpoint"
```

**Trace backwards through call stack:**
```
End: User runs production command
  ↓ Entry: CLI command.action(...) or API endpoint handler
  ↓ Service: createXService({ deps }).method(...)
  ↓ Types: InputType → OutputType
```

**Identify what you own in each layer:**
- **L1 Types:** Which types does YOUR slice need? (not other slices)
- **L2 Tests:** Import actual production code (CLI/API), not test-only export
- **L3 Implementation:** Service method + wiring with real dependencies (not TODO)

### Step 1: Read Beads task for full context

```bash
bd show <task-id>
```

### Step 2: Update status

```bash
bd update <task-id> --status=in_progress
```

### Step 3: Implement your vertical slice in layers

**Layer 1: Types (your slice only)**
- Create only types YOUR slice needs
- Don't add types for other slices

**Layer 2: Tests FIRST (import production code)**
- Write the tests **before** the implementation. The tests ARE the executable
  verification of the validation-case contract agreed with the user during URE
  and Plan UAT (the universal validation cases — see [frag--validation-cases]
  and `C-validation-cases`): definition of done plus correct/incorrect behaviours.
- Import actual CLI/API package: `import "myproject/cmd/feature"`
- NOT test-only handler: ~~`import "myproject/internal/testhelpers/feature"`~~
- Tests will FAIL initially — **red-first** is expected (no impl yet). As you
  implement Layer 3, progressively fewer tests fail until all are green.

**Layer 3: Implementation + Wiring**
- Service method for your slice
- CLI/API wiring with real dependencies: `NewService(ServiceDeps{ FS: fs, Logger: logger })`
- NOT TODO placeholders: ~~`// TODO: Wire service`~~

Follow:
- validation_checklist items
- acceptance_criteria (BDD Given/When/Then)
- tradeoffs from ratified plan

### Step 3b: Evaluate against validation cases (R6, every request)

For **every** request (not only fix-intent ones), the URE/UAT captured concrete validation cases — the definition of done plus the correct/incorrect behaviours that must pass or must fail. Per [frag--validation-cases] and `C-validation-cases`:
- These validation cases are the contract you wrote your Layer-2 tests against (tests-first); evaluate the implementation against each confirmed case.
- Store the failing real-data cases as **test fixtures** so the behaviour is locked in.
- The slice is not done until its validation cases pass (red → green).

There is **no** request-type axis or enum gating this — what a request needs is recognized from the REQUEST/URD, not classified.

### Step 4: Verify quality gates

- Type checking passes
- Tests pass

### Step 5: Commit safely in a shared worktree

Stage **only** the files belonging to your slice, by name:
```bash
git add cmd/feature/list.go pkg/feature/service.go pkg/feature/types.go
git agent-commit -m "feat(feature): add list subcommand"
```

**Never** use `git add .`, `git add -A`, or `git commit -am ...` —
they sweep peer-worker WIP into your commit.

**Never** use destructive git operations (`git reset --hard`,
`git checkout HEAD -- <path>`, `git stash pop`, `git stash apply`,
`git clean -fd`, `git branch -D`) on the shared worktree. A
PreToolUse hook blocks these for worker agents; if you find peer
work in your way, post `bd comments add` and wait for supervisor
coordination instead. See **Shared-Worktree Git Discipline** in
`/pasture:worker` for the full rationale and the escape hatch.

## Checklist

- [ ] Planned backwards from production code path
- [ ] Read Beads task for validation_checklist
- [ ] Each validation_checklist item satisfied
- [ ] BDD acceptance_criteria met
- [ ] Tests import actual production code (not test-only export)
- [ ] No dual-export anti-pattern (one code path for tests and production)
- [ ] No TODO placeholders in production code
- [ ] Service wired with real dependencies (not mocks in production)
- [ ] Quality gates pass (type checking + tests)
- [ ] Production code path verified (via code inspection: no TODOs, real deps wired, tests import production code)
- [ ] Files staged individually by name (no `git add .` / `git add -A`)
- [ ] No destructive git operations (`reset --hard`, `checkout HEAD -- <path>`, `stash pop/apply`, `clean -fd`, `branch -D`) used on the shared worktree

## Follow-up Slices (FOLLOWUP_SLICE-N)

If your Beads task is a `FOLLOWUP_SLICE-N`, the implementation procedure is identical. Additionally:
- Check for a "DEFER'd-Item Leaf Tasks" section in `bd show <task-id>` — these are user-DEFER'd UAT items you must resolve
- Your implementation must address each DEFER'd-item leaf task's acceptance criteria
- On completion, report which DEFER'd-item leaf tasks were resolved

## Review-Fix Cycle (within the chosen review-effort budget)

Your slice is not finished when the first pass lands. Code review iterates **review → fix → re-review** up to the **review-effort budget chosen at Phase 8** until a fix-free clean round confirms **0 BLOCKER + 0 IMPORTANT + 0 MINOR** within budget. Stay available to fix findings of every severity — IMPORTANT and MINOR must reach 0 too, not just BLOCKER. If the budget is exhausted before a clean round, the outstanding findings are surfaced to the user at a gate (not proceeded-past silently). Do not treat "tests pass once" as wave completion.

## Next

- Complete: `/pasture:worker-complete`
- Blocked: `/pasture:worker-blocked`
<!-- END GENERATED FROM pasture schema -->
