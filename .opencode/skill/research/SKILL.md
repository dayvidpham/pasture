---
name: research
description: Domain research — find standards, prior art, and competing approaches
---

# Research

<!-- BEGIN GENERATED FROM pasture schema -->
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
- Then: ALSO add a summary comment on the REQUEST task via `pasture task comment add`
- Should not: only write the file without updating the REQUEST task

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
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
  "Research findings ({{depth}}):
  - Standards: {{list or 'none found'}}
  - Prior art: {{list of projects/solutions}}
  - Patterns: {{established approaches}}
  - Recommendation: {{brief direction}}
  - Full report: llm/research/{{topic}}.md"
```
<!-- END GENERATED FROM pasture schema -->
