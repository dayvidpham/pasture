---
status: RATIFIED (with two ratification amendments)
proposal: PROPOSAL-10 — Progressive hook activation on the shared acceptance corpus
references:
  proposal: aura-plugins-6ljvd
  request: aura-plugins-s43qq
  urd: aura-plugins-hznvh
  uat: aura-plugins-q5ams
  supersedes: aura-plugins-p4e29 (PROPOSAL-9)
  authority: llm/research/hooks-ir-compilers-architecture-lessons.md
  superseded_plan: llm/plan/lifecycle-ir-waist.md (PROPOSAL-4)
  deferred_followups: aura-plugins-0si2b (corpus non-vacuity), aura-plugins-ub89r (retention)
  open_prerequisite: aura-plugins-9umx1 (P0-CAPTURE)
  open_risk: aura-plugins-cvlu6 (2s start-signal deadline)
  pinned_provenance: github.com/dayvidpham/provenance@v0.0.4-0.20260730015136-0976165224e9
---

# PROPOSAL-10: the ratified hook lifecycle architecture

This document is the repository-resident form of the ratified architecture. Until now
that architecture existed only as a Beads task body (`aura-plugins-6ljvd`: a description
plus seven "NORMATIVE CONTINUATION n/7" comments plus two ratification amendments), while
the only architecture document in the tree — `llm/plan/lifecycle-ir-waist.md` — was
**PROPOSAL-4**, superseded six proposals ago.

Standing authority is `llm/research/hooks-ir-compilers-architecture-lessons.md`. That
document is **not** superseded, and §4 of this document reconciles the ratified pipeline
against its §12 target, stage by stage.

**Convention used throughout.** *Stated* means the ratified plan says it in those terms
and the citation is to `bd show aura-plugins-6ljvd` section numbers. *Inferred* means this
document is drawing a conclusion the plan does not state. Repository claims cite
`path:line`.

---

## 0. Ratification status and what is normative

| | |
|---|---|
| Ratified | 2026-07-30 03:33, comment "RATIFICATION AMENDMENT + RATIFIED" |
| Gate applied | Explicit user rule: the three general reviewers gate; mirrored secondary-reviewer findings defer to follow-up |
| Review wave | Delta-A `aura-plugins-i3bhf` (REVISE, one blocker D1), Delta-B `aura-plugins-j7wuw` (REVISE, deferred) |
| Body self-description | The description text still reads "Design only. Not ratified." (§0). That sentence is **stale**; the labels carry `aura:p6-plan:s6-ratify` and the ratification comment is explicit. Read §0's first paragraph as provenance, not status. |

### 0.1 Amendment 1 — INV-1 totality domain (Delta-A blocker D1)

Reviewer `aura-plugins-i3bhf` measured PROPOSAL-10's own Go fences: the INV-1
classification table held 24 types against 193 declared exported types, leaving 169
unclassified — including all of `model/control.go` and all of `model/reader.go`. No class
could host a `PageRequest` or an `EpochControlService`, because they are not records.
A totality guard gated at S1 L2 would have arrived red against 50+ legitimate types.

**Adopted verbatim:** a **fifth class**, *non-journal value* — "carries no journal identity
of its own and is never resolved from or folded out of the journal" — expressed as a
**predicate, not a name list**. Covers queries, pages, cursors, bounds, inputs, typed
errors, typed ID views, enums, and service interfaces. §3.0's four additions, the
`DisclosureRecord` split, and the shape guard carry forward verbatim. S1 owns making the
predicate mechanical.

*Landed:* `internal/lifecycle/guard/classification.go:77` (`mechanicallyNonJournal`).

### 0.2 Amendment 2 — retention mode (post-ratification user decision)

User verbatim: *"Actually: we should just keep all bodies, for all objects. at a later
date, we can decide to truncate or filter uninteresting bodies. SQLite is great for the
MVP. just put the blob there."*

Supersedes the metadata-only decision §19.1 recorded as an M1 blocker. Consequences:

- **No S1R retention slice.** §19.1's per-option slice sets collapse to `S1 + S2`.
- `retention_encrypted_local.yaml` / `retention_external_cas.yaml` are not needed at M1;
  `retention_metadata_only.yaml` is replaced by a store-all corpus.
- **One rule changes.** §4.4's "T1 is exactly one write transaction per delivery" would
  otherwise hold the write lock for a 1 MiB body write on the file the engine needs for
  `start_slice` within 2 seconds. Amended: a delivery performs an **ordered pair** of write
  transactions — blob first, then the small occurrence commit referencing it. Blob-first is
  independently mandatory for crash safety: an orphan blob is reclaimable, a dangling
  reference is corruption. The write budget applies to the occurrence commit; the blob
  write is bounded separately by payload size.
- Deferred to `aura-plugins-ub89r`: truncation, filtering, age-tiering, external CAS, GC,
  redaction boundary.

*Landed:* `internal/lifecycle/receipt/journal.go:31-102` (`BlobStore`, `SQLiteBlobStore`,
`Reclaimable`), `internal/lifecycle/receipt/service.go:57-61` (blob-before-journal with the
crash-safety comment stating the ordering rationale).

### 0.3 Deferred at ratification

`aura-plugins-0si2b` — Delta-B's finding that §16's coverage evaluation and source-mutation
execution are *declared but not wired*. Risk stated openly on that task: **until fixed, the
anti-vacuity machinery does not prove non-vacuity.** This is a live, acknowledged hole in
the validation story.

---

## 1. The middle-end question, answered

> **Does PROPOSAL-10's versioned T2 interpretation replace the middle-end, or does it sit
> above an IR waist that was supposed to survive?**

**Neither framing is right, and the premise that PROPOSAL-10 is silent on the middle-end
is false.** PROPOSAL-10 keeps the middle-end, renames it, and **splits it across two
transaction planes**. The IR waist survives; it is no longer called a waist.

Concretely, and all *stated*:

| Research §12 concept | PROPOSAL-10 name | Where |
|---|---|---|
| Level-2 lifecycle IR (the waist) | `OccurrenceRecord` — "one canonical post-T1 occurrence" | §5.2 |
| Level-1 → Level-2 lowering | T1 receipt: `ReceiptService.Receive(CapturedDelivery) → EvidenceReceipt` | §5.2 |
| Operation selection ("in the middle-end, once", research §5) | `Interpreter.Interpret(InterpretationInput) → InterpretationDraft` emitting `TransitionCandidateDraft` | §8.2 |
| Legalization | `TransitionCandidateDraft` constructor validating against `protocol.PhaseSpecFor` | §8.2 |
| Authorization | `EpochControlService.CommitCandidate` under a scoped capability + phase CAS | §11.2 |
| Level-3 → Level-4 effect mapping | `internal/lifecycle/lowering/{canonicalize,effects}.go` | §18.1 (S3) |

The word "lowering" **does** appear in PROPOSAL-10, three times, all load-bearing:

- §18.1 S3-INTERPRETATION creates `internal/lifecycle/lowering/{canonicalize,effects}.go`
  (proposal §18.1 slice table).
- §16.4's compile-fail guard row names it as a target-neutral package: *"model, codebook,
  **lowering**, context, and control importing private host response or descriptor
  packages"*.
- §18.3's S3 L3 leaf reads *"resolver, state fold, codebook, enricher, canonical writer and
  readers; **delete old lowering**"*.

And the disposition of the deleted file is explicit — §18.5, committed-prototype table:

> | `internal/lifecycle/lower.go`, `lower_test.go`, `lower_internal_test.go` | **S3** | **replace** with pure T2 canonicalization and effect mapping |

**PROPOSAL-10 uses "delete" and "replace" as distinct words, deliberately.** In the same
table: `key.go` → *"**delete**; the static guard prevents a replacement replay key"*;
`backend.go` → *"**delete** the same-package host behaviour side door"*; `event.go` →
*"**replace** with the canonical model"*; `lower.go` → *"**replace** with pure T2
canonicalization and effect mapping"*.

### 1.1 So this is not a subject change — but the naming did drift

The P4 → P10 chain did **not** silently change subject. It changed vocabulary, and the
vocabulary change is what made the middle-end look absent:

- P4 said *waist*, *IR*, *Level 1/2/3/4*, *frontend*, *Lower*.
- P10 says *occurrence*, *envelope*, *T1/T2/T3*, *ingress capture*, *interpretation*.

P10 uses "IR" as a word-boundary token **twice** in 4,289 lines, "waist" only inside
inherited task titles, and never says "Level 1"/"Level 2" or "middle-end". A grep for the
compiler vocabulary returns nothing because the compiler vocabulary was dropped, not
because the stages were.

**This is a real cost and it is the direct cause of the `lower.go` incident** (§7). An
architecture whose stages are named only in a Beads comment, in a vocabulary that does not
match its own standing authority document, cannot be checked against that authority by
grep — and grep is what a worker actually runs.

