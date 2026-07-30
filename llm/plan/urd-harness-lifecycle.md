---
title: User Requirements Document — harness lifecycle compiler
status: CONFIRMED by user
authority: llm/research/hooks-ir-compilers-architecture-lessons.md (standing, never superseded)
sources:
  request: aura-plugins-s43qq
  plan_uat: aura-plugins-q5ams
  impl_uat: aura-plugins-sj1sc
  version_policy: aura-plugins-16aam
supersedes_planning_for:
  - aura-plugins-neccm   # PROPOSAL-4
  - aura-plugins-6ljvd   # PROPOSAL-10
  - aura-plugins-sgxp6   # IMPL_PLAN derived from PROPOSAL-4
---

# URD — Harness Lifecycle Compiler

**This document did not previously exist.** Ten proposals and roughly twenty-four
review rounds were produced without one. Every proposal from the fifth onward
justified itself against the previous round's reviewer findings rather than
against stated requirements, which is the mechanism by which the chain produced
successive locally-reasonable but mutually incompatible architectures, lost the
middle-end, and dropped multi-harness support from the milestone map.

This document is assembled from the surviving record — the request, both user
acceptance tests, and the version-policy decision. Every requirement below is
traceable to a quoted user statement. Nothing here is inferred without being
marked as such.

**Vocabulary is compiler vocabulary.** The prior chain invented a private
dialect (`T1`/`T2`/`T3`, *occurrence*, *ingress capture*, *interpretation*,
*codebook*) for a problem domain that already has a precise and widely
understood one. A search in one dialect could not see the other, and a live
compiler stage was deleted as unreferenced. Terms of art here are: *frontend*,
*IR*, *narrow waist*, *Level 1–4*, *lowering*, *middle-end*, *legalization*,
*backend*, *pass*, *differential equivalence*.

---

## 1. Product outcome

Pasture compiles native harness lifecycle events into typed protocol operations
through a narrow waist, so that:

- every event a coding agent's harness emits is recorded losslessly and
  attributably;
- generated code that an agent produces can be traced back to the human signal
  that prompted, designed, motivated, architected, and verified it, even across
  a multi-agent orchestration;
- the SDLC advances as an explicitly phased deterministic state machine rather
  than by agent improvisation;
- agents receive phase- and role-scoped context as they move through that state
  machine.

**Verbatim, the two originating goals** (`q5ams`, 2026-07-29 20:45):

> "One of the main reasons that Pasture exists is to shift agentic workflows
> towards more deterministic directions, where the SDLC was shfited into
> explicitly named and phased pieces. […] The other goal was to be able to track
> at a fine-grained level which decisions were made by humans, when humans gave
> their input, and what was done by the agent. That is, fine-grained data
> provenance within agent coding sessions, turns, and to be able to trace back to
> the human signal that prompted, designed, motivated, architected, and verified
> it --- from the code that is created by agents, even with a multi-agent
> orchestration setup being used inbetween to create that code. This would be
> enabled by our metadata enrichment by committing to some code book for the SDLC
> at an early stage, though that WILL be subject to change, and it is important
> to know which version of the codebook and Pasture Epoch system that was used."

And (`q5ams`, 2026-07-29 20:46):

> "It is also for context and harness engineering: so that we have progressive
> disclosure as the agents move through an FSM, determined by our model of the
> SDLC."

---

## 2. The pipeline

```text
  Claude Code        OpenCode          Codex           <- N harnesses
       |                 |               |
   [frontend]        [frontend]      [frontend]        native payload -> Level 1
       |                 |               |
       +--------+--------+---------------+
                |
          ===========  Level 1: harness dialect
                |       PreToolUse, tool.execute.before, ...
          [ lowering ]                                  THE MIDDLE-END
                |                                       Level 1 -> Level 2
          ===========  Level 2: lifecycle dialect       <- THE NARROW WAIST
                |       evidence, gate-consultation, human-response
        [ legalization ]                                Level 2 -> Level 3
                |       what the phase law permits + who is authorized
          ===========  Level 3: protocol dialect
                |       StartReview, RecordPlanUAT, Land
           [ backend ]                                  Level 3 -> Level 4
                |       effect selection and emission
          ===========  Level 4: effects
                        journal operations, tasks, assignments
```

**The waist is the reason for the architecture.** With `N` harnesses and `M`
protocol operations, the waist makes the work `N + M` rather than `N × M`.
Adding a harness is one frontend; adding an operation is one backend rule.

