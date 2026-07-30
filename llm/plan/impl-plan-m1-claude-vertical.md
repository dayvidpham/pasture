---
title: IMPL_PLAN — M1 Claude vertical
status: DRAFT rev2 — revised after a six-reviewer wave returned 6/6 REVISE
proposal: llm/plan/proposal-11-harness-lifecycle-compiler.md
urd: llm/plan/urd-harness-lifecycle.md
authority: llm/research/hooks-ir-compilers-architecture-lessons.md
obsoletes: aura-plugins-sgxp6
baseline: 9b40334
review_effort_budget: 3 rounds per slice, then surface to user
worker_isolation: one worktree per slice; orchestrator merges
---

# IMPL_PLAN — M1: Claude vertical

**Ten enabled events. Eight slices.** M1 closes the pipeline for one harness with
every stage as an addressable pass.

**M1 is mostly separation and renaming, not construction.** Three of the four
stages already exist under non-compiler names, and the IR exists in the deleted
source. Rev1 read as a from-scratch build, which is what produced its defects.

Repository: `worktree/proposal-57-integration/pasture`, branch
`feat/proposal-57-integration`, baseline `9b40334`.

---

## 1. Target package layout

```text
internal/lifecycle/
    dialect/             L1 and L2 IR. No provenance import, no journal ID, no timestamp.
    frontend/claude/     native Claude payload -> L1     (extracted from ingress/claude)
    lowering/            L1 -> L2. Pure. No storage.
    legalize/            L2 -> L3. Reads committed state.
    backend/             L3 -> L4 effects.

    receipt/     (exists) raw-body record + NEW interpreted record
    projection/  (exists) EXTENDED with a binding filter
    activation/  (exists) enabled/withheld set
    guard/       (exists) EXTENDED with a tree-walking driver
    model/       (exists) durable journal records; may import dialect, never the reverse
internal/acceptance/     (exists) EXTENDED with a case executor
internal/runtime/        (exists) RETAINED — EventSemantic + LifecycleEventMapping
```

**Not `internal/lifecycle/ir`.** `internal/codegen/ir` exists and 116 files bind
the identifier; the authority warns against conflation (§7:195-197).

---

## 2. Slices

Eight slices. Each names the files it owns **exclusively** — verified
non-overlapping.

### SLICE-0 — Framework drivers (prerequisite)

**Owns:** `internal/lifecycle/guard/driver.go`,
`internal/acceptance/executor.go` (+ tests)

Both frameworks the plan builds on are **shells**. `guard` has no tree-walking
driver and zero importers; `acceptance` has no `os/exec` and nothing runs
`Case.Target.Command`. Until fixed, every "the guard fails the build" claim is
false and every corpus is unexecutable.

**Merges first, with SLICE-1.**

- `DR-1` `guard.CheckTree(root) ([]Finding, error)` walking a **derived** file
  set, plus a test invoking it at the repo root that fails on any finding. Scope
  derived by walk, never by hand-maintained list.
- `DR-2` register the existing `CheckBoundedReaderSource` and `CheckTimeoutSource`
  with the driver; `CheckTimeoutSource` currently runs against four hardcoded paths.
- `DR-3` `acceptance.RunCase(ctx, Case, binaryPath) (Observation, error)` — exec
  the built binary with `Case.Target.Command.Argv`, capture exit/stdout/stderr.
  `EvaluateMutations` already requires exactly this `Observation`.
- `DR-4` a source-mutation runner over a scratch worktree so declared operators
  are actually executed (closes `aura-plugins-0si2b`, on which four M1 guards depend).

### SLICE-1 — The dialect (L1 and L2 IR)

**Owns:** `internal/lifecycle/dialect/**`, `internal/lifecycle/guard/dialect_imports.go`

**Port source:** `git show 43dbbf1^:internal/lifecycle/event.go` (657 lines) and
`:internal/lifecycle/key.go` (86). This is the IR. Do not build from scratch.

**Consumes** `runtime.EventSemantic` and `runtime.LifecycleEventMapping` —
retained per authority §7:190-193.

**Exports:** IP-1 (L1), IP-2 (L2 + `SemanticFields()` + equivalence table).

- `DI-1` port L1 from `event.go`. Event identity is a **generated ordinal** with
  pinned-contract lookup — no per-event Go constant, so `dialect` has no
  `PreToolUse` symbol.
- `DI-2` port L2 (`Semantics`). **Type-alias or re-export `runtime.EventSemantic`;
  declare no second arm enum.** Axes are read from `LifecycleEventMapping`.
- `DI-3` **drop the dedup surface here, not downstream:** `Digest`,
  `Origin.digest`, `Origin.PayloadDigest`, `Origin.ReplayKey`. The pre-parse
  digest stays on the recording side (`claude.Capture.Digest`). SLICE-1 merges
  first; if it ports these faithfully they become load-bearing before removal.
