---
name: research
description: Domain research — find standards, prior art, and competing approaches
---

# Research

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
**Command:** `pasture:research` — Domain research — find standards, prior art, and competing approaches

General-purpose domain research skill. Finds standards, prior art, existing solutions, and established patterns for a given topic. Writes structured findings to `llm/research/<topic>.md`.

See `../protocol/CONSTRAINTS.md` for coding standards.

**[research-topic-deliverable]**
- Given: a research topic
- When: investigating
- Then: follow the depth-scoped checklist and write findings to `llm/research/<topic>.md`
- Should not: skip writing the deliverable file

**[research-depth-quick-scan]**
- Given: depth is quick-scan
- When: researching
- Then: search local project only (Grep, Glob, Read)
- Should not: make web requests

**[research-depth-standard]**
- Given: depth is standard-research
- When: researching
- Then: search local project AND web for domain standards and established patterns
- Should not: skip local analysis

**[research-depth-deep-dive]**
- Given: depth is deep-dive
- When: researching
- Then: perform full local analysis, web search for competing solutions, RFCs, academic papers, and well-regarded projects
- Should not: produce an unstructured dump

**[research-findings-format]**
- Given: findings exist
- When: writing deliverable
- Then: use the structured report format with per-topic sections, code citations (file:line), assessment tables, and adoption recommendations
- Should not: write a flat bullet list for standard-research or deep-dive depths

**[research-phase1-recording]**
- Given: Phase 1 context
- When: recording findings
- Then: ALSO add a summary comment on the REQUEST task via `"${EPOCH_REC}" note`
- Should not: only write the file without updating the REQUEST task

## Task Recovery

The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami, signal.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

The adapter passes `--namespace` only when EPOCH.md records `namespace:`. That pin wins on both backends. Otherwise it omits the flag. Pasture then derives the namespace itself: PASTURE_NAMESPACE, else the git remote, then file:// of the working directory. The ledger calls derive_namespace for the header and for every write that has no pin. That function is the one list of remote forms; do not restate it. A ledger with no origin uses file:// of the git work-tree root, so a subdirectory does not split the namespace. Queries without a pin follow that backend's derivation; pass `--all-namespaces` to opt out. When neither a pin nor PASTURE_NAMESPACE is set, pasture reads are unscoped and creation falls back to the git remote, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run `"${EPOCH_REC}" ready`, `"${EPOCH_REC}" query --blocked`, and `"${EPOCH_REC}" query --status in_progress`. For each referenced active task, run `"${EPOCH_REC}" show "$TASK_URI"`, `"${EPOCH_REC}" show "$TASK_URI" --notes`, and `"${EPOCH_REC}" show "$TASK_URI" --history`. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with `"${EPOCH_REC}" label "$TASK_URI" owner:<role>`. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: `"${EPOCH_REC}" block "$PARENT_URI" --by "$CHILD_URI"`. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records `backend: error`, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

## Task Attribution

Select the current registered author with "${EPOCH_REC}" whoami. It prints the author recorded in the header or PASTURE_AUTHOR, and on the pasture backend it confirms that actor with agents show. It does not list agents and it does not invent one. If neither source names an author, it stops: set author: in EPOCH.md or export PASTURE_AUTHOR. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## When to Use

- **Phase 1 (s1_2-research):** Spawned by `/pasture:user-request` after user confirms research depth. Findings recorded as REQUEST task comment AND written to `llm/research/`.
- **Standalone:** Any agent needing domain research outside the 12-phase workflow. Invoke directly with a topic and depth.

## Inputs

| Parameter | Required | Description |
|-----------|----------|--------------|
| `topic` | Yes | The research subject (e.g., "CEL policy engines", "HTTP proxy patterns") |
| `depth` | Yes | One of: `quick-scan`, `standard-research`, `deep-dive` |
| `request-task-id` | Phase 1 only | Pasture task ID to record findings as comment |

## Research Checklist

Apply all items appropriate to the depth level:

### 1. Domain Standards

- What RFCs, specs, or community conventions exist?
- Are there formal standards bodies or working groups?

### 2. Prior Art

- What well-regarded projects solve similar problems?
- What is the maturity, adoption, and maintenance status of each?
- Which approaches have been tried and abandoned (and why)?

### 3. Established Patterns

- What idioms and best practices are established in this domain?
- Are there canonical implementations or reference architectures?
- What do experienced practitioners recommend?

### 4. Reusable Solutions

- Are there existing libraries, frameworks, or tools that could be reused or adapted?
- What are the tradeoffs of build-vs-buy for this domain?

## Depth Scoping

| Depth | Local | Web | Deliverable |
|-------|-------|-----|-------------|
| **quick-scan** | Grep project for related patterns, check README/docs, scan dependency manifests | None | 1-paragraph summary per checklist item (4 paragraphs total) |
| **standard-research** | Local scan + check project dependencies, related repos, read key source files | Search for domain standards, established patterns, well-regarded projects | Per-topic sections with relevance notes and brief assessment |
| **deep-dive** | Full local analysis + dependency tree, architectural trace | Search for competing solutions, RFCs, academic papers, canonical implementations | Full structured report (see format below) |

## Output Format

Write findings to `llm/research/<topic>.md` using the structured report format.

### File Structure

```markdown
---
title: "<Topic> — Domain Research"
date: "<YYYY-MM-DD>"
depth: "<quick-scan|standard-research|deep-dive>"
request: "<request-task-id or 'standalone'>"
---

## Executive Summary

<1-3 paragraphs: key finding, scope of research, recommended direction>

---

## <Topic Area 1>

### <Subject A>: <Approach/Pattern Name>

<Description of how this subject implements/addresses the topic area.
Include code snippets with file:line citations where applicable.>

```<language>
// source-file.go:150-152
code snippet here
```

### <Subject B>: <Alternative Approach>

<Description of alternative.>

### Assessment

| Aspect | Subject A | Subject B |
|--------|-----------|-----------|
| <dimension 1> | ... | ... |
| <dimension 2> | ... | ... |

**Adoption recommendation:** <adopt/adapt/defer/skip with rationale>

---

## <Topic Area 2>

<Same structure: subjects → code citations → assessment table → recommendation>

---

## Summary

| Topic Area | Recommendation | Rationale |
|------------|---------------|-----------|
| Area 1 | Adopt/Adapt/Defer/Skip | Brief reason |
| Area 2 | ... | ... |

## Key Takeaways

### Adopt
- <Pattern or solution to adopt immediately>

### Adapt
- <Pattern to adapt with modifications>

### Defer
- <Interesting but not needed for MVP>

### Skip
- <Evaluated and rejected, with reason>
```

### Adoption Categories

| Category | Meaning |
|----------|---------|
| **Adopt** | Use directly or with minimal modification |
| **Adapt** | Useful pattern but needs significant modification for our context |
| **Defer** | Valuable but not needed for current scope; track for later |
| **Skip** | Evaluated and rejected; document why to prevent re-evaluation |

## Phase 1 Integration

When invoked as part of Phase 1 (s1_2-research), record a summary on the REQUEST task in addition to writing the full report:

```bash
"${EPOCH_REC}" note "${REQUEST_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body \
  "Research findings ({{depth}}):
  - Standards: {{list or 'none found'}}
  - Prior art: {{list of projects/solutions}}
  - Patterns: {{established approaches}}
  - Recommendation: {{brief direction}}
  - Full report: llm/research/{{topic}}.md"
```
<!-- END GENERATED FROM pasture schema -->
