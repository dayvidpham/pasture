---
references:
  request: aura-plugins-s43qq
  impl_plan: aura-plugins-sgxp6
  research: llm/research/hooks-ir-compilers-architecture-lessons.md
  target_table: internal/runtime/lifecycle.go, internal/runtime/lifecycle_profiles.go
---

# PROPOSAL-1: Canonical lifecycle IR waist

## 0. Provenance of this plan

This plan does not originate from taste. Every structural decision below is
derived from a documented compiler-architecture principle recorded in
`llm/research/hooks-ir-compilers-architecture-lessons.md`. That document is the
authority; this one is its application to Pasture.

| This plan | Derives from | Principle |
|---|---|---|
| §2 two-sided hourglass | §2 The hourglass | Narrow waist: N frontends + M backends via one IR, not N×M translators |
| §3 Go frontend is not "JSON as API" | §3, §13.1 | A parser bound to a versioned input language is a component, not an API |
| §4.1 type erasure at `BindEvent` | §7 Progressive lowering | L1 harness dialect is generic per harness; L2 must be target-agnostic |
| §4.2 constructor-enforced invariants | §8 Verifier | Verify at pass boundaries; make illegal states unrepresentable |
| §4.3 single `Lower` entry point | §6 Frontends emit IR, never target operations | Caller-selected operation is worse than syntax-directed codegen |
| §4.4 per-surface `Responder` | §2 hourglass, backend half | A waist has two sides; emitters are backends |
| §4.3 `UnsupportedSemanticError` | §8 Legalization | Failure must be first-class and actionable, never a silent no-op |
| §4.5 reuse `AdapterOperation` as L3 | §12 Retained assets | The operation DTO set is a serviceable backend instruction set |
| D7 no IR serialization yet | §9 Serialization is a boundary artifact | The in-memory typed structure is the API; JSON is not the semantic model |
| M7 escape hatch | §10 Escape hatches | Same IR, same verifier, visibly marked, never the default |
| M2 differential equivalence | §11 Testing discipline | The one test that proves a waist exists |

**Resolution of the open decisions left in §13 of the research document:**

- §13.1 — Resolved there by the user; restated in §3 of this plan.
- §13.2 *Where does epoch/assignment/actor context come from?* — Observations
  need an **actor** only; they need no epoch or assignment. See §6 and D6.
  Gates and human decisions need full context, which is why they are sequenced
  behind M4 (context binding) rather than attempted at M1.
- §13.3 *What is the honest first semantic for a native observation?* — A
  recorded Provenance activity plus task event. Explicitly **not** an epoch
  lifecycle transition. A host telling us a session started is evidence that a
  session started; it is not authority to advance a protocol phase. See §5.
- §13.4 *Daemon split now or later?* — Later. See D7.

DECISION D7 — the CLI hosts the pipeline in-process; the IR is an in-memory Go
value with no serialized form. Per research §9, the typed in-memory structure
is the API and serialization is a boundary artifact. No boundary exists yet, so
introducing a wire format now would be an unforced versioning commitment. If a
daemon split is later measured to be necessary, the IR gains a serialized form
*at that boundary only*, and the in-memory type stays authoritative.

## 1. Problem

Today a native lifecycle occurrence reaches Pasture like this:

```
native event -> generated Python translator -> `pasture __adapter invoke`
                       ^                              ^
                       |                              |
        decides WHICH semantic operation      accepts a caller-chosen
        via PASTURE_ADAPTER_* env vars        AdapterOperation + JSON DTO
```

Three defects follow from that shape:

1. **Operation selection is delegated outward.** The generated script and its
   environment decide which Pasture operation runs. Authorization and meaning
   are therefore decided by the least trustworthy, least testable component.
2. **Semantics are duplicated per harness.** Each new harness re-implements
   translation in a different foreign language. Three harnesses means three
   chances to disagree, and no mechanism can detect the disagreement.
3. **Raw JSON became the de-facto semantic API.** `__adapter invoke` accepts a
   JSON DTO that names an operation. That is a public, unversioned semantic
   surface wearing a `__`-prefixed disguise.

The pinned tables in `internal/runtime` are correct and are NOT the problem.
They are a declarative *target description* — the equivalent of a machine
description file. The defect is that nothing consumes them as such.

## 2. Shape: a two-sided hourglass

