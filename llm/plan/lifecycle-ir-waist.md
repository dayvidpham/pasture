---
status: SUPERSEDED — historical provenance only. DO NOT IMPLEMENT FROM THIS DOCUMENT.
superseded_by: aura-plugins-6ljvd (PROPOSAL-10), llm/plan/proposal-10-hook-lifecycle-architecture.md
references:
  request: aura-plugins-s43qq
  impl_plan: aura-plugins-sgxp6
  supersedes: aura-plugins-gwva1
  reviews_round1: aura-plugins-dkuiu, aura-plugins-4yydp, aura-plugins-b627b
  reviews_round2: aura-plugins-1q0hn, aura-plugins-hsth1, aura-plugins-4kb2i
  reviews_round3: aura-plugins-79vaz, aura-plugins-1fv7y, aura-plugins-28mlb
  authority: llm/research/hooks-ir-compilers-architecture-lessons.md
---

> # ⚠ SUPERSEDED — PROPOSAL-4
>
> **This is PROPOSAL-4. It was superseded six proposals ago and is retained only as
> provenance. Do not implement, cite, or plan from it.**
> Claude lifecycle M1 subsequently landed at
> `0414ad9a7455905c6f865468fe0f2c23222d11b7`; none of this document's replay-key,
> actor-resolution, `BackendView`, or monolithic `Lower` design describes that
> runtime. Current exact provider captures are record-specific privacy evidence,
> not authorization to restore this superseded IR.
>
> **Current ratified architecture:**
> [`llm/plan/proposal-10-hook-lifecycle-architecture.md`](proposal-10-hook-lifecycle-architecture.md)
> (PROPOSAL-10, `aura-plugins-6ljvd`, ratified 2026-07-30 with two amendments).
>
> Standing authority is unchanged and is **not** superseded:
> [`llm/research/hooks-ir-compilers-architecture-lessons.md`](../research/hooks-ir-compilers-architecture-lessons.md).
>
> **What changed between PROPOSAL-4 and PROPOSAL-10** — the stages survive; the vocabulary
> and the transaction structure do not:
>
> | This document (P4) | PROPOSAL-10 | Note |
> |---|---|---|
> | `Event` / `Semantics` / `Origin`, the "waist" | `OccurrenceRecord` (§5.2) | Same role. Now journal-resident, envelope-stamped, and reachable only through `LifecycleReader`. |
> | `BindEvent` / `NewEvent` | `ReceiptService.Receive` → T1 `Journal.Apply` (§5.2) | No separately addressable pure L1→L2 pass. |
> | `Lower(ctx, deps, event)` — one terminal L2→L4 observation pass | **Split.** T1 receipt (§5.2) for evidence; `Interpreter.Interpret` (§8.2) for semantics; `internal/lifecycle/lowering/{canonicalize,effects}.go` (§18.1 S3) for effect mapping; `EpochControlService.CommitCandidate` (§11.2) for authority. | The word "lowering" now denotes **L3→L4**, not L1→L2. |
> | `ReplayKey`, `RecordReplayed`, dedup on payload digest (§2, §6) | **FORBIDDEN.** §5.2 bans these symbols by name; §16.4's replay source guard enforces it, proven by `MutateReintroduceReplaySymbol`. | Binding UAT decision (§0.3): *every host delivery is appended as a distinct occurrence.* This is the single largest reversal. |
> | `Outcome{OutcomeRecorded}` | `EvidenceReceipt{OccurrenceID}` (§5.2) | |
> | `Frontend.Parse(payload, requested) (Event, error)` | `claude.Parse` → private `CapturedDelivery` (§5.1) | `CapturedDelivery` is private transport, never a public model. |
> | M1–M4 milestone ladder (§9) | P0-PIN / P0-CAPTURE / M1–M6 + catalogue completeness (§17) | |
> | Differential equivalence as the M2 gate (§9) | **Not required at any milestone.** PROPOSAL-10 is a Claude-2.1.210-only plan. | Recorded as conflict C3 in the current document §8.1. |
>
> The files this document specified — `internal/lifecycle/{event,key,lower,frontend,backend}.go`
> — have been removed from the tree (`43dbbf1`, and S1/S2 replacements). See
> `proposal-10-hook-lifecycle-architecture.md` §7 for why `lower.go` stays deleted.

