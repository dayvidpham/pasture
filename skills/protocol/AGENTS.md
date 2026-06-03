# Agent Roles and Phase Ownership

This document maps each agent role to its owned phases, available tools/skills, and handoff procedures.

See [PROCESS.md](PROCESS.md) for the full step-by-step workflow. See [SKILLS.md](SKILLS.md) for the complete command reference.

---

## Role Summary

| Role | Phases Owned | Primary Responsibility |
|------|-------------|----------------------|
| **Epoch** | 1-12 (orchestrator) | Master orchestrator; delegates to other roles per phase |
| **Architect** | 1-7 | Specs, tradeoffs, proposals, review coordination, ratification, handoff |
| **Reviewer** | 4, 10 | End-user alignment review for plans (P4) and code (P10) |
| **Supervisor** | 7-12 | Task decomposition, worker allocation, review coordination, landing |
| **Worker** | 9 | Vertical slice implementation (full production code path) |

---

## Epoch (Orchestrator)

**Command:** `/pasture:epoch`

**Phases:** All 12 (delegates to specialists)

**Responsibility:** Coordinates the full 12-phase workflow. Creates the initial REQUEST task, invokes the appropriate role skill for each phase, and ensures dependency chaining and audit trail preservation throughout.

**Skills invoked:**
- Phase 1: `/pasture:user-request`
- Phase 2: `/pasture:user-elicit`
- Phases 3-7: `/pasture:architect`
- Phases 5, 11: `/pasture:user-uat`
- Phases 7-10: `/pasture:supervisor`
- Phase 12: Manual git commit and push

---

## Architect

**Command:** `/pasture:architect`

**Phases owned:** 1-7

| Phase | Label | Skill | What Happens |
|-------|-------|-------|-------------|
| 1 | `pasture:p1-user:s1_1-classify` | `/pasture:user-request` | Classify request along 4 axes |
| 1 | `pasture:p1-user:s1_2-research` | `/pasture:research` | Domain research — find standards, prior art, competing approaches |
| 1 | `pasture:p1-user:s1_3-explore` | `/pasture:explore` | Codebase exploration — find integration points, patterns, conflicts |
| 2 | `pasture:p2-user:s2_1-elicit` | `/pasture:user-elicit` | URE survey with user |
| 2 | `pasture:p2-user:s2_2-urd` | (within elicit) | Create URD task |
| 3 | `pasture:p3-plan:s3-propose` | `/pasture:architect-propose-plan` | Create PROPOSAL-N |
| 4 | `pasture:p4-plan:s4-review` | `/pasture:architect-request-review` | Spawn 3 axis-specific reviewers |
| 5 | `pasture:p5-user:s5-uat` | `/pasture:user-uat` | Plan UAT with user |
| 6 | `pasture:p6-plan:s6-ratify` | `/pasture:architect-ratify` | Ratify proposal, mark old as superseded |
| 7 | `pasture:p7-plan:s7-handoff` | `/pasture:architect-handoff` | Create handoff document, hand off to supervisor |

**Receives from:** User (Phase 1 request) OR Supervisor via h6 (follow-up lifecycle: FOLLOWUP_URE + FOLLOWUP_URD for FOLLOWUP_PROPOSAL creation)
**Hands off to:** Supervisor (Phase 7 h1 handoff document, or follow-up h1 after FOLLOWUP_PROPOSAL ratified)

**Handoff:** Authored in the HANDOFF Beads task body with full inline provenance — includes all task references (REQUEST, URD, PROPOSAL, ratified plan), key decisions with rationale, open items, and acceptance criteria.

---

## Reviewer

**Command:** `/pasture:reviewer`

**Phases owned:** 4 (plan review), 10 (code review)

### Plan Review (Phase 4)

| Axis | Label | Focus |
|------|-------|-------|
| A — Correctness | `pasture:p4-plan:s4-review` | Does the plan faithfully serve the user's request? Technical consistency? |
| B — Test quality | `pasture:p4-plan:s4-review` | Are test strategies adequate? Integration > unit? Mock deps not SUT? |
| C — Elegance | `pasture:p4-plan:s4-review` | Is complexity proportional to the problem? No over/under-engineering? |

**Task title:** `PROPOSAL-N-REVIEW-{axis}-{round}` (e.g., `PROPOSAL-2-REVIEW-A-1`)

**Votes:** Binary — ACCEPT or REVISE. No intermediate levels. All 3 must ACCEPT.

**No severity tree** for plan reviews — binary vote only.

### Code Review (Phase 10)

Same 3 axes, applied to implementation slices.

**Task title:** `SLICE-N-REVIEW-{axis}-{round}` (e.g., `SLICE-1-REVIEW-A-1`)

**Severity tree (EAGER creation):** Always create 3 severity groups per review round:
- `pasture:severity:blocker` — Blocks the slice (dual-parent); must reach 0
- `pasture:severity:important` — Fixed in-wave; must reach 0 (NOT deferrable)
- `pasture:severity:minor` — Fixed in-wave; must reach 0 (NOT deferrable)

Empty groups are closed immediately. **No cycle cap:** iterate review → fix → re-review until a fix-free clean round confirms 0 BLOCKER + 0 IMPORTANT + 0 MINOR. No review severity is routed to FOLLOWUP — the FOLLOWUP epic is fed only by user-DEFER'd UAT items.

**Skills:** `/pasture:reviewer-review-plan`, `/pasture:reviewer-review-code`, `/pasture:reviewer-comment`, `/pasture:reviewer-vote`

