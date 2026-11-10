// The one authored home of the user-facing writing standard: 25 rules adapted
// from ASD-STE100 Issue 9, Part 1, seven house rules for questions and options,
// two message templates, the weak-question-tool degrade clause, the exempt
// material, and the pre-send check.
//
// The fragment is referenced by exactly four skills (user-elicit, user-uat,
// architect-handoff, supervisor) through fragRef, so the rules exist once in the
// tree and render into each consumer without a copy. The standard's Part 2
// dictionary is not adopted: the text says STE-aligned, never a compliance claim.
package codegen

import "strings"

// writingStandardFragment builds the shared writing fragment. The rule text is
// authored here once; every consumer references FragWritingStandard.
func writingStandardFragment() SharedFragment {
	prose := ProseSection{
		Id:    string(FragWritingStandard),
		Title: "Writing to the User",
		Content: "These rules adapt ASD-STE100 Issue 9, Part 1 to chat messages. They are " +
			"STE-aligned. The standard's Part 2 dictionary is not adopted, so no message can " +
			"claim full compliance. The rules are authored here once; the user-elicit, " +
			"user-uat, architect-handoff and supervisor skills reference them.",
		Subsections: []ProseSection{
			{
				Id:      "frag-writing-standard-rules",
				Title:   "The 25 rules",
				Content: writingRulesContent(),
			},
			{
				Id:      "frag-writing-standard-house-rules",
				Title:   "House rules for questions, options and recommendations",
				Content: writingHouseRulesContent(),
			},
			{
				Id:      "frag-writing-standard-present-template",
				Title:   "Template: present a component, then ask",
				Content: writingPresentTemplateContent(),
			},
			{
				Id:      "frag-writing-standard-status-template",
				Title:   "Template: status update",
				Content: writingStatusTemplateContent(),
			},
			{
				Id:      "frag-writing-standard-degrade",
				Title:   "Degrade when the question tool is weak",
				Content: writingDegradeContent(),
			},
			{
				Id:      "frag-writing-standard-exempt",
				Title:   "Exempt material",
				Content: writingExemptContent(),
			},
			{
				Id:      "frag-writing-standard-check",
				Title:   "Pre-send check",
				Content: writingCheckContent(),
			},
		},
	}
	return SharedFragment{
		Id:    FragWritingStandard,
		Kind:  FragmentKindProse,
		Prose: &prose,
	}
}

