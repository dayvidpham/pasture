---
references:
  request: aura-plugins-s43qq
  impl_plan: aura-plugins-sgxp6
  supersedes: aura-plugins-9ih8s
  reviews_round1: aura-plugins-dkuiu, aura-plugins-4yydp, aura-plugins-b627b
  reviews_round2: aura-plugins-1q0hn, aura-plugins-hsth1, aura-plugins-4kb2i
  authority: llm/research/hooks-ir-compilers-architecture-lessons.md
---

# PROPOSAL-3: Canonical lifecycle IR waist

Supersedes PROPOSAL-2. Round 2 returned REVISE on all three axes, with three
axes independently reporting the same four defects. That convergence is the
useful signal: the *architecture* stopped being contested after round 1, and
round 2 found only places where the declared contract could not actually be
implemented. This revision closes those.

| Defect | Reported by |
|---|---|
| D8 reverse lookup does not exist and was deliberately excluded | A (blocker), C (important) |
| D5 payload dedup unimplementable — no payload reaches the IR | A (blocker), C (blocker) |
| `Lower` forbidden to read `Origin`, yet must pass it to `ActorResolver` | A, B, C (all blocker) |
| `Outcome` declared frozen while incomplete | B (blocker), C (blocker), A (important) |

## 0. Provenance

Authority: `llm/research/hooks-ir-compilers-architecture-lessons.md`. Axis A
correctly objected again that two rows still laundered derivation as citation.

| This plan | Research | Relationship |
|---|---|---|
| §2 hourglass waist | §2 | **stated** |
| §3 Go frontend is not "JSON as API" | §13.1 | **stated** |
| middle-end owns operation selection | §6 | **stated** |
| legalization failure is first-class | §8 | **stated** |
| no IR serialization yet | §9 | **stated** |
| differential equivalence proves a waist | §11 | **stated** |
| `Semantics`/`Origin` split | §7 | **derived** |
| type erasure at `BindEvent` | §7 | **derived** |
| *one exported* `Lower` symbol | §6 | **derived** — research requires middle-end ownership, not one symbol |
| constructor-enforced invariants | §8 | **derived** — research requires verification at pass boundaries, not a specific mechanism |
| replay key from payload digest | §8 | **derived** |

## 1. The waist

The equivalence that the M2 test must prove is what fixes the waist's width.
Claude `PreToolUse` against OpenCode `tool.execute.before`:

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

**Five** of the eight axes differ (A and C both corrected PROPOSAL-2's "six";
`stopLoop` matches here). The five that differ describe how to speak back to one
specific host — target description, which research §7 says the L2 dialect drops.

```go
// THE WAIST. Target-agnostic: nothing here names a harness or a wire format.
type Semantics struct {
    Semantic   runtime.EventSemantic
    Blocking   runtime.BlockingMode
    Identities []SemanticIdentity   // sorted; see §4.3
}

// Occurrence identity, stripped of native field naming (axis C).
type SemanticIdentity struct {
    Kind  runtime.NativeIdentityKind
    Value string
}
```

## 2. Origin, and the death of D8

PROPOSAL-2 said the response backend could recover the five target axes by
looking them up from `(Contract, NativeEventName)`. Axis A found this is
impossible and axis C independently found the same: `LifecycleContract.Mapping`
is generic over `E`, `Origin` erases `E`, and `internal/runtime/lifecycle.go`
**deliberately has no native-name lookup** — its doc comment says so explicitly.

D8 is withdrawn. Neither offered fix is taken wholesale: adding a reverse lookup
would re-open the string-keyed lookup that `runtime` was designed to forbid.

DECISION D9 — `Event` retains the immutable `LifecycleEventMapping` it was
bound to, unexported, exposed only through a backend-facing accessor.

