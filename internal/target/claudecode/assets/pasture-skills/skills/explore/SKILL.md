---
name: explore
description: Codebase exploration — find integration points, existing patterns, and related code
---

# Explore

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:explore` — Codebase exploration — find integration points, existing patterns, and related code

General-purpose codebase exploration skill. Searches the codebase for integration points, existing patterns, data flow, dependencies, and potential conflicts relevant to a topic or feature.

See `../protocol/CONSTRAINTS.md` for coding standards.

**[explore-topic-structured]**
- Given: a topic or feature
- When: exploring
- Then: follow the depth-scoped checklist and produce structured findings
- Should not: produce an unstructured list of file paths

**[explore-depth-quick-scan]**
- Given: depth is quick-scan
- When: exploring
- Then: grep for keywords, check obvious entry points
- Should not: read entire modules or trace full dependency graphs

**[explore-depth-standard-research]**
- Given: depth is standard-research
- When: exploring
- Then: trace data flow, map dependencies, read related modules
- Should not: skip tracing how data flows through the relevant code paths

**[explore-depth-deep-dive]**
- Given: depth is deep-dive
- When: exploring
- Then: build full dependency graph, perform architectural analysis, identify all touchpoints
- Should not: miss transitive dependencies or indirect consumers

**[explore-code-refs]**
- Given: code references
- When: documenting
- Then: use `file:line` citation format
- Should not: reference files without line numbers for specific code

**[explore-phase1-recording]**
- Given: Phase 1 context
- When: recording findings
- Then: add a structured comment on the REQUEST task via `pasture task comment add`
- Should not: only produce output without updating the REQUEST task

## Task Recovery

Recover from live Pasture state at session start and after compaction; never trust cached task rows.

Store selection: an explicit --db overrides PASTURE_DB_PATH; otherwise use XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db. Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

Set PASTURE_NAMESPACE to the repository's canonical namespace URI, for example https://github.com/dayvidpham/pasture. Explicit --namespace overrides the git-remote-derived namespace, then file:// of the working directory. List requires an explicit namespace to avoid mixing repositories. ready/blocked have only an exact --label filter, not namespace filtering; inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run pasture task ready, pasture task blocked, and pasture task list --namespace "$PASTURE_NAMESPACE" --status in_progress. For each referenced active task, run pasture task show "$TASK_URI", pasture task comments "$TASK_URI", and pasture task timeline "$TASK_URI". All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the actual JSON id returned by create; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create returns an object whose id is the task URI: capture it with --format json and python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'. Persist that URI before label/comment steps. Create-plus-label is non-atomic: on failure retain the URI and retry only the failed label/comment operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

Assignment transfer is not initial allocation: before using pasture task assignment transfer, obtain the existing owner-responsibility assignment and its exact successor assignment ID, registered committing actor, and registered worker occupant from the supervisor. If the task is unassigned, stop and let the supervisor arrange allocation; no assignment-start command is implied. Never repeat an identical transfer as a substitute for allocating different workers.

Parent stays open and is blocked by child: pasture task dep add "$PARENT_URI" --blocked-by "$CHILD_URI". Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

## Task Attribution

Select the current registered author with pasture task agents list and pasture task agents show ACTOR-ID. Never auto-register or guess an identity during recovery. Every comment supplies --author explicitly; machine examples use the pre-registered pasture-system--00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## When to Use

- **Phase 1 (s1_3-explore):** Spawned by `/pasture:user-request` after user confirms research depth. Findings recorded as REQUEST task comment.
- **Standalone:** Any agent needing to understand codebase structure for a topic. Invoke directly with a topic and depth.

## Inputs

| Parameter | Required | Description |
|-----------|----------|--------------|
| `topic` | Yes | The feature or concept to explore (e.g., "session management", "CLI command registration") |
| `depth` | Yes | One of: `quick-scan`, `standard-research`, `deep-dive` |
| `request-task-id` | Phase 1 only | Pasture task ID to record findings as comment |

## Exploration Checklist



### 1. Entry Points

Where would this feature plug in?
- CLI commands, subcommands, flag definitions
- API routes, handlers, middleware
- Event handlers, hooks, lifecycle callbacks
- Configuration loaders, init functions

### 2. Data Flow

What existing data structures, types, or schemas are relevant?
- Type definitions, interfaces, structs
- Database schemas, migration files
- Protobuf/OpenAPI/JSON schemas
- Configuration types

### 3. Dependencies

What modules/packages would this feature depend on or extend?
- Direct imports and consumers
- Shared utilities, helpers, constants
- External packages and their versions
- Build system integration (Nix flakes, package.json, go.mod)

### 4. Existing Patterns

How do similar features work in this codebase?
- Naming conventions (files, functions, types, tests)
- DI patterns (constructor injection, functional options, context)
- Error handling conventions (error types, wrapping, logging)
- Test structure (fixtures, mocks, test helpers, BDD style)

### 5. Conflicts

Are there existing implementations that would need modification or could conflict?
- Overlapping functionality that might be duplicated
- Shared state that could race or deadlock
- Configuration keys or CLI flags that could collide
- Import cycles or circular dependencies

## Depth Scoping

| Depth | Scope | Tools | Deliverable |
|-------|-------|-------|-------------|
| **quick-scan** | Grep for keywords, check obvious entry points, scan file tree | Glob, Grep | 1-paragraph summary per checklist item |
| **standard-research** | Trace data flow, map dependencies, read related modules, check test patterns | Glob, Grep, Read | Per-section structured findings with file:line citations |
| **deep-dive** | Full dependency graph, architectural analysis, identify all touchpoints, trace transitive consumers | Glob, Grep, Read, Bash (build/dep tools) | Complete architectural map with dependency diagram and risk assessment |

## Output Format



### Structured Findings

```markdown
## Explore Findings: <topic>

