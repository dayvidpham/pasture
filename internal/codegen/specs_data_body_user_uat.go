// Body content for the user-uat command SKILL.md.
// Ported from aura-plugins/skills/user-uat/SKILL.md.
package codegen

var userUatBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-5-plan-uat)** <- Phase 5 (Plan UAT) and Phase 11 (Impl UAT)",

	Behaviors: []BehaviorSpec{
		{
			ID:        "uat-demonstrative-examples",
			Given:     "reviewers reach consensus",
			When:      "conducting UAT",
			Then:      "show demonstrative examples",
			ShouldNot: "ask abstract questions",
		},
		{
			ID:        "uat-one-component-at-a-time",
			Given:     "UAT questions",
			When:      "asking",
			Then:      "present one component at a time with definition + implementation + example BEFORE asking",
			ShouldNot: "dump all questions at once about all components simultaneously",
		},
		{
			ID:        "uat-real-alternatives",
			Given:     "UAT questions",
			When:      "forming options",
			Then:      "describe specific tradeoffs and design choices made",
			ShouldNot: "use generic approval options like 'exactly matches', 'mostly matches', 'requires revisions'",
		},
		{
			ID:        "uat-verbatim-feedback",
			Given:     "user feedback",
			When:      "storing",
			Then:      "record verbatim with context",
			ShouldNot: "paraphrase concerns",
		},
		{
			ID:        "uat-plan-revise",
			Given:     "user rejects",
			When:      "plan UAT",
			Then:      "return to proposal phase",
			ShouldNot: "proceed to implementation",
		},
		{
			ID:        "uat-impl-revise",
			Given:     "user rejects",
			When:      "impl UAT",
			Then:      "return to relevant slice",
			ShouldNot: "proceed to landing",
		},
		{
			ID:        "uat-open-ended-alongside",
			Given:     "component questions",
			When:      "presenting",
			Then:      "ALWAYS include an open-ended feedback question alongside the ACCEPT/REVISE decision so the user can raise related concerns",
			ShouldNot: "present only the ACCEPT/REVISE decision without a free-text feedback opportunity",
		},
		{
			ID:        "uat-update-urd",
			Given:     "UAT completes",
			When:      "results are captured",
			Then:      "update the URD with UAT results via `bd comments add <urd-id> \"UAT: <summary>\"`",
			ShouldNot: "leave the URD out of date after UAT",
		},
	},

	Sections: []ProseSection{
		{
			ID:      "uat-phases",
			Title:   "UAT Phases",
			Content: "",
			Subsections: []ProseSection{
				{
					ID:    "uat-plan-phase",
					Title: "Plan UAT (Phase 5 — aura:p5-user:s5-uat)",
					Content: `After 3 reviewers ACCEPT the proposal, present each major design decision to the user one at a time. For each component:
1. Show the proposed interface definition (code snippet)
2. Show a motivating example (how a user would use it)
3. Ask about the specific design choices made (tradeoffs, alternatives considered)`,
				},
				{
					ID:    "uat-impl-phase",
					Title: "Implementation UAT (Phase 11 — aura:p11-user:s11-uat)",
					Content: `After code review consensus, demonstrate what was actually built component by component. For each component:
1. Run the actual command / show real output
2. Compare against the original proposal
3. Ask about the specific behavioral decisions made in the implementation`,
				},
			},
		},
		{
			ID:    "uat-question-design",
			Title: "How to Structure UAT Questions",
			Content: `**Critical:** Questions must split the engineering design space on its ambiguous boundaries to extract maximum information — like a decision tree, where each question bisects the remaining uncertainty.

The user needs to see the actual thing — definition, behavior, example — and then evaluate the specific engineering tradeoffs at the boundaries where the design could go either way.

### Question Design Principles

1. **Each question targets one ambiguous boundary.** Identify where in the design space two or more viable alternatives exist, and ask the user to choose.
2. **Options describe competing tradeoffs, not approval levels.** Each option is a real engineering alternative with its own pros/cons.
3. **Later questions depend on earlier answers.** Structure the survey as a decision tree — Round 1 settles the highest-leverage boundaries, Round 2 addresses dependent decisions informed by Round 1 answers, etc.
4. **Show context before asking.** The user MUST see a code snippet, interface definition, or motivating before/after example before being asked to evaluate.
5. **One component per AskUserQuestion call.** Never batch all components into one survey.`,
		},
		{
			ID:    "uat-wrong-vs-right",
			Title: "Wrong vs Right Question Patterns",
			Content: "**WRONG — generic approval (DO NOT USE):**\n" +
				"```\n" +
				"\"Does this match your vision?\"\n" +
				"options: [\"Yes exactly\", \"Mostly yes\", \"Partially\", \"No\"]\n" +
				"```\n\n" +
				"These fail because the options don't represent engineering alternatives.\n\n" +
				"**RIGHT — boundary-splitting design decisions:**\n" +
				"```\n" +
				"\"The verbose flag adds the following fields to each log entry. Which fields are most useful?\"\n" +
				"options based on actual fields implemented, e.g.:\n" +
				"  - \"session ID on every transcript event — adds noise but enables correlation\"\n" +
				"  - \"backupDir on backup events — confirms where files land\"\n" +
				"  - \"repo path + hash on sync events — confirms which repo was detected\"\n" +
				"  - \"full key=value dump for unknown events — good for debugging\"\n\n" +
				"\"We sanitize emails in file paths by replacing @ with _ and non-alphanumeric chars with _.\n" +
				" Which sanitization behavior is correct?\"\n" +
				"options based on real alternatives:\n" +
				"  - \"@ → _AT_ (reversible, unambiguous)\"\n" +
				"  - \"@ → _ (current behavior, ambiguous if username contains _)\"\n" +
				"  - \"keep @ (valid on most filesystems except Windows)\"\n" +
				"  - \"base64-encode the email (fully reversible, opaque)\"\n" +
				"```\n\n" +
				"These work because each option is a real engineering alternative with clear tradeoffs.",
		},
		{
			ID:    "uat-prerequisite-cross-ref",
			Title: "Pre-requisite: Cross-reference URE Against the Proposal",
			Content: `UAT is the **second time** the user evaluates this feature. Before designing UAT questions, cross-reference the URE responses and URD against the proposal:

` + "```bash" + `
bd show <elicit-id>     # Re-read the user's original URE responses
bd show <urd-id>        # The structured requirements document
bd show <proposal-id>   # The architect's proposal and tradeoffs
` + "```" + `

Look for:
- **Faithful translations:** Where the proposal directly implements a URE choice — confirm it matches their intent
- **Tradeoffs the architect resolved:** Where the URE left ambiguity and the architect chose one direction — surface the choice and its rationale
- **New dimensions the proposal introduced:** Engineering concerns that weren't in the URE — present these with context
- **Gaps or drift:** Where the proposal may have diverged from, reinterpreted, or dropped a URE requirement — flag these explicitly`,
		},
		{
			ID:    "uat-question-sequence",
			Title: "Question Sequence (Decision Tree)",
			Content: `Structure questions to progressively validate the proposal against the user's original requirements.

**Round 1: Highest-leverage tradeoffs** (1-2 questions per AskUserQuestion call)

Start with the 2-3 architectural decisions where the proposal made the biggest choices. For each, show the user:
1. What they originally said in URE (their stated requirement/preference)
2. What the proposal chose (the concrete interface, type, or approach)
3. The alternatives that were considered and why this one was picked

**Round 2: Dependent and derivative decisions** (informed by Round 1)

With the major tradeoffs validated, surface the second-order decisions that flow from them.

**Round 3: New dimensions not in URE** (if any)

Present engineering concerns that emerged after URE (from research, codebase exploration, or reviewer feedback).

**Final: Catch-all**

One open-ended question — "Is there anything from your original requirements that you don't see reflected in this proposal?"`,
		},
		{
			ID:      "uat-component-pattern",
			Title:   "Component-at-a-Time Pattern",
			Content: "",
			Subsections: []ProseSection{
				{
					ID:    "uat-show-definition",
					Title: "Step 1: Show the definition and motivating example",
					Content: "```\n" +
						"Present the interface/type definition (e.g., the TypeScript type or function signature)\n" +
						"Then show a concrete before/after or input/output example:\n\n" +
						"  BEFORE this change:  $ aura watch --follow\n" +
						"  [23:24:20] Updated: session-abc123\n" +
						"  → Backed up 3 files\n\n" +
						"  AFTER this change: $ aura watch --follow --verbose\n" +
						"  [23:24:20] Updated: -home-minttea-dev.../session-abc123\n" +
						"    path: /home/minttea/.claude/projects/...\n" +
						"    session: abc123\n" +
						"    → Repo sync: enqueued (debounced)\n" +
						"```",
				},
				{
					ID:    "uat-ask-decisions",
					Title: "Step 2: Ask about specific decisions",
					Content: `Design-space questions to ask per component type:

**For output/display decisions:**
- Which fields are useful vs. noise at the default verbosity level?
- Which fields belong only in verbose mode?

**For data model / type decisions:**
- Should this be statically defined (enum) or dynamic (string)?
- Should the schema be strict (reject unknown fields) or loose (allow extra)?

**For behavioral/algorithm decisions:**
- Should this fail fast or recover silently?
- Should side effects be eager (immediate) or lazy (deferred/debounced)?

**For API/interface decisions:**
- Is the flag name/command name intuitive?
- Does the default behavior match expectations?`,
				},
			},
		},
		{
			ID:    "uat-survey-template",
			Title: "UAT Survey Template",
			Content: "Use one AskUserQuestion call per component — do NOT batch all components into one survey.\n\n" +
				"```\n" +
				"AskUserQuestion({\n" +
				"  questions: [\n" +
				"    {\n" +
				"      question: `The verbose flag shows the following extra lines for backup events:\n" +
				"  backupDir: /home/user/.aura/aura-sync/repo/project/provider/claude/session/abc123\n" +
				"  session: abc123\n" +
				"Which of these verbose fields are useful?`,\n" +
				"      header: \"Verbose fields\",\n" +
				"      multiSelect: true,\n" +
				"      options: [\n" +
				"        { label: \"backupDir (full path)\", description: \"Shows where the backup actually landed\" },\n" +
				"        { label: \"session ID\", description: \"Enables log correlation across events\" },\n" +
				"      ]\n" +
				"    },\n" +
				"    {\n" +
				"      question: \"Any related feedback, concerns, or gaps not covered above?\",\n" +
				"      header: \"Feedback\",\n" +
				"      multiSelect: false,\n" +
				"      options: [\n" +
				"        { label: \"No additional feedback\", description: \"All concerns addressed\" },\n" +
				"        { label: \"Related concern\", description: \"I have feedback on something adjacent\" },\n" +
				"      ]\n" +
				"    },\n" +
				"    {\n" +
				"      question: \"Do you ACCEPT this component to proceed?\",\n" +
				"      header: \"Decision\",\n" +
				"      multiSelect: false,\n" +
				"      options: [\n" +
				"        { label: \"ACCEPT\", description: \"Proceed to next component\" },\n" +
				"        { label: \"REVISE\", description: \"Needs changes before proceeding\" }\n" +
				"      ]\n" +
				"    }\n" +
				"  ]\n" +
				"})\n" +
				"```",
		},
		{
			ID:      "uat-creating-task",
			Title:   "Creating UAT Task",
			Content: "",
			Subsections: []ProseSection{
				{
					ID:    "uat-plan-task",
					Title: "Plan UAT Task (Phase 5)",
					Content: "```bash\n" +
						"bd create --labels \"aura:p5-user:s5-uat\" \\\n" +
						"  --title \"UAT: Plan acceptance for <feature>\" \\\n" +
						"  --description \"---\n" +
						"references:\n" +
						"  request: <request-task-id>\n" +
						"  urd: <urd-task-id>\n" +
						"  proposal: <proposal-N-id>\n" +
						"---\n" +
						"## Components Reviewed\n\n" +
						"### Component: <component-name>\n" +
						"**Definition shown:** <interface/type/signature shown to user>\n" +
						"**Motivating example shown:** <before/after or input/output example>\n" +
						"**Question asked:** <exact question text>\n" +
						"**Options presented:** <exact option labels and descriptions>\n" +
						"**User response:** <verbatim selection(s)>\n\n" +
						"## Final Decision\n" +
						"<ACCEPT or REVISE with verbatim reason>\"\n\n" +
						"bd dep add <proposal-id> --blocked-by <uat-task-id>\n\n" +
						"# Update URD with plan UAT results\n" +
						"bd comments add <urd-id> \"Plan UAT: <ACCEPT or REVISE> - <summary of key decisions>\"\n" +
						"```",
				},
				{
					ID:    "uat-impl-task",
					Title: "Implementation UAT Task (Phase 11)",
					Content: "```bash\n" +
						"bd create --labels \"aura:p11-user:s11-uat\" \\\n" +
						"  --title \"UAT: Implementation acceptance for <feature>\" \\\n" +
						"  --description \"---\n" +
						"references:\n" +
						"  request: <request-task-id>\n" +
						"  urd: <urd-task-id>\n" +
						"  impl_plan: <impl-plan-task-id>\n" +
						"---\n" +
						"## Components Demonstrated\n\n" +
						"### Component: <component-name>\n" +
						"**Command run / output shown:** <actual terminal output shown to user>\n" +
						"**Question asked:** <exact question>\n" +
						"**User response:** <verbatim response>\n\n" +
						"## Final Decision\n" +
						"<ACCEPT or REVISE>\"\n\n" +
						"bd dep add <impl-plan-id> --blocked-by <impl-uat-task-id>\n\n" +
						"# Update URD with implementation UAT results\n" +
						"bd comments add <urd-id> \"Impl UAT: <ACCEPT or REVISE> - <summary of findings>\"\n" +
						"```",
				},
			},
		},
		{
			ID:    "uat-handling-revise",
			Title: "Handling REVISE",
			Content: `If user selects REVISE:
- **Plan UAT:** Return to architect for proposal revision on the specific component
- **Impl UAT:** Return to relevant slice for implementation fixes

Document the specific component and the user's verbatim feedback in the task description. Do not generalize.`,
		},
	},

	Recipes: []RecipeBlock{},
}
