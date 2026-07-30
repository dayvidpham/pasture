---
title: IMPL_PLAN — M1 Claude vertical (MVP)
status: rev5 — after review round 4 (A/B REVISE on one blocker each, C ACCEPT)
proposal: llm/plan/proposal-11-harness-lifecycle-compiler.md
urd: llm/plan/urd-harness-lifecycle.md
authority: llm/research/hooks-ir-compilers-architecture-lessons.md
obsoletes: aura-plugins-sgxp6
baseline: fb00691
---

# IMPL_PLAN — M1: Claude vertical (MVP)

**Ten events. One package for the waist. Get it running, then add.**

## The point of M1

**We have never run this pipeline once.** M1 is reverse-planned from the end goal
to the shortest path to a native Claude event reaching Provenance through the
compiler stages, then adds onto a running thing.

```
M1a  THE SPINE          one event, end to end, through the built binary
     ─────────────────  <-- THE PIPELINE RUNS. Everything after is addition.
M1b  BREADTH            remaining nine events, gate arm, activation, read verb
```

**M1a is the deliverable that matters.** Slices `SLICE-1`…`SLICE-7` below are the
full M1 scope; **M1a is the `SessionStart`-sufficient subset of SLICE-1, 2, 3 and
7.** There is no separate set of slices — the same owner carries a slice from its
M1a subset through its M1b remainder.

---

## 1. Package layout

```text
internal/lifecycle/
    waist/        L1 + L2 types, verified constructors, Lower. ONE package.
    frontend/claude/   native payload -> L1
    legalize/     L2 -> L3        (M1b)
    backend/      L3 -> L4        (M1b)
    receipt/  (exists)  raw record + interpreted record + consultation record
    activation/ (exists) enabled/withheld set
    projection/ (exists) UNTOUCHED at M1a; SLICE-3 owns it at M1b
```

`legalize`/`backend` stay separate from `waist` because they **write** and the
waist does not — not for boundary enforcement.

---

## 2. Slices

### SLICE-1 — The waist

**Owns:** `internal/lifecycle/waist/**`
**M1a subset:** everything below. This slice is complete at M1a.

Port from `git show 43dbbf1^:internal/lifecycle/event.go` (657). Consume
`runtime.EventSemantic` and `runtime.LifecycleEventMapping` — retained per
authority §7:190-193. Declare no second arm enum.

- L1 and L2 types with the verified constructors (`BindEvent`,
  `EventBinding.NewEvent`). Unexported fields, constructor-owned — that opacity
  is what makes contract agreement a real check rather than a tautology
  (`event.go:360-364`).
- `func Lower(L1) (L2, error)` — no `context.Context`, no interface parameters,
  no receiver carrying dependencies. That signature is the purity enforcement.
- Drop the dedup surface: `Digest`, `Origin.digest`, `Origin.PayloadDigest`,
  `Origin.ReplayKey`. `NewEvent` loses its `digest` parameter.
- **Do not port `key.go`.** `CanonicalKey` and `EquivalentTo` have no M1
  consumer and serve only the M2 differential gate — the same rationale that cut
  `SemanticFields()`. Restore all three together at M2 from
  `git show 43dbbf1^:internal/lifecycle/key.go`.
- **The waist declares no mutation field.** Mutation is target detail reached
  through `BackendView`; `Origin` already retains `behaviour`, so `Mutation()` is
  reachable without widening the waist. A waist representation of an axis one
  harness has is the UNCOL widening the authority names (§40-44).
- Typed unresolved fact with a closed reason enum.
- **Unit tests cover one event of each of the three arms** and open no database.
  Arm selection needs no capture, so this is free and it is what establishes
  `Lower` behaves — not an end-to-end oracle (see §3).

### SLICE-2 — Claude frontend and captures

**Owns:** `internal/lifecycle/frontend/claude/**`,
`internal/lifecycle/ingress/claude/capture.go`,
`internal/lifecycle/ingress/claude/testdata/**`,
`internal/lifecycle/receipt/service.go`
**M1a subset:** `SessionStart` only, plus its provenance record.

- **`ingress/claude.Parse` must retain the native field name.** It currently
  emits `model.NativeBinding{Kind, Value}` and discards it (`capture.go:113`),
  but `verifyIdentities` matches on the **(kind, native name) pair**. This is not
  a rename — the bridge from `registration.Event` to
  `runtime.ClaudeLifecycleEvent` needs three mappings that do not exist:
  ordinal→typed enum, `NativeFieldID`→native name, and
  `model.NativeBindingKind` (8 values) → `runtime.NativeIdentityKind` (6 values).
  Write that bridge as **one function with one declared table**, plus one
  table-driven parity test over all thirty events asserting ordinal, native name
  and identity agreement between `registration` and
  `runtime.ClaudeCode2_1_210Lifecycle()`. The two tables currently agree by
  coincidence and nothing enforces it.