**Operation selection happens in the middle-end, once.** Not in a generated
script, not in an environment variable, not per-harness. Research §6 names the
failure being avoided — *syntax-directed code generation*, where the parser emits
target operations directly — and notes the pre-existing design was worse than
that, because the operation was selected by an environment variable supplied by
the caller.

### 2.1 The three planes, in pipeline terms

The user's three planes are not parallel subsystems; they are stages.

| Plane (user's term) | Pipeline stage | Authority |
|---|---|---|
| Evidence / provenance | frontend + lowering, and the Level-1/Level-2 record | broad, automated, lossless |
| Normative control | legalization | separately authorized; only this may advance the FSM |
| Context disclosure | derived from committed Level-3 state | read-only projection; may not transition |

**Governing principle, verbatim** (`q5ams`, 2026-07-29 20:48):

> "we will need many hooks that WILL be in the write gate: data entry into the
> Pasture ecosystem needs to be as automated as possible, but this automation
> needs to be as semantically aligned as possible as well."

Restated as the design rule the record derives from it: **automate data entry,
not semantic guessing.** When a frontend or the lowering pass has insufficient
identity or causal context to produce one interpretation, it emits an explicit
*unresolved* fact. It does not drop the input, collapse it, or manufacture
authority.

---

## 3. Requirements

### R1 — Generated host artifacts are mechanical, and call typed public CLI commands

Generated hooks, scripts, plugins and manifests must not parse native JSON,
select protocol operations, or duplicate authorization rules. They register the
event and invoke the built binary. (`s43qq`)

**Verbatim selection** (`sj1sc` Component 2): *"Direct public CLI commands via
option 2 is better."*

The adopted path:

```text
native harness event
  -> generated per-harness wrapper (shell, or direct command configuration)
  -> TYPED PUBLIC Pasture CLI command
  -> CLI/engine owns parsing, validation, lifecycle state, Provenance writes
  -> frontend lowers the harness event to Level 1
```

**Retires:** the hidden `pasture __adapter invoke` command and the
`pasture.adapter-invocation/v1` envelope; generated Python translators as the
transport; `PASTURE_ADAPTER_*` environment-bound operation selection; and raw
native JSON as a Pasture transport or API.

No requirement ever called for Python — it was implementation convenience.

**Identity posture, explicitly accepted:** `--actor` remains **unauthenticated**
by intent. A local caller controlling the process or environment can assert any
actor. The rejected envelope never prevented this either — its
`nativeInvocation` was a replay/correlation key, not proof of identity.
Cryptographic actor authentication is out of scope at this stage.

### R2 — Frontends emit IR, never target operations

One frontend per harness, written in Go, bound to a pinned harness contract,
producing Level-1 IR. A frontend may not name a protocol operation. (`s43qq`,
research §6)

### R3 — One middle-end owns operation selection

A single lowering pass takes Level-1 to Level-2. Operation selection lives here
and nowhere else. The pass must be addressable and separately testable — a pure
function over IR values with no storage dependency — because research §7
requires each level be separately testable. (`s43qq`, research §5, §7)

### R4 — Raw JSON ingestion is a versioned escape hatch, not a second model

Raw ingestion decodes into the *same* IR. It is explicitly not the recommended
path and must not define a second semantic model. (`s43qq`)

### R5 — Append every delivery; no deduplication

**Verbatim selection** (`q5ams` C1): *"Append always, no dedup"* and *"No time
window at all"*.

**Verbatim reasoning:**

> "Claude can fire identical JSON, but our responsibility when we receive that is
> to timestamp it and also have some kind of incrementing integer row ID we can
> use in the DB that will disambiguate these."

And, correcting the architect (`q5ams` C2):

> "How is ObservedAt half of the disambiguation pair? the disambiguator pair is
> only the autoincrementing integer ID."

**Consequences:** no replay key, no `RecordReplayed`, no repeat count, no time
window, no payload-digest-as-identity. The database row identity is the sole
disambiguator; the timestamp is descriptive metadata only. `PayloadDigest` was
removed entirely as a Level-1 field — *"Remove it entirely"* (`q5ams` C2).

Content addressing of stored **bodies** is unaffected and is required by R6; a
digest identifies a body, never an occurrence.

### R6 — Store every payload body, in SQLite, content-addressed

**Verbatim** (`q5ams`, 2026-07-30 03:40), superseding an earlier metadata-only
decision:

> "Actually: we should just keep all bodies, for all objects. at a later date, we
> can decide to truncate or filter uninteresting bodies. SQLite is great for the
> MVP. just put the blob there."

**Consequences:**
- Every delivery is an **ordered pair of write transactions**: blob write, then
  the record commit. Blob-first is mandatory — an orphan blob is harmless and
  reclaimable; a dangling reference is corruption.
- Truncation, filtering and any external store are deliberately deferred.
- **Privacy posture must be documented, not discovered:** every prompt, tool
  input and file content passing a registered hook is persisted by default.

### R7 — The middle-end's interpretation is versioned, and the version is recorded

Every interpreted record must carry enough version identity to answer which SDLC
codebook and which Pasture implementation interpreted it. The codebook is
expected to change; records must stay interpretable after it does. This is the
stated purpose of retaining bodies at all. (`q5ams`, 2026-07-29 20:45)

### R8 — Gates may answer; only authorized sources may transition

A blocking event means the host **waits** for the hook process and interprets its
exit code — exit 0 proceed, exit 2 deny on Claude and Codex. It does not mean the
native event fails.

**Verbatim selection** (`q5ams` C3): *"Answer yes, write only explicit human
responses"* — later widened by the 20:48 clarification that many hooks will be
automated write gates at the evidence and provenance stages. The durable rule is:
**broad automated writes before legalization; separately verified authority at
legalization.**

### R9 — Exit 0 is a temporary posture, and must be replaceable

**Verbatim** (`q5ams` C3):

> "Let's JUST exit 0 for now and report on stderr. However: this does not mean
> 'never exit non-zero'. In future iterations, we will want Pasture's lifecycle
> and current phase to potentially block some tools from occurring."

**Consequences:** the response/emission stage is **retained**, not cut. Pre-deny,
a gate event must be **recorded as evidence of consultation and answered
proceed** — refusing while exiting 0 records nothing and answers proceed, which
is the silent no-op this architecture exists to remove. The open hazard is that
exit code 2 currently maps to `CategoryConnection`, so an internal fault would be
read by the host as *deny*; this must be resolved before any real deny ships.

### R10 — Bound the wait upstream; the write budget is an observational SLO

**Verbatim** (`q5ams`, 2026-07-30 01:31): *"Do A."* — deadline only, via upstream
context threading. Not an off-path spool, not cross-process admission control.

**Consequences:** contended ingress fails fast with a typed deadline error and
zero writes, rather than stalling. The writer-count and lock-hold figures are
observational, not enforced ceilings.

### R11 — Timeout ordering is enforced by construction

**Verbatim selection** (`q5ams`, 2026-07-30 05:32): *"Fix as a class, enforced by
construction"*.

No inner timeout may exceed the deadline of any caller that waits on it. This is
enforced by a guard over every declared profile, not by correcting individual
constants — three independent sites had drifted identically.

### R12 — Record the observed harness version; never reject on it

**Verbatim** (`q5ams`, 2026-07-30 04:49):

> "Widen range and no skew detection right now. Detected version mismatch
> shouldn't be a blocking issue right now: this is a clear DEFER. In a future
> epic, we will list the versions of harnesses that we explicitly support, for
> each version of Pasture. Right now, we just need to lay down the most minimal
> groundwork."

Plus the selection *"Yes — record version, never reject"*.

This is safe **because** of R6: a payload from an unsupported version is retained
in full, so the evidence to detect and re-interpret it is in the ledger rather
than discarded at ingest.

### R13 — Unproved events are visibly withheld, never silently absent

An event Pasture cannot prove end-to-end must be registered as withheld with a
typed reason, not omitted. Omission without a visible report is itself a silent
no-op (`q5ams` C2 consequences).

### R14 — Atomic review-batch materialization (ACCEPTED)

**Verbatim** (`sj1sc` Component 1): *"Atomic eager batch (Recommended)"*,
decision *"ACCEPT (Recommended)"*.

A review round commits its whole closure or none of it; exact retries return the
same identities with zero persisted deltas. This component is already accepted
and must not regress.

---

## 4. MVP sequencing

**Verbatim intent:** implement for Claude first as an MVP, then translate the
approach to OpenCode and Codex.

| Stage | Content | Proves |
|---|---|---|
| MVP | Claude frontend + lowering + legalization + backend, end to end | the pipeline works |
| +1 | OpenCode frontend + **differential equivalence** | the waist *is* a waist |
| +2 | Codex frontend, retiring `PASTURE_ADAPTER_*` | `N + M` holds |

**Differential equivalence is not optional.** Research §11 names it as the test
that proves a narrow waist exists at all: semantically equivalent native events
from two harnesses must lower to the same Level-2 IR. A single-harness MVP
*cannot* demonstrate a waist by construction — only the second frontend can. No
prior plan carried this gate at any milestone.

---

## 5. Non-goals and dropped scope

| Item | Status | Source |
|---|---|---|
| Config fencing / ownership tags in host config | **DROPPED**, not deferred | `s43qq`: *"This fencing should just be deferred… Or dropped, rather."* |
| Version/commit build stamping | not required by this request | same |
| Body truncation / filtering / external store | deferred | `q5ams` R6 |
| Harness support matrix, version skew detection | deferred to a later epic | `q5ams` R12 |
| Cross-process admission control | rejected on design grounds | `q5ams` R10 |
| Off-path spool | available, explicitly not chosen | `q5ams` R10 |

---

## 6. Open decisions

1. **Exit-code contract.** Exit 2 means `CategoryConnection` internally and *deny*
   to the host. Must be resolved before any real deny ships. (R9)
2. **What the write gate is.** R8 fixes the *principle* — broad automated writes
   before legalization, separately verified authority at legalization — but not
   the mechanism. The superseded PROPOSAL-10 proposed a capability handshake
   (Pasture mints a challenge; a hook consumes it only under byte-exact
   request/session/contract/nonce match; scope fixed at issue and never widened).
   That mechanism is **unbuilt** and is not carried forward by default.
   **Deferred by explicit user decision; to be decided before the legalization
   stage is implemented.**

---

## 7. Validation cases

Per the standing constraint that every request carries concrete validation cases:

| # | Given | When | Then | Must not |
|---|---|---|---|---|
| V1 | two byte-identical deliveries | both are received | two records with two distinct row IDs | collapse, report replay, or expose a repeat count |
| V2 | any delivery | it is recorded | the exact body is retrievable by digest | discard, truncate, or store only metadata |
| V3 | a delivery whose record commit fails | after the blob is written | the blob is a reclaimable orphan and no record references it | leave a dangling reference |
| V4 | a Level-1 IR value | the lowering pass runs | Level-2 IR is produced with no storage involved | require a database to test the pass |
| V5 | semantically equivalent events from two harnesses | both are lowered | identical Level-2 IR | differ in any semantic field |
| V6 | a host version outside the pinned range | a delivery arrives | it is recorded with the observed version attached | reject, withhold, or silently reinterpret |
| V7 | an event without end-to-end proof | registration is generated | it appears as withheld with a typed reason | be silently absent |
| V8 | a blocking gate event before deny ships | it is received | evidence of consultation is recorded and the host is answered proceed | record nothing, or deny |
| V9 | a generated host artifact | it is inspected | it contains no operation selection and no JSON parsing | name a protocol operation |
| V10 | a timeout profile whose inner budget exceeds a caller deadline | the guard runs | the build fails | pass because only the default profile was checked |
| V11 | a review round | it is materialized | the whole closure commits or none does, and exact retry is identity-stable | create partial visible state |
| V12 | a delivery the codebook cannot fully classify | it is processed | the record is appended and an explicit unresolved fact is recorded | drop it, guess, or manufacture a transition |

---

## 8. Traceability

| Requirement | Source | Kind |
|---|---|---|
| R1, R2, R4 | `s43qq` | request |
| R3 | `s43qq` + research §5, §7 | request + standing authority |
| R5 | `q5ams` C1, C2 | ratified UAT, verbatim |
| R6 | `q5ams` 03:40 | ratified UAT, verbatim (supersedes 03:33) |
| R7 | `q5ams` 20:45 | ratified UAT, verbatim |
| R8 | `q5ams` C3 + 20:48 | ratified UAT, verbatim |
| R9 | `q5ams` C3 | ratified UAT, verbatim |
| R10 | `q5ams` 01:31 | ratified UAT, verbatim |
| R11 | `q5ams` 05:32 | ratified UAT, verbatim |
| R12 | `q5ams` 04:49, `16aam` | ratified UAT, verbatim |
| R13 | `q5ams` C2 consequences | derived, marked |
| R14 | `sj1sc` C1 | accepted implementation UAT, verbatim |
| R1 adapter boundary | `sj1sc` C2 | ratified UAT, verbatim (Option 2 selected) |
| MVP sequencing | user statement, this session | verbatim intent |
| Differential equivalence | research §2, §11 | standing authority |