**Receives from:** Architect (Phase 4, including FOLLOWUP_PROPOSAL review) or Supervisor (Phase 10, including FOLLOWUP_SLICE code review)
**Hands off to:** Architect (Phase 4 results) or Supervisor/Followup (Phase 10 findings)

---

## Supervisor

**Command:** `/pasture:supervisor`

**Phases owned:** 7-12

| Phase | Label | Skill | What Happens |
|-------|-------|-------|-------------|
| 7 | `pasture:p7-plan:s7-handoff` | (receives handoff) | Read architect handoff document |
| 8 | `pasture:p8-impl:s8-plan` | `/pasture:supervisor-plan-tasks` | Decompose ratified plan into vertical slices |
| 9 | `pasture:p9-impl:s9-slice` | `/pasture:supervisor-spawn-worker` | Spawn workers, assign slices |
| 9-10 | — | `/pasture:supervisor-track-progress` | Monitor worker status |
| 10 | `pasture:p10-impl:s10-review` | `/pasture:impl-review` | Spawn 3 code reviewers per slice |
| 11 | `pasture:p11-user:s11-uat` | `/pasture:user-uat` | Implementation UAT with user |
| 12 | `pasture:p12-impl:s12-landing` | `/pasture:supervisor-commit` | Atomic commit, push, hand off |

**Receives from:** Architect (Phase 7 handoff document, or follow-up h1 after FOLLOWUP_PROPOSAL ratified)
**Hands off to:** Workers (Phase 9 slice assignments), Reviewers (Phase 10), User (Phase 11 UAT), Architect via h6 (follow-up lifecycle)

**Key constraints:**
- Never implements code — always spawns workers
- Each production code path owned by exactly ONE worker
- Decomposes interface-first: prefers a FOUNDATION slice exporting shared identifiers before dependent slices (justifies any linear decomposition in the IMPL_PLAN)
- Drives the code-review wave to a fix-free clean round (0 BLOCKER + 0 IMPORTANT + 0 MINOR, no cycle cap) before closing slices
- Creates the follow-up epic (`pasture:epic-followup`) at **UAT** when the user DEFERs items — fed ONLY by user-DEFER'd UAT items, never by review severities
- Initiates follow-up lifecycle: creates FOLLOWUP_URE, FOLLOWUP_URD, then hands off to Architect via h6 for FOLLOWUP_PROPOSAL

---

## Worker

**Command:** `/pasture:worker`

**Phases owned:** 9

| Phase | Label | Skill | What Happens |
|-------|-------|-------|-------------|
| 9 | `pasture:p9-impl:s9-slice` | `/pasture:worker-implement` | Implement assigned vertical slice |
| 9 | — | `/pasture:worker-complete` | Signal completion after quality gates |
| 9 | — | `/pasture:worker-blocked` | Report blockers to supervisor |

**Receives from:** Supervisor (Phase 9 slice assignment with handoff document, including FOLLOWUP_SLICE-N for follow-up lifecycle)
**Hands off to:** Reviewer (Phase 10 via supervisor). For FOLLOWUP_SLICE-N, completion handoff reports which original leaf tasks were resolved.

**Vertical slice ownership:** Worker owns the full vertical — types, tests, implementation, and wiring. The slice's leaf tasks may take ANY shape (named after the real work units); the TDD layers below are one illustrative decomposition, not a required triple:
1. Layer 1: Types and schemas
2. Layer 2: Tests (import production code — will fail initially)
3. Layer 3: Implementation + wiring (make tests pass)

**Completion gates:**
1. Type checking passes
2. Tests pass
3. Production code path verified via code inspection (no TODOs, real deps wired)
4. Update Beads task with completion comment

---

## Handoff Matrix

Every handoff is **authored inline in its own HANDOFF Beads task body** (no filesystem path) and located by task ID:

| # | From | To | Phase | Authored In | Content Level |
|---|------|----|-------|-------------|--------------|
| 1 | Architect | Supervisor | 7 | HANDOFF Beads task body | Full inline provenance |
| 2 | Supervisor | Worker | 9 | HANDOFF Beads task body | Summary + bd IDs |
| 3 | Supervisor | Reviewer | 10 | HANDOFF Beads task body | Summary + bd IDs |
| 4 | Worker | Reviewer | 10 | HANDOFF Beads task body | Summary + bd IDs |
| 5 | Reviewer | Followup | post-10 | HANDOFF Beads task body | Summary + bd IDs |
| 6 | Supervisor | Architect | Follow-up lifecycle | HANDOFF Beads task body | Summary + bd IDs |

**Same-actor transitions** (no handoff needed): Plan UAT → Ratify (Phase 5→6), Ratify → Handoff (Phase 6→7) — both performed by the architect. In the follow-up lifecycle, Supervisor creating FOLLOWUP_URE and FOLLOWUP_URD are also same-actor transitions.

See [HANDOFF_TEMPLATE.md](HANDOFF_TEMPLATE.md) for the standardized template.

---

## References

- [PROCESS.md](PROCESS.md) — Step-by-step workflow execution (single source of truth)
- [SKILLS.md](SKILLS.md) — Complete command reference
- [CONSTRAINTS.md](CONSTRAINTS.md) — Coding standards and naming conventions
- [HANDOFF_TEMPLATE.md](HANDOFF_TEMPLATE.md) — Handoff document template
