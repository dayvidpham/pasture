// The one authored home of the user-facing writing standard: the STE-aligned
// rules adapted from ASD-STE100 Issue 9, Part 1, the house rules for questions
// and options, the two message templates, the weak-question-tool degrade
// clause, the exempt material, and the pre-send check.
//
// The rule tables are rendered from typed registries, not hand-written rows, so
// a guard can read each rule and prove it is authored once. Which skills
// reference the fragment is derived from their fragRef markers by
// FragmentToOwnerRefs; this file does not restate that set.
package codegen

import (
	"fmt"
	"strings"
)

// WritingRule is one row of the STE-aligned rule table. Requirement is the
// rule in imperative form, Adapts names the STE rule it adapts or "house" where
// STE has no equivalent, and BadToGood is one compact rewrite.
type WritingRule struct {
	Number      int
	Requirement string
	Adapts      string
	BadToGood   string
}

// WritingRuleGroup is one titled table of rules. Note, when set, is the line
// that precedes the table.
type WritingRuleGroup struct {
	Heading string
	Note    string
	Rules   []WritingRule
}

// WritingRuleGroups is the one authored registry of the rules. Rule numbers are
// explicit so a gap or a duplicate fails ValidateWritingRules rather than
// silently renumbering the shipped text.
var WritingRuleGroups = []WritingRuleGroup{
	{
		Heading: "Shape",
		Rules: []WritingRule{
			{1, "Lead with the result, the decision, or the blocker.", "6.1, 6.2",
				"'This pins both the edge and the core case per branch, so a same-count swap fails.' to 'Result: the test now fails if a real case is replaced with a filler case.'"},
			{2, "Ground before you ask. Show what the thing is, where it lives, and one example. Never put the only explanation in the options.", "6.1, 4.4",
				"'Which order: the types to the gate in parallel, or the gate first?' to 'Three parts share one type. The gate reads it. The fixtures copy it. Code: `ir.Event`.'"},
			{3, "One topic per paragraph. Max 3 sentences.", "6.5, 6.6",
				"One 25-word sentence with four stacked ideas, to three sentences with one idea each."},
			{4, "For 3 or more items, a flow, or a comparison, use a list, a table, or an ASCII diagram.", "4.3",
				"'Adopt the first option, then the second, then both' to a three-row table: option, what happens, cost."},
			{5, "Cut filler, self-narration, hedging, praise, and closing offers.", "house",
				"'It is important to note that this change effectively serves to ensure the test can detect' to 'The test detects a removed case.'"},
		},
	},
	{
		Heading: "Terms",
		Rules: []WritingRule{
			{6, "Put every term the user did not introduce in a Terms block before first use. Max 20 words per definition. Include the code name.", "1.1, 1.6",
				"'per arm' to 'Arm: one of the 5 branches of the skip gate. Code: `arm`.'"},
			{7, "Use the industry term for the concept. Do not invent a term.", "1.8, 1.10",
				"'a same-count swap' to 'a replacement that keeps the test count the same'."},
			{8, "One name per item, in the message and across the session.", "1.11, 9.4",
				"'the gate', then 'the event types', then 'the IR', to 'the event types' each time."},
			{9, "One meaning per word. Do not use one word in two senses.", "1.2, 1.3",
				"'pins' for a version lock and 'pins' for a test assertion, to 'locks the version' and 'the test checks'."},
			{10, "No internal shorthand in prose. Write the substance first, then the reference in parentheses.", "1.10, 2.2, 8.6",
				"'Adopt the option code and skip the explanation' to 'Adopt the retry limit (task: Importer retry limit, round 6).'"},
			{11, "Max 3 words in a noun cluster, and no stack of hidden ideas. A hyphenated stack still hides several. Break it with 'of', 'that', or a clause.", "2.1, 1.9",
				"'mutation-load-bearing core case' to 'the core case (the one case per branch that fails when the branch breaks)'."},
			{12, "Write each abbreviation in full once, then the short form in parentheses.", "2.2, 8.3",
				"'the continuous integration job, then the CI job, with no first full form' to 'the continuous integration (CI) job, then the CI job.'"},
			{13, "Use plain verbs. No slang, no noun-as-verb, no phrasal verb.", "1.7, 1.10, 9.3",
				"'reddens' to 'fails'."},
		},
	},
	{
		Heading: "Sentences",
		Rules: []WritingRule{
			{14, "Max 20 words for an instruction or a question. Max 25 for a description. An identifier or a quote counts as one word. The cap is necessary, not sufficient.", "5.1, 6.3, 8.6",
				"'The test checks ten cases, and because each branch needs two named cases and the count is no longer enough, a replacement can pass.' to 'The test checks 10 cases. A filler case can no longer pass.'"},
			{15, "Active voice. Name the actor: the test, CI, the agent, the merge. Simple tenses only.", "3.2, 3.6",
				"'The gate will be rebased on the types.' to 'The agent rebases the gate on the event types.'"},
			{16, "One instruction per sentence. Imperative. Condition first.", "5.2, 5.3, 5.4",
				"'All three at once, then check.' to 'If the types change, rebase the gate. Then run the tests.'"},
			{17, "Full sentences keep articles and verbs. Fragments only in lists, tables, and labels.", "4.2, 4.3, 4.5",
				"'Defer the timeout, keep the retry limit fix' to 'Keep the retry limit. Defer the timeout change. The correctness review is round 3 and the test-quality review is round 2.'"},
			{18, "No semicolons. No em-dashes. Use a period or a list.", "8.1 + house",
				"'per arm, so a swap fails' with an em-dash, to 'per arm. That is 10 checks, so a swap fails.'"},
			{19, "Show the logic with because, so, but, if, then.", "4.4",
				"'10 predicates. A same-count swap fails.' to 'The test checks 10 cases, so a replacement that keeps the count cannot pass.'"},
		},
	},
	{
		Heading: "Exempt material",
		Rules: []WritingRule{
			{20, "Backtick each identifier. Max 2 per sentence. Give a plain gloss on first use.", "8.6 + house",
				"Four bare references in one line, to one task, one design decision, and two reviews, each written in substance first."},
			{21, "Quote user text, code, paths, and logs exactly. Explain them outside the quote.", "8.6",
				"A quote silently edited, to the blockquote verbatim with a plain reading on the next line."},
		},
	},
	{
		Heading: "Questions, options, risk",
		Note:    "Rules 22 to 24 have no STE equivalent. They come from the house rules below. Do not attach an STE number to a house rule.",
		Rules: []WritingRule{
			{22, "One question, one decision. Say what the answer changes.", "house",
				"'Which order do you want, and are the labels clear?' to 'Which part merges first? The answer sets the order for the next 3 days.'"},
			{23, "Each option is self-contained: a label of max 6 words, what happens, and the cost. Use only Terms words.", "house",
				"'Both' to 'Both limits. The import stops at the first limit it reaches. Cost: one more setting to test.'"},
			{24, "Put the recommendation outside the options, with one reason. You may also mark the label '(recommended)'.", "house",
				"An option labelled 'recommended' with no reason, to 'Recommendation: merge the event types first, because a later change costs rework in two places.'"},
			{25, "Mark an irreversible action with WARNING or CAUTION, then the condition, then the result.", "7.1, 7.2, 7.3",
				"'Removing the core case will fail the suite.' to 'CAUTION: if you remove the core case, the test passes and a broken branch ships.'"},
		},
	},
}

