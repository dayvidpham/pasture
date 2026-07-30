---
title: IMPL_PLAN — M1 Claude vertical (MVP)
status: rev4 — cut to MVP scope
proposal: llm/plan/proposal-11-harness-lifecycle-compiler.md
urd: llm/plan/urd-harness-lifecycle.md
authority: llm/research/hooks-ir-compilers-architecture-lessons.md
obsoletes: aura-plugins-sgxp6
baseline: a20bc1a
---

# IMPL_PLAN — M1: Claude vertical (MVP)

**Ten events. One package for the waist. Get it running, then add.**

## The point of M1

**We have never run this pipeline once.** Everything else is secondary to that.
M1 is reverse-planned from the end goal to find the shortest path to a native
Claude event reaching Provenance through the compiler stages — and then adds
onto a running thing rather than assembling a large thing that has never run.

So M1 has two phases with a hard checkpoint between them:

```
M1a  THE SPINE          one event, end to end, through the built binary
     ─────────────────  <-- THE PIPELINE RUNS. Everything after is addition.
M1b  BREADTH + SAFETY   remaining nine events, gate arm, exit-code safety
```

**M1a is the deliverable that matters.** If M1a lands and M1b slips, we have a
working pipeline and a known list of additions. If we build all seven slices
before anything executes, we have three review rounds of speculation and no
evidence — which is what the last three revisions were.

Rev1–rev3 accumulated a guard framework, five package boundaries, five
integration-point contracts, and a mechanism ledger, and took three review rounds
without converging. Almost none of that was load-bearing. Rev4 keeps what the
MVP needs and deletes the rest.

**What changed and why**

| Deleted | Reason |
|---|---|
| `dialect`/`lowering` package split, IP-1/IP-2/IP-3 | Package boundaries are the hardest structure to reverse and we are least certain now. The waist is a **type and signature** property, not a directory layout. Split at M2, when a second frontend shows where the seam actually is. |
| Import-boundary guards (`DI-9`, `LO-3`) | The concern they inherited (`mgn58`) was a *same-package* worry that no longer applies, was never demonstrated, and the purity property is not import-checkable at all: `internal/runtime`'s closure contains `os`, `os/exec`, `syscall`, and `DI-2` requires consuming it. Purity is enforced by `Lower(L1) (L2, error)` taking no context and no dependency. |
| Guard framework — driver, `ScopePredicate`, registry, `DR-6` | Framework for guards that no longer exist. |
| Corpus executor, classifier, must-pass verification | `EE-2` is ten events. That is a table-driven test with `exec.Command`, not a corpus framework. `internal/acceptance` was built for something else. |
| `SemanticFields()`, equivalence-class table | Only used by the M2 differential gate. Build it at M2 with two harnesses in hand, not speculatively with one. |
| `model/ids.go` alias cleanup | Unrelated repo hygiene. Note `:22-23` are **live** (`definition.go:30,53,54,71`) — do not delete that range. |
| V9 generated-artifact guard | M2/M3 concern; Claude already emits the ratified shape. |

**Kept, because each is a real hazard rather than ceremony:** authentic captures,
the exit-code safety work, and an end-to-end test that reads records back.

---

## 1. Package layout

```text
internal/lifecycle/
    waist/        L1 + L2 types, the verified constructors, and Lower.
                  ONE package. This is event.go + lower.go's L1->L2 half,
                  as they originally were.
    frontend/claude/   native payload -> L1
    legalize/     L2 -> L3
    backend/      L3 -> L4 effects
    receipt/  (exists)  raw record + interpreted record + consultation record
    projection/ (exists) EXTEND with a reader for the new records
    activation/ (exists) enabled/withheld set
```

`legalize` and `backend` stay separate from `waist` only because they **write**
and the waist does not — it is easier to keep a pure thing pure when the writing
code is not in the same drawer. Not for boundary enforcement.

---

## 2. M1a — the spine

The shortest path to a running pipeline. **`SessionStart` only** — non-blocking,
already captured (`session_start_2_1_210.json`, digest verified), already
enabled, and the observation arm terminates at the interpreted record with no
legalization needed.

