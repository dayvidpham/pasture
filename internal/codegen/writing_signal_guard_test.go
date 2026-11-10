// Guards for the writing standard and the signal-capture component.
//
// These prove the properties the requirements name by reading the authored
// registries rather than by matching whole sentences: the rule and house-rule
// tables render once, every required subsection carries a body, the signal
// kinds are a closed set, and the signal line has one field order. Each guard
// ships a clean sample and a defective sample, so a guard that cannot fail is
// visible in this file (core tenets 8 and 9).
package codegen

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fragmentAllText concatenates the fragment's title, content, and every nested
// subsection content, so a test can read the one authored home as one string.
func fragmentAllText(section ProseSection) string {
	parts := []string{section.Title, section.Content}
	for _, sub := range section.Subsections {
		parts = append(parts, fragmentAllText(sub))
	}
	return strings.Join(parts, "\n")
}

// subsectionByID returns one nested subsection by its stable id.
func subsectionByID(section ProseSection, id string) (ProseSection, bool) {
	for _, sub := range section.Subsections {
		if sub.Id == id {
			return sub, true
		}
		if found, ok := subsectionByID(sub, id); ok {
			return found, true
		}
	}
	return ProseSection{}, false
}

// blankSubsection empties one subsection's body while leaving its Title, which
// is the defect a title-only guard cannot see.
func blankSubsection(section *ProseSection, id string) bool {
	for i := range section.Subsections {
		sub := &section.Subsections[i]
		if sub.Id == id {
			sub.Content = ""
			return true
		}
		if blankSubsection(sub, id) {
			return true
		}
	}
	return false
}

// writingRequiredPart names one subsection and the body phrases that prove the
// subsection was not emptied to its title.
type writingRequiredPart struct {
	Subsection  string
	BodyPhrases []string
}

var writingWordingParts = []writingRequiredPart{
	{"frag-writing-standard-rules", []string{"| # | Rule | Adapts | Bad to good |", "Lead with the result, the decision, or the blocker."}},
	{"frag-writing-standard-house-rules", []string{"| Rule | Requirement |", "No self-praise is the wording house rule."}},
	{"frag-writing-standard-status-template", []string{"**Status:**", "**Done**", "**Needs you**"}},
	{"frag-writing-standard-exempt", []string{"Quoted user text", "Code, logs, diffs, test output"}},
	{"frag-writing-standard-check", []string{"Delete one sentence.", "[A-Z]{1,3}-?[0-9]"}},
}

var writingQuestionParts = []writingRequiredPart{
	{"frag-writing-questions-rules", []string{"Ground before you ask.", "One question, one decision."}},
	{"frag-writing-questions-house-rules", []string{"| Rule | Requirement |", "Never cite an STE number for a house rule."}},
	{"frag-writing-standard-present-template", []string{"**Decision needed:**", "| Option | What happens | Cost |", "**Recommendation:**"}},
	{"frag-writing-standard-degrade", []string{"Ask one question.", "An assumption never satisfies a user gate"}},
	{"frag-writing-questions-check", []string{"Before a question:", "choose from the options alone"}},
}

// writingWordingTopLevel is the fragment intro the subsection walk does not
// read: the STE-aligned claim and the derived carrier sentence.
var writingWordingTopLevel = []string{
	"These rules adapt ASD-STE100",
	"They are STE-aligned.",
	"the wording house rule",
	writingWordingCarrierSentence(),
}

// writingQuestionTopLevel is the question fragment intro: the derived carrier
// sentence and the ask-binding sentence.
var writingQuestionTopLevel = []string{
	writingQuestionCarrierSentence(),
	writingAskBindingSentence,
}

