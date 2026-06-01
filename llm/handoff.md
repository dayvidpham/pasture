# Handoff: Codegen Go Port — Follow-up Work

## Completed Work

Branch: `feat--pasture--initial-golang-port`
Status: UAT ACCEPT (Phase 11 complete), pushed to remote, working tree clean.

### What Was Built

The full Python codegen pipeline has been ported to Go in `internal/codegen/`:

| Wave | Commits | Description |
|------|---------|-------------|
| Original port (SLICE 1-7) | `e287f99`..`556edda` | Spec types, context injection, markers, skills gen, schema gen, agent gen, go:generate entry point |
| UAT revision SLICE-A | `e600d15` | `schema_types.go` (689 lines) — 17 encoding/xml section structs; `validate.go` interface stubs |
| UAT revision SLICE-B | `bfe9f19` | Hybrid XML refactor — 15 build* functions converted from fmt.Fprintf to encoding/xml |
| UAT revision SLICE-C | `119f69e` | 3-layer schema validator (1048 lines) ported from validate_schema.py |
| UAT revision SLICE-D | `84b7d7f` | Enhanced test fixtures — MustContainHeaders, round-trip validation, malformed XML test |
| Review fixes | `94029d2` | agents.yaml figure blocks documentation, empty root assertion, rules 7-8 comment |
| Regenerated output | `50a5222` | schema.xml + agent definitions regenerated after hybrid refactor |

130 tests passing. All pushed.

### Architecture Summary

```
internal/codegen/
├── specs.go           # Type definitions (25 spec types)
├── specs_data.go      # Canonical data maps (13 maps, hand-authored)
├── context.go         # RoleContext/PhaseContext builders
├── markers.go         # BEGIN/END marker surgery for SKILL.md
├── skills.go          # SKILL.md generation (text/template)
├── agents.go          # Agent definition generation
├── schema.go          # schema.xml generation (hybrid: 15 encoding/xml + 2 manual CDATA)
├── schema_types.go    # 17 encoding/xml annotated section structs
├── validate.go        # 3-layer validator (structural, referential, semantic)
├── embed.go           # Template embedding
├── codegen.go         # Package entry point
└── testdata/          # YAML-driven test fixtures
    ├── skills.yaml, agents.yaml, schema.yaml, context.yaml, markers.yaml
tools/codegen/main.go  # go:generate entry point
```

### Key Design Decisions (Ratified)

1. **Hybrid XML**: 15 sections use `encoding/xml` struct marshalling, 2 CDATA sections (`buildConstraints`, `buildProcedureSteps`) remain manual `fmt.Fprintf`. User accepted but wants custom `xml.Marshaler` explored as follow-up.
2. **Validator independence**: `validate.go` does NOT import `schema_types.go`. Uses only stdlib.
3. **Error contract**: `ValidateSchema(r io.Reader) ([]ValidationError, error)` — io.Reader failure → `(nil, error)`; XML parse → `([]ValidationError{Structural}, nil)`; violations → `([]ValidationError{...}, nil)`; valid → `(nil, nil)`.
4. **Tree-based parsing**: Full XMLNode tree via `encoding/xml`, not streaming. User confirmed.
5. **Semantic equivalence**: Whitespace differences between old manual output and new struct-marshalled output are acceptable.

---

## Follow-up Work

### Beads Tasks

| ID | Title | Priority | Source |
|----|-------|----------|--------|
| `aura-plugins-zbgn7` | Custom xml.Marshaler for CDATA sections | P3 | UAT feedback |
| `aura-plugins-21lgi` | Add figure ID references to agent definitions | P3 | UAT feedback |
| `aura-plugins-m0teq` | FOLLOWUP epic: Non-blocking improvements from code review | P3 | Code review round 1 (8 MINOR findings) |

### FOLLOWUP-1: Custom xml.Marshaler for CDATA (aura-plugins-zbgn7)

**What**: Implement `xml.Marshaler` interface on `ConstraintsSection` and `ProcedureStepsSection` so all 17 sections use encoding/xml struct marshalling.

