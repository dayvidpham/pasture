---
title: PROPOSAL-11 — Harness lifecycle compiler
status: rev5 — reconciled with impl-plan rev6. The impl plan is the governing checklist for M1.
urd: llm/plan/urd-harness-lifecycle.md
authority: llm/research/hooks-ir-compilers-architecture-lessons.md (standing, never superseded)
supersedes:
  - PROPOSAL-4  (aura-plugins-neccm)  — architecture retained, dedup model rejected
  - PROPOSAL-10 (aura-plugins-6ljvd)  — document discarded, requirements retained in the URD
obsoletes_impl_plan: aura-plugins-sgxp6
---

# PROPOSAL-11 — Harness lifecycle compiler

> **Landed-state note.** The Claude M1 described here is implemented at
> `0414ad9a7455905c6f865468fe0f2c23222d11b7`. This proposal remains design
> history; future-tense implementation language below is not current status.

Satisfies [`urd-harness-lifecycle.md`](urd-harness-lifecycle.md). Compiler
vocabulary throughout, per user ruling.

**Revision 5.** Earlier revisions are cited below only where they name a specific superseded claim; the frontmatter tracks the current one. The architecture was accepted by all
six reviewers; the decomposition was not. The single root cause: rev1 treated M1
as *building four new stages* when it is *separating and renaming code that
already exists*, in the tree and in the deleted source. Every correction below
follows from that.

---

## 1. Why there is an eleventh proposal

Ten proposals were written without a requirements document. From the fifth
onward each justified itself against the previous round's reviewer findings.
Three consequences followed, none decided by anyone:

1. **The middle-end was lost.** `internal/lifecycle/lower.go` was deleted because
   a search found no production callers. It had none because the slice that would
   have called it was never built.
2. **Multi-harness fell out of the plan.** PROPOSAL-4's M2 (OpenCode +
   differential equivalence) and M3 (Codex) were renumbered into unrelated work.
3. **A private dialect made the drift invisible.** PROPOSAL-4 said *waist / IR /
   Level 1-4 / frontend / lowering*; PROPOSAL-10 said *occurrence / T1-T3 /
   interpretation*, containing the token `IR` twice in 4,289 lines.

---

## 2. The pipeline

```text
  Claude Code          OpenCode            Codex
       |                   |                 |
  [ frontend ]        [ frontend ]      [ frontend ]     native -> Level 1
       +---------+---------+-----------------+
                 |
           ===========  L1  harness dialect
                 |          SessionStart, PreToolUse, tool.execute.before
           [ lowering ]                                  THE MIDDLE-END
                 |                                       arm + axis selection
           ===========  L2  lifecycle dialect            THE NARROW WAIST
                 |          observation | gate-consultation | human-response
        [ legalization ]                                 authority verified here
                 |
           ===========  L3  protocol dialect
            [ backend ]
           ===========  L4  effects
```

| Stage | Consumes | Produces | Package | Storage |
|---|---|---|---|---|
| frontend | native payload + host version | L1 | `lifecycle/frontend/<harness>` | no |

L1, L2 and `Lower` live in **one package**, `internal/lifecycle/waist`. Splitting the waist is deferred to M2, when a second frontend shows where the seam belongs.
| lowering | L1 | L2 | `lifecycle/waist` (with the IR types) | **no** |
| **record** | L1 + L2 | durable interpreted record | `lifecycle/receipt` | **writes** |
| legalization | L2 + committed state | L3, or none | `lifecycle/legalize` | reads only |
| backend | L3 | L4 effects | `lifecycle/backend` | writes |

The adjacent `lifecycle/ingress` package owns bounded native capture and generated
host contracts; `lifecycle/model` owns durable lifecycle values and reader
contracts. They are part of the landed package map even though they are not
compiler transformation stages in the table.

**The lowering pass is a pure function.** Research §7 requires each level be
separately testable. Its signature admits no dependency capable of I/O:

```go
func Lower(l1 waist.L1) (waist.L2, error)   // no ctx, no interfaces, no receiver deps
```

A function with no context and no injected dependency cannot perform I/O except
through a package-level global, which is one AST check.

### 2.1 The record stage — why lowering has a durable consumer

Rev1 recorded only the raw payload at the frontend boundary, and terminated the
pipeline at L4 effects. Under M1's "legalization exercises no authority", no L3
is emitted, so no L4 effect is emitted, so **lowering's output was computed and
discarded** — a pass with no production caller, which is the exact condition
under which `lower.go` was deleted as unreferenced.

