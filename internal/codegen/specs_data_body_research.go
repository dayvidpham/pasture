// Body content for the research skill SKILL.md.
// Ported from aura-plugins/skills/research/SKILL.md.
package codegen

var researchBody = SkillBody{
	Preamble: "General-purpose domain research skill. Finds standards, prior art, existing solutions, and established patterns for a given topic. " +
		"Writes structured findings to `llm/research/<topic>.md`.\n\n" +
		"See `../protocol/CONSTRAINTS.md` for coding standards.",

	Behaviors: []BehaviorSpec{
		{
			Id:        "research-topic-deliverable",
			Given:     "a research topic",
			When:      "investigating",
			Then:      "follow the depth-scoped checklist and write findings to `llm/research/<topic>.md`",
			ShouldNot: "skip writing the deliverable file",
		},
		{
			Id:        "research-depth-quick-scan",
			Given:     "depth is quick-scan",
			When:      "researching",
			Then:      "search local project only (Grep, Glob, Read)",
			ShouldNot: "make web requests",
		},
		{
			Id:        "research-depth-standard",
			Given:     "depth is standard-research",
			When:      "researching",
			Then:      "search local project AND web for domain standards and established patterns",
			ShouldNot: "skip local analysis",
		},
		{
			Id:        "research-depth-deep-dive",
			Given:     "depth is deep-dive",
			When:      "researching",
			Then:      "perform full local analysis, web search for competing solutions, RFCs, academic papers, and well-regarded projects",
			ShouldNot: "produce an unstructured dump",
		},
		{
			Id:        "research-findings-format",
			Given:     "findings exist",
			When:      "writing deliverable",
			Then:      "use the structured report format with per-topic sections, code citations (file:line), assessment tables, and adoption recommendations",
			ShouldNot: "write a flat bullet list for standard-research or deep-dive depths",
		},
		{
			Id:        "research-phase1-recording",
			Given:     "Phase 1 context",
			When:      "recording findings",
			Then:      "ALSO add a summary comment on the REQUEST task via `bd comments add`",
			ShouldNot: "only write the file without updating the REQUEST task",
		},
	},

	Sections: []ProseSection{
		{
			Id:    "research-when-to-use",
			Title: "When to Use",
			Content: "- **Phase 1 (s1_2-research):** Spawned by `/pasture:user-request` after user confirms research depth. Findings recorded as REQUEST task comment AND written to `llm/research/`.\n" +
				"- **Standalone:** Any agent needing domain research outside the 12-phase workflow. Invoke directly with a topic and depth.",
		},
		{
			Id:    "research-inputs",
			Title: "Inputs",
			Content: "| Parameter | Required | Description |\n" +
				"|-----------|----------|--------------|\n" +
				"| `topic` | Yes | The research subject (e.g., \"CEL policy engines\", \"HTTP proxy patterns\") |\n" +
				"| `depth` | Yes | One of: `quick-scan`, `standard-research`, `deep-dive` |\n" +
				"| `request-task-id` | Phase 1 only | Beads task ID to record findings as comment |",
		},
		{
			Id:      "research-checklist",
			Title:   "Research Checklist",
			Content: "Apply all items appropriate to the depth level:",
			Subsections: []ProseSection{
				{
					Id:    "research-checklist-standards",
					Title: "1. Domain Standards",
					Content: "- What RFCs, specs, or community conventions exist?\n" +
						"- Are there formal standards bodies or working groups?",
				},
				{
					Id:    "research-checklist-prior-art",
					Title: "2. Prior Art",
					Content: "- What well-regarded projects solve similar problems?\n" +
						"- What is the maturity, adoption, and maintenance status of each?\n" +
						"- Which approaches have been tried and abandoned (and why)?",
				},
				{
					Id:    "research-checklist-patterns",
					Title: "3. Established Patterns",
					Content: "- What idioms and best practices are established in this domain?\n" +
						"- Are there canonical implementations or reference architectures?\n" +
						"- What do experienced practitioners recommend?",
				},
				{
					Id:    "research-checklist-reusable",
					Title: "4. Reusable Solutions",
					Content: "- Are there existing libraries, frameworks, or tools that could be reused or adapted?\n" +
						"- What are the tradeoffs of build-vs-buy for this domain?",
				},
			},
		},
		{
			Id:    "research-depth-scoping",
			Title: "Depth Scoping",
			Content: "| Depth | Local | Web | Deliverable |\n" +
				"|-------|-------|-----|-------------|\n" +
				"| **quick-scan** | Grep project for related patterns, check README/docs, scan dependency manifests | None | 1-paragraph summary per checklist item (4 paragraphs total) |\n" +
				"| **standard-research** | Local scan + check project dependencies, related repos, read key source files | Search for domain standards, established patterns, well-regarded projects | Per-topic sections with relevance notes and brief assessment |\n" +
				"| **deep-dive** | Full local analysis + dependency tree, architectural trace | Search for competing solutions, RFCs, academic papers, canonical implementations | Full structured report (see format below) |",
		},
		{
			Id:      "research-output-format",
			Title:   "Output Format",
			Content: "Write findings to `llm/research/<topic>.md` using the structured report format.",
			Subsections: []ProseSection{
				{
					Id:    "research-file-structure",
					Title: "File Structure",
					Content: "```" + `markdown
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

` + "```" + `<language>
// source-file.go:150-152
code snippet here
` + "```" + `

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
` + "```",
				},
				{
					Id:    "research-adoption-categories",
					Title: "Adoption Categories",
					Content: "| Category | Meaning |\n" +
						"|----------|---------|\n" +
						"| **Adopt** | Use directly or with minimal modification |\n" +
						"| **Adapt** | Useful pattern but needs significant modification for our context |\n" +
						"| **Defer** | Valuable but not needed for current scope; track for later |\n" +
						"| **Skip** | Evaluated and rejected; document why to prevent re-evaluation |",
				},
			},
		},
		{
			Id:    "research-phase1-integration",
			Title: "Phase 1 Integration",
			Content: "When invoked as part of Phase 1 (s1_2-research), record a summary on the REQUEST task in addition to writing the full report:\n\n" +
				"```" + `bash
bd comments add {{request-task-id}} \
  "Research findings ({{depth}}):
  - Standards: {{list or 'none found'}}
  - Prior art: {{list of projects/solutions}}
  - Patterns: {{established approaches}}
  - Recommendation: {{brief direction}}
  - Full report: llm/research/{{topic}}.md"
` + "```",
		},
	},
}