- `DI-4` keep `Semantics.CanonicalKey` (`key.go:50`) — the M2 equivalence
  primitive. Add `SemanticFields() SemanticProjection`, the **only** surface
  differential equivalence compares; harness identity, timestamps and row
  identity are excluded by construction.
- `DI-5` declare the equivalence-class table (Claude event → semantic class) with
  a totality guard: every M1-enabled event is in exactly one class or is
  explicitly harness-unique with a typed reason.
- `DI-6` typed unresolved fact with a **closed reason enum**
  (`ReasonNoToolCallBinding`, …) carried on L1 and L2. V12 has no representation
  without this; an empty binding slice cannot be distinguished from one never computed.
- `DI-7` boundary guard: `dialect` may not import `provenance`, `model`,
  `receipt`, `ingress`, or `database/sql`. **Falsifying mutation:** add
  `import ".../internal/lifecycle/receipt"` to a `dialect` file → guard red.
- `DI-8` delete the ten unreferenced ID aliases in `model/ids.go:13-26` (identity
  surface of the dropped mechanisms) or annotate each with its dropped mechanism.

### SLICE-2 — Claude frontend

**Owns:** `internal/lifecycle/frontend/claude/**`; removes the frontend
responsibility from `internal/lifecycle/ingress/claude/capture.go`

**Scope: ten events** — 1, 3, 8, 11, 12, 13, 25, 26, 29, 30.

`ingress/claude.Parse` (`capture.go:27-49`) is already a frontend. Extract and
rename; do not rewrite.

- `FE-1` payload → L1 for **all ten**, driven by the generated descriptor
- `FE-2` malformed or unclassifiable payloads produce a typed disposition and a
  typed unresolved fact — never a panic, never a guess
- `FE-3` extract from `capture.go` preserving **observable** recording behaviour;
  the pre-parse digest stays. `receipt.Delivery` becomes a projection of L1 via
  exactly one function, or is replaced by L1 — state which; do not leave two
  spellings of one parse.
- `FE-4` bindings: `BindingSession` on **all ten** (required by the contract);
  `BindingToolCall` on 8/11/12; `BindingRequest` on 29/30. `PostToolBatch` binds
  session and emits a **tool-call-unresolved** fact — not a blanket unresolved.
- `FE-5` the pinned descriptor selects **interpretation only, never admission**.
  An out-of-range host version is recorded verbatim, never rejected (R12/V6).
- `FE-6` golden cases per authority §11: captured payload → expected L1 → expected
  L2, for every enabled event plus malformed and unresolved cases.

### SLICE-3 — Lowering pass (the middle-end)

**Owns:** `internal/lifecycle/lowering/**`,
`internal/lifecycle/guard/lowering_imports.go`

L1 → L2. **Port source is `event.go`'s `EventBinding.NewEvent` (:475) and
`verifyIdentities` (:533) — NOT `lower.go`.** `lower.go` consumes
`event.Semantics()`, branches on blocking, and writes; it is legalization plus
backend and belongs to SLICE-4.

**Hard constraint:** `func Lower(dialect.L1) (dialect.L2, error)` — no
`context.Context`, no interface parameters, no receiver carrying dependencies. A
function with no injected dependency cannot perform I/O.

- `LO-1` the pass; arms and axes read from `LifecycleEventMapping`; declares no
  per-event table of its own
- `LO-2` unit tests constructing L1 directly, opening no database
- `LO-3` guard: `lowering`'s **transitive** import closure equals a declared
  allowlist (`dialect` + stdlib minus `database/sql`). An equality check catches
  the next storage package; a denylist would not. **Falsifying mutation:** add
  `import ".../internal/lifecycle/receipt"` — chosen because it is the *indirect*
  case a direct-import check misses.
- `LO-4` two distinct Claude events sharing an arm produce equal `EquivalentTo`
  shape and distinct `CanonicalKey`. Use `PreToolUse`/`PreCompact` so the test
  does not depend on the at-risk elicitation capture.

### SLICE-4 — Legalization and backend

**Owns:** `internal/lifecycle/legalize/**`, `internal/lifecycle/backend/**`

**Port source:** `43dbbf1^:internal/lifecycle/lower.go` — its blocking dispatch,
semantic dispatch, actor resolution and single-transaction write.

**Scope: ten events, five blocking, three arms.**

- `LG-1` legalization L2 → L3, reads committed state, no writes
- `LG-2` backend L3 → L4 effect selection
- `LG-3` **static** no-authority proof: `legalize` and `backend` contain no write
  call at M1. Static, so it holds whether or not the elicitation pair is enabled.
