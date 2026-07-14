# Pasture Codegen Pipeline

This document is the **conceptual overview** of the pasture code-generation
pipeline: what it is, why it exists, the data flow, and the marker-region model.
For step-by-step "how do I add a constraint / role / phase / skill / section"
recipes, see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Why codegen at all

The Pasture Protocol is a body of structured facts: 12 phases, a handful of
roles, ~30 constraints, commands, figures, checklists, and the prose bodies of
each role/command skill. Those same facts have to appear consistently across the
shipped protocol schema and two runtime harnesses:

- `schema.xml` — the canonical machine-readable protocol schema;
- `skills/<dir>/SKILL.md` and `agents/<role>.md` — Claude Code skills and agents;
- `.opencode/skill/<dir>/SKILL.md`, `.opencode/agent/<role>.md`, and
  `opencode.json` — OpenCode skills, agents, and manifest.

Hand-maintaining the same facts in three formats guarantees drift: a constraint
reworded in `schema.xml` but stale in two SKILL.md copies, a phase renamed in one
place and not the others. Codegen makes the facts **single-source** — declared
once as typed Go values — and renders every harness from them. Registry tests
reject incomplete inventories, and CI rejects committed generated-output drift.

A second reason it is **typed Go** rather than a data file (YAML/JSON): the specs
are validated by the Go compiler and by completeness tests (every `RoleId` has a
`RoleSpec`, every `PhaseId` has a `PhaseSpec`, phase numbers are unique, every
referenced fragment ID resolves). Externalising the specs to data would discard
those compile-time and exact-count guarantees — which is the whole point.

## Data flow

```
   SOURCE OF TRUTH (typed Go)                GENERATOR                 SHIPPED ARTEFACTS
   ─────────────────────────                 ─────────                 ─────────────────

   internal/codegen/
     specs_data.go ............ phases, roles, constraints, commands,
                                figures, checklists, workflows
     specs_data_body.go ....... explicit SkillBodySpecs registry
     specs_data_body_<skill>.go one generated skill body per file
     specs_data_fragments.go .. shared prose/behaviour fragments
     context.go ............... role↔constraint, phase↔constraint maps
            │
            │   make generate
            │   (runs both claude-code and opencode; see codegen.go)
            ▼
   ┌─────────────────────────────────────────────────┐
   │  tools/codegen/main.go                            │
   ├─────────────────────────────────────────────────┤
   │  GenerateSchemaToFile ──────────────────────────► schema.xml
   │  EmitHarness(claude-code) ──────────────────────► skills/, agents/
   │  EmitHarness(opencode) ─────────────────────────► .opencode/, opencode.json
   └─────────────────────────────────────────────────┘
            │
            ▼
   go test ./internal/codegen/...   (registry, fixture, and sync guards)
   CI Codegen Drift                 (clean-tree all-target regeneration)
```

The canonical command, run from the module root:

```bash
make generate
```

`make generate` invokes `go generate ./internal/codegen/...`; the directive
selects both registered targets. The tool locates the module root by walking up
to `go.mod`, then emits the schema and each harness. Each emitter prints what it
wrote (and a unified diff for `schema.xml` if it changed). Ordinary builds do not
trigger generation.

## Core emitters

| # | Function | Output | Overwrite model |
|---|----------|--------|-----------------|
| 1 | `GenerateSchemaToFile` | `schema.xml` (17 sections) | full file |
| 2 | role-skill renderer | Claude Code and OpenCode role skills | marker merge for Claude Code; full file for OpenCode |
| 3 | command-skill renderer | Claude Code and OpenCode command skills | marker merge for Claude Code; full file for OpenCode |
| 4 | agent emitters | Claude Code and OpenCode role agents | full file |

Role and command emitters select the target-specific templates registered in
`harness.go`; agent emitters do the same for their harness. Every template pulls
from context structs assembled in `skills.go` / `agents.go`.

## The marker-region model (Claude Code generated skills only)

Schema, agent, and OpenCode files are **fully generated**. Claude Code generated
skills are seeded with a BEGIN/END marker pair so the generator can safely take
ownership of their frontmatter, heading, and body:

```
<!-- BEGIN GENERATED FROM pasture schema -->
   ... generated content (replaced on every run) ...
<!-- END GENERATED FROM pasture schema -->
```