// AllWritingRules flattens the registry in table order.
func AllWritingRules() []WritingRule {
	var rules []WritingRule
	for _, group := range WritingRuleGroups {
		rules = append(rules, group.Rules...)
	}
	return rules
}

// ValidateWritingRules proves the rule registry has no gap, duplicate or
// missing field. It is called on the generation path.
func ValidateWritingRules() error {
	return validateWritingRules(WritingRuleGroups)
}

// validateWritingRules is the pure core of the registry guard. It accepts any
// group list, so a defective fixture can prove the guard fires.
func validateWritingRules(groups []WritingRuleGroup) error {
	rules := make([]WritingRule, 0)
	for _, group := range groups {
		if strings.TrimSpace(group.Heading) == "" {
			return fmt.Errorf("codegen.ValidateWritingRules: a rule group has no heading — why: a reader cannot tell what the group covers; where: internal/codegen/specs_data_writing.go WritingRuleGroups; fix: add a heading")
		}
		rules = append(rules, group.Rules...)
	}
	if len(rules) == 0 {
		return fmt.Errorf("codegen.ValidateWritingRules: the rule table is empty — why: the writing standard would ship with no rules; where: internal/codegen/specs_data_writing.go WritingRuleGroups; fix: add the rules")
	}
	seen := make(map[int]bool, len(rules))
	for i, rule := range rules {
		if rule.Number != i+1 {
			return fmt.Errorf("codegen.ValidateWritingRules: rule at position %d carries number %d — why: the shipped numbers must stay gap-free and ordered; where: internal/codegen/specs_data_writing.go WritingRuleGroups; fix: renumber the rule or move it", i+1, rule.Number)
		}
		if seen[rule.Number] {
			return fmt.Errorf("codegen.ValidateWritingRules: rule %d appears twice — why: one number names one rule; where: internal/codegen/specs_data_writing.go WritingRuleGroups; fix: remove the duplicate", rule.Number)
		}
		seen[rule.Number] = true
		if strings.TrimSpace(rule.Requirement) == "" || strings.TrimSpace(rule.Adapts) == "" || strings.TrimSpace(rule.BadToGood) == "" {
			return fmt.Errorf("codegen.ValidateWritingRules: rule %d has an empty requirement, adapts or bad-to-good field — why: a partial row teaches nothing; where: internal/codegen/specs_data_writing.go WritingRuleGroups; fix: fill every field", rule.Number)
		}
	}
	return nil
}

