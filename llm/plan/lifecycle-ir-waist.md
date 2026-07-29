---
references:
  request: aura-plugins-s43qq
  impl_plan: aura-plugins-sgxp6
  supersedes: aura-plugins-42tgj
  reviews: aura-plugins-dkuiu, aura-plugins-4yydp, aura-plugins-b627b
  authority: llm/research/hooks-ir-compilers-architecture-lessons.md
---

# PROPOSAL-2: Canonical lifecycle IR waist

Supersedes PROPOSAL-1. All three review axes voted REVISE and converged, from
three different directions, on one defect: **PROPOSAL-1 copied the pinned target
table into the IR and called the result a waist.** It was not narrow, so the
test that was supposed to prove it existed could not be written.

| Axis | Route to the same defect |
|---|---|
| A | `Responder` dispatch keyed on surface alone is axis-incomplete |
| B | Differential equivalence unwritable; naive comparison would also be *wrong* |
| C | `Order`/`Reconciliation`/`StopLoop` have no L2 consumer |

## 0. Provenance of this plan

Authority: `llm/research/hooks-ir-compilers-architecture-lessons.md`. That
document is normative; this one applies it. Axis A correctly objected that
PROPOSAL-1's traceability table laundered derivation as citation, so the table
now separates the two.

| This plan | Research | Relationship |
|---|---|---|
| §2 hourglass waist | §2 | **stated** — narrow waist, N+M not N×M |
| §3 Go frontend is not "JSON as API" | §13.1 | **stated** — resolved by the user |
| §4.3 single `Lower`, middle-end owns selection | §6 | **stated** — frontends never emit target ops |
| §4.2 constructor-enforced invariants | §8 | **stated** — verify at pass boundaries |
| §4.4 `UnsupportedSemanticError` | §8 | **stated** — legalization failure is first-class |
| §7 no IR serialization yet | §9 | **stated** — in-memory type is the API |
| M2 differential equivalence | §11 | **stated** — the test that proves a waist |
| §4.1 `Semantics`/`Origin` split | §7 | **derived** — multi-level lowering implies the L2 dialect drops target detail; the research does not name this split |
| §4.1 type erasure at `BindEvent` | §7 | **derived** — forced by `LifecycleContract[E]` being generic |
| §5 dedup scope from declared identities | §8 | **derived** — a verifier-style invariant, not stated |
| §6 response encoding deferred | §2 | **derived** — the backend half is implied; its design is not given |

## 1. What changed from PROPOSAL-1

1. `Axes` — the flat eight-enum struct — **is deleted**.
2. `Event` splits into `Semantics` (the waist) and `Origin` (target coordinates).
3. `Responder` is **withdrawn from M0** and deferred to M5.
4. Replay identity is derived from declared identity scope, not fixed.
5. `Deps` is concretely declared.
6. Observation lowers **L2 → L4 directly**; only gates and human responses pass
   through the L3 protocol dialect.

## 2. The waist, correctly narrow

The test that proves a waist exists is: the *same logical occurrence* arriving
from two different harnesses must lower to *equivalent* IR. Working out what
"equivalent" means is what determines the waist. Concretely:

```
                      Claude PreToolUse      OpenCode tool.execute.before
  semantic            gate-consultation      gate-consultation        SAME
  blocking            blocking               blocking                 SAME
  identity kinds      session, tool-call     session, tool-call       SAME
  ------------------------------------------------------------------------
  surface             claude-command-json    opencode-named-output    DIFFERS
  mutation            input                  output-object            DIFFERS
  order               concurrent-native      sequential-load          DIFFERS
  reconciliation      host-native            sequential-mutation      DIFFERS
  failure             exit-2-blocks          throw-fail-fast          DIFFERS
```

Six of PROPOSAL-1's eight axes differ, and differ **correctly**. They describe
how to speak back to one specific host. They are target description, and per
research §7 the L2 dialect is precisely where target detail is dropped.

```go
// THE WAIST. Target-agnostic. Nothing here names a harness.
type Semantics struct {
    Semantic   runtime.EventSemantic   // observation | gate | human-response
    Blocking   runtime.BlockingMode    // does the host await a result
    Identities []Identity              // typed kind + value
}

// Target coordinates. Three fields, not nine.
type Origin struct {
    Harness         ir.HarnessID
    Contract        ir.RuntimeContractID
    NativeEventName NativeEventName
}
```

DECISION D8 — `Origin` carries **only** the coordinates needed to look the
event up again. Surface, mutation, order, reconciliation, failure and stop-loop
are *not stored*: they are recoverable from the pinned table via
`(Contract, NativeEventName)`. Copying them into the IR would create a second
copy of the pinned table that can drift from the first. This answers axis C's
"no L2 consumer" blocker completely — those axes have a consumer, but it is the
response backend at M5, and it should read the table rather than a stale copy.

`Lower` reads only `Semantics`. The response backend reads `Origin` and looks
up the rest. `Lower` must never touch `Origin`; that is the invariant that keeps
the middle-end target-agnostic, and it is mechanically checkable by review.

