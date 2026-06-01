# Contributing to Pasture Codegen

## Architecture Overview

`specs_data.go` is the single source of truth. All three generators — schema.xml,
SKILL.md headers, and agent definitions — are driven from the canonical data maps
declared there. Edits flow in one direction: change a map entry, run `go generate`,
inspect the diff, run tests.

The pipeline has four stages:

1. **GenerateSchemaToFile** — marshals spec maps → `schema.xml` (17 sections)
2. **GenerateSkill** — single unified pass → `skills/{role}/SKILL.md` (marker-bounded):
   - `ReplaceMarkerRegion` renders `templates/skill.go.tmpl` between the BEGIN/END markers (header + body in one pass)
   - `ValidateSkillStructure` validates heading hierarchy via goldmark (duplicate H2 titles, orphan H3 headings)
3. **GenerateSubSkill** — single unified pass → `skills/{dir}/SKILL.md` using `templates/skill_sub.go.tmpl`
4. **GenerateAgent** — renders `templates/agent_definition.go.tmpl` → `agents/{role}.md` (fully overwritten)

The entry point is `tools/codegen/main.go`, invoked by:

```
go generate ./internal/codegen/...
```

---

## File Map

| File | Purpose | When to touch it |
|------|---------|-----------------|
| `internal/codegen/specs_data.go` | All canonical data maps (`PhaseSpecs`, `ConstraintSpecs`, `RoleSpecs`, `CommandSpecs`, `HandoffSpecs`, `FigureSpecs`, `ChecklistSpecs`, `CoordinationCommands`, `WorkflowSpecs`, `ReviewAxisSpecs`, `ProcedureSteps`, `LabelSpecs`, `TitleConventions`, `SubstepDataMap`) | Adding/changing any protocol concept |
| `internal/codegen/specs_data_body.go` | `SkillBodySpecs` map: body content for all skill SKILL.md files (preamble, sections, recipes, behaviors). Source of truth for body content inside the BEGIN/END markers. | Adding/changing skill body prose |
| `internal/codegen/specs.go` | Go type definitions for all spec structs (`ConstraintSpec`, `RoleSpec`, `PhaseSpec`, etc.) | Adding a new field to any spec struct |
| `internal/codegen/context.go` | `generalConstraints`, `roleConstraints`, `phaseConstraints` maps; `GetRoleContext`, `GetPhaseContext` | Adding/removing a constraint-role or constraint-phase association |
| `internal/codegen/schema.go` | `generateSchemaContent`, `sections` slice, `buildConstraints` and `buildProcedureSteps` (manual CDATA builders), `marshalSection` helper | Adding a new schema section or modifying CDATA sections |
| `internal/codegen/schema_types.go` | `encoding/xml` annotated structs for 15 marshallable sections; doc-only structs for the 2 CDATA sections | Adding a new schema section's XML shape |
| `internal/codegen/skills.go` | `GenerateSkill`, `GenerateSubSkill`, `skillContext`, `skillSubContext`, figure-loading helpers | Changing SKILL.md generation logic or template context shape |
| `internal/codegen/agents.go` | `GenerateAgent`, `agentTemplateData`, `renderAgent` | Changing agent definition generation logic or template context shape |
| `internal/codegen/templates/skill.go.tmpl` | Unified SKILL.md template (header + body: role commands, constraints, handoffs, phases, checklists, workflows, figures, preamble, behaviors, sections, recipes) | Changing the layout of generated SKILL.md files |
| `internal/codegen/templates/skill_sub.go.tmpl` | Sub-skill SKILL.md template (command name, description, figures, preamble, behaviors, sections, recipes) | Changing sub-skill layout |
| `internal/codegen/templates/agent_definition.go.tmpl` | Agent definition template (role spec, phases, constraints, behaviors, checklists, workflows, figure refs) | Changing agent definition layout |
| `tools/codegen/main.go` | `go:generate` entry point; `roleSkillDirs` and `commandSkillDirs` maps | Adding a new role or command that needs skill generation |
| `internal/codegen/testdata/context.yaml` | YAML fixture for `context_test.go` — exact constraint counts and must_contain/must_not_contain per role/phase | Any change to `roleConstraints` or `phaseConstraints` |
| `internal/codegen/testdata/skills.yaml` | YAML fixture for skill generation tests | Adding/removing roles or commands in skill generation |
| `internal/codegen/testdata/agents.yaml` | YAML fixture for agent generation tests | Adding/removing roles in agent generation |
| `internal/codegen/testdata/schema.yaml` | YAML fixture for schema generation tests | Adding/removing schema sections |
| `internal/codegen/testdata/markers.yaml` | YAML fixture for marker region tests | Changing BEGIN/END marker logic |