*Inferred:* the vocabulary drift is worth correcting in the plan's own terms rather than
tolerated. §4's stage table is the correction: it restores the research doc's names as an
index into PROPOSAL-10's names.

### 1.2 What PROPOSAL-10 genuinely never addressed

Two things, and neither is the middle-end:

1. **The waist's economic justification is untested.** Research §2 justifies the narrow
   waist by `N + M` versus `N × M` integration cost, and §11 names *differential
   equivalence* — Claude `PreToolUse` and OpenCode `tool.execute.before` lowering to the
   same Level-2 IR — as the test that proves a waist is real. PROPOSAL-4 §9 made this its
   M2 gate. **PROPOSAL-10 has no differential-equivalence requirement at any milestone**,
   because it is a single-harness plan: §17 lists "cross-harness equivalence" and "a
   generic fourth harness" as later work, and the strings `codex` and `opencode` appear in
   the entire proposal exactly twice, neither substantively.
2. **The other two harnesses are still on the condemned path.** `internal/codegen/
   codex_manifest.go:39,54` still generates `.codex/hooks/pasture-lifecycle.py` through
   `renderPythonLifecycleAdapter`, still bound by `PASTURE_ADAPTER_OPERATION`
   (`internal/codegen/claude_hooks.go:19`). `internal/codegen/opencode_hooks.go:17` still
   generates `.opencode/plugins/pasture-lifecycle.ts`. Both are tracked
   (`git ls-files | rg pasture-lifecycle`). Research §6 names caller-selected operations as
   *worse than syntax-directed code generation*. PROPOSAL-10 disposes of this path only for
   Claude (§18.5, owner S2: *"delete when the first replacement event is Enabled"*), and is
   silent on Codex and OpenCode.

See §8 for whether these are conflicts or silences.

---

## 2. The ratified architecture

### 2.1 End users and the required result (§1)

The end users are humans directing and approving agentic work, agents receiving
phase-appropriate context, operators diagnosing lifecycle failures, and researchers tracing
generated artifacts through tools and agents back to human signals.

They must be able to answer, without timestamp inference or hidden SQL:

1. Which exact host deliveries reached Pasture, and in what local database order?
2. Which native IDs are *claims*, and which Pasture actors/tasks/assignments were
   independently *resolved*?
3. Which exact runtime, schema, codebook, interpreter, build, and context policy
   definitions produced each semantic record?
4. Which facts are asserted, unresolved, candidate, suppressed, rejected, or normatively
   committed?
5. Which explicit links connect a human signal to an agent action, tool call, worktree,
   artifact, and commit, in both directions?
6. Which context packet was planned, which delivery was attempted, and what did the host
   positively prove about delivery or acknowledgement?
7. Why is an event withheld from registration, and what proof is missing?

The simplest design — append native events plus typed labels — satisfies query 1 and fails
2 through 7. **Query 1 is exactly M1**, which ships first with a correspondingly thin
surface. This is the proportionality argument for everything below.

### 2.2 Engineering axes (§1.1)

| Axis | Binding |
|---|---|
| Parallelism | Separate hook processes and readers share one SQLite file. `BEGIN IMMEDIATE`, JournalID order, and transaction-local conditions decide races. |
| Distribution | MVP is multi-process local storage, not cross-machine consensus or total order. |
| Frequency | Every identifiable invocation appends; bytes, fields, effects, fanout, deadlines, and reads are statically bounded. |
| Reliability | T1 survives T2 failure; T2 cannot grant T3 authority; projections rebuild from the journal. |
| Cardinality | One occurrence has zero-to-many T2 batches; one batch has bounded facts, unresolved facts, links, and candidates. |
| Change rate | Host contracts, schemas, codebooks, interpreters, builds, policies, and execution cohorts evolve independently and have immutable identities. |
| Has-a | An occurrence has native claims and a capture disposition; an interpretation has exact definitions and outputs; a disclosure has a plan, attempts, and immutable outcomes. |
| Is-a | A candidate is evidence, not a transition; an observed path is not a commit; a host event is not authority; a projection is not truth; a snapshot is not a state. |

### 2.3 The pipeline (§2, with Amendment 2 applied)

```text
                     BUILD TIME
private hostcontract descriptor          (sole event/field/behaviour source)
       |
       +--> generated payload descriptors
       |
       +--> independent Enabled/Withheld activation manifest
                 |
                 +--> generated registration for Enabled events ONLY
                      + pasture-activation.json support report

                     RUN TIME
native bytes --> built `pasture hook lifecycle` process
                              |
                              +--> private bounded capture       (frontend)
                              |
                              +--> blob write                    (Amendment 2, first)
                              |
                              +--> T1 Journal.Apply              (caller deadline, P0-PIN)
                              |      slot: occurrence
                              |      one new host delivery = one new operation
                              |
                              +--> T2 Journal.Apply
                              |      slot: interpretation
                              |      fact / link / candidate / unresolved slots
                              |
                              +--> pure ContextProjector over committed state
                              |      plan -> attempt -> immutable result facts
                              |
                              +--> EpochControlService
                              |      issue / bind / revoke, then
                              |      capability + deadline + phase CAS -> T3 Apply
                              |
                              +--> mechanical contract-specific response

Provenance Journal.Apply / Journal.Facts ---------------- canonical truth
Pasture lifecycle/lineage/disclosure/phase projections --- rebuildable indexes
```

**One-substrate rule (§2).** The pinned Provenance journal is the only lifecycle semantic
write substrate. Pasture's typed application envelopes are `EffectEvidence` or existing
typed task/decision effects. The existing `pkg/protocol` phase machine is the **sole**
normative FSM and the **sole** holder of phase adjacency. No lifecycle SQL row, audit row,
DBOS checkpoint, cache, cursor, or native host identifier has independent semantic
identity.

### 2.4 Transaction boundaries (§2.1, amended)

- **Blob** (Amendment 2): written first, outside the occurrence transaction. Orphan blobs
  are reclaimable; dangling references are corruption.
- **T1** appends one occurrence header through one `Journal.Apply`. Capture failure after a
  trusted registration coordinate becomes bounded occurrence evidence.
- **T2** appends one interpretation header and all canonical sibling outputs through one
  `Journal.Apply`. **T1 is never rolled back by T2.**
- **Context** policy evaluation is pure. Plan, delivery attempt, and every result are
  separate `Journal.Apply` operations.
- **T3** checks current capability, scope, assignment, phase, and transaction time **inside
  the same `Journal.Apply`** that consumes the capability and commits control.
- **Response** cannot select an operation, policy, or authority.

### 2.5 INV-1 — the immutability invariant, total (§3.0 + Amendment 1)

> **INV-1.** Any type this design calls an *immutable snapshot* exposes no mutable field
> and no lifecycle status. Changing state is expressed as append-only facts plus a *derived
> projection* that carries the `SnapshotJournalID` it was folded at.
>
> **INV-1-T (totality).** Every exported type declared in the lifecycle model packages
> appears in exactly one of the **five** classes below. An unclassified or doubly-classified
> type is itself a guard failure, not a documentation gap.

| Class | Rule | Members |
|---|---|---|
| 1. **Immutable snapshot** — resolved by JournalID | no status field, no mutable field, no lifecycle enum | `DefinitionSnapshot`, `OccurrenceRecord`, `EnrichmentSnapshot`, `DisclosurePlan`, `DisclosureAttempt`, `DisclosurePlanRecord`, `DisclosureAttemptRecord` |
| 2. **Immutable fact** — append-only, content includes what it asserts | may carry a status *as the content of the assertion*, never as mutable state | `DefinitionStateFact`, `DisclosureResult`, `DisclosureResultRecord`, `FactRecord`, `UnresolvedFactRecord`, `CausalLinkRecord`, `TransitionCandidateRecord`, `CommitRecord`, `InterpretationRecord`, `CommittedTransition`, `BuildIdentity`, `IssuedRequestRecord` |
| 3. **Derived projection** — folded from facts in JournalID order | must carry `SnapshotJournalID`; writes no truth; delete + replay reproduces it exactly | `DefinitionStateRecord`, `CapabilityStateRecord`, `DisclosureCurrentState` |
| 4. **Build input** — source-controlled, not a journal record | may carry status because it is not a snapshot of journal state | `HookActivation`, `HookActivationManifest` |
| 5. **Non-journal value** *(Amendment 1)* — carries no journal identity of its own, is never resolved from or folded out of the journal | recognised by **predicate**, not by name list | queries, pages, cursors, bounds, inputs, typed errors, typed ID views, enums, service interfaces |