```
   L1  native payloads (many, host-owned, versioned by the host)
       Claude command JSON    Codex strict JSON    OpenCode named/SSE
              \                     |                    /
               \                    |                   /
   FRONTENDS    +-------------------+------------------+
               (one per harness, written in Go, linked into Pasture)
                                    |
                                    v
   L2  ============ canonical lifecycle Event ============   <- THE WAIST
       target-agnostic, verified against the pinned contract
                                    |
                    +---------------+---------------+
                    |                               |
              LOWERING (one pass)            RESPONSE EMIT (one per surface)
              semantic -> operation          decision -> native encoding
                    |                               |
                    v                               ^
   L3  typed protocol operation                     |
       (AdapterOperation + DTOs)                    |
                    |                               |
                    v                               |
   L4  EpochService -> Engine -> Provenance --------+
                    (decision flows back out)
```

The left half (native -> IR) is a **frontend**. The right half (IR decision ->
native encoding) is a **backend/emitter**. Both are necessary. My earlier
decomposition planned only the left half; see §7.

Generated hooks sit strictly *outside* the diagram. They are trampolines: they
forward bytes and an event name, and contain no semantics.

## 3. Why a per-harness Go frontend is not "raw JSON as API"

A C compiler parses C text. That does not make arbitrary text a compiler API.
The API is the IR and the driver contract; the parser is an internal component
bound to a specific, versioned input language.

Identically: `internal/lifecycle/claude` parses Claude's payload because it is
linked into Pasture, bound to one pinned contract, reviewed, and its only
output is IR. What is forbidden is a *public entry point that accepts arbitrary
JSON naming a semantic operation* — i.e. `__adapter invoke`. Confirmed by user.

## 4. Layer contracts

### 4.1 L2 — the Event (package `internal/lifecycle`)

`runtime.LifecycleContract[E]` is generic over a harness-specific event enum.
A uniform frontend interface therefore cannot expose `E`. The IR must **erase**
the type parameter. That erasure is the waist, and it is load-bearing.

```go
// Type-erasing bridge. Generic in, non-generic out. This is the ONLY
// admission point into the IR: an Event cannot exist without a binding, and a
// binding cannot exist without a pinned contract and a typed event value.
func BindEvent[E comparable](
    contract runtime.LifecycleContract[E],
    event E,
) (EventBinding, error)

type EventBinding struct{ /* unexported */ }

func (b EventBinding) Contract() ir.RuntimeContractID
func (b EventBinding) Harness() ir.HarnessID
func (b EventBinding) NativeEventName() NativeEventName
func (b EventBinding) Axes() Axes
func (b EventBinding) Identities() []runtime.NativeIdentityField

// The Event itself: opaque, constructor-validated, no exported fields.
type Event struct{ /* unexported */ }

func (b EventBinding) NewEvent(identities []Identity) (Event, error)

func (e Event) Harness() ir.HarnessID
func (e Event) Contract() ir.RuntimeContractID
func (e Event) NativeEventName() NativeEventName
func (e Event) Axes() Axes
func (e Event) Semantic() runtime.EventSemantic
func (e Event) Identities() []Identity
func (e Event) Identity(kind runtime.NativeIdentityKind) (Identity, bool)
```

`Axes` is a flat struct of the eight existing `internal/runtime` enums. It is
**derived from the binding, never supplied by the caller**.

DECISION D1 — the constructor takes only identities. A signature of the form
`NewEvent(binding, nativeName, axes, identities)` requires the caller to
restate `nativeName` and `axes`, which the binding already fixes, and then
verifies the restatement. That is a tautological check across a wider API: the
frontend's only source for those values is the binding itself, so the check can
only ever catch a frontend that deliberately lied. Narrow the constructor;
make the illegal state unrepresentable rather than detected.

REJECTED ALTERNATIVE — `Event` as a plain struct with exported fields and a
separate `Verify()`. Rejected: an unverified `Event` becomes representable, and
every downstream consumer must then defensively re-verify or trust.

### 4.2 L2 invariants (the verifier)

Enforced in the constructor, not in a separate pass:

- Axes equal the pinned mapping's axes for `(contract, event)`. No drift.
- Every identity's `nativeName` is declared by the mapping; every `required`
  declared identity is present; unknown identities are rejected.
- Identity values are bounded (`identityValueMaxBytes`) and non-empty.
- Pasture actors, assignments, `JournalID`s, revisions, review evidence, and
  publication evidence are **unrepresentable** — there is no field for them.

### 4.3 L2 -> L3 — ONE lowering pass

DECISION D2 — a single entry point that dispatches on semantic internally.

