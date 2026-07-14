# Contributing to Pasture Codegen

> **New here?** Read [docs/codegen.md](docs/codegen.md) first — it is the
> conceptual overview (what the pipeline is, why it exists, a data-flow diagram,
> and the marker-region model). This file is the operational cookbook: the
> step-by-step recipes for changing each protocol concept. Releasing is covered
> at the [bottom of this file](#releasing).

## Architecture Overview

Typed Go values under `internal/codegen/` are the single source of truth. Protocol
metadata lives in `specs_data.go`, shared prose lives in
`specs_data_fragments.go`, and each generated skill body lives in its own
`specs_data_body_<skill>.go` file. `specs_data_body.go` is the explicit registry
that ties those body declarations together. Edits flow in one direction: change
the canonical Go data, run `make generate`, inspect the diff, then run tests.

The pipeline has four stages:

1. **GenerateSchemaToFile** — marshals spec maps to `schema.xml`.
2. **EmitHarness(claude-code)** — marker-merges all 29 generated skills under
   `skills/` and fully rewrites the five role agents under `agents/`.
3. **EmitHarness(opencode)** — fully rewrites the OpenCode skills, agents, and
   manifest, and copies the two verbatim skills.
4. **ValidateGlobalIds** — rejects unresolved or duplicate protocol identifiers
   after the complete registry has been assembled.

The entry point is `tools/codegen/main.go`, invoked for all committed targets by:

```
make generate
```

---

## File Map

| File | Purpose | When to touch it |
|------|---------|-----------------|
| `internal/codegen/specs_data.go` | All canonical data maps (`PhaseSpecs`, `ConstraintSpecs`, `RoleSpecs`, `CommandSpecs`, `HandoffSpecs`, `FigureSpecs`, `ChecklistSpecs`, `CoordinationCommands`, `WorkflowSpecs`, `ReviewAxisSpecs`, `ProcedureSteps`, `LabelSpecs`, `TitleConventions`, `SubstepDataMap`) | Adding/changing any protocol concept |
| `internal/codegen/specs_data_body.go` | Slim, explicit `SkillBodySpecs` registry: one skill directory key mapped to each body declaration. | Registering or removing a generated skill body |
| `internal/codegen/specs_data_body_<skill>.go` | One `SkillBody` declaration containing that skill's preamble, sections, recipes, and behaviors. | Adding/changing one skill's body prose |
| `internal/codegen/specs.go` | Go type definitions for all spec structs (`ConstraintSpec`, `RoleSpec`, `PhaseSpec`, etc.) | Adding a new field to any spec struct |
| `internal/codegen/context.go` | `generalConstraints`, `roleConstraints`, `phaseConstraints` maps; `GetRoleContext`, `GetPhaseContext` | Adding/removing a constraint-role or constraint-phase association |
| `internal/codegen/schema.go` | `generateSchemaContent`, `sections` slice, `buildConstraints` and `buildProcedureSteps` (manual CDATA builders), `marshalSection` helper | Adding a new schema section or modifying CDATA sections |
| `internal/codegen/schema_types.go` | `encoding/xml` annotated structs for 15 marshallable sections; doc-only structs for the 2 CDATA sections | Adding a new schema section's XML shape |
| `internal/codegen/skills.go` | `GenerateSkill`, `GenerateSubSkill`, `skillContext`, `skillSubContext`, figure-loading helpers | Changing SKILL.md generation logic or template context shape |
| `internal/codegen/agents.go` | `GenerateAgent`, `agentTemplateData`, `renderAgent` | Changing agent definition generation logic or template context shape |
| `internal/codegen/templates/skill.go.tmpl` | Unified SKILL.md template (header + body: role commands, constraints, handoffs, phases, checklists, workflows, figures, preamble, behaviors, sections, recipes) | Changing the layout of generated SKILL.md files |
| `internal/codegen/templates/skill_sub.go.tmpl` | Sub-skill SKILL.md template (command name, description, figures, preamble, behaviors, sections, recipes) | Changing sub-skill layout |
| `internal/codegen/templates/agent_definition.go.tmpl` | Agent definition template (role spec, phases, constraints, behaviors, checklists, workflows, figure refs) | Changing agent definition layout |
| `internal/codegen/templates/opencode_*.go.tmpl` | OpenCode role-skill, command-skill, and agent templates | Changing OpenCode-specific layout or frontmatter |
| `internal/codegen/harness.go` | Target harness registry, `roleSkillDirs`, `commandSkillDirs`, and target routing helpers | Adding a new generation target, role skill, or command skill |
| `tools/codegen/main.go` | Thin `go:generate` entry point; parses flags and invokes the selected harness targets | Changing CLI flags or invocation flow |
| `internal/codegen/testdata/context.yaml` | YAML fixture for `context_test.go` — exact constraint counts and must_contain/must_not_contain per role/phase | Any change to `roleConstraints` or `phaseConstraints` |
| `internal/codegen/testdata/skills.yaml` | YAML fixture for skill generation tests | Adding/removing roles or commands in skill generation |
| `internal/codegen/testdata/agents.yaml` | YAML fixture for agent generation tests | Adding/removing roles in agent generation |
| `internal/codegen/testdata/schema.yaml` | YAML fixture for schema generation tests | Adding/removing schema sections |
| `internal/codegen/testdata/markers.yaml` | YAML fixture for marker region tests | Changing BEGIN/END marker logic |

---

## Regeneration

The canonical regeneration command (run from the module root):

```bash
make generate
```

This runs `go generate ./internal/codegen/...`, whose directive invokes
`tools/codegen` for both the `claude-code` and `opencode` targets. The binary
locates the module root by walking upward from cwd to find `go.mod`.

What it does, in order:
1. Writes `schema.xml` (diff printed to stdout if changed)
2. Writes Claude Code skills under `skills/` and agents under `agents/`
3. Writes OpenCode skills under `.opencode/skill/`, agents under
   `.opencode/agent/`, and `opencode.json`
4. Copies the hand-authored `protocol` and `install-cli` skills verbatim into
   the OpenCode target

All committed generated outputs must remain byte-identical after regeneration.
To capture a new baseline intentionally, start from a clean tree, run
`make generate`, review the generated-path diff, and commit that output with the
source change. An unexpected generated diff is a regression. For focused tool
development, `go run ./tools/codegen --targets claude-code` selects one target,
but it is not a substitute for the canonical all-target regeneration gate.

### Changed X → regenerates Y

| What you changed | Regenerates |
|-----------------|-------------|
| Any map in `specs_data.go` | schema.xml and the affected skills/agents in both harnesses |
| `specs_data_body_<skill>.go` plus its `SkillBodySpecs` registry entry | Claude Code and OpenCode SKILL.md body content |
| `context.go` (`roleConstraints` / `phaseConstraints`) | affected SKILL.md and agent definitions in both harnesses |
| `schema_types.go` | schema.xml only |
| `schema.go` section builders | schema.xml only |
| `templates/skill.go.tmpl` | Claude Code role SKILL.md files |
| `templates/skill_sub.go.tmpl` | Claude Code command SKILL.md files |
| `templates/agent_definition.go.tmpl` | Claude Code agent definitions |
| `templates/opencode_*.go.tmpl` | corresponding OpenCode skills or agents |
| `internal/codegen/harness.go` `roleSkillDirs` | which SKILL.md files are regenerated |
| `internal/codegen/harness.go` `commandSkillDirs` | which sub-skill SKILL.md files are regenerated |

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

4. Run `make generate` then `go test ./internal/codegen/... -count=1`.

---

### Adding a Constraint to an Existing Role

This is a subset of the above when the `ConstraintSpec` already exists.

1. In `context.go`, add the constraint ID to `roleConstraints[types.RoleXxx]` with value `true`.

2. In `testdata/context.yaml`, find the entry for that role:
   - Increment `exact_count` by 1.
   - Add the constraint ID to `must_contain`.
   - Remove it from `must_not_contain` if it was listed there.

3. Run `make generate` then `go test ./internal/codegen/... -count=1`.

---

### Adding a Figure

1. Add an entry to `FigureSpecs` in `specs_data.go` (around line 1161). Set `ID`, `Title`, `Type` (e.g. `"ascii-diagram"`), `RoleRefs`, `SectionRef` (e.g. `"workflows"`), and optionally `WorkflowRefs` or `CommandRefs`.

2. Create a YAML file at `skills/protocol/figures/{id}.yaml` with a `content` key containing the ASCII diagram text. This is loaded at generation time by `loadFigureContent` in `skills.go`.

3. Run `make generate`. The figure will appear automatically in SKILL.md files (via `skill.go.tmpl`) and agent definition figure-ref lists (via `agent_definition.go.tmpl`) for the referenced roles.

4. Run `go test ./internal/codegen/... -count=1`. Update `testdata/agents.yaml` if fixture checks figure references.

---

### Adding a New Role

1. Add a `RoleId` constant to `pkg/protocol/enums.go` and add it to `AllRoleIds`.

2. Add an entry to `RoleSpecs` in `specs_data.go`. Fill `ID`, `Name`, `Description`, `OwnedPhases`, `Introduction`, `OwnershipNarrative`, `Behaviors`, and optionally `Model`, `Thinking`, `Tools`.

3. Add the role's command metadata to `CommandSpecs`. Its `RoleRef` must be the
   new role and its `File` must be `skills/<dir>/SKILL.md`.

4. Add a `roleConstraints` entry in `context.go` using `mergeConstraints(generalConstraints, map[string]bool{...})`.

5. Add the role to `roleSkillDirs` in `internal/codegen/harness.go` (map the `RoleId` constant to its skill directory).

6. Create `internal/codegen/specs_data_body_<skill>.go` with exactly one
   package-level `SkillBody` declaration, then add that declaration to the
   explicit `SkillBodySpecs` map in `specs_data_body.go`.

7. Seed the Claude Code output at `skills/<dir>/SKILL.md` with the marker pair:
   ```
   <!-- BEGIN GENERATED FROM pasture schema -->
   <!-- END GENERATED FROM pasture schema -->
   ```
   The OpenCode output is fully generated and needs no seed file.

8. Update `testdata/context.yaml`: add a `role_constraint_checks` entry with `exact_count`, `must_contain`, and `must_not_contain`.

9. Update `testdata/agents.yaml` and `testdata/skills.yaml` as needed for the new role.

10. Run `make generate` then `go test ./internal/codegen/... -count=1`.

`TestGeneratedSkillRegistryParity` checks that the command metadata, harness
emitter, and body registry contain exactly the same generated skill inventory.

---

### Adding a New Phase

1. Add a `PhaseId` constant to `pkg/protocol/` and add it to `AllPhaseIds`.

2. Add an entry to `PhaseSpecs` in `specs_data.go` (around line 17). Set `ID`, `Name`, `Number` (must be unique, 1–12 range extended if needed), `Domain`, `OwnerRoles`, and `Transitions`.

3. Add a `phaseConstraints` entry in `context.go` using `mergeConstraints(generalConstraints, map[string]bool{...})` or `copyConstraints(generalConstraints)`.

4. Update `phaseOrder` in `schema.go` (around line 63) to include the new phase in the canonical ordering.

5. Update `testdata/context.yaml`: add a `phase_constraint_checks` entry for the new phase.

6. Run `make generate` then `go test ./internal/codegen/... -count=1`.

---

### Adding a New Schema Section

1. Define the XML struct types in `schema_types.go` with `xml:` struct tags. Follow the existing pattern: a top-level `*Section` struct with `XMLName xml.Name`, and nested element types for children.

2. Write a `build{Name}` function in `schema.go` with the signature `func build{Name}(buf *bytes.Buffer, depth int)`. Use `marshalSection(buf, depth, ...)` to marshal the top-level struct.

   Exception: if your section requires `<![CDATA[...]]>` content (e.g. `<code>` blocks), you must use manual `fmt.Fprintf(buf, ...)` instead of `marshalSection`. See `buildConstraints` and `buildProcedureSteps` in `schema.go` for the pattern.

3. Add a `{comment, build{Name}}` entry to the `sections` slice in `generateSchemaContent` (around line 1576 in `schema.go`).

4. Run `make generate` then `go test ./internal/codegen/... -count=1`. Update `testdata/schema.yaml` to cover the new section.

---

### Adding a New Command / Skill

1. Add an entry to `CommandSpecs` in `specs_data.go`. Set `ID`, `Name` (for
   example `"pasture:role:action"`), `Description`, `RoleRef`, `Phases`, `File`,
   `Title`, and optionally `CreatesLabels`. `File` must be
   `skills/<skill-dir>/SKILL.md`.

2. Add exactly one emitter entry to `commandSkillDirs` in
   `internal/codegen/harness.go`:
   ```go
   "cmd-your-id": "your-skill-dir",
   ```

3. Create `internal/codegen/specs_data_body_<skill>.go` with exactly one
   package-level `SkillBody` declaration. Register that declaration under the
   same skill-directory key in `SkillBodySpecs` in `specs_data_body.go`.

4. Seed `skills/<skill-dir>/SKILL.md` with the BEGIN/END marker pair. The
   OpenCode output is fully generated and needs no seed file.

5. Update `testdata/skills.yaml` to cover the new command.

6. Run `make generate` then `go test ./internal/codegen/... -count=1`.

`TestGeneratedSkillRegistryParity` fails with the missing or orphaned skill
directory if the metadata, emitter, or body-registry leg is forgotten.

---

### Modifying a Template

1. Edit the `.go.tmpl` file in `internal/codegen/templates/`:
   - `skill.go.tmpl` — unified role SKILL.md template. Context type: `skillContext` (declared in `skills.go`). Available fields: `Role` (RoleSpec), `Commands` ([]CommandSpec), `Constraints` ([]ConstraintSpec), `Handoffs` ([]HandoffSpec), `OwnedPhases`, `PhasesDetail`, `Steps`, `PhaseSlug`, `SubSkills`, `Introduction`, `OwnershipNarrative`, `Behaviors`, `Checklists`, `CoordinationCommands`, `Workflows`, `FiguresByWorkflow`, `ReviewAxes`, `Preamble`, `BodyBehaviors`, `BodySections`, `BodyRecipes`.
   - `skill_sub.go.tmpl` — sub-skill template. Context type: `skillSubContext`. Available fields: `CommandName`, `CommandDescription`, `Figures`, `Preamble`, `BodySections`, `BodyRecipes`, `BodyBehaviors`.
   - `agent_definition.go.tmpl` — agent definitions. Context type: `agentTemplateData`. Available fields: `Role` (RoleSpec), `PhasesDetail`, `PhaseSlug`, `Constraints`, `Behaviors`, `Checklists`, `Workflows`, `Figures`.

2. Run `make generate` to preview the rendered output.

3. Template functions available in all templates: `join(items []string, sep string)`, `lower(s string)`, `last(i, length int) bool`, `not(b bool) bool`.

---

## Repeating a constraint or prose fragment across multiple skills/agents (define once, reference by ID)

When the same rule must appear in more than one role, phase, or skill body, **define it once and reference it by ID** — never copy the text. Duplicated prose drifts: each copy must be hand-updated and one always gets missed. This caused review findings **C-MIN-1, C-MIN-2, and A-IMP-1** in the epoch-protocol-improvements epoch (a constraint reworked in one place but stale in its duplicates). Define-once-by-ID keeps a single source of truth, and the `global_ids` parity check + `context_test` exact-count assertions enforce consistency.

### To make the SAME constraint appear in additional roles/phases

Add the constraint's **ID** to the relevant set in `internal/codegen/context.go`:

- `roleConstraints[types.RoleXxx]` — to attach it to a role
- `phaseConstraints[protocol.PhaseXxx]` — to attach it to a phase

The single `ConstraintSpecs` definition (in `specs_data.go`) then renders into each target's generated `skills/<role>/SKILL.md` **and** `agents/<role>.md`. Do **not** restate the rule as a fresh role/phase behavior.

Then update `testdata/context.yaml` in lockstep (`context_test` asserts **exact** equality):

- increment `exact_count` by 1 for each role/phase you attached it to
- add the ID to that entry's `must_contain`
- remove it from `must_not_contain` if listed

This is the subset recipe documented above under [Adding a Constraint to an Existing Role](#adding-a-constraint-to-an-existing-role).

**Worked examples (v2-2 re-UAT propagation):**

- **V2-PROP** — the deferral rule lives once in `ConstraintSpecs["C-uat-feedback-disposition"]`. To make the epoch orchestrator carry it, add `"C-uat-feedback-disposition": true` to `roleConstraints[types.RoleEpoch]` and bump the epoch `context.yaml` entry (`exact_count` +1, add to `must_contain`). It now renders into `skills/epoch/SKILL.md` + `agents/epoch.md` — no new epoch-body prose.
- **V4-PROP** — the validation-case contract lives once in `ConstraintSpecs["C-validation-cases"]`. To make the supervisor carry it, add `"C-validation-cases": true` to `roleConstraints[types.RoleSupervisor]` and bump the supervisor `context.yaml` entry. It now renders into `skills/supervisor/SKILL.md` + `agents/supervisor.md` — no duplicated TDD paragraph.

### To reuse the SAME prose/behaviour across multiple skill BODIES

Define it once in `SharedFragmentSpecs` (`specs_data_fragments.go`) and register its ID in `AllFragmentIds` (`specs.go`), then reference it from each consuming body via `fragRef(<id>)` (a `ProseSection`) or `behaviorRef(<id>)` (a `BehaviorSpec`) in `specs_data_body*.go`. Never copy the fragment text into a second body. The `global_ids` parity check enforces `AllFragmentIds` ↔ `SharedFragmentSpecs` agreement, and guard G5 fails on any inline `[frag--…]` token that does not resolve to a live fragment.

> Note: `fragRef`/`behaviorRef` reach skill **bodies** (`skills/<dir>/SKILL.md`) only. Agent definitions (`agents/<role>.md`) render only RoleSpec behaviors and attached constraints — to repeat a rule into an agent definition, use the constraint-attachment path above.

### Hand-authored protocol docs (`skills/protocol/*.md`)

`CONSTRAINTS.md` is the single constraint catalog — **one entry per constraint ID**. `PROCESS.md`, `CLAUDE.md`, `AGENTS.md`, and `SKILL.md` **reference** constraints by ID (e.g. "per `C-uat-feedback-disposition`") rather than restating their Given/When/Then. This mirrors the codegen rule: one definition, many references.

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

---

## Releasing

Releases are cut by `pasture-release` and **tagged automatically on merge** by the
`Release` workflow (`.github/workflows/release.yml`). A git tag is the unit of
release. For the **versioning policy** — what counts as MAJOR / MINOR / PATCH on
each consumption channel — see [docs/VERSIONING.md](docs/VERSIONING.md). This
section is the operational how-to.

### The flow (tag-on-merge)

```bash
# 1. Branch off main. Do NOT use a release/* name — the `release/**` ref pattern
#    is ruleset-protected (creation restricted). Use chore/* (or any other prefix).
git checkout -b chore/release-vX.Y.Z main

# 2. Bump + changelog + commit, but DO NOT tag locally (the tag is created on the
#    merged commit by CI). Preview first with --dry-run.
pasture-release patch --dry-run        # preview
pasture-release patch --no-tag         # writes .claude-plugin/plugin.json + CHANGELOG.md, commits

# 3. Push, open a PR, merge to main (any merge method is allowed).
git push -u origin chore/release-vX.Y.Z
gh pr create --base main --fill
#   → merge the PR

# 4. On merge, release.yml fires (it triggers on a push to main that changes
#    .claude-plugin/plugin.json): it tags vX.Y.Z on the merged commit, builds the
#    static binaries (linux/darwin × amd64/arm64 for pastured/pasture/
#    pasture-release), and publishes the GitHub Release with those assets.
```

Replace `patch` with `minor` / `major` as the change warrants (policy in
docs/VERSIONING.md).

### `pasture-release` flags

| Flag | Effect |
|------|--------|
| `--dry-run` | print the bump, changelog entry, and planned commit/tag without writing |
| `--no-tag` | bump + changelog + commit, but skip the local tag (**required** for the PR flow — CI tags the merged commit) |
| `--no-commit` | skip the git commit (write files only) |
| `--no-changelog` | skip `CHANGELOG.md` generation |
| `--sync` | reconcile version drift across manifests before bumping |
| `--plugin <name>` | after the bump, sync that plugin's entry in its registered (cross-repo) marketplace.json to the new version |

### Prerequisites (one-time)

The tag-on-merge workflow pushes the tag using a **GitHub App token**, so the
repo needs two secrets:

- `RELEASE_APP_ID` — the release App's ID
- `RELEASE_APP_PRIVATE_KEY` — the App's private key (PEM)

The App must have **`Contents: write`** on the repo and be installed on it. (An
App token is used instead of the default `GITHUB_TOKEN` so the tag is created
under a real bot identity and can fire other tag-watchers / survive future
tag-ref protection.)

### Marketplace mirror (parent repo)

pasture is distributed as a github-source plugin inside the `aura-plugins`
marketplace. After a pasture release, bump the pasture entry's `version` in the
parent `aura-plugins/.claude-plugin/marketplace.json` to match the new tag (this
is a change in the **parent** repo, committed there). `pasture-release
--plugin pasture` can perform this sync if the registry is configured.

### Re-running / recovering a release

The workflow is idempotent and also accepts `workflow_dispatch`:

- If `plugin.json` is already at the new version on `main` but the tag is missing
  (e.g. the first run failed), re-run with `gh workflow run release.yml --ref main`
  — on manual dispatch it tags whenever the tag is absent.
- If `vX.Y.Z` is already tagged, the workflow detects it and skips (safe re-runs).

### Troubleshooting

- **Tag push fails `403 ... denied to github-actions[bot]`** — the checkout
  persisted the default `GITHUB_TOKEN` as a git `http.extraheader`, which
  overrides the App token in the push URL. The detect job's `actions/checkout`
  must set `persist-credentials: false` so the minted App token is the only
  credential. (If it instead 403s as the *App* identity, the App is missing
  `Contents: write`.)
- **Branch push rejected "creations being restricted"** — you used a `release/*`
  branch name, which the ruleset blocks. Rename to `chore/*`.
- **No release fired after merge** — the trigger is a change to
  `.claude-plugin/plugin.json` on `main`. A merge that didn't change that file
  (e.g. a workflow-only fix) won't trigger; use `workflow_dispatch`.