## 3. Level traversal is not uniform

Axis C correctly found PROPOSAL-1 internally inconsistent: it drew a linear
`L2 → L3 → L4` pipeline, but no observation operation exists in
`handlers.AdapterOperation`, and the prototype writes straight to the tracker.

The resolution is not to invent an observation operation. It is to state that
**not every semantic traverses every level** — normal in a multi-level compiler,
where some IR operations lower directly to machine operations while others pass
through an intermediate dialect.

```
  observation      L2 ─────────────────────────> L4 effects
                       (record activity + event)

  gate             L2 ──> L3 protocol op ──────> L4 effects ──> response
  human response   L2 ──> L3 protocol op ──────> L4 effects ──> response
```

L3 is the *protocol* dialect — `StartReview`, `RecordPlanUAT`, `Land`. An
observation is not a protocol transition. A host reporting that a session
started is evidence, not authority to advance a phase (research §13.3). Forcing
observations through L3 would manufacture exactly the authority this work
exists to remove.

Consequence: M0–M3 legitimately do not touch `AdapterOperation` at all.

## 4. Declared surface (frozen at M0)

### 4.1 Construction

```go
func BindEvent[E comparable](c runtime.LifecycleContract[E], event E) (EventBinding, error)

type EventBinding struct{ /* unexported */ }
func (b EventBinding) Origin() Origin
func (b EventBinding) DeclaredIdentities() []runtime.NativeIdentityField
func (b EventBinding) NewEvent(identities []Identity) (Event, error)   // D1

type Identity struct{ /* unexported */ }
func NewIdentity(kind runtime.NativeIdentityKind, nativeName, value string) (Identity, error)

type Event struct{ /* unexported */ }
func (e Event) Semantics() Semantics
func (e Event) Origin() Origin
```

DECISION D1 (unchanged; confirmed by all three axes) — the constructor takes
only identities. `Origin` and `Semantics` are derived from the binding. A
constructor that accepts caller-supplied axes and then verifies them is a
tautological check across a wider API. The prototype currently uses the wider
four-argument form and **must be narrowed**.

Axis C's scope note is accepted: `EventBinding` exposes only identity
declarations, origin, and event construction — nothing else.

### 4.2 Invariants (constructor-enforced, research §8)

- Every identity's `nativeName` is declared by the pinned mapping.
- Every `required` declared identity is present.
- Unknown identities rejected; values bounded and non-empty.
- Pasture actors, assignments, `JournalID`s, revisions and evidence have **no
  field to occupy**.

### 4.3 Equivalence (makes the M2 test writable)

```go
// Target-agnostic equivalence. Compares Semantics only; Origin is ignored by
// construction. Identity KINDS are compared, values are not: two harnesses
// report the same logical occurrence with different id strings.
func (s Semantics) EquivalentTo(other Semantics) bool

// Stable canonical rendering for golden-IR tests and diffing.
func (s Semantics) CanonicalKey() string
```

This is the load-bearing correctness argument of the whole design, and it was
unwritable in PROPOSAL-1. It is writable now precisely because the waist got
narrower.

### 4.4 Lowering

```go
func Lower(ctx context.Context, deps Deps, event Event) (Outcome, error)

type Deps struct {
    Recorder ObservationRecorder
    Actors   ActorResolver
    Clock    func() time.Time
    Logger   *slog.Logger
}

// Consumer-owned narrow interface. NOT protocol.TaskTracker wholesale.
type ObservationRecorder interface {
    RecordActivity(ctx context.Context, a ActivityRecord) (provenance.ActivityID, error)
    RecordEvent(ctx context.Context, e EventRecord) error
}

// Fails closed. An event with no registered actor is an actionable error,
// never an invented actor.
type ActorResolver interface {
    ResolveHookActor(ctx context.Context, o Origin) (provenance.AgentID, error)
}

type OutcomeKind uint8
const (
    OutcomeRecorded OutcomeKind = iota + 1
    OutcomeAllow
    OutcomeDeny
    OutcomeMutate
)
```

`Lower` is the **only** exported lowering symbol. Per-semantic helpers are
unexported. Axis A is right that research §6 mandates middle-end ownership
rather than one Go symbol specifically — but an exported per-semantic entry
point lets a caller pick, and that is the defect being removed.

Unimplemented semantics return `UnsupportedSemanticError`, write nothing, and
name the milestone that will support them.

## 5. Replay identity