# PROPOSAL-4: Canonical lifecycle IR waist

Supersedes PROPOSAL-3. Round 3 returned ACCEPT on axis C and REVISE on A and B,
with **all three axes converging on the same list**. No architectural objection
remains; every open finding is an undeclared type or an unspecified encoding —
material I cut from PROPOSAL-2 while trimming speculative surface, and took
load-bearing declarations with it.

| Round | What was contested |
|---|---|
| 1 | The architecture — is the waist real, is it too heavy |
| 2 | The contract — four things declared but unimplementable |
| 3 | The declarations — types referenced but never defined |

## 0. Provenance

Authority: `llm/research/hooks-ir-compilers-architecture-lessons.md`.

| This plan | Research | Relationship |
|---|---|---|
| hourglass waist | §2 | **stated** |
| Go frontend is not "JSON as API" | §13.1 | **stated** |
| middle-end owns operation selection | §6 | **stated** |
| legalization failure is first-class | §8 | **stated** |
| no IR serialization yet | §9 | **stated** |
| differential equivalence proves a waist | §11 | **stated** |
| `Semantics`/`Origin` split | §7 | **derived** |
| type erasure at `BindEvent` | §7 | **derived** |
| *one exported* `Lower` symbol | §6 | **derived** |
| constructor-enforced invariants | §8 | **derived** |
| replay key from payload digest | §8 | **derived** |

## 1. The waist

Claude `PreToolUse` against OpenCode `tool.execute.before` — **five** of eight
axes differ, and differ correctly, because they describe how to speak back to
one specific host (research §7 drops target detail at L2):

```
                    Claude PreToolUse      OpenCode tool.execute.before
  semantic          gate-consultation      gate-consultation        SAME
  blocking          blocking               blocking                 SAME
  stop-loop         not-applicable         not-applicable           SAME
  identity kinds    session, tool-call     session, tool-call       SAME
  ---------------------------------------------------------------------
  surface           claude-command-json    opencode-named-output    DIFFERS
  mutation          input                  output-object            DIFFERS
  order             concurrent-native      sequential-load          DIFFERS
  reconciliation    host-native            sequential-mutation      DIFFERS
  failure           exit-2-blocks          throw-fail-fast          DIFFERS
```

## 2. Declared types

All previously-undangling references are now defined (A-blocker-1, B-blocker).

```go
// Exact native event spelling. Validated against the pinned catalog.
type NativeEventName string

// Fixed-width SHA-256 over the exact bytes read at the process boundary,
// computed BEFORE parsing so it is well-defined even for payloads that fail
// to parse. The zero value is invalid and rejected.
type Digest [32]byte

func NewDigest(raw []byte) Digest
func (d Digest) IsZero() bool

// Native identity input to the constructor: what the frontend extracted,
// still carrying its native field name for validation against the mapping.
type Identity struct{ /* unexported: kind, nativeName, value */ }

func NewIdentity(kind runtime.NativeIdentityKind, nativeName, value string) (Identity, error)
func (i Identity) Kind() runtime.NativeIdentityKind
func (i Identity) NativeName() string
func (i Identity) Value() string

// Waist-side identity: native field naming stripped (axis C, round 2).
type SemanticIdentity struct {
    Kind  runtime.NativeIdentityKind
    Value string
}
```

`Identity` → `SemanticIdentity` conversion happens **inside** the constructor,
which drops `nativeName` and sorts by `(Kind, Value)`. Native field names exist
to validate against the pinned mapping; once validated they are target detail
and do not belong in the waist.

### 2.1 Constructor invariants (restored)

Enforced in `NewEvent`, per research §8:

- every `Identity`'s `(Kind(), NativeName())` **pair** matches a declared
  `NativeIdentityField` on the pinned mapping. Validating the name alone is
  insufficient: `session_id` could be supplied as `IdentityRequest`, and since
  the waist compares identity *kinds*, that would produce a semantically wrong
  correlation inside IR the verifier has already blessed (axis A, round 4);