// findWritingStandardGaps is the pure core of the content guard. It reports a
// missing top-level phrase, a missing subsection, an empty body, or a missing
// body phrase. It reads Content only, so a Title cannot satisfy it.
func findWritingStandardGaps(prose ProseSection, parts []writingRequiredPart, topLevel []string) []string {
	var gaps []string
	for _, phrase := range topLevel {
		if !strings.Contains(prose.Content, phrase) {
			gaps = append(gaps, "top-level: missing "+strconv.Quote(phrase))
		}
	}
	for _, part := range parts {
		sub, ok := subsectionByID(prose, part.Subsection)
		if !ok {
			gaps = append(gaps, part.Subsection+": missing subsection")
			continue
		}
		if strings.TrimSpace(sub.Content) == "" {
			gaps = append(gaps, part.Subsection+": empty body")
			continue
		}
		for _, phrase := range part.BodyPhrases {
			if !strings.Contains(sub.Content, phrase) {
				gaps = append(gaps, part.Subsection+": body is missing "+strconv.Quote(phrase))
			}
		}
	}
	return gaps
}

// writingRuleNeedles returns one distinctive requirement per authored rule, so
// a duplicated table is visible without pinning a count.
func writingRuleNeedles() []string {
	var needles []string
	for _, rule := range AllWritingRules() {
		needles = append(needles, rule.Requirement)
	}
	for _, rule := range WritingHouseRules {
		needles = append(needles, rule.Requirement)
	}
	return needles
}

// findNonSingleNeedles reports every needle that does not appear exactly once
// across the rendered sources. Exactly-once is the property; a pasted second
// copy fails, and a deleted home leaves zero.
func findNonSingleNeedles(needles []string, sources []renderingSource) []string {
	var findings []string
	for _, needle := range needles {
		count := 0
		for _, source := range sources {
			count += strings.Count(source.Text, needle)
		}
		if count != 1 {
			findings = append(findings, strconv.Quote(needle)+" appears "+strconv.Itoa(count)+" times")
		}
	}
	return findings
}

// TestWritingStandardDeclaredOnce pins the one authored home and the R1 named
// consumers. The rule and house-rule bodies are covered by the exactly-once
// guard below.
func TestWritingStandardDeclaredOnce(t *testing.T) {
	t.Parallel()

	fragment, ok := SharedFragmentSpecs[FragWritingStandard]
	require.True(t, ok, "the writing standard must be declared in SharedFragmentSpecs")
	require.NotNil(t, fragment.Prose, "the writing standard must be a prose fragment")

	owners := FragmentToOwnerRefs()[FragWritingStandard]
	assert.Equal(t, WritingStandardCarriers, owners,
		"the wording fragment must ship in the five declared skills, no more and no fewer")

	question, ok := SharedFragmentSpecs[FragWritingQuestions]
	require.True(t, ok, "question design must be declared in SharedFragmentSpecs")
	require.NotNil(t, question.Prose)
	assert.Equal(t, WritingQuestionCarriers, FragmentToOwnerRefs()[FragWritingQuestions],
		"question design must ship in the four declared skills, and not in status")

	const marker = "These rules adapt ASD-STE100"
	sources, err := renderedSources()
	require.NoError(t, err)
	var occurrences int
	for _, source := range sources {
		if strings.Contains(source.Text, marker) {
			occurrences++
			assert.Equal(t, `SharedFragmentSpecs["frag--writing-standard"]`, source.Corpus,
				"writing-standard text must live only in its fragment, not in %s %s", source.Corpus, source.Owner)
		}
	}
	assert.Equal(t, 1, occurrences, "the writing standard must be authored once")
}

