---
title: PROPOSAL-11 — Harness lifecycle compiler
status: DRAFT — not yet reviewed, not ratified
urd: llm/plan/urd-harness-lifecycle.md
authority: llm/research/hooks-ir-compilers-architecture-lessons.md (standing, never superseded)
supersedes:
  - PROPOSAL-4  (aura-plugins-neccm)  — architecture retained, dedup model rejected
  - PROPOSAL-10 (aura-plugins-6ljvd)  — document discarded, requirements retained in the URD
obsoletes_impl_plan: aura-plugins-sgxp6
---

# PROPOSAL-11 — Harness lifecycle compiler

Satisfies [`urd-harness-lifecycle.md`](urd-harness-lifecycle.md). Vocabulary is
compiler vocabulary throughout, per user ruling.

---

## 1. Why there is an eleventh proposal

Ten proposals were written without a requirements document. From the fifth
onward each justified itself against the previous round's reviewer findings
rather than against stated requirements. Three consequences followed, none of
them decided by anyone:

1. **The middle-end was lost.** `internal/lifecycle/lower.go` — the Level-1 to
   Level-2 lowering pass, the one place operation selection was supposed to live
   — was deleted because a search found no production callers. It had no callers
   because the slice that would have called it was never built. The compiler
   cannot distinguish *unfinished* from *obsolete*.
2. **Multi-harness fell out of the plan.** PROPOSAL-4's M2 (OpenCode +
   differential equivalence) and M3 (Codex) were renumbered into unrelated work.
   Neither appears in any PROPOSAL-10 milestone.
3. **A private dialect made the drift invisible.** PROPOSAL-4 said *waist / IR /
   Level 1-4 / frontend / lowering*. PROPOSAL-10 said *occurrence / T1-T3 /
   ingress capture / interpretation*, and contains the token `IR` twice in 4,289
   lines. A search in one dialect could not see the other.

PROPOSAL-11 restores the architecture PROPOSAL-4 ratified, keeps the ratified
decisions that superseded PROPOSAL-4's mechanisms, and adds the milestone that
proves the architecture works.

---

## 2. The pipeline

```text
  Claude Code          OpenCode            Codex
       |                   |                 |
  [ frontend ]        [ frontend ]      [ frontend ]     native -> Level 1
       |                   |                 |
       +---------+---------+-----------------+
                 |
           ===========  L1  harness dialect
                 |          SessionStart, PreToolUse, tool.execute.before
           [ lowering ]                                  THE MIDDLE-END
                 |                                       operation selection
           ===========  L2  lifecycle dialect            THE NARROW WAIST
                 |          evidence | gate-consultation | human-response
        [ legalization ]                                 authority verified here
                 |
           ===========  L3  protocol dialect
                 |          StartReview, RecordPlanUAT, Land
            [ backend ]                                  effect selection
                 |
           ===========  L4  effects
                            journal operations, tasks, assignments
```

| Stage | Consumes | Produces | Package | Storage? |
|---|---|---|---|---|
| frontend | native payload + host version | L1 IR | `internal/lifecycle/frontend/<harness>` | no |
| lowering | L1 IR | L2 IR | `internal/lifecycle/lowering` | **no** |
| legalization | L2 IR + committed state | L3 operation, or refusal | `internal/lifecycle/legalize` | reads only |
| backend | L3 operation | L4 effects | `internal/lifecycle/backend` | writes |

**The lowering pass must be a pure function.** Research §7 requires each level be
separately testable; a pass fused into a storage-bearing service cannot be tested
without a database. This is the single most important structural constraint in
this proposal, because violating it is what made the middle-end invisible.

### 2.1 Evidence is recorded before lowering, not after

Recording is not a pipeline stage. It is a side-channel taken at the frontend
boundary, so that a payload we cannot lower is still preserved:

```text
native payload --> [record: blob, then row]  (ordered pair, R6)
               \
                -> [frontend] -> L1 -> [lowering] -> L2 -> ...
```

A payload that fails to lower still produces a durable record plus an explicit
*unresolved* fact (URD V12). This is the concrete form of *automate data entry,
not semantic guessing*.

---

## 3. What already exists

Honest starting point. Fourteen commits on `feat/proposal-57-integration`.