- every mapping identity marked `required` is present;
- unknown identity names rejected;
- values non-empty and bounded (`identityValueMaxBytes`);
- duplicate `(kind, nativeName)` pairs rejected;
- `Digest` non-zero;
- Pasture actors, assignments, `JournalID`s, revisions and evidence have **no
  field to occupy**.

## 3. The waist and its origin

```go
// Opaque (A/B round 3: an exported slice field is aliasable and in-place
// sorting would mutate the caller's data). Accessors return copies.
type Semantics struct{ /* unexported */ }

func (s Semantics) Semantic() runtime.EventSemantic
func (s Semantics) Blocking() runtime.BlockingMode
func (s Semantics) Identities() []SemanticIdentity   // defensive copy, sorted
func (s Semantics) EquivalentTo(other Semantics) bool
func (s Semantics) CanonicalKey() string

type Origin struct{ /* unexported */ }

func (o Origin) Contract() ir.RuntimeContractID
func (o Origin) Harness() ir.HarnessID          // derived from Contract
func (o Origin) NativeEventName() NativeEventName
func (o Origin) PayloadDigest() Digest
```

### 3.1 The backend capability boundary

D9 retains the immutable `LifecycleEventMapping` from bind time rather than
re-deriving it — `internal/runtime` deliberately has no native-name lookup, so
PROPOSAL-2's D8 was impossible. But axis C is right that an exported
`Origin.TargetBehaviour()` is a side door: the §5 invariant would depend on
nobody reaching for a method `Lower` already holds.

```go
// Backend-only. Lower must never reference this symbol.
func BackendView(e Event) Backend

type Backend struct{ /* unexported */ }
func (b Backend) TargetBehaviour() runtime.LifecycleEventMapping
```

Go cannot make this compiler-enforced across packages without ceremony that
costs more than it buys. What it does buy is collapsing the invariant to one
greppable question — *does `Lower`, or anything it calls, reference
`BackendView`?* — answerable mechanically rather than by reading for intent.

### 3.2 CanonicalKey and replay-key encoding

Axis B: rejecting a separator at identity construction is not viable, because
values come from host payloads and Pasture cannot dictate their content.
Length-prefixing is unambiguous regardless of content.

```
field    := decimal-length ":" raw-bytes
key      := field(contract) field(nativeEventName) field(hex(digest))
canonical:= field(semantic) field(blocking) field(count) [field(kind) field(value)]...
```

Identities are sorted by `(Kind, Value)` before encoding; duplicates of the same
kind are retained in sorted order. Two different triples cannot produce the same
string (A round 3).

## 4. Frontend

```go
type Frontend interface {
    Harness() ir.HarnessID
    Parse(payload []byte, requested NativeEventName) (Event, error)
}
```

Pure: no store, no clock, no effects. Golden-IR tests need no host and no
database.

## 5. Construction

```go
func BindEvent[E comparable](c runtime.LifecycleContract[E], event E) (EventBinding, error)

type EventBinding struct{ /* unexported */ }
func (b EventBinding) DeclaredIdentities() []runtime.NativeIdentityField
func (b EventBinding) NewEvent(digest Digest, identities []Identity) (Event, error)

type Event struct{ /* unexported */ }
func (e Event) Semantics() Semantics
func (e Event) Origin() Origin
```

D1 (confirmed by all three axes, three times) — the constructor derives
semantics and target behaviour from the binding, and accepts only what the
binding cannot know: the digest and the extracted identity values.

## 6. Lowering

```go
func Lower(ctx context.Context, deps Deps, event Event) (Outcome, error)

type Deps struct {
    Recorder ObservationRecorder   // required
    Actors   ActorResolver         // required
    Clock    func() time.Time      // required; no time.Now fallback
}
```

`Deps.Logger` is **removed** (B, C round 3): a field declared required, with no
fallback permitted and nothing consuming it, is the speculative surface the
previous two rounds were spent removing. It returns when something needs it.

