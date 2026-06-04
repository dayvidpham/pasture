# Command Reference

All `/pasture:*` skills, organized by role and phase. Each skill is a `SKILL.md` file in its own directory under `skills/`.

See [AGENTS.md](AGENTS.md) for role definitions and phase ownership.

---

## Orchestration

| Command | Phase | Description |
|---------|-------|-------------|
| `/pasture:epoch` | 1-12 | Master orchestrator for the full 12-phase workflow. Delegates to role-specific skills per phase. |
| `/pasture:status` | any | Project status and monitoring via Beads queries. |

---

## User Interaction

| Command | Phase | Label | Description |
|---------|-------|-------|-------------|
| `/pasture:user-request` | 1 | `pasture:p1-user:s1_1-classify` | Capture user's feature request verbatim. Classify along 4 axes (scope, complexity, risk, domain novelty). Triggers parallel research and explore sub-steps. |
| `/pasture:user-elicit` | 2 | `pasture:p2-user:s2_1-elicit`, `pasture:p2-user:s2_2-urd` | User Requirements Elicitation (URE) survey. Creates ELICIT task with full Q&A transcript and URD task as single source of truth. |
| `/pasture:user-uat` | 5, 11 | `pasture:p5-user:s5-uat`, `pasture:p11-user:s11-uat` | User Acceptance Testing. Phase 5 tests the plan; Phase 11 tests the implementation. Uses structured demonstrative examples. |

---

## Architect

| Command | Phase | Label | Description |
|---------|-------|-------|-------------|
| `/pasture:architect` | 1-7 | (multiple) | Main architect role. Coordinates specification, proposals, reviews, ratification, and handoff. |
| `/pasture:architect-propose-plan` | 3 | `pasture:p3-plan:s3-propose` | Create a PROPOSAL-N task with full technical plan. Includes public interfaces, implementation approach, validation checklist, and BDD acceptance criteria. |
| `/pasture:architect-request-review` | 4 | `pasture:p4-plan:s4-review` | Spawn 3 parallel reviewers with axis-specific focus. Creates PROPOSAL-N-REVIEW-{axis}-{round} tasks (A=Correctness, B=Test quality, C=Elegance). |
| `/pasture:architect-ratify` | 6 | `pasture:p6-plan:s6-ratify` | Ratify proposal after all 3 reviewers ACCEPT. Marks old proposals as `pasture:superseded`. Creates placeholder IMPL_PLAN task for supervisor. |
| `/pasture:architect-handoff` | 7 | `pasture:p7-plan:s7-handoff` | Author the handoff (full inline provenance) inline in the HANDOFF Beads task body. Transfers ownership to supervisor. |

---

## Supervisor

| Command | Phase | Label | Description |
|---------|-------|-------|-------------|
| `/pasture:supervisor` | 7-12 | (multiple) | Main supervisor role. Receives handoff, decomposes plan, spawns workers, coordinates reviews, and lands code. |
| `/pasture:supervisor-plan-tasks` | 8 | `pasture:p8-impl:s8-plan` | Decompose ratified plan into vertical slices (SLICE-N tasks). Each slice = one production code path owned by one worker. |
| `/pasture:supervisor-spawn-worker` | 9 | `pasture:p9-impl:s9-slice` | Launch a worker agent for an assigned slice. Creates handoff document (summary + bd IDs). Worker must call `/pasture:worker` at start. |
| `/pasture:supervisor-track-progress` | 9-10 | `pasture:p9-impl:s9-slice` | Monitor worker status via Beads. Check for blocked/complete/in-progress slices. |
| `/pasture:supervisor-commit` | 12 | `pasture:p12-impl:s12-landing` | Atomic commit per completed layer/slice. Uses `git agent-commit`. Syncs Beads and pushes. |

---

## Worker

| Command | Phase | Label | Description |
|---------|-------|-------|-------------|
| `/pasture:worker` | 9 | `pasture:p9-impl:s9-slice` | Main worker role. Reads slice assignment, implements full vertical (types → tests → impl → wiring). |
| `/pasture:worker-implement` | 9 | `pasture:p9-impl:s9-slice` | Implement the assigned vertical slice following TDD layers (L1 types, L2 tests, L3 implementation). |
| `/pasture:worker-complete` | 9 | `pasture:p9-impl:s9-slice` | Signal slice completion after all quality gates pass (typecheck + tests + production code path verified). |
| `/pasture:worker-blocked` | 9 | `pasture:p9-impl:s9-slice` | Report a blocker to the supervisor via Beads comment. |

