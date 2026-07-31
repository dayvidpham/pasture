---
title: IMPL_PLAN — M1 Claude vertical (MVP)
status: rev9 — after review round 8 (1 BLOCKER + 8 IMPORTANT closed; MINORs deferred to plan UAT)
proposal: llm/plan/proposal-11-harness-lifecycle-compiler.md
urd: llm/plan/urd-harness-lifecycle.md
authority: llm/research/hooks-ir-compilers-architecture-lessons.md
obsoletes: aura-plugins-sgxp6
baseline: 5271a12
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
- **`waist` exports no second constructor for L1 or L2**, and both carry the
  port's `constructed` flag with an `IsValid()` accessor
  (`43dbbf1^:event.go:369,424` shows the idiom on `EventBinding`). This is what
  makes "`Lower` is L2's only constructor" a property of the package rather than
  an assumption — and it is only enforceable at runtime: `waist.L2{}` is a legal
  composite literal from any package, so downstream guards must reject
  `!IsValid()` (§SLICE-3). There is no compile-time equivalent.
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
- **Unit tests cover one event of each of the three arms**, plus **five** negative
  cases for `verifyIdentities` (`43dbbf1^:event.go:533-611` — the implementation.
  `:460-470` is `NewEvent`'s doc comment, which collapses the first two branches
  below into a single bullet and must not be used as the test list):
  - an undeclared native field name (`:555-564`);
  - a **declared name supplied under the wrong kind** — `session_id` as
    `IdentityRequest` (`:565-574`). **This is the branch §3's whole argument rests
    on**; an "undeclared pair" case alone returns at `:555-564` and never reaches
    it;
  - a duplicate `(kind, native name)` pair (`:576-586`);
  - an absent declared-required identity (`:592-607`);
  - a value `validateIdentityValue` must reject (`:550`) — over 512 bytes, per
    `43dbbf1^:event.go:68-73`, which exists to stop a transcript-sized value
    passing under an identity field name. **This case needs a white-box test in
    `package waist`**, constructing the `Identity` literal directly with
    `constructed: true`: `NewIdentity` applies the same check at `:161`,
    `verifyIdentities` rejects `!IsValid()` at `:538-545` before reaching `:550`,
    and ingress independently caps values at 512
    (`ingress/claude/capture.go:110`). A black-box test asserting `NewIdentity`'s
    rejection does **not** execute `:550` — which is the branch's whole purpose
    (`:468-470`: "a verifier that trusts its input is not a verifier").

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

- **`receipt.Service.Receive` takes the record effects as inputs, and the
  parameter shape must admit absence.** It builds the `Effects` slice
  (`receipt/service.go:83`), so the interpreted effect (M1a) and the consultation
  effect (M1b) must reach it as parameters rather than being appended by a later
  stage — that is what holds the delivery to one `Append` (§SLICE-3). **Make them
  variadic (`…provenance.Effect`) or otherwise optional:** SLICE-7's short-circuit
  branch passes a non-valid capture with the occurrence effect *alone*, and this
  slice merges **before** SLICE-7, so a required non-optional parameter would
  force SLICE-7 to edit a file it does not own at the last merge in the chain.
  The interpreted-record type and its constructor are SLICE-3's, due at M1a (§4).
