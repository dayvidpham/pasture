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

**Layer 2: Tests (import production code)**
- Import actual CLI/API package: `import "myproject/cmd/feature"`
- NOT test-only handler: ~~`import "myproject/internal/testhelpers/feature"`~~
- Tests will FAIL - expected (no impl yet)

**Layer 3: Implementation + Wiring**
- Service method for your slice
- CLI/API wiring with real dependencies: `NewService(ServiceDeps{ FS: fs, Logger: logger })`
- NOT TODO placeholders: ~~`// TODO: Wire service`~~

Follow:
- validation_checklist items
- acceptance_criteria (BDD Given/When/Then)
- tradeoffs from ratified plan

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
- Check for an "Adopted Leaf Tasks" section in `bd show <task-id>` — these are IMPORTANT/MINOR findings you must resolve
- Your implementation must address each adopted leaf task's acceptance criteria
- On completion, report which leaf tasks were resolved

## Next

- Complete: `/pasture:worker-complete`
- Blocked: `/pasture:worker-blocked`
<!-- END GENERATED FROM pasture schema -->