// writingRulesContent is the 25-rule table. Each row names the requirement, the
// STE rule it adapts (or "house" where STE has no equivalent), and a compact
// bad-to-good pair. Rule numbers are cited; the wording is ours.
func writingRulesContent() string {
	return strings.Join([]string{
		"Format: number, requirement, the STE rule it adapts, then a compact bad-to-good pair.",
		"",
		"**Shape**",
		"",
		"| # | Rule | Adapts | Bad to good |",
		"|---|---|---|---|",
		"| 1 | Lead with the result, the decision, or the blocker. | 6.1, 6.2 | 'This pins both the edge and the core case per branch, so a same-count swap fails.' to 'Result: the test now fails if a real case is replaced with a filler case.' |",
		"| 2 | Ground before you ask. Show what the thing is, where it lives, and one example. Never put the only explanation in the options. | 6.1, 4.4 | 'Which order: the types to the gate in parallel, or the gate first?' to 'Three parts share one type. The gate reads it. The fixtures copy it. Code: `ir.Event`.' |",
		"| 3 | One topic per paragraph. Max 3 sentences. | 6.5, 6.6 | One 25-word sentence with four stacked ideas, to three sentences with one idea each. |",
		"| 4 | For 3 or more items, a flow, or a comparison, use a list, a table, or an ASCII diagram. | 4.3 | 'Adopt the first option, then the second, then both' to a three-row table: option, what happens, cost. |",
		"| 5 | Cut filler, self-narration, hedging, praise, and closing offers. | house | 'It is important to note that this change effectively serves to ensure the test can detect' to 'The test detects a removed case.' |",
		"",
		"**Terms**",
		"",
		"| # | Rule | Adapts | Bad to good |",
		"|---|---|---|---|",
		"| 6 | Put every term the user did not introduce in a Terms block before first use. Max 20 words per definition. Include the code name. | 1.1, 1.6 | 'per arm' to 'Arm: one of the 5 branches of the skip gate. Code: `arm`.' |",
		"| 7 | Use the industry term for the concept. Do not invent a term. | 1.8, 1.10 | 'a same-count swap' to 'a replacement that keeps the test count the same'. |",
		"| 8 | One name per item, in the message and across the session. | 1.11, 9.4 | 'the gate', then 'the event types', then 'the IR', to 'the event types' each time. |",
		"| 9 | One meaning per word. Do not use one word in two senses. | 1.2, 1.3 | 'pins' for a version lock and 'pins' for a test assertion, to 'locks the version' and 'the test checks'. |",
		"| 10 | No internal shorthand in prose. Write the substance first, then the reference in parentheses. | 1.10, 2.2, 8.6 | 'Adopt the option code and skip the explanation' to 'Adopt the retry limit (task: Importer retry limit, round 6).' |",
		"| 11 | Max 3 words in a noun cluster, and no stack of hidden ideas. A hyphenated stack still hides several. Break it with 'of', 'that', or a clause. | 2.1, 1.9 | 'mutation-load-bearing core case' to 'the core case (the one case per branch that fails when the branch breaks)'. |",
		"| 12 | Write each abbreviation in full once, then the short form in parentheses. | 2.2, 8.3 | 'the continuous integration job, then the CI job, with no first full form' to 'the continuous integration (CI) job, then the CI job.' |",
		"| 13 | Use plain verbs. No slang, no noun-as-verb, no phrasal verb. | 1.7, 1.10, 9.3 | 'reddens' to 'fails'. |",
		"",
		"**Sentences**",
		"",
		"| # | Rule | Adapts | Bad to good |",
		"|---|---|---|---|",
		"| 14 | Max 20 words for an instruction or a question. Max 25 for a description. An identifier or a quote counts as one word. The cap is necessary, not sufficient. | 5.1, 6.3, 8.6 | 'The test checks ten cases, and because each branch needs two named cases and the count is no longer enough, a replacement can pass.' to 'The test checks 10 cases. A filler case can no longer pass.' |",
		"| 15 | Active voice. Name the actor: the test, CI, the agent, the merge. Simple tenses only. | 3.2, 3.6 | 'The gate will be rebased on the types.' to 'The agent rebases the gate on the event types.' |",
		"| 16 | One instruction per sentence. Imperative. Condition first. | 5.2, 5.3, 5.4 | 'All three at once, then check.' to 'If the types change, rebase the gate. Then run the tests.' |",
		"| 17 | Full sentences keep articles and verbs. Fragments only in lists, tables, and labels. | 4.2, 4.3, 4.5 | 'Defer the timeout, keep the retry limit fix' to 'Keep the retry limit. Defer the timeout change. The correctness review is round 3 and the test-quality review is round 2.' |",
		"| 18 | No semicolons. No em-dashes. Use a period or a list. | 8.1 | 'per arm, so a swap fails' with an em-dash, to 'per arm. That is 10 checks, so a swap fails.' |",
		"| 19 | Show the logic with because, so, but, if, then. | 4.4 | '10 predicates. A same-count swap fails.' to 'The test checks 10 cases, so a replacement that keeps the count cannot pass.' |",
		"",
		"**Exempt material**",
		"",
		"| # | Rule | Adapts | Bad to good |",
		"|---|---|---|---|",
		"| 20 | Backtick each identifier. Max 2 per sentence. Give a plain gloss on first use. | 8.6 | Four bare references in one line, to one task, one design decision, and two reviews, each written in substance first. |",
		"| 21 | Quote user text, code, paths, and logs exactly. Explain them outside the quote. | 8.6 | A quote silently edited, to the blockquote verbatim with a plain reading on the next line. |",
		"",
		"**Questions, options, risk**",
		"",
		"Rules 22 to 24 have no STE equivalent. They come from the house rules below. Do not attach an STE number to a house rule.",
		"",
		"| # | Rule | Adapts | Bad to good |",
		"|---|---|---|---|",
		"| 22 | One question, one decision. Say what the answer changes. | house | 'Which order do you want, and are the labels clear?' to 'Which part merges first? The answer sets the order for the next 3 days.' |",
		"| 23 | Each option is self-contained: a label of max 6 words, what happens, and the cost. Use only Terms words. | house | 'Both' to 'Both limits. The import stops at the first limit it reaches. Cost: one more setting to test.' |",
		"| 24 | Put the recommendation outside the options, with one reason. You may also mark the label '(recommended)'. | house | An option labelled 'recommended' with no reason, to 'Recommendation: merge the event types first, because a later change costs rework in two places.' |",
		"| 25 | Mark an irreversible action with WARNING or CAUTION, then the condition, then the result. | 7.1, 7.2, 7.3 | 'Removing the core case will fail the suite.' to 'CAUTION: if you remove the core case, the test passes and a broken branch ships.' |",
		"",
		"Rules 1.9, 1.10, 2.1 and 2.2 carry most of the value. If you keep four rules, keep those four.",
	}, "\n")
}

