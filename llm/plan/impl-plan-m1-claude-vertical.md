---
title: IMPL_PLAN — M1 Claude vertical
status: DRAFT — not yet executed
proposal: llm/plan/proposal-11-harness-lifecycle-compiler.md
urd: llm/plan/urd-harness-lifecycle.md
authority: llm/research/hooks-ir-compilers-architecture-lessons.md
obsoletes: aura-plugins-sgxp6
review_effort_budget: 3 rounds per slice, then surface to user
worker_isolation: one worktree per slice; orchestrator merges
---

# IMPL_PLAN — M1: Claude vertical

Decomposition of PROPOSAL-11 M1 into vertical slices. M1 closes the pipeline for
one harness and one event, with **every stage existing as an addressable pass**.

Repository: `worktree/proposal-57-integration/pasture`, branch
`feat/proposal-57-integration`, at `baf4166`.

---

## 1. Target package layout

```text
internal/lifecycle/
    ir/                  L1 and L2 IR types. No harness knowledge. No storage.
    frontend/claude/     native Claude payload -> L1
    lowering/            L1 -> L2. Pure. Operation selection lives here.
    legalize/            L2 -> L3. Reads committed state; verifies authority.
    backend/             L3 -> L4. Effect selection and emission.

    receipt/             (exists) blob-then-row recording at the frontend boundary
    projection/          (exists) replay-derived read model
    activation/          (exists) enabled/withheld registration
    registration/        (exists) generated event ordinals
    ingress/             (exists) capture; frontend is extracted OUT of here
    guard/               (exists) static invariant guards — EXTEND, never fork
```

---

## 2. Slices

Six slices. Each names the files it owns exclusively.

### SLICE-1 — IR contract (L1 and L2)

**Owns:** `internal/lifecycle/ir/**`

The types every other slice depends on. Defines the harness dialect (L1) and the
lifecycle dialect (L2), the level span of each, and the boundary that keeps
harness knowledge out of L2.

**Exports:** IP-1 (L1 types), IP-2 (L2 types).

**Must:** contain no harness-specific identifiers; contain no storage types;
compile with no dependency on `receipt`, `projection`, or `database/sql`.

Leaf tasks:
- `IR-1` L1 harness-dialect types and their level-span doc comments
- `IR-2` L2 lifecycle-dialect types: evidence, gate-consultation, human-response
- `IR-3` boundary guard in `lifecycle/guard` that fails the build if `ir` imports
  a harness package or a storage package

### SLICE-2 — Claude frontend

**Owns:** `internal/lifecycle/frontend/claude/**`; removes the frontend
responsibility from `internal/lifecycle/ingress/claude/capture.go`

Native Claude payload plus host version → L1 IR. Bound to the pinned contract
range. Must not name a protocol operation.

**Consumes:** IP-1.

Leaf tasks:
- `FE-1` payload → L1 for `SessionStart`, driven by the generated descriptor
- `FE-2` unlowerable and malformed payloads produce a typed disposition, never a
  panic and never a guess
- `FE-3` extract from `ingress/claude/capture.go` without changing recording
  behaviour; capture keeps the pre-parse digest

### SLICE-3 — Lowering pass (the middle-end)

**Owns:** `internal/lifecycle/lowering/**`

L1 → L2. **Operation selection lives here and nowhere else.** Port the logic from
`43dbbf1^:internal/lifecycle/lower.go` (preserved at
`/tmp/opencode/lowering-restore/`, 434 lines plus 601 lines of tests) onto the
IR types. Drop the dedup surface entirely.

**Consumes:** IP-1. **Exports:** IP-3 (the pass signature).

**Hard constraint:** the pass is a pure function. Its tests must open no
database. If it needs storage, the decomposition is wrong — stop and report.

Leaf tasks:
- `LO-1` the pass: L1 → L2 with explicit level-span documentation
- `LO-2` unit tests that construct L1 values directly and open no database
- `LO-3` guard in `lifecycle/guard` asserting `lowering` imports no storage
  package, with the mutation that must turn it red

### SLICE-4 — Legalization and backend

**Owns:** `internal/lifecycle/legalize/**`, `internal/lifecycle/backend/**`

L2 → L3 → L4. Thin but real: sufficient for the one enabled event, structured so
the write gate can be inserted at legalization without moving the seam.

**Consumes:** IP-2.

**Note:** the normative write-gate mechanism is an open user decision. M1
legalization records evidence of consultation and answers *proceed*; it exercises
no authority. Do not invent a gate.