The URD placed the evidence plane at *"frontend + lowering, and the
Level-1/Level-2 record"* (§2.1). Rev2 restores that:

```text
native payload --> [record: blob, then row]          raw body, ordered pair (R6)
               \
                -> [frontend] -> L1 -> [lowering] -> L2
                                          |
                                          +--> [record: interpreted row]   (R7)
                                          |
                                          +--> [legalization] -> L3 -> [backend] -> L4
```

The interpreted record carries the L2 arm, the bindings, and the identity of the
contract that produced it. **R7 has no M1 discharge** (§5). It is what makes the pass's output
observable, and it is the M1 terminal for the observation arm.

**Per-arm terminals at M1** — the pipeline does not terminate uniformly:

| L2 arm | M1 terminal |
|---|---|
| observation | the interpreted record |
| gate-consultation | interpreted record + consultation record + host response `Proceed` |
| human-response | interpreted record; **no L3**, typed `NoAuthority` result |

---

## 3. What already exists — M1 is mostly separation, not construction

**This section is load-bearing.** Rev1's defects all trace to omitting it.

| Already exists | Where | Rev2 treats it as |
|---|---|---|
| a Claude **frontend** | `ingress/claude.Parse` (`capture.go:27-49`) — parses against the generated descriptor, extracts typed identity bindings | extract and rename; do not rewrite |
| **the record stage** | `receipt.Service.Receive` (`service.go:46-89`) | retain in place; `receipt` *is* the record stage |
| **the L2 arm enum** | `runtime.EventSemantic` — `SemanticObservation`, `SemanticGateConsultation`, `SemanticExplicitHumanResponse` (`internal/runtime/lifecycle.go:20-22`) | **retain and consume. Declare no second enum.** |
| **the L1→L2 axis table** | `runtime.LifecycleEventMapping` — semantic, blocking, mutation, order, reconciliation, failure, stop-loop, identities, per event | **retain and consume.** Authority §7:190-193 says so explicitly |
| the human-response correlation invariant | `runtime/lifecycle.go:390-398` — rejects `SemanticExplicitHumanResponse` without a request identity | retain; it constrains the M1 event table (§4) |
| append-every-delivery, no dedup | tree-wide | unchanged |
| content-addressed blob store, ordered pair | `receipt/journal.go` | unchanged |
| replay-derived projection, bounded reader | `lifecycle/projection` | **extend** — see §3.2 |
| ordered timeout profiles + guard | `internal/timeouts`, `lifecycle/guard/timeouts.go` | unchanged |
| descriptor generator that emits + drift gate | `ingress/cmd/hostcontractgen` | unchanged |
| public CLI in the ratified Option-2 shape | `cmd/pasture/hook_lifecycle.go` | **argv surface only** — see §3.1 |

The **deleted** source is also mostly reusable, and rev1 mis-assigned it:

| `43dbbf1^` | Lines | What it actually is | Owner |
|---|---|---|---|
| `event.go` | 657 | **the L1/L2 IR itself**; `EventBinding.NewEvent` (:475) + `verifyIdentities` (:533) is the real L1→L2 transform | SLICE-1 |
| `key.go` | 86 | `CanonicalKey` (:50) deferred to **M2**; `ReplayKey` (:77) dropped | M2 |
| `lower.go` | 434 | consumes `event.Semantics()` (:239), branches on blocking (:241), **writes** via `RecordObservation` (:288) — this is **legalization + backend**, not lowering | SLICE-4 |

Rev1 handed `lower.go` to the lowering slice and called it the middle-end. It is
L2→L4. A worker following rev1 would have fused writes back into the pass —
recreating the fusion that hid the stage.

### 3.1 Defects carried in, verified in-tree