---

## Reviewer

| Command | Phase | Label | Description |
|---------|-------|-------|-------------|
| `/pasture:reviewer` | 4, 10 | `pasture:p4-plan:s4-review`, `pasture:p10-impl:s10-review` | Main reviewer role. Reviews plans (Phase 4) and code (Phase 10) against 3 axes. |
| `/pasture:reviewer-review-plan` | 4 | `pasture:p4-plan:s4-review` | Review a PROPOSAL-N against one axis (A/B/C). Binary vote: ACCEPT or REVISE. No severity tree for plan reviews. |
| `/pasture:reviewer-review-code` | 10 | `pasture:p10-impl:s10-review`, `pasture:severity:*` | Review implementation slices against one axis. EAGER severity tree creation (BLOCKER/IMPORTANT/MINOR). |
| `/pasture:reviewer-comment` | 4, 10 | — | Leave structured feedback on a Beads task. Utility command (no task creation). |
| `/pasture:reviewer-vote` | 4, 10 | — | Cast ACCEPT or REVISE vote. Binary only. |

### Review Axes

| Axis | Letter | Focus | Key Questions |
|------|--------|-------|---------------|
| Correctness | A | Spirit and technicality | Does it faithfully serve the user's request? Technical consistency? |
| Test quality | B | Test strategy adequacy | Integration > unit? Mock deps not SUT? Shared fixtures? |
| Elegance | C | Complexity matching | Proportional to problem? No over/under-engineering? |

---

## Implementation Coordination

| Command | Phase | Label | Description |
|---------|-------|-------|-------------|
| `/pasture:impl-slice` | 9 | `pasture:p9-impl:s9-slice` | Vertical slice assignment and tracking. Manages slice creation, worker assignment, and progress aggregation. |
| `/pasture:impl-review` | 10 | `pasture:p10-impl:s10-review` | Code review coordination across all slices. Spawns 3 reviewers (A/B/C) who each review ALL slices. Creates severity tree per review round. |

---

## Research & Exploration

| Command | Phase | Label | Description |
|---------|-------|-------|-------------|
| `/pasture:research` | 1 (s1_2), any | `pasture:p1-user:s1_2-research` | Domain research — find standards, prior art, competing approaches. Writes structured findings to `docs/research/<topic>.md`. Depth-scoped: quick-scan, standard-research, deep-dive. |
| `/pasture:explore` | 1 (s1_3), any | `pasture:p1-user:s1_3-explore` | Codebase exploration — find integration points, existing patterns, dependencies, conflicts. Depth-scoped: quick-scan, standard-research, deep-dive. |

---

## Phase → Command Quick Reference

| Phase | Commands Used |
|-------|-------------|
| 1 — REQUEST | `/pasture:user-request`, `/pasture:research` (s1_2), `/pasture:explore` (s1_3) |
| 2 — ELICIT + URD | `/pasture:user-elicit` |
| 3 — PROPOSAL-N | `/pasture:architect-propose-plan` |
| 4 — REVIEW | `/pasture:architect-request-review`, `/pasture:reviewer-review-plan`, `/pasture:reviewer-vote` |
| 5 — Plan UAT | `/pasture:user-uat` |
| 6 — Ratification | `/pasture:architect-ratify` |
| 7 — Handoff | `/pasture:architect-handoff` |
| 8 — IMPL_PLAN | `/pasture:supervisor-plan-tasks` |
| 9 — SLICE-N | `/pasture:supervisor-spawn-worker`, `/pasture:worker-implement`, `/pasture:worker-complete`, `/pasture:impl-slice` |
| 10 — Code Review | `/pasture:impl-review`, `/pasture:reviewer-review-code`, `/pasture:reviewer-vote` |
| 11 — Impl UAT | `/pasture:user-uat` |
| 12 — Landing | `/pasture:supervisor-commit` |
| Follow-up (post-10) | `/pasture:supervisor` (create FOLLOWUP_URE/URD), `/pasture:architect` (FOLLOWUP_PROPOSAL via h6), `/pasture:supervisor-plan-tasks` (FOLLOWUP_IMPL_PLAN), `/pasture:supervisor-spawn-worker` (FOLLOWUP_SLICE-N), `/pasture:impl-review` (follow-up code review) |

---

## References

- [AGENTS.md](AGENTS.md) — Role taxonomy and handoff procedures
- [PROCESS.md](PROCESS.md) — Step-by-step workflow execution (single source of truth)
- [CONSTRAINTS.md](CONSTRAINTS.md) — Coding standards and naming conventions