| Slice | Owns | Does |
|---|---|---|
| **A1** waist | `internal/lifecycle/waist/**` | L1/L2 types, verified constructors, `Lower(L1) (L2, error)` |
| **A2** frontend | `frontend/claude/**`, `ingress/claude/capture.go`, `receipt/service.go` | `SessionStart` payload → L1 |
| **A3** record | `receipt/interpreted.go`, `projection/**`, `model/{reader,occurrence}.go`, new `audit/migrate_v*.go` | interpreted record + a reader that returns it |
| **A4** wiring | `handlers/hook_lifecycle.go` + its test | wire the stages; end-to-end test through the built binary |

**M1a is done when:** a real `SessionStart` from the installed host traverses
frontend → `Lower` → record through `bin/pasture`, and the interpreted record is
read back with the expected L2 arm. Four slices, one event, no new safety
surface — `SessionStart` is non-blocking, so exit-code work is not yet on the
critical path.

That is the foundation. It is also the first evidence that any of this design is
right.

## 3. M1b — breadth and safety, added onto a running pipeline

### B0 — (M1a slices A1–A4 are the prerequisite)

### SLICE-1 — The waist

**Owns:** `internal/lifecycle/waist/**`

**Port from** `git show 43dbbf1^:internal/lifecycle/event.go` (657) and
`key.go` (86). Consumes `runtime.EventSemantic` and
`runtime.LifecycleEventMapping` — retained per authority §7:190-193; do **not**
declare a second arm enum.

- L1 and L2 types, with the verified constructors (`BindEvent`,
  `EventBinding.NewEvent`). Keep the unexported-field discipline: `Semantics`
  and `Event` are constructor-owned, which is what makes contract agreement a
  real check rather than a tautology (`event.go:360-364`). In-package code
  *could* bypass it; that is a visible five-line diff and is caught in review.
- `func Lower(L1) (L2, error)` — no `context.Context`, no interface parameters,
  no receiver carrying dependencies. That signature is the purity enforcement.
- Drop the dedup surface: `Digest`, `Origin.digest`, `Origin.PayloadDigest`,
  `Origin.ReplayKey`. `NewEvent` loses its `digest` parameter. The pre-parse
  digest stays on the recording side (`claude.Capture.Digest`).
- Keep `Semantics.CanonicalKey` (`key.go:50`) and `EquivalentTo`
  (`event.go:301`) — M2 needs them.
- Typed unresolved fact with a closed reason enum.
- Unit tests construct L1 directly and open no database.

### SLICE-2 — Claude frontend and captures

**Owns:** `internal/lifecycle/frontend/claude/**`,
`internal/lifecycle/ingress/claude/capture.go`,
`internal/lifecycle/receipt/service.go`,
`internal/lifecycle/ingress/claude/testdata/fixtures/**`

**Ten events:** 1, 3, 8, 11, 12, 13, 25, 26, 29, 30.

`ingress/claude.Parse` (`capture.go:27-49`) is already a frontend — extract and
rename, do not rewrite.

- payload → L1 for all ten, driven by the generated descriptor
- **authentic captures** for all ten. A descriptor-derived fixture cannot
  falsify the descriptor it came from. Capture on the installed 2.1.220 — the
  range is `>=2.1.210,<2.2.0-0`, so no downgrade.
- bindings: `BindingSession` on all ten (contract-required); `BindingToolCall`
  on 8/11/12; `BindingRequest` on 29/30. `PostToolBatch` binds session and emits
  a **tool-call-unresolved** fact.
- malformed payloads produce a typed disposition and a durable unresolved fact
- the pinned descriptor selects **interpretation only, never admission** — an
  out-of-range host version is recorded verbatim (R12)
- golden cases: captured payload → expected L1 → expected L2, per event

### SLICE-3 — Record stage and readers

**Owns:** `internal/lifecycle/receipt/{interpreted,consultation}.go`,
`internal/lifecycle/receipt/journal.go`, `internal/lifecycle/projection/**`,
`internal/lifecycle/model/{reader,occurrence}.go`,
the new `internal/audit/migrate_v*.go` step

- **interpreted record**: L2 arm, bindings, unresolved facts, pinned contract ID
  and codebook version (R7, minimal form)
- **consultation record**: for gate-consultation events — the L3 value legalized,
  the `HostResponse` returned, and a reference to the interpreted record. It
  carries what the legalization plane knows and the interpreted record does not;
  it does not restate arm/contract/codebook.