The `DisclosureRecord` union is **deleted and split** into `DisclosurePlanRecord`,
`DisclosureAttemptRecord`, `DisclosureResultRecord`: "immutable snapshot *or* fact per
Kind" is not a legal value under a rule that says *exactly one of N kinds*, and the union
carried a `DisclosureDeliveryStatus` meaningless for two of its three arms.

**Why this invariant exists.** Three review rounds produced the same defect from three
different reviewers (`Semantics.Identities`, `ContextDisclosure.State`,
`DefinitionSnapshot.Status`). PROPOSAL-9 introduced INV-1 to stop the fourth point-fix and
then shipped the fourth instance inside the same revision, invisible to INV-1's own guard,
because the guard's only data source was a table that omitted the offending type. The
totality rule turns "every record is classified" from a sentence into a check.

### 2.6 T1 — receipt: append every delivery, expose no replay (§5)

**Transport stage is private.** `CapturedDelivery{Contract, Event, Bindings, Capture,
Payload}` exists only between the generated registration coordinate, the private host
frontend, and the retention codec. It has no occurrence ID, actor, timestamp, semantic
fact, candidate, policy, or response. It is never returned by a public reader.

**The canonical occurrence** is `OccurrenceRecord{OccurrenceID, Kind, RuntimeContract,
Envelope, ReceivedAt, Actor, Bindings, Capture, Payload}` — INV-1 class 1.
`OccurrenceID` is exactly the positive `ProducedJournalID` for the mandatory `occurrence`
result slot. There is no second row identity.

**Operation identity.** Each CLI invocation mints one fresh operation ID before the bounded
storage retry loop and reads the T1 clock once. A retry inside that invocation reuses the
same operation input. **A second host invocation always mints a new operation ID, even when
every byte and native binding is identical.**

**Ingress never invokes the activation aggregate.** `internal/tasks/system_identity.go`
previously called `provadapter.ActivatePastureSystem` even on the already-persisted path,
taking an unconditional `BEGIN IMMEDIATE` before discovering there was nothing to write —
so a hook invocation acquired the write lock at least twice. Lifecycle ingress resolves the
persisted `(actor, authority)` pair **read-only**.

**Forbidden symbols and concepts** in lifecycle source and public results:
`ReceiptResult.Replayed`, `CommittedTransition.Replayed`, `RecordReplayed`, `ReplayKey`,
content deduplication, occurrence repeat counts, occurrence windows, and payload digest as
occurrence identity. Content hashes may identify retained evidence objects only. The static
guard is **scoped to lifecycle results**, leaving pre-existing Provenance-operation
idempotency flags untouched.

*This is the clause that decides the `lower.go` question. See §7.*

### 2.7 Progressive exact-version activation (§6)

`HookActivation{Contract, Event, Status, Fixture, Validation, Reason}` — INV-1 class 4.
`Status ∈ {HookEnabled, HookWithheld}`; `WithheldReason ∈ {MissingFixture,
ContractMismatch, ProductionPathFailed, ExplicitlyDisabled, UnverifiedBuild,
RetentionMatrixPending}`.

Constructor rules, closed and non-vacuous:

- Enabled requires nonzero **authentic** fixture and exact-version validation refs;
  `Reason` must be zero.
- Enabled requires the backing build identity to be `BuildIdentityExact` (§2.9).
- Enabled requires the UAT-selected retention matrix to have passed **for the capture class
  the event carries**.
- Withheld requires exactly one nonzero typed reason.
- Enabled ∪ Withheld equals the independent expected 30-event manifest exactly once.

**Two disjoint manifests with different authority.** A hand-authored independent 30-event
manifest under `testdata` is the catalogue-completeness oracle and does not import or
iterate the production hostcontract. A separate source-controlled *activation* manifest
carries only typed ordinals, status, proof refs, and reasons — no names, fields,
identities, or semantic rules.

**The activation bar.** An event becomes Enabled only after its authentic exact-version raw
fixture passes the real *generated registration/trampoline → built binary/stdin →
file-backed journal → public bounded occurrence reader* path. **Self-derived payloads,
direct frontend calls, direct `Apply`, or another event's fixture do not count.**

**One host descriptor source (§6.3).** `internal/lifecycle/ingress/internal/hostcontract`
is the sole typed source for exact event names and ordinals; fields, scalar shape,
requiredness, identity additions; blocking, mutation, reconciliation, failure, stop-loop
behaviour; command surface and host timeout. One generator under `ingress/cmd/
hostcontractgen` emits the closed `ContractEventKind` constants, the payload descriptors,
the registration candidate manifest, and the audit/completeness data. Enforcement is five
focused rules, not a taint analyser: clean regeneration; Go package visibility; a `go list`
dependency test; a `go/ast` pass rejecting a second table keyed by `ContractEventKind`; and
**the same pass rejecting a phase-adjacency table outside `pkg/protocol`**.

### 2.8 Immutable definitions and derived state (§7.1–7.2)

`DefinitionKind ∈ {RuntimeContract, LifecycleSchema, Codebook, Interpreter,
EpochImplementation, ContextPolicy, ContextPacketSchema, RetentionPolicy}`.

- `DefinitionSnapshot` (class 1) — **no `Status` field.** The body of a definition never
  changes, so nothing about it can be Active or Retired.
- `DefinitionStateFact` (class 2) — one append-only record per state change.
- `DefinitionStateRecord` (class 3) — folded in JournalID order, carries
  `SnapshotJournalID`.

`ResolveHistorical` returns only the immutable body and ref: **historical resolution never
means latest, and never returns a status.** Retirement never deletes the body or the old
decoder and never rewrites a prior JournalID.

Two envelopes carry the exact definitions that produced a record:

- `OccurrenceEnvelopeRef{Runtime, Schema, Implementation, Retention}` — T1.
- `SemanticEnvelopeRef{Runtime, Schema, Codebook, Interpreter, Implementation}` — T2.

`InterpreterDefinitionRef` and `EpochImplementationRef` stay separate because
`InterpretationRegistry.Resolve` takes two arguments precisely so one build may ship several
interpreters. The asymmetry favours separation: over-separating costs one JournalID per
envelope; under-separating costs unrecoverable ambiguity in an append-only ledger.

### 2.9 Build identity honesty (§7.3)

```go
type BuildIdentityStatus uint8
const (
    BuildIdentityExact BuildIdentityStatus = iota + 1
    BuildIdentityDirtySource
    BuildIdentityUnresolved
)
type BuildIdentity struct { // validated content of a DefinitionEpochImplementation body
    Status       BuildIdentityStatus
    Revision     ContentIdentity // zero exactly when Unresolved
    BuildContent ContentIdentity // zero exactly when Unresolved
}
```

- Two definitions with the **same `Revision` and different `BuildContent`** are two distinct
  definitions with distinct JournalIDs. Neither is "the" build for that revision.
- Dirty and Unresolved are recordable at T1 and T2; they produce a typed
  `UnresolvedUnverifiedBuild` fact rather than a clean envelope claim.
- **Only `BuildIdentityExact` may back an Enabled activation or a normative T3 commit.** A
  dirty or missing build can be observed, but cannot claim clean provenance and cannot
  create context or control effects.

### 2.10 T2 — interpretation and enrichment (§8)

**Enrichment (§8.1).** `Enricher.Snapshot(EnrichmentRequest) → EnrichmentSnapshot` over
`EnrichmentFact{Fact provenance.JournalID, Source, Payload, Confidence}`, bounded at 1..64
facts / 256 KiB. The requested snapshot is at or before the reader snapshot and never reads
unbounded current state. **An enricher cannot change the occurrence, semantic envelope,
phase, context, or authority.**

**The output algebra (§8.2).**

```go
type InterpretationInput struct {
    Occurrence OccurrenceRecord
    Envelope   SemanticEnvelopeRef
    Enrichment EnrichmentSnapshot
}
type InterpretationDraft struct {
    Facts      []FactDraft
    Links      []CausalLinkDraft
    Candidates []TransitionCandidateDraft
    Unresolved []UnresolvedFactDraft
}
type Interpreter interface {
    Interpret(context.Context, InterpretationInput) (InterpretationDraft, error)
}
type InterpretationRegistry interface {
    Resolve(CodebookDefinitionRef, InterpreterDefinitionRef) (Interpreter, error)
}
```

`FactKind` has 10 values (`FactSession` … `FactParseIssue`). `CausalRelation` has 11
(`RelationPromptedBy` … `RelationCommittedAs`). `UnresolvedReason` has 9, each with a typed
`UnresolvedBasis`.

**Adjacency lives in exactly one place.** `TransitionCandidateDraft`'s constructor validates
`(From, To)` against `protocol.PhaseSpecFor(From)` and returns `CandidateAdjacencyError`
for a pair the FSM disallows. T3's CAS would reject an illegal commit either way — but
without this rule the ledger fills with permanently-uncommittable candidates, and the
natural "fix" is to teach the codebook the adjacency table, **which is how a second FSM gets
born**. §6.3 item 5 guards it statically.