- **`SessionStart` keeps its existing authentic 2.1.210 capture** and gains the
  provenance record it has never had — `origin: authentic-capture`,
  `harness: claude-code`, `harnessVersion: 2.1.210`,
  `rawFileDigest: sha256:30d524e5d2cb22d486faad05adbaa1a4b7e0d72cd6301f38fe18ca5e3f167003`,
  plus `captureSource`/`capturedAt` from `aura-plugins-16aam`. **Without this
  record the one enabled event goes withheld the moment SLICE-5's gate lands.**
- The remaining nine are captured on the installed **2.1.220**, which the range
  `>=2.1.210,<2.2.0-0` admits. Use `tools/capture-claude-hook.sh`. Captures are a
  **parallel, non-gating** workstream: events sit Withheld with a typed reason
  and flip to Enabled as fixtures arrive.
- **The provenance corpus is `internal/lifecycle/ingress/claude/testdata/captures.yaml`**,
  beside `fixtures/`. `acceptance.LoadCorpus` sets `root = filepath.Dir(path)`
  and `ValidateFixture` rejects fixtures outside it (`capture.go:67-70`), so this
  placement makes containment hold by construction.
- bindings: `BindingSession` on all ten; `BindingToolCall` on 8/11/12;
  `BindingRequest` on 29/30. `PostToolBatch` binds session and emits a
  **tool-call-unresolved** fact.
- the pinned descriptor selects **interpretation only, never admission** — an
  out-of-range host version is recorded verbatim (R12/V6, already satisfied).

### SLICE-3 — Record stage and read surface

**Owns:** `internal/lifecycle/receipt/{interpreted,consultation}.go`,
`internal/lifecycle/receipt/journal.go`, `internal/lifecycle/projection/**`,
`internal/lifecycle/model/{reader,occurrence}.go`, `cmd/pasture/hook_lifecycle_list.go`,
the new `internal/audit/migrate_v*.go` step
**M1a subset:** the interpreted record only — **no projection, no migration, no
new reader.** M1a reads it back with `journal.Facts().QueryEvidence` on its
evidence kind, the pattern `projection/rebuild.go:41` already uses.

- **interpreted record**: L2 arm, bindings, unresolved facts, pinned contract ID
  and codebook version (R7, minimal form)
- **consultation record** (M1b): for gate-consultation events — the L3 value
  legalized, the `HostResponse` returned, and a reference to the interpreted
  record. It carries what the legalization plane knows and the interpreted record
  does not; it does not restate arm/contract/codebook.
- both disambiguated **solely** by autoincrementing row ID. Blob writes use
  `ON CONFLICT(digest) DO NOTHING` (`journal.go:53`) — correct for
  content-addressed bodies, **wrong** for these.
- **a read verb** — `pasture hook lifecycle list`. Today
  `RebuildLifecycleOccurrences` has **zero production callers** and the CLI has
  no read verb, so an occurrence is journaled and then permanently unobservable;
  the existing end-to-end test passes only by performing a rebuild from Go that
  no user can perform. Roughly forty lines wrapping the rebuild plus
  `tasks.NewLifecycleReader`.
- projection, migration, binding filter and payload-by-digest reader land here at
  M1b, designed once against all ten events. **Extract the paging/cursor helper
  so both readers share it** — the filter-applied-after-`LIMIT` defect
  (`projection/reader.go:55-56`) must not be fixable in one copy and not the
  other.
- all new durable writes go through `receipt.JournalAppender`, which already
  enforces the caller deadline (`journal.go:118-136`).

### SLICE-4 — Legalization and backend (M1b)

**Owns:** `internal/lifecycle/legalize/**`, `internal/lifecycle/backend/**`

Port from `43dbbf1^:lower.go` — blocking dispatch, semantic dispatch, actor
resolution, the single-transaction write.

- the gate-consultation arm produces an L3 value; `backend` maps it to the
  consultation record. That is **four of ten** events (8, 13, 25, 29 —
  `ElicitationResult` is human-response, not gate), and it is what gives these
  two stages a real M1 caller.
- **key the consultation obligation on the arm** (`SemanticGateConsultation`),
  not on blocking mode. Gate-consultation ⟹ blocking is already enforced at
  `runtime/lifecycle.go:381-388`, so nothing escapes. Keying on blocking breaks
  `ElicitationResult`, which is human-response **and** blocking.
- **invert the ported refusal.** `lower.go:242` returns `awaitedReplyError`
  (`:388`) for every blocking event. Blocking events record consultation and
  answer proceed; `awaitedReplyError` must not survive.
