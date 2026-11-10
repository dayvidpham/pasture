package codegen

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file owns the review-data-model guards for the two-phase review.
//
// The model has two phases, and the old model did not:
//
//  1. Per wave: only an open BLOCKER finding gates the wave. IMPORTANT and
//     MINOR findings may stay open past a wave gate.
//  2. At the end: convergence queries every open pasture:finding across all
//     waves and targets 0/0/0 within maxRounds; on exhaustion the outstanding
//     findings are surfaced to the user at a gate.
//
// A finding closes only when the reviewer who raised it verifies the fix. A
// worker or supervisor cannot close it, and a wave boundary never closes it.
//
// The guards below are written from the rendered text, not from a line list.
// That is deliberate (amendment A-4): schema.xml is generated from the Go spec
// sources, so a guard over generated line numbers would be stale after the
// next regeneration. renderedSources is the one enumeration of every authored
// string that reaches a generated artifact.

// perWaveCleanPattern pairs one retired per-wave clean-round form with a name
// and the exemplar that proves the pattern can still fail. The patterns are
// narrow on purpose: they flag a clean requirement ATTACHED TO A WAVE
// BOUNDARY, never the convergence target 0/0/0 that lives at the end.
type perWaveCleanPattern struct {
	name     string
	re       *regexp.Regexp
	exemplar string
}

var perWaveCleanPatterns = []perWaveCleanPattern{
	{"clean-exit", regexp.MustCompile(`(?i)\bclean exit\b`), "clean exit = 0 BLOCKER + 0 IMPORTANT + 0 MINOR"},
	{"clean-review", regexp.MustCompile(`(?i)\bclean review\b`), "CLEAN REVIEW = 0 BLOCKER + 0 IMPORTANT + 0 MINOR"},
	{"clean-re-review", regexp.MustCompile(`(?i)\bclean re-?review\b`), "Fix-free clean re-review: 0 BLOCKER + 0 IMPORTANT + 0 MINOR"},
	{"clean-round", regexp.MustCompile(`(?i)\bclean (?:0/0/0 )?round\b`), "until a fix-free clean round confirms 0/0/0"},
	{"fix-free", regexp.MustCompile(`(?i)\bfix-free\b`), "on a fix-free clean round"},
	{"until-clean", regexp.MustCompile(`(?i)\buntil clean\b`), "repeat until clean (0/0/0)"},
	{"slice-is-clean", regexp.MustCompile(`(?i)\bslice is clean\b`), "repeat until the slice is clean (0/0/0)"},
	{"slice-passes", regexp.MustCompile(`(?i)\bslice passes\b`), "YES (0/0/0) → slice passes, proceed"},
	{
		"eligible-to-close",
		regexp.MustCompile(`(?i)eligible to close[^\n]{0,100}(?:0/0/0|0 blocker|clean|severit)`),
		"Eligible to close only after review with 0 BLOCKER + 0 IMPORTANT + 0 MINOR findings",
	},
	{
		"all-severities-zero",
		regexp.MustCompile(`(?i)\ball (?:three )?(?:review )?severit\w*[^\n]{0,70}(?:reach|at|to) 0\b`),
		"ALL severity groups must reach 0 before wave close",
	},
	{
		"all-groups-zero",
		regexp.MustCompile(`(?i)\ball (?:review )?groups?[^\n]{0,70}(?:reach|at|to) 0\b`),
		"ALL groups must reach 0 before wave close",
	},
	{
		"all-findings-zero",
		regexp.MustCompile(`(?i)\ball (?:review )?findings?[^\n]{0,70}(?:reach|at|to) 0\b`),
		"all review findings must reach 0",
	},
	{
		"reach-zero-before-wave",
		regexp.MustCompile(`(?i)reach 0[^\n]{0,30}before (?:the )?(?:review )?wave`),
		"Must reach 0 before review wave closes",
	},
	{
		"every-severity-before-wave",
		regexp.MustCompile(`(?i)\bevery severit\w*[^\n]{0,60}before (?:the )?(?:review )?wave`),
		"Review findings of every severity must be resolved before the review wave closes",
	},
	{
		"wave-never-closes-finding",
		regexp.MustCompile(`(?i)\bwave never closes[^\n]{0,80}(?:finding|outstanding)`),
		"A wave never closes on a fix-applying round, and never with any finding silently outstanding.",
	},
	{
		"zero-before-wave",
		regexp.MustCompile(`(?i)\b0/0/0\b[^\n]{0,50}(?:before (?:the )?(?:review )?wave|wave close|wave closes|per-wave|at the wave|wave gate)`),
		"0/0/0 at the wave gate",
	},
	{
		"wave-before-zero",
		regexp.MustCompile(`(?i)(?:wave close|before (?:the )?(?:review )?wave|per-wave|at the wave|wave gate)[^\n]{0,50}\b0/0/0\b`),
		"per-wave 0/0/0",
	},
}