**`Interpreter` is pure** with respect to storage, context, response, and control. It cannot
mutate the occurrence or envelope. All collections are canonicalized, bounded, and copied.
There are no undefined string event names.

**Operation service (§8.3).** The service — not the pure interpreter — owns the injected
clock. It resolves the occurrence plus all five immutable definitions before calling
`Interpreter`, obtains one bounded enrichment snapshot, **canonicalizes output**, and calls
`Journal.Apply` once. Result slots are exact and stable: `interpretation`, `fact-%03d`,
`unresolved-%03d`, `link-%03d`, `candidate-%03d`. Canonical order is output kind → schema/
rule definition JournalID → canonical body → canonical basis. **Input slice order cannot
change slots.**

### 2.11 Journal-native causal lineage (§9)

A causal link is an `EffectEvidence` record with the `CausalLinkDraft` shape; its produced
JournalID is its sole identity. Cycles are legal **when explicitly evidenced**. Shared
timestamps, sessions, actors, paths, or worktrees **never** create a link.

**The no-promotion rule** is a direct comparison over one `Confidence` type: a link's
confidence may not exceed the minimum confidence of the enrichment facts in its basis. The
minimum is taken over exactly those basis entries whose `DraftRefKind` is
`DraftRefCommittedJournal` and whose `JournalID` equals an `EnrichmentFact.Fact` in the
same-operation snapshot. Entries that are `DraftRefBatchOutput`, or that resolve to a
committed non-enrichment record, contribute nothing. **If no basis entry resolves to an
enrichment fact, confidence is at most `ConfidenceObserved`** — the stated guard against
`min` over an empty set silently becoming "unbounded".

### 2.12 Context disclosure (§10)

`ContextProjector.Project(ContextProjectionInput) → ContextProjection` is **pure** and
receives only exact committed phase/assignment facts and exact definitions. It has no
Journal, FSM, control service, transport, or mutable projection. Candidate-only, stale,
unauthorized, wrong-recipient, and unknown-schema input fails **before** planning.

**The selection rule is one invariant, not a field.** Every bounded history collection —
`Basis`, `PriorPlans`, `Acknowledgements` — is selected newest-first by descending JournalID
at or before the reader snapshot, then truncated at its bound. `ContextSelectionRule` and
`ContextSelection.Rule` are deleted: a single-valued enum representing a choice nobody makes
is a placeholder, not a contract.

**Truncation is never silent.** Where the policy declares completeness required, the
projector returns `ContextCompletenessError` and the packet is **suppressed**, producing a
`DisclosureSuppressed` fact rather than a partial packet.

Plan → attempt → result are three separate `Journal.Apply` operations with mandatory slots
`disclosure-plan` / `disclosure-attempt` / `disclosure-result`. **A crash after the attempt
fact but before a result remains an honest unknown attempt.** Delivered, acknowledged,
failed, revoked, and suppressed are separate facts; **delivery never implies
acknowledgement.**

### 2.13 One FSM, capability, and T3 (§11)

`pkg/protocol` owns one **private static** phase table; production constructors no longer
accept a spec map; read-only lookup and iteration return deep copies. `PhaseSpecFor` is also
the single adjacency oracle consulted by `TransitionCandidateDraft`'s constructor.

**The Pasture-issued request chain (§11.2.1).** `IssuedRequestRecord{Request, Contract,
Session, Nonce, Operation, IssuedAt}` — class 2, minted under Pasture's own authority. A
host cannot create one: its JournalID is produced by a Pasture `Apply` and the `Nonce` is
Pasture-chosen. For `CapabilityRecordHumanDecision`:

- `Issue` **requires** a matching `IssuedRequestRef`; for every other operation the ref must
  be zero.
- `Bind` **requires** a `ResponseOccurrenceRef`, validated **inside the same
  `Journal.Apply`, before any write**, for exact request / session / contract / occurrence
  equality. Failure returns `IssuedRequestMismatch{Axis}` with zero writes.
- `CommitCandidate` consumes only a Bound capability carrying that validated response.

**Scope is fixed at Issue; Bind never widens (§11.2.2).** `Bind` may narrow; it may never
add, replace, or widen. The rejected alternative — *bind-defines* — makes the native axes
compare host data to host data, which is vacuous.

**Fifteen scope axes (§11.2.3)**, each with a stated Left/Right comparison and its own
must-fail corpus row. The six native axes are held in **bijection** with the eight
`NativeBindingKind` values by a static parity guard. `CapabilityScope.Sources` is
**provenance, not a match criterion** — mandatory, and deliberately never compared.

`EpochControlService` keeps exactly four methods: `Issue`, `Bind`, `Revoke`,
`CommitCandidate`.

**Claude 2.1.210 cannot satisfy this chain.** The pin has no field for Pasture to place a
nonce into and have it returned, so `ElicitationResult` remains evidence and candidate only.
The chain exists so that when a contract *can* prove it, the proof is a type and a
transaction rather than a paragraph.

### 2.14 Reader, malformed ingress, response (§12–§13)

`LifecycleReader` is the single public bounded reader; production tests and shipped commands
may use only it or the underlying public API. The **bounded-reader guard** is a path-pattern
denylist (any `_test.go` under `cmd/pasture/**`, `internal/lifecycle/**`,
`internal/engine/budget/**`) rejecting `database/sql` imports, projection-internal imports,
and SQL string literals — with exactly one named exemption, `acceptance.SnapshotFile`.

**Invalid-delivery matrix (§13.1)** — eight rows, all `neutral exit 0`. Malformed /
duplicate-key / invalid-UTF-8 / truncated / over-limit bytes still produce **one bounded
occurrence with the exact capture disposition**; withheld or unregistered events produce
**no store open and no receipt**; a T1 success followed by T2 failure **keeps the receipt**;
a contended append past the 1-second deadline produces **zero writes and a typed failure**.

Host stdout contains **only** the mechanical native response. It never exposes JournalID,
rowid, ActivityID, database path, raw prompt/tool/transcript data, or retention keys.
Generic CLI exit code 2 can **never** become native deny.

### 2.15 The write model (§4, as amended)

- **P0-PIN: satisfied.** Upstream `0976165`; Pasture pins
  `v0.0.4-0.20260730015136-0976165224e9`. Capability required: *a context-bearing Apply
  providing a caller lock-acquisition deadline that returns a definite failure with zero
  writes.* Landed shape is the **optional** interface `ContextJournal.ApplyContext`, so the
  Pasture-side change is the re-pin **plus one type assertion and a stated non-contextual
  fallback**. Measured ~303 ms bounded vs ~5.008 s unbounded, mutation-verified.
- **K = 48 concurrent writers**, itemised once in §4.4; the load harness reads its writer
  count from that table.
- **p99 ≤ 5 ms T1 hold is a documented estimate, not a gate.** In-transaction hold
  instrumentation is not available until M6 (Provenance owns the `*sql.DB`), and a gate at
  M1 cannot depend on instrumentation arriving at M6.
- **Gated instead:** contended/uncontended p99 ratio ≤ K in the same run; **delivery-outcome
  count of `IngressDeadlineError` must be zero**; exactly one write transaction per
  successful occurrence commit; no fixed sub-100 ms lock-retry loop.
- **A deadline-failed `Apply` enters the p99 sample at the full 1000 ms value**, not
  excluded — the tail the gate exists to see must not be the tail the gate discards.
- **Revisit trigger (e):** any deadline failure in the sample escalates to Option B
  (off-path spool). Bounding the wait converted contention into **dropped receipts**, a
  failure mode that did not exist before the pin, and §22 records it as a cost of Option A.

Selected: **Option A** (bound the wait upstream + honest SLO). Rejected: **C** (cross-process
admission control — `flock` leaks permits on crash, a daemon socket makes hooks *fail* when
the daemon is down, SQLite-as-semaphore is self-defeating) and **D** (defer the T1 append —
breaks receipt-before-response). **E** (stage activation by fanout and blocking profile) is
already selected: **the activation manifest is the throttle.**

---

## 3. The 30-event Claude 2.1.210 contract (§14)

Exactly 30 events, in pinned order, verified row by row. Behaviour abbreviations: `OBS`
nonblocking; `GATE` blocking; `COND` conditionally blocking; `STOP` blocking gate consulted
only while `stop_hook_active=false`; `HUMAN` blocking explicit-human-response.

**The behaviour column describes host capability, not Pasture authority.** Only an Enabled
activation is registered; every audit row remains in the independent catalogue whether
Enabled or Withheld.

Load-bearing rows for the milestone plan:

| # | Event | Behaviour | Why it matters here |
|---:|---|---|---|
| 1 | `SessionStart` | OBS | Session- (not tool-) scoped: low fanout, non-blocking. The first Enabled event. |
| 4 | `UserPromptSubmit` | GATE | The human-signal leg. **Explicit M3 gate.** Turn-scoped, so the `aura-plugins-cvlu6` restriction does not bar it. |
| 8 | `PreToolUse` | GATE + input mutation | Per-tool-call and blocking: highest fanout, competes directly with engine signal delivery. Blocked behind `aura-plugins-cvlu6`. |
| 14 | `FileChanged` | OBS | Asserts `RelationObservedPath` **only** until explicit tool/VCS/worktree enrichment resolves an artifact or commit. |
| 29/30 | `Elicitation` / `ElicitationResult` | GATE / HUMAN | Evidence and candidate only — the pin cannot prove a Pasture-owned challenge round trip. |

---

## 4. Stage table, reconciled against research §12

Research `llm/research/hooks-ir-compilers-architecture-lessons.md:273-291` states the target
pipeline:

```text
native event
  -> generated registration + thin trampoline
  -> typed Pasture entry point
  -> per-harness frontend (Go)       : native payload -> Level 1
  -> lowering pass (Go)              : Level 1 -> Level 2 lifecycle IR
  -> legalization/authorization pass : Level 2 -> Level 3 protocol operation
  -> engine + Provenance             : Level 3 -> Level 4 effects
```

### 4.1 The ratified stages

| # | Stage | Consumes | Emits | Owning package | Slice / MS | Built? |
|---:|---|---|---|---|---|---|
| B1 | Host descriptor | pinned host contract source | `ContractEventKind` / `NativeFieldID` ordinals, payload descriptors, registration candidate manifest, audit data | `internal/lifecycle/ingress/internal/hostcontract` + `ingress/cmd/hostcontractgen` | S2 / M1 | **yes** |
| B2 | Activation | descriptor manifest + fixture / validation / build proofs | `HookActivation` manifest (Enabled ∪ Withheld = 30) | `internal/lifecycle/activation` | S2 / M1 | **yes** |
| B3 | Registration emission | activation manifest | `hooks.json` (Enabled only) + `pasture-activation.json` support report | `internal/codegen` → `internal/target/claudecode/assets/**` | S2 / M1 | **yes** |
| R1 | Trampoline | native event | `exec` of `pasture hook lifecycle --harness … --event …` with a **trusted registration coordinate** | generated `hooks.json` | S2 / M1 | **yes** |
| R2 | Typed entry point | argv + stdin | validated invocation; no store opened on an invalid one | `cmd/pasture/hook_lifecycle.go`, `internal/handlers/hook_lifecycle.go` | S2 / M1 | **yes** |
| R3 | **Ingress capture (frontend)** | raw native bytes + registration coordinate | `CapturedDelivery` (**private**): `CaptureDisposition` + `[]NativeBinding` | `internal/lifecycle/ingress/claude` | S2 / M1 | **yes** |
| R4 | Blob write *(Amendment 2)* | raw body | content-addressed blob, committed **before** the occurrence | `internal/lifecycle/receipt` (`SQLiteBlobStore`) | S1 / M1 | **yes** |
| R5 | **T1 receipt** | `CapturedDelivery` | `OccurrenceRecord` / `EvidenceReceipt` via one `Journal.Apply`, slot `occurrence` | `internal/lifecycle/receipt` | S1 / M1 | **yes** |
| R6 | Projection / public reader | committed journal | bounded typed pages via `LifecycleReader`; **M1 derives projections by replay** | `internal/lifecycle/model` + `internal/lifecycle/projection` | S1 / M1 | **yes** |
| R7 | Definition resolution | `DefinitionQuery` | `DefinitionRef` / `DefinitionSnapshot` / `DefinitionStateRecord`, `SemanticEnvelopeRef` | `internal/lifecycle/definitions` | S3 / M2 | **no** |
| R8 | Enrichment | `EnrichmentRequest` | `EnrichmentSnapshot` (bounded, ≤64 facts / 256 KiB) | `internal/lifecycle/enrichment` | S3 / M2 (+ S4 tool/vcs/worktree at M3) | **no** |
| R9 | **T2 interpretation (pure)** | `InterpretationInput{OccurrenceRecord, SemanticEnvelopeRef, EnrichmentSnapshot}` | `InterpretationDraft{Facts, Links, Candidates, Unresolved}` | `internal/lifecycle/codebook` (rules), resolved by `internal/lifecycle/interpretation` | S3 / M2 | **no** |
| R10 | **Canonicalization + effect mapping** | `InterpretationDraft` | canonically ordered journal effects bound to exact result slots | `internal/lifecycle/lowering/{canonicalize,effects}.go` | S3 / M2 | **no** |
| R11 | T2 commit | effects | `InterpretationResult` via one `Journal.Apply` | `internal/lifecycle/interpretation/{journal,service}.go` | S3 / M2 | **no** |
| R12 | Lineage query | committed links | bounded forward/reverse graph pages | `internal/lifecycle/lineage` | S4 / M3 | **no** |
| R13 | Context projection (pure) | `ContextProjectionInput` (committed facts + policy defs) | `ContextProjection` | `internal/lifecycle/context` | S5 / M4 | **no** |
| R14 | Disclosure plan / attempt / result | `ContextProjection` | three separate immutable journal facts | `internal/lifecycle/context` + one UAT-selected transport | S5 / M4 | **no** |
| R15 | Response encoding | typed disposition | native host response bytes, **mechanical only** | `internal/lifecycle/response` + `ingress/claude/response.go` | S6 / M5 | **no** |
| R16 | **Legalization + authorization (T3)** | `TransitionCandidateID` + `CapabilityID` | `CommittedTransition` — capability consume + phase CAS **inside one `Apply`** | `internal/lifecycle/control` + `pkg/protocol` FSM | S8 / M6 | **no** |

### 4.2 Stage-by-stage reconciliation

| Research §12 stage | PROPOSAL-10 counterpart | Verdict |
|---|---|---|
| generated registration + thin trampoline | B1–B3, R1 | **Match, and stronger.** Research asked for mechanical projection of a pinned table; P10 adds progressive activation (§6) so an unproven event is *visibly Withheld with a typed reason* rather than registered-and-broken. Research has no counterpart to activation. |
| typed Pasture entry point | R2 | **Match.** |
| per-harness frontend (Go): native payload → **Level 1** | R3 | **Match.** P10 calls the artifact `CapturedDelivery` and the stage "the private host frontend" (§5.1). It never says "Level 1". |
| lowering pass (Go): Level 1 → **Level 2 lifecycle IR** | R5 (`ReceiptService.Receive`) | **Match in role, divergent in form.** `OccurrenceRecord` **is** the Level-2 waist: target-neutral, no host response internals, guarded by package visibility. But P10 **fuses the L1→L2 conversion with a durable write**: there is no separately addressable pure pass and no `Lower`-equivalent function boundary. See §4.3. |
| legalization/authorization: Level 2 → **Level 3 protocol operation** | **Split into R9 + R16** | **Refinement, not a gap.** Research folds selection and authorization into one pass. P10 separates them on principle (§0.3: *"blocking host behaviour does not grant protocol authority"*): R9 *selects* a `TransitionCandidateDraft` (evidence, non-authoritative, adjacency-legal); R16 *authorizes* it under a scoped capability with a phase CAS. |
| engine + Provenance: Level 3 → **Level 4 effects** | R10 + R11 (+ R16 for control effects) | **Match, with an inverted name.** P10's package called `lowering` performs *this* stage (canonicalize + effect mapping), not the L1→L2 stage the research doc calls "lowering pass". See §4.4. |

**Research §12 stages with no PROPOSAL-10 counterpart: none.** All six are covered.

**PROPOSAL-10 stages with no research §12 counterpart** (P10 adds, research is silent):

- **B2 activation** — progressive per-event Enable/Withhold with authentic-capture proof.
- **R4 blob retention** — content-addressed body storage (Amendment 2).
- **R6/R7 the evidence/semantics split itself** — research §12 has *one* lowering pass and
  no notion of two write planes. P10's T1/T2 split (receipt survives interpretation failure)
  is new.
- **R13–R14 context disclosure** — research has no context plane at all.
- **R15 response encoding** — research §12 stops at effects.

### 4.3 The one genuine structural divergence: L1→L2 is not a pure pass

Research §11 requires *"Golden IR per frontend — each harness event lowers to expected IR.
Fast, no host or daemon required"* and *"Per-pass tests — LLVM's lit/FileCheck runs one pass
on one IR file. An end-to-end test is valuable but is not a substitute."* PROPOSAL-4 §4 made
`Frontend.Parse` explicitly pure — *"no store, no clock, no effects."*