| Defect | Evidence |
|---|---|
| The lifecycle command **exits non-zero today** and **panics** | `cmd/pasture/hook_lifecycle.go:46` `exitWithCode(...)`; `:58` `panic(...)`. An unrecovered panic exits 2 = deny |
| `FixtureEvidenceAuthentic` is a bare constant; the gate checks the caller passed it | `activation/types.go:28-31,52` |
| `ProductionProofPassing` is the **same defect**, two lines below, checked by the same gate | `activation/types.go:33-38,55-57` |
| Activation hardcodes `if event.NativeName == "SessionStart"` | `activation/claude_2_1_210.go:11` |
| `internal/lifecycle/guard` has **no tree-walking driver and zero importers** | no `WalkDir`; no file imports the package |
| `internal/acceptance` has **no executor** | no `os/exec`; nothing runs `Case.Target.Command` |
| `OccurrenceQuery` filters contract and event only — no binding filter | `model/reader.go:9-14` |
| No public payload-by-digest reader; `SQLiteBlobStore` has `Put`/`Exists`/`Reclaimable` | `receipt/journal.go` |
| Twelve unreferenced lifecycle ID aliases | `model/ids.go:13-21,24-26` — `:22-23` are LIVE (`definition.go:30,53,54,71`) |
| `PASTURE_ADAPTER_*` env binding | `codegen/claude_hooks.go:18-20`, via `renderPythonLifecycleAdapter` (`:388-393`) |
| Python transport (Codex) | `codegen/codex_manifest.go:54,128` |
| `__adapter invoke` envelope (OpenCode) | `codegen/opencode_hooks.go:162,172` |

**The guard and corpus frameworks are shells** — recorded as a fact about the
tree, not as M1 work. Rev3 made giving them a driver and an executor M1
prerequisites; **rev5 struck that**. The guard driver is unnecessary once the
package boundary and the `Lower` signature carry the enforcement, and
`internal/acceptance` is a mutation-testing framework whose `Case` schema
(`loader.go:273`, `:503`) is wrong for capture provenance. See impl-plan SLICE-2
for the corpus shape actually used.

### 3.2 Naming

The IR package is **`internal/lifecycle/waist`** — not `ir`, which collides with
`internal/codegen/ir` (116 importing Go files) and which the authority warns
against conflating (§7:195-197); and not `dialect`, which rev3 proposed and
rev5 superseded by collapsing the split.

**Boundary rule** (retained from rev3, restated for `waist`): `waist` types carry
no `provenance.JournalID`, no timestamp, and no `provenance` import — they exist
before and independent of any write. `model` records what *was* written and may
import `waist`, never the reverse. This is a design invariant, not a guarded one:
the compiler enforces the import direction, and the rest is visible in review.

---

## 4. M1 event set

Ten enabled, twenty withheld.

| # | Event | Blocking | Failure | Bindings | L2 arm |
|---|---|---|---|---|---|
| 1 | `SessionStart` | no | report | session | observation |
| 3 | `SessionEnd` | no | report | session | observation |
| 8 | `PreToolUse` | **yes** | **exit 2 = deny** | session, **toolUse** | gate-consultation |
| 11 | `PostToolUse` | no | report | session, **toolUse** | observation |
| 12 | `PostToolUseFailure` | no | report | session, **toolUse** | observation |
| 13 | `PostToolBatch` | **yes** | **exit 2 = deny** | session | gate-consultation |
| 25 | `PreCompact` | **yes** | **exit 2 = deny** | session | gate-consultation |
| 26 | `PostCompact` | no | report | session | observation |
| 29 | `Elicitation` | **yes** | **exit 2 = deny** | session, **request** | gate-consultation |
| 30 | `ElicitationResult` | **yes** | **exit 2 = deny** | session, **request** | **human-response** |

**Three correlation domains,** not two: `BindingSession` (all thirty events,
required), `BindingToolCall` (8/11/12), `BindingRequest` (29/30).

**Corrections to rev1, each verified against `registration/claude_2_1_210.gen.go`:**

- `PostToolBatch` binds `BindingSession` **required** and has nine allowed
  fields, not one. It is `SemanticGateConsultation`, not observation. Rev1 said
  "binds none, carries only `batchResults`, evidence arm" — all three false.
  What is genuinely absent is only the **tool-call** correlation, so it records a
  session-correlated occurrence plus an explicit **tool-call-unresolved** fact.
  Recording it as wholly unresolved would assert Pasture knows less than it does.
- **`PostToolUse` cannot carry the human-response arm.** Rev1 assigned it that
  arm when `tool_name == "AskUserQuestion"`. `runtime/lifecycle.go:390-398`
  rejects `SemanticExplicitHumanResponse` without a request identity, because
  *"an unrelated native occurrence could manufacture a user decision"* — the
  precise hazard R8 exists to prevent. `PostToolUse` binds no request identity.
  It lowers to observation only. Capturing the human's `AskUserQuestion` answer
  needs its own correlation design and is **not** in M1.
- All ten arms are taken from `runtime.LifecycleEventMapping`, not re-declared.