The low-level `ReplaceMarkerRegion` helper preserves a trailing region for its
generic marker-manipulation callers. `GenerateSkill` and `GenerateSubSkill` are
stricter: every registered skill has a `SkillBodySpecs` entry, so these public
generators render the complete body inside the markers and remove anything
after the END marker. Put maintained prose in that skill's
`specs_data_body_<skill>.go` declaration, not in an on-disk tail. A new Claude
Code role/command skill must have an empty marker pair before first generation;
the OpenCode target needs no seed file.

After rendering, `ValidateSkillStructure` parses the result with goldmark and
fails on malformed heading hierarchy (duplicate H2 titles, orphan H3s), so a
broken template can't ship a malformed skill.

## Source-of-truth files (where the facts live)

| File | Holds |
|------|-------|
| `internal/codegen/specs_data.go` | the canonical maps: `PhaseSpecs`, `RoleSpecs`, `ConstraintSpecs`, `CommandSpecs`, `FigureSpecs`, `ChecklistSpecs`, `WorkflowSpecs`, … |
| `internal/codegen/specs_data_body.go` | the explicit `SkillBodySpecs` registry |
| `internal/codegen/specs_data_body_<skill>.go` | one generated skill's prose body declaration |
| `internal/codegen/specs_data_fragments.go` | `SharedFragmentSpecs` — prose/behaviour fragments reused across skill bodies by ID |
| `internal/codegen/context.go` | `roleConstraints` / `phaseConstraints` — which constraints attach to which roles/phases |
| `internal/codegen/specs.go` | the Go struct definitions for every spec type + `AllFragmentIds` |
| `internal/codegen/templates/*.go.tmpl` | target-specific skill and agent output templates |
| `internal/codegen/harness.go` | harness registry, target routing, and the `roleSkillDirs` + `commandSkillDirs` emitter inventories |
| `tools/codegen/main.go` | thin CLI entry point that resolves target flags and invokes schema/harness generation |

## Define-once, reference-by-ID

Because the whole point is single-sourcing, **never copy a constraint or prose
fragment** into a second skill/agent. To repeat a rule:

- **same constraint in more roles/phases** → add its **ID** to the set in
  `context.go` (`roleConstraints` / `phaseConstraints`); the one `ConstraintSpecs`
  entry then renders into each target's SKILL.md *and* agent definition.
- **same prose/behaviour in more skill bodies** → define it once in
  `SharedFragmentSpecs` and reference it via `fragRef()` / `behaviorRef()`.

A `global_ids` parity check and the `context_test` exact-count assertions fail
the build if a copy drifts or an ID doesn't resolve. Full recipes and worked
examples are in
[CONTRIBUTING.md](../CONTRIBUTING.md#repeating-a-constraint-or-prose-fragment-across-multiple-skillsagents-define-once-reference-by-id).

## Tests as guardrails

`go test ./internal/codegen/...` is YAML-fixture-driven (`testdata/*.yaml`) and
enforces the invariants codegen depends on:

- **Completeness** — every `RoleId`/`PhaseId` has a spec with non-empty required
  fields; phase numbers are unique (1–12).
- **Generated-skill registry parity** — `TestGeneratedSkillRegistryParity`
  requires every generated skill directory to have exactly one metadata entry
  in `CommandSpecs`, one emitter in `roleSkillDirs`/`commandSkillDirs`, and one
  body in `SkillBodySpecs`.
- **Constraint-set exactness** — `context.yaml` pins the exact constraint count
  per role/phase, so adding a constraint without updating the fixture fails
  immediately (drift gate).
- **Output sync** — schema/skill/agent generation fixtures assert the rendered
  shape.

The CI **Codegen Drift** job adds the clean-tree guard: it runs `make generate`
for both committed targets and then `git diff --exit-code`. If it fails, run
`make generate` locally, inspect the generated-path diff, and commit the intended
output alongside the source change.

This is why every codegen change in CONTRIBUTING.md ends with "update the
fixture, then `make generate` + `go test`": the inventories, fixtures, and
committed outputs are the contract.

## See also

- [CONTRIBUTING.md](../CONTRIBUTING.md) — the operational recipes (add a
  constraint / role / phase / schema section / command / template; the CDATA
  exception; test fixtures).
- [AGENTS.md](../AGENTS.md) — coding standards, including the
  References & Internal Identifiers rule (the codegen pipeline is one of its
  explicit exceptions, since the protocol *is* its subject matter).
