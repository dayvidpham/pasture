---
title: IMPL_PLAN — M1 Claude vertical (MVP)
status: rev6 — after review round 5 (0 blockers across all axes; 4 IMPORTANT closed)
proposal: llm/plan/proposal-11-harness-lifecycle-compiler.md
urd: llm/plan/urd-harness-lifecycle.md
authority: llm/research/hooks-ir-compilers-architecture-lessons.md
obsoletes: aura-plugins-sgxp6
baseline: 511e2bb
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
- **Do not port `key.go`** (`CanonicalKey` at `:50`, `ReplayKey` at `:77`), and
  **additionally drop `Semantics.EquivalentTo` (`43dbbf1^:event.go:283-320`)**,
  which is in the file being ported, not in `key.go`. All three have no M1
  consumer and serve only the M2 differential gate — the same rationale that cut
  `SemanticFields()`. Restore `key.go` and re-add `EquivalentTo` at M2.
- **The waist declares no mutation field**, and **drop `Origin.behaviour` from
  the port** along with the two `[BackendView]` doc references. A waist
  representation of an axis one harness has is the UNCOL widening the authority
  names (§40-44). `BackendView` does **not** exist — it lived in
  `internal/lifecycle/backend.go`, deleted at `e236e5e`, and is not in the port
  source, so `behaviour` would be written at `43dbbf1^:event.go:512` and read
  nowhere. SLICE-4 restores both from `git show 0f7380e:internal/lifecycle/backend.go`
  when it first needs target detail.
- Typed unresolved fact with a closed reason enum.
- **Unit tests cover one event of each of the three arms**, plus three negative
  cases for `verifyIdentities` — an undeclared `(kind, native name)` pair, an
  absent required identity, and a duplicate pair (`43dbbf1^:event.go:460-470`).
  Those are the plan's stated reason the constructor is "a real check rather than
  a tautology"; without them it is only checked positively. All open no database.
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
  a rename — but **only one mapping is genuinely missing: ordinal→typed enum.**
  Declare no other table:
  - `NativeFieldID`→native name is **already generated** as `fieldNames`
    (`ingress/claude/payload_2_1_210.gen.go:9`). `capture.go:101` already binds
    the name to a local and discards it at `:113` — retain it. One struct field,
    not a table.
  - the `model.NativeBindingKind` (8) → `runtime.NativeIdentityKind` (6) mapping
    is **avoidable**: read the kind off the pinned contract through
    `runtime.NativeIdentityField.Kind()`/`.NativeName()`
    (`runtime/lifecycle.go:283-295`). Hand-writing it would be lossy —
    `BindingTask`/`BindingWorktree` (`model/occurrence.go:33-34`) have no
    `runtime` counterpart — and §5 already records three hand-maintained copies
    of this description. Do not make it five.

  Write that bridge as **one function with one declared table**, plus one
  table-driven parity test over all thirty events asserting ordinal, native name
  and identity agreement between `registration` and
  `runtime.ClaudeCode2_1_210Lifecycle()`. The two tables currently agree by
  coincidence and nothing enforces it.
- **`SessionStart` keeps its existing authentic 2.1.210 capture** and gains the
  provenance record it has never had — `origin: authentic-capture`,
  `harness: claude-code`, `harnessVersion: 2.1.210`,
  `rawFileDigest: sha256:30d524e5d2cb22d486faad05adbaa1a4b7e0d72cd6301f38fe18ca5e3f167003`,
  plus `captureSource` and `capturedAt`, which **are author-supplied** —
  `aura-plugins-16aam` records the digest and the downgrade narrative but neither
  field, and `acceptance/capture.go:48,51-54` requires both. Cite commit
  `fb00691` as `captureSource` and its committer date as `capturedAt`, and say so
  rather than implying they were observed. **Without this record the one enabled
  event goes withheld the moment SLICE-5's gate lands.**
- The remaining nine are captured on the installed **2.1.220**, which the range
  `>=2.1.210,<2.2.0-0` admits. Use `tools/capture-claude-hook.sh`. Captures are a
  **parallel, non-gating** workstream: events sit Withheld with a typed reason
  and flip to Enabled as fixtures arrive.