PROPOSAL-10 keeps the frontend pure (`claude.Parse` at
`internal/lifecycle/ingress/claude/capture.go:27` takes bytes and returns a value), but
there is **no pure L1→L2 pass**: `CapturedDelivery → OccurrenceRecord` happens inside
`receipt.Service.Receive` (`internal/lifecycle/receipt/service.go:46`), which refuses to run
without a `BlobStore`, an `IdentityResolver`, a `Clock`, and an `OperationIDSource`
(`service.go:49-51`), and additionally writes through a `JournalAppender` (`service.go:31,77`
— note this fifth dependency has no corresponding wiring guard). The `OccurrenceRecord` is
only observable through `LifecycleReader` against a committed journal.

This is **deliberate, not accidental**: §6.2 states that *"self-derived payloads, direct
frontend calls, direct `Apply`, or another event's fixture do not count"* toward Enabling an
event, and §16.1 item 1 requires the whole authentic path. P10 trades per-pass
addressability for authentic-path proof.

*Inferred:* the trade is defensible for the *activation gate* and questionable as a
*general* stance. Research §11's point is that per-pass tests and end-to-end tests answer
different questions; P10 has effectively deleted the per-pass question rather than answered
it. Nothing in P10 forbids a pure `CapturedDelivery → OccurrenceRecord` function existing
and being golden-tested — it simply would not satisfy the activation gate. Recommend S3
extract one when R9 needs `OccurrenceRecord` construction independent of storage anyway.

### 4.4 Naming hazard: "lowering" means different things in the two documents

| Document | "lowering" denotes | Level span |
|---|---|---|
| research §12 / PROPOSAL-4 §6 | `Lower(ctx, deps, event)` | L1→L2, then L2→L4 terminal for observations |
| PROPOSAL-10 §18.1 | `internal/lifecycle/lowering/{canonicalize,effects}.go` | L3 draft → L4 journal effects |

This collision is the mechanical cause of the incident in §7. A reader who greps `lower`
across both documents gets two different stages under one word. **Recommendation:** when S3
creates the package, name it `internal/lifecycle/emit/` or
`internal/lifecycle/interpretation/emit/`, or keep `lowering` but document the span in its
package comment. Either way the collision must not survive silently into the tree.

---

## 5. Where operation selection lives

> **One named location: `Interpreter.Interpret`, PROPOSAL-10 §8.2 — implemented in
> `internal/lifecycle/codebook/{registry,claude_2_1_210}.go`, resolved by
> `InterpretationRegistry.Resolve(CodebookDefinitionRef, InterpreterDefinitionRef)`, and
> driven by `internal/lifecycle/interpretation/service.go`.**
>
> **Owner: S3-INTERPRETATION. Gated at M2. NOT YET BUILT — no file under
> `internal/lifecycle/` implements it today.**

This satisfies research §5's requirement that operation selection live *"in the middle-end,
once"*, and research §6's requirement that frontends emit IR rather than target operations:
the frontend (R3) emits `CapturedDelivery`; the waist (R5) emits `OccurrenceRecord`; only
the interpreter names a phase transition, and it names it as a **candidate**, never a
commit.

Authorization of that selection is a **second** named location:
`EpochControlService.CommitCandidate` (§11.2), in `internal/lifecycle/control/`, owner S8,
gated at M6, **also not built**.

### 5.1 The unowned interval, stated plainly

Between now and M2, operation selection has **no owner in the tree**. PROPOSAL-10 intends
this: M1 emits evidence only, and §17's M1 exclusion is explicit — *"no semantics, context,
or control."* An M1 with no operation selection is correct.

But "no operation selection at M1" is only safe if the **old** operation selection is gone,
and it is not:

| Harness | Old env-selected path | Status |
|---|---|---|
| Claude Code | `hooks/scripts/pasture-lifecycle.py` | **Gone from generated assets.** `internal/target/claudecode/assets/pasture-hooks/` contains only `git-discipline.sh`; `hooks.json` invokes `pasture hook lifecycle` for `SessionStart`. The Python emitter is now dead code (`internal/codegen/claude_hooks.go:17,207` — `claudeLifecycleScriptPath` declared, unused by `claudeHooksEmitter.Emit` at `:418`). |
| Codex | `.codex/hooks/pasture-lifecycle.py` | **LIVE.** `internal/codegen/codex_manifest.go:39,54` still calls `renderPythonLifecycleAdapter` and writes the file; `:128` still emits the `exec python3 … --event` trampoline. Bound by `PASTURE_ADAPTER_OPERATION` (`internal/codegen/claude_hooks.go:19`) into `pasture __adapter invoke` (`:324`). |
| OpenCode | `.opencode/plugins/pasture-lifecycle.ts` | **LIVE.** `internal/codegen/opencode_hooks.go:17`. |

Both live files are tracked (`git ls-files | rg pasture-lifecycle`). `internal/handlers/
adapter.go:39-56` still exposes the closed `AdapterOperation` set — 16 operations selected
by the caller.

**This is the exact failure mode research §6 names**, still shipping for two of three
harnesses, and PROPOSAL-10 does not mention it because PROPOSAL-10 is a Claude-only plan.
§18.5's disposition row — *"generated `pasture-lifecycle.py` and the hidden env-selected
lifecycle adapter path | S2 | delete when the first replacement event is Enabled"* — reads
naturally as covering all of it, but S2's file list scopes it to the Claude assets, and
`SessionStart` **is** Enabled today
(`internal/lifecycle/activation/claude_2_1_210.go:11-12`), so the trigger condition has
already fired for the part that was in scope.

*Inferred, and recorded as a finding:* the Codex and OpenCode generated adapters are an
**unowned responsibility**. They are not in any PROPOSAL-10 slice, they still perform
caller-selected operation dispatch, and nothing in the plan will remove them. File a task.

---

## 6. M1 versus M2+ scope

### 6.1 Milestones (§17)

| Milestone | First user-visible value | Essential gate | Explicit exclusion |
|---|---|---|---|
| **P0-PIN** *(prerequisite)* | none | **SATISFIED** — upstream `0976165`; S1 L3 asserts optional `ContextJournal`, maps failure to `IngressDeadlineError` | not a slice |
| **P0-CAPTURE** *(prerequisite)* | none | an authentic captured raw payload at the pinned host version under `OriginAuthenticCapture` with a matching digest, **for each UAT-selected event — not all 30**. `aura-plugins-9umx1`, recorded as IP-C | not a slice; needs a live host |
| **M1 progressive receipt** | ≥1 UAT-selected event registered and publicly queryable; others visibly Withheld | P0-PIN asserted; P0-CAPTURE per Enabled event; fixed actor resolved read-only; **no replay symbols**; authentic process proof; `ingress_invalid.yaml` green; bounded reader served by replay; support report; retention matrix per capture class; write model measured with items 9 & 10 passing incl. **zero deadline-failed deliveries** | no full-catalogue claim; **no semantics, context, or control**; no blocking per-tool-call event before `aura-plugins-cvlu6` |
| **M2 interpretation** | Enabled events produce versioned facts, unresolved facts, links, candidates | exact definitions; connected T1→T2→reader; stable slots; version evolution; `definition_versioning.yaml` green incl. dirty/missing build honesty bound to the public reader | no context send; no normative mutation |
| **M3 lineage** | human/agent/tool/worktree/artifact/commit chains query both ways | **`UserPromptSubmit` Enabled**; named positive path; missing and contradictory cases; bounded graph/cursors/replay | no probabilistic or timestamp causation; no Pasture-issued human decision |
| **M4 context** | committed-state context plan/attempt/result history queryable | UAT channel/ack choice; exact policy refs; recipient isolation; immutable outcomes; truncation and suppression | no deliberate deny; no candidate-as-state |
| **M5 response** | exact contract response encoding mechanically verified | verdict / process-health UAT; per-contract fixture; generic-fault and deny isolation | no unsupported host result |
| **M6 authority** | scoped current capability can commit selected candidates through one FSM | S7 upstream transaction-time condition + second re-pin; `capability_scope.yaml` green across all 15 axes, 5 `RequestBindingAxis` rows, 3 bind-never-widens rows; process races; old writer disabled; projection replay | **Claude 2.1.210 Elicitation authority remains unavailable** — the pin cannot prove a challenge round trip |
| **Catalogue completeness** | all 30 events Enabled | independent all-30 authentic process corpus | does not block earlier verified events |

### 6.2 What M1 legitimately omits

M1 answers §2.1 query 1 and nothing else. It legitimately omits:

- **All semantics.** No `Interpreter`, no codebook, no `FactKind`, no `CausalRelation`, no
  `TransitionCandidateDraft`. An M1 occurrence is an attributed, envelope-stamped, bounded
  record of *"this exact delivery arrived, in this journal order"*.