**Why**: User preference from UAT. Currently 2 of 17 sections use manual `fmt.Fprintf` because `encoding/xml` cannot emit `<![CDATA[...]]>` natively. A custom Marshaler could write CDATA by implementing `MarshalXML` and using `xml.Encoder.EncodeToken` with `xml.CharData` wrapped in raw `<![CDATA[` delimiters.

**Scope**:
- Implement `MarshalXML` on the `Example` type from `specs.go` (or a wrapper for its `Code` field)
- Convert `buildConstraints` and `buildProcedureSteps` from manual `fmt.Fprintf` to struct population + `marshalSection`
- Verify CDATA output is preserved (existing `TestGenerateSchema_CDATAInCodeElements` must pass)
- Remove the "NOT used for xml.Marshal" comments from `ConstraintsSection`/`ProcedureStepsSection`

**Risk**: `encoding/xml.Encoder.EncodeToken(xml.CharData(...))` escapes content. Emitting raw `<![CDATA[` requires bypassing the encoder and writing directly to the underlying writer, which may be fragile. Research `xml.Encoder.Flush()` + direct write pattern.

**Files**: `internal/codegen/schema_types.go`, `internal/codegen/schema.go`

### FOLLOWUP-2: Figure ID References in Agent Definitions (aura-plugins-21lgi)

**What**: Agent definitions should include figure IDs (references) rather than omitting figures entirely.

**Why**: User feedback from UAT. Currently `must_have_figure_blocks` is `false` in agents.yaml because `FigureSpec.Content` (full ASCII diagrams) is not loaded into agent definitions — it's loaded only at SKILL.md generation time. The user wants agent definitions to reference figures by ID so the agent knows which figures exist, without embedding the full content.

**Scope**:
- Add a `## Figures` section to `agent_definition.go.tmpl` that lists figure IDs and titles
- Populate `RoleContext.Figures` with ID+Title only (or add a `FigureRef` type with just ID/Title)
- Update `agents.yaml` to set `must_have_figure_blocks: true` (or add a new `must_have_figure_refs` field)
- Add test assertions for figure references in generated agent output

**Files**: `internal/codegen/templates/agent_definition.go.tmpl`, `internal/codegen/context.go`, `internal/codegen/testdata/agents.yaml`, `internal/codegen/agents_test.go`

### FOLLOWUP-3: Prior Code Review MINOR Findings (aura-plugins-m0teq)

8 MINOR findings from the original codegen port code review. See `bd show aura-plugins-m0teq` for the full list. Highlights:

- **Dual template-loading** (`aura-plugins-c5zlu`): skills.go uses string-embed for 2 templates while agents.go uses ParseFS. Unify on ParseFS.
- **GenerateSchemaToFile 0% coverage** (`aura-plugins-sq11q`): Public function called by production `go:generate` but never tested directly.
- **GenerateSubSkill Init mode untested** (`aura-plugins-9wpwf`): 61.9% coverage gap.
- **Inline path.Dir** (`aura-plugins-oprzr`): Use `filepath.Dir` instead of manual `strings.LastIndex`.

---

## Context for Next Session

### Branch State
- Branch: `feat--pasture--initial-golang-port`
- Remote: up to date (`50a5222`)
- Tests: 130 passing
- Working tree: clean

### Beads State
- REQUEST `aura-plugins-oc1tm`: OPEN (blocked by IMPL_PLAN and FOLLOWUP epic)
- IMPL_PLAN `aura-plugins-8silc`: IN_PROGRESS (all original + UAT slices closed, but IMPL_PLAN itself not closed due to follow-ups)
- PROPOSAL-4 `aura-plugins-r6wcq`: CLOSED (UAT ACCEPT)
- FOLLOWUP epic `aura-plugins-m0teq`: OPEN (8 MINOR findings)
- 2 new follow-up tasks: `aura-plugins-zbgn7`, `aura-plugins-21lgi`

### What's Next
1. Close IMPL_PLAN if follow-ups are considered independent
2. Work on follow-up tasks (P3, non-blocking)
3. Eventually close REQUEST when all follow-ups are resolved or explicitly deferred