// findPerWaveCleanRound is the pure core of TestNoPerWaveCleanRound. It returns
// one finding per retired per-wave clean assertion in content. It reads line by
// line because a clean requirement and the wave boundary it attaches to are
// written on the same line throughout the authored sources.
func findPerWaveCleanRound(content string) []string {
	var findings []string
	for _, line := range strings.Split(content, "\n") {
		for _, pattern := range perWaveCleanPatterns {
			loc := pattern.re.FindStringIndex(line)
			if loc == nil {
				continue
			}
			findings = append(findings, pattern.name+": "+line[loc[0]:loc[1]])
		}
	}
	return findings
}

// TestNoPerWaveCleanRound proves no rendered text requires a clean 0/0/0 round
// at a wave boundary. It carries two pinned samples: the defective sample must
// produce at least one finding, and the clean sample (the two-phase model) must
// produce none. It then sweeps every rendering source, including role bodies,
// supervisor checklists and workflow descriptions (tenets 8 and 9, amendment
// A-4.1).
func TestNoPerWaveCleanRound(t *testing.T) {
	t.Parallel()

	// Non-vacuity: every pattern must still match its own documented exemplar.
	for _, pattern := range perWaveCleanPatterns {
		require.Truef(t, pattern.re.MatchString(pattern.exemplar),
			"pattern %q no longer matches its own exemplar %q — the guard would pass vacuously; fix the pattern before relying on the sweep",
			pattern.name, pattern.exemplar)
	}

	defective := strings.Join([]string{
		"The clean exit = 0 BLOCKER + 0 IMPORTANT + 0 MINOR at the wave boundary.",
		"ALL severity groups must reach 0 before wave close.",
		"Eligible to close only after review with 0 BLOCKER + 0 IMPORTANT + 0 MINOR findings.",
		"All reviewers report 0 BLOCKER + 0 IMPORTANT + 0 MINOR on a fix-free clean round.",
		"CLEAN? YES (0/0/0) → slice passes, proceed",
		"repeat until clean (0/0/0)",
	}, "\n")
	require.NotEmpty(t, findPerWaveCleanRound(defective),
		"the defective sample must produce at least one finding")

	clean := "During a wave, only an open BLOCKER finding gates progress; IMPORTANT and MINOR findings stay open. " +
		"After the last wave, convergence queries every open pasture:finding across all waves, targets 0/0/0 within maxRounds, " +
		"and surfaces any remaining finding to the user at a gate. A finding closes only when the reviewer who raised it verifies the fix."
	assert.Empty(t, findPerWaveCleanRound(clean),
		"the clean two-phase sample must produce no finding")

	sources, err := renderedSources()
	require.NoError(t, err)
	require.NotEmpty(t, sources, "the rendering-source enumeration must not be empty")

	for _, source := range sources {
		assert.Emptyf(t, findPerWaveCleanRound(source.Text),
			"%s %s still requires a clean round at a wave boundary: %v — the review model is two-phase: only an open BLOCKER gates a wave, and convergence over open pasture:finding tasks happens at the end within maxRounds",
			source.Corpus, source.Owner)
	}
}

// TestFindingLifecycle proves the finding label is registered without severity,
// that the review commands declare they create it, and that the lifecycle
// constraint states the close rule in the generated reviewer and supervisor
// skills.
func TestFindingLifecycle(t *testing.T) {
	t.Parallel()

	spec, ok := LabelSpecs["L-finding"]
	require.True(t, ok, "LabelSpecs must register L-finding")
	assert.Equal(t, "pasture:finding", spec.Value)
	assert.True(t, spec.Special, "pasture:finding is a special label, not a phase label")
	assert.NotEmpty(t, spec.Description, "the registered finding label needs a description")
	assert.Empty(t, spec.SeverityRef,
		"the finding label must not encode severity; severity lives on the parent severity group")

	for _, cmd := range []string{"cmd-rev-code", "cmd-impl-review"} {
		assert.Containsf(t, CommandSpecs[cmd].CreatesLabels, "L-finding",
			"%s creates findings and must declare the pasture:finding label", cmd)
	}

	constraint, ok := ConstraintSpecs["C-finding-lifecycle"]
	require.True(t, ok, "ConstraintSpecs must carry C-finding-lifecycle")
	then := strings.ToLower(constraint.Then)
	assert.Contains(t, then, "reviewer who raised it",
		"the lifecycle constraint must name the raiser as the closing authority")
	assert.Contains(t, then, "verifies the fix",
		"the lifecycle constraint must require the raiser to verify the fix")
	shouldNot := strings.ToLower(constraint.ShouldNot)
	assert.Contains(t, shouldNot, "worker or supervisor",
		"the lifecycle constraint must forbid a worker or supervisor closing a finding")
	assert.Contains(t, shouldNot, "wave boundary",
		"the lifecycle constraint must forbid a wave boundary closing a finding")

	assert.True(t, roleConstraints[protocol.RoleReviewer]["C-finding-lifecycle"],
		"the reviewer role must carry the finding lifecycle constraint")
	assert.True(t, roleConstraints[protocol.RoleSupervisor]["C-finding-lifecycle"],
		"the supervisor role must carry the finding lifecycle constraint")

	root, err := scan.ModuleRoot()
	require.NoError(t, err)
	for _, rel := range []string{
		filepath.Join("skills", "reviewer", "SKILL.md"),
		filepath.Join("skills", "supervisor", "SKILL.md"),
	} {
		body, err := os.ReadFile(filepath.Join(root, rel))
		require.NoErrorf(t, err, "reading generated %q failed; run make generate", rel)
		content := string(body)
		assert.Containsf(t, content, "C-finding-lifecycle",
			"generated %q must state the finding lifecycle constraint", rel)
		assert.Containsf(t, content, "close it only when the reviewer who raised it verifies the fix",
			"generated %q must state the raiser-verifies close rule in one sentence", rel)
	}
}