Leaf tasks:
- `LG-1` legalization: L2 → L3, reads committed state, no writes
- `LG-2` backend: L3 → L4 effect selection
- `LG-3` tests proving no authority is exercised at M1

### SLICE-5 — Authentic-capture evidence binding

**Owns:** `internal/lifecycle/activation/types.go`,
`internal/lifecycle/activation/claude_2_1_210.go`

`FixtureEvidenceAuthentic` is currently a bare enum constant. The enabling gate
checks that the caller passed it, while its own error text demands "a verified
digest" that nothing records or verifies. Bind it to a real digest and ref, so
the gate verifies evidence instead of an assertion.

**Independent of IP-1/IP-2** — may run in parallel from the start.

Leaf tasks:
- `EV-1` carry digest and fixture ref on the evidence value
- `EV-2` the enabling gate verifies the digest against bytes on disk
- `EV-3` a mutation that alters fixture bytes must turn the gate red

### SLICE-6 — End-to-end wiring and production proof

**Owns:** `cmd/pasture/hook_lifecycle.go`, `internal/handlers/hook_lifecycle.go`,
and the end-to-end acceptance test

Wires frontend → lowering → legalization → backend behind the existing public
CLI command. The command surface is already the ratified Option-2 shape and must
stay that way.

**Consumes:** IP-1, IP-2, IP-3. **Merges last.**

Leaf tasks:
- `EE-1` wire the stages; no stage is bypassed
- `EE-2` end-to-end proof through the built binary, file-backed SQLite, observed
  only through public bounded readers
- `EE-3` a malformed invocation creates no database file

---

## 3. Layer Integration Points

| ID | Contract | Owner | Consumers | Merge timing |
|---|---|---|---|---|
| IP-1 | L1 harness-dialect IR types | SLICE-1 | SLICE-2, SLICE-3 | **before SLICE-2/3 implementation begins** |
| IP-2 | L2 lifecycle-dialect IR types | SLICE-1 | SLICE-3, SLICE-4 | same |
| IP-3 | lowering pass signature | SLICE-3 | SLICE-6 | before SLICE-6 wiring |

**SLICE-1 merges first and alone.** Every other slice consumes its types. This is
the "merge sooner, not later" rule: with isolated worktrees, divergence on a
shared contract is invisible until merge, and M1's coupling is entirely on the IR
contract.

```text
  SLICE-1  IR contract
     |  (merge to integration before others start implementing)
     +----------+----------+----------+
     |          |          |          |
  SLICE-2   SLICE-3    SLICE-4    SLICE-5   (parallel)
  frontend  lowering   legalize   evidence
     |          |          |
     +----------+----------+
                |
            SLICE-6  wiring + production proof   (merges last)
```

---

## 4. Execution rules

**Worktrees.** One per slice, branched off the integration branch:

```bash
git worktree add -b <worktree-name> <repo-host>/worktree/<worktree-name> feat/proposal-57-integration
```

Workers never share a worktree. **Merge conflicts are the orchestrator's job**,
not the worker's; ambiguous design choices are surfaced to the user rather than
settled inside a conflict resolution.

**Generated files are never hand-merged.** On conflict in `*.gen.go`, merge the
typed source the generator reads, keep the target branch's generator config,
re-run `make generate`, and verify a zero-diff regen.

**Review budget: three rounds per slice.** Review → fix → re-review, aiming for a
fix-free round with zero BLOCKER, IMPORTANT and MINOR. On exhaustion without a
clean round, the orchestrator surfaces the outstanding findings to the user.

**Gates before every commit:** `make fmt`, `make lint`, `make build`,
`go test -race ./...`, and a zero-diff `make generate`. `go` is not on PATH by
default in this environment.

**Do not fork shared machinery.** `internal/acceptance` is the corpus framework
and `internal/lifecycle/guard` is the static-guard framework. Extend them. This
epic has rejected a duplicate framework five times.

---

## 5. Definition of done for M1

- [ ] A native Claude `SessionStart` traverses frontend → lowering →
      legalization → backend → effects through the built binary
- [ ] The lowering pass is unit-tested with **no database**
- [ ] `SessionStart` is enabled on a **verified digest**, not a constant
- [ ] The other 29 events remain visibly withheld with typed reasons
- [ ] No `ReplayKey`, `RecordReplayed`, or payload-digest-as-identity exists
- [ ] A malformed invocation creates no database file
- [ ] All gates pass; `make generate` is a zero diff

**Explicitly not in M1:** differential equivalence (needs a second harness, M2),
the normative write gate (open user decision), versioned interpretation identity,
lineage, and context disclosure.
