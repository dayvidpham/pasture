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
			Then:      "use `aura:p1-user:s1_1-classify` label",
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
			Then:      "invoke `/aura:user-elicit` for Phase 2",
			ShouldNot: "skip to proposal",
		},
	},

	Sections: []ProseSection{
		{
			Id:    "user-req-substeps",
			Title: "Phase 1 Sub-steps",
			Content: "| Sub-step | Label | Description | Parallel? |\n" +
				"|----------|-------|-------------|----------|\n" +
				"| s1_1-classify | `aura:p1-user:s1_1-classify` | Capture verbatim + classify along 4 axes | Sequential (first) |\n" +
				"| s1_2-research | `aura:p1-user:s1_2-research` | Find domain standards, prior art | Parallel with s1_3 |\n" +
				"| s1_3-explore | `aura:p1-user:s1_3-explore` | Codebase exploration for integration points | Parallel with s1_2 |",
		},
		{
			Id:      "user-req-step1",
			Title:   "Step 1: Capture and Classify (s1_1)",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:    "user-req-step1-capture",
					Title: "Capture verbatim and create the request task",
					Content: "1. **Get the user's request verbatim:**\n" +
						"   ```\n" +
						"   AskUserQuestion: \"What feature or change would you like to request?\"\n" +
						"   ```\n" +
						"\n" +
						"2. **Create the request task:**\n" +
						"   " + "```" + `bash` + "\n" +
						"   bd create --labels \"aura:p1-user:s1_1-classify\" \\\n" +
						"     --title \"REQUEST: {{short summary}}\" \\\n" +
						"     --description \"{{VERBATIM user request - do not edit}}\" \\\n" +
						"     --assignee architect\n" +
						"   " + "```" + "\n" +
						"\n" +
						"3. **Classify along 4 axes:**\n" +
						"   - **Scope:** Single file, module, cross-cutting\n" +
						"   - **Complexity:** Low, medium, high\n" +
						"   - **Risk:** Breaking changes, new API, internal-only\n" +
						"   - **Domain novelty:** Familiar patterns vs new territory\n" +
						"\n" +
						"4. **Record classification** via comment on the request task:\n" +
						"   " + "```" + `bash` + "\n" +
						"   bd comments add {{request-task-id}} \\\n" +
						"     \"Classification: scope={{scope}}, complexity={{complexity}}, risk={{risk}}, novelty={{novelty}}\"\n" +
						"   " + "```",
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
					Content: "Record the user's depth choice, then spawn two parallel agents:\n" +
						"\n" +
						"```" + `bash` + "\n" +
						"bd comments add {{request-task-id}} \\\n" +
						"  \"Research depth: {{depth}} (user confirmed)\"\n" +
						"```\n" +
						"\n" +
						"Spawn both agents in parallel (via Task tool with `run_in_background: true`). Each agent invokes its dedicated skill.",
				},
				{
					Id:    "user-req-step3-research",
					Title: "s1_2-research: Domain Research",
					Content: "Invoke `/aura:research` with:\n" +
						"- **topic:** derived from the user's request\n" +
						"- **depth:** the user-confirmed research depth\n" +
						"- **request-task-id:** the REQUEST beads task ID\n" +
						"\n" +
						"The `/aura:research` skill handles the full research workflow: depth-scoped checklist, structured report written to `docs/research/<topic>.md`, and summary comment on the REQUEST task.\n" +
						"\n" +
						"See [skills/research/SKILL.md](../research/SKILL.md) for full procedure, output format, and examples.\n" +
						"\n" +
						"**Depth determines scope:**\n" +
						"\n" +
						"| Depth | Local | Web | Deliverable |\n" +
						"|-------|-------|-----|-------------|\n" +
						"| **Quick scan** | Grep project for related patterns, check README/docs | None | 1-paragraph summary of local findings |\n" +
						"| **Standard research** | Local scan + check project dependencies, related repos | Search for domain standards, established patterns | List of prior art with relevance notes |\n" +
						"| **Deep dive** | Full local analysis + dependency tree | Search for competing solutions, RFCs, academic papers, well-regarded projects | Structured report: standards found, competing approaches, recommended direction |\n" +
						"\n" +
						"**Research checklist:**\n" +
						"1. What domain standards exist? (RFCs, specs, community conventions)\n" +
						"2. What well-regarded projects solve similar problems? (prior art)\n" +
						"3. What patterns are established in this domain? (idioms, best practices)\n" +
						"4. Are there existing solutions that could be reused or adapted?\n" +
						"\n" +
						"**Record findings** as a comment on the REQUEST task:\n" +
						"```" + `bash` + "\n" +
						"bd comments add {{request-task-id}} \\\n" +
						"  \"Research findings ({{depth}}):\n" +
						"  - Standards: {{list or 'none found'}}\n" +
						"  - Prior art: {{list of projects/solutions}}\n" +
						"  - Patterns: {{established approaches}}\n" +
						"  - Recommendation: {{brief direction}}\n" +
						"  - Full report: docs/research/{{topic}}.md\"\n" +
						"```",
				},
				{
					Id:    "user-req-step3-explore",
					Title: "s1_3-explore: Codebase Exploration",
					Content: "Invoke `/aura:explore` with:\n" +
						"- **topic:** derived from the user's request\n" +
						"- **depth:** the user-confirmed research depth (same depth applies)\n" +
						"- **request-task-id:** the REQUEST beads task ID\n" +
						"\n" +
						"The `/aura:explore` skill handles the full exploration workflow: depth-scoped checklist, structured findings, and summary comment on the REQUEST task.\n" +
						"\n" +
						"See [skills/explore/SKILL.md](../explore/SKILL.md) for full procedure, output format, and examples.\n" +
						"\n" +
						"**Exploration checklist:**\n" +
						"1. **Entry points:** Where would this feature plug in? (CLI commands, API routes, event handlers)\n" +
						"2. **Data flow:** What existing data structures, types, or schemas are relevant?\n" +
						"3. **Dependencies:** What modules/packages would this feature depend on or extend?\n" +
						"4. **Existing patterns:** How do similar features work in this codebase? (conventions, DI patterns, test structure)\n" +
						"5. **Conflicts:** Are there existing implementations that would need modification or could conflict?\n" +
						"\n" +
						"**Depth determines thoroughness:**\n" +
						"\n" +
						"| Depth | Scope | Tools |\n" +
						"|-------|-------|-------|\n" +
						"| **Quick scan** | Grep for keywords, check obvious entry points | Glob, Grep |\n" +
						"| **Standard research** | Trace data flow, map dependencies, read related modules | Glob, Grep, Read |\n" +
						"| **Deep dive** | Full dependency graph, architectural analysis, identify all touchpoints | Glob, Grep, Read, Bash (for build/dep tools) |\n" +
						"\n" +
						"**Record findings** as a comment on the REQUEST task:\n" +
						"```" + `bash` + "\n" +
						"bd comments add {{request-task-id}} \\\n" +
						"  \"Explore findings ({{depth}}):\n" +
						"  - Entry points: {{list of files/functions}}\n" +
						"  - Related types: {{existing types/schemas}}\n" +
						"  - Dependencies: {{modules this would use}}\n" +
						"  - Patterns: {{how similar features work here}}\n" +
						"  - Conflicts: {{potential issues or 'none'}}\"\n" +
						"```",
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
			Content: "User says: \"I want to add a logout button to the header that clears the session and redirects to the login page\"\n" +
				"\n" +
				"```" + `bash` + "\n" +
				"bd create --labels \"aura:p1-user:s1_1-classify\" \\\n" +
				"  --title \"REQUEST: Add logout button to header\" \\\n" +
				"  --description \"I want to add a logout button to the header that clears the session and redirects to the login page\" \\\n" +
				"  --assignee architect\n" +
				"# Returns: bd-abc123\n" +
				"\n" +
				"bd comments add bd-abc123 \\\n" +
				"  \"Classification: scope=module, complexity=low, risk=internal-only, novelty=familiar\"\n" +
				"```",
		},
		{
			Id:    "user-req-next-phase",
			Title: "Next Phase",
			Content: "After Phase 1 completes, invoke `/aura:user-elicit` to begin requirements elicitation (Phase 2).\n" +
				"\n" +
				"The elicit task will block this request task:\n" +
				"```" + `bash` + "\n" +
				"bd dep add {{request-task-id}} --blocked-by {{elicit-task-id}}\n" +
				"```",
		},
	},

	Recipes: []RecipeBlock{},
}