// TestWaveTitleConvention proves the title-convention registry names waves,
// not unqualified slices: SLICE-{W}-{N} for a slice and
// WAVE-{W}-REVIEW-{axis}-{round} for a review round. No rendered text keeps
// the retired unqualified forms.
func TestWaveTitleConvention(t *testing.T) {
	t.Parallel()

	var sliceTitle, reviewTitle bool
	for _, convention := range TitleConventions {
		switch convention.Pattern {
		case "SLICE-{W}-{N}: {description}":
			sliceTitle = true
		case "WAVE-{W}-REVIEW-{axis}-{round}: {description}":
			reviewTitle = true
		case "SLICE-{N}: {description}", "SLICE-{N}-REVIEW-{axis}-{round}: {description}":
			t.Errorf("title-convention registry still carries the unqualified slice form %q", convention.Pattern)
		}
	}
	require.True(t, sliceTitle, "the registry must declare SLICE-{W}-{N}: {description}")
	require.True(t, reviewTitle, "the registry must declare WAVE-{W}-REVIEW-{axis}-{round}: {description}")

	sources, err := renderedSources()
	require.NoError(t, err)
	var sawSliceWave, sawReviewWave bool
	for _, source := range sources {
		// FOLLOWUP_SLICE-{N} is a distinct, non-wave follow-up title; strip it
		// before checking for the retired unqualified slice form.
		text := strings.ReplaceAll(source.Text, "FOLLOWUP_SLICE-{N}:", "")
		assert.NotContainsf(t, text, "SLICE-{N}:",
			"%s %s still names an unqualified slice title; use SLICE-{W}-{N}", source.Corpus, source.Owner)
		assert.NotContainsf(t, source.Text, "SLICE-{N}-REVIEW",
			"%s %s still names an unqualified slice review; use WAVE-{W}-REVIEW-{axis}-{round}", source.Corpus, source.Owner)
		if strings.Contains(source.Text, "SLICE-{W}-{N}") {
			sawSliceWave = true
		}
		if strings.Contains(source.Text, "WAVE-{W}-REVIEW") {
			sawReviewWave = true
		}
	}
	assert.True(t, sawSliceWave, "some rendered text must name the wave-scoped slice title")
	assert.True(t, sawReviewWave, "some rendered text must name the wave-scoped review title")
}

// TestReviewPhaseGateAttribute proves the gate declaration (the phase's
// severity-tree element) carries the maxRounds count parameter and that the
// convergence rule reads it rather than restating a number.
func TestReviewPhaseGateAttribute(t *testing.T) {
	t.Parallel()

	var schema bytes.Buffer
	require.NoError(t, GenerateSchema(&schema))
	rendered := schema.String()

	start := strings.Index(rendered, `<phase id="p10"`)
	require.NotEqual(t, -1, start, "the generated schema must contain the p10 phase")
	end := strings.Index(rendered[start:], `<phase id="p11"`)
	require.NotEqual(t, -1, end, "the generated schema must contain the p11 phase after p10")
	reviewPhase := rendered[start : start+end]

	assert.Contains(t, reviewPhase, `maxRounds=`,
		"the p10 gate declaration must carry the maxRounds attribute")
	assert.Contains(t, reviewPhase, `<severity-tree`,
		"the maxRounds attribute belongs on the severity-tree gate declaration")

	convergence, ok := ConstraintSpecs["C-clean-review-exit"]
	require.True(t, ok, "the convergence rule lives in C-clean-review-exit")
	assert.Contains(t, convergence.Then, "maxRounds",
		"the convergence rule must read maxRounds, not restate a count")
	assert.Contains(t, WorkflowSpecs["ride-the-wave"].Description, "maxRounds",
		"the ride-the-wave workflow description must read maxRounds")
}