| Built | Where | URD |
|---|---|---|
| Ordered pair: blob write then row commit, crash-window tested | `lifecycle/receipt` | R6 |
| Content-addressed blob store, reclaimable-orphan query | `receipt/journal.go` | R6 |
| Append-every-delivery; no replay key, no dedup symbols | tree-wide, guarded | R5 |
| Replay-derived projection, bounded reader, direct-SQL guard | `lifecycle/projection` | — |
| Typed ingress deadline, zero writes on breach | `receipt`, `model` | R10 |
| Ordered timeout profiles enforced by a guard | `internal/timeouts`, `lifecycle/guard` | R11 |
| Host descriptor generator that **emits**, with a drift gate | `ingress/cmd/hostcontractgen` | R1 |
| Claude registration: 1 enabled, 29 visibly withheld | `lifecycle/activation` | R13 |
| Host version recorded, never rejected; range `>=2.1.210,<2.2.0-0` | `internal/runtime` | R12 |
| **Public CLI boundary already in the ratified shape** | `cmd/pasture/hook_lifecycle.go` | R1 |
| INV-1 record classification with a `go/ast` totality guard | `lifecycle/guard` | — |
| Shared acceptance corpus with mutation accounting | `internal/acceptance` | — |

The generated Claude registration already invokes ordinary argv against a typed
public command:

```
${PASTURE_BIN:-pasture} hook lifecycle --harness claude-code --event SessionStart --host-version "..."
```

That is exactly the Option-2 boundary ratified at `sj1sc` Component 2. No Python,
no hidden envelope, no `PASTURE_ADAPTER_*` on the Claude path.

### 3.1 Known defects carried in

| Defect | Evidence |
|---|---|
| No addressable L1→L2 pass — lowering is absent and capture is fused into a storage-bearing service | `lifecycle/receipt` |
| `FixtureEvidenceAuthentic` carries no digest and no ref; the enabling gate checks that the caller passed a constant | `activation/types.go:28-31,52` |
| Mutation operators declared but not all executed | `aura-plugins-0si2b` |
| INV-1 status check is a hardcoded list of one entry | `aura-plugins-k1dvf` |
| Codex and OpenCode still generate the rejected `PASTURE_ADAPTER_*` Python path | `codegen/codex_manifest.go:54,128` |

---

## 4. What is deliberately NOT carried forward

PROPOSAL-10 designed nine mechanisms. **None is implemented.** They are dropped
as *mechanisms*; the requirements they served survive in the URD and must be
answered again, on evidence.

| Dropped mechanism | Requirement it served | Status |
|---|---|---|
| Capability handshake: `IssuedRequestRecord`, Issue/Bind/Revoke, 15 scope axes | R8 — the write gate | **user-deferred**, decide before legalization |
| `TransitionCandidateRecord` → `CommittedTransition` | legalization split | re-derive at legalization |
| Three-way disclosure record split | context disclosure | re-derive at that stage |
| `BuildIdentity` + status | R7 | re-derive |
| `DefinitionSnapshot`, definition resolution | R7 | re-derive |
| `InterpretationRecord` | R7 | re-derive |
| `CausalLinkRecord`, lineage queries | the core provenance goal | re-derive |
| `EnrichmentFact` snapshots | lineage | re-derive |

Dropping unbuilt design costs nothing implemented. Carrying it forward
unexamined would import a dialect and a set of unratified assumptions.

---

## 5. Milestones

Claude first as the proof of concept, then OpenCode, then Codex.

### M1 — Claude vertical, end to end

Close the pipeline for one harness across the session and tool-use events. Every
stage exists as an addressable pass.

- restore the **lowering** pass as a pure L1→L2 function (port from `43dbbf1^`,
  dedup surface removed, boundary compiler-enforced via `lifecycle/guard`)
- extract the **frontend** from ingress capture so Claude payload → L1 is a
  separate testable step
- **legalization** and **backend** as thin but real stages
- bind `FixtureEvidenceAuthentic` to an actual digest and ref
- **enable ten events**; the other 20 stay visibly withheld