- human-response arm returns a typed `NoAuthority`; no backend rule maps it.
- neither package performs an authority-bearing write — the consultation record
  is the only permitted L4 effect.

### SLICE-5 — Activation (M1b)

**Owns:** `internal/lifecycle/activation/**`

- the enabling gate requires `Origin == OriginAuthenticCapture` **and** a passing
  `acceptance.CaptureProvenance.ValidateFixture` **and** an in-range
  `HarnessVersion`. This closes the `capture.go:45-47` early-return bypass.
- **State plainly what the gate does not do.** After the origin check, everything
  is author-supplied: `CaptureSource` need only be non-empty (`:48`),
  `CapturedAt` need only be RFC3339 UTC (`:51-54`), and `RawFileDigest` is
  checked against *the file being validated* (`:71-77`) — so a synthesised
  payload digested by its author passes. **What actually stops synthesis is human
  review of the fixture-adding commit.** The gate is tamper-evidence over a
  caller assertion, not proof of provenance, and must not later be mistaken for
  one.
- `ProductionProof` is bound to **the named SLICE-7 end-to-end case for that
  event**, established by that test's existence and the CI gate that runs it —
  not by a runtime check. `internal/acceptance` is a mutation-testing framework
  and cannot execute a `Case`; there is no other referent.
- replace the hardcoded `if event.NativeName == "SessionStart"`
  (`claude_2_1_210.go:11`) with the declared ten-ordinal set; the rest withheld
  with typed reasons.

### SLICE-7 — Wiring, command, and end-to-end proof

**Owns:** `internal/handlers/hook_lifecycle.go`, `cmd/pasture/hook_lifecycle.go`,
`cmd/pasture/hook_lifecycle_production_test.go`, `docs/privacy.md`
**M1a subset:** wiring plus the `SessionStart` case. **Merges last within each phase.**

SLICE-6 no longer exists; the exit-code work is these three bullets, in the slice
that owns the command.

- wire frontend → `Lower` → record → legalize → backend; no stage bypassed
- **return exit 0 always, report on stderr.** Per the user's ruling: *"For now
  return exit code 0 for all."* Deny semantics are deferred until there is a
  working pipeline.
- **delete the `MarkFlagRequired` loop** (`cmd/pasture/hook_lifecycle.go:56-60`)
  rather than relocating it. All three flags are already validated by the handler
  with better messages (`:46-48` harness, `:49-51` host-version, `:52-62` event).
  Deleting kills the `:58` panic at source, needs no `PreRunE`, and routes the
  missing-flag case through the handler to exit 0 — where today it returns from
  `Execute()` and exits 1 at `main.go:35`, outside this file.
- **extend the existing test; do not write a new one.**
  `cmd/pasture/hook_lifecycle_production_test.go:20` already builds the binary,
  execs `hook lifecycle --harness claude-code --event SessionStart
  --host-version 2.1.220`, and reads back. `:57` already covers "a malformed
  invocation creates no database file". Use `main_test.go`'s
  `binaryPath`/`runCLI` (`:27-67`, `:99-117`), which build once per test binary
  into a temp dir. **Do not `make build` from a package test** — `bin/pasture` is
  one shared path and `go test ./...` runs packages concurrently.
- **fault cases against the built binary: unwritable `--db`, missing flag,
  malformed payload** — each asserting exit 0 and a stderr report naming the
  fault class. Panic and nil-dereference are exercised **in-process** through the
  existing `open` seam (`internal/handlers/hook_lifecycle.go:42`), or via a
  separate fixture main following `internal/codegen/ir/capability_panic_subprocess_test.go:22-35`.
  **No fault-injection flag or env var in `cmd/pasture`** — that would be a
  deliberate crash path in the binary Claude executes on every hook.
- deliver byte-identical input twice; assert two distinct records (**V1**)
- `docs/privacy.md` — every prompt, tool input and file content passing a
  registered hook is persisted by default. **Gates enabling `PreToolUse`.**

---

## 3. What the end-to-end test can and cannot prove

**It cannot isolate `Lower`.** `Lower`'s output is a pure function of
`(event kind, bindings)` taken from `runtime.LifecycleEventMapping`, and both are
available upstream of it — the event comes from `--event`, not the payload. So a
wiring that never calls `Lower` and writes `arm = mapping[kind]` satisfies any
arm assertion. Asking for "an assertion only `Lower` can satisfy" is unsatisfiable
by construction.