- **`ingress/claude.Parse` must retain the native field name.** It currently
  emits `model.NativeBinding{Kind, Value}` and discards it
  (`ingress/claude/capture.go:113`),
  but `verifyIdentities` matches on the **(kind, native name) pair**. This is not
  a rename — but **only one mapping is genuinely missing: ordinal→typed enum.**
  Declare no other table:
  - `NativeFieldID`→native name is **already generated** as `fieldNames`
    (`ingress/claude/payload_2_1_210.gen.go:9`). `ingress/claude/capture.go:101` already binds
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
  **`45a8cc4`** as `captureSource` — that is the commit that introduced the
  fixture, as `aura-plugins-16aam`'s own opening line says; `fb00691` added only
  `tools/capture-claude-hook.sh` and did not produce this capture — and its
  committer date **normalised to UTC** as `capturedAt`, because
  `acceptance/capture.go:51-54` parses RFC3339 and then rejects any value whose
  location is not `time.UTC`, so a raw `-07:00` committer date fails the gate.
  Say both are author-supplied rather than implying they were observed. **Without this record the one enabled
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

  Use the shape proven at `peasant-labs/schema/develop` (`testcase/testcase.go:132-139`;
  `:53-60` is the classification constants, not the case shape):
  `name, input, expected, classification (must-pass|must-fail), provenance{source, ref},
  mutation{description}` — where `mutation.description` is **prose** stating what
  change would make the case matter, not an executable operator.

  The must-fail cases are the valuable ones, and each maps to a real bypass:

  | must-fail case | Closes |
  |---|---|
  | a fixture relabelled `origin: authored` | `acceptance/capture.go:45-47`'s early return |
  | a fixture whose `rawFileDigest` does not match its bytes | tampering |
  | a `harnessVersion` outside `>=2.1.210,<2.2.0-0` | version drift |
  | a fixture path escaping the corpus root | `acceptance/capture.go:67-70` |

  **Declare the case instantiation**, because the reference `Case` is generic
  (`peasant-labs/schema/develop/testcase/testcase.go:132-139`) and two slices
  depend on it: `input` is `{fixture: <path relative to the corpus root>}`, and
  the `CaptureProvenance` is read from the sibling
  `<fixture-basename>.provenance.json` the capture script already emits —
  **not inlined in YAML**, because `acceptance.CaptureProvenance`
  (`internal/acceptance/capture.go:35-42`) carries **no struct tags of any kind**:
  `yaml.v3` matches only the lowercased field name, so `harnessVersion`,
  `rawFileDigest` and `capturedAt` decode to empty **with no error**, and the
  record is then rejected by `ValidateFixture` at `:48`/`:51-54` for fields the
  author did supply. `encoding/json` matches those keys case-insensitively.
  (`yaml.v3` **does** honour `encoding.TextUnmarshaler` — `decode.go:591-608` —
  so the origin enum is not the hazard; the field names are.) `expected` is the activation decision:
  `enabled` or `withheld{reason}`. `provenance.source` is a **closed** enum, per
  the reference (`testcase.go:76-108`), not a free string.

  **SLICE-2 authors only `internal/lifecycle/ingress/claude/testdata/**`** — the
  corpus file and its cases. The corpus *type*, its *loader*, the negative-control
  corpus and its rejection test are **SLICE-5's**, and are specified in §SLICE-5.

  `tools/capture-claude-hook.sh:105-119` already emits the provenance record the
  gate reads, but it writes `${event}.provenance.json` while the corpus addresses
  siblings as `<fixture-basename>.provenance.json` — for the one existing capture
  those differ (`SessionStart.provenance.json` vs
  `session_start_2_1_210.provenance.json`). **Author
  `session_start_2_1_210.provenance.json` by hand and rename the script's output
  to match.** The file carries **eight** keys — the six `CaptureProvenance` fields
  plus `redaction` and `event` — so **decode it non-strictly**. This corpus does
  not use `testutil.LoadFixtures` (whose `readFixture` sets `KnownFields(true)`)
  because that loader takes `*testing.T` and this one runs inside the activation
  gate.
- bindings: `BindingSession` on all ten; `BindingToolCall` on 8/11/12;
  `BindingRequest` on 29/30. `PostToolBatch` binds session and emits a
  **tool-call-unresolved** fact.
- the pinned descriptor selects **interpretation only, never admission** — an
  out-of-range host version is recorded verbatim (R12/V6, already satisfied).

### SLICE-3 — Record stage and read surface

**Owns:** `internal/lifecycle/receipt/{interpreted,consultation}.go`,
`internal/lifecycle/receipt/journal.go`, `internal/lifecycle/projection/**`,
`internal/lifecycle/model/{reader,occurrence}.go`, `internal/lifecycle/receipt/reader.go`,
`cmd/pasture/hook_lifecycle_list.go`, `internal/handlers/hook_lifecycle_list.go`,
`internal/audit/migrate.go` and the new `internal/audit/migrate_v*.go` step

`receipt/reader.go` is a five-line file whose only content is a comment saying it
contains no reader — delete it; the comment belongs on `model.LifecycleReader`
(`model/reader.go:33-35`), where it already is.
**M1a subset:** the interpreted record, **plus the two things SLICE-2 consumes at
M1a — the native-name field on `model.NativeBinding`
(`internal/lifecycle/model/occurrence.go:37-41`, today `Kind` + `Value` only) and
the interpreted-record type with its constructor** — and **no projection, no
migration, no new reader.** M1a reads it back with
`journal.Facts().QueryEvidence` on its evidence kind, the pattern
`projection/rebuild.go:41` already uses. **Because SLICE-2 imports both, SLICE-3
merges before SLICE-2 at M1a** (§4).