```go
type Origin struct {
    Contract        ir.RuntimeContractID
    NativeEventName NativeEventName
    PayloadDigest   Digest      // §3
    // unexported: mapping runtime.LifecycleEventMapping
}

func (o Origin) Harness() ir.HarnessID   // derived; no stored field (axis C minor)

// Backend-only. Lower must not call this; see §5.
func (o Origin) TargetBehaviour() runtime.LifecycleEventMapping
```

This preserves D8's actual intent. The anti-drift worry was a *copy that can
diverge from the table*; a value read from the immutable table at `BindEvent`
and never mutated cannot diverge from it. And the impossible reverse lookup
disappears rather than being built.

## 3. Replay identity — one rule

PROPOSAL-2's two-branch `DedupScope` was unimplementable: `DedupByPayload`
needs the payload, and no payload reaches the IR. The Claude frontend discards
`file_path`, so the field distinguishing two `FileChanged` occurrences was gone.
Axis C also noted the table omitted Codex's `IdentityTurn`.

Rather than complete the table, delete it. The frontend digests the raw native
payload it received and carries the digest as origin provenance:

```
replay key = (Contract, NativeEventName, PayloadDigest)
```

DECISION D5 (final) — one rule, total by construction. Identity values live
inside the payload, so identity-based dedup is subsumed; the `IdentityTurn` gap
and every future one closes automatically. A byte-identical redelivery
collapses, which is the retry case dedup exists for. Two different file changes
carry different paths, therefore different digests, therefore different records.

`Digest` is a bounded, fixed-width value (SHA-256) computed over the exact bytes
read, before any parsing, so it is well-defined even for payloads that fail to
parse. Wall-clock time is not in the key.