---

## Regeneration

The single regeneration command (run from anywhere inside the module):

```bash
go generate ./internal/codegen/...
```

This runs `go run ../../tools/codegen` from the `internal/codegen/` package directory.
The binary locates the module root by walking upward from cwd to find `go.mod`.

What it does, in order:
1. Writes `schema.xml` (diff printed to stdout if changed)
2. Writes `skills/{role}/SKILL.md` headers for each role in `roleSkillDirs`
3. Writes `skills/{dir}/SKILL.md` headers for each command in `commandSkillDirs`
4. Writes `agents/{role}.md` for each role with non-empty `Tools`

### Changed X → regenerates Y

| What you changed | Regenerates |
|-----------------|-------------|
| Any map in `specs_data.go` | schema.xml, SKILL.md headers, agent definitions (all 4 stages) |
| `specs_data_body.go` (`SkillBodySpecs`) | SKILL.md body content (stages 2–3) |
| `context.go` (`roleConstraints` / `phaseConstraints`) | SKILL.md headers, agent definitions (stages 2–4 only) |
| `schema_types.go` | schema.xml only (stage 1) |
| `schema.go` section builders | schema.xml only (stage 1) |
| `templates/skill.go.tmpl` | SKILL.md role files (stage 2) |
| `templates/skill_sub.go.tmpl` | SKILL.md sub-skill files (stage 3) |
| `templates/agent_definition.go.tmpl` | agent definitions (stage 4) |
| `tools/codegen/main.go` `roleSkillDirs` | which SKILL.md files are regenerated |
| `tools/codegen/main.go` `commandSkillDirs` | which sub-skill SKILL.md files are regenerated |

After regenerating, run:

```bash
go test ./internal/codegen/... -count=1
```

---

## Recipes

### Adding a Constraint

1. Add an entry to `ConstraintSpecs` in `specs_data.go` (lines starting at `// ─── ConstraintSpecs`, around line 200). Provide `ID`, `Given`, `When`, `Then`, `ShouldNot`, and optional `Command` and `Examples`.

2. If the constraint is universal (all roles, all phases), add it to `generalConstraints` in `context.go`. Otherwise, add its ID to the relevant role sets in `roleConstraints` and/or phase sets in `phaseConstraints`.

3. Update `testdata/context.yaml`: increment `exact_count` for each role or phase that gains the constraint, and add the ID to `must_contain` lists.

4. Run `go generate ./internal/codegen/...` then `go test ./internal/codegen/... -count=1`.

---

### Adding a Constraint to an Existing Role

This is a subset of the above when the `ConstraintSpec` already exists.

1. In `context.go`, add the constraint ID to `roleConstraints[types.RoleXxx]` with value `true`.

2. In `testdata/context.yaml`, find the entry for that role:
   - Increment `exact_count` by 1.
   - Add the constraint ID to `must_contain`.
   - Remove it from `must_not_contain` if it was listed there.

3. Run `go generate ./internal/codegen/...` then `go test ./internal/codegen/... -count=1`.

---

### Adding a Figure

1. Add an entry to `FigureSpecs` in `specs_data.go` (around line 1161). Set `ID`, `Title`, `Type` (e.g. `"ascii-diagram"`), `RoleRefs`, `SectionRef` (e.g. `"workflows"`), and optionally `WorkflowRefs` or `CommandRefs`.

2. Create a YAML file at `skills/protocol/figures/{id}.yaml` with a `content` key containing the ASCII diagram text. This is loaded at generation time by `loadFigureContent` in `skills.go`.