- **interpreted record**: constructed **from an L2 value** —
  `receipt.NewInterpreted(l2 waist.L2, contract ir.RuntimeContractID) (Record, error)`,
  in package `receipt`, file `receipt/interpreted.go` — carrying the L2 arm,
  **identities in the waist's vocabulary (`[]waist.SemanticIdentity`, whose `Kind`
  is a `runtime.NativeIdentityKind` — NOT `[]model.NativeBinding`, which is the
  occurrence's vocabulary and is what §6's M1a gate explicitly rejects)**,
  unresolved facts, and the pinned contract ID. **Take the L2 itself rather than a
  destructured field list, and reject an L2 that is not `IsValid()`** — hence the
  `error` return. `waist.L2{}` is a legal composite literal from any package (Go
  forbids only *specifying* an unexported field, not the empty literal), so this is
  a runtime rejection and **not** a compile error; with it, the only route to a
  non-empty identity list is through `Lower` (§3, §6). **No codebook version at M1.** `CodebookDefinitionRef`
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
  `tasks.NewLifecycleReader` — **split per the project's command pattern: the
  cobra `RunE` in `cmd/pasture/hook_lifecycle_list.go` delegates to a handler in
  `internal/handlers/hook_lifecycle_list.go`, so the read path is testable without
  the built binary.** SLICE-3 and SLICE-7 therefore both write
  `internal/handlers`, file-disjoint, and SLICE-7 still merges last (§4).
  **It must also return the interpreted record** —
  `model.LifecycleReader` yields `OccurrencePage` only (`model/reader.go:36-38`),
  and the interpreted record is what this epic exists to produce. A user who can
  list what arrived but not what it was interpreted as has half a pipeline.
  **The rebuild is O(all history)**: `projection/rebuild.go:39-57` accumulates
  every row unbounded, then `DELETE`s and re-inserts (`:69`). Accepted at M1
  because history is small; the missing incremental maintainer is recorded in §5.
- projection, migration, binding filter and payload-by-digest reader land here at
  M1b, designed once against all ten events. **The migration exists for the
  binding filter** — bindings are stored as `bindings_json` and decoded in Go, so
  the filter cannot be expressed in SQL without a schema change (next paragraph).
  Adding a step is **not** one new file: `migrationSteps()`
  (`internal/audit/migrate.go:112`) is a hand-maintained ordered registry and
  `MaxKnownSchemaVersion` (`:84`, currently 6) must be bumped in the same change —
  which is why SLICE-3 owns `migrate.go` itself. **Extract the paging/cursor helper
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
`make generate`. **`claude_hooks.go:454` and `:456` index
`config.Hooks["SessionStart"][0]` with no length guard** while `:457` guards
`PreCompact` — so if `SessionStart` ever goes withheld, `make generate` panics
instead of reporting. Guard it.

- the enabling gate requires `Origin == OriginAuthenticCapture` **and** a passing
  `acceptance.CaptureProvenance.ValidateFixture` **and** an in-range
  `HarnessVersion`. This closes the `acceptance/capture.go:45-47` early-return bypass.
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
- **The corpus type and loader live in `internal/lifecycle/activation/corpus.go`.**
  SLICE-2 authors the corpus cases under
  `internal/lifecycle/ingress/claude/testdata/**` and the case shape is specified
  in §SLICE-2; this slice owns the type that reads them, and passes the root
  explicitly rather than borrowing a loader's, so containment holds because we
  chose it.
- **The negative control is
  `internal/lifecycle/activation/testdata/captures_vacuous.yaml`, and this slice
  owns both the file and its rejection test.** Follow `peasant-labs`' precedent
  (`testcase/testdata/vacuous_corpus.yaml`), but **name the invalidity**: the file
  is *syntactically valid* and contains **only `classification: must-pass` cases**.
  The validator must reject it **with a typed reason naming the absent must-fail
  class**, and the test asserts *that reason*, not merely that an error occurred.
  A corpus of malformed YAML would falsify the parser rather than the validator,
  and would leave every validation branch free to `return nil`.