- **All operation selection and all authority.** §17's M1 exclusion is literal.
- **All context disclosure** and **all response encoding** beyond one mechanical neutral
  response.
- **29 of 30 events.** Progressive activation is the point, not a shortcut: an event without
  an authentic capture stays **visibly** Withheld with a typed reason, and the support report
  says why. A support claim may name only the Enabled subset.
- **In-transaction lock-hold instrumentation** (§4.6 recorded override) — it requires an
  upstream change scheduled at M6, and a gate at M1 cannot depend on it.
- **Definition activation/retirement machinery** — M1's `OccurrenceEnvelopeRef` carries
  definition refs, but `DefinitionResolver` and the state fold are S3/M2.

M1 does **not** legitimately omit: the replay-symbol guard, the bounded-reader guard, the
INV-1 shape and totality guards, the write-model guard, authentic-capture provenance, or the
zero-deadline-failure delivery-outcome assertion. Those are M1 gates.

### 6.3 Slices and integration points (§18)

| Slice | MS | Scope |
|---|---|---|
| S0-FSM | prereq M2/M6 | private phase law, defensive read API, codegen parity |
| S1-RECEIPT | M1 | model, `internal/acceptance` extensions, receipt, projection, lifecycle identity, audit v6 migration, `internal/engine/budget` load cases |
| ~~S1R-RETENTION~~ | — | **eliminated by Amendment 2** |
| S2-CLAUDE-ACTIVATION | M1 | hostcontract + generator, activation, registration, ingress/claude, codegen assets, CLI/handler spine, old-path removal |
| S3-INTERPRETATION | M2 | definitions, codebook, enrichment, interpretation, **lowering**, build identity |
| S4-LINEAGE | M3 | lineage model/reader/service, tool/VCS/worktree enrichment, query command |
| S5-CONTEXT | M4 | disclosure model, policy/service/state/transport, one UAT-selected channel adapter |
| S6-RESPONSE | M5 | response model/encoder/service, per-contract fixtures |
| S7-PROVENANCE-DEADLINE | M6 prereq | upstream transaction-time condition; second re-pin |
| S8-CONTROL-CUTOVER | M6 | control model/service, capability, engine/handler/audit cutover |

**Ownership rule (§18.0).** *Ownership is re-derived from the set of files that MUST change
to satisfy each milestone gate, and only then checked for exclusivity. A file that must
change and has no owner is a plan defect of the same severity as two owners for one file.*
Extension 1: validation files are part of the must-change set. Extension 2: search before
declaring a new shared package.