**All three L2 arms are reachable**, which no smaller set achieves.

### 4.1 The exit-code guard is a safety requirement

Five of ten enabled events are blocking with `FailureExitTwoBlocks`: the host
waits and reads exit 2 as *deny the user's tool call*. `AGENTS.md` maps
`CategoryConnection` to exit 2. The command **exits non-zero today**
(`hook_lifecycle.go:46`) and **panics** (`:58`); an unrecovered panic exits 2.

So the lifecycle command returns 0 always and reports on stderr, and installs a
top-level `recover()` in `RunE` so a panic becomes a stderr report rather than
exit 2. The `MarkFlagRequired` panic at `hook_lifecycle.go:58` is in `init()` and
therefore unreachable by that recover — it is deleted at source instead.

*(Struck in rev5: the syntactic exit-token ban and the `errors.Category` totality
guard. Both were mechanisms invented for a settled decision; three rounds of
review found defects in them and no slice owns them. The impl plan's SLICE-7
carries the actual work.)*

This does not resolve §9.2 — it makes deny unreachable until that decision is
made.

### 4.2 `MutationInput` modelled, never emitted

`PreToolUse` alone carries `MutationInput`. The axis is modelled faithfully in
L1 and L2 because it is part of the pinned contract; no backend rule emits it.

### 4.3 Capture logistics

Every enabled event needs an authentic capture. **A digest proves bytes have not
changed; it does not prove they came from a host.** `acceptance.CaptureProvenance`
already carries the real check but returns `nil` when `Origin !=
OriginAuthenticCapture`, and `Origin` is caller-asserted — so labelling a
synthesised fixture `authored` bypasses everything. The enabling gate must
require `Origin == OriginAuthenticCapture`, a passing `ValidateFixture`, and a
`HarnessVersion` inside the pinned range.

Difficulty is uneven: session and tool events are trivial; compaction needs
forcing; **`Elicitation`/`ElicitationResult` need an MCP server that elicits.**
If that round-trip cannot be captured they stay visibly withheld and the
human-response arm goes untested. **Do not synthesise a fixture** — surface it.
The "no authority at M1" claim survives regardless, because §5's static form of
it does not depend on the event being enabled.

---

## 5. Milestones

### M1 — Claude vertical

Separate the stages, enable ten events, and make the pipeline observable.

**Exit criteria:**
- every enabled event traverses frontend → lowering → record → (legalization →
  backend) through the built binary, with the per-arm terminals of §2.1
- the lowering pass is unit-tested with **no database**
- each enabled event is enabled on `OriginAuthenticCapture` + passing
  `ValidateFixture` + in-range version — not a constant
- `ProductionProof` stays a caller-asserted constant at M1, backed by the named SLICE-7 end-to-end case and the CI gate that runs it — `internal/acceptance` cannot execute a `Case`, so there is no runtime referent
- no non-zero exit from the lifecycle path for any externally reachable fault
- correlated pairs retrievable through a public reader: `toolUseID` for 8/11,
  `requestID` for 29/30
- every durable record carries the pinned contract ID. **R7 has no M1
  discharge** — the codebook reference has no producer in the tree and full
  definition resolution is deferred to M5, so a codebook field at M1 could only be
  a zero value that satisfies the gate while answering neither half of the
  requirement.
- **static** proof of no authority: `legalize` and `backend` contain no write
  call at M1, and the human-response arm returns a typed `NoAuthority`

### M2 — OpenCode frontend and differential equivalence

- generated **thin forwarding** TS plugin — *replace*, not retire. Authority
  §122-148 requires OpenCode to have a minimal in-process plugin that forwards
  and returns. Rev1 said "retire", which would leave no native path.
- differential equivalence over a declared equivalence-class table, comparing a
  `SemanticFields()` projection — harness identity, timestamps and row identity
  excluded **by construction**, not by the test remembering

`SemanticFields()`, the equivalence-class table, `CanonicalKey` and
`EquivalentTo` are all **M2**, built with two harnesses in hand.

### M3 — Codex frontend

Replace the Python transport; retire `PASTURE_ADAPTER_*`. Extend equivalence to
three harnesses.

### M4 — raw ingestion escape hatch (R4)

R4 had **no milestone in rev1** — a stated requirement with no owner, the exact
failure this proposal exists to end. A typed versioned raw-JSON command that
decodes into the same L1 through the same verifier, visibly marked as
non-recommended per authority §10.

### M5+ — deferred