- **The provenance corpus is `internal/lifecycle/ingress/claude/testdata/captures.yaml`**,
  beside `fixtures/`, carrying **both must-pass and must-fail cases**. Non-vacuity
  is the point: a corpus of only-passing captures proves nothing.

  **Do not load it with `acceptance.LoadCorpus`.** That schema is for
  production-path corpora — `loader.go:273` requires eight fields per case and
  `:503` requires all eight `ExactDelta` sections with a `sha256` digest each,
  which for ten captures is ~80 author-supplied digests asserting state deltas
  nothing evaluates. That is the bare-constant defect this gate exists to close,
  in bulk, inside the gate's own input.

  Use the shape proven at `peasant-labs/schema/develop` (`testcase/testcase.go:53-60`):
  `name, input, expected, classification (must-pass|must-fail), provenance{source, ref},
  mutation{description}` — where `mutation.description` is **prose** stating what
  change would make the case matter, not an executable operator.

  The must-fail cases are the valuable ones, and each maps to a real bypass:

  | must-fail case | Closes |
  |---|---|
  | a fixture relabelled `origin: authored` | `capture.go:45-47`'s early return |
  | a fixture whose `rawFileDigest` does not match its bytes | tampering |
  | a `harnessVersion` outside `>=2.1.210,<2.2.0-0` | version drift |
  | a fixture path escaping the corpus root | `capture.go:67-70` |

  **Declare the case instantiation**, because the reference `Case` is generic
  (`peasant-labs/schema/develop/testcase/testcase.go:132-139`) and two slices
  depend on it: `input` is `{fixture: <path relative to the corpus root>}`, and
  the `CaptureProvenance` is read from the sibling `<Event>.provenance.json` the
  capture script already emits — **not inlined in YAML**, because
  `acceptance.CaptureProvenance` carries no yaml tags and `CaptureOrigin`/
  `HarnessKind` implement `encoding.TextUnmarshaler`, which `yaml.v3` does not
  honour, so an out-of-set origin would decode silently instead of being
  rejected. `encoding/json` honours both. `expected` is the activation decision:
  `enabled` or `withheld{reason}`. `provenance.source` is a **closed** enum, per
  the reference (`testcase.go:76-108`), not a free string.

  **The corpus type and loader live in `internal/lifecycle/activation/corpus.go`,
  owned by SLICE-5**; SLICE-2 owns only `testdata/**`. The negative-control
  corpus lives beside it as `testdata/captures_vacuous.yaml`, with the test
  asserting rejection owned by SLICE-5. SLICE-5 passes the root explicitly rather
  than borrowing a loader's, so containment holds because we chose it. Follow `peasant-labs`' negative-control
  precedent (`testcase/testdata/vacuous_corpus.yaml`): one deliberately invalid
  corpus that the validator must reject, so the validator itself is falsified.
  `tools/capture-claude-hook.sh:105-119` already emits the six-field provenance
  record the gate reads.
- bindings: `BindingSession` on all ten; `BindingToolCall` on 8/11/12;
  `BindingRequest` on 29/30. `PostToolBatch` binds session and emits a
  **tool-call-unresolved** fact.
- the pinned descriptor selects **interpretation only, never admission** — an
  out-of-range host version is recorded verbatim (R12/V6, already satisfied).

### SLICE-3 — Record stage and read surface

**Owns:** `internal/lifecycle/receipt/{interpreted,consultation}.go`,
`internal/lifecycle/receipt/journal.go`, `internal/lifecycle/projection/**`,
`internal/lifecycle/model/{reader,occurrence}.go`, `internal/lifecycle/receipt/reader.go`,
`cmd/pasture/hook_lifecycle_list.go`, the new `internal/audit/migrate_v*.go` step

`receipt/reader.go` is a five-line file whose only content is a comment saying it
contains no reader — delete it; the comment belongs on `model.LifecycleReader`
(`model/reader.go:33-35`), where it already is.
**M1a subset:** the interpreted record only — **no projection, no migration, no
new reader.** M1a reads it back with `journal.Facts().QueryEvidence` on its
evidence kind, the pattern `projection/rebuild.go:41` already uses.

- **interpreted record**: L2 arm, bindings, unresolved facts, and the pinned
  contract ID. **No codebook version at M1.** `CodebookDefinitionRef`
  (`model/definition.go:84-88`) and `SemanticEnvelopeRef` (`model/envelope.go:15-22`)
  have zero constructors and zero references in the tree, and R7's full
  definition resolution is deferred to M5 — so a codebook field at M1 could only
  be a zero value nothing produces, satisfying the gate while answering neither
  half of the user's requirement. R7 has **no M1 discharge**; the contract ID,
  which `ingress/claude/capture.go:30` already populates, is what M1 carries.
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
  `tasks.NewLifecycleReader`. **It must also return the interpreted record** —
  `model.LifecycleReader` yields `OccurrencePage` only (`model/reader.go:36-38`),
  and the interpreted record is what this epic exists to produce. A user who can
  list what arrived but not what it was interpreted as has half a pipeline.
  **The rebuild is O(all history)**: `projection/rebuild.go:39-57` accumulates
  every row unbounded, then `DELETE`s and re-inserts (`:69`). Accepted at M1
  because history is small; the missing incremental maintainer is recorded in §5.