// TestWritingStandardCarriesTheRequiredParts asserts the bodies of the rule
// table, both templates, the degrade clause, the exemptions and the pre-send
// check. Two defective samples blank a body while leaving its title.
func TestWritingStandardCarriesTheRequiredParts(t *testing.T) {
	t.Parallel()

	fragment := SharedFragmentSpecs[FragWritingStandard]
	require.NotNil(t, fragment.Prose)
	assert.Empty(t, findWritingStandardGaps(*fragment.Prose, writingWordingParts, writingWordingTopLevel),
		"the wording fragment must carry every required body and its intro")

	question := SharedFragmentSpecs[FragWritingQuestions]
	require.NotNil(t, question.Prose)
	assert.Empty(t, findWritingStandardGaps(*question.Prose, writingQuestionParts, writingQuestionTopLevel),
		"the question fragment must carry every required body and its intro")

	present := writingQuestionFragment()
	require.True(t, blankSubsection(present.Prose, "frag-writing-standard-present-template"),
		"the defective sample must find the present template")
	assert.NotEmpty(t, findWritingStandardGaps(*present.Prose, writingQuestionParts, writingQuestionTopLevel),
		"blanking a template body must fail the guard")

	check := writingStandardFragment()
	require.True(t, blankSubsection(check.Prose, "frag-writing-standard-check"),
		"the defective sample must find the pre-send check")
	assert.NotEmpty(t, findWritingStandardGaps(*check.Prose, writingWordingParts, writingWordingTopLevel),
		"blanking the pre-send check body must fail the guard")

	intro := writingQuestionFragment()
	intro.Prose.Content = "Question and Option Design"
	assert.NotEmpty(t, findWritingStandardGaps(*intro.Prose, writingQuestionParts, writingQuestionTopLevel),
		"deleting the ask-binding sentence or the carrier numeral from the intro must fail the guard")
}

// TestWritingRulesAreAuthoredOnce proves each rule and house rule has exactly
// one home, so a pasted rule table fails instead of shipping twice.
func TestWritingRulesAreAuthoredOnce(t *testing.T) {
	t.Parallel()

	sources, err := renderedSources()
	require.NoError(t, err)
	needles := writingRuleNeedles()
	require.NotEmpty(t, needles)
	assert.Empty(t, findNonSingleNeedles(needles, sources),
		"every writing rule must be authored exactly once")

	pasted := append(append([]renderingSource(nil), sources...),
		renderingSource{Corpus: `SkillBodySpecs["epoch"]`, Owner: "pasted rule", Text: needles[0]})
	assert.NotEmpty(t, findNonSingleNeedles(needles, pasted),
		"a rule pasted into a second body must fail")
}

// TestWritingBodyTextsAreAuthoredOnce proves a pasted template row, degrade
// clause or pre-send check fails. The turn-split reason and closing-offer
// phrase are in the same needle list so a second home fails too.
func TestWritingBodyTextsAreAuthoredOnce(t *testing.T) {
	t.Parallel()

	sources, err := renderedSources()
	require.NoError(t, err)
	needles := writingBodyTexts()
	require.NotEmpty(t, needles)
	assert.Empty(t, findNonSingleNeedles(needles, sources),
		"each template row, degrade clause and pre-send check must appear once")

	pasted := append(append([]renderingSource(nil), sources...),
		renderingSource{Corpus: `SkillBodySpecs["status"]`, Owner: "pasted row", Text: needles[0]})
	assert.NotEmpty(t, findNonSingleNeedles(needles, pasted),
		"a template row pasted into another skill must fail")
}

// TestWritingRuleRegistryIsClosed proves the rule registry rejects a gap or a
// duplicate rather than renumbering silently.
func TestWritingRuleRegistryIsClosed(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateWritingRules())

	duplicate := []WritingRuleGroup{
		{Heading: "Shape", Rules: []WritingRule{{1, "a", "house", "b", WritingFragmentWording}, {2, "c", "house", "d", WritingFragmentWording}}},
		{Heading: "Terms", Rules: []WritingRule{{2, "e", "house", "f", WritingFragmentWording}}},
	}
	require.Error(t, validateWritingRules(duplicate), "a duplicated rule number must fail")

	empty := []WritingRuleGroup{{Heading: "Shape"}}
	require.Error(t, validateWritingRules(empty), "an empty rule table must fail")
}