- both are disambiguated **solely** by autoincrementing row ID. Blob writes use
  `ON CONFLICT(digest) DO NOTHING` (`journal.go:53`) — correct for
  content-addressed bodies, **wrong** for these; do not mirror it.
- **a reader for the new records.** They are not occurrences, so
  `OccurrenceQuery` and `projection.Reader.Occurrences` cannot return them
  (`projection/rebuild.go:14,32,85` populates that table from the occurrence
  evidence kind only). This slice owns the DDL, the replay-apply path, the view
  types, and the query.
- binding filter on `OccurrenceQuery`, applied **in SQL before `LIMIT`** — a
  filter applied in Go after `ORDER BY … LIMIT ?` (`projection/reader.go:55-56`)
  under-fills pages and corrupts cursors. Thread it through `queryFingerprint`
  (`:106`).
- payload-by-digest reader (V2 has no public path today)
- all new durable writes go through `receipt.JournalAppender`, which already
  enforces the caller deadline (`journal.go:118-136`) — R10's mechanism exists;
  do not bypass it.

### SLICE-4 — Legalization and backend

**Owns:** `internal/lifecycle/legalize/**`, `internal/lifecycle/backend/**`

**Port from** `43dbbf1^:lower.go` — blocking dispatch, semantic dispatch, actor
resolution, the single-transaction write.

- gate-consultation arm produces an L3 value; `backend` maps it to the
  consultation record. That is 5 of 10 events and it is what makes URD **V8**
  implementable, and what gives these two stages a real M1 caller.
- observation and human-response arms produce no L3.
- **key the consultation obligation on the arm** (`SemanticGateConsultation`),
  not on blocking mode. Gate-consultation ⟹ blocking is already enforced at
  `runtime/lifecycle.go:382`, so nothing escapes. Keying on blocking mode breaks
  `ElicitationResult`, which is human-response **and** blocking.
- **invert the ported refusal.** `lower.go:242` returns `awaitedReplyError`
  (`:388`) for every blocking event; M1 enables five. Blocking events record
  consultation and answer proceed. `awaitedReplyError` must not survive.
- human-response arm returns a typed `NoAuthority`; no backend rule maps it.
- `MutationInput` modelled in the waist, emitted by no backend rule.
- neither package performs an authority-bearing write — the consultation record
  is the only permitted L4 effect.

### SLICE-5 — Activation

**Owns:** `internal/lifecycle/activation/**`

- the enabling gate requires `Origin == OriginAuthenticCapture` **and** a passing
  `acceptance.CaptureProvenance.ValidateFixture` **and** an in-range
  `HarnessVersion`. A digest proves non-tampering, not provenance;
  `ValidateFixture` returns `nil` early for non-authentic origins
  (`capture.go:45-47`) and `Origin` is caller-asserted.
- bind `ProductionProof` to a referenced passing case — same bare-constant defect
  two lines below `FixtureEvidence` (`activation/types.go:33-38,55-57`)
- replace the hardcoded `if event.NativeName == "SessionStart"`
  (`claude_2_1_210.go:11`) with the declared ten-ordinal set; twenty withheld
  with typed reasons

### SLICE-6 — Exit-code safety

**Owns:** `cmd/pasture/hook_lifecycle.go`

Five of ten events are blocking and the host reads **exit 2 as deny the user's
tool call**. The command exits non-zero today (`:46`) and panics (`:58`).
**Merges before any blocking event is enabled.**

- no non-zero exit from the lifecycle path; report on stderr instead
- top-level `recover()` in `RunE`
- **the panic at `:58` is inside `func init()`** — it cannot return an error and
  no `RunE`-level recover reaches it, because `init()` runs before `main`. Move
  the `MarkFlagRequired` calls into `PreRunE`, which can return an error.
- integration cases against the **built binary**: injected panic, nil
  dereference, storage failure (unwritable `--db`), missing required flag — each
  asserting exit 0 and a stderr report naming the fault class
- typed `HostResponse` with one legal value (`Proceed`) at M1

### SLICE-7 — Wiring and end-to-end proof

**Owns:** `internal/handlers/hook_lifecycle.go`,
`internal/handlers/hook_lifecycle_test.go`, `docs/privacy.md`

**Merges last.**