- projection, migration, binding filter and payload-by-digest reader land here at
  M1b, designed once against all ten events. **Extract the paging/cursor helper
  so both readers share it.** The hazard is prospective, not present: today's
  contract and event predicates sit in the SQL `WHERE` clause
  (`projection/reader.go:43-54`) before `ORDER BY … LIMIT ?` (`:55`), and keyset
  paging (`:98-102`) is correct. But bindings are stored as `bindings_json` and
  decoded in Go (`:84-87`), so a **binding** filter cannot be expressed in SQL
  without a schema change and will necessarily post-filter a `LIMIT`-bounded
  page. That must not be fixable in one reader copy and not the other.
- **The ratified ordered pair is preserved by construction.** URD R6 fixes a
  delivery as *two* write transactions — blob, then the record commit. The
  occurrence and interpreted evidence are therefore emitted as **two
  `provenance.Effect` values in one `provenance.OperationInput`**, not two
  `Append` calls: `JournalAppender.Append` (`receipt/journal.go:113`) is one
  `ApplyContext` per call, so a second call would be a third transaction on the
  blocking-hook path, where contended ingress p99 already equals the deadline.
  `receipt/service.go:83` passes a one-element `Effects` slice today;
  `internal/tasks/governed_create_slice.go:161,230` demonstrates multi-effect
  operations in this tree. The consultation record (M1b) is a third effect in the
  same operation. Any change to this requires amending R6 and re-deriving the
  lock-hold budget.

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

**Owns:** `internal/lifecycle/activation/**`,
`internal/target/claudecode/assets/pasture-hooks/hooks/{hooks.json,pasture-activation.json}`,
`internal/codegen/claude_hooks.go`

Changing activation state regenerates those two artifacts
(`codegen/claude_hooks.go:479-485`), and M1b's Done gate requires a zero-diff
`make generate`. **`claude_hooks.go:453` indexes `config.Hooks["SessionStart"][0]`
with no length guard** while `:456` guards `PreCompact` — so if `SessionStart`
ever goes withheld, `make generate` panics instead of reporting. Guard it.

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

- wire frontend → `Lower` → legalize → backend → **record**. The journal
  `Append` is issued **once, after backend returns**, carrying the occurrence,
  interpreted and (for gate events) consultation effects together. `Effects` is
  built in `receipt.Service.Receive` (`service.go:83`), so the consultation
  effect must be an input to it — a record-then-legalize order would force a
  second `Append`, i.e. a third transaction, which §SLICE-3 forbids. Proposal
  §2.1's diagram shows record and legalization as parallel consumers of L2; this
  is the ordering that realises it. No stage bypassed.
- **return exit 0 always, report on stderr.** Per the user's ruling: *"For now
  return exit code 0 for all."* Deny semantics are deferred until there is a
  working pipeline. Remove the `exitWithCode` call at
  `cmd/pasture/hook_lifecycle.go:46`, and install a **top-level `recover()` in
  `RunE`** converting a panic to a stderr report and exit 0 — Go exits 2 on an
  unrecovered panic, which five M1b events read as *deny*.
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
  invocation creates no database file". **Keep its existing self-contained
  `go build -o t.TempDir()/pasture .` (`:21-26`)** — that already avoids the
  shared-`bin/pasture` race. Do **not** reach for `main_test.go`'s
  `binaryPath`/`runCLI`: that file is `package main_test` while
  `hook_lifecycle_production_test.go` is `package main`, so those identifiers do
  not cross, and moving the file breaks its `lifecycleCLIClock` /
  `lifecycleCLIOperations` references (`cmd/pasture/hook_lifecycle.go:16,20`).
  **Do not `make build` from a package test.**
- **fault cases against the built binary: unwritable `--db`, missing flag, and a
  payload over `model.MaxNativePayloadBytes`**
  (`internal/handlers/hook_lifecycle.go:75-77`) — each asserting exit 0 and a
  stderr report naming the fault class.
- **A malformed payload is NOT a fault.** `ingress/claude/capture.go:42-47` sets
  `Disposition = model.CaptureMalformed` and keeps the delivery;
  `receipt/service.go:97-98` rejects only `Capture == 0`, so the occurrence
  commits and the handler returns nil — exit 0, empty stderr. Assert exactly
  that: **exit 0, no stderr, recorded with `Capture == model.CaptureMalformed`.**
  Making malformed input emit a stderr fault would invert the ratified rule
  *"automate data entry, not semantic guessing… do not silently drop it"*.
  **No fault-injection flag or env var in `cmd/pasture`** — that would be a
  deliberate crash path in the binary Claude executes on every hook.