```go
// The whole middle-end. Callers never choose an operation, and never choose a
// per-semantic entry point either.
func Lower(ctx context.Context, deps Deps, event Event) (Outcome, error)
```

REJECTED ALTERNATIVE — per-semantic exported entry points
(`LowerObservation`, `LowerGate`, `LowerHumanResponse`). Rejected because it
reintroduces the exact defect being removed: the caller selects the operation.
Moving that selection from a `PASTURE_ADAPTER_*` env var into a Go call site
narrows the blast radius but does not change the shape. The `Event` already
carries `Semantic()`, verified against the pinned contract; dispatch belongs
inside the pass that owns the semantic -> operation table.

`Deps` carries injected collaborators only — a narrow tracker interface, a
clock, and a logger. No global state, no env reads.

`Outcome` is a closed sum over what the middle-end decided:

```go
type OutcomeKind uint8
const (
    OutcomeRecorded OutcomeKind = iota + 1 // observation persisted, no response
    OutcomeAllow                            // gate consulted, proceed
    OutcomeDeny                             // gate consulted, block
    OutcomeMutate                           // gate consulted, payload amended
)
```

Unimplemented semantics return an actionable `UnsupportedSemanticError` and
perform no writes. That is legalization: an explicit, named refusal, never a
silent no-op.

### 4.4 L2 + Outcome -> L1 — the response emitter (the missing half)

An observation is fire-and-forget. A *gate consultation* is not: the host is
blocked awaiting a result, and the result must be encoded in that surface's
native dialect. The encodings are genuinely different per `runtime.HookSurface`:

| Surface | Deny encoding | Mutation encoding | Failure encoding |
|---|---|---|---|
| `SurfaceClaudeCommandJSON` | exit 2 | stdout JSON | report-and-continue |
| `SurfaceCodexStrictCommandJSON` | exit 2, strict | stdout JSON, strict | strict-hook-failure |
| `SurfaceOpenCodeNamedOutput` | throw | mutate output object | throw, fail-fast |
| `SurfaceOpenCodeCatchAllSSE` | n/a (observe-only) | n/a | observe-only |

```go
// Encodes an Outcome into one native surface's response dialect.
// Selected by event.Axes().Surface — never by the caller.
type Responder interface {
    Surface() runtime.HookSurface
    Respond(w io.Writer, event Event, outcome Outcome) (ExitCode, error)
}
```

DECISION D3 — the responder is selected from the Event's verified surface, so
a Claude event can never be answered in Codex's dialect. The `Failure`,
`Mutation`, `Blocking`, and `StopLoop` axes already in the pinned table are the
responder's input; this is precisely what that table was built for and is
currently unused.

M1 ships only the degenerate responder (observation: write nothing, exit 0),
but the interface is defined at M0 so M5 is an addition, not a redesign.

### 4.5 L3/L4 — reuse, do not rebuild

`internal/handlers.AdapterOperation` and its DTOs are a serviceable L3
instruction set, and `EpochService` is L4. Neither is redesigned by this work.
What changes is *who selects the operation*: the `Lower` pass, from verified
IR — never a caller, an env var, or a generated script.

### 4.6 Process boundary

```
pasture hook lifecycle --harness <id> [--event <native-name>]   # payload on stdin
```

Reads at most `MaxNativePayloadBytes`. Validates flags and parses the payload
*before* opening any store, so a malformed invocation cannot create a database.
Emits no storage identities (`ActivityID`, `JournalID`, row ids) to stdout.

`--event` is cross-checked against the payload when the payload names the event
and both are present; disagreement is a hard error, because it means a hook is
misregistered.

DECISION D4 — `--harness` is required and typed. There is no default and no
autodetection: guessing the harness from payload shape is exactly the kind of
inference that produces silent misclassification.

## 5. Information flow (M1, concrete)

```
Claude fires SessionStart
  -> hooks.json runs: pasture hook lifecycle --harness claude-code --event SessionStart
  -> stdin: {"hook_event_name":"SessionStart","session_id":"abc","source":"startup"}
  -> claude.Frontend.Parse
       strict-decode, reject unknown fields
       resolve ClaudeEventSessionStart from the pinned catalog
       BindEvent(runtime.ClaudeCode2_1_210Lifecycle(), ClaudeEventSessionStart)
       extract declared identity session_id="abc"
       binding.NewEvent([session_id]) -> Event{Semantic: Observation}
  -> Lower(ctx, deps, event)
       dispatch: Observation
       derive deterministic identity from (contract, native name, identities)
       tracker.RecordActivity + RecordEvent
  -> Outcome{Kind: OutcomeRecorded}
  -> Responder(SurfaceClaudeCommandJSON).Respond -> exit 0, no stdout
```