`Clock`'s only consumer is the observed-at timestamp. It is deliberately not in
the replay key, so a fake clock cannot change dedup behaviour.

```go
// One call, one transaction (B round 3). The activity and task event are
// written together or neither is visible; exposing two calls invited a
// partial write.
type ObservationRecorder interface {
    RecordObservation(ctx context.Context, o ObservationRecord) (RecordOutcome, error)
}

type ObservationRecord struct {
    Actor      provenance.AgentID
    Observed   Origin
    Semantics  Semantics
    ObservedAt time.Time
    ReplayKey  string
}

type RecordOutcome uint8
const (
    RecordCreated RecordOutcome = iota + 1
    RecordReplayed
)

type ActorResolver interface {
    ResolveHookActor(ctx context.Context, o Origin) (provenance.AgentID, error)
}
```

The production adapter MUST implement `RecordObservation` in a single
transaction such that an error leaves neither the activity nor the event
visible. Actor resolution MUST succeed before any write is attempted, so a
failed resolve cannot leave a partial record (A round 3).

`ActorResolver` MUST verify the resolved ID is non-zero and registered, MUST
namespace by harness — the existing `pasture/automaton/hook/<name>` convention
has no harness segment, so OpenCode `session.idle` and a same-spelled Claude
event would collide (A round 3) — and MUST remain compatible with the existing
registered Claude names.

### 6.1 Outcome — provisional by declaration

```go
type Outcome struct {
    Kind   OutcomeKind
    Record RecordOutcome
}

type OutcomeKind uint8
const (
    OutcomeRecorded OutcomeKind = iota + 1
)
```

D10 — M1–M3 declare only `OutcomeRecorded`. A struct rather than a bare enum so
M5 can add variants and payloads without breaking callers. Declared
**provisional**: the observation path is frozen, the gate path is not designed.

### 6.2 What keeps the middle-end target-agnostic

`Lower`'s behaviour must not vary by harness:

- MAY read `Semantics` and `Origin`'s coordinates;
- MUST NOT reference `BackendView` (§3.1);
- `Deps` contains no lifecycle-table accessor, so harness-varying lookups can
  only occur behind `ActorResolver`.

## 7. Level traversal is not uniform

```
  observation      L2 ──────────────────────> L4 effects   (terminal pass)
  gate             L2 ──> L3 protocol op ───> L4 ──> response
  human response   L2 ──> L3 protocol op ───> L4 ──> response
```

An observation is not a protocol transition: a host reporting a session started
is *evidence*, not authority to advance a phase (research §13.3). M1–M3 never
touch `AdapterOperation`.

D11 — M1–M3 refuse any event whose blocking mode is not `NonBlocking`, with an
actionable error naming M5. Before M5 there is no response encoding, so a
blocking event would leave the host awaiting a result it never receives.
Refusing loudly is the legalization behaviour research §8 requires.

## 8. Production process path (restored)

A and C both flagged its loss as blocking. This is where the properties that
stop a malformed invocation from creating a database actually live.

```
pasture hook lifecycle --harness <id> [--event <native-name>]    # payload on stdin
```

1. `--harness` is **required and typed**; no default, no autodetection.
   Inferring the harness from payload shape is the guess that produces silent
   misclassification.
2. Read stdin bounded by `MaxNativePayloadBytes`; exceeding it is an error, not
   a truncation.
3. Compute `Digest` over the exact bytes read, before parsing.
4. Dispatch to the frontend for `--harness` through a typed table; an unknown
   harness is `UnsupportedHarnessError` naming the supported set.
5. `Parse`. If `--event` is present and the payload names an event, disagreement
   is a hard error — it means a hook is misregistered.
6. **Only now** open the store, construct the recorder adapter and the actor
   resolver, and call `Lower`.
7. Emit no storage identities (`ActivityID`, `JournalID`, row ids) on stdout.

Steps 1–5 perform no I/O beyond reading stdin, so an invalid invocation cannot
create a database file.

## 9. Milestones and testing

| # | Milestone | Status |
|---|---|---|
| M1 | Contract freeze + Claude observation spine | committed |
| M2 | OpenCode frontend + differential equivalence + lazy actor derivation | committed |
| M3 | Codex frontend | committed |
| M4+ | Context binding, gates, human response, escape hatch, retire drift | directional |