// TestWritingHouseRulesRejectADefectiveSample is the two-sample proof that
// validateWritingHouseRules can fail. The clean registry produces no error. A
// row that says "below" does.
func TestWritingHouseRulesRejectADefectiveSample(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateWritingHouseRules(WritingHouseRules, AllWritingRules()))

	bad := append([]WritingHouseRule(nil), WritingHouseRules...)
	bad[0] = WritingHouseRule{Name: bad[0].Name, Requirement: "see the rules below"}
	err := validateWritingHouseRules(bad, AllWritingRules())
	require.Error(t, err, "a house row that says below must fail")
	assert.Contains(t, err.Error(), "below")
}

// TestWritingPartitionFollowsTheFragmentField proves membership is the
// Fragment field. Flipping a rule's field moves it; a parallel number list
// cannot.
func TestWritingPartitionFollowsTheFragmentField(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []int{2, 22, 23, 24}, writingRuleNumbers(WritingFragmentQuestion))
	wording := writingRuleNumbers(WritingFragmentWording)
	assert.NotContains(t, wording, 2)
	assert.Contains(t, wording, 25)
	assert.NotContains(t, houseNames(writingHouseRulesFor(WritingFragmentWording)), "Question shape")
	assert.Contains(t, houseNames(writingHouseRulesFor(WritingFragmentWording)), "No self-praise")

	groups := append([]WritingRuleGroup(nil), WritingRuleGroups...)
	shape := groups[0]
	shape.Rules = append([]WritingRule(nil), groups[0].Rules...)
	for i := range shape.Rules {
		if shape.Rules[i].Number == 2 {
			shape.Rules[i].Fragment = WritingFragmentWording
		}
	}
	groups[0] = shape
	assert.NotContains(t, ruleNumbersIn(writingRuleGroupsForFrom(groups, WritingFragmentQuestion)), 2,
		"flipping rule 2's Fragment field must remove it from the question fragment")

	// The question heading is data on the group, not a rename of "Shape" when
	// one rule remains. A group that declares QuestionHeading renders it. A
	// group that does not keeps its heading even when only one question rule
	// is left.
	declared := writingRuleGroupsFor(WritingFragmentQuestion)
	var sawGrounding bool
	for _, group := range declared {
		if group.Heading == "Grounding" {
			sawGrounding = true
		}
	}
	assert.True(t, sawGrounding, "the Shape group's QuestionHeading must render as Grounding")
	unnamed := []WritingRuleGroup{{
		Heading: "Shape",
		Rules:   []WritingRule{{2, "ground", "house", "example", WritingFragmentQuestion}},
	}}
	got := writingRuleGroupsForFrom(unnamed, WritingFragmentQuestion)
	require.Len(t, got, 1)
	assert.Equal(t, "Shape", got[0].Heading, "a missing QuestionHeading must not be invented from the heading text or the rule count")

	house := append([]WritingHouseRule(nil), WritingHouseRules...)
	house[len(house)-1].Fragment = WritingFragmentQuestion
	assert.Contains(t, houseNames(writingHouseRulesForFrom(house, WritingFragmentQuestion)), "No self-praise",
		"flipping a house rule's Fragment field must move it")
}

// TestWritingCarrierNumeralIsDerived proves the carrier sentence uses the
// registry length. A stale numeral fails the same guard the live fragment passes.
func TestWritingCarrierNumeralIsDerived(t *testing.T) {
	t.Parallel()

	word, err := writingTitleCount(len(WritingStandardCarriers))
	require.NoError(t, err)
	_, err = writingTitleCount(len(WritingQuestionCarriers))
	require.NoError(t, err)

	fragment := SharedFragmentSpecs[FragWritingStandard]
	require.NotNil(t, fragment.Prose)
	want := writingWordingCarrierSentence()
	require.Contains(t, fragment.Prose.Content, want)
	staleWord, err := writingTitleCount(len(WritingStandardCarriers) - 1)
	require.NoError(t, err)
	stale := strings.Replace(want, word, staleWord, 1)
	require.NotEqual(t, want, stale)
	bad := strings.Replace(fragment.Prose.Content, want, stale, 1)
	assert.NotEmpty(t, findWritingStandardGaps(ProseSection{Content: bad}, nil, []string{want}),
		"a carrier numeral that disagrees with the registry must fail")

	src, err := os.ReadFile("specs_data_writing.go")
	require.NoError(t, err)
	assert.NotContains(t, string(src), `"Five skills carry`)
	assert.NotContains(t, string(src), `"Four skills carry`)
	assert.NotContains(t, string(src), "writingQuestionRuleNumbers")
	assert.NotContains(t, string(src), "writingQuestionHouseNames")
	assert.NotContains(t, string(src), "Questions, options, risk")
}

