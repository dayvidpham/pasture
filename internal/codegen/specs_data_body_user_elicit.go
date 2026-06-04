// Body content for the user-elicit skill SKILL.md.
// Ported from aura-plugins/skills/user-elicit/SKILL.md.
package codegen

var userElicitBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-2-elicit--urd-aurap2-user)** <- Phase 2",

	Behaviors: []BehaviorSpec{
		{
			Id:        "user-elicit-plan-backwards",
			Given:     "user request captured",
			When:      "eliciting",
			Then:      "plan backwards from end vision to MVP",
			ShouldNot: "start proposal without elicitation",
		},
		{
			Id:        "user-elicit-read-phase1",
			Given:     "Phase 1 complete",
			When:      "starting elicitation",
			Then:      "read Phase 1 outputs (classification, research findings, explore findings) from REQUEST task comments to scope URE questions",
			ShouldNot: "ignore prior art discoveries or codebase exploration results",
		},
		{
			Id:        "user-elicit-multiselect",
			Given:     "elicitation questions",
			When:      "asking",
			Then:      "use multiSelect: true for flexibility",
			ShouldNot: "force single-choice answers",
		},
		{
			Id:        "user-elicit-verbatim-responses",
			Given:     "responses captured",
			When:      "storing",
			Then:      "record questions AND answers verbatim (including all options presented)",
			ShouldNot: "summarize user responses or omit option text",
		},
		{
			Id:        "user-elicit-chain-dep",
			Given:     "elicitation complete",
			When:      "creating task",
			Then:      "chain dependency to request task",
			ShouldNot: "skip dependency",
		},
		{
			Id:        "user-elicit-urd-reference",
			Given:     "URD created",
			When:      "linking to other tasks",
			Then:      "include URD ID in description frontmatter of referencing tasks",
			ShouldNot: "use `bd dep add --blocked-by` for URD links (URD is a reference document, not a blocking dependency)",
		},
		{
			Id:        "user-elicit-code-shown",
			Given:     "any definition, code snippet, interface, or before/after example shown to the user during elicitation (e.g. in an AskUserQuestion preview)",
			When:      "recording the Q&A",
			Then:      "capture the shown definition/code VERBATIM in the elicit task alongside the question and response (parity with UAT's 'Definition shown' / 'Command run' fields)",
			ShouldNot: "record only the answer while dropping the code or definition the user was reacting to — the response is meaningless without what was shown",
		},
		{
			Id:        "user-elicit-invoke-skill",
			Given:     "the Phase 2 URE interview",
			When:      "conducting it",
			Then:      "MUST invoke `Skill(/pasture:user-elicit)` so the verbatim-capture and validation-case elicitation procedures are loaded",
			ShouldNot: "conduct the URE without invoking its skill — skipping it loses verbatim capture and the validation-case lifecycle",
		},
		{
			Id:        "user-elicit-raise-deferrals",
			Given:     "deferred items outstanding from a prior phase (flagged by the user OR proposed by the architect/supervisor)",
			When:      "conducting this URE gate (a user gate)",
			Then:      "ALL deferred items, whoever proposed them, MUST be raised to the user at the next user gate (URE, Plan UAT, or Impl UAT) for confirmation — present the complete outstanding deferral set and let the user confirm or override each item; nothing is silently deferred",
			ShouldNot: "silently carry a deferral forward without raising it to the user at this gate",
		},
		// R6: EVERY REQUEST elicits concrete validation cases during URE (generalized
		// from fix-intent-only at v2-2). behaviorRef resolves to
		// SharedFragmentSpecs[FragValidationCases] (SLICE-1) so the lifecycle renders
		// into the generated SKILL.md.
		behaviorRef(FragValidationCases),
	},

	Sections: []ProseSection{
		{
			Id:    "user-elicit-substeps",
			Title: "Sub-steps",
			Content: "| Sub-step | Label | Description |\n" +
				"|----------|-------|-------------|\n" +
				"| s2_1-elicit | `pasture:p2-user:s2_1-elicit` | URE survey — structured requirements elicitation |\n" +
				"| s2_2-urd | `pasture:p2-user:s2_2-urd` (also `pasture:urd`) | Create URD — single source of truth for requirements |",
		},
		{
			Id:      "user-elicit-strategy",
			Title:   "Elicitation Strategy (s2_1)",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:    "user-elicit-end-vision",
					Title: "1. End Vision (Plan Backwards)",
					Content: "Ask about the user's ultimate goal and what interfaces they envision:\n" +
						"- What does the final feature look like?\n" +
						"- How will users interact with it?\n" +
						"- What other systems need to integrate?",
				},
				{
					Id:    "user-elicit-mvp-scope",
					Title: "2. MVP Scope (Plan Forward)",
					Content: "Jump to the starting point:\n" +
						"- What's the minimum viable version?\n" +
						"- What can be deferred to later iterations?\n" +
						"- What are the must-have vs nice-to-have features?",
				},
				{
					Id:    "user-elicit-engineering-dims",
					Title: "3. Engineering Dimensions",
					Content: "Ask targeted questions to map the problem space:\n" +
						"- Parallelism: Can operations run concurrently?\n" +
						"- Distribution: Single process or distributed?\n" +
						"- Scale: How many users/requests/items?\n" +
						"- Has-a / Is-a relationships in the domain",
				},
				{
					Id:    "user-elicit-boundaries",
					Title: "4. Boundaries and Constraints",
					Content: "- Performance requirements?\n" +
						"- Security considerations?\n" +
						"- Compatibility constraints?\n" +
						"- Error handling expectations?",
				},
				{
					Id:      "user-elicit-catchall",
					Title:   "5. Catch-All",
					Content: "Final question to capture anything missed.",
				},
				{
					Id:    "user-elicit-validation-cases",
					Title: "6. Validation Cases (EVERY request)",
					Content: "Elicit **concrete validation cases** during this URE — for **every** request, not only fix-intent ones. We always need to know what \"done\" means, and what correct vs incorrect behaviour looks like:\n" +
						"- The **definition of done** — what observable outcome means the request is satisfied.\n" +
						"- The exact inputs/behaviors that MUST PASS (the expected correct output).\n" +
						"- The exact inputs/behaviors that MUST FAIL or are explicitly out of scope (incorrect behaviour). For fix-intent requests this includes the inputs/behaviors that currently FAIL (the bug as the user observes it).\n" +
						"- Any real data, commands, or reproduction steps the user can provide — capture these **verbatim**.\n" +
						"\n" +
						"These cases seed the request's test fixtures and are the set confirmed with the user in UAT (`/pasture:user-uat`) and evaluated against the implementation. Do NOT introduce a `request-type` enum to gate this — what a request needs is recognized semantically.",
				},
				{
					Id:    "user-elicit-prereq",
					Title: "Pre-requisite: Read Phase 1 Outputs",
					Content: "Before designing URE questions, **read all Phase 1 outputs** (classification,\n" +
						"research findings, codebase exploration) from the REQUEST task and its comments.\n" +
						"These narrow the design space and reveal which boundaries are already clear vs\n" +
						"which need user input.\n" +
						"\n" +
						"```" + `bash` + "\n" +
						"bd show <request-task-id>   # Read classification + research + explore findings\n" +
						"```\n" +
						"\n" +
						"Use the Phase 1 findings to identify:\n" +
						"- Which engineering dimensions are **already decided** (don't ask about these)\n" +
						"- Which dimensions have **multiple viable alternatives** (ask about these)\n" +
						"- Which dimensions the user **may not have considered** (surface these)",
				},
				{
					Id:    "user-elicit-question-sequence",
					Title: "Question Sequence (Decision Tree)",
					Content: "Structure questions as a decision tree that progressively narrows the design\n" +
						"space. Each question should depend on the answers to previous questions.\n" +
						"\n" +
						"**Round 1: Highest-leverage boundaries** (1-2 questions per AskUserQuestion call)\n" +
						"\n" +
						"Identify the 2-3 dimensions that most constrain the design. These are the axes\n" +
						"where different choices lead to fundamentally different architectures.\n" +
						"\n" +
						"Ask one component at a time. Show the user:\n" +
						"1. The concrete thing being decided (code snippet, interface, diagram)\n" +
						"2. A motivating example of how each option plays out\n" +
						"3. The tradeoffs between options\n" +
						"\n" +
						"**Round 2: Dependent decisions** (informed by Round 1 answers)\n" +
						"\n" +
						"With the high-level architecture settled, ask about the next layer of decisions\n" +
						"that were ambiguous.\n" +
						"\n" +
						"**Round 3: Edge cases and constraints** (if needed)\n" +
						"\n" +
						"Remaining questions about error handling, performance targets, compatibility\n" +
						"requirements — but only where the answer isn't obvious from prior context.\n" +
						"\n" +
						"**Final: Catch-all**\n" +
						"\n" +
						"One open-ended question to capture anything the decision tree missed.",
				},
			},
		},
		{
			Id:    "user-elicit-example-survey",
			Title: "Example Survey",
			Content: "```\n" +
				"AskUserQuestion(questions: [\n" +
				"  {\n" +
				"    question: \"What is your end vision for this feature? How will users interact with it when complete?\",\n" +
				"    header: \"End Vision\",\n" +
				"    multiSelect: true,\n" +
				"    options: [\n" +
				"      { label: \"Simple UI control\", description: \"Button/link users click\" },\n" +
				"      { label: \"Automated process\", description: \"Happens without user action\" },\n" +
				"      { label: \"API endpoint\", description: \"Programmatic access\" },\n" +
				"      { label: \"Background service\", description: \"Runs continuously\" }\n" +
				"    ]\n" +
				"  },\n" +
				"  {\n" +
				"    question: \"What is the minimum viable version (MVP) that would be useful?\",\n" +
				"    header: \"MVP Scope\",\n" +
				"    multiSelect: true,\n" +
				"    options: [\n" +
				"      { label: \"Core functionality only\", description: \"Just the basic action\" },\n" +
				"      { label: \"With confirmation\", description: \"User confirms before action\" },\n" +
				"      { label: \"With feedback\", description: \"Show success/error state\" },\n" +
				"      { label: \"Full featured\", description: \"All bells and whistles\" }\n" +
				"    ]\n" +
				"  },\n" +
				"  {\n" +
				"    question: \"Are there any specific constraints or requirements?\",\n" +
				"    header: \"Constraints\",\n" +
				"    multiSelect: true,\n" +
				"    options: [\n" +
				"      { label: \"Performance critical\", description: \"Must be fast\" },\n" +
				"      { label: \"Security sensitive\", description: \"Handles sensitive data\" },\n" +
				"      { label: \"Backwards compatible\", description: \"Can't break existing\" },\n" +
				"      { label: \"No constraints\", description: \"Flexible implementation\" }\n" +
				"    ]\n" +
				"  },\n" +
				"  {\n" +
				"    question: \"Is there anything else we should know about this feature?\",\n" +
				"    header: \"Other\",\n" +
				"    multiSelect: true,\n" +
				"    options: [\n" +
				"      { label: \"Related to existing feature\", description: \"Connects to something\" },\n" +
				"      { label: \"Inspired by another product\", description: \"Has a reference\" },\n" +
				"      { label: \"Urgent timeline\", description: \"Needed soon\" },\n" +
				"      { label: \"Nothing else\", description: \"Covered everything\" }\n" +
				"    ]\n" +
				"  }\n" +
				"])\n" +
				"```",
		},
		{
			Id:    "user-elicit-create-task",
			Title: "Creating the Elicit Task (s2_1)",
			Content: "After survey completion, capture the full Q&A record using the same structured\n" +
				"format as [UAT_TEMPLATE.md](../protocol/UAT_TEMPLATE.md). Each question must\n" +
				"include the exact question text, ALL options with their descriptions, and the\n" +
				"user's verbatim response. When a definition, code snippet, or example was shown\n" +
				"to the user before a question, capture it verbatim in a **Definition/code shown:**\n" +
				"field (parity with UAT's 'Definition shown' / 'Command run' fields). For\n" +
				"**every** request, also record the elicited validation cases verbatim.\n" +
				"\n" +
				"```" + `bash` + "\n" +
				"bd create --labels \"pasture:p2-user:s2_1-elicit\" \\\n" +
				"  --title \"ELICIT: {{feature name}}\" \\\n" +
				"  --description \"---\n" +
				"references:\n" +
				"  request: {{request-task-id}}\n" +
				"---\n" +
				"## Questions and Responses\n" +
				"\n" +
				"### End Vision\n" +
				"Q: What is your end vision for this feature? How will users interact with it when complete?\n" +
				"Definition/code shown: {{verbatim definition/snippet shown to user, or 'none'}}\n" +
				"Options: Simple UI control (Button/link users click), Automated process (Happens without user action), API endpoint (Programmatic access), Background service (Runs continuously)\n" +
				"A: {{user's verbatim selections and any custom input}}\n" +
				"\n" +
				"### MVP Scope\n" +
				"Q: What is the minimum viable version (MVP) that would be useful?\n" +
				"Options: Core functionality only (Just the basic action), With confirmation (User confirms before action), With feedback (Show success/error state), Full featured (All bells and whistles)\n" +
				"A: {{user's verbatim selections}}\n" +
				"\n" +
				"### Constraints\n" +
				"Q: Are there any specific constraints or requirements?\n" +
				"Options: Performance critical (Must be fast), Security sensitive (Handles sensitive data), Backwards compatible (Can't break existing), No constraints (Flexible implementation)\n" +
				"A: {{user's verbatim selections}}\n" +
				"\n" +
				"### Other\n" +
				"Q: Is there anything else we should know about this feature?\n" +
				"Options: Related to existing feature (Connects to something), Inspired by another product (Has a reference), Urgent timeline (Needed soon), Nothing else (Covered everything)\n" +
				"A: {{user's verbatim input}}\n" +
				"\n" +
				"## Validation Cases (EVERY request)\n" +
				"- Definition of done: {{verbatim observable outcome that means the request is satisfied}}\n" +
				"- Must pass (correct behaviour): {{verbatim expected correct behavior}}\n" +
				"- Must fail / out of scope (incorrect behaviour): {{verbatim — for fix-intent, the input/behavior that fails today}}\n" +
				"- Repro / real data: {{verbatim commands, data, or steps — or 'none'}}\" \\\n" +
				"  --assignee architect\n" +
				"\n" +
				"# Chain dependency: REQUEST blocked by ELICIT\n" +
				"bd dep add {{request-task-id}} --blocked-by {{elicit-task-id}}\n" +
				"```",
		},
		{
			Id:    "user-elicit-create-urd",
			Title: "Creating the URD (s2_2)",
			Content: "After the elicit task is created, create the URD as the single source of truth for user requirements:\n" +
				"\n" +
				"```" + `bash` + "\n" +
				"bd create --labels \"pasture:urd,pasture:p2-user:s2_2-urd\" \\\n" +
				"  --title \"URD: {{feature name}}\" \\\n" +
				"  --description \"---\n" +
				"references:\n" +
				"  request: {{request-task-id}}\n" +
				"  elicit: {{elicit-task-id}}\n" +
				"---\n" +
				"## Requirements\n" +
				"{{structured requirements extracted from URE survey}}\n" +
				"\n" +
				"## Priorities\n" +
				"{{user-stated priorities from survey responses}}\n" +
				"\n" +
				"## Design Choices\n" +
				"{{design decisions surfaced during elicitation}}\n" +
				"\n" +
				"## MVP Goals\n" +
				"{{minimum viable scope identified}}\n" +
				"\n" +
				"## End-Vision Goals\n" +
				"{{user's ultimate vision for the feature}}\"\n" +
				"```\n" +
				"\n" +
				"The URD is a **reference document**, not a blocking dependency. Other tasks reference it via description frontmatter (`urd: <urd-task-id>`), not via blocking dependency commands.\n" +
				"\n" +
				"Record the URD task ID — pass it to the architect for Phase 3.",
		},
		{
			Id:    "user-elicit-next-phase",
			Title: "Next Phase",
			Content: "After elicitation and URD creation, invoke `/pasture:architect` to begin proposal creation (Phase 3). Pass the URD ID so the architect can reference it.\n" +
				"\n" +
				"The proposal task will block the elicit task:\n" +
				"```" + `bash` + "\n" +
				"bd dep add {{elicit-task-id}} --blocked-by {{proposal-task-id}}\n" +
				"```",
		},
	},

	Recipes: []RecipeBlock{},
}