**So assert payload-derived values instead.** At M1a: the recorded correlation
value equals the fixture's `session_id`
(`b3cfe877-feb4-4ba3-9500-414c8bfb51c4`), resolved through the contract's
identity table. That cannot come from a table lookup on `--event`. At M1b: the
`tool_use_id` from the captured `PreToolUse`/`PostToolUse` payloads surviving
into the interpreted record.

**`Lower`'s presence is established by code inspection** — SLICE-7's "no stage
bypassed" — **and by its DB-free unit tests** (SLICE-1). That is the honest
claim; an end-to-end oracle cannot make it.

---

## 4. Order

```text
M1a   SLICE-1 waist ──> SLICE-2 frontend ──> SLICE-3 record ──> SLICE-7 wiring
                                                                     |
                                                 ===== PIPELINE RUNS =====
                                                                     |
M1b   SLICE-2 nine events + captures  (parallel, non-gating) ────────┤
      SLICE-3 consultation + projection + read verb  ────────────────┤
      SLICE-4 legalize + backend  ────────────────────────────────────┤
      SLICE-5 activation  ────────────────────────────────────────────┤
                                                                     |
                                                    SLICE-7 breadth proof
```

**Cross-slice contracts in M1b** — declared because the slices are parallel in
separate worktrees, not because a package boundary is implied:

| Type | Exported by | Imported by |
|---|---|---|
| L1, L2, `Lower` | SLICE-1 | SLICE-2, 4 |
| interpreted-record type | SLICE-3 | SLICE-2 (`receipt/service.go` emits it) |
| consultation-record type | SLICE-3 | SLICE-4 |
| `CaptureProvenance` corpus path | SLICE-2 | SLICE-5 |

---

## 5. Constraints verified in-tree

- **`internal/runtime`'s closure contains `os`, `os/exec`, `syscall`.**
  Import-based purity checks on anything consuming it are meaningless.
- **`RebuildLifecycleOccurrences` and `RebuildOccurrences` have zero production
  callers**; the CLI has no read verb. Occurrences are currently write-only.
- **`cmd/pasture/hook_lifecycle_production_test.go` already exists** and covers
  most of M1a's condition, plus V3 (`receipt/service_test.go:32,43,108`) and V6.
- **Interpreted records are not occurrences** — `projection/rebuild.go:14,32,85`
  populates that table from the occurrence evidence kind only.
- **Gate-consultation ⟹ blocking is already enforced** (`runtime/lifecycle.go:381-388`).
- **`SemanticExplicitHumanResponse` requires a request identity**
  (`runtime/lifecycle.go:390-398`) — `PostToolUse` cannot carry that arm, and
  `AskUserQuestion` is a *tool*, not a hook event.
- **R10's deadline is already enforced** (`journal.go:118-136`).
- **cobra runs `PreRunE` (`command.go:999`) before `ValidateRequiredFlags`
  (`:1007`)** — recorded so the `MarkFlagRequired` question is not re-checked.
- **`internal/runtime` and `ingress/internal/hostcontract` are two
  hand-maintained copies of the same Claude target description**, agreeing by
  coincidence; `ingress/claude/catalogue_test.go:59-73` is a third re-derivation
  of the axis table. SLICE-2's parity test is the first thing that checks them.
- **`model/ids.go:22-23` are live** (`definition.go:30,53,54,71`).
- **`acceptance.Observation` cannot hold a process result** (`report.go:20-25`).

---

## 6. Done

**M1a:** a real `SessionStart` traverses frontend → `Lower` → record through the
built binary, and the recorded correlation value equals the fixture's
`session_id`. Nothing else.

**M1b:**
- [ ] Every **enabled** event traverses the pipeline, asserted on payload-derived
      values
- [ ] Two byte-identical deliveries yield two distinct records (V1)
- [ ] `Lower` unit-tested with no database, one event per arm
- [ ] Each enabled event has an authentic capture **and a provenance record**
- [ ] `pasture hook lifecycle list` returns records a user can read
- [ ] Gate-consultation events produce a consultation record (V8)
- [ ] No non-zero exit from the lifecycle path
- [ ] No `ReplayKey`, `RecordReplayed`, `Origin.PayloadDigest`
- [ ] `docs/privacy.md` published before `PreToolUse` is enabled
- [ ] `make fmt`, `make lint`, `make build`, `go test -race ./...`,
      zero-diff `make generate`

**Capture risk:** `Elicitation`/`ElicitationResult` need an MCP server that
elicits; `PostToolBatch` needs the host to emit a batch event, which is not
determinable from this tree. Any uncapturable event stays **visibly withheld**.
**Do not synthesise a fixture.**

**Not in M1:** differential equivalence and `key.go`/`SemanticFields()` (M2), the
write gate, lineage, context disclosure, the raw-ingestion escape hatch (M4), and
any package split of the waist (M2).