// TestQuestionFragmentDoesNotClaimRisk proves a heading that says risk with
// no risk rule fails, and the live question fragment does not use that heading.
func TestQuestionFragmentDoesNotClaimRisk(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateWritingQuestionHeadings(WritingRuleGroups))
	for _, group := range writingRuleGroupsFor(WritingFragmentQuestion) {
		assert.NotContains(t, strings.ToLower(group.Heading), "risk")
	}
	var sawRisk bool
	for _, group := range writingRuleGroupsFor(WritingFragmentWording) {
		if group.Heading == "Risk" {
			sawRisk = true
			require.Len(t, group.Rules, 1)
			assert.Equal(t, 25, group.Rules[0].Number)
		}
	}
	assert.True(t, sawRisk, "the wording fragment must keep the risk rule under Risk")

	bad := append([]WritingRuleGroup(nil), WritingRuleGroups...)
	for i := range bad {
		if bad[i].Heading == "Questions and options" {
			bad[i].Heading = "Questions, options, risk"
		}
	}
	err := validateWritingQuestionHeadings(bad)
	require.Error(t, err, "a question heading that claims risk must fail")
	assert.Contains(t, err.Error(), "risk")
}

// TestWritingLivesSentenceIsOnlyOnTheWordingOnlyCarrier proves the four
// question carriers do not restate where question rules ship. Status, which
// does not carry the question fragment, is the one document that says it.
func TestWritingLivesSentenceIsOnlyOnTheWordingOnlyCarrier(t *testing.T) {
	t.Parallel()

	sentence := writingQuestionLivesSentence()
	wording := SharedFragmentSpecs[FragWritingStandard]
	require.NotNil(t, wording.Prose)
	assert.NotContains(t, wording.Prose.Content, sentence,
		"the shared wording fragment must not state the carrier fact; question carriers already do")

	status, ok := SkillBodySpecs["status"]
	require.True(t, ok)
	var found bool
	for _, section := range status.Sections {
		if strings.Contains(section.Content, sentence) {
			found = true
		}
	}
	assert.True(t, found, "status, the wording-only carrier, must render the lives-sentence")
}

// TestValidateWritingRulesUsesTheGroupsArgument proves a fixture is validated
// as itself. The live fragment does not name the question section, so a check
// that ignored groups would pass this fixture.
func TestValidateWritingRulesUsesTheGroupsArgument(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateWritingRules())
	poisoned := append([]WritingRuleGroup(nil), WritingRuleGroups...)
	poisoned[0].Rules = append([]WritingRule(nil), WritingRuleGroups[0].Rules...)
	poisoned[0].Rules[0].Requirement = questionSectionTitle
	err := validateWritingRules(poisoned)
	require.Error(t, err, "a groups argument that names the question section must fail")
	assert.Contains(t, err.Error(), questionSectionTitle)
}

