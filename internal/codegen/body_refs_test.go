package codegen

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rawHarnessLiteralPatterns are the native call prefixes a SkillBody must not
// spell. Each one is authored as a typed marker instead, so the name is
// resolved once per target.
//
// TeamCreate is deliberately absent: it is a retired mechanism with no honest
// IR kind (the closed orchestration set models delegation, not a team object).
// Its removal is the contradiction sweep's work; classifying it as a mapped
// literal here would require inventing a kind.
var rawHarnessLiteralPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bSkill\(`),
	regexp.MustCompile(`\bTask\(`),
	regexp.MustCompile(`\bSendMessage\(`),
	regexp.MustCompile(`\bAskUserQuestion\b`),
	regexp.MustCompile(`\bpasture task\s+(create|label|dep|comment|show|list|blocked|ready|close|update|comments|timeline|agents|assignment)\b`),
}

// findRawHarnessLiterals is the pure core of the body-literal guard. It
// returns one finding per raw harness literal in content.
func findRawHarnessLiterals(content string) []string {
	var findings []string
	for _, pattern := range rawHarnessLiteralPatterns {
		for _, match := range pattern.FindAllString(content, -1) {
			findings = append(findings, match)
		}
	}
	return findings
}

// TestBodyLiteralGuardHasTwoSamples proves the guard can fail and can pass.
// One defective sample must produce at least one finding; one clean sample
// must produce none. Tenets 8 and 9.
func TestBodyLiteralGuardHasTwoSamples(t *testing.T) {
	t.Parallel()

	defective := "Run `pasture task show \"${ID}\"` and call `Skill(/pasture:worker)`."
	findings := findRawHarnessLiterals(defective)
	require.NotEmpty(t, findings, "defective sample must produce at least one finding: %q", defective)

	clean := "Run `[[rec]] show \"${ID}\"` and call `[[op:invoke_skill]](/pasture:worker)`."
	assert.Empty(t, findRawHarnessLiterals(clean), "clean sample must produce no finding")
}

// TestBodySpecsCarryNoRawHarnessLiterals is the production guard: every
// authored body string must name a typed marker, never a native call prefix or
// a tracker command. The build fails when a literal is re-added.
func TestBodySpecsCarryNoRawHarnessLiterals(t *testing.T) {
	t.Parallel()

	for skill, body := range SkillBodySpecs {
		for owner, content := range bodyStrings(body) {
			findings := findRawHarnessLiterals(content)
			assert.Emptyf(t, findings,
				"body literal in SkillBodySpecs[%q] %s: %v — use [[tool:...]], [[op:...]] or [[rec]]",
				skill, owner, findings,
			)
		}
	}
}

// TestResolveBodyMarkersPerTarget pins the one authored body to each target's
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
		{"[[rec]]", HarnessClaudeCode, "pasture task"},
		{"[[rec]]", HarnessOpenCode, "pasture task"},
		{"[[rec]]", HarnessCodex, "pasture task"},
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
		})
	}

	// A clean document with no marker is returned unchanged.
	clean := "plain prose"
	got, err := ResolveBodyMarkers(clean, HarnessClaudeCode)
	require.NoError(t, err)
	assert.Equal(t, clean, got)
}

// TestBodySpecsMarkersResolveForEveryTarget fails the build when a body marker
// names an unregistered value or the tracker marker is malformed, before any
// target emits a byte.
func TestBodySpecsMarkersResolveForEveryTarget(t *testing.T) {
	t.Parallel()

	for skill, body := range SkillBodySpecs {
		for owner, content := range bodyStrings(body) {
			for _, target := range []HarnessName{HarnessClaudeCode, HarnessOpenCode, HarnessCodex} {
				if _, err := ResolveBodyMarkers(content, target); err != nil {
					t.Errorf("SkillBodySpecs[%q] %s does not resolve for %s: %v", skill, owner, target, err)
				}
			}
		}
	}
}

// bodyStrings returns every authored string of a SkillBody keyed by a
// human-readable owner path, so a failure names the exact field.
func bodyStrings(body SkillBody) map[string]string {
	out := map[string]string{
		"Preamble": body.Preamble,
	}
	var walk func(prefix string, sections []ProseSection)
	walk = func(prefix string, sections []ProseSection) {
		for i, section := range sections {
			owner := fmt.Sprintf("%s.Sections[%d](%s)", prefix, i, section.Id)
			out[owner+".Title"] = section.Title
			out[owner+".Content"] = section.Content
			walk(owner, section.Subsections)
		}
	}
	walk("Body", body.Sections)
	for i, behavior := range body.Behaviors {
		owner := fmt.Sprintf("Behaviors[%d](%s)", i, behavior.Id)
		out[owner+".Given"] = behavior.Given
		out[owner+".When"] = behavior.When
		out[owner+".Then"] = behavior.Then
		out[owner+".ShouldNot"] = behavior.ShouldNot
	}
	for i, recipe := range body.Recipes {
		owner := fmt.Sprintf("Recipes[%d](%s)", i, recipe.Id)
		out[owner+".Title"] = recipe.Title
		out[owner+".Description"] = recipe.Description
		out[owner+".Code"] = recipe.Code
	}
	return out
}

// TestTrackerEntryPointIsNamedOnce proves the tracker command prefix appears in
// exactly one production source: the entry-point constant. A body that copies
// it would defeat tenet 4. A bare product-name mention ("the REQUEST pasture
// task ID") is prose, not a command, and is not a copy of the entry point.
func TestTrackerEntryPointIsNamedOnce(t *testing.T) {
	t.Parallel()

	commandPrefix := regexp.MustCompile(`pasture task\s+(create|label|dep|comment|show|list|blocked|ready|close|update|comments|timeline|agents|assignment)\b`)
	for skill, body := range SkillBodySpecs {
		for owner, content := range bodyStrings(body) {
			assert.Emptyf(t, commandPrefix.FindAllString(content, -1),
				"SkillBodySpecs[%q] %s copies the tracker command prefix; use the [[rec]] marker", skill, owner)
		}
	}
	assert.True(t, strings.HasPrefix(trackerEntryPoint, "pasture"),
		"the tracker entry point must be the single named command prefix")
}