- **Panic behaviour is out of M1 scope and deliberately unasserted.** There is no
  `recover` on this path today (`cmd/pasture/main.go:28-36`,
  `hook_lifecycle.go:34-48`, `handlers.hookLifecycle`), so a panic exits 2 —
  which Claude reads as *deny*. Closing that requires a stated production change
  (a top-level `recover` reporting on stderr and exiting 0), not an unassigned
  test. Recorded in §5 as a known gap; **do not** add a panic test that asserts a
  crash, which is what `internal/codegen/ir/capability_panic_subprocess_test.go:27`
  does and which contradicts the exit-0 contract.
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

**So assert payload-derived values instead.** At M1a: exactly one binding,
equal to `model.NativeBinding{Kind: model.BindingSession, Value:
"b3cfe877-feb4-4ba3-9500-414c8bfb51c4"}`. Assert the **(kind, value) pair**, not
the bare value — `verifyIdentities` exists precisely because a name-only check
would let a session field supplied under a request kind produce a semantically
wrong correlation inside IR the verifier already blessed
(`43dbbf1^:event.go:462-466`). A bare-value assertion is also satisfiable by
`transcript_path`, which embeds the same UUID. At M1b: the `tool_use_id` from the
captured `PreToolUse`/`PostToolUse` payloads.

**The same argument defeats the waist constructors, not just `Lower`.**
`ingress/claude.Parse` already emits this binding today (`capture.go:113`) and
`receipt.Service.Receive` already persists it (`service.go:70`), so the assertion
would pass at `511e2bb` with no waist at all. `BindEvent`, `NewEvent` and
`verifyIdentities` are likewise established only by code inspection and SLICE-1's
unit tests — which is why those tests carry the negative cases below.

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
| `CaptureProvenance` corpus path + root | SLICE-2 | SLICE-5 |
| `hookLifecycleCmd` parent command var (`cmd/pasture/hook_lifecycle.go:30`) | SLICE-7 | SLICE-3 |
| `model.NativeBinding` native-name field | SLICE-3 | SLICE-2 (`Parse` must retain it) |
| consultation effect into `receipt.Service.Receive` | SLICE-4 | SLICE-2 (`service.go:83` builds `Effects`) |
| capture corpus case type + loader | SLICE-5 | SLICE-2 (authors `testdata/**`) |

SLICE-3's `hook_lifecycle_list.go` and SLICE-7's `hook_lifecycle.go` are
file-disjoint but share package `main`: the `list` file calls `AddCommand` on a
variable SLICE-7 is rewriting in another worktree. SLICE-7's merge-last rule is
binding across the whole `cmd/pasture` package, not just its own file.

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
- **`acceptance.Observation` cannot hold a process result** (`report.go:20-25`),
  and `acceptance.Case` requires eight fields (`loader.go:273`) including all
  eight `ExactDelta` sections with digests (`:503`) — a production-path corpus
  schema, not a provenance one.
- **The occurrence projection has no incremental maintainer.** Nothing on the
  write path inserts a projection row, so any read must rebuild from the journal.
  Recorded as a known gap, not solved at M1.
- **V6 holds by construction, not by test.** Ingress performs no version
  admission check at all; the existing test uses `2.1.220`, which the widened
  range admits, so it exercises no out-of-range case.
- **`internal/lifecycle/claude/` is untracked**, not in the tree
  (`git ls-files` returns nothing).

---

## 6. Done

**M1a** — three lines, one moved up from M1b rather than added:
- [ ] A real `SessionStart` reaches the interpreted record through the built
      binary, and the recorded binding equals the **pair**
      `{Kind: model.BindingSession, Value: "b3cfe877-feb4-4ba3-9500-414c8bfb51c4"}`
- [ ] SLICE-1's DB-free unit tests pass: one event per arm, plus the three
      `verifyIdentities` negatives *(moved up from M1b — SLICE-1 is complete at
      M1a, so its gate belongs here, and §3 names these as the honest evidence
      that `Lower` and the constructors are load-bearing)*
- [ ] Code inspection confirms no stage bypassed (SLICE-7)

**M1b:**
- [ ] Every **enabled** event traverses the pipeline, asserted on payload-derived
      values
- [ ] Two byte-identical deliveries yield two distinct records (V1)
- [ ] `Lower` unit-tested with no database, one event per arm
- [ ] Each enabled event has an authentic capture **and a provenance record**
- [ ] `pasture hook lifecycle list` returns occurrence **and interpreted**
      records a user can read
- [ ] A payload body is retrievable by digest through a public reader (V2)
- [ ] A delivery is two write transactions: blob, then one operation carrying the
      occurrence and interpreted effects together (R6)
- [ ] Gate-consultation events produce a consultation record — the durable
      consumer that keeps `legalize`/`backend` from being no-caller passes. V8
      itself is discharged by the interpreted record's arm plus the proceed
      answer; it does not compel a second record type.
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
