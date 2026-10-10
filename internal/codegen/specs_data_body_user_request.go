// Body content for the user-request skill SKILL.md.
// Ported from aura-plugins/skills/user-request/SKILL.md.
package codegen

var userRequestBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-1-request-aurap1-user)** <- Phase 1",

	Behaviors: []BehaviorSpec{
		{
			Id:        "user-req-verbatim-capture",
			Given:     "user provides request",
			When:      "capturing",
			Then:      "store verbatim without paraphrasing",
			ShouldNot: "summarize or interpret",
		},
		{
			Id:        "user-req-classify-label",
			Given:     "request captured",
			When:      "classifying",
			Then:      "use `pasture:p1-user:s1_1-classify` label",
			ShouldNot: "use other labels for the initial capture",
		},
		{
			Id:        "user-req-research-depth",
			Given:     "classification complete",
			When:      "user confirms research depth",
			Then:      "run s1_2-research and s1_3-explore in parallel",
			ShouldNot: "skip research depth confirmation",
		},
		{
			Id:        "user-req-proceed-to-elicit",
			Given:     "Phase 1 complete",
			When:      "proceeding",
			Then:      "invoke `/pasture:user-elicit` for Phase 2",
			ShouldNot: "skip to proposal",
		},
		{
			Id:        "user-req-fix-intent",
			Given:     "a request whose user intent is to FIX existing behavior (a bug, regression, or incorrect output)",
			When:      "classifying in Phase 1",
			Then:      "recognize the fix-intent SEMANTICALLY during classification (record it in the classification comment) so the downstream URE/UAT/impl validation cases capture the currently-failing behaviours; validation cases themselves are elicited for EVERY request regardless",
			ShouldNot: "introduce a request-type axis or enum to detect fix-intent — recognition is semantic, not a fifth classification axis; gate validation cases on fix-intent",
		},
		// R6/A2: surface the shared validation-cases lifecycle at the classification
		// entry point — it applies to EVERY request (generalized from fix-intent-only
		// at v2-2). behaviorRef resolves to SharedFragmentSpecs[FragValidationCases]
		// (SLICE-1).
		behaviorRef(FragValidationCases),
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:    "user-req-substeps",
			Title: "Phase 1 Sub-steps",
			Content: "| Sub-step | Label | Description | Parallel? |\n" +
				"|----------|-------|-------------|----------|\n" +
				"| s1_1-classify | `pasture:p1-user:s1_1-classify` | Capture verbatim + classify along 4 axes | Sequential (first) |\n" +
				"| s1_2-research | `pasture:p1-user:s1_2-research` | Find domain standards, prior art | Parallel with s1_3 |\n" +
				"| s1_3-explore | `pasture:p1-user:s1_3-explore` | Codebase exploration for integration points | Parallel with s1_2 |",
		},
		{
			Id:      "user-req-step1",
			Title:   "Step 1: Capture and Classify (s1_1)",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:    "user-req-step1-capture",
					Title: "Capture verbatim and create the request task",
					Content: `1. **Get the user's request verbatim:**
   ` + "```" + `
   AskUserQuestion: "What feature or change would you like to request?"
   ` + "```" + `

2. **Create the request task:**
   ` + "```" + `bash
   REQUEST_ID_URI=$(pasture task create "REQUEST: {{short summary}}" --phase request --namespace "$PASTURE_NAMESPACE" --format json \
     --description "{{VERBATIM user request - do not edit}}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
   pasture task label add "$REQUEST_ID_URI" pasture:p1-user:s1_1-classify
   pasture task update "$REQUEST_ID_URI" --notes "Requested role (not an assignment): architect"
   ` + "```" + `

3. **Classify along 4 axes:**
   - **Scope:** Single file, module, cross-cutting
   - **Complexity:** Low, medium, high
   - **Risk:** Breaking changes, new API, internal-only
   - **Domain novelty:** Familiar patterns vs new territory

4. **Record classification** via comment on the request task:
   ` + "```" + `bash
   pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
     "Classification: scope={{scope}}, complexity={{complexity}}, risk={{risk}}, novelty={{novelty}}"
   ` + "```" + ``,
				},
				{
					Id:    "user-req-fix-intent-recognition",
					Title: "Recognize fix-intent (semantic, NOT a classification axis)",
					Content: `Separately from the 4 axes above, judge **semantically** whether the user's intent is to **FIX existing behavior** (a bug, regression, or wrong output) versus build something new. This is a recognition step, **not a fifth axis and not a ` + "`" + `request-type` + "`" + ` enum** — do not add a typed field for it.

When the intent is to fix existing behavior, the **validation-case lifecycle** applies for the rest of the epoch: concrete failing/expected cases are elicited in URE (` + "`" + `/pasture:user-elicit` + "`" + `), confirmed with the user in UAT (` + "`" + `/pasture:user-uat` + "`" + `), evaluated against the fix, and the failing real-data cases are stored as test fixtures.

Record the recognition in the same classification comment so downstream phases pick it up:
` + "```" + `bash
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
  "Fix-intent: yes — validation-case lifecycle applies (elicit cases in URE, confirm in UAT, store as fixtures)"
` + "```" + ``,
				},
			},
		},
		{
			Id:    "user-req-step2",
			Title: "Step 2: Research Depth Confirmation",
			Content: "After classification, confirm research depth with the user:\n" +
				"\n" +
				"```\n" +
				"AskUserQuestion:\n" +
				"  question: \"Based on classification ({{scope}}, {{complexity}}, {{risk}}, {{novelty}}), how deep should research go?\"\n" +
				"  header: \"Research Depth\"\n" +
				"  options:\n" +
				"    - label: \"Quick scan\"\n" +
				"      description: \"Familiar domain, low complexity — brief prior art check\"\n" +
				"    - label: \"Standard research\"\n" +
				"      description: \"Moderate complexity or some novelty — find existing patterns and standards\"\n" +
				"    - label: \"Deep dive\"\n" +
				"      description: \"High complexity, new territory, or high risk — thorough domain analysis\"\n" +
				"```",
		},
		{
			Id:      "user-req-step3",
			Title:   "Step 3: Record Depth + Spawn Parallel Agents (s1_2 || s1_3)",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:    "user-req-step3-record",
					Title: "Record depth and spawn agents",
					Content: `Record the user's depth choice, then spawn two parallel agents:

` + "```" + `bash
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
  "Research depth: {{depth}} (user confirmed)"
` + "```" + `

Spawn both agents in parallel (via Task tool with ` + "`" + `run_in_background: true` + "`" + `). Each agent invokes its dedicated skill.`,
				},
				{
					Id:    "user-req-step3-research",
					Title: "s1_2-research: Domain Research",
					Content: `Invoke ` + "`" + `/pasture:research` + "`" + ` with:
- **topic:** derived from the user's request
- **depth:** the user-confirmed research depth
- **request-task-id:** the REQUEST pasture task ID

The ` + "`" + `/pasture:research` + "`" + ` skill handles the full research workflow: depth-scoped checklist, structured report written to ` + "`" + `docs/research/<topic>.md` + "`" + `, and summary comment on the REQUEST task.

See [skills/research/SKILL.md](../research/SKILL.md) for full procedure, output format, and examples.

**Depth determines scope:**

| Depth | Local | Web | Deliverable |
|-------|-------|-----|-------------|
| **Quick scan** | Grep project for related patterns, check README/docs | None | 1-paragraph summary of local findings |
| **Standard research** | Local scan + check project dependencies, related repos | Search for domain standards, established patterns | List of prior art with relevance notes |
| **Deep dive** | Full local analysis + dependency tree | Search for competing solutions, RFCs, academic papers, well-regarded projects | Structured report: standards found, competing approaches, recommended direction |

**Research checklist:**
1. What domain standards exist? (RFCs, specs, community conventions)
2. What well-regarded projects solve similar problems? (prior art)
3. What patterns are established in this domain? (idioms, best practices)
4. Are there existing solutions that could be reused or adapted?

**Record findings** as a comment on the REQUEST task:
` + "```" + `bash
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
  "Research findings ({{depth}}):
  - Standards: {{list or 'none found'}}
  - Prior art: {{list of projects/solutions}}
  - Patterns: {{established approaches}}
  - Recommendation: {{brief direction}}
  - Full report: docs/research/{{topic}}.md"
` + "```" + ``,
				},
				{
					Id:    "user-req-step3-explore",
					Title: "s1_3-explore: Codebase Exploration",
					Content: `Invoke ` + "`" + `/pasture:explore` + "`" + ` with:
- **topic:** derived from the user's request
- **depth:** the user-confirmed research depth (same depth applies)
- **request-task-id:** the REQUEST pasture task ID

The ` + "`" + `/pasture:explore` + "`" + ` skill handles the full exploration workflow: depth-scoped checklist, structured findings, and summary comment on the REQUEST task.

See [skills/explore/SKILL.md](../explore/SKILL.md) for full procedure, output format, and examples.

**Exploration checklist:**
1. **Entry points:** Where would this feature plug in? (CLI commands, API routes, event handlers)
2. **Data flow:** What existing data structures, types, or schemas are relevant?
3. **Dependencies:** What modules/packages would this feature depend on or extend?
4. **Existing patterns:** How do similar features work in this codebase? (conventions, DI patterns, test structure)
5. **Conflicts:** Are there existing implementations that would need modification or could conflict?

**Depth determines thoroughness:**

| Depth | Scope | Tools |
|-------|-------|-------|
| **Quick scan** | Grep for keywords, check obvious entry points | Glob, Grep |
| **Standard research** | Trace data flow, map dependencies, read related modules | Glob, Grep, Read |
| **Deep dive** | Full dependency graph, architectural analysis, identify all touchpoints | Glob, Grep, Read, Bash (for build/dep tools) |

**Record findings** as a comment on the REQUEST task:
` + "```" + `bash
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
  "Explore findings ({{depth}}):
  - Entry points: {{list of files/functions}}
  - Related types: {{existing types/schemas}}
  - Dependencies: {{modules this would use}}
  - Patterns: {{how similar features work here}}
  - Conflicts: {{potential issues or 'none'}}"
` + "```" + ``,
				},
				{
					Id:      "user-req-step3-completion",
					Title:   "Completion",
					Content: "Both agents must complete before proceeding to Phase 2. Their findings are recorded as comments on the REQUEST task, available for the elicitation survey and proposal phases.",
				},
			},
		},
		{
			Id:    "user-req-example",
			Title: "Example",
			Content: `User says: "I want to add a logout button to the header that clears the session and redirects to the login page"

` + "```" + `bash
REQUEST_ID_URI=$(pasture task create "REQUEST: Add logout button to header" --phase request --namespace "$PASTURE_NAMESPACE" --format json \
  --description "I want to add a logout button to the header that clears the session and redirects to the login page" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$REQUEST_ID_URI" pasture:p1-user:s1_1-classify
pasture task update "$REQUEST_ID_URI" --notes "Requested role (not an assignment): architect"

pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REQUEST_ID_URI}" \
  "Classification: scope=module, complexity=low, risk=internal-only, novelty=familiar"
` + "```" + ``,
		},
		{
			Id:    "user-req-next-phase",
			Title: "Next Phase",
			Content: `After Phase 1 completes, invoke ` + "`" + `/pasture:user-elicit` + "`" + ` to begin requirements elicitation (Phase 2).

The elicit task will block this request task:
` + "```" + `bash
pasture task dep add "${REQUEST_ID_URI}" --blocked-by "${ELICIT_ID_URI}"
` + "```" + ``,
		},
	},

	Recipes: []RecipeBlock{},
}