Systematic unit coverage is deferred by user direction. Two tests are not:

**M1 built-binary replay test.** Invoke the built binary as a hook would — real
native payload on stdin, isolated `PASTURE_DB_PATH`, unrelated working
directory. One observation writes one activity and one task event; identical
re-invocation returns `RecordReplayed` with zero deltas; an unknown-field
payload is rejected with zero writes.

**M2 differential equivalence.** Compares **Events straight out of the two
frontends and never invokes `Lower`** (axis C round 3): the pair is
`PreToolUse` / `tool.execute.before`, both blocking gate consultations, which
D11 refuses before M5. Testing at the waist is also the more correct level,
isolating the property proved from the effect path. Three assertions:

1. each side's `Origin.NativeEventName()` is the expected native event;
2. each side carries the exact expected identity values;
3. the two `Semantics` are `EquivalentTo`.

`EquivalentTo` alone is insufficient — it is a coarse shape relation, and
Claude `UserPromptSubmit` and `Stop` also reduce to the same shape (axis B
round 2). Assertions 1 and 2 are what catch a frontend emitting the wrong event.

Actor coverage (D6): the static registry has 9 `HookHandler` agents against
30/10/42 pinned events. Do not expand it to 82 — that is a second copy of the
pinned tables and will drift. Derive lazily from `(harness, native name)` at M2.
M1 uses the registered `SessionStart` agent and fails closed otherwise.

## 10. Response encoding — M5, with constraints

`Responder` stays withdrawn. **C1** — one canonical process-boundary response
form; each harness trampoline applies a fixed mechanical mapping to its native
dialect, emitted statically by the generator from the pinned table.
**C2** — verified against `AGENTS.md` and `internal/errors/errors.go:226-255`:
exit code 2 means `CategoryConnection`, but exit 2 is exactly how Claude and
Codex hooks signal *deny*. M5 needs a distinct typed lifecycle response
disposition rather than reusing the general CLI exit-code contract.

## 11. Acceptance criteria

- GIVEN any environment, WHEN an event is processed, THEN the operation is
  selected only by `Lower` from verified IR, AND SHOULD NOT be influenced by any
  env var, argv value, or generated script.
- GIVEN `Lower`, WHEN it executes, THEN it never references `BackendView` and
  contains no harness branch.
- GIVEN the same logical occurrence from Claude and OpenCode, WHEN both are
  parsed, THEN each carries its expected native event name and exact identity
  values, AND their `Semantics` are `EquivalentTo`.
- GIVEN an `Event`, WHEN constructed, THEN semantics and target behaviour are
  derived from the pinned contract, AND SHOULD NOT be caller-supplied.
- GIVEN an identity whose kind does not match the pinned declaration for that
  native field name, WHEN `NewEvent` is called, THEN it is rejected, AND SHOULD
  NOT produce IR carrying a mis-kinded correlation.
- GIVEN an actor, assignment, JournalID or revision, WHEN placing it in an
  `Event`, THEN no field exists to hold it.
- GIVEN two occurrences with different native payloads, WHEN both are lowered,
  THEN two records exist, AND SHOULD NOT collapse.
- GIVEN the identical payload delivered twice, WHEN both are lowered, THEN one
  record exists and the second returns `RecordReplayed`.
- GIVEN a recorder error, WHEN it occurs, THEN neither the activity nor the
  event is visible, AND SHOULD NOT leave a partial record.
- GIVEN a blocking event before M5, WHEN lowered, THEN it is refused with an
  actionable error naming M5, AND SHOULD NOT be silently allowed.
- GIVEN an event with no registered actor, WHEN lowered, THEN it fails closed
  before any write, AND SHOULD NOT invent an actor.
- GIVEN an invalid invocation, WHEN it is rejected, THEN no database file is
  created.
- GIVEN a nil `Deps` field, WHEN `Lower` is called, THEN it fails actionably,
  AND SHOULD NOT fall back to `time.Now`.
