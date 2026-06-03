# Pasture Protocol Constraints

Common constraints referenced by all agent and skill files.

## Coding Standards

**Given** shared resources **when** modifying **then** use atomic operations with timeouts **should never** check-then-act

**Given** external input **when** parsing **then** validate at system boundaries with the project's schema/validation tooling **should never** trust raw input or cast types unsafely

**Given** parallel work **when** assigning files **then** ensure each file has exactly one owner with atomic commits **should never** have multiple workers on same file

**Given** a feature request **when** writing requirements **then** use Given/When/Then/Should,Should Not format **should never** write vague criteria

**Given** a class or struct with dependencies **when** designing **then** inject all deps (including clocks, loggers) **should never** hard-code

**Given** runtime events **when** logging **then** use structured logging with context **should never** log secrets or use unstructured print statements

**Given** status/type fields **when** defining **then** use strongly-typed enums **should never** use bare strings at API boundaries

**Given** an error, exception, or user-facing message **when** creating or raising **then** make it actionable: describe (1) what went wrong, (2) why it happened, (3) where it failed (file location, module, or function), (4) when it failed (step, operation, or timestamp), (5) what it means for the caller, and (6) how to fix it **should never** raise generic or opaque error messages (e.g., "invalid input", "operation failed") that don't guide the user toward resolution

**Given** code changes **when** committing **then** type checking and tests must pass **should never** allow optional CI

**Given** task is implemented **when** you are about to commit **then** you **should** use `git agent-commit -m ...`, **should never** use `git commit -m ...`

**Given** you want to execute Beads **when** you are about to call `bd <command> ...` **then** you **should never** `cd <repo_root> && bd <command> ...`, instead you **should** always just call `bd <command> ...`

## Workflow Constraints

**Given** per-slice code review **when** evaluating review results **then** iterate review → fix → re-review with **NO cycle cap** until a fix-free clean round confirms 0 BLOCKER + 0 IMPORTANT + 0 MINOR **should never** close a wave on a fix-applying round, proceed with ANY finding outstanding, or impose a maximum review-cycle cap

**Given** UAT (Phase 5 or Phase 11) feedback **when** recording each item **then** assign an explicit, user-confirmed FIX-NOW or DEFER disposition — DEFER'd items are the SOLE source feeding the FOLLOWUP epic **should never** leave a UAT item without a confirmed disposition, or route a review severity into FOLLOWUP

**Given** a user-interview phase (URE, Plan UAT, Impl UAT) **when** conducting it **then** invoke the matching interview skill (`Skill(/pasture:user-elicit)` or `Skill(/pasture:user-uat)`) so verbatim-capture and disposition procedures load **should never** conduct an interview phase without invoking its skill

**Given** a REQUEST whose intent is to FIX existing behavior (recognized semantically — no request-type axis/enum) **when** eliciting, acceptance-testing, or implementing **then** elicit concrete validation cases, confirm them with the user in UAT, evaluate the fix against them, and store failing real-data cases as fixtures **should never** ship a fix without validation cases

**Given** decomposing a RATIFIED plan into slices (Phase 8) **when** ordering them **then** prefer an interface-first FOUNDATION slice exporting all shared identifiers that lands green before dependent slices (Strong SHOULD); justify any linear decomposition explicitly in the IMPL_PLAN **should never** leave cross-slice contracts implicit

**Given** a vertical slice **when** decomposing it into leaf tasks **then** create one or more leaves named after the real work units — a slice may have ANY number (the L1/L2/L3 triple is one illustrative shape, not a required count) **should never** force every slice into a fixed L1/L2/L3 triple, or create a slice with no leaf tasks

**Given** an actor-change transition **when** authoring the handoff **then** author it inline in its own HANDOFF Beads task body and locate it by task ID **should never** write a `.git/.aura/handoff/...` file (that filesystem pattern is retired)

## Checklists

### Security
- No secrets in code or logs
- Input validated at all boundaries
- No SQL/command injection vectors
- File permissions 0o600 for sensitive data

### Scalability
- No N+1 queries or unbounded loops
- All collections have bounded sizes
- Resource cleanup (timeouts, `defer`/`finally`, `.Close()`)

### Correctness
- Tests cover happy path AND error cases
- Types strict (no `any`, proper discriminated unions or typed enums)
- BDD acceptance criteria met
- Production code path verified via code inspection

## Vote Levels

| Vote | Meaning |
|------|---------|
| ACCEPT | No BLOCKER issues; all review criteria satisfied |
| REVISE | BLOCKER issues found; must provide actionable feedback |

Binary only. No intermediate levels.

## Issue Severity