| # | Event | Blocking | Failure | Identity | L2 arm |
|---|---|---|---|---|---|
| 1 | `SessionStart` | no | report | — | evidence |
| 3 | `SessionEnd` | no | report | — | evidence |
| 8 | `PreToolUse` | **yes** | **exit 2 = deny** | `toolUseID` | gate-consultation — agent **intent** |
| 11 | `PostToolUse` | no | report | `toolUseID` | evidence — **result**; human answer when the tool is `AskUserQuestion` |
| 12 | `PostToolUseFailure` | no | report | `toolUseID` | evidence — failed result |
| 13 | `PostToolBatch` | **yes** | **exit 2 = deny** | **none** | evidence — **uncorrelatable** |
| 25 | `PreCompact` | **yes** | **exit 2 = deny** | — | gate-consultation |
| 26 | `PostCompact` | no | report | — | evidence |
| 29 | `Elicitation` | **yes** | **exit 2 = deny** | `requestID` | gate-consultation |
| 30 | `ElicitationResult` | **yes** | **exit 2 = deny** | `requestID` | **human-response** |

**All three L2 arms are exercised.** Evidence, gate-consultation and
human-response each have at least one enabled event, so the lifecycle dialect is
tested across its full shape rather than two thirds of it. No smaller event set
achieves this.

**Two correlation domains.** `toolUseID` (`BindingToolCall`) joins tool intent to
result; `requestID` (`BindingRequest`) joins an elicitation to its answer. Two
domains exercise the identity machinery in a way one cannot.

**`AskUserQuestion` is a tool, not a hook event** (`internal/runtime/profiles.go:228`).
It arrives through `PreToolUse`/`PostToolUse` with `tool_name == "AskUserQuestion"`,
already covered. OpenCode's equivalent is `question(prompt, options)`
(`profiles.go:267`) — a natural differential-equivalence case at M2.

**`ElicitationResult` is enabled as EVIDENCE ONLY.** It is the event URD R8 names
as the canonical explicit-human-response write path, and the write-gate mechanism
is a deferred user decision. M1 must record it and exercise no authority. This
makes the "no authority at M1" assertion meaningful rather than vacuous: the one
event that *would* write is present and provably does not.

**Why the tool events matter architecturally.** `toolUseID` is the join key
between intent (`PreToolUse`) and result (`PostToolUse`). That pair is what makes
tracing generated code back to a human signal constructible at all — with
`SessionStart` alone the provenance is session-grained and no finer. It also
makes L2's **gate-consultation** arm non-vacuous; with one non-blocking event,
two of the three L2 arms are never exercised and the waist is untested where it
matters most.

#### M1-1 — the exit-code guard is a safety requirement, not a convention

**Five of the ten** enabled events are **blocking with `FailureExitTwoBlocks`**
(`PreToolUse`, `PostToolBatch`, `PreCompact`, `Elicitation`, `ElicitationResult`): the
host waits, and exit 2 means *deny the user's tool call*. `AGENTS.md` maps exit
code 2 to `CategoryConnection`. Therefore an internal Pasture storage or
connection fault during `PreToolUse` would exit 2 and be read by the host as a
denial, silently blocking the user's work — converting an unrelated Pasture fault
into lost user work.

The always-exit-0 posture (URD R9) must therefore be **structurally enforced**:
a guard over the lifecycle command's exit paths, naming the mutation that must
turn it red. A convention is insufficient; this epic's documented failure mode is
that only guarded invariants hold, and the failure here is invisible to Pasture
and expensive to the user.

This does not resolve open decision §9.2 — it makes deny *unreachable* until that
decision is made, which is the correct M1 posture per R9.

#### M1-2 — `MutationInput` is modelled but never emitted

`PreToolUse` carries `MutationInput`: it is the one event permitted to rewrite the
agent's tool input. **User decision: model the axis faithfully in L1 and L2,
because it is part of the pinned harness contract, but no backend rule emits it.**
The IR stays a truthful description of the harness; the capability stays unused.

#### M1-3 — `PostToolBatch` has no identity binding

It carries only `batchResults` and binds no identity, so it cannot be joined to
the `PreToolUse`/`PostToolUse` pair by `toolUseID`. Per URD V12 it must record the
occurrence and emit an explicit **unresolved** fact rather than infer an
association from ordering or timing. Do not correlate it by proximity.

#### M1-4 — capture logistics are a real schedule risk

Every enabled event needs an authentic captured payload (P0-CAPTURE); a
descriptor-derived fixture cannot falsify the descriptor it came from. Difficulty
varies sharply:

| Event | Capture difficulty |
|---|---|
| `SessionStart`, `SessionEnd`, `PreToolUse`, `PostToolUse` | trivial |
| `PostToolUseFailure` | easy — induce a failing tool call |
| `PreCompact`, `PostCompact` | moderate — force compaction |
| `PostToolBatch` | moderate — needs a batched tool call |
| `Elicitation`, `ElicitationResult` | **hard — needs an MCP server that elicits** |

If the elicitation round-trip cannot be captured, those two events stay
**visibly withheld** per R13 and the human-response arm goes untested at M1. That
is an acceptable outcome and must not be worked around by synthesising a fixture.
It should be surfaced, not absorbed.

**Exit:** each enabled event traverses frontend → lowering → legalization →
backend → effects through the built binary, observed only through public bounded
readers; the lowering pass is unit-tested with no database; the exit-code guard
fails the build when mutated; a `PreToolUse`/`PostToolUse` pair sharing a
`toolUseID` is retrievable as a correlated pair; an `Elicitation`/
`ElicitationResult` pair sharing a `requestID` likewise; and `ElicitationResult`
is proven to exercise no authority.

### M2 — OpenCode frontend and differential equivalence

- OpenCode frontend producing L1 for the same semantic events
- **differential equivalence gate**: semantically equivalent native events from
  Claude and OpenCode lower to identical L2 IR

**Exit:** the gate passes. This is the milestone that proves a waist exists —
research §11. No prior plan carried it.

### M3 — Codex frontend

- Codex frontend; retire `PASTURE_ADAPTER_*`, the Python transport, and the
  generated TS plugin
- extend differential equivalence to three harnesses

**Exit:** `N + M` demonstrated. No harness-specific operation selection remains.

### M4+ — deferred stages

Versioned interpretation identity (R7), lineage, context disclosure, and the
normative write gate (R8) follow M3 and are re-derived then, not inherited.

---

## 6. Tradeoffs

| Decision | Rationale |
|---|---|
| Restore lowering as a **port**, not a revert | Its type substrate is gone: `Origin`, `Semantics`, `ObservationRecord`, `ActorResolver` no longer exist. The logic is the asset; the 2026-07-29 types are not. |
| Lowering is pure, with no storage | Research §7 demands separate testability. Fusing it into a service is what hid its absence. |
| Record before lowering, not after | A payload that cannot be lowered must still be preserved (R6, V12). Ordering also gives crash safety: orphan blob reclaimable, dangling reference is corruption. |
| Differential equivalence at M2, not M1 | A single-harness MVP cannot demonstrate a waist by construction. Placing the gate at M1 would make it vacuous. |
| Drop P10's mechanisms rather than translate them | None is implemented, so nothing is lost; translating would import the dialect that caused the drift. |
| Leave the write gate undecided | User decision. The principle (R8) is fixed; the mechanism is not, and legalization is three milestones away. |
| Keep actor unauthenticated | Explicit user scope decision. The rejected envelope did not prevent forgery either. |

---

## 7. Validation checklist

- [ ] `make fmt`, `make lint`, `make build`, `go test -race ./...` pass
- [ ] `make generate` produces a zero diff
- [ ] The lowering pass is exercised by tests that open no database
- [ ] No symbol named `ReplayKey`, `RecordReplayed`, or a payload-digest-as-identity exists
- [ ] Every enabled event's fixture carries a digest that matches bytes on disk
- [ ] Every withheld event carries a typed reason and appears in a report
- [ ] A malformed invocation creates no database file
- [ ] Every timeout profile passes the ordering guard, including test profiles
- [ ] Production tests read only public bounded readers
- [ ] Generated host artifacts contain no operation selection and no JSON parsing
- [ ] Differential equivalence passes at M2

## 8. Acceptance criteria

The twelve cases in URD §7 are the acceptance criteria for this proposal and are
not restated here. V4 (lowering testable without storage) and V5 (differential
equivalence) are the two that no prior plan could satisfy.

---

## 9. Open decisions

1. **The write gate mechanism** (URD §6.2) — deferred by the user; required
   before legalization is implemented beyond M1's thin form.
2. **Exit-code contract** — exit 2 means `CategoryConnection` internally and
   *deny* to the host; must be resolved before any real deny ships (R9).
