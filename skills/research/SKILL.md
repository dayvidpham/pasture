# Research

<!-- BEGIN GENERATED FROM aura schema -->
**Command:** `aura:research` — Domain research — find standards, prior art, and competing approaches

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
- Then: ALSO add a summary comment on the REQUEST task via `bd comments add`
- Should not: only write the file without updating the REQUEST task

## When to Use

- **Phase 1 (s1_2-research):** Spawned by `/aura:user-request` after user confirms research depth. Findings recorded as REQUEST task comment AND written to `llm/research/`.
- **Standalone:** Any agent needing domain research outside the 12-phase workflow. Invoke directly with a topic and depth.

## Inputs

| Parameter | Required | Description |
|-----------|----------|--------------|
| `topic` | Yes | The research subject (e.g., "CEL policy engines", "HTTP proxy patterns") |
| `depth` | Yes | One of: `quick-scan`, `standard-research`, `deep-dive` |
| `request-task-id` | Phase 1 only | Beads task ID to record findings as comment |

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
bd comments add {{request-task-id}} \
  "Research findings ({{depth}}):
  - Standards: {{list or 'none found'}}
  - Prior art: {{list of projects/solutions}}
  - Patterns: {{established approaches}}
  - Recommendation: {{brief direction}}
  - Full report: llm/research/{{topic}}.md"
```
<!-- END GENERATED FROM aura schema -->