**Depth:** <quick-scan|standard-research|deep-dive>
**Date:** <YYYY-MM-DD>

### Entry Points

| File | Line | Type | Description |
|------|------|------|-------------|
| `src/cli/commands.ts` | 42 | CLI subcommand | `register` function adds command to CLI router |
| `src/api/routes.ts` | 118 | HTTP handler | `POST /sessions` endpoint |

### Data Flow

```
User input → CLI parser (src/cli/parse.ts:30)
  → Command handler (src/commands/session.ts:15)
    → Service layer (src/services/session.ts:42)
      → Repository (src/db/session-repo.ts:28)
        → Database
```

**Relevant types:**
- `SessionConfig` at `src/types/session.ts:12` — configuration schema
- `SessionState` at `src/types/session.ts:45` — runtime state enum

### Dependencies

**Direct:**
- `src/services/session.ts` imports `src/db/session-repo.ts`
- `src/commands/session.ts` imports `src/services/session.ts`

**Shared utilities:**
- `src/utils/logger.ts` — structured logging (used by all services)
- `src/utils/config.ts` — configuration loader

**External:**
- `better-sqlite3@11.0.0` — database driver
- `zod@3.23.0` — schema validation

### Existing Patterns

**Naming:** Commands use `<verb>-<noun>.ts` (e.g., `create-session.ts`)
**DI:** Constructor injection via factory functions (see `src/services/index.ts:20`)
**Tests:** Vitest with `describe/it` blocks, fixtures in `tests/fixtures/`
**Errors:** Custom error classes extending `AppError` at `src/errors/base.ts:5`

### Conflicts

- `src/services/auth.ts:88` — existing session cleanup logic may overlap
- `src/types/session.ts:45` — `SessionState` enum may need new values
- No import cycles detected
```

## Phase 1 Integration

When invoked as part of Phase 1 (s1_3-explore), record findings on the REQUEST task:

```bash
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
  "Explore findings ({{depth}}):
  - Entry points: {{list of files/functions with line numbers}}
  - Related types: {{existing types/schemas with locations}}
  - Dependencies: {{modules this would use}}
  - Patterns: {{how similar features work here}}
  - Conflicts: {{potential issues or 'none'}}"
```

## Standalone Use

When used outside Phase 1, produce the structured findings directly as output. No pasture comment is needed unless a task ID is provided.

```
/pasture:explore
Topic: "Nix flake module system"
Depth: deep-dive
```

This produces the full architectural map without requiring a REQUEST task context.
<!-- END GENERATED FROM pasture schema -->