**Merge-timing points that matter most:** IP-A (`internal/acceptance` extensions before any
consumer authors a corpus), IP-C (authentic captures before any event is Enabled), IP-1/IP-2
(S1 contracts and appender before S2's first Enabled event), **IP-2b** (`S1 L3 → S2 L3 → S1
L4 → M1 complete` — the load cases need S2's generated registration and built binary, so
their *gate* moved post-S2-L3 while S1 kept *ownership*, because S1 owns the write and
therefore owns the proof of the write budget), IP-4 (T2 result algebra before lineage,
context, control).

---

## 7. Recommendation on `internal/lifecycle/lower.go`

> **Leave it deleted. Do not restore it. But record that the deletion was correct for a
> reason different from the one given, and that its replacement is unbuilt and unowned in
> the tree.**

### 7.1 What was deleted

Commit `43dbbf1` ("refactor(lifecycle): remove superseded dedup lowering") removed 1,820
lines: `event.go` (657), `key.go` (86), `lower.go` (434), `lower_fixture_test.go` (42),
`lower_internal_test.go` (114), `lower_test.go` (487).

### 7.2 Why restoring it would be wrong

**Reason 1 — its core semantic is now a guarded design violation.** `lower.go`'s purpose
was deduplication by replay key. At `43dbbf1^`:

- `lower.go:91-93` — *"Implementations MUST deduplicate on `ObservationRecord.ReplayKey` …
  and reported as `RecordReplayed`."*
- `lower.go:128-132` — the `ReplayKey` field.
- `lower.go:136-143` — `RecordOutcome{RecordCreated, RecordReplayed}`.
- `key.go:77` — `Origin.ReplayKey()`.

PROPOSAL-10 §5.2 forbids, by name, in lifecycle source and public results:
`RecordReplayed`, `ReplayKey`, content deduplication, occurrence repeat counts, occurrence
windows, and payload digest as occurrence identity. §16.4 backs this with the **Replay
source guard**, proven by `MutateReintroduceReplaySymbol`. Restoring `lower.go` would turn
that guard red on arrival.

The reason is architectural, not stylistic: §0.3's binding UAT decision is *"every host
delivery is appended as a distinct occurrence"*, and §22's benefit row is *"append every
delivery — no silent undercount"*. Dedup and append-every-delivery are mutually exclusive.

**Reason 2 — it cannot compile, and its dependencies were correctly removed.** `lower.go`
consumed `Event`, `Semantics`, `Origin`, `BindEvent` (`event.go`) and `CanonicalKey` /
`ReplayKey` (`key.go`). §18.5 disposes of both: `event.go` → S1, *"replace with the
canonical model; remove digest and the one-semantic event"* — **done**, at
`internal/lifecycle/model/occurrence.go:52` (`OccurrenceRecord`) and `:71`
(`NewOccurrenceRecord`). `key.go` → S1, *"**delete**; the static guard prevents a
replacement replay key"* — **done, and terminal**.

**Reason 3 — its M1 function already exists.** In PROPOSAL-4 §7, an observation was a
terminal `L2 → L4` pass, and `Lower` performed it. In PROPOSAL-10 that terminal pass is T1:
`receipt.Service.Receive` (`internal/lifecycle/receipt/service.go:46`) →
`JournalAppender.Append` (`internal/lifecycle/receipt/journal.go:113`). Same role, no dedup,
plus Amendment 2's blob-first ordering. Restoring `Lower` would create the dual semantic
path §18.5 exists to prevent.

**Reason 4 — the plan says "replace", and the replacement is a different stage.**
`internal/lifecycle/lowering/{canonicalize,effects}.go` (S3) maps `InterpretationDraft` to
canonically ordered journal effects. That is L3→L4. `lower.go` was L2→L4-terminal. They are
not the same code and the restored file would not be a head start on the new one.

### 7.3 What was wrong about the deletion anyway

**The stated justification does not support the action.** "No remaining production
consumers" was true at `43dbbf1^` and is not a valid deletion criterion here: `lifecycle.
Lower` had no consumers because the process boundary that would call it was never built —
`AGENTS.md`'s own guidance (*"Can I delete this? The compiler."*) answers "is this
referenced", not "is this finished". An unfinished middle-end and an obsolete middle-end are
indistinguishable to the compiler. The correct justification was available and was not
used: **§5.2 forbids the symbols this file is built on.**

**The commit was also out of slice order.** §18.5 assigns `lower.go` to **S3**, gated at
**M2**, with the disposition *"replace with pure T2 canonicalization and effect mapping"* —
a replacement, delivered together. The deletion landed during M1 work with no replacement
and no S3 in flight. Compare `event.go`: deleted in the same commit, owner S1, replacement
landed. That one was in order. `lower.go` was not.

**Net effect:** the tree is now correct with respect to §5.2 and incomplete with respect to
§8/§18.1, and nothing in the tree records that a stage is missing.

### 7.4 What to do instead of restoring

1. **Keep the deletion.** Do not resurrect `lower.go`, `key.go`, or `ReplayKey` in any form.
2. **Make the gap visible.** File a Beads task under S3-INTERPRETATION for
   `internal/lifecycle/{codebook,interpretation,lowering,definitions,enrichment}` naming
   `Interpreter.Interpret` as the sole home of operation selection, blocking M2. Today the
   absence of a middle-end is indistinguishable from a decision not to have one — which is
   precisely the condition research §6 warns produces syntax-directed generation.
3. **File the Codex/OpenCode adapter removal** (§5.1). Those are live, unowned instances of
   the failure mode.
4. **Rename or document the `lowering` package** when S3 creates it (§4.4).
5. **`git show 43dbbf1^:internal/lifecycle/lower.go`** remains the provenance. Nothing is
   lost; it should not be reachable from the build.

---

## 8. PROPOSAL-10 versus the research doc: conflicts, and mere silences

Research `llm/research/hooks-ir-compilers-architecture-lessons.md` is standing authority and
is not superseded. Classified honestly:

### 8.1 Genuine conflicts

| # | Research | PROPOSAL-10 | Assessment |
|---|---|---|---|
| C1 | §11: per-pass tests, golden IR per frontend, *"an end-to-end test is valuable but is not a substitute"* | §6.2: *"self-derived payloads, **direct frontend calls**, direct `Apply` … do not count"*; §16.1 requires the full authentic path | **Genuine, and P10 wins on its own terms.** P10's target is falsifiability of a *pinned host contract*, which a descriptor-derived fixture cannot provide (§22: *"a fixture derived from the descriptor it validates cannot falsify that descriptor"*). But P10 states the rule as a general disqualification rather than as an activation-gate scope, which reads as forbidding per-pass tests. **Resolve by scoping:** direct frontend calls do not satisfy the *activation gate*; they remain legitimate as per-pass tests. |
| C2 | §7: four explicitly named IR levels, each *"small, separately testable, and independently reviewable"* | L1→L2 has no addressable pass (§4.3); L3→L4 is called "lowering" (§4.4) | **Genuine.** Not a disagreement about structure — the stages exist — but about *addressability* and *naming*. The cost is already realised: it is the direct cause of §7. |
| C3 | §2: the waist's justification is `N + M` vs `N × M`; §11: differential equivalence is what *proves* a waist | P10 is single-harness; no differential-equivalence gate at any milestone; §17 defers cross-harness equivalence | **Genuine.** P10 builds a waist and never tests that it is one. Defensible sequencing (Claude first), but it means the central architectural claim is unfalsified through M6, and §22 does not list this as a tradeoff. |

### 8.2 Silences, not conflicts

| Research | PROPOSAL-10 | Assessment |
|---|---|---|
| §5: *"Operation selection … in the middle-end, once"* | §8.2 `Interpreter.Interpret` | **Satisfied**, in P10's vocabulary. Not silent — just not greppable. |
| §6: frontends emit IR, never target operations | §5.1 `CapturedDelivery` has *"no occurrence ID, actor, timestamp, semantic fact, candidate, policy, or response"*; §6.3's `go/ast` and `go list` guards enforce it | **Satisfied and strengthened** — research asserts it; P10 guards it. |
| §8: legalization failure is first-class and actionable | §13.1's eight-row matrix, typed `WithheldReason`, `IssuedRequestMismatch{Axis}`, `CapabilityScopeMismatch{Axis}`, `IngressDeadlineError` | **Satisfied and strengthened.** Research warned adapters *"silently no-op when unconfigured"*; P10's Withheld report is the direct answer. |
| §9: serialization is a boundary artifact, not the semantic model | §13.2: host stdout carries only the mechanical response; JournalIDs never cross the host boundary; §12's reader is the typed API | **Satisfied.** |
| §10: escape hatches are modeled explicitly | — | **Silent.** P10 has no raw-JSON escape hatch. *Inferred:* correct for M1–M6 — an unneeded escape hatch is exactly the speculative surface P10 spends three sections removing. Revisit if a fourth harness lands. |
| §7 note: *"`internal/runtime.LifecycleEventMapping` … is a genuine target-description table and should be retained"* | §18.5: `internal/runtime/{lifecycle,lifecycle_profiles,lookup}.go` → S2, *"replace manual host behaviour tables with generated derivatives and a facade"* | **Not a conflict.** Research said retain the *table as a concept*; P10 retains it and moves its authorship from hand-maintained to generated-from-hostcontract. Strictly stronger. |
| §14: config fencing | Marked **DROPPED by user decision** in the research doc itself | **Correctly ignored.** |
| §3: adapter / trampoline / frontend / driver terminology | Not used | **Silent.** This is the vocabulary drift of §1.1. |

### 8.3 The one place a silence has teeth

Research §5's target shape (`:140-148`) is explicitly three-harness:

```text
Claude:   generated hooks.json + ~3-line shell trampoline
Codex:    generated hooks.json + ~3-line shell trampoline
OpenCode: generated opencode.json + thin TS plugin
```

*"Python disappears as an accidental dependency."* PROPOSAL-10 delivers this for Claude and
is silent on the other two, where Python has **not** disappeared
(`internal/codegen/codex_manifest.go:39,54`). A silence over a live violation of the
standing authority is worth one task, not a plan revision.

---

## 9. Current tree state against this architecture

Branch `feat/proposal-57-integration`, head `35d4686`.

**Built (S1 + S2, M1):**

| Path | Stage |
|---|---|
| `internal/lifecycle/ingress/internal/hostcontract/{types,claude_2_1_210}.go` | B1 |
| `internal/lifecycle/ingress/cmd/hostcontractgen/main.go` | B1 generator |
| `internal/lifecycle/registration/{types,event_kinds.gen,claude_2_1_210.gen}.go` | B1/B3 |
| `internal/lifecycle/activation/{types,claude_2_1_210}.go` | B2 — `SessionStart` Enabled, 29 Withheld (`claude_2_1_210.go:11-19`) |
| `internal/target/claudecode/assets/pasture-hooks/hooks/{hooks.json,pasture-activation.json}` | B3 |
| `cmd/pasture/hook_lifecycle.go`, `internal/handlers/hook_lifecycle.go` | R2 |
| `internal/lifecycle/ingress/claude/{capture,payload_2_1_210.gen}.go` | R3 |
| `internal/lifecycle/receipt/{clock,journal,reader,service}.go` | R4, R5 |
| `internal/lifecycle/model/{ids,bounds,definition,envelope,occurrence,reader}.go` | R5, R6 |
| `internal/lifecycle/projection/{reader,rebuild}.go` | R6 |
| `internal/lifecycle/guard/{classification,bounded_reader,timeouts}.go` | §16.4 guards, incl. Amendment 1's predicate at `classification.go:77` |
| `internal/tasks/lifecycle_identity.go` | §5.2 read-only actor resolve |
| `internal/engine/budget/budget_test.go` | S1 L4 load cases |

**Not built:** every stage from R7 onward — `internal/lifecycle/{definitions,codebook,
enrichment,interpretation,lowering,lineage,context,response,control}` do not exist.

**Live contradictions with the ratified architecture:**

1. `.codex/hooks/pasture-lifecycle.py` and `.opencode/plugins/pasture-lifecycle.ts` are
   generated and tracked; `PASTURE_ADAPTER_OPERATION` still selects operations from the
   caller (§5.1). Unowned by any slice.
2. `internal/codegen/claude_hooks.go:17,207` retains the dead Claude Python emitter.
3. `aura-plugins-0si2b` — corpus coverage and source-mutation execution are declared but not
   wired; the anti-vacuity machinery does not currently prove non-vacuity.
4. **Authentic-capture provenance is asserted, not recorded.** `aura-plugins-9umx1`
   (P0-CAPTURE) is open, yet `SessionStart` is Enabled with `FixtureEvidenceAuthentic`
   (`internal/lifecycle/activation/claude_2_1_210.go:12`). The fixture itself
   (`internal/lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_210.json`) does
   appear to be a genuine live-host capture — it carries a real session UUID, a real
   `~/.claude/projects/…` transcript path, and this worktree's `cwd`. But §6.1 requires a
   *nonzero authentic fixture ref* and §16.0.2(a) requires `OriginAuthenticCapture` with a
   **matching `RawFileDigest`**, and neither exists: `activation.FixtureEvidence`
   (`internal/lifecycle/activation/types.go:26-31`) is a bare two-value enum with no digest
   and no ref, and `internal/lifecycle/ingress/claude/testdata/corpora/
   claude_2_1_210_catalogue.yaml` carries no capture-provenance rows.

   So the authenticity that gates activation is currently a hand-set constant. That is the
   precise defect §22 describes — *"a fixture derived from the descriptor it validates cannot
   falsify that descriptor, so `MutateRemovePayloadField` had no oracle that could kill it"* —
   with the check present in name only. Combined with item 3 (`aura-plugins-0si2b`: the
   mutation operators are declared but not executed), nothing currently forces the fixture to
   stay authentic. Track under S1's `internal/acceptance` capture-provenance extension (IP-A)
   and S2's IP-C.

---

## 10. Provenance

| Document | Status |
|---|---|
| `llm/research/hooks-ir-compilers-architecture-lessons.md` | **Standing authority. Not superseded.** §14 is marked DROPPED within the document itself. |
| `llm/plan/lifecycle-ir-waist.md` | **PROPOSAL-4. SUPERSEDED** by PROPOSAL-5 → 10. Retained as provenance; header amended. |
| `aura-plugins-6ljvd` | **PROPOSAL-10. RATIFIED** with Amendments 1 and 2. Description + 7 continuation comments + ratification comments are the normative source; this document is its repository-resident form. |
| `aura-plugins-p4e29` | PROPOSAL-9. Superseded by PROPOSAL-10. |
| `aura-plugins-sgxp6` | IMPL_PLAN. **Obsolete** — must be replaced by the supervisor (§22). |

Where this document and `aura-plugins-6ljvd` disagree, **the task is authoritative** and this
document is the bug.