// TestStatusPreSendDoesNotTestQuestionRules proves the wording check drops
// the question items, and the question fragment is where those items resolve.
func TestStatusPreSendDoesNotTestQuestionRules(t *testing.T) {
	t.Parallel()

	wording := SharedFragmentSpecs[FragWritingStandard]
	require.NotNil(t, wording.Prose)
	check, ok := subsectionByID(*wording.Prose, "frag-writing-standard-check")
	require.True(t, ok)
	assert.Empty(t, findQuestionChecksInWording(check.Content),
		"the wording pre-send check must not test question rules")
	assert.NotContains(t, fragmentAllText(*wording.Prose), questionSectionTitle,
		"the wording fragment must not point at a section status does not contain")
	_, ok = subsectionByID(*wording.Prose, "frag-writing-standard-status-template")
	assert.True(t, ok, "status must carry its own template")

	question := SharedFragmentSpecs[FragWritingQuestions]
	require.NotNil(t, question.Prose)
	qcheck, ok := subsectionByID(*question.Prose, "frag-writing-questions-check")
	require.True(t, ok)
	for _, phrase := range writingQuestionCheckPhrases() {
		assert.Contains(t, qcheck.Content, phrase)
	}
	_, ok = subsectionByID(*question.Prose, "frag-writing-standard-status-template")
	assert.False(t, ok, "the question fragment must not keep the status template")

	pasted := check.Content + "\n" + writingQuestionCheckPhrases()[0]
	assert.NotEmpty(t, findQuestionChecksInWording(pasted),
		"pasting a question check back into the wording list must fail")
}

func findQuestionChecksInWording(check string) []string {
	var hits []string
	for _, phrase := range writingQuestionCheckPhrases() {
		if strings.Contains(check, phrase) {
			hits = append(hits, phrase)
		}
	}
	return hits
}

func ruleNumbersIn(groups []WritingRuleGroup) []int {
	var numbers []int
	for _, group := range groups {
		numbers = append(numbers, writingNumbers(group.Rules)...)
	}
	return numbers
}

func houseNames(rules []WritingHouseRule) []string {
	names := make([]string, len(rules))
	for i, rule := range rules {
		names[i] = rule.Name
	}
	return names
}

// signalKindBulletPattern matches one closed-kind bullet in fragment prose.
var signalKindBulletPattern = regexp.MustCompile("(?m)^- `([a-z][a-z-]*)`:")

// findUnregisteredSignalKinds reports every bullet token that is neither a
// registered signal kind nor a registered signal-line field name. The field
// names take the same bullet shape in the line section.
func findUnregisteredSignalKinds(content string, kinds []SignalKind, fields []SignalLineField) []string {
	registered := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		registered[string(kind)] = true
	}
	for _, field := range fields {
		registered[field.Field] = true
	}
	var findings []string
	for _, match := range signalKindBulletPattern.FindAllStringSubmatch(content, -1) {
		if !registered[match[1]] {
			findings = append(findings, match[1])
		}
	}
	return findings
}

// TestSignalCaptureDeclaredOnce pins the one authored home and one rendered
// line template.
func TestSignalCaptureDeclaredOnce(t *testing.T) {
	t.Parallel()

	fragment, ok := SharedFragmentSpecs[FragSignalCapture]
	require.True(t, ok, "signal capture must be declared in SharedFragmentSpecs")
	require.NotNil(t, fragment.Prose, "signal capture must be a prose fragment")
	assert.NotEmpty(t, FragmentToOwnerRefs()[FragSignalCapture],
		"signal capture must be referenced by at least one skill")

	marker := SignalLineTemplate()
	sources, err := renderedSources()
	require.NoError(t, err)
	var occurrences int
	for _, source := range sources {
		if strings.Contains(source.Text, marker) {
			occurrences++
			assert.Equal(t, `SharedFragmentSpecs["frag--signal-capture"]`, source.Corpus,
				"the signal line must live only in its fragment, not in %s %s", source.Corpus, source.Owner)
		}
	}
	assert.Equal(t, 1, occurrences, "the signal line must be authored once")
}

