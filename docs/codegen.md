# Pasture Codegen Pipeline

This document is the **conceptual overview** of the pasture code-generation
pipeline: what it is, why it exists, the data flow, and the marker-region model.
For step-by-step "how do I add a constraint / role / phase / skill / section"
recipes, see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Why codegen at all

The Pasture Protocol is a body of structured facts: 12 phases, a handful of
roles, ~30 constraints, commands, figures, checklists, and the prose bodies of
each role/command skill. Those same facts have to appear, consistently, in
**three** different shipped artefacts:

- `schema.xml` — the canonical machine-readable protocol schema;
- `skills/<dir>/SKILL.md` — the Claude Code skill files agents load at runtime;
- `agents/<role>.md` — the Claude Code agent definitions.

Hand-maintaining the same facts in three formats guarantees drift: a constraint
reworded in `schema.xml` but stale in two SKILL.md copies, a phase renamed in one
place and not the others. Codegen makes the facts **single-source** — declared
once as typed Go values — and renders all three artefacts from them. Drift
becomes impossible by construction, and a test suite asserts the data is
complete and the outputs are in sync.

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
     specs_data.go ............ phases, roles, constraints,
     specs_data_body*.go ...... commands, figures, checklists,
     specs_data_fragments.go .. skill-body prose, shared fragments
     context.go ............... role↔constraint, phase↔constraint maps
            │
            │   go generate ./internal/codegen/...
            │   (runs `go run ../../tools/codegen`, see codegen.go)
            ▼
   ┌─────────────────────────────────────────────────┐
   │  tools/codegen/main.go — 4 stages, in order       │
   ├─────────────────────────────────────────────────┤
   │  1. GenerateSchemaToFile ───────────────────────►  schema.xml          (root; full overwrite)
   │  2. GenerateSkill        ───────────────────────►  skills/<role>/SKILL.md   (marker-bounded)
   │       for each role in roleSkillDirs               │
   │  3. GenerateSubSkill     ───────────────────────►  skills/<dir>/SKILL.md    (marker-bounded)
   │       for each command in commandSkillDirs         │
   │  4. GenerateAgent        ───────────────────────►  agents/<role>.md     (full overwrite)
   │       for each role with non-empty Tools           │
   └─────────────────────────────────────────────────┘
            │
            ▼
   go test ./internal/codegen/...   (YAML-fixture tests assert completeness + sync)
```

The single command, run from anywhere in the module:

```bash
go generate ./internal/codegen/...
```

It locates the module root by walking up to `go.mod`, then runs the four stages
above. Each stage prints what it wrote (and a unified diff for `schema.xml` if it
changed). Nothing else triggers generation — there is one entry point.

## The four stages

| # | Function | Output | Overwrite model |
|---|----------|--------|-----------------|
| 1 | `GenerateSchemaToFile` | `schema.xml` (17 sections) | full file |
| 2 | `GenerateSkill` | `skills/<role>/SKILL.md` | **marker-bounded** (header + body) |
| 3 | `GenerateSubSkill` | `skills/<dir>/SKILL.md` (commands) | **marker-bounded** |
| 4 | `GenerateAgent` | `agents/<role>.md` | full file |

Stages 2–3 render `templates/skill.go.tmpl` / `templates/skill_sub.go.tmpl`;
stage 4 renders `templates/agent_definition.go.tmpl`. All three templates pull
their data from the spec maps via context structs assembled in `skills.go` /
`agents.go`.

## The marker-region model (SKILL.md only)

Agent and schema files are **fully generated** — the generator owns the whole
file. SKILL.md files are different: they have a **generated header/body region
plus a hand-authored tail**. The generator only rewrites the region between:

```
<!-- BEGIN GENERATED FROM pasture schema -->
   ... generated content (replaced on every run) ...
<!-- END GENERATED FROM pasture schema -->
   ... hand-authored prose below the END marker is preserved verbatim ...
```

`ReplaceMarkerRegion` (see `markers.go`) swaps only the inside of the markers, so
contributors can keep human-written guidance after the END marker without it
being clobbered by `go generate`. A new role/command skill must be created with
at least an empty BEGIN/END marker pair before its first generation.

After rendering, `ValidateSkillStructure` parses the result with goldmark and
fails on malformed heading hierarchy (duplicate H2 titles, orphan H3s), so a
broken template can't ship a malformed skill.

## Source-of-truth files (where the facts live)

| File | Holds |
|------|-------|
| `internal/codegen/specs_data.go` | the canonical maps: `PhaseSpecs`, `RoleSpecs`, `ConstraintSpecs`, `CommandSpecs`, `FigureSpecs`, `ChecklistSpecs`, `WorkflowSpecs`, … |
| `internal/codegen/specs_data_body*.go` | `SkillBodySpecs` — the prose body of each skill (one file per skill body) |
| `internal/codegen/specs_data_fragments.go` | `SharedFragmentSpecs` — prose/behaviour fragments reused across skill bodies by ID |
| `internal/codegen/context.go` | `roleConstraints` / `phaseConstraints` — which constraints attach to which roles/phases |
| `internal/codegen/specs.go` | the Go struct definitions for every spec type + `AllFragmentIds` |
| `internal/codegen/templates/*.go.tmpl` | the three output templates |
| `tools/codegen/main.go` | the `go:generate` entry point; `roleSkillDirs` + `commandSkillDirs` decide which skills get generated |

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
- **Constraint-set exactness** — `context.yaml` pins the exact constraint count
  per role/phase, so adding a constraint without updating the fixture fails
  immediately (drift gate).
- **Output sync** — schema/skill/agent generation fixtures assert the rendered
  shape.

This is why every codegen change in CONTRIBUTING.md ends with "update the
fixture, then `go generate` + `go test`": the fixtures are the contract.

## See also

- [CONTRIBUTING.md](../CONTRIBUTING.md) — the operational recipes (add a
  constraint / role / phase / schema section / command / template; the CDATA
  exception; test fixtures).
- [AGENTS.md](../AGENTS.md) — coding standards, including the
  References & Internal Identifiers rule (the codegen pipeline is one of its
  explicit exceptions, since the protocol *is* its subject matter).