Re-running the identical invocation replays: the derived identity is a pure
function of `(RuntimeContractID, NativeEventName, sorted identities)`, so the
second write is a no-op rather than a duplicate.

DECISION D5 — replay identity deliberately excludes wall-clock time. Two
genuinely distinct occurrences that share a session and event name collapse
into one record. That is the correct trade for M1: a hook that fires twice
because the host retried is far more common than two semantically distinct
`SessionStart`s in one session, and a duplicate-free ledger is the property
that matters. Events with finer identities (`tool_use_id`, `request_id`) do
not have this collapse. Revisit at M4 when turn/session context binding lands.

## 6. Known gap: actor coverage

`internal/tasks/well_known_registry.go` registers exactly 9 `HookHandler`
agents, named `pasture/automaton/hook/<name>`, covering the legacy Claude event
set. The pinned Claude profile has **30** events; Codex has 10; OpenCode has 42.

An observation must be attributed to an actor. M1's `SessionStart` is covered
by the existing registry. Everything beyond it is not.

DECISION D6 — do not expand the well-known registry to 82 entries. Derive the
hook-handler actor from `(harness, native event name)` and register lazily on
first use, keeping the 15 static well-known agents as-is. Expanding the static
registry per harness event makes it a second, redundant copy of the pinned
lifecycle tables, which will drift. Deferred to M2, where it first bites; M1
uses the existing registered `SessionStart` agent.

## 7. Revised milestone ladder

The original ladder omitted the emitter half. Revised:

| # | Milestone | Adds |
|---|---|---|
| M0 | Contract freeze | `Event`, `EventBinding`, verifier, `Frontend`, `Lower` signature, `Outcome`, `Responder` |
| M1 | Claude observation spine | Claude frontend, observation lowering, degenerate responder, CLI |
| M2 | OpenCode frontend + differential equivalence | second frontend; proves the waist is target-agnostic; lazy actor derivation |
| M3 | Codex frontend | third frontend; strict-mode failure semantics |
| M4 | Context binding | session -> epoch/assignment/actor correlation |
| M5 | Gate consultation | blocking lowering + real per-surface responders |
| M6 | Explicit human response | request-id correlated human decisions |
| M7 | Escape hatch | versioned raw-payload entry decoding into the same IR |
| M8 | Retire drift | delete Python translators, `PASTURE_ADAPTER_*`, `__adapter invoke` |

M2 is deliberately second: OpenCode's surface is structurally different from
Claude's (named handlers with output-object mutation, plus a catch-all SSE
stream). Retiring the generalization risk early is worth more than a third
Claude-shaped harness. The differential test — same logical occurrence from two
harnesses lowering to equivalent IR — is the only test that actually proves a
waist exists, and it is unwritable against the current design.

## 8. Testing posture

Deferred by explicit user direction. M0-M3 carry compile-time contract
enforcement plus one built-binary spine test per milestone. Systematic
per-package unit coverage is a follow-up. The differential equivalence test at
M2 is NOT deferred: it is the load-bearing correctness argument.

## 9. Acceptance criteria

- GIVEN a caller with any environment, WHEN a lifecycle event is processed,
  THEN the semantic operation is selected only by `Lower` from verified IR,
  AND SHOULD NOT be influenced by any env var, argv value, or generated script.
- GIVEN a generated trampoline, WHEN inspected, THEN it forwards bytes and an
  event name only, AND SHOULD NOT contain branching on Pasture semantics.
- GIVEN an `Event`, WHEN constructed, THEN its axes equal the pinned contract's
  axes, AND SHOULD NOT be constructible with caller-supplied axes.
- GIVEN a Pasture actor, assignment, JournalID, or revision, WHEN attempting to
  place it in an `Event`, THEN no field exists to hold it.
- GIVEN an unimplemented semantic, WHEN lowered, THEN an actionable named error
  is returned and nothing is written, AND SHOULD NOT silently succeed.
- GIVEN the same native occurrence delivered twice, WHEN lowered, THEN exactly
  one activity and one task event exist.
- GIVEN a Claude event, WHEN a response is emitted, THEN the encoding is chosen
  from the event's verified surface, AND SHOULD NOT be chosen by the caller.