- `LG-4` **INVERT the ported refusal.** `lower.go:241` returns
  `awaitedReplyError` for every blocking event; M1 enables five. Blocking events
  **record consultation and answer proceed; they never refuse.** `awaitedReplyError`
  must not survive the port — name it as the symbol to remove.
- `LG-5` the consultation obligation keys on `Blocking != NonBlocking`, **not on
  the arm** — `PostToolBatch` is blocking, and an arm-keyed rule would let it escape.
- `LG-6` `MutationInput` modelled in the dialect, emitted by no backend rule
- `LG-7` human-response arm returns a typed `NoAuthority`; a guard asserts no
  backend rule maps it
- `LG-8` the L3 type has no field capable of holding authority. This is where the
  dropped candidate/committed split is re-derived, since M1 builds this stage.

### SLICE-5 — Interpreted record and readers

**Owns:** `internal/lifecycle/receipt/interpreted.go`,
`internal/lifecycle/model/reader.go`, `internal/lifecycle/projection/reader.go`

Without this, lowering's output is computed and discarded — a no-caller pass,
the condition under which the previous middle-end was deleted.

- `RC-1` durable interpreted record: L2 arm, bindings, unresolved facts, plus the
  pinned contract ID and codebook version (R7, minimal form)
- `RC-2` extend `OccurrenceQuery` with a typed binding filter; thread it through
  `projection.Reader` **including `queryFingerprint`**, or cursors silently mismatch
- `RC-3` negative test: a cursor from a binding-filtered query is rejected by an
  unfiltered one
- `RC-4` public bounded payload-by-digest reader — V2 has no public path today
  (`SQLiteBlobStore` exposes `Put`/`Exists`/`Reclaimable` only)

### SLICE-6 — Activation and evidence binding

**Owns:** `internal/lifecycle/activation/**`

- `EV-1` evidence carries the whole `acceptance.CaptureProvenance`, not a bare digest
- `EV-2` the enabling gate requires `Origin == OriginAuthenticCapture` **and** a
  passing `ValidateFixture` **and** an in-range `HarnessVersion`. A digest proves
  non-tampering, not provenance; `Origin` is caller-asserted and `ValidateFixture`
  no-ops for non-authentic origins.
- `EV-3` **falsifying mutation: relabel an authentic fixture's origin to
  `authored`** → gate red. This is the mutation that distinguishes authenticity
  from tamper-detection.
- `EV-4` bind `ProductionProof` to a referenced passing case ID and its recorded
  outcome. It is the identical bare-constant defect two lines below
  `FixtureEvidence`, checked by the same gate.
- `EV-5` replace the hardcoded `if event.NativeName == "SessionStart"`
  (`claude_2_1_210.go:12`) with the declared ten-ordinal enabled set, each gated
  on verified evidence; assert twenty withheld with typed reasons.

### SLICE-7 — Exit-code safety

**Owns:** `cmd/pasture/hook_lifecycle.go`, `internal/lifecycle/guard/exit_codes.go`

**Sole owner of the lifecycle command.** It is a 62-line flag-parsing shell;
stage wiring belongs in the handler (SLICE-8).

Five of ten events are blocking; the host reads exit 2 as deny. The command
**exits non-zero today** (`:46`) and **panics** (`:58`); an unrecovered panic
exits 2.

**Merges before any blocking event is enabled** — gates SLICE-6 and SLICE-8.

- `XC-1` syntactic ban over lifecycle packages: no non-zero `os.Exit`, no
  `log.Fatal*`, no `panic(`
- `XC-2` **falsifying mutation: change one `return nil` on the storage-failure
  path to `return err`** → guard red
- `XC-3` fault classes enumerated over the **closed** `errors.Category` enum plus
  panic; totality guard fails when a category is added without a row
- `XC-4` top-level `recover()` converting any panic to exit 0 + stderr; replace
  the `panic` at `:58` with a returned error; integration test injecting a panic
  and a nil dereference asserts exit 0