3. Run `go generate ./internal/codegen/...`. The figure will appear automatically in SKILL.md files (via `skill.go.tmpl`) and agent definition figure-ref lists (via `agent_definition.go.tmpl`) for the referenced roles.

4. Run `go test ./internal/codegen/... -count=1`. Update `testdata/agents.yaml` if fixture checks figure references.

---

### Adding a New Role

1. Add a `RoleId` constant to `internal/types/` (wherever `RoleId` values are declared) and add it to `AllRoleIds`.

2. Add an entry to `RoleSpecs` in `specs_data.go` (around line 535). Fill `ID`, `Name`, `Description`, `OwnedPhases`, `Introduction`, `OwnershipNarrative`, `Behaviors`, and optionally `Model`, `Thinking`, `Tools`.

3. Add a `roleConstraints` entry in `context.go` using `mergeConstraints(generalConstraints, map[string]bool{...})`.

4. Add the role to `roleSkillDirs` in `tools/codegen/main.go` (map the `types.RoleId` constant to the directory name under `skills/`).

5. Create `skills/{dir}/SKILL.md` with at least the BEGIN/END marker pair:
   ```
   <!-- BEGIN GENERATED FROM pasture schema -->
   <!-- END GENERATED FROM pasture schema -->
   ```

6. Update `testdata/context.yaml`: add a `role_constraint_checks` entry with `exact_count`, `must_contain`, and `must_not_contain`.

7. Update `testdata/agents.yaml` and `testdata/skills.yaml` as needed for the new role.

8. Run `go generate ./internal/codegen/...` then `go test ./internal/codegen/... -count=1`.

---

### Adding a New Phase

1. Add a `PhaseId` constant to `pkg/protocol/` and add it to `AllPhaseIds`.

2. Add an entry to `PhaseSpecs` in `specs_data.go` (around line 17). Set `ID`, `Name`, `Number` (must be unique, 1–12 range extended if needed), `Domain`, `OwnerRoles`, and `Transitions`.

3. Add a `phaseConstraints` entry in `context.go` using `mergeConstraints(generalConstraints, map[string]bool{...})` or `copyConstraints(generalConstraints)`.

4. Update `phaseOrder` in `schema.go` (around line 63) to include the new phase in the canonical ordering.

5. Update `testdata/context.yaml`: add a `phase_constraint_checks` entry for the new phase.

6. Run `go generate ./internal/codegen/...` then `go test ./internal/codegen/... -count=1`.

---

### Adding a New Schema Section

1. Define the XML struct types in `schema_types.go` with `xml:` struct tags. Follow the existing pattern: a top-level `*Section` struct with `XMLName xml.Name`, and nested element types for children.

2. Write a `build{Name}` function in `schema.go` with the signature `func build{Name}(buf *bytes.Buffer, depth int)`. Use `marshalSection(buf, depth, ...)` to marshal the top-level struct.

   Exception: if your section requires `<![CDATA[...]]>` content (e.g. `<code>` blocks), you must use manual `fmt.Fprintf(buf, ...)` instead of `marshalSection`. See `buildConstraints` and `buildProcedureSteps` in `schema.go` for the pattern.

3. Add a `{comment, build{Name}}` entry to the `sections` slice in `generateSchemaContent` (around line 1576 in `schema.go`).

4. Run `go generate ./internal/codegen/...` then `go test ./internal/codegen/... -count=1`. Update `testdata/schema.yaml` to cover the new section.

---

### Adding a New Command / Skill

1. Add an entry to `CommandSpecs` in `specs_data.go` (around line 786). Set `ID`, `Name` (e.g. `"pasture:role:action"`), `Description`, `RoleRef`, `Phases`, `File`, and optionally `CreatesLabels`.

2. If the command has associated figures (i.e., a `FigureSpec` entry references this command via `CommandRefs`), add it to `commandSkillDirs` in `tools/codegen/main.go`:
   ```go
   "cmd-your-id": "your-skill-dir",
   ```

3. Create `skills/{dir}/SKILL.md` with the BEGIN/END marker pair.

4. Update `testdata/skills.yaml` to cover the new command.

5. Run `go generate ./internal/codegen/...` then `go test ./internal/codegen/... -count=1`.