- **The fixture gate runs in this package's tests, never at runtime.**
  `ValidateFixture` does `os.ReadFile` on the fixture
  (`internal/acceptance/capture.go:71`) and `testdata/` is not embedded, so
  evaluating it inside `ClaudeCode2_1_210()` — which is called on every hook
  invocation (`internal/handlers/hook_lifecycle.go:35`) — would break the shipped
  binary from any working directory. `ClaudeCode2_1_210()` stays a **static
  declared set with no filesystem access**; the corpus and the gate are evaluated
  in `activation`'s tests, on the same principle as `ProductionProof` above.

### SLICE-7 — Wiring, command, and end-to-end proof

**Owns:** `internal/handlers/hook_lifecycle.go`, `cmd/pasture/hook_lifecycle.go`,
`cmd/pasture/hook_lifecycle_production_test.go`, `docs/privacy.md`
**M1a subset:** wiring `frontend → Lower → record` (legalize/backend arrive with
SLICE-4 at M1b) plus the `SessionStart` case. **Merges last within each phase.**

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
- **Only `Capture == model.CaptureValid` enters the waist — a declared branch,
  not a bypass. This is M1a work, because it is a regression guard.** Today a
  malformed payload **is** recorded: `Parse` sets a non-zero `Capture` and
  `receipt/service.go:97-98` rejects only `Capture == 0`. The moment this slice
  wires the waist in at M1a, that same payload would instead fail inside the waist
  and be dropped — a silent regression against the ratified *"do not silently drop
  it"*, and one M1a's other gates cannot catch, because the M1a end-to-end case
  feeds the valid fixture.
  `ingress/claude/capture.go` assigns `Delivery.Bindings` **only**
  in the `validateMembers` branch, so `CaptureInvalidUTF8`,
  `CaptureDuplicateField`, `CaptureMalformed`, `CaptureUnsupportedSchema` and
  `CaptureEventMismatch` each produce a delivery carrying **zero bindings**. Every
  Claude event declares `{FieldSessionID, BindingSession, Required: true}`
  (`registration/claude_2_1_210.gen.go`), so a non-valid capture entering
  `NewEvent` fails `verifyIdentities`' required-identity branch
  (`43dbbf1^:event.go:592-607`), writes an error to stderr and never reaches
  `Append` — contradicting the malformed-payload assertion below **and** the
  ratified *"do not silently drop it"* rule. So a non-valid capture
  **short-circuits to the record stage carrying the occurrence effect alone**: no
  interpreted record, still one `Append`, exit 0, no stderr. The "no stage
  bypassed" gate ranges over valid captures; this branch is declared here and
  gated by the assertion below, rather than left to a worker to invent.
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
- **fault cases against the built binary: unwritable `--db`, missing flag, an
  unknown flag, and a payload over `model.MaxNativePayloadBytes`**
  (`internal/handlers/hook_lifecycle.go:75-77`) — each asserting exit 0 and a
  stderr report naming the fault class.
- **intercept cobra's own errors inside this slice's own file.** Deleting the
  `MarkFlagRequired` loop removes `ValidateRequiredFlags` as an error source, but
  cobra's flag-parse errors and the `Args: cobra.NoArgs` violation
  (`cmd/pasture/hook_lifecycle.go:33`) still return from `Execute()` and exit 1 at
  `main.go:35`. **`SilenceErrors`/`SilenceUsage` do NOT close this** — they
  suppress *printing* only, and `ExecuteC` returns the error regardless
  (`cobra@v1.10.2/command.go:1130`, `:1159`, `:1167`). Two changes in
  `cmd/pasture/hook_lifecycle.go`, which this slice already owns, do close it —
  both verified by execution against cobra 1.10.2:
  - `hookLifecycleCmd.SetFlagErrorFunc(func(*cobra.Command, error) error { …report
    to stderr; return nil })`. `FlagErrorFunc()` (`command.go:547-553`) resolves on
    the command itself before walking to its parent, so this binds this subtree
    only and does not quiet the rest of the CLI.
  - replace `Args: cobra.NoArgs` with `cobra.ArbitraryArgs`, and report unexpected
    positional arguments from `RunE`.

  Keep `SilenceErrors`/`SilenceUsage` as the *quieting* measure so cobra does not
  also print — but do not claim either closes the exit code. A typo'd flag, or
  drift between the generated `hooks.json` command line and the binary, would
  otherwise land as a **deny**. That is the escape the unknown-flag and
  extra-argument fault cases above gate.