// RenderWritingRules renders the authored rule registry. The table shape and
// every row come from WritingRuleGroups, so the shipped text cannot disagree
// with the registry a guard reads.
func RenderWritingRules(groups []WritingRuleGroup) string {
	var b strings.Builder
	b.WriteString("Format: number, requirement, the STE rule it adapts, then a compact bad-to-good pair.")
	for _, group := range groups {
		b.WriteString("\n\n**")
		b.WriteString(group.Heading)
		b.WriteString("**")
		if group.Note != "" {
			b.WriteString("\n\n")
			b.WriteString(group.Note)
		}
		b.WriteString("\n\n| # | Rule | Adapts | Bad to good |\n|---|---|---|---|")
		for _, rule := range group.Rules {
			fmt.Fprintf(&b, "\n| %d | %s | %s | %s |", rule.Number, rule.Requirement, rule.Adapts, rule.BadToGood)
		}
	}
	return b.String()
}

// WritingHouseRule is one row of the house-rule table. A house rule never
// carries an STE number, so there is no Adapts field.
type WritingHouseRule struct {
	Name        string
	Requirement string
}

// WritingHouseRules is the one authored registry of the house rules. The rows
// add only what rules 2, 22 and 24 do not carry; the intro references those
// rules instead of restating them.
var WritingHouseRules = []WritingHouseRule{
	{"Question shape", "A short header, max 4 words."},
	{"Option design", "Never use approval levels such as 'exactly matches', 'mostly matches', or 'requires revisions' as an option label."},
	{"Text-before-question turn", "Send the grounding as text and end the turn. Ask through the `request_user_decision` operation in the next turn, because a question tool can hide prose sent in the same turn."},
	{"No self-praise", "No self-congratulation, no praise of the question or the user, and no closing offers such as 'let me know if you have questions'. State the next step in one line instead."},
}

// RenderWritingHouseRules renders the house-rule table from its registry.
func RenderWritingHouseRules(rules []WritingHouseRule) string {
	var b strings.Builder
	b.WriteString("ASD-STE100 has no rule for questions, options, recommendations, or the turn split around a question tool. Rules 2, 22 and 24 already carry grounding, one-decision shape and recommendation placement, so the rows below add only what those rules do not. Never cite an STE number for a house rule.")
	b.WriteString("\n\n| Rule | Requirement |\n|---|---|")
	for _, rule := range rules {
		fmt.Fprintf(&b, "\n| %s | %s |", rule.Name, rule.Requirement)
	}
	return b.String()
}

