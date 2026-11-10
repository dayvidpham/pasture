// The authored home of the user-facing writing standard, split into two
// fragments so a reporting skill does not ship a questionnaire guide.
// FragWritingStandard covers wording, the status template, and the wording
// pre-send check. FragWritingQuestions covers question and option design.
// The union is the STE-aligned rules, the house rules, both templates, the
// degrade clause, the exempt material, and the pre-send checks.
//
// Which fragment a rule ships in is the Fragment field on that rule. Which
// skills carry each fragment is writingCarrierRegistry. Tables, intros,
// headings, and carrier numerals are rendered from those two declarations.
// FragmentToOwnerRefs must equal the carrier registry; this file does not
// restate the fragRef set.
package codegen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// WritingFragment is which authored fragment a rule ships in. The zero value
// is wording, so a rule that forgets the field stays in the fragment every
// carrier reads.
type WritingFragment int

const (
	WritingFragmentWording WritingFragment = iota
	WritingFragmentQuestion
)

// WritingRule is one row of the STE-aligned rule table. Requirement is the
// rule in imperative form, Adapts names the STE rule it adapts or "house" where
// STE has no equivalent, and BadToGood is one compact rewrite. Fragment is the
// one declaration of which fragment the rule ships in.
type WritingRule struct {
	Number      int
	Requirement string
	Adapts      string
	BadToGood   string
	Fragment    WritingFragment
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
				"'This pins both the edge and the core case per branch, so a same-count swap fails.' to 'Result: the test now fails if a real case is replaced with a filler case.'", WritingFragmentWording},
			{2, "Ground before you ask. Show what the thing is, where it lives, and one example. Never put the only explanation in the options.", "6.1, 4.4",
				"'Which order: the types to the gate in parallel, or the gate first?' to 'Three parts share one type. The gate reads it. The fixtures copy it. Code: `ir.Event`.'", WritingFragmentQuestion},
			{3, "One topic per paragraph. Max 3 sentences.", "6.5, 6.6",
				"One 25-word sentence with four stacked ideas, to three sentences with one idea each.", WritingFragmentWording},
			{4, "For 3 or more items, a flow, or a comparison, use a list, a table, or an ASCII diagram.", "4.3",
				"'Adopt the first option, then the second, then both' to a three-row table: option, what happens, cost.", WritingFragmentWording},
			{5, "Cut filler, self-narration, hedging, praise, and closing offers.", "house",
				"'It is important to note that this change effectively serves to ensure the test can detect' to 'The test detects a removed case.'", WritingFragmentWording},
		},
	},
	{
		Heading: "Terms",
		Rules: []WritingRule{
			{6, "Put every term the user did not introduce in a Terms block before first use. Max 20 words per definition. Include the code name.", "1.1, 1.6",
				"'per arm' to 'Arm: one of the 5 branches of the skip gate. Code: `arm`.'", WritingFragmentWording},
			{7, "Use the industry term for the concept. Do not invent a term.", "1.8, 1.10",
				"'a same-count swap' to 'a replacement that keeps the test count the same'.", WritingFragmentWording},
			{8, "One name per item, in the message and across the session.", "1.11, 9.4",
				"'the gate', then 'the event types', then 'the IR', to 'the event types' each time.", WritingFragmentWording},
			{9, "One meaning per word. Do not use one word in two senses.", "1.2, 1.3",
				"'pins' for a version lock and 'pins' for a test assertion, to 'locks the version' and 'the test checks'.", WritingFragmentWording},
			{10, "No internal shorthand in prose. Write the substance first, then the reference in parentheses.", "1.10, 2.2, 8.6",
				"'Adopt the option code and skip the explanation' to 'Adopt the retry limit (task: Importer retry limit, round 6).'", WritingFragmentWording},
			{11, "Max 3 words in a noun cluster, and no stack of hidden ideas. A hyphenated stack still hides several. Break it with 'of', 'that', or a clause.", "2.1, 1.9",
				"'mutation-load-bearing core case' to 'the core case (the one case per branch that fails when the branch breaks)'.", WritingFragmentWording},
			{12, "Write each abbreviation in full once, then the short form in parentheses.", "2.2, 8.3",
				"'the continuous integration job, then the CI job, with no first full form' to 'the continuous integration (CI) job, then the CI job.'", WritingFragmentWording},
			{13, "Use plain verbs. No slang, no noun-as-verb, no phrasal verb.", "1.7, 1.10, 9.3",
				"'reddens' to 'fails'.", WritingFragmentWording},
		},
	},
	{
		Heading: "Sentences",
		Rules: []WritingRule{
			{14, "Max 20 words for an instruction or a question. Max 25 for a description. An identifier or a quote counts as one word. The cap is necessary, not sufficient.", "5.1, 6.3, 8.6",
				"'The test checks ten cases, and because each branch needs two named cases and the count is no longer enough, a replacement can pass.' to 'The test checks 10 cases. A filler case can no longer pass.'", WritingFragmentWording},
			{15, "Active voice. Name the actor: the test, CI, the agent, the merge. Simple tenses only.", "3.2, 3.6",
				"'The gate will be rebased on the types.' to 'The agent rebases the gate on the event types.'", WritingFragmentWording},
			{16, "One instruction per sentence. Imperative. Condition first.", "5.2, 5.3, 5.4",
				"'All three at once, then check.' to 'If the types change, rebase the gate. Then run the tests.'", WritingFragmentWording},
			{17, "Full sentences keep articles and verbs. Fragments only in lists, tables, and labels.", "4.2, 4.3, 4.5",
				"'Defer the timeout, keep the retry limit fix' to 'Keep the retry limit. Defer the timeout change. The correctness review is round 3 and the test-quality review is round 2.'", WritingFragmentWording},
			{18, "No semicolons. No em-dashes. Use a period or a list.", "8.1 + house",
				"'per arm, so a swap fails' with an em-dash, to 'per arm. That is 10 checks, so a swap fails.'", WritingFragmentWording},
			{19, "Show the logic with because, so, but, if, then.", "4.4",
				"'10 predicates. A same-count swap fails.' to 'The test checks 10 cases, so a replacement that keeps the count cannot pass.'", WritingFragmentWording},
		},
	},
	{
		Heading: "Exempt material",
		Rules: []WritingRule{
			{20, "Backtick each identifier. Max 2 per sentence. Give a plain gloss on first use.", "8.6 + house",
				"Four bare references in one line, to one task, one design decision, and two reviews, each written in substance first.", WritingFragmentWording},
			{21, "Quote user text, code, paths, and logs exactly. Explain them outside the quote.", "8.6",
				"A quote silently edited, to the blockquote verbatim with a plain reading on the next line.", WritingFragmentWording},
		},
	},
	{
		Heading: "Questions and options",
		Rules: []WritingRule{
			{22, "One question, one decision. Say what the answer changes.", "house",
				"'Which order do you want, and are the labels clear?' to 'Which part merges first? The answer sets the order for the next 3 days.'", WritingFragmentQuestion},
			{23, "Each option is self-contained: a label of max 6 words, what happens, and the cost. Use only Terms words.", "house",
				"'Both' to 'Both limits. The import stops at the first limit it reaches. Cost: one more setting to test.'", WritingFragmentQuestion},
			{24, "Put the recommendation outside the options, with one reason. You may also mark the label '(recommended)'.", "house",
				"An option labelled 'recommended' with no reason, to 'Recommendation: merge the event types first, because a later change costs rework in two places.'", WritingFragmentQuestion},
		},
	},
	{
		Heading: "Risk",
		Rules: []WritingRule{
			{25, "Mark an irreversible action with WARNING or CAUTION, then the condition, then the result.", "7.1, 7.2, 7.3",
				"'Removing the core case will fail the suite.' to 'CAUTION: if you remove the core case, the test passes and a broken branch ships.'", WritingFragmentWording},
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
	if err := validateWritingHouseRules(WritingHouseRules, rules); err != nil {
		return err
	}
	if err := validateWritingQuestionHeadings(groups); err != nil {
		return err
	}
	if err := validateWritingCarrierNumerals(); err != nil {
		return err
	}
	if err := validateWordingFragmentResolves(writingStandardFragment()); err != nil {
		return err
	}
	return nil
}

// validateWordingFragmentResolves rejects a wording fragment that names the
// question section. Status carries this fragment and does not contain that
// section, so the name would be a dangling pointer.
func validateWordingFragmentResolves(fragment SharedFragment) error {
	if fragment.Prose == nil {
		return fmt.Errorf("codegen.ValidateWritingRules: the wording fragment has no prose — why: status would ship without its writing rules; where: internal/codegen/specs_data_writing.go writingStandardFragment; fix: build the prose fragment")
	}
	if proseTreeContains(*fragment.Prose, questionSectionTitle) {
		return fmt.Errorf("codegen.ValidateWritingRules: the wording fragment names %q — why: status carries this fragment and does not contain that section; where: internal/codegen/specs_data_writing.go writingStandardFragment; fix: name the skills that carry question design, not the section title", questionSectionTitle)
	}
	check, ok := proseSubsectionByID(*fragment.Prose, "frag-writing-standard-check")
	if !ok {
		return fmt.Errorf("codegen.ValidateWritingRules: the wording fragment has no pre-send check — why: a reader would send without the list; where: internal/codegen/specs_data_writing.go writingCheckContent; fix: keep the pre-send subsection")
	}
	for _, phrase := range writingQuestionCheckPhrases() {
		if strings.Contains(check.Content, phrase) {
			return fmt.Errorf("codegen.ValidateWritingRules: the wording pre-send check tests %q — why: that check belongs to question rules, which status does not carry; where: internal/codegen/specs_data_writing.go writingCheckContent; fix: move the check to the question fragment", phrase)
		}
	}
	return nil
}

func proseTreeContains(section ProseSection, needle string) bool {
	if strings.Contains(section.Title, needle) || strings.Contains(section.Content, needle) {
		return true
	}
	for _, sub := range section.Subsections {
		if proseTreeContains(sub, needle) {
			return true
		}
	}
	return false
}

func proseSubsectionByID(section ProseSection, id string) (ProseSection, bool) {
	for _, sub := range section.Subsections {
		if sub.Id == id {
			return sub, true
		}
		if found, ok := proseSubsectionByID(sub, id); ok {
			return found, true
		}
	}
	return ProseSection{}, false
}

// validateWritingQuestionHeadings rejects a question-fragment heading that
// claims risk when no risk rule is in that rendered group. The live registry
// splits risk into its own wording group, so the old combined heading fails.
func validateWritingQuestionHeadings(groups []WritingRuleGroup) error {
	for _, group := range writingRuleGroupsForFrom(groups, WritingFragmentQuestion) {
		if !strings.Contains(strings.ToLower(group.Heading), "risk") {
			continue
		}
		hasRisk := false
		for _, rule := range group.Rules {
			if rule.Number == 25 {
				hasRisk = true
			}
		}
		if hasRisk {
			continue
		}
		return fmt.Errorf("codegen.ValidateWritingRules: question group %q claims risk — why: the risk rule is not in that group, so the heading names a rule the reader cannot find; where: internal/codegen/specs_data_writing.go WritingRuleGroups; fix: drop risk from the heading, or move the risk rule into the group", group.Heading)
	}
	return nil
}

// validateWritingCarrierNumerals fails when a carrier count has no word, so a
// sentence cannot fall back to a hardcoded numeral.
func validateWritingCarrierNumerals() error {
	if _, err := writingTitleCount(len(WritingStandardCarriers)); err != nil {
		return err
	}
	if _, err := writingTitleCount(len(WritingQuestionCarriers)); err != nil {
		return err
	}
	return nil
}

// validateWritingHouseRules proves the house table has seven rows: three with
// full text and four explicit pointers. A dangling "below" and a copied rule
// sentence both fail.
func validateWritingHouseRules(house []WritingHouseRule, rules []WritingRule) error {
	if len(house) != 7 {
		return fmt.Errorf("codegen.ValidateWritingRules: the house table has %d rows, want 7 — why: the writing standard requires seven house rules; where: internal/codegen/specs_data_writing.go WritingHouseRules; fix: keep three full-text rows and four pointers", len(house))
	}
	byNumber := map[int]string{}
	for _, rule := range rules {
		byNumber[rule.Number] = rule.Requirement
	}
	pointers := 0
	full := 0
	for _, row := range house {
		if strings.Contains(row.Requirement, "below") {
			return fmt.Errorf("codegen.ValidateWritingRules: house rule %q says \"below\" — why: that word pointed at a table that does not hold the cited rules; where: internal/codegen/specs_data_writing.go WritingHouseRules; fix: name the rule by number, or drop the word", row.Name)
		}
		if strings.HasPrefix(row.Requirement, "Extends rule ") {
			pointers++
			var number int
			if _, err := fmt.Sscanf(row.Requirement, "Extends rule %d.", &number); err != nil {
				return fmt.Errorf("codegen.ValidateWritingRules: house rule %q is not an explicit pointer — why: a pointer must name the rule number; where: internal/codegen/specs_data_writing.go WritingHouseRules; fix: write \"Extends rule N.\"", row.Name)
			}
			if _, ok := byNumber[number]; !ok {
				return fmt.Errorf("codegen.ValidateWritingRules: house rule %q points at rule %d — why: that rule is not in the rule table; where: internal/codegen/specs_data_writing.go WritingHouseRules; fix: point at a rule that exists", row.Name, number)
			}
			continue
		}
		full++
		for _, rule := range rules {
			if strings.Contains(row.Requirement, rule.Requirement) {
				return fmt.Errorf("codegen.ValidateWritingRules: house rule %q repeats rule %d — why: a sentence may appear once; where: internal/codegen/specs_data_writing.go WritingHouseRules; fix: keep the sentence in the rule table and point at it", row.Name, rule.Number)
			}
		}
	}
	if pointers != 4 || full != 3 {
		return fmt.Errorf("codegen.ValidateWritingRules: house table has %d pointers and %d full-text rows — why: the contract is 4 pointers and 3 full-text rows; where: internal/codegen/specs_data_writing.go WritingHouseRules; fix: restore that split", pointers, full)
	}
	for _, group := range WritingRuleGroups {
		if strings.Contains(group.Note, "below") {
			return fmt.Errorf("codegen.ValidateWritingRules: group %q note says \"below\" — why: the cited rules are not in a following house table; where: internal/codegen/specs_data_writing.go WritingRuleGroups; fix: name the house-rule table, and drop \"below\"", group.Heading)
		}
	}
	questionIntro := writingQuestionHouseIntro()
	if strings.Contains(questionIntro, "below") {
		return fmt.Errorf("codegen.ValidateWritingRules: the house intro says \"below\" — why: that word is a dangling cross-reference; where: internal/codegen/specs_data_writing.go writingQuestionHouseIntro; fix: name the table in this section")
	}
	wordingIntro := writingWordingHouseIntro()
	if strings.Contains(wordingIntro, "below") {
		return fmt.Errorf("codegen.ValidateWritingRules: the wording house intro says \"below\" — why: that word is a dangling cross-reference; where: internal/codegen/specs_data_writing.go writingWordingHouseIntro; fix: name the rule, and drop \"below\"")
	}
	if strings.Contains(wordingIntro, questionSectionTitle) {
		return fmt.Errorf("codegen.ValidateWritingRules: the wording house intro names %q — why: status carries the wording fragment and does not contain that section; where: internal/codegen/specs_data_writing.go writingWordingHouseIntro; fix: name the skills that carry question design, not the section title", questionSectionTitle)
	}
	return nil
}

// writingBodyTexts returns the template rows, the degrade clause, the pre-send
// check, the turn-split reason and the closing-offer phrase. Each must appear
// once, so pasting one into another skill fails.
func writingBodyTexts() []string {
	return []string{
		"| <label, max 6 words> | <one sentence> | <one sentence> |",
		"**Status:** <the result or the state, max 25 words>.",
		"An assumption never satisfies a user gate: a gated phase waits for the user's answer.",
		"Search the draft for a token that matches `[A-Z]{1,3}-?[0-9]`, for a section sign (`§`), and for a five- or six-character token that matches `[A-Za-z0-9]{5,6}` and contains both a letter and a digit.",
		writingTurnSplitReason,
		"closing offer",
		writingWordingCarrierSentence(),
		writingQuestionCarrierSentence(),
		writingAskBindingSentence,
		writingQuestionLivesSentence(),
	}
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
// carries an STE number, so there is no Adapts field. Fragment is the same
// declaration the STE rules use.
type WritingHouseRule struct {
	Name        string
	Requirement string
	Fragment    WritingFragment
}

// writingTurnSplitReason is the one home of the turn-split reason. The
// present template points at the house rule by name and does not repeat it.
const writingTurnSplitReason = "a question tool can hide prose sent in the same turn"

// questionSectionTitle is the question fragment's own heading. The wording
// fragment must not use it: status carries wording and does not contain the
// section.
const questionSectionTitle = "Question and Option Design"

// writingQuestionHouseIntro precedes the question house-rule table. The rule
// numbers, the pointer count, and the full-text names come from the Fragment
// field, not from a second list.
func writingQuestionHouseIntro() string {
	var pointers, full []string
	var numbered []struct {
		n    int
		name string
	}
	for _, rule := range writingHouseRulesFor(WritingFragmentQuestion) {
		if n, ok := writingPointerNumber(rule.Requirement); ok {
			pointers = append(pointers, rule.Name)
			numbered = append(numbered, struct {
				n    int
				name string
			}{n, rule.Name})
			continue
		}
		full = append(full, rule.Name)
	}
	sort.Slice(numbered, func(i, j int) bool { return numbered[i].n < numbered[j].n })
	numbers := make([]int, len(numbered))
	duties := make([]string, len(numbered))
	for i, row := range numbered {
		numbers[i] = row.n
		duties[i] = writingLowerFirst(row.name)
	}
	var wording []string
	for _, rule := range writingHouseRulesFor(WritingFragmentWording) {
		wording = append(wording, rule.Name)
	}
	return strings.Join([]string{
		"ASD-STE100 has no rule for questions, options, recommendations, or the turn split around a question tool.",
		"Rules " + writingJoinNumbers(numbers) + " already carry " + writingJoinList(duties) + ".",
		writingPointerCountSentence(len(pointers)),
		writingFullTextSentence(full),
		writingWordingHousePointer(wording),
		"Never cite an STE number for a house rule.",
	}, " ")
}

func writingLowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// writingWordingHouseIntro precedes the wording house rules. It names those
// rules and does not point at a section status does not contain.
func writingWordingHouseIntro() string {
	var names []string
	for _, rule := range writingHouseRulesFor(WritingFragmentWording) {
		names = append(names, rule.Name)
	}
	switch len(names) {
	case 0:
		return "This fragment has no wording house rule."
	case 1:
		return names[0] + " is the wording house rule."
	default:
		return writingJoinList(names) + " are the wording house rules."
	}
}

// writingHousePointer is an explicit pointer. It names the rule and does not
// restate that rule's sentence.
func writingHousePointer(number int) string {
	return fmt.Sprintf("Extends rule %d. Do not restate it.", number)
}

// WritingHouseRules is the one authored registry of the seven house rules.
// Three rows carry full text. Four are pointers to the rules they extend.
var WritingHouseRules = []WritingHouseRule{
	{"Question shape", "A short header, max 4 words.", WritingFragmentQuestion},
	{"Option design", writingHousePointer(23), WritingFragmentQuestion},
	{"Recommendation placement", writingHousePointer(24), WritingFragmentQuestion},
	{"Grounding before asking", writingHousePointer(2), WritingFragmentQuestion},
	{"One decision per question", writingHousePointer(22), WritingFragmentQuestion},
	{"Text-before-question turn", "Send the grounding as text and end the turn. Ask through the `request_user_decision` operation in the next turn, because " + writingTurnSplitReason + ".", WritingFragmentQuestion},
	{"No self-praise", "No self-congratulation. State the next step in one line instead.", WritingFragmentWording},
}

// writingCarrier is one skill and whether it also carries question design.
// writingCarrierRegistry is the one declaration of which skills carry each
// fragment. The two exported slices are derived from it.
type writingCarrier struct {
	Skill    string
	Question bool
}

var writingCarrierRegistry = []writingCarrier{
	{Skill: "architect-handoff", Question: true},
	{Skill: "status"},
	{Skill: "supervisor", Question: true},
	{Skill: "user-elicit", Question: true},
	{Skill: "user-uat", Question: true},
}

func writingCarriers(question bool) []string {
	var out []string
	for _, row := range writingCarrierRegistry {
		if question && !row.Question {
			continue
		}
		out = append(out, row.Skill)
	}
	return out
}

// WritingStandardCarriers names the skills that carry the wording fragment.
// It is derived from writingCarrierRegistry. FragmentToOwnerRefs must match it.
var WritingStandardCarriers = writingCarriers(false)

// WritingQuestionCarriers names the skills that carry question and option
// design. Status asks no question, so its row leaves Question false.
var WritingQuestionCarriers = writingCarriers(true)

func writingWordingCarrierSentence() string {
	return writingTitleCountOrDecimal(len(WritingStandardCarriers)) + " skills carry these wording rules: " + strings.Join(WritingStandardCarriers, ", ") + "."
}

func writingQuestionCarrierSentence() string {
	sentence := writingTitleCountOrDecimal(len(WritingQuestionCarriers)) + " skills carry these question rules: " + strings.Join(WritingQuestionCarriers, ", ") + "."
	omitted := writingSkillsOnlyIn(WritingStandardCarriers, WritingQuestionCarriers)
	switch len(omitted) {
	case 0:
		return sentence
	case 1:
		return sentence + " The " + omitted[0] + " skill asks no question, so it does not carry this fragment."
	default:
		return sentence + " These skills ask no question, so they do not carry this fragment: " + strings.Join(omitted, ", ") + "."
	}
}

// writingQuestionLivesSentence tells a wording reader where question design
// ships. It names skills, not a section title, so it resolves in status.
func writingQuestionLivesSentence() string {
	numbers := writingRuleNumbers(WritingFragmentQuestion)
	return "Question rules " + writingJoinNumbers(numbers) + " ship in " + strings.Join(WritingQuestionCarriers, ", ") + ". This fragment does not repeat them."
}

// writingAskBindingSentence points at the role-tool binding that enforces
// asking through the question tool rather than a subagent.
const writingAskBindingSentence = "Ask through the `request_user_decision` operation, never by launching a subagent. That launch is `delegate_assignment`, a different operation. The role-tool binding in `internal/runtime/profiles.go` lowers `request_user_decision` to the harness question tool and lowers `delegate_assignment` to the subagent tool."

// RenderWritingHouseRules renders one house-rule table. intro is the sentence
// that precedes that table; it is not copied from the other fragment.
func RenderWritingHouseRules(intro string, rules []WritingHouseRule) string {
	var b strings.Builder
	if intro != "" {
		b.WriteString(intro)
		b.WriteString("\n\n")
	}
	b.WriteString("| Rule | Requirement |\n|---|---|")
	for _, rule := range rules {
		fmt.Fprintf(&b, "\n| %s | %s |", rule.Name, rule.Requirement)
	}
	return b.String()
}

// writingRuleGroupsFor returns the rule groups for one fragment. Membership
// is the Fragment field. A question-only remainder of Shape is labelled
// Grounding, which is a display name for that one rule, not a second list.
func writingRuleGroupsFor(home WritingFragment) []WritingRuleGroup {
	return writingRuleGroupsForFrom(WritingRuleGroups, home)
}

func writingRuleGroupsForFrom(groups []WritingRuleGroup, home WritingFragment) []WritingRuleGroup {
	var out []WritingRuleGroup
	for _, group := range groups {
		var rules []WritingRule
		for _, rule := range group.Rules {
			if rule.Fragment == home {
				rules = append(rules, rule)
			}
		}
		if len(rules) == 0 {
			continue
		}
		rendered := WritingRuleGroup{Heading: group.Heading, Rules: rules}
		if home == WritingFragmentQuestion && group.Heading == "Shape" && len(rules) == 1 {
			rendered.Heading = "Grounding"
		}
		if home == WritingFragmentQuestion && writingRulesAreHouse(rules) {
			rendered.Note = "Rules " + writingJoinNumbers(writingNumbers(rules)) + " are house rules. The house-rule table names them by number. Do not attach an STE number to a house rule."
		}
		out = append(out, rendered)
	}
	return out
}

// writingHouseRulesFor returns the house rows whose Fragment field matches.
func writingHouseRulesFor(home WritingFragment) []WritingHouseRule {
	return writingHouseRulesForFrom(WritingHouseRules, home)
}

func writingHouseRulesForFrom(rules []WritingHouseRule, home WritingFragment) []WritingHouseRule {
	var out []WritingHouseRule
	for _, rule := range rules {
		if rule.Fragment == home {
			out = append(out, rule)
		}
	}
	return out
}

// writingStandardFragment builds the wording fragment. Question design is
// writingQuestionFragment; this fragment names that fragment and does not
// copy it.
func writingStandardFragment() SharedFragment {
	prose := ProseSection{
		Id:    string(FragWritingStandard),
		Title: "Writing to the User",
		Content: "These rules adapt ASD-STE100 Issue 9, Part 1 to chat messages. They are " +
			"STE-aligned. The standard's Part 2 dictionary is not adopted, so no message can " +
			"claim full compliance. This fragment covers wording: plain terms, short sentences, " +
			"active voice, and no invented labels, plus the pre-send check and the status template. " +
			writingQuestionLivesSentence() + " " +
			writingWordingCarrierSentence(),
		Subsections: []ProseSection{
			{
				Id:      "frag-writing-standard-rules",
				Title:   "The rules",
				Content: RenderWritingRules(writingRuleGroupsFor(WritingFragmentWording)),
			},
			{
				Id:      "frag-writing-standard-house-rules",
				Title:   "Wording house rule",
				Content: RenderWritingHouseRules(writingWordingHouseIntro(), writingHouseRulesFor(WritingFragmentWording)),
			},
			{
				Id:      "frag-writing-standard-status-template",
				Title:   "Template: status update",
				Content: writingStatusTemplateContent(),
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

// writingQuestionFragment builds the question and option fragment. It is not
// carried by status. It points at the wording fragment for the STE-aligned
// claim and the pre-send check.
func writingQuestionFragment() SharedFragment {
	prose := ProseSection{
		Id:    string(FragWritingQuestions),
		Title: "Question and Option Design",
		Content: "This fragment covers question and option design: grounding before asking, one " +
			"decision, option shape, recommendation placement, the present template, and the " +
			"weak-tool degrade. Wording rules, the STE-aligned claim, the status template, and the " +
			"pre-send check are in Writing to the User. " + writingQuestionCarrierSentence() + " " +
			writingAskBindingSentence,
		Subsections: []ProseSection{
			{
				Id:      "frag-writing-questions-rules",
				Title:   "The rules",
				Content: RenderWritingRules(writingRuleGroupsFor(WritingFragmentQuestion)),
			},
			{
				Id:      "frag-writing-questions-house-rules",
				Title:   "House rules for questions, options and recommendations",
				Content: RenderWritingHouseRules(writingQuestionHouseIntro(), writingHouseRulesFor(WritingFragmentQuestion)),
			},
			{
				Id:      "frag-writing-standard-present-template",
				Title:   "Template: present a component, then ask",
				Content: writingPresentTemplateContent(),
			},
			{
				Id:      "frag-writing-standard-degrade",
				Title:   "Degrade when the question tool is weak",
				Content: writingDegradeContent(),
			},
			{
				Id:      "frag-writing-questions-check",
				Title:   "Pre-send check for a question",
				Content: writingQuestionCheckContent(),
			},
		},
	}
	return SharedFragment{
		Id:    FragWritingQuestions,
		Kind:  FragmentKindProse,
		Prose: &prose,
	}
}

// writingPresentTemplateContent is the component-then-question template. The
// grounding and the question are split across turns so the question tool cannot
// hide the grounding.
func writingPresentTemplateContent() string {
	return strings.Join([]string{
		"Send the block below as text only, then end the turn. In the next turn, ask through the `request_user_decision` operation with the same options. The split is the Text-before-question turn house rule.",
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
		"Budget: 120 words. Every Done item names an artefact. Do not restate the plan. One status per message, not one per thought.",
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
		"Two more rows are relaxations, not exemptions. **Spelling** keeps one spelling per message and does not enforce American spelling. **Identifiers** are not exempt from a gloss, as rule 20 says.",
		"",
		"The caps stay live for everything else. Rule 3 sets paragraph length, rule 14 sets the word caps, and rule 17 sets where a fragment is allowed; none of those three is an exemption.",
	}, "\n")
}

// writingCheckContent is the wording pre-send check. It tests rules this
// fragment carries. Question checks live in writingQuestionCheckContent, so
// status does not test rules it does not carry.
func writingCheckContent() string {
	return strings.Join([]string{
		"Run this list on every user-facing message. A clean pass is not a gate.",
		"",
		"1. Does sentence one give the result, the decision, or the blocker?",
		"2. Does every term the reader did not introduce appear in Terms before first use?",
		"3. Search the draft for a token that matches `[A-Z]{1,3}-?[0-9]`, for a section sign (`§`), and for a five- or six-character token that matches `[A-Za-z0-9]{5,6}` and contains both a letter and a digit. Is each one defined, or replaced by its substance?",
		"4. Does one item have two names, or one word have two senses?",
		"5. Any sentence over 20 words (instruction or question) or 25 (description)?",
		"6. Any noun cluster over 3 words? Any slang verb, noun-as-verb, or phrasal verb?",
		"7. Any semicolon or em-dash?",
		"8. Any passive verb with no actor?",
		"9. Does an irreversible action carry WARNING or CAUTION?",
		"10. Delete one sentence. If nothing is lost, keep it deleted.",
	}, "\n")
}

// writingQuestionCheckPhrases are the pre-send items that test question rules.
// They ship in the question fragment only.
func writingQuestionCheckPhrases() []string {
	return []string{
		"Before a question: did the reader already see what the thing is, its definition, and one example, in an earlier turn or an earlier block?",
		"Can the reader choose from the options alone, with no other document? Is the recommendation outside the options?",
	}
}

// writingQuestionCheckContent is the pre-send list for a question. It names
// Writing to the User, which every question carrier also contains.
func writingQuestionCheckContent() string {
	lines := []string{
		"Run this list before you send a question. It tests the question rules in this fragment. The wording pre-send check is in Writing to the User.",
		"",
	}
	for i, phrase := range writingQuestionCheckPhrases() {
		lines = append(lines, strconv.Itoa(i+1)+". "+phrase)
	}
	return strings.Join(lines, "\n")
}

func writingTitleCount(n int) (string, error) {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve"}
	if n < 0 || n >= len(words) {
		return "", fmt.Errorf("codegen.writingTitleCount: count %d has no word — why: a carrier sentence would need a hardcoded numeral; where: internal/codegen/specs_data_writing.go writingTitleCount; fix: extend the word list or shorten the carrier registry", n)
	}
	word := words[n]
	return strings.ToUpper(word[:1]) + word[1:], nil
}

func writingTitleCountOrDecimal(n int) string {
	word, err := writingTitleCount(n)
	if err != nil {
		return strconv.Itoa(n)
	}
	return word
}

func writingSkillsOnlyIn(all, subset []string) []string {
	have := make(map[string]bool, len(subset))
	for _, name := range subset {
		have[name] = true
	}
	var out []string
	for _, name := range all {
		if !have[name] {
			out = append(out, name)
		}
	}
	return out
}

func writingRuleNumbers(home WritingFragment) []int {
	var numbers []int
	for _, rule := range AllWritingRules() {
		if rule.Fragment == home {
			numbers = append(numbers, rule.Number)
		}
	}
	return numbers
}

func writingNumbers(rules []WritingRule) []int {
	numbers := make([]int, len(rules))
	for i, rule := range rules {
		numbers[i] = rule.Number
	}
	return numbers
}

func writingRulesAreHouse(rules []WritingRule) bool {
	if len(rules) == 0 {
		return false
	}
	for _, rule := range rules {
		if rule.Adapts != "house" {
			return false
		}
	}
	return true
}

func writingJoinNumbers(numbers []int) string {
	parts := make([]string, len(numbers))
	for i, n := range numbers {
		parts[i] = strconv.Itoa(n)
	}
	return writingJoinList(parts)
}

func writingJoinList(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}

func writingPointerNumber(requirement string) (int, bool) {
	var number int
	if _, err := fmt.Sscanf(requirement, "Extends rule %d.", &number); err != nil {
		return 0, false
	}
	return number, true
}

func writingPointerCountSentence(n int) string {
	word := writingTitleCountOrDecimal(n)
	if n == 1 {
		return word + " row in this table is a pointer to those rules."
	}
	return word + " rows in this table are pointers to those rules."
}

func writingFullTextSentence(names []string) string {
	if len(names) == 1 {
		return names[0] + " adds only what those rules do not."
	}
	return writingJoinList(names) + " add only what those rules do not."
}

func writingWordingHousePointer(names []string) string {
	switch len(names) {
	case 0:
		return "No wording house rule is named here."
	case 1:
		return names[0] + " is a wording rule; it is in Writing to the User."
	default:
		return writingJoinList(names) + " are wording rules; they are in Writing to the User."
	}
}