- **A malformed payload is NOT a fault.** `ingress/claude/capture.go:42-47` sets
  `Disposition = model.CaptureMalformed` and keeps the delivery;
  `receipt/service.go:97-98` rejects only `Capture == 0`, so the occurrence
  commits and the handler returns nil — exit 0, empty stderr. Assert exactly
  that: **exit 0, no stderr, recorded with `Capture == model.CaptureMalformed`,
  and NO interpreted record for that delivery** — that last clause is what gates
  the short-circuit branch above instead of leaving it assumed.
  Making malformed input emit a stderr fault would invert the ratified rule
  *"automate data entry, not semantic guessing… do not silently drop it"*.
  **No fault-injection flag or env var in `cmd/pasture`** — that would be a
  deliberate crash path in the binary Claude executes on every hook.
- **The `recover()` is in M1 scope; asserting its behaviour is not.** Building it
  is the second bullet above, and it is a ratified safety requirement
  (`llm/plan/proposal-11-harness-lifecycle-compiler.md:227,234-235`). There is no
  `recover` on this path today (`cmd/pasture/main.go:28-36`,
  `hook_lifecycle.go:34-48`, `handlers.hookLifecycle`), so a panic exits 2 — which
  Claude reads as *deny*. What is deliberately unasserted is its **behaviour**:
  reaching it would require a deliberate crash path, and the fault-injection ban
  in the bullet above applies. So it is established by code inspection (§6) and
  recorded in §5 — and because "a `recover()` is present" is not a criterion
  (`defer func(){ _ = recover() }()` satisfies it while silently swallowing the
  panic and violating the exit-0-and-report contract), **the inspection must
  confirm a specific shape: `RunE` defers a `recover()` that writes a stderr
  report naming the recovered value and returns nil, so the process exits 0.**
  **Do not** add a panic test that asserts a crash —
  which is what `internal/codegen/ir/capability_panic_subprocess_test.go:27` does
  and which contradicts the exit-0 contract.
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

**So assert payload-derived values instead.** At M1a: **the interpreted record's
identity list** is exactly one entry, `{Kind: runtime.IdentitySession, Value:
"b3cfe877-feb4-4ba3-9500-414c8bfb51c4"}` — the **waist's** kind vocabulary
(`runtime.NativeIdentityKind`), never the occurrence's `model.NativeBindingKind`,
which the next paragraph shows is satisfiable with no waist at all. Assert the
**(kind, value) pair**, not
the bare value — `verifyIdentities` exists precisely because a name-only check
would let a session field supplied under a request kind produce a semantically
wrong correlation inside IR the verifier already blessed
(rationale `43dbbf1^:event.go:462-466`; implemented at `:565-574`). `SessionStart`
declares exactly one identity, so at M1a the pair and the bare value are equally
strong — assert the pair anyway, because at M1b `PreToolUse` binds both session
and tool-call and a value-only check no longer distinguishes them. At M1b: the
`tool_use_id` from the captured `PreToolUse`/`PostToolUse` payloads.

**The same argument defeats the waist constructors, not just `Lower`.**
`ingress/claude.Parse` already emits the *occurrence-side* binding today
(`ingress/claude/capture.go:113`) and `receipt.Service.Receive` already persists
it (`service.go:70`), so an assertion written against
`model.NativeBinding{Kind: model.BindingSession, …}` would pass at the pre-waist
tree (`511e2bb`) with no waist at all. That is exactly why the assertion above is
written against the **interpreted record's** identity list: no interpreted record
exists at `511e2bb`, so it cannot pass without the waist having run.
`BindEvent`, `NewEvent` and
`verifyIdentities` are likewise established only by code inspection and SLICE-1's
unit tests — which is why those tests carry the negative cases below.

**`Lower`'s presence is established by the constructor guard, by code inspection,
and by its DB-free unit tests** (SLICE-1). **The guard is a runtime rejection, not
a compile error** — `waist.L2{}` is a legal composite literal from any package,
because Go forbids only *specifying* another package's unexported field, not the
empty literal. So the interpreted-record constructor takes an L2 and **rejects one
that is not `IsValid()`** (§SLICE-3), which is the idiom the port source already
applies to every opaque type (`constructed` + `IsValid()` on `EventBinding` at
`43dbbf1^:event.go:369,424`, and likewise `Identity`, `Semantics`, `Origin`,
`Event`) — precisely because zero values are constructible. With that guard the
only route to a non-empty identity list is through `Lower`. Code inspection —
SLICE-7's "no stage bypassed" — then covers only the stages carrying no such
obligation. That is the honest claim; an end-to-end oracle cannot make it.