| Severity | When to Use | Must reach 0 before wave close? |
|----------|-------------|--------------------------------|
| BLOCKER | Security, type errors, test failures, broken production code paths | Yes (also blocks the slice — dual-parent) |
| IMPORTANT | Performance, missing validation, architectural concerns | Yes — fixed in-wave, NOT deferrable |
| MINOR | Style, optional optimizations, naming improvements | Yes — fixed in-wave, NOT deferrable |

**Clean-review exit (no cycle cap):** A code-review wave iterates review → fix → re-review with **NO maximum cycle cap** until a **fix-free clean round** confirms **0 BLOCKER + 0 IMPORTANT + 0 MINOR** from all reviewers. A clean round applies no fixes and finds nothing across all three severities. The wave MUST end on a review wave (never proceed after a worker wave without a clean re-review). No review severity is deferrable to FOLLOWUP.

**EAGER severity group creation:** For every code review round (Phase 10), ALWAYS create 3 severity group tasks (BLOCKER, IMPORTANT, MINOR) immediately. Empty groups have no children and are closed immediately. This is NOT lazy creation.

**Follow-up epic:** Created at **UAT (Phase 5 or Phase 11)** when the user DEFERs one or more items. The FOLLOWUP epic is fed **ONLY** by user-DEFER'd UAT items — **never** by a review severity (BLOCKER/IMPORTANT/MINOR), since all review findings must reach 0 before wave close. FIX-NOW items are resolved in the current wave. Owner: Supervisor (label `pasture:epic-followup`).

**Follow-up lifecycle:** The follow-up epic runs the same protocol phases with FOLLOWUP_* prefixed task types: FOLLOWUP_URE → FOLLOWUP_URD → FOLLOWUP_PROPOSAL → FOLLOWUP_IMPL_PLAN → FOLLOWUP_SLICE-N. The supervisor creates FOLLOWUP_URE and FOLLOWUP_URD, then hands off to architect via h6 for FOLLOWUP_PROPOSAL. User-DEFER'd UAT-item leaf tasks are adopted by FOLLOWUP_SLICE-N (dual-parent: original DEFER'd-items tracking group + follow-up slice).

**No followup-of-followup:** Any user-DEFER'd items from a follow-up UAT are tracked on the existing follow-up epic as tasks. A nested follow-up-of-followup epic is never created.

## Beads Task Naming & Tagging Standards

All work flows through Beads with standardized titles and the v2 label schema:

### Label Schema

```
Format: pasture:p{phase}-{domain}:s{step}-{type}

Phase-domain pairs (12 phases):
  pasture:p1-user     — Request + classify + research + explore
  pasture:p2-user     — Elicit + URD
  pasture:p3-plan     — Propose
  pasture:p4-plan     — Plan review
  pasture:p5-user     — Plan UAT
  pasture:p6-plan     — Ratify
  pasture:p7-plan     — Handoff
  pasture:p8-impl     — Impl plan
  pasture:p9-impl     — Worker slices
  pasture:p10-impl    — Code review
  pasture:p11-user    — Impl UAT
  pasture:p12-impl    — Landing

Special labels:
  pasture:urd                  — User Requirements Document
  pasture:superseded           — Superseded proposal/plan
  pasture:severity:blocker     — Blocker severity group
  pasture:severity:important   — Important severity group
  pasture:severity:minor       — Minor severity group
  pasture:epic-followup        — Follow-up epic
```

### Planning Phase Tasks

| Title Format | Label | Purpose | Created By |
|---|---|---|---|
| `REQUEST: Description` | `pasture:p1-user:s1_1-classify` | Capture user's problem statement | User or Coordinator |
| `PROPOSAL-N: Description` | `pasture:p3-plan:s3-propose` | Architect's full technical proposal (N increments per revision) | Architect |
| `PROPOSAL-N-REVIEW-{axis}-{round}: Description` | `pasture:p4-plan:s4-review` | Reviewer assessment of proposal N (axis=A/B/C, round=1,2,...) | Reviewers (spawned by architect) |
| `URD: Description` | `pasture:urd` | Single source of truth for user requirements | Architect (after Phase 2 URE) |

### Implementation Phase Tasks

| Title Format | Label | Ownership |
|---|---|---|
| `IMPL_PLAN: Description` | `pasture:p8-impl:s8-plan` | Supervisor |
| `SLICE-N: Description` | `pasture:p9-impl:s9-slice` | One worker per slice |

**Vertical Slice Ownership Model:**
- Each **production code path** is owned by exactly ONE worker
- A worker owns the full vertical (types → tests → implementation → wiring)
- Never assign the same production code path to multiple workers
- Workers CAN share Layer 0 infrastructure (common types/enums)

### Naming Conventions

- **PROPOSAL-N:** N starts at 1 and increments with each revision. Old proposals are marked `pasture:superseded`.
- **PROPOSAL-N-REVIEW-{axis}-{round}:** Axis identifies the reviewer's criteria focus (A=Correctness, B=Test quality, C=Elegance). Round increments per re-review cycle.
- **SLICE-N:** N identifies the slice number within the implementation plan.

