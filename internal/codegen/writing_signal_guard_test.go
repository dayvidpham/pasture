// Structural guards for the writing standard and the signal-capture
// component. These pin the two authored homes, the exact consumer sets, and the
// properties the requirements name: 25 STE-aligned rules, seven house rules,
// two templates, a pre-send check, five closed signal kinds, and a fixed signal
// line. They are deliberately structural: the behavioural review of the prose
// belongs to the reviewer, not to a string match.
package codegen

import (
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

// TestWritingStandardDeclaredOnce pins the one authored home and the four
// consumers. The rules must not be copied into a consumer body.
func TestWritingStandardDeclaredOnce(t *testing.T) {
	t.Parallel()

	fragment, ok := SharedFragmentSpecs[FragWritingStandard]
	require.True(t, ok, "the writing standard must be declared in SharedFragmentSpecs")
	require.NotNil(t, fragment.Prose, "the writing standard must be a prose fragment")

	owners := FragmentToOwnerRefs()[FragWritingStandard]
	assert.Equal(t,
		[]string{"architect-handoff", "supervisor", "user-elicit", "user-uat"},
		owners,
		"the writing standard must be referenced by exactly four skills")

	// A distinctive sentence from the fragment must appear in exactly one
	// rendered source. Skill bodies carry only a placement marker, so a second
	// occurrence means a consumer copied the rule text.
	const marker = "These rules adapt ASD-STE100"
	sources, err := renderedSources()
	require.NoError(t, err)
	var occurrences int
	for _, source := range sources {
		if strings.Contains(source.Text, marker) {
			occurrences++
			assert.Equal(t, "SharedFragmentSpecs[\"frag--writing-standard\"]", source.Corpus,
				"writing-standard text must live only in its fragment, not in %s %s", source.Corpus, source.Owner)
		}
	}
	assert.Equal(t, 1, occurrences, "the writing standard must be authored once")
}

// TestWritingStandardCarriesTheRequiredParts checks the 25 rules, the seven
// named house rules, both templates, and the pre-send check.
func TestWritingStandardCarriesTheRequiredParts(t *testing.T) {
	t.Parallel()

	fragment := SharedFragmentSpecs[FragWritingStandard]
	require.NotNil(t, fragment.Prose)
	text := fragmentAllText(*fragment.Prose)

	for i := 1; i <= 25; i++ {
		assert.Containsf(t, text, "| "+strconv.Itoa(i)+" |", "the writing standard must carry rule %d", i)
	}

	for _, houseRule := range []string{
		"Question shape",
		"Option design",
		"Recommendation placement",
		"Grounding before asking",
		"One decision per question",
		"Text-before-question turn",
		"No self-praise",
	} {
		assert.Containsf(t, text, houseRule, "the writing standard must carry the %q house rule", houseRule)
	}

	assert.Contains(t, text, "Template: present a component, then ask")
	assert.Contains(t, text, "Template: status update")
	assert.Contains(t, text, "Pre-send check")
	assert.Contains(t, text, "Degrade when the question tool is weak")
	assert.Contains(t, text, "STE-aligned")
	assert.NotContains(t, text, "STE compliant")
	assert.NotContains(t, text, "STE-compliant")
}

// TestSignalCaptureDeclaredOnce pins the one authored home and the five
// consumers.
func TestSignalCaptureDeclaredOnce(t *testing.T) {
	t.Parallel()

	fragment, ok := SharedFragmentSpecs[FragSignalCapture]
	require.True(t, ok, "signal capture must be declared in SharedFragmentSpecs")
	require.NotNil(t, fragment.Prose, "signal capture must be a prose fragment")

	owners := FragmentToOwnerRefs()[FragSignalCapture]
	assert.Equal(t,
		[]string{"architect", "epoch", "supervisor", "user-elicit", "user-uat"},
		owners,
		"signal capture must be referenced by exactly five skills")

	const marker = "signal: kind=<kind> phase=<phase> skill=<skill> entry=<entry-ref>"
	sources, err := renderedSources()
	require.NoError(t, err)
	var occurrences int
	for _, source := range sources {
		if strings.Contains(source.Text, marker) {
			occurrences++
			assert.Equal(t, "SharedFragmentSpecs[\"frag--signal-capture\"]", source.Corpus,
				"signal line must live only in its fragment, not in %s %s", source.Corpus, source.Owner)
		}
	}
	assert.Equal(t, 1, occurrences, "the signal line must be authored once")
}

// TestSignalKindsClosedAndLineOrdered pins the five-kind closed list and the
// fixed field order.
func TestSignalKindsClosedAndLineOrdered(t *testing.T) {
	t.Parallel()

	fragment := SharedFragmentSpecs[FragSignalCapture]
	require.NotNil(t, fragment.Prose)
	text := fragmentAllText(*fragment.Prose)

	for _, kind := range []string{"frustration", "positive-signal", "satisfaction", "confusion", "correction"} {
		assert.Containsf(t, text, "`"+kind+"`", "the closed kind list must carry %q", kind)
	}
	assert.Contains(t, text,
		"signal: kind=<kind> phase=<phase> skill=<skill> entry=<entry-ref> quote=\"<short verbatim quote>\"",
		"the signal line must keep the fixed field order")
	assert.Contains(t, text, "one feedback task per epoch")
	assert.Contains(t, text, "never prompts the user")
	assert.Contains(t, text, "never blocks a phase")
	assert.Contains(t, text, "Do not ask permission to record")
}