---

## 4. Order

```text
M1a   SLICE-1 waist ──> SLICE-3 record ──> SLICE-2 frontend ──> SLICE-7 wiring
                                                                     |
                                                 ===== PIPELINE RUNS =====
                                                                     |
M1b   ordered, because four M1b contracts run between these lanes:
        SLICE-3 consultation + projection + read verb
          ──> SLICE-4 legalize + backend
          ──> SLICE-5 activation + corpus type/loader
          ──> SLICE-2 nine events + corpus cases  ───────────────────┤
                                                                     |
      SLICE-2 capture gathering (fixtures) — parallel, non-gating ───┤
                                                                     |
                                                    SLICE-7 breadth proof
```

**Cross-slice contracts** — declared because the slices are parallel in separate
worktrees, not because a package boundary is implied. **The milestone column is
load-bearing: a row must be satisfied inside its own milestone's chain above.**
That is why SLICE-3 precedes SLICE-2 at M1a — and **the rule applies to M1b too**.
Four M1b rows run between lanes, and SLICE-2/SLICE-5 would otherwise form a
**cycle**: SLICE-5 exports the case type and loader while SLICE-2 exports the
corpus path and root. Landing SLICE-5's case type before SLICE-2's corpus cases
breaks it, which is what the M1b order above does. Only SLICE-2's **capture
gathering** is genuinely non-gating; its M1b *code* work imports from both SLICE-4
and SLICE-5.

| Type | Exported by | Imported by | Milestone |
|---|---|---|---|
| L1, L2, `Lower` | SLICE-1 | SLICE-2, 4 | **M1a** |
| interpreted-record type + constructor (takes L2) | SLICE-3 | SLICE-2 (`receipt/service.go` emits it) | **M1a** |
| `model.NativeBinding` native-name field | SLICE-3 | SLICE-2 (`Parse` must retain it) | **M1a** |
| consultation-record type | SLICE-3 | SLICE-4 | M1b |
| `hookLifecycleCmd` parent command var (`cmd/pasture/hook_lifecycle.go:30`) | SLICE-7 | SLICE-3 | M1b |
| `CaptureProvenance` corpus path + root | SLICE-2 | SLICE-5 | M1b |
| consultation effect into `receipt.Service.Receive` | SLICE-4 | SLICE-2 (`service.go:83` builds `Effects`) | M1b |
| capture corpus case type + loader | SLICE-5 | SLICE-2 (authors `ingress/claude/testdata/**`) | M1b |
| negative-control corpus + its rejection test | SLICE-5 | — (SLICE-5 owns both; listed so SLICE-2 does not author it) | M1b |

SLICE-3's `hook_lifecycle_list.go` and SLICE-7's `hook_lifecycle.go` are
file-disjoint but share package `main`: the `list` file calls `AddCommand` on a
variable SLICE-7 is rewriting in another worktree. The same holds for their two
files in `internal/handlers`. SLICE-7's merge-last rule is binding across the
whole `cmd/pasture` **and `internal/handlers`** surface, not just its own files.

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
- **No `recover` exists on the lifecycle command path today**
  (`cmd/pasture/main.go:28-36`, `cmd/pasture/hook_lifecycle.go:34-48`), so an
  unrecovered panic exits 2 — which blocking events read as *deny*. SLICE-7
  installs one; its behaviour is unasserted by choice, not by oversight.
- **`yaml.v3` honours `encoding.TextUnmarshaler`** (`decode.go:591-608`). The
  provenance-decoding hazard is the **absence of struct tags** on
  `acceptance.CaptureProvenance` (`internal/acceptance/capture.go:35-42`), which
  makes `yaml.v3` match only the lowercased field name — not the enum types.
- **`migrationSteps()` is a hand-maintained ordered registry**
  (`internal/audit/migrate.go:112`) and `MaxKnownSchemaVersion` (`:84`) is a
  constant, so a new migration step is two edits to `migrate.go` plus the new
  file — not one new file alone.