Definition resolution, lineage, context disclosure, the normative write gate.

### Deferrals named explicitly, so none is lost by omission

| Requirement | Milestone | Why not M1 |
|---|---|---|
| R4 raw-ingestion escape hatch | M4 | needs the L1 path stable first |
| **R10 bounded-wait deadline** (*"Do A."*) | **already enforced** (`receipt/journal.go:113-127`) | `Append` refuses a non-`ContextJournal` and a non-positive deadline, and calls `ApplyContext` under `context.WithTimeout`. Rev3's deferral was stale. The observational writer-count SLO remains an M2 concern. |
| R7 versioned interpretation identity | M5 | **no M1 discharge** — the codebook reference has no producer in the tree; the pinned contract ID is what M1 carries |

---

## 6. Tradeoffs

| Decision | Rationale |
|---|---|
| Restore the IR from `event.go`, not `lower.go` | `lower.go` consumes `Semantics()` and writes; it is L2→L4. The L1→L2 transform is `EventBinding.NewEvent`. |
| Consume `runtime.EventSemantic` and `LifecycleEventMapping` | They already encode the arms and the L1→L2 axes; authority §7 says retain. Declaring a second enum is the sixth duplicate. |
| Add the record stage | Without a durable consumer, lowering is a no-caller pass — the condition under which it was deleted. |
| Name the package `waist`, not `ir` or `dialect` | `internal/codegen/ir` exists (116 importing files) and the authority warns against conflating them (§7:195-197); `dialect` was rev3's name for a split that rev5 collapsed. |
| Pure `Lower(L1) (L2, error)` with no ctx | Construction-enforcement: a function with no dependency cannot do I/O. Same idiom as `timeouts.Profile`, the one invariant here that held. |

| Defer `CanonicalKey` to M2, drop `ReplayKey` | `CanonicalKey` serves only the M2 equivalence gate — the same rationale that defers `SemanticFields()`; `ReplayKey` is digest-derived dedup that R5 removed. |

| Differential-equivalence machinery designed at M1, run at M2 | A single-harness gate is vacuous; a back-fitted projection is worse than none. |

---

## 7. Validation checklist

- [ ] `make fmt`, `make lint`, `make build`, `go test -race ./...`, zero-diff `make generate`
- [ ] no second `EventSemantic`-shaped enum exists under `internal/lifecycle`
- [ ] no symbol named `ReplayKey`, `RecordReplayed`, `Origin.PayloadDigest`
- [ ] enabling gate requires authentic origin, passing validation, in-range version
- [ ] no non-zero exit from the lifecycle path
- [ ] every durable record carries the pinned contract ID (R7 has no M1 discharge)
- [ ] privacy posture documented before `PreToolUse` is enabled
- [ ] `TestEngineStartReviewUsesAttachedProvenanceAdapter` stays green (R14/V11)

*(Struck in rev5: the guard tree-walking driver, the `acceptance` case executor,
the `dialect`/`lowering` import-closure allowlists, `SemanticFields()`, and "each
guard names its falsifying mutation". The impl plan is the governing checklist.)*

## 8. Acceptance criteria

URD §7's twelve cases, with these scopings recorded rather than left implicit:

- **V5** requires the declared equivalence-class table and `SemanticFields()`;
  both are built at **M2**, with two harnesses in hand. `key.go`'s `CanonicalKey`
  and `EquivalentTo` are restored then, alongside them.
- **V9** holds for Claude at M1 by construction (`claude_hooks.go:449` already
  emits the ratified shape), for OpenCode at M2 and Codex at M3. No M1 guard;
  `PASTURE_ADAPTER_*` reaches generated output only via the Codex and OpenCode
  emitters, both of which M2/M3 replace.
- **V2** requires a public payload-by-digest reader, which does not exist yet.
- **V12** requires a typed unresolved fact with a closed reason enum, which does
  not exist yet.

---

## 9. Open decisions

1. **The write-gate mechanism** — deferred by the user. M1 exercises no
   authority, proven statically.
2. **Exit-code contract** — exit 2 means `CategoryConnection` internally and
   *deny* to the host. Must be resolved before any deliberate deny ships.
3. **The hook-by-hook write-plane audit** the user requested at `q5ams` 20:45
   (*"Audit the Claude Hooks for me, where you place them, why, and how they
   might interact bi-directionally"*) has not been performed. It is deferred to
   the milestone that implements the write gate, and recorded here so it is not
   lost again.