- wire frontend → `Lower` → record → legalize → backend; no stage bypassed
- **end-to-end table test over the ten events.** `make build`, exec
  `bin/pasture` with argv, then read back through the public readers: the raw
  record, the interpreted record with the expected arm, and the arm's terminal.
  **Exit code cannot be the only assertion** — SLICE-6 makes exit 0 a constant,
  so an exit-only test is satisfied by a handler that returns `nil`.
  At least one assertion must be one only `Lower` can satisfy — e.g. the
  `PostToolBatch` tool-call-unresolved fact, which no mapping lookup yields.
- deliver byte-identical input twice; assert two distinct occurrence IDs and two
  distinct interpreted-record IDs (**V1**)
- a malformed invocation creates no database file
- `docs/privacy.md` — every prompt, tool input and file content passing a
  registered hook is persisted by default. **Gates enabling `PreToolUse`**,
  which carries `FieldToolInput`.

---

## 4. Order

```text
M1a   A1 waist ──> A2 frontend ──> A3 record ──> A4 wiring
                                                    |
                                   ===== PIPELINE RUNS =====
                                                    |
M1b   SLICE-2 remaining nine events + captures  ────┤
      SLICE-3 consultation record + readers     ────┤
      SLICE-4 legalize + backend (gate arm)     ────┤
      SLICE-6 exit-code safety  ────────────────────┤   before any blocking
      SLICE-5 activation (needs captures + SLICE-6) ┤   event is enabled
                                                    |
                                          SLICE-7 breadth proof
```

Within M1b the four slices are parallel; SLICE-6 gates SLICE-5 because enabling a
blocking event before exit-code safety lands means an internal fault can deny the
user's tool call.

---

## 5. Constraints verified in-tree

Facts established by review, recorded so they are not rediscovered:

- **`internal/runtime`'s import closure contains `os`, `os/exec`, `syscall`.**
  Import-based purity checks on anything consuming it are meaningless.
- **Interpreted records are not occurrences.** `projection.Reader.Occurrences`
  reads `lifecycle_occurrences`, populated only from the occurrence evidence
  kind. A new reader is required.
- **Gate-consultation ⟹ blocking is already enforced** at
  `runtime/lifecycle.go:382`.
- **`SemanticExplicitHumanResponse` requires a request identity**
  (`runtime/lifecycle.go:390-398`) — so `PostToolUse` cannot carry that arm, and
  `AskUserQuestion` (a *tool*, `profiles.go:228`) does not give M1 a human-answer
  path.
- **R10's deadline is already enforced** at `journal.go:118-136`.
- **`model/ids.go:22-23` are live** (`definition.go:30,53,54,71`).
- **`ElicitationResult` is human-response AND blocking** — any rule keyed on
  blocking mode must account for it.
- **`acceptance.Observation` cannot hold a process result** (`report.go:20-25`).
- **Claude already emits the ratified Option-2 shape** (`claude_hooks.go:449`);
  `PASTURE_ADAPTER_*` reaches generated output only via the Codex emitter.

---

## 6. Done

**M1a:** a real `SessionStart` traverses frontend → `Lower` → record through the
built binary and is read back with the expected L2 arm. Nothing else.

**M1b:**
- [ ] Ten events traverse the pipeline through the built binary, verified by
      reading records back — not by exit code
- [ ] Two byte-identical deliveries yield two distinct records (V1)
- [ ] `Lower` is unit-tested with no database
- [ ] Each enabled event has an authentic capture; twenty withheld with reasons
- [ ] Gate-consultation events produce a consultation record (V8)
- [ ] No non-zero exit from the lifecycle path; injected panic, nil deref,
      storage failure and missing flag each exit 0
- [ ] No `ReplayKey`, `RecordReplayed`, `Origin.PayloadDigest`
- [ ] `docs/privacy.md` published before `PreToolUse` is enabled
- [ ] `make fmt`, `make lint`, `make build`, `go test -race ./...`,
      zero-diff `make generate`

**Capture risk:** `Elicitation`/`ElicitationResult` need an MCP server that
elicits. If uncapturable they stay **visibly withheld** — eight enabled, twenty-
two withheld — and the human-response arm is exercised at the `Lower` unit level
only. **Do not synthesise a fixture.**

**Not in M1:** differential equivalence (M2), the write gate, lineage, context
disclosure, the raw-ingestion escape hatch (M4), and any package split of the
waist (M2, when a second frontend shows where the seam is).