DECISION D5 (replaces PROPOSAL-1's D5, which axis A correctly blocked) —
PROPOSAL-1 generalized from `SessionStart`, where a session-scoped identity does
distinguish occurrences, to all events. `FileChanged` declares only `session_id`,
so repeated file changes in one session would have silently collapsed into one
record. `PermissionDenied`, `Notification`, `MessageDisplay`, `CwdChanged` and
`InstructionsLoaded` share that shape.

Dedup scope is a pure function of the mapping's declared identities:

| Declared identity kinds | Scope | Key |
|---|---|---|
| Any of Request, ToolCall, Message | `DedupByIdentity` | contract + native name + identity values |
| Only Session and/or Agent | `DedupByPayload` | contract + native name + digest of canonical payload |

Under `DedupByPayload` a byte-identical redelivery collapses — the retry case
that motivated dedup — while two different file changes carry different paths
and so different keys. The residual false-collapse is two byte-identical
payloads that are genuinely distinct; that is now a narrow, named case rather
than a silent universal one.

Wall-clock time stays out of the key. Where declared identity cannot
distinguish occurrences, the design must not silently collapse them: silent
loss is worse than visible duplication.

## 6. Response encoding — deferred to M5, with constraints

PROPOSAL-1 froze a `Responder` interface at M0. Axis B showed it could not model
OpenCode's output-object mutation or throw semantics, and axis C called it
premature. Both are right: I argued early definition avoids an M5 redesign, but
an interface I cannot yet specify correctly guarantees that redesign.

`Responder` is withdrawn. `Outcome` is frozen at M0 because `Lower`'s signature
requires it. Two constraints are recorded now so M5 is not designed blind:

**C1 — one canonical encoding.** A single process-boundary response form, with
each harness trampoline applying a fixed mechanical mapping to its native
dialect. N mechanical trampolines beat N runtime responders and keep trampolines
trivial. Per-surface knowledge is emitted **statically** by the generator from
the pinned table, not selected at runtime.

**C2 — exit-code collision, newly found.** `AGENTS.md` maps process exit code 2
to `CategoryConnection`, but exit 2 is exactly how Claude and Codex hooks signal
*deny*. The lifecycle command cannot reuse the general CLI exit-code contract
without conflating a connection failure with a deliberate denial. M5 must
resolve this explicitly. Recorded now so it is not discovered mid-implementation.

## 7. Serialization

DECISION D7 (unchanged) — the CLI hosts the pipeline in-process; the IR is an
in-memory Go value with no serialized form (research §9). No boundary exists, so
a wire format would be an unforced versioning commitment. If a daemon split is
later measured to be necessary, the IR gains a serialized form at that boundary
only, and the in-memory type stays authoritative.

## 8. Process boundary

```
pasture hook lifecycle --harness <id> [--event <native-name>]    # payload on stdin
```

Bounded read (`MaxNativePayloadBytes`). Parse and validate **before** opening any
store. Emit no storage identities. `--harness` is required and typed — no
autodetection, because inferring the harness from payload shape is the kind of
guess that produces silent misclassification. `--event` is cross-checked against
the payload when both are present; disagreement is a hard error, since it means
a hook is misregistered.

## 9. Milestones

Axis C called the nine-milestone ladder inflated. M0 and M1 are merged (they were
already one slice), and everything past M3 is marked directional rather than
committed scope.

| # | Milestone | Status |
|---|---|---|
| M1 | Contract freeze + Claude observation spine | committed |
| M2 | OpenCode frontend + **differential equivalence** + lazy actor derivation | committed |
| M3 | Codex frontend, strict-mode failure semantics | committed |
| M4+ | Context binding, gates, human response, escape hatch, retire drift | directional |

M2 stays ahead of Codex: OpenCode's surface is structurally different, and
retiring the generalization risk early is worth more than a third Claude-shaped
harness.

## 10. Actor coverage

`internal/tasks/well_known_registry.go` registers 9 `HookHandler` agents; the
pinned Claude profile has 30 events, Codex 10, OpenCode 42.

DECISION D6 (unchanged, now with a fail-closed rule) — do not expand the static
registry to 82 entries; that would be a second copy of the pinned tables and
would drift. Derive the hook actor from `(harness, native event name)` and
register lazily, at M2 where it first bites. M1 uses the already-registered
`SessionStart` agent and **fails closed** with an actionable error for any event
lacking a registered actor.

## 11. Acceptance criteria

- GIVEN any environment, WHEN an event is processed, THEN the operation is
  selected only by `Lower` from verified IR, AND SHOULD NOT be influenced by any
  env var, argv value, or generated script.
- GIVEN `Lower`, WHEN it executes, THEN it reads only `Semantics`, AND SHOULD
  NOT read `Origin`.
- GIVEN the same logical occurrence from Claude and from OpenCode, WHEN both are
  lowered, THEN their `Semantics` are equivalent, AND SHOULD NOT be required to
  share `Origin`.
- GIVEN an `Event`, WHEN constructed, THEN axes are derived from the pinned
  contract, AND SHOULD NOT be caller-supplied.
- GIVEN an actor, assignment, JournalID or revision, WHEN placing it in an
  `Event`, THEN no field exists to hold it.
- GIVEN an event whose declared identities cannot distinguish occurrences, WHEN
  two different occurrences are lowered, THEN two records exist, AND SHOULD NOT
  collapse into one.
- GIVEN an unimplemented semantic, WHEN lowered, THEN an actionable named error
  is returned naming the milestone, and nothing is written.
- GIVEN an event with no registered actor, WHEN lowered, THEN it fails closed,
  AND SHOULD NOT invent an actor.
