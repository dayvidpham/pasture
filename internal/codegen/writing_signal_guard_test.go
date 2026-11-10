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

var writingRequiredParts = []writingRequiredPart{
	{"frag-writing-standard-rules", []string{"| # | Rule | Adapts | Bad to good |", "Lead with the result, the decision, or the blocker."}},
	{"frag-writing-standard-house-rules", []string{"| Rule | Requirement |", "Never cite an STE number for a house rule."}},
	{"frag-writing-standard-present-template", []string{"**Decision needed:**", "| Option | What happens | Cost |", "**Recommendation:**"}},
	{"frag-writing-standard-status-template", []string{"**Status:**", "**Done**", "**Needs you**"}},
	{"frag-writing-standard-degrade", []string{"Ask one question.", "An assumption never satisfies a user gate"}},
	{"frag-writing-standard-exempt", []string{"Quoted user text", "Code, logs, diffs, test output"}},
	{"frag-writing-standard-check", []string{"Delete one sentence.", "[A-Z]{1,3}-?[0-9]"}},
}

// findWritingStandardGaps is the pure core of the content guard. It reports a
// missing subsection, an empty body, or a missing body phrase. It reads Content
// only, so a Title cannot satisfy it.
func findWritingStandardGaps(prose ProseSection, parts []writingRequiredPart) []string {
	var gaps []string
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
	assert.Subset(t, owners,
		[]string{"architect-handoff", "status", "user-elicit", "user-uat"},
		"the R1 named skills must reference the writing standard")

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
	assert.Empty(t, findWritingStandardGaps(*fragment.Prose, writingRequiredParts),
		"the writing standard must carry every required body")

	present := writingStandardFragment()
	require.True(t, blankSubsection(present.Prose, "frag-writing-standard-present-template"),
		"the defective sample must find the present template")
	assert.NotEmpty(t, findWritingStandardGaps(*present.Prose, writingRequiredParts),
		"blanking a template body must fail the guard")

	check := writingStandardFragment()
	require.True(t, blankSubsection(check.Prose, "frag-writing-standard-check"),
		"the defective sample must find the pre-send check")
	assert.NotEmpty(t, findWritingStandardGaps(*check.Prose, writingRequiredParts),
		"blanking the pre-send check body must fail the guard")
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

// TestWritingRuleRegistryIsClosed proves the rule registry rejects a gap or a
// duplicate rather than renumbering silently.
func TestWritingRuleRegistryIsClosed(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateWritingRules())

	duplicate := []WritingRuleGroup{
		{Heading: "Shape", Rules: []WritingRule{{1, "a", "house", "b"}, {2, "c", "house", "d"}}},
		{Heading: "Terms", Rules: []WritingRule{{2, "e", "house", "f"}}},
	}
	require.Error(t, validateWritingRules(duplicate), "a duplicated rule number must fail")

	empty := []WritingRuleGroup{{Heading: "Shape"}}
	require.Error(t, validateWritingRules(empty), "an empty rule table must fail")
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

// TestSignalLineIsOneRegistry proves the template and both recorded examples
// share one field order, and that the two recording targets are distinct.
func TestSignalLineIsOneRegistry(t *testing.T) {
	t.Parallel()

	template := SignalLineTemplate()
	example := SignalLineExample()
	for _, line := range []string{template, example} {
		rest := line
		for _, field := range SignalLineFields {
			idx := strings.Index(rest, field.Field+"=")
			require.GreaterOrEqualf(t, idx, 0, "line %q must carry field %q", line, field.Field)
			rest = rest[idx+len(field.Field)+1:]
		}
	}

	fragment := SharedFragmentSpecs[FragSignalCapture]
	require.NotNil(t, fragment.Prose)
	text := fragmentAllText(*fragment.Prose)
	require.Equal(t, 1, strings.Count(text, template), "the line template must render once")

	assert.Contains(t, text, "- `backend: pasture`:")
	assert.Contains(t, text, "- `backend: ledger`:")
	assert.Contains(t, text, ".agents.local/pasture/signals.jsonl",
		"the ledger target must name its destination")

	sed := strings.Index(text, "sed -n 's/^feedback: //p'")
	note := strings.Index(text, "[[rec:note]]")
	require.GreaterOrEqualf(t, sed, 0, "the pasture target must read the feedback id from the header")
	require.GreaterOrEqualf(t, note, sed, "the pasture note must follow the id read, so note never runs without an id")
	assert.Contains(t, text[note:], `"$FEEDBACK_ID_URI"`,
		"the pasture note must pass the id the header read produced")
}