// writingHouseRulesContent is the seven-house-rule table that STE does not
// cover. The labels here are the ones the user asked for; they are house rules,
// not STE rules.
func writingHouseRulesContent() string {
	return strings.Join([]string{
		"ASD-STE100 has no rule for questions, options, recommendations, or the turn split around a question tool. Write these seven rules yourself, and never cite an STE number for them.",
		"",
		"| Rule | Requirement |",
		"|---|---|",
		"| Question shape | Each question asks for one decision, names the component, and states what the answer changes. Use a short header, max 4 words. |",
		"| Option design | Each option is a self-contained engineering alternative: a label of max 6 words, what happens, and the cost. Never use approval levels such as 'exactly matches', 'mostly matches', or 'requires revisions'. Never put a bare reference in an option. |",
		"| Recommendation placement | Put the recommendation outside the options, with one reason and the tradeoff you accept. You may also mark one label '(recommended)'. |",
		"| Grounding before asking | Show the definition, one concrete example, and the decision's consequence in normal message text before the question. Never put the only explanation in the options. |",
		"| One decision per question | Never bundle two decisions. If the answer needs two choices, split the question. |",
		"| Text-before-question turn | Send the grounding as text and end the turn. Ask through the [[op:request_user_decision]] tool in the next turn, because a question tool can hide prose sent in the same turn. |",
		"| No self-praise | No self-congratulation, no praise of the question or the user, and no closing offers such as 'let me know if you have questions'. State the next step in one line instead. |",
	}, "\n")
}

// writingPresentTemplateContent is the component-then-question template. The
// grounding and the question are split across turns so the question tool cannot
// hide the grounding.
func writingPresentTemplateContent() string {
	return strings.Join([]string{
		"Send the block below as text only, then end the turn. In the next turn, call the [[op:request_user_decision]] tool with the same options. The split exists because a question tool can hide prose sent in the same turn.",
		"",
		"~~~~markdown",
		"## <Part name>: <what it does for the user, max 10 words>",
		"",
		"**Decision needed:** <one sentence>. The answer changes <what>.",
		"",
		"**Terms**",
		"- **<term>**: <max 20 words>. Code: `<identifier>`.",
		"- **<term>**: <max 20 words>.",
		"",
		"**What it is**",
		"<max 3 sentences. Name the actor. One fact per sentence.>",
		"",
		"**Definition**",
		"~~~<lang>",
		"<the smallest type, interface, schema, or command shape that shows the choice>",
		"~~~",
		"",
		"**Example**",
		"~~~",
		"<one realistic before and after, or input and output>",
		"~~~",
		"",
		"**Diagram**",
		"~~~",
		"<ASCII, plain labels, no em-dash>",
		"~~~",
		"",
		"**Options**",
		"",
		"| Option | What happens | Cost |",
		"|---|---|---|",
		"| <label, max 6 words> | <one sentence> | <one sentence> |",
		"| <label> | <one sentence> | <one sentence> |",
		"",
		"**Recommendation:** <option>, because <one reason>.",
		"~~~~",
		"",
		"Then, in the next turn:",
		"",
		"~~~",
		"[[op:request_user_decision]]: '<question, max 20 words>'",
		"options: the same labels, with the 'what happens' and 'cost' text repeated in each description, because the picker can hide the table.",
		"~~~",
		"",
		"One component per call. Use multi-select only when the options combine. Always add one open-ended feedback question.",
	}, "\n")
}