### Follow-up Lifecycle Tasks

| Title Format | Label | Purpose | Created By |
|---|---|---|---|
| `FOLLOWUP: Description` | `pasture:epic-followup` | Follow-up epic for user-DEFER'd UAT items | Supervisor |
| `FOLLOWUP_URE: Description` | `pasture:p2-user:s2_1-elicit` | Scoping URE: which user-DEFER'd UAT items to address | Supervisor |
| `FOLLOWUP_URD: Description` | `pasture:p2-user:s2_2-urd,pasture:urd` | Requirements document for follow-up scope | Supervisor |
| `FOLLOWUP_PROPOSAL-N: Description` | `pasture:p3-plan:s3-propose` | Architect's follow-up proposal (accounts for original URD + FOLLOWUP_URD) | Architect (after h6) |
| `FOLLOWUP_IMPL_PLAN: Description` | `pasture:p8-impl:s8-plan` | Follow-up implementation plan | Supervisor (after follow-up h1) |
| `FOLLOWUP_SLICE-N: Description` | `pasture:p9-impl:s9-slice` | Follow-up slice (adopts user-DEFER'd UAT-item leaf tasks as dual-parent children) | Supervisor |

### Frontmatter References

Instead of peer-reference commands, include task IDs in the description frontmatter:

```bash
bd create --title "URD: Feature name" \
  --description "---
references:
  request: <request-task-id>
  elicit: <elicit-task-id>
---
## Requirements
..."
```

This replaces the old peer-reference command for linking related tasks. The URD is a living reference document — not a blocking dependency.

### Design Field Schema (Canonical)

All implementation tasks use this structure in the `design` field:

```json
{
  "productionCodePath": "tool feature list",
  "validation_checklist": [
    "Type checking passes",
    "Tests pass",
    "Production code path implemented (not test-only export)",
    "Tests verify actual production code (CLI/API users will run)",
    "All TODO placeholders replaced with working code",
    "Production code verified (via code inspection: no TODOs, real deps wired)"
  ],
  "acceptance_criteria": [
    {
      "given": "implementation complete",
      "when": "user runs production code",
      "then": "it works (not just tests passing)",
      "should_not": "have separate test-only code paths or dual-export anti-pattern"
    }
  ],
  "tradeoffs": [
    {
      "decision": "chosen approach",
      "rationale": "why this over alternatives"
    }
  ],
  "ratified_plan": "<task-id>"
}
```

---

## User Requirements Document (URD)

**Given** Phase 2 (URE) completes **when** creating the URD **then** use label `pasture:urd` and include structured requirements (priorities, design choices, MVP goals, end-vision goals) **should never** leave requirements scattered across REQUEST and ELICIT tasks without a URD

**Given** a URD exists **when** linking to other tasks **then** include the URD task ID in the description frontmatter of referencing tasks (e.g., `urd: <urd-id>`) **should never** use `--blocked-by` for URD links — it is a reference document, not a blocking dependency

**Given** scope changes at any phase **when** updating requirements **then** add a comment to the URD via `bd comments add <urd-id> "..."` **should never** leave the URD out of date when UAT results, ratification, or user feedback modify requirements

## Documentation Standards

All documentation must follow these patterns:

### Command File Headers

Every `skills/*/SKILL.md` file must start with:

```yaml
---
name: agent-name
description: Brief role/purpose
---

# Agent Name

Brief description of role. See `CONSTRAINTS.md` for coding standards.

**-> [Full workflow in PROCESS.md](PROCESS.md#phase-x)** <- Link to relevant phase
```

### Section Organization

Use consistent structure:
- **Given/When/Then/Should** constraints (borrowed from BDD)
- **Tools & Skills** table (what this agent can do)
- **Common Patterns** with correct/wrong examples
- **See Also** section linking to PROCESS.md

### Code Examples

Always show both:
1. **CORRECT pattern** (preferred approach)
2. **WRONG pattern** (anti-pattern to avoid)

With explanatory comments.

### Linking Convention

**PROCESS.md links:**
```markdown
-> [Full workflow in PROCESS.md](PROCESS.md#phase-1-request)
```

**CONSTRAINTS.md links:**
```markdown
See `CONSTRAINTS.md` for [section name]
```

**Cross-file references in commands:**
```markdown
See: [../agent/SKILL.md](../agent/SKILL.md)
```

---

## References

See also:
- [PROCESS.md](PROCESS.md) - Step-by-step workflow execution (single source of truth)
- [HANDOFF_TEMPLATE.md](HANDOFF_TEMPLATE.md) - Standardized handoff document template
- [MIGRATION_v1_to_v2.md](MIGRATION_v1_to_v2.md) - Migration procedure from v1 to v2 labels
- `skills/` - Detailed agent role definitions