// TestSignalKindsAreClosed proves the kind list is a typed closed set: every
// registered kind renders, an unregistered kind cannot render, and a sixth kind
// pasted into the fragment fails.
func TestSignalKindsAreClosed(t *testing.T) {
	t.Parallel()

	kinds := AllSignalKinds()
	require.NoError(t, ValidateSignalKinds())
	content, err := renderSignalKinds(kinds, SignalKindSpecs)
	require.NoError(t, err)
	for _, kind := range kinds {
		assert.Containsf(t, content, "`"+string(kind)+"`:", "the closed kind list must carry %q", kind)
	}
	assert.Empty(t, findUnregisteredSignalKinds(content, kinds, SignalLineFields))

	fragment := SharedFragmentSpecs[FragSignalCapture]
	require.NotNil(t, fragment.Prose)
	text := fragmentAllText(*fragment.Prose)
	assert.Empty(t, findUnregisteredSignalKinds(text, kinds, SignalLineFields),
		"every kind bullet in the fragment must be registered")

	sixth := append(append([]SignalKind(nil), kinds...), SignalKind("impatience"))
	_, err = renderSignalKinds(sixth, SignalKindSpecs)
	require.Error(t, err, "an unregistered sixth kind must fail rendering")
	require.Error(t, validateSignalKinds(sixth, SignalKindSpecs),
		"an unregistered sixth kind must fail the generation gate")

	defective := text + "\n- `impatience`: the user wants the phase to end sooner."
	assert.NotEmpty(t, findUnregisteredSignalKinds(defective, kinds, SignalLineFields),
		"a sixth kind pasted into the fragment must fail the closed-set guard")
}

// TestSignalLineIsOneRegistry proves the template is one JSON object and the
// skill body ships one signal call, not a backend recipe.
func TestSignalLineIsOneRegistry(t *testing.T) {
	t.Parallel()

	template := SignalLineTemplate()
	example := SignalLineExample()
	for _, line := range []string{template, example} {
		var parsed map[string]string
		require.NoError(t, json.Unmarshal([]byte(line), &parsed), "line %q must be one JSON object", line)
		for _, field := range SignalLineFields {
			_, ok := parsed[field.Field]
			assert.Truef(t, ok, "line %q must carry field %q", line, field.Field)
		}
	}

	fragment := SharedFragmentSpecs[FragSignalCapture]
	require.NotNil(t, fragment.Prose)
	text := fragmentAllText(*fragment.Prose)
	require.Equal(t, 1, strings.Count(text, template), "the line template must render once")
	assert.Contains(t, text, "[[rec:signal]]", "the skill body must ship one signal call")
	assert.NotContains(t, text, "[[rec:create]]", "the signal section must not create the feedback task")
	assert.NotContains(t, text, "Pasture target")
	assert.NotContains(t, text, "Ledger target")
	assert.NotContains(t, text, "printf ")
}

// TestSignalCaptureSectionRejectsABackendRecipe is the two-sample guard for
// the tracker clause. A clean Signal Capture section produces no finding. A
// section that pastes a backend recipe does, while the clause still forbids one.
func TestSignalCaptureSectionRejectsABackendRecipe(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateSignalCaptureHasNoBackendRecipe())

	clause := buildTrackerAdapterContent()
	require.Contains(t, clause, trackerClauseForbidsBackendRecipe)

	fragment := SharedFragmentSpecs[FragSignalCapture]
	require.NotNil(t, fragment.Prose)
	rendered, err := ResolveBodyMarkers(proseTreeText(*fragment.Prose), HarnessClaudeCode)
	require.NoError(t, err)
	clean := "## Tracker Adapter\n" + clause + "\n\n## Signal Capture\n" + rendered + "\n\n## Next\n"
	section, ok := SignalCaptureSection(clean)
	require.True(t, ok, "the rendered skill must contain a Signal Capture section")
	assert.Empty(t, findSignalBackendRecipes(section, clause),
		"a rendered Signal Capture section must not name a backend recipe")
	assert.NotContains(t, section, "printf ", "the adapter recipes must stay outside the section")

	defective := strings.Replace(clean, "## Next", "printf '%s\\n' 'x' >> .agents.local/pasture/signals.jsonl\n\n## Next", 1)
	require.NotEqual(t, clean, defective, "the defective sample must actually paste a recipe")
	bad, ok := SignalCaptureSection(defective)
	require.True(t, ok)
	assert.NotEmpty(t, findSignalBackendRecipes(bad, clause),
		"a pasted backend recipe must fail while the tracker clause forbids one")
}