// writingStatusTemplateContent is the status-update template.
func writingStatusTemplateContent() string {
	return strings.Join([]string{
		"~~~markdown",
		"**Status:** <the result or the state, max 25 words>.",
		"",
		"**Done**",
		"- <artifact or outcome, past tense, actor named> (`<path>`, pull request #N, or task title).",
		"",
		"**Next**",
		"- <what happens next, and who does it>.",
		"",
		"**Needs you** (omit if empty)",
		"- <the decision>, needed for <what it unblocks>.",
		"",
		"**Risk** (omit if empty)",
		"- WARNING or CAUTION: <condition>. <result>.",
		"",
		"**Terms** (new terms only)",
		"- **<term>**: <max 20 words>.",
		"~~~",
		"",
		"Budget: 120 words. Every Done item names an artefact. Do not restate the plan. Do not add a closing offer. One status per message, not one per thought.",
	}, "\n")
}

// writingDegradeContent is the weak-question-tool clause: one question,
// single-select phrasing, and a stated assumption.
func writingDegradeContent() string {
	return strings.Join([]string{
		"Some hosts expose a weak question tool: it takes one question per call, it has no multi-select, or its picker hides option descriptions. Degrade in this order:",
		"",
		"1. Ask one question.",
		"2. Phrase the options as a single-select choice.",
		"3. State the assumption you will use if the answer is unavailable.",
		"",
		"Put the assumption in the text turn that grounds the question, not inside the options. The question still asks for one decision, and the recommendation names the cost of the assumption.",
	}, "\n")
}

// writingExemptContent writes the exemptions so a reviewer does not apply the
// word and punctuation caps to code, quotes, or lists.
func writingExemptContent() string {
	return strings.Join([]string{
		"- **Identifiers.** Code identifiers, paths, commands, flags, URIs, commit IDs, and task references: write them verbatim in backticks. Max 2 per sentence. Give one plain gloss on first use. A backticked identifier still needs a gloss; the word-count rule is not a permission slip.",
		"- **Quoted user text.** Verbatim in a blockquote. Never rewrite it into the house style. Put the plain reading after the quote.",
		"- **Code, logs, diffs, test output.** Exempt. Explain them in prose around the block.",
		"- **Industry terms of art.** Fixture, golden test, mutation testing, schema migration, fail-safe default, backfill, and closed enum are allowed as technical nouns. Define one in Terms if the user did not use it first.",
		"- **Spelling.** Keep one spelling per message. Do not enforce American spelling.",
		"- **Paragraphs.** Max 3 sentences in chat, stricter than the STE limit of 6.",
		"- **Fragments.** Allowed in list items, table cells, and diagram labels. A full sentence keeps its articles and verbs.",
		"- **Word caps.** Max 20 words for an instruction or a question, max 25 for a description. An identifier or a quote counts as one word. The cap is necessary, not sufficient.",
	}, "\n")
}

// writingCheckContent is the 12-item pre-send check.
func writingCheckContent() string {
	return strings.Join([]string{
		"Run this list on every user-facing message. Items 1, 2, 10 and 12 need judgment; the rest are mechanical. A clean pass is not a gate.",
		"",
		"1. Does sentence one give the result, the decision, or the blocker?",
		"2. Before a question: did the reader already see what the thing is, its definition, and one example, in an earlier turn or an earlier block?",
		"3. Does every term the reader did not introduce appear in Terms before first use?",
		"4. Search the draft for label-like tokens (one to three capital letters with a number), the section sign, and mixed five- or six-character tokens. Is each one defined, or replaced by its substance?",
		"5. Does one item have two names, or one word have two senses?",
		"6. Any sentence over 20 words (instruction or question) or 25 (description)?",
		"7. Any noun cluster over 3 words? Any slang verb, noun-as-verb, or phrasal verb?",
		"8. Any semicolon or em-dash?",
		"9. Any passive verb with no actor?",
		"10. Can the reader choose from the options alone, with no other document? Is the recommendation outside the options?",
		"11. Does an irreversible action carry WARNING or CAUTION?",
		"12. Delete one sentence. If nothing is lost, keep it deleted.",
	}, "\n")
}
