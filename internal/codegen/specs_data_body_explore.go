// Body content for the explore skill SKILL.md.
// Ported from aura-plugins/skills/explore/SKILL.md.
package codegen

var exploreBody = SkillBody{
	Preamble: "General-purpose codebase exploration skill. Searches the codebase for integration points, existing patterns, data flow, dependencies, and potential conflicts relevant to a topic or feature.\n\n" +
		"See `../protocol/CONSTRAINTS.md` for coding standards.",

	Behaviors: []BehaviorSpec{
		{
			ID:        "explore-topic-structured",
			Given:     "a topic or feature",
			When:      "exploring",
			Then:      "follow the depth-scoped checklist and produce structured findings",
			ShouldNot: "produce an unstructured list of file paths",
		},
		{
			ID:        "explore-depth-quick-scan",
			Given:     "depth is quick-scan",
			When:      "exploring",
			Then:      "grep for keywords, check obvious entry points",
			ShouldNot: "read entire modules or trace full dependency graphs",
		},
		{
			ID:        "explore-depth-standard-research",
			Given:     "depth is standard-research",
			When:      "exploring",
			Then:      "trace data flow, map dependencies, read related modules",
			ShouldNot: "skip tracing how data flows through the relevant code paths",
		},
		{
			ID:        "explore-depth-deep-dive",
			Given:     "depth is deep-dive",
			When:      "exploring",
			Then:      "build full dependency graph, perform architectural analysis, identify all touchpoints",
			ShouldNot: "miss transitive dependencies or indirect consumers",
		},
		{
			ID:        "explore-code-refs",
			Given:     "code references",
			When:      "documenting",
			Then:      "use `file:line` citation format",
			ShouldNot: "reference files without line numbers for specific code",
		},
		{
			ID:        "explore-phase1-recording",
			Given:     "Phase 1 context",
			When:      "recording findings",
			Then:      "add a structured comment on the REQUEST task via `bd comments add`",
			ShouldNot: "only produce output without updating the REQUEST task",
		},
	},

	Sections: []ProseSection{
		{
			ID:    "explore-when-to-use",
			Title: "When to Use",
			Content: "- **Phase 1 (s1_3-explore):** Spawned by `/aura:user-request` after user confirms research depth. Findings recorded as REQUEST task comment.\n" +
				"- **Standalone:** Any agent needing to understand codebase structure for a topic. Invoke directly with a topic and depth.",
		},
		{
			ID:    "explore-inputs",
			Title: "Inputs",
			Content: "| Parameter | Required | Description |\n" +
				"|-----------|----------|--------------|\n" +
				"| `topic` | Yes | The feature or concept to explore (e.g., \"session management\", \"CLI command registration\") |\n" +
				"| `depth` | Yes | One of: `quick-scan`, `standard-research`, `deep-dive` |\n" +
				"| `request-task-id` | Phase 1 only | Beads task ID to record findings as comment |",
		},
		{
			ID:    "explore-checklist",
			Title: "Exploration Checklist",
			Subsections: []ProseSection{
				{
					ID:    "explore-checklist-entry-points",
					Title: "1. Entry Points",
					Content: "Where would this feature plug in?\n" +
						"- CLI commands, subcommands, flag definitions\n" +
						"- API routes, handlers, middleware\n" +
						"- Event handlers, hooks, lifecycle callbacks\n" +
						"- Configuration loaders, init functions",
				},
				{
					ID:    "explore-checklist-data-flow",
					Title: "2. Data Flow",
					Content: "What existing data structures, types, or schemas are relevant?\n" +
						"- Type definitions, interfaces, structs\n" +
						"- Database schemas, migration files\n" +
						"- Protobuf/OpenAPI/JSON schemas\n" +
						"- Configuration types",
				},
				{
					ID:    "explore-checklist-dependencies",
					Title: "3. Dependencies",
					Content: "What modules/packages would this feature depend on or extend?\n" +
						"- Direct imports and consumers\n" +
						"- Shared utilities, helpers, constants\n" +
						"- External packages and their versions\n" +
						"- Build system integration (Nix flakes, package.json, go.mod)",
				},
				{
					ID:    "explore-checklist-patterns",
					Title: "4. Existing Patterns",
					Content: "How do similar features work in this codebase?\n" +
						"- Naming conventions (files, functions, types, tests)\n" +
						"- DI patterns (constructor injection, functional options, context)\n" +
						"- Error handling conventions (error types, wrapping, logging)\n" +
						"- Test structure (fixtures, mocks, test helpers, BDD style)",
				},
				{
					ID:    "explore-checklist-conflicts",
					Title: "5. Conflicts",
					Content: "Are there existing implementations that would need modification or could conflict?\n" +
						"- Overlapping functionality that might be duplicated\n" +
						"- Shared state that could race or deadlock\n" +
						"- Configuration keys or CLI flags that could collide\n" +
						"- Import cycles or circular dependencies",
				},
			},
		},
		{
			ID:    "explore-depth-scoping",
			Title: "Depth Scoping",
			Content: "| Depth | Scope | Tools | Deliverable |\n" +
				"|-------|-------|-------|-------------|\n" +
				"| **quick-scan** | Grep for keywords, check obvious entry points, scan file tree | Glob, Grep | 1-paragraph summary per checklist item |\n" +
				"| **standard-research** | Trace data flow, map dependencies, read related modules, check test patterns | Glob, Grep, Read | Per-section structured findings with file:line citations |\n" +
				"| **deep-dive** | Full dependency graph, architectural analysis, identify all touchpoints, trace transitive consumers | Glob, Grep, Read, Bash (build/dep tools) | Complete architectural map with dependency diagram and risk assessment |",
		},
		{
			ID:    "explore-output-format",
			Title: "Output Format",
			Subsections: []ProseSection{
				{
					ID:    "explore-structured-findings",
					Title: "Structured Findings",
					Content: "```" + `markdown
## Explore Findings: <topic>

**Depth:** <quick-scan|standard-research|deep-dive>
**Date:** <YYYY-MM-DD>

### Entry Points

| File | Line | Type | Description |
|------|------|------|-------------|
| ` + "`src/cli/commands.ts`" + ` | 42 | CLI subcommand | ` + "`register`" + ` function adds command to CLI router |
| ` + "`src/api/routes.ts`" + ` | 118 | HTTP handler | ` + "`POST /sessions`" + ` endpoint |

### Data Flow

` + "```" + `
User input → CLI parser (src/cli/parse.ts:30)
  → Command handler (src/commands/session.ts:15)
    → Service layer (src/services/session.ts:42)
      → Repository (src/db/session-repo.ts:28)
        → Database
` + "```" + `

**Relevant types:**
- ` + "`SessionConfig`" + ` at ` + "`src/types/session.ts:12`" + ` — configuration schema
- ` + "`SessionState`" + ` at ` + "`src/types/session.ts:45`" + ` — runtime state enum

### Dependencies

**Direct:**
- ` + "`src/services/session.ts`" + ` imports ` + "`src/db/session-repo.ts`" + `
- ` + "`src/commands/session.ts`" + ` imports ` + "`src/services/session.ts`" + `

**Shared utilities:**
- ` + "`src/utils/logger.ts`" + ` — structured logging (used by all services)
- ` + "`src/utils/config.ts`" + ` — configuration loader

**External:**
- ` + "`better-sqlite3@11.0.0`" + ` — database driver
- ` + "`zod@3.23.0`" + ` — schema validation

### Existing Patterns

**Naming:** Commands use ` + "`<verb>-<noun>.ts`" + ` (e.g., ` + "`create-session.ts`" + `)
**DI:** Constructor injection via factory functions (see ` + "`src/services/index.ts:20`" + `)
**Tests:** Vitest with ` + "`describe/it`" + ` blocks, fixtures in ` + "`tests/fixtures/`" + `
**Errors:** Custom error classes extending ` + "`AppError`" + ` at ` + "`src/errors/base.ts:5`" + `

### Conflicts

- ` + "`src/services/auth.ts:88`" + ` — existing session cleanup logic may overlap
- ` + "`src/types/session.ts:45`" + ` — ` + "`SessionState`" + ` enum may need new values
- No import cycles detected
` + "```",
				},
			},
		},
		{
			ID:    "explore-phase1-integration",
			Title: "Phase 1 Integration",
			Content: "When invoked as part of Phase 1 (s1_3-explore), record findings on the REQUEST task:\n\n" +
				"```" + `bash
bd comments add {{request-task-id}} \
  "Explore findings ({{depth}}):
  - Entry points: {{list of files/functions with line numbers}}
  - Related types: {{existing types/schemas with locations}}
  - Dependencies: {{modules this would use}}
  - Patterns: {{how similar features work here}}
  - Conflicts: {{potential issues or 'none'}}"
` + "```",
		},
		{
			ID:    "explore-standalone",
			Title: "Standalone Use",
			Content: "When used outside Phase 1, produce the structured findings directly as output. No beads comment is needed unless a task ID is provided.\n\n" +
				"```" + `
/aura:explore
Topic: "Nix flake module system"
Depth: deep-dive
` + "```" + `

This produces the full architectural map without requiring a REQUEST task context.`,
		},
	},
}