// writingStandardFragment builds the shared writing fragment.
func writingStandardFragment() SharedFragment {
	prose := ProseSection{
		Id:    string(FragWritingStandard),
		Title: "Writing to the User",
		Content: "These rules adapt ASD-STE100 Issue 9, Part 1 to chat messages. They are " +
			"STE-aligned. The standard's Part 2 dictionary is not adopted, so no message can " +
			"claim full compliance.",
		Subsections: []ProseSection{
			{
				Id:      "frag-writing-standard-rules",
				Title:   "The rules",
				Content: RenderWritingRules(WritingRuleGroups),
			},
			{
				Id:      "frag-writing-standard-house-rules",
				Title:   "House rules for questions, options and recommendations",
				Content: RenderWritingHouseRules(WritingHouseRules),
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

// writingPresentTemplateContent is the component-then-question template. The
// grounding and the question are split across turns so the question tool cannot
// hide the grounding.
func writingPresentTemplateContent() string {
	return strings.Join([]string{
		"Send the block below as text only, then end the turn. In the next turn, ask through the `request_user_decision` operation with the same options. The split exists because a question tool can hide prose sent in the same turn.",
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
		"`request_user_decision`: '<question, max 20 words>'",
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
// single-select phrasing, and a stated assumption that never replaces a gate.
func writingDegradeContent() string {
	return strings.Join([]string{
		"Some hosts expose a weak question tool: it takes one question per call, it has no multi-select, or its picker hides option descriptions. Degrade in this order:",
		"",
		"1. Ask one question.",
		"2. Phrase the options as a single-select choice.",
		"3. State the assumption you will use if the answer is unavailable.",
		"",
		"Put the assumption in the text turn that grounds the question, not inside the options. An assumption never satisfies a user gate: a gated phase waits for the user's answer. The question still asks for one decision, and the recommendation names the cost of the assumption.",
	}, "\n")
}

// writingExemptContent writes the exemptions so a reviewer applies the word and
// sentence caps to prose, not to code, quotes, or terms of art. It names the
// rows that are not exemptions so a numbered rule cannot be read as one.
func writingExemptContent() string {
	return strings.Join([]string{
		"Two material classes are exempt from the word and sentence caps: quoted text and code blocks. A third is allowed as technical nouns. Everything else obeys the caps.",
		"",
		"- **Quoted user text.** Verbatim in a blockquote. Never rewrite it into the house style. Put the plain reading after the quote.",
		"- **Code, logs, diffs, test output.** Exempt. Explain them in prose around the block.",
		"- **Industry terms of art.** Fixture, golden test, mutation testing, schema migration, fail-safe default, backfill, and closed enum are allowed as technical nouns. Define one in Terms if the user did not use it first.",
		"",
		"Two more rows are relaxations, not exemptions. **Spelling** keeps one spelling per message and does not enforce American spelling. **Identifiers** are not exempt from a gloss: backtick each one, keep max 2 per sentence, and give one plain gloss on first use, as rule 20 says.",
		"",
		"The caps stay live for everything else. Rule 3 sets paragraph length, rule 14 sets the word caps, and rule 17 sets where a fragment is allowed; none of those three is an exemption.",
	}, "\n")
}

// writingCheckContent is the pre-send check.
func writingCheckContent() string {
	return strings.Join([]string{
		"Run this list on every user-facing message. A clean pass is not a gate.",
		"",
		"1. Does sentence one give the result, the decision, or the blocker?",
		"2. Before a question: did the reader already see what the thing is, its definition, and one example, in an earlier turn or an earlier block?",
		"3. Does every term the reader did not introduce appear in Terms before first use?",
		"4. Search the draft for a token that matches `[A-Z]{1,3}-?[0-9]`, for a section sign (`§`), and for a five- or six-character token that matches `[A-Za-z0-9]{5,6}` and contains both a letter and a digit. Is each one defined, or replaced by its substance?",
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