- **`Delivery.Bindings` is assigned only at `ingress/claude/capture.go:45`**, and
  `validateMembers` returns `nil` for every non-valid outcome — so the **five**
  non-valid dispositions `Parse` can produce (`CaptureInvalidUTF8`,
  `CaptureDuplicateField`, `CaptureMalformed`, `CaptureUnsupportedSchema`,
  `CaptureEventMismatch`) carry zero bindings and cannot satisfy a required
  identity. `model.CaptureDisposition` (`model/occurrence.go:13-22`) declares two
  further members — `CaptureTruncated` and `CaptureOverLimit` — that `Parse` never
  produces; do not write an exhaustive switch from this bullet alone.

---

## 6. Done

**M1a** — four lines: one moved up from M1b, one a regression guard, neither added scope:
- [ ] A real `SessionStart` reaches the interpreted record through the built
      binary, and **the interpreted record's identity list** is exactly one entry,
      `{Kind: runtime.IdentitySession, Value: "b3cfe877-feb4-4ba3-9500-414c8bfb51c4"}`
      — the **waist's** kind vocabulary. Asserting
      `model.NativeBinding{Kind: model.BindingSession, …}` off the occurrence
      instead would pass at baseline `511e2bb` with no waist at all (§3)
- [ ] SLICE-1's DB-free unit tests pass: one event per arm, plus the **five**
      `verifyIdentities` negatives *(moved up from M1b — SLICE-1 is complete at
      M1a, so its gate belongs here, and §3 names these as the honest evidence
      that `Lower` and the constructors are load-bearing)*
- [ ] The interpreted-record constructor takes an L2 and **rejects one that is not
      `IsValid()`** — a runtime guard, since `waist.L2{}` compiles from any
      package — so the only route to a non-empty identity list is through `Lower`;
      code inspection confirms the same for the stages carrying no such obligation
      (SLICE-7)
- [ ] A non-valid capture still records its occurrence: malformed input is **not**
      dropped by the newly-wired waist (SLICE-7's short-circuit branch). This is a
      regression guard — it holds at `511e2bb` today and must still hold once the
      waist is wired

**M1b:**
- [ ] Every **enabled** event traverses the pipeline, asserted on payload-derived
      values
- [ ] Two byte-identical deliveries yield two distinct records (V1)
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
- [ ] No non-zero exit from the lifecycle path — **including cobra's own
      flag-parse and `NoArgs` errors**, closed inside `hook_lifecycle.go` by the
      flag-error func and permissive `Args`, **not** by `SilenceErrors`, which
      suppresses printing only while `ExecuteC` returns the error regardless
      (`cobra@v1.10.2/command.go:1167`). Gated by the unknown-flag and
      extra-argument fault cases asserting exit 0 against the built binary
- [ ] SLICE-7's `RunE` defers a `recover()` that **writes a stderr report naming
      the recovered value and returns nil** — inspected against that shape, not
      merely for the presence of a `recover()`, which a silently-swallowing
      `defer func(){ _ = recover() }()` would also satisfy. Behaviour unasserted
      by choice (SLICE-7, §5)
- [ ] A non-valid capture records the occurrence with **no interpreted record**,
      exit 0, no stderr — the declared short-circuit branch, gated not assumed
- [ ] The vacuous corpus is rejected **with a typed reason naming the absent
      must-fail class**, and the test asserts that reason (SLICE-5)
- [ ] No `ReplayKey`, `RecordReplayed`, `Origin.PayloadDigest`
- [ ] `docs/privacy.md` published before `PreToolUse` is enabled
- [ ] `make fmt`, `make lint`, `make build`, `go test -race ./...`,
      zero-diff `make generate`

**Capture risk:** `Elicitation`/`ElicitationResult` need an MCP server that
elicits; `PostToolBatch` needs the host to emit a batch event, which is not
determinable from this tree. Any uncapturable event stays **visibly withheld**.
**Do not synthesise a fixture.**

**Not in M1:** differential equivalence and `key.go`/`SemanticFields()` (M2), the
write gate, lineage, context disclosure, the raw-ingestion escape hatch (M4),
any package split of the waist (M2), and **R7's codebook-version identity (M5) —
the pinned contract ID is what M1 carries, and R7 has no M1 discharge**
(§SLICE-3). Panic *behaviour* is likewise unasserted, though the `recover()`
itself is built (§SLICE-7).