**Named residual:** two genuinely distinct occurrences with byte-identical
payloads collapse into one record. This is narrow and documented, and replaces
PROPOSAL-2's silent universal collapse. The acceptance criterion in §10 is
worded to match this, rather than claiming a guarantee the design cannot make
(axis A's contradiction finding).

## 4. Declared surface

### 4.1 Frontend (restored — axis B found it dropped)

```go
type Frontend interface {
    Harness() ir.HarnessID
    Parse(payload []byte, requested NativeEventName) (Event, error)
}
```

Pure: no effects, no store, no clock. Golden-IR tests need no host and no
database. `MaxNativePayloadBytes` bounds the read at the process boundary.

### 4.2 Construction

```go
func BindEvent[E comparable](c runtime.LifecycleContract[E], event E) (EventBinding, error)

type EventBinding struct{ /* unexported */ }
func (b EventBinding) DeclaredIdentities() []runtime.NativeIdentityField
func (b EventBinding) NewEvent(digest Digest, identities []Identity) (Event, error)

type Event struct{ /* unexported */ }
func (e Event) Semantics() Semantics
func (e Event) Origin() Origin
```

D1 (confirmed by all three axes, twice) — the constructor derives `Semantics`
and the mapping from the binding. It accepts only what the binding cannot know:
the payload digest and the extracted identity values.

### 4.3 Equivalence — honest about what it proves

Axis B found the relation too coarse: Claude `UserPromptSubmit` and `Stop` both
reduce to (gate-consultation, blocking, [session]) and would compare equivalent
despite being unrelated events. That is correct, and it means `EquivalentTo`
alone cannot be the M2 test.

```go
// Coarse SHAPE relation over the waist. Necessary, NOT sufficient.
func (s Semantics) EquivalentTo(other Semantics) bool

// Deterministic rendering. Identities sorted by (Kind, Value); duplicates of
// the same Kind retained in sorted order; fields joined with a reserved
// separator that cannot occur in an identity value.
func (s Semantics) CanonicalKey() string
```

The M2 differential test is therefore specified as three assertions, not one:

1. each frontend produced the **expected native event** (`Origin.NativeEventName`
   is `PreToolUse` on one side, `tool.execute.before` on the other);
2. each side carries the **exact expected identity values**;
3. the two `Semantics` are `EquivalentTo`.

Without (1) and (2) the test would pass even if a frontend produced the wrong
event, which is precisely the bug class it exists to catch.

### 4.4 Lowering

```go
func Lower(ctx context.Context, deps Deps, event Event) (Outcome, error)

type Deps struct {
    Recorder ObservationRecorder   // required
    Actors   ActorResolver         // required
    Clock    func() time.Time      // required; no time.Now fallback
    Logger   *slog.Logger          // required; no slog.Default fallback
}
```

All fields required and validated; nil is an actionable error, never a silent
default. Axis B is right that fallbacks to `time.Now` or `slog.Default`
reintroduce the hidden global state the injection exists to remove.

`Clock`'s only consumer is the observed-at timestamp on the recorded activity.
It is deliberately *not* in the replay key (§3), so a fake clock cannot change
dedup behaviour.

```go
// Consumer-owned, narrow. Not protocol.TaskTracker wholesale.
type ObservationRecorder interface {
    RecordObservation(ctx context.Context, o ObservationRecord) (RecordOutcome, error)
}

type ObservationRecord struct {
    Actor      provenance.AgentID
    Actee      Origin           // what was observed
    Semantics  Semantics
    ObservedAt time.Time
    ReplayKey  string           // §3
}

type RecordOutcome uint8
const (
    RecordCreated RecordOutcome = iota + 1  // new activity + event written
    RecordReplayed                          // replay key already present; no write
)

// Fails closed. Must verify the resolved ID is non-zero and registered.
type ActorResolver interface {
    ResolveHookActor(ctx context.Context, o Origin) (provenance.AgentID, error)
}
```

Axis B's finding that `ActivityRecord`/`EventRecord` were undefined is
addressed by collapsing them: the activity and the task event are written
together or not at all, so exposing two calls invited a partial write. One call,
one transaction, one typed outcome that distinguishes a write from a replay.

### 4.5 Outcome — provisional by declaration

```go
type Outcome struct {
    Kind   OutcomeKind
    Record RecordOutcome   // meaningful when Kind == OutcomeRecorded
}

type OutcomeKind uint8
const (
    OutcomeRecorded OutcomeKind = iota + 1
)
```

DECISION D10 — M1–M3 declare **only** `OutcomeRecorded`. Axis B and C both
found PROPOSAL-2 repeating, one level smaller, the same error that got
`Responder` withdrawn: freezing `OutcomeAllow`/`OutcomeDeny`/`OutcomeMutate`
with no payloads and no consumers. `OutcomeMutate` in particular cannot be
specified while the IR carries no mutation input.

`Outcome` is a struct rather than a bare enum specifically so M5 can add
variants and payloads without breaking callers, and it is declared
**provisional**: the observation path is frozen, the gate path is not yet
designed. Saying so is more honest than implying a stability the design has not
earned.

## 5. What keeps the middle-end target-agnostic

All three axes found the contradiction: PROPOSAL-2's criterion said `Lower`
reads only `Semantics`, but actor resolution and the replay key both need
`Origin`. The criterion was wrong, not the design.

The property actually wanted is that **`Lower`'s behaviour does not vary by
harness**. Restated precisely and checkably:

- `Lower` MAY read `Semantics` and `Origin`'s coordinates.
- `Lower` MUST NOT call `Origin.TargetBehaviour()`. Those five axes govern how
  to speak back to a host; a middle-end that branches on them is not
  target-agnostic.
- `Deps` deliberately contains no lifecycle-table accessor, so harness-varying
  lookups can only happen behind `ActorResolver`. `Lower` itself contains no
  harness branch.

## 6. Level traversal is not uniform

```
  observation      L2 ──────────────────────> L4 effects   (terminal pass)
  gate             L2 ──> L3 protocol op ───> L4 ──> response
  human response   L2 ──> L3 protocol op ───> L4 ──> response
```

L3 is the protocol dialect (`StartReview`, `RecordPlanUAT`, `Land`). An
observation is not a protocol transition: a host reporting that a session
started is *evidence*, not authority to advance a phase (research §13.3).
Axis A and C both accepted this as a genuine resolution when named as a
**terminal observation-effect pass**, which is how §6 now labels it.
Consequence: M1–M3 never touch `AdapterOperation`.

## 7. Blocking events are refused until M5

Axis A: since no response encoding exists before M5, a blocking event processed
at M1–M3 would leave the host awaiting a result it never receives.

DECISION D11 — M1–M3 refuse any event whose `Semantics.Blocking` is not
`NonBlocking`, with an actionable error naming M5. Refusing loudly is the
legalization behaviour research §8 requires; hanging or silently allowing is
the failure mode this work exists to remove.

## 8. Response encoding — M5, with constraints

`Responder` stays withdrawn. Two constraints recorded so M5 is not designed blind:

**C1** — one canonical process-boundary response form; each harness trampoline
applies a fixed mechanical mapping to its native dialect. Per-surface knowledge
is emitted statically by the generator from the pinned table, not selected at
runtime.

**C2** — verified by axis B against `AGENTS.md` and
`internal/errors/errors.go:226-255`: exit code 2 means `CategoryConnection`,
but exit 2 is exactly how Claude and Codex hooks signal *deny*. M5 needs a
distinct typed lifecycle response disposition rather than reusing the general
CLI exit-code contract.

## 9. Milestones

| # | Milestone | Status |
|---|---|---|
| M1 | Contract freeze + Claude observation spine | committed |
| M2 | OpenCode frontend + differential equivalence + lazy actor derivation | committed |
| M3 | Codex frontend | committed |
| M4+ | Context binding, gates, human response, escape hatch, retire drift | directional |

M3 is the Codex **frontend only** — axis C found PROPOSAL-2 claiming strict
failure semantics at M3 while deferring all response encoding to M5.

Actor coverage (D6, unchanged): the static registry has 9 `HookHandler` agents
against 30/10/42 pinned events. Do not expand it to 82 — that is a second copy
of the pinned tables and will drift. Derive lazily from `(harness, native name)`
at M2. M1 uses the registered `SessionStart` agent and fails closed otherwise.
`ActorResolver` must verify the resolved ID is non-zero and registered, and must
remain compatible with the existing `pasture/automaton/hook/<name>` naming.

## 10. Acceptance criteria

- GIVEN any environment, WHEN an event is processed, THEN the operation is
  selected only by `Lower` from verified IR, AND SHOULD NOT be influenced by any
  env var, argv value, or generated script.
- GIVEN `Lower`, WHEN it executes, THEN it never calls `Origin.TargetBehaviour()`
  and contains no harness branch.
- GIVEN the same logical occurrence from Claude and OpenCode, WHEN both are
  lowered, THEN each carries its own expected native event name and exact
  identity values, AND their `Semantics` are `EquivalentTo`.
- GIVEN an `Event`, WHEN constructed, THEN semantics and target behaviour are
  derived from the pinned contract, AND SHOULD NOT be caller-supplied.
- GIVEN an actor, assignment, JournalID or revision, WHEN placing it in an
  `Event`, THEN no field exists to hold it.
- GIVEN two occurrences with **different** native payloads, WHEN both are
  lowered, THEN two records exist, AND SHOULD NOT collapse.
- GIVEN the identical payload delivered twice, WHEN both are lowered, THEN one
  record exists and the second returns `RecordReplayed`.
- GIVEN a blocking event before M5, WHEN lowered, THEN it is refused with an
  actionable error naming M5, AND SHOULD NOT be silently allowed.
- GIVEN an event with no registered actor, WHEN lowered, THEN it fails closed,
  AND SHOULD NOT invent an actor.
- GIVEN a nil `Deps` field, WHEN `Lower` is called, THEN it fails actionably,
  AND SHOULD NOT fall back to `time.Now` or `slog.Default`.
