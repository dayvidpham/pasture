package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rawHarnessLiteralPatterns are the native call prefixes and harness phrases a
// rendering source must not spell. Each one is authored as a typed marker
// instead, so the name is resolved once per target.
//
// TeamCreate is included because the retired mechanism must not return to
// generated prose; it has no IR kind and no lowering, so a source that names it
// was never resolved.
var rawHarnessLiteralPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bSkill\(`),
	regexp.MustCompile(`\bTask\(`),
	regexp.MustCompile(`\bSendMessage\b`),
	regexp.MustCompile(`\bAskUserQuestion\b`),
	regexp.MustCompile(`\bpasture task\s+(create|label|dep|comment|show|list|blocked|ready|close|update|comments|timeline|agents|assignment)\b`),
	regexp.MustCompile(`(?i)\b(?:Task|Agent|Skill|SendMessage)\s+tool\b`),
	regexp.MustCompile(`\bTeamCreate\b`),
}

// articleBeforeMarkerPattern catches an indefinite article that agrees with a
// raw marker placeholder rather than with any resolved noun. No single article
// fits AskUserQuestion / question / request-input, so the source must avoid the
// construction instead of repairing it per target.
var articleBeforeMarkerPattern = regexp.MustCompile(`\b(?:a|an)\s+\[\[`)

// findRawHarnessLiterals is the pure core of the rendering-source literal guard.
// It returns one finding per raw harness literal in content.
func findRawHarnessLiterals(content string) []string {
	var findings []string
	for _, pattern := range rawHarnessLiteralPatterns {
		for _, match := range pattern.FindAllString(content, -1) {
			findings = append(findings, match)
		}
	}
	return findings
}

// TestBodyLiteralGuardHasTwoSamples proves the guard can fail and can pass for
// every literal class. One defective sample must produce at least one finding;
// one clean sample must produce none. Tenets 8 and 9.
func TestBodyLiteralGuardHasTwoSamples(t *testing.T) {
	t.Parallel()

	defective := []string{
		"Run `pasture task show \"${ID}\"` and call `Skill(/pasture:worker)`.",
		"Call `SendMessage` with the assignment, or use TeamCreate.",
		"Spawn the worker with the Task tool and the reviewer with the Agent tool.",
	}
	for _, sample := range defective {
		findings := findRawHarnessLiterals(sample)
		require.NotEmpty(t, findings, "defective sample must produce at least one finding: %q", sample)
	}

	clean := "Run `[[rec]] show \"${ID}\"` and call `[[op:invoke_skill]](/pasture:worker)` with the [[op:delegate_assignment]] operation."
	assert.Empty(t, findRawHarnessLiterals(clean), "clean sample must produce no finding")
}

// TestRenderingSourcesCarryNoRawHarnessLiterals is the production guard: every
// authored string that reaches generated output must name a typed marker, never
// a native call prefix, a harness phrase, or a tracker command.
func TestRenderingSourcesCarryNoRawHarnessLiterals(t *testing.T) {
	t.Parallel()

	sources, err := renderedSources()
	require.NoError(t, err)
	require.NotEmpty(t, sources, "the rendering-source enumeration must not be empty")

	for _, source := range sources {
		findings := findRawHarnessLiterals(source.Text)
		assert.Emptyf(t, findings,
			"rendering-source literal in %s %s: %v — use [[tool:...]], [[op:...]] or [[rec]]",
			source.Corpus, source.Owner, findings,
		)
	}
}

// TestRenderingSourcesAvoidArticleBeforeMarker keeps an indefinite article away
// from a marker: the article would agree with the marker placeholder and
// disagree with at least one target's resolved noun.
func TestRenderingSourcesAvoidArticleBeforeMarker(t *testing.T) {
	t.Parallel()

	defective := "shown to the user in an [[op:request_user_decision]] preview"
	require.NotEmpty(t, articleBeforeMarkerPattern.FindAllString(defective, -1),
		"defective sample must produce at least one finding: %q", defective)

	clean := "shown to the user in the preview the [[op:request_user_decision]] call shows"
	assert.Empty(t, articleBeforeMarkerPattern.FindAllString(clean, -1),
		"clean sample must produce no finding")

	sources, err := renderedSources()
	require.NoError(t, err)
	for _, source := range sources {
		assert.Emptyf(t, articleBeforeMarkerPattern.FindAllString(source.Text, -1),
			"article before marker in %s %s: %q",
			source.Corpus, source.Owner, source.Text,
		)
	}
}

// TestSchemaProseIsSweptByArticleGuard proves schema.go's authored marker prose
// is in the shared sweep, and that an article before one of its markers fails
// the same article guard. schema.go renders into schema.xml, so a defect there
// would ship just like a body defect.
func TestSchemaProseIsSweptByArticleGuard(t *testing.T) {
	t.Parallel()

	sources, err := renderedSources()
	require.NoError(t, err)

	var schemaTexts []string
	for _, source := range sources {
		if source.Corpus == schemaSourceFile {
			schemaTexts = append(schemaTexts, source.Text)
		}
	}
	require.NotEmpty(t, schemaTexts,
		"renderedSources must sweep schema.go's authored marker prose into schema.xml")

	// Use the real schema prose as the clean sample, then inject the article
	// the guard exists to catch. This fails if schema.go ever stops flowing
	// through the sweep or the article guard stops seeing its markers.
	var clean string
	for _, text := range schemaTexts {
		if strings.Contains(text, "[[op:invoke_skill]]") {
			clean = text
			break
		}
	}
	require.NotEmpty(t, clean, "schema.go must author at least one [[op:invoke_skill]] directive")
	assert.Empty(t, articleBeforeMarkerPattern.FindAllString(clean, -1),
		"clean schema prose must produce no article finding")

	defective := strings.Replace(clean, "[[op:invoke_skill]]", "an [[op:invoke_skill]]", 1)
	require.NotEmpty(t, articleBeforeMarkerPattern.FindAllString(defective, -1),
		"an article before a marker in schema.go prose must fail the article guard: %q", defective)
}

// TestEveryMarkerBearingCodegenFileIsRepresented is the completeness guard for
// the sweep itself. Every non-test internal/codegen/*.go file that authors a
// complete body marker must have that authored text represented in
// renderedSources. A new rendering corpus added to a codegen file fails here
// until its marker text is swept, so it cannot be added blind.
func TestEveryMarkerBearingCodegenFileIsRepresented(t *testing.T) {
	t.Parallel()

	root, err := scan.ModuleRoot()
	require.NoError(t, err)
	sources, err := renderedSources()
	require.NoError(t, err)
	require.NotEmpty(t, sources, "the rendering-source enumeration must not be empty")

	entries, err := os.ReadDir(filepath.Join(root, "internal", "codegen"))
	require.NoError(t, err)

	var sawSchema bool
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// body_refs.go is the sweep itself: it holds the marker grammar and
		// diagnostic examples, not authored rendering prose. The other files
		// are the authoring surface the sweep must cover.
		if name == "body_refs.go" {
			continue
		}
		literals, err := authoredMarkerLiterals(filepath.Join(root, "internal", "codegen", name))
		require.NoError(t, err)
		if len(literals) == 0 {
			continue
		}
		if name == schemaSourceFile {
			sawSchema = true
		}
		for _, literal := range literals {
			represented := false
			for _, source := range sources {
				if strings.Contains(source.Text, literal) {
					represented = true
					break
				}
			}
			assert.Truef(t, represented,
				"%s authors marker prose %q that no rendered source carries — "+
					"add its corpus to renderedSources so the literal guards sweep it",
				name, literal)
		}
	}
	assert.True(t, sawSchema,
		"the completeness scan must find schema.go, which authors marker prose for schema.xml")
}

// TestResolveBodyMarkersPerTarget pins one authored marker to each target's
// documented native spelling. Non-native targets resolve to the neutral
// operation or capability name, never to another harness's tool.
func TestResolveBodyMarkersPerTarget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		marker string
		target HarnessName
		want   string
	}{
		{"[[tool:read]]", HarnessClaudeCode, "Read"},
		{"[[tool:read]]", HarnessOpenCode, "read"},
		{"[[tool:read]]", HarnessCodex, "read"},
		{"[[tool:glob]]", HarnessClaudeCode, "Glob"},
		{"[[tool:glob]]", HarnessOpenCode, "glob"},
		{"[[op:invoke_skill]]", HarnessClaudeCode, "Skill"},
		{"[[op:invoke_skill]]", HarnessOpenCode, "skill"},
		{"[[op:invoke_skill]]", HarnessCodex, "invoke_skill"},
		{"[[op:delegate_assignment]]", HarnessClaudeCode, "Agent"},
		{"[[op:delegate_assignment]]", HarnessOpenCode, "task"},
		{"[[op:delegate_assignment]]", HarnessCodex, "delegate_assignment"},
		{"[[op:request_user_decision]]", HarnessClaudeCode, "AskUserQuestion"},
		{"[[op:request_user_decision]]", HarnessOpenCode, "question"},
		{"[[op:request_user_decision]]", HarnessCodex, "request-input"},
		{"[[rec]]", HarnessClaudeCode, trackerEntryPoint},
		{"[[rec]]", HarnessOpenCode, trackerEntryPoint},
		{"[[rec]]", HarnessCodex, trackerEntryPoint},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(fmt.Sprintf("%s/%s", testCase.marker, testCase.target), func(t *testing.T) {
			t.Parallel()
			got, err := ResolveBodyMarkers(testCase.marker, testCase.target)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

// TestResolveBodyMarkersNeutral pins the target-neutral spelling used by
// generated artifacts that name no single harness (schema.xml).
func TestResolveBodyMarkersNeutral(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"[[tool:read]]":                "read",
		"[[op:invoke_skill]]":          "invoke_skill",
		"[[op:delegate_assignment]]":   "delegate_assignment",
		"[[op:request_user_decision]]": "request_user_decision",
		"[[rec]]":                      neutralTrackerEntryPoint,
	}
	for marker, want := range cases {
		marker, want := marker, want
		t.Run(marker, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveBodyMarkersNeutral(marker)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

// TestResolveBodyMarkersRejectsUnknownPayloads proves resolution fails closed:
// an unknown marker cannot reach generated output.
func TestResolveBodyMarkersRejectsUnknownPayloads(t *testing.T) {
	t.Parallel()

	for _, marker := range []string{
		"[[tool:telepathy]]",
		"[[op:telepathy]]",
		"[[nonsense]]",
		"[[tool:read",
	} {
		marker := marker
		t.Run(marker, func(t *testing.T) {
			t.Parallel()
			_, err := ResolveBodyMarkers(marker, HarnessClaudeCode)
			require.Error(t, err)
			_, err = ResolveBodyMarkersNeutral(marker)
			require.Error(t, err)
		})
	}

	// A clean document with no marker is returned unchanged.
	clean := "plain prose"
	got, err := ResolveBodyMarkers(clean, HarnessClaudeCode)
	require.NoError(t, err)
	assert.Equal(t, clean, got)
	neutral, err := ResolveBodyMarkersNeutral(clean)
	require.NoError(t, err)
	assert.Equal(t, clean, neutral)
}

// TestRenderingSourcesMarkersResolveForEveryTarget fails the build when a
// rendering source names an unregistered value, before any target emits a byte.
func TestRenderingSourcesMarkersResolveForEveryTarget(t *testing.T) {
	t.Parallel()

	sources, err := renderedSources()
	require.NoError(t, err)
	for _, source := range sources {
		for _, target := range []HarnessName{HarnessClaudeCode, HarnessOpenCode, HarnessCodex} {
			if _, err := ResolveBodyMarkers(source.Text, target); err != nil {
				t.Errorf("%s %s does not resolve for %s: %v", source.Corpus, source.Owner, target, err)
			}
		}
	}
}

// TestTrackerEntryPointIsNamedOnce proves the tracker command prefix appears in
// exactly one production source: the entry-point constant. A rendering source
// that copies it would defeat tenet 4. A bare product-name mention ("the
// REQUEST pasture task ID") is prose, not a command, and is not a copy.
func TestTrackerEntryPointIsNamedOnce(t *testing.T) {
	t.Parallel()

	// Share the tracker pattern with the literal guard instead of re-declaring it.
	commandPattern := rawHarnessLiteralPatterns[4]

	defective := "Run `pasture task close \"$X\"` when the work lands."
	require.NotEmpty(t, commandPattern.FindAllString(defective, -1),
		"defective sample must produce at least one finding: %q", defective)

	clean := "Run `[[rec]] close \"$X\"` and cite the pasture task ID as prose."
	assert.Empty(t, commandPattern.FindAllString(clean, -1),
		"clean sample must produce no finding")

	sources, err := renderedSources()
	require.NoError(t, err)
	for _, source := range sources {
		assert.Emptyf(t, commandPattern.FindAllString(source.Text, -1),
			"%s %s copies the tracker command prefix; use the [[rec]] marker",
			source.Corpus, source.Owner)
	}

	// The marker must actually resolve to the entry point on every target.
	for _, target := range []HarnessName{HarnessClaudeCode, HarnessOpenCode, HarnessCodex} {
		resolved, err := ResolveBodyMarkers(bodyRecMarker, target)
		require.NoError(t, err)
		assert.Equal(t, trackerEntryPoint, resolved, "[[rec]] must resolve to the one tracker entry point on %s", target)
	}
}