// TestFeedbackTaskHasOneCreator proves the epoch start step is the only place
// that creates the feedback task, and that the signal section says how a
// second run finds it.
func TestFeedbackTaskHasOneCreator(t *testing.T) {
	t.Parallel()

	const title = "FEEDBACK: epoch signals"
	sources, err := renderedSources()
	require.NoError(t, err)
	var homes []string
	for _, source := range sources {
		if strings.Contains(source.Text, title) {
			homes = append(homes, source.Corpus+" "+source.Owner)
		}
	}
	require.Len(t, homes, 1, "only one skill body may create the feedback task, got %v", homes)
	assert.Contains(t, homes[0], `SkillBodySpecs["epoch"]`, "the creator must be the epoch skill")
	assert.Contains(t, homes[0], "epoch-starting", "the creator must be the epoch start step")

	fragment := SharedFragmentSpecs[FragSignalCapture]
	text := fragmentAllText(*fragment.Prose)
	assert.Contains(t, text, feedbackLookupRule)
	assert.Contains(t, text, "On the ledger backend, create nothing and record nothing")
	assert.NotContains(t, text, title, "the signal section must not carry a second create")

	assert.Empty(t, findNonSingleNeedles([]string{feedbackLookupRule}, sources),
		"the feedback lookup rule must have one home")
	pasted := append(append([]renderingSource(nil), sources...),
		renderingSource{Corpus: `SkillBodySpecs["epoch"]`, Owner: "restated lookup", Text: feedbackLookupRule})
	assert.NotEmpty(t, findNonSingleNeedles([]string{feedbackLookupRule}, pasted),
		"a second copy of the lookup rule must fail")
}

// TestUATTemplateIsOneDecisionPerCall proves each survey call asks one question.
func TestUATTemplateIsOneDecisionPerCall(t *testing.T) {
	t.Parallel()

	body, ok := SkillBodySpecs["user-uat"]
	require.True(t, ok)
	var template string
	var walk func(ProseSection)
	walk = func(section ProseSection) {
		if section.Id == "uat-survey-template" {
			template = section.Content
		}
		for _, sub := range section.Subsections {
			walk(sub)
		}
	}
	for _, section := range body.Sections {
		walk(section)
	}
	require.NotEmpty(t, template, "the UAT survey template must exist")
	calls := strings.Split(template, "[[op:request_user_decision]]")
	var decisions int
	for _, call := range calls[1:] {
		if !strings.Contains(call, "questions:") {
			continue
		}
		decisions++
		assert.Equal(t, 1, strings.Count(call, "question:"),
			"call %d must ask one question", decisions)
	}
	assert.GreaterOrEqual(t, decisions, 3, "the template must split its questions into separate calls")
}

// TestElicitExampleIsOneDecisionPerCall proves the example survey does not
// batch several decisions into one call.
func TestElicitExampleIsOneDecisionPerCall(t *testing.T) {
	t.Parallel()

	body, ok := SkillBodySpecs["user-elicit"]
	require.True(t, ok)
	var template string
	var walk func(ProseSection)
	walk = func(section ProseSection) {
		if section.Id == "user-elicit-example-survey" {
			template = section.Content
		}
		for _, sub := range section.Subsections {
			walk(sub)
		}
	}
	for _, section := range body.Sections {
		walk(section)
	}
	require.NotEmpty(t, template, "the elicit example survey must exist")
	calls := strings.Split(template, "[[op:request_user_decision]]")
	var decisions int
	for _, call := range calls[1:] {
		if !strings.Contains(call, "questions:") {
			continue
		}
		decisions++
		assert.Equal(t, 1, strings.Count(call, "question:"),
			"call %d must ask one question", decisions)
	}
	assert.GreaterOrEqual(t, decisions, 4, "the example must split its four questions into separate calls")
}