- `XC-5` typed `HostResponse` with a closed decision enum, exactly one legal
  value (`Proceed`) at M1, and a guard asserting the enum has one arm. Deny later
  becomes *adding an arm*, not *removing a guard* (R9's retained responder).

### SLICE-8 — Wiring and production proof

**Owns:** `internal/handlers/hook_lifecycle.go`, the end-to-end acceptance test,
and the privacy documentation

**Consumes** IP-1..IP-5. **Merges last.**

- `EE-1` wire frontend → lowering → record → legalization → backend in the
  handler; no stage bypassed
- `EE-2` end-to-end proof: `make build`, exec `bin/pasture` with argv, assert
  exit code and stderr. **The test must not import `internal/handlers`** — add
  that to the guard's denylist for this file, or an in-process call silently
  satisfies "through the built binary".
- `EE-3` a malformed invocation creates no database file
- `EE-4` privacy posture documented in user-facing docs. **Gates enabling
  `PreToolUse`**, which carries `FieldToolInput` — full file contents for
  `Write`/`Edit`.
- `EE-5` `TestEngineStartReviewUsesAttachedProvenanceAdapter` stays green (R14/V11)

---

## 3. Layer Integration Points

| ID | Contract | Owner | Consumers | Merge timing |
|---|---|---|---|---|
| IP-0 | guard driver + corpus executor | SLICE-0 | all | **first** |
| IP-1 | L1 types | SLICE-1 | SLICE-2, 3 | before their implementation |
| IP-2 | L2 + `SemanticFields()` + class table | SLICE-1 | SLICE-3, 4, 5 | same |
| IP-3 | `Lower` signature | SLICE-3 | SLICE-8 | before wiring |
| IP-4 | exit-code guard + `HostResponse` | SLICE-7 | SLICE-6, 8 | **before any blocking event is enabled** |
| IP-5 | interpreted record + binding-filtered reader | SLICE-5 | SLICE-8 | before wiring |

```text
  SLICE-0  drivers ────┐
  SLICE-1  dialect ────┤  (merge together, first)
                       |
     +-----------+-----+-----+-----------+
     |           |           |           |
  SLICE-2    SLICE-3     SLICE-4     SLICE-7        (parallel)
  frontend   lowering    legalize    exit-code
     |           |           |           |
     +-----------+-----------+           |
                 |                       |
             SLICE-5  record/readers     |
                 |                       |
                 +-----------+-----------+
                             |
                        SLICE-6  activation   (gated by IP-4)
                             |
                        SLICE-8  wiring + proof   (merges last)
```

---

## 4. Execution rules

**One worktree per slice**, branched off the integration branch:
`git worktree add -b <name> <repo-host>/worktree/<name> feat/proposal-57-integration`

**Merge conflicts are the orchestrator's job.** Ambiguous design choices are
surfaced to the user, not settled inside a conflict resolution.

**Generated files are never hand-merged.** Merge the typed source, re-run
`make generate`, verify a zero-diff regen.

**Review budget: 3 rounds per slice**, aiming for a fix-free round at 0 BLOCKER /
0 IMPORTANT / 0 MINOR. On exhaustion without a clean round, surface to the user.

**Guard scope is derived**, by walk or by exhaustive enumeration over a closed
typed set. A hand-maintained path or symbol list is not acceptable scope for a
new guard — that defect already exists three times in this tree.

**Every guard names its falsifying mutation, and that mutation is executed.**
Named above: DI-7, LO-3, EV-3, XC-2.

**Do not fork shared machinery.** After SLICE-0 both frameworks can actually run;
extend them.

Gates before every commit: `make fmt`, `make lint`, `make build`,
`go test -race ./...`, zero-diff `make generate`. `go` is not on PATH by default.
Stage only owned paths, never `git add -A`, commit with `git agent-commit`.

---

## 5. Definition of done for M1

- [ ] All ten events traverse the pipeline through the built binary, with §2.1's
      per-arm terminals
- [ ] The lowering pass is unit-tested with **no database**; its transitive
      import closure equals the declared allowlist
- [ ] Each enabled event: authentic origin + passing validation + in-range version
- [ ] `ProductionProof` bound to a referenced passing case
- [ ] Twenty events withheld with typed reasons
- [ ] All three L2 arms exercised; human-response returns typed `NoAuthority`
- [ ] `toolUseID` and `requestID` correlated pairs retrievable through a public reader
- [ ] `PostToolBatch` records session correlation plus a **tool-call-unresolved** fact
- [ ] Every durable record carries contract ID and codebook version
- [ ] No non-zero exit, no `log.Fatal*`, no `panic(`; injected panic exits 0
- [ ] No second arm enum; no `ReplayKey`/`RecordReplayed`/`Origin.PayloadDigest`
- [ ] `CanonicalKey` and `SemanticFields()` exist; equivalence class table is total
- [ ] Privacy posture documented before `PreToolUse` is enabled
- [ ] All four named falsifying mutations executed and red
- [ ] All gates pass; `make generate` is a zero diff

**Capture risk:** `Elicitation`/`ElicitationResult` need an MCP server that
elicits. If uncapturable they stay **visibly withheld** and the human-response
arm goes untested — acceptable, and **must not** be closed with a synthesised
fixture. `LG-3`'s static no-authority proof holds regardless.

**Not in M1:** differential equivalence execution (M2; its surface is built here),
the normative write gate, definition resolution, lineage, context disclosure, and
the raw-ingestion escape hatch (M4).