---

### Modifying a Template

1. Edit the `.go.tmpl` file in `internal/codegen/templates/`:
   - `skill.go.tmpl` — unified role SKILL.md template. Context type: `skillContext` (declared in `skills.go`). Available fields: `Role` (RoleSpec), `Commands` ([]CommandSpec), `Constraints` ([]ConstraintSpec), `Handoffs` ([]HandoffSpec), `OwnedPhases`, `PhasesDetail`, `Steps`, `PhaseSlug`, `SubSkills`, `Introduction`, `OwnershipNarrative`, `Behaviors`, `Checklists`, `CoordinationCommands`, `Workflows`, `FiguresByWorkflow`, `ReviewAxes`, `Preamble`, `BodyBehaviors`, `BodySections`, `BodyRecipes`.
   - `skill_sub.go.tmpl` — sub-skill template. Context type: `skillSubContext`. Available fields: `CommandName`, `CommandDescription`, `Figures`, `Preamble`, `BodySections`, `BodyRecipes`, `BodyBehaviors`.
   - `agent_definition.go.tmpl` — agent definitions. Context type: `agentTemplateData`. Available fields: `Role` (RoleSpec), `PhasesDetail`, `PhaseSlug`, `Constraints`, `Behaviors`, `Checklists`, `Workflows`, `Figures`.

2. Run `go generate ./internal/codegen/...` to preview the rendered output.

3. Template functions available in all templates: `join(items []string, sep string)`, `lower(s string)`, `last(i, length int) bool`, `not(b bool) bool`.

---

## The CDATA Exception

Two of the 17 schema sections cannot use `encoding/xml` marshalling:

- **`buildConstraints`** — `<constraint>` elements contain `<example><code><![CDATA[...]]></code></example>`. The CDATA wrapper is required by the Python reference implementation and cannot be produced by `encoding/xml`.
- **`buildProcedureSteps`** — `<step>` elements may contain `<example><code><![CDATA[...]]></code></example>` for the same reason.

Both functions use `fmt.Fprintf(buf, ...)` to write XML directly. The corresponding struct types (`ConstraintsSection`, `ProcedureStepsSection`) are defined in `schema_types.go` for documentation and type-safety only — they carry a `// NOT used for xml.Marshal.` comment and have no `XMLName xml.Name` field.

The remaining 15 sections use `marshalSection(buf, depth, v)` which calls `xml.MarshalIndent`.

---

## Test Fixtures

Tests in `internal/codegen/` are YAML-driven. Fixtures live in `testdata/`.

### Pattern

Each `*_test.go` file declares a Go struct that mirrors the YAML shape, loads the fixture with `testutil.LoadFixtures`, and runs table-driven subtests. Fixture keys use snake_case.

### `context.yaml` — constraint set assertions

Used by `context_test.go` (`TestGetRoleContext_ConstraintSets`, `TestGetPhaseContext_ConstraintSets`).

Each entry specifies:
- `role` or `phase`: the subject
- `exact_count`: the total number of constraints returned. This is a drift-detection gate — if you add a constraint to a role/phase without updating this value, the test fails immediately.
- `must_contain`: constraint IDs that must appear in the result
- `must_not_contain`: constraint IDs that must not appear (guards against accidental inclusion)

When you add a constraint to a role or phase, you must increment `exact_count` and add the ID to `must_contain`. When you remove one, decrement and move to `must_not_contain`.

### `specs_test.go` — completeness tests

`TestPhaseSpecsCompleteness` verifies every `PhaseId` (except `PhaseComplete`) has an entry in `PhaseSpecs`, with non-empty `Name`, `Domain`, `OwnerRoles`, and `Transitions`.

`TestRoleSpecsCompleteness` verifies every `RoleId` in `types.AllRoleIds` has an entry in `RoleSpecs` with non-empty required fields.

`TestPhaseSpecsNumbering` verifies phase numbers 1–12 with no duplicates.

These tests act as compile-time-equivalent completeness guards for the data maps. When you add a new role or phase, they will fail until you add the corresponding map entry.

### Running tests

```bash
nix-shell -p go --run "go test ./internal/codegen/... -count=1"
```
