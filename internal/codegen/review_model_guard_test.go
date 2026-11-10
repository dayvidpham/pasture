package codegen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
		regexp.MustCompile(`(?i)reach 0[^\n]{0,30}before (?:the )?(?:next |following |subsequent )?(?:review )?wave`),
		"Must reach 0 before review wave closes",
	},
	{
		"every-severity-before-wave",
		regexp.MustCompile(`(?i)\bevery severit\w*[^\n]{0,60}before (?:the )?(?:next |following |subsequent )?(?:review )?wave`),
		"Review findings of every severity must be resolved before the review wave closes",
	},
	{
		"wave-never-closes-finding",
		regexp.MustCompile(`(?i)\bwave never closes[^\n]{0,80}(?:finding|outstanding)`),
		"A wave never closes on a fix-applying round, and never with any finding silently outstanding.",
	},
	{
		"zero-before-wave",
		regexp.MustCompile(`(?i)\b0/0/0\b[^\n]{0,50}(?:before (?:the )?(?:next |following |subsequent )?(?:review )?wave|wave close|wave closes|per-wave|at the wave|wave gate)`),
		"0/0/0 at the wave gate",
	},
	{
		"wave-before-zero",
		regexp.MustCompile(`(?i)(?:wave close|before (?:the )?(?:next |following |subsequent )?(?:review )?wave|per-wave|at the wave|wave gate)[^\n]{0,50}\b0/0/0\b`),
		"per-wave 0/0/0",
	},
	{
		"close-wave-all-zero",
		regexp.MustCompile(`(?i)\bclose the wave[^\n]{0,80}\ball\b[^\n]{0,25}\b0\b`),
		"close the wave only when BLOCKER, IMPORTANT and MINOR are all 0",
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

// reviewModelShippedSources returns the shipped files the review-model guards
// sweep in addition to renderedSources. renderedSources covers authored prose
// that reaches generated output; G2ScopedFiles adds the hand-authored
// skills/protocol/*.md corpus — shipped verbatim into every target tree but
// never passed through the generator — plus the generated trees themselves.
// Reusing it keeps one corpus list, not a second one (tenet 11: a rewritten
// rule must not survive in the shipped docs).
func reviewModelShippedSources(t *testing.T) []renderingSource {
	t.Helper()

	root, err := scan.ModuleRoot()
	require.NoError(t, err)

	files := G2ScopedFiles(t, root)
	sources := make([]renderingSource, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file)
		require.NoErrorf(t, err, "reading %q failed; run make generate", file)
		rel, err := filepath.Rel(root, file)
		require.NoErrorf(t, err, "relativizing %q failed", file)
		sources = append(sources, renderingSource{Corpus: "shipped", Owner: rel, Text: string(data)})
	}
	return sources
}

// reviewModelGuardSources returns the full corpus every review-model guard
// sweeps: authored rendering sources plus the shipped files from
// reviewModelShippedSources.
func reviewModelGuardSources(t *testing.T) []renderingSource {
	t.Helper()

	sources, err := renderedSources()
	require.NoError(t, err)
	require.NotEmpty(t, sources, "the rendering-source enumeration must not be empty")
	return append(sources, reviewModelShippedSources(t)...)
}

// TestNoPerWaveCleanRound proves no rendered or shipped text requires a clean
// 0/0/0 round at a wave boundary. It carries two pinned samples: the defective
// sample must produce at least one finding, and the clean sample (the two-phase
// model) must produce none. It then sweeps every rendering source and every
// shipped file — including the hand-authored skills/protocol/*.md corpus
// (tenets 8 and 9, amendment A-4.1).
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
		"every wave must close at 0/0/0 before the next wave starts",
		"close the wave only when BLOCKER, IMPORTANT and MINOR are all 0",
	}, "\n")
	require.NotEmpty(t, findPerWaveCleanRound(defective),
		"the defective sample must produce at least one finding")

	clean := "During a wave, only an open BLOCKER finding gates progress; IMPORTANT and MINOR findings stay open. " +
		"After the last wave, convergence queries every open pasture:finding across all waves, targets 0/0/0 within maxRounds, " +
		"and surfaces any remaining finding to the user at a gate. A finding closes only when the reviewer who raised it verifies the fix."
	assert.Empty(t, findPerWaveCleanRound(clean),
		"the clean two-phase sample must produce no finding")

	sources := reviewModelGuardSources(t)

	for _, source := range sources {
		findings := findPerWaveCleanRound(source.Text)
		assert.Emptyf(t, findings,
			"%s %s still requires a clean round at a wave boundary: %v — the review model is two-phase: only an open BLOCKER gates a wave, and convergence over open pasture:finding tasks happens at the end within maxRounds",
			source.Corpus, source.Owner, findings)
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
		// Pin the constraint marker, not a sentence: the close rule has one home
		// (C-finding-lifecycle) and the generated skills must reference it. A
		// sentence pin would re-create the duplicated wording tenet 7 forbids.
		assert.Containsf(t, string(body), "C-finding-lifecycle",
			"generated %q must reference the finding lifecycle constraint", rel)
	}
}

// concreteSliceTitlePattern catches a concrete unqualified slice title: the
// colon is what makes it a title. SLICE-{W}-{N}: is wave-qualified and does not
// match because the character after the first number is a hyphen.
var concreteSliceTitlePattern = regexp.MustCompile(`\bSLICE-\d+:`)

// concreteSliceReviewPattern catches a concrete unqualified slice review title,
// e.g. SLICE-1-REVIEW-A-1. The wave-scoped form is WAVE-{W}-REVIEW-...
var concreteSliceReviewPattern = regexp.MustCompile(`\bSLICE-\d+-REVIEW\b`)

// waveReviewTitlePattern matches one concrete wave review title and captures
// its wave number, axis, and round. The review unit is (wave, axis, round).
var waveReviewTitlePattern = regexp.MustCompile(`\bWAVE-(\d+)-REVIEW-([A-C])-(\d+)\b`)

// followupSliceTokenPattern matches the distinct, non-wave follow-up slice form
// so it is stripped before the unqualified-slice checks run.
var followupSliceTokenPattern = regexp.MustCompile(`FOLLOWUP_SLICE-(?:\{N\}|\d+)`)

// findWaveTitleDefects is the pure core of the wave-title guard. It flags:
//
//   - the retired unqualified slice title template (SLICE-{N}:) and its
//     concrete form (SLICE-<n>:), and the unqualified slice review form;
//   - a line that maps ONE review unit (axis, round) onto several wave
//     numbers. One review covers one whole wave, so it yields one title for
//     that (axis, round); the retired defect reviewed three slices of one wave
//     and produced WAVE-1/2/3-REVIEW-A-1 for the same axis and round. A line
//     lists the wave's slices instead.
func findWaveTitleDefects(content string) []string {
	var defects []string
	for _, rawLine := range strings.Split(content, "\n") {
		line := followupSliceTokenPattern.ReplaceAllString(rawLine, "FOLLOWUP_SLICE")

		if strings.Contains(line, "SLICE-{N}:") {
			defects = append(defects, "retired unqualified slice title template: SLICE-{N}:")
		}
		if strings.Contains(line, "SLICE-{N}-REVIEW") {
			defects = append(defects, "retired unqualified slice review template: SLICE-{N}-REVIEW")
		}
		if loc := concreteSliceTitlePattern.FindString(line); loc != "" {
			defects = append(defects, "concrete unqualified slice title: "+loc)
		}
		if loc := concreteSliceReviewPattern.FindString(line); loc != "" {
			defects = append(defects, "concrete unqualified slice review: "+loc)
		}

		// A review is identified by (axis, round). Its wave number must be
		// unique on a line: several wave numbers for one axis and round means
		// one review was split by slice into several wave titles.
		type axisRound struct{ axis, round string }
		wavesByReview := map[axisRound]map[string]bool{}
		for _, m := range waveReviewTitlePattern.FindAllStringSubmatch(line, -1) {
			key := axisRound{axis: m[2], round: m[3]}
			if wavesByReview[key] == nil {
				wavesByReview[key] = map[string]bool{}
			}
			wavesByReview[key][m[1]] = true
		}
		for key, waves := range wavesByReview {
			if len(waves) < 2 {
				continue
			}
			numbers := make([]string, 0, len(waves))
			for wave := range waves {
				numbers = append(numbers, wave)
			}
			sort.Strings(numbers)
			defects = append(defects, fmt.Sprintf(
				"one review (axis %s, round %s) maps to %d wave titles (WAVE-%s); one review covers one wave, so list its slices instead",
				key.axis, key.round, len(numbers), strings.Join(numbers, ", WAVE-")))
		}
	}
	return defects
}

// TestWaveTitleConvention proves the title-convention registry names waves,
// not unqualified slices: SLICE-{W}-{N} for a slice and
// WAVE-{W}-REVIEW-{axis}-{round} for a review round. Two samples prove the
// guard can fail (a concrete SLICE-<n>: title, and one review mapped onto three
// wave titles) and can pass (one review per wave listing its slices). No
// rendered text keeps the retired forms.
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

	defective := strings.Join([]string{
		"SLICE-1: Implement the command",
		"Creates: SLICE-1-REVIEW-A-1 for the first slice",
		"Reviews SLICE-1, SLICE-2, SLICE-3 -> Creates: WAVE-1-REVIEW-A-1, WAVE-2-REVIEW-A-1, WAVE-3-REVIEW-A-1",
	}, "\n")
	require.NotEmpty(t, findWaveTitleDefects(defective),
		"the defective sample must produce at least one finding: a concrete SLICE-<n>: title and a one-review-to-many-wave-title mapping")

	clean := strings.Join([]string{
		"SLICE-{W}-{N}: <slice description>",
		"Wave 1 contains SLICE-1-1, SLICE-1-2, and SLICE-1-3. Reviewer A reviews the whole wave and creates WAVE-1-REVIEW-A-1.",
		"FOLLOWUP_SLICE-{N}: <description>",
	}, "\n")
	assert.Empty(t, findWaveTitleDefects(clean),
		"the clean sample must produce no finding: one review per wave lists its slices and yields one title")

	sources := reviewModelGuardSources(t)
	var sawSliceWave, sawReviewWave bool
	for _, source := range sources {
		for _, defect := range findWaveTitleDefects(source.Text) {
			assert.Failf(t, "retired wave-title form", "%s %s: %s", source.Corpus, source.Owner, defect)
		}
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

	// The attribute must sit INSIDE the <severity-tree ...> open tag. Two
	// independent Contains checks would both pass when the attribute is deleted
	// and a rule merely mentions it, which is not the property under test.
	gateStart := strings.Index(reviewPhase, "<severity-tree")
	require.NotEqual(t, -1, gateStart,
		"the p10 gate declaration must carry a <severity-tree> element")
	gateOpenEnd := strings.Index(reviewPhase[gateStart:], ">")
	require.NotEqual(t, -1, gateOpenEnd, "the <severity-tree> element must have a closing '>'")
	gateOpenTag := reviewPhase[gateStart : gateStart+gateOpenEnd+1]
	assert.Contains(t, gateOpenTag, `maxRounds="3"`,
		"the maxRounds attribute must be on the <severity-tree> open tag, not merely mentioned in the phase body; got %q", gateOpenTag)

	convergence, ok := ConstraintSpecs["C-clean-review-exit"]
	require.True(t, ok, "the convergence rule lives in C-clean-review-exit")
	assert.Contains(t, convergence.Then, "maxRounds",
		"the convergence rule must read maxRounds, not restate a count")
	assert.Contains(t, WorkflowSpecs["ride-the-wave"].Description, "maxRounds",
		"the ride-the-wave workflow description must read maxRounds")
}

// TestFollowupEpicIsUATDeferred proves the schema no longer creates a
// FOLLOWUP epic from review completion. Only user-DEFER'd UAT items feed it.
func TestFollowupEpicIsUATDeferred(t *testing.T) {
	t.Parallel()

	var schema bytes.Buffer
	require.NoError(t, GenerateSchema(&schema))
	rendered := schema.String()

	start := strings.Index(rendered, `<phase id="p10"`)
	require.NotEqual(t, -1, start, "the generated schema must contain the p10 phase")
	end := strings.Index(rendered[start:], `<phase id="p11"`)
	require.NotEqual(t, -1, end, "the generated schema must contain the p11 phase after p10")
	p10 := rendered[start : start+end]

	assert.NotContains(t, p10, "<followup-epic",
		"the p10 review phase must not declare a follow-up epic; only user-DEFER'd UAT items create one")
	assert.NotContains(t, strings.ToLower(rendered), "review-completion",
		"no schema directive may trigger a follow-up epic from review completion")

	lifecycleStart := strings.Index(rendered, "<followup-lifecycle>")
	require.NotEqual(t, -1, lifecycleStart, "the schema must describe the follow-up lifecycle")
	lifecycleEnd := strings.Index(rendered[lifecycleStart:], "</followup-lifecycle>")
	require.NotEqual(t, -1, lifecycleEnd, "the follow-up lifecycle element must close")
	lifecycle := rendered[lifecycleStart : lifecycleStart+lifecycleEnd]

	assert.Contains(t, lifecycle, "UAT",
		"the follow-up lifecycle trigger must name UAT as its source")
	assert.Contains(t, lifecycle, "DEFER",
		"the follow-up lifecycle trigger must name user-DEFER'd UAT items as its source")
	assert.NotContains(t, lifecycle, "Code review completion",
		"the follow-up lifecycle must not be triggered by code review completion")
}

// followupHandoffStartPattern catches the retired claim that the h5 reviewer
// handoff starts the follow-up lifecycle. The lifecycle starts only from
// user-DEFER'd UAT items (C-followup-timing); h5 closes the review wave.
var followupHandoffStartPattern = regexp.MustCompile(`(?i)h5[^\n]{0,40}starts the follow-up lifecycle`)

// findFollowupStartClaims returns one finding per retired h5-starts claim.
func findFollowupStartClaims(content string) []string {
	var findings []string
	for _, line := range strings.Split(content, "\n") {
		if loc := followupHandoffStartPattern.FindString(line); loc != "" {
			findings = append(findings, loc)
		}
	}
	return findings
}

// TestFollowupLifecycleStartsAtUAT proves no rendered or shipped text claims
// the h5 reviewer handoff starts the follow-up lifecycle. Two samples prove the
// guard fails on the retired claim and passes on the UAT-deferred wording.
func TestFollowupLifecycleStartsAtUAT(t *testing.T) {
	t.Parallel()

	defective := strings.Join([]string{
		"| 1 | h5 | Reviewer → Followup: **Starts** the follow-up lifecycle |",
		"The h5 handoff starts the follow-up lifecycle.",
	}, "\n")
	require.NotEmpty(t, findFollowupStartClaims(defective),
		"the defective sample must produce at least one finding")

	clean := strings.Join([]string{
		"| 1 | *(none)* | Supervisor creates the FOLLOWUP epic from user-DEFER'd UAT items (same actor) |",
		"The follow-up lifecycle starts ONLY from user-DEFER'd UAT items (`C-followup-timing`).",
		"The Reviewer → Supervisor h5 handoff closes the review wave; it never starts the follow-up lifecycle.",
	}, "\n")
	assert.Empty(t, findFollowupStartClaims(clean),
		"the clean sample must produce no finding")

	for _, source := range reviewModelGuardSources(t) {
		claims := findFollowupStartClaims(source.Text)
		assert.Emptyf(t, claims,
			"%s %s still claims the h5 handoff starts the follow-up lifecycle: %v — the lifecycle starts only from user-DEFER'd UAT items (C-followup-timing); h5 closes the review wave",
			source.Corpus, source.Owner, claims)
	}
}

// TestSpecialLabelIDsMatchRegistry ties the schema's ordered special-label list
// to the label registry, so the two cannot drift: every LabelSpecs entry marked
// Special must be emitted, and the schema must carry its value.
func TestSpecialLabelIDsMatchRegistry(t *testing.T) {
	t.Parallel()

	registered := map[string]bool{}
	for id, spec := range LabelSpecs {
		if spec.Special {
			registered[id] = true
		}
	}
	require.NotEmpty(t, registered, "the label registry must mark at least one special label")

	listed := map[string]bool{}
	for _, id := range specialLabelIDs {
		assert.Falsef(t, listed[id], "specialLabelIDs lists %q twice", id)
		listed[id] = true
		spec, ok := LabelSpecs[id]
		require.Truef(t, ok, "specialLabelIDs names %q, which LabelSpecs does not register", id)
		assert.Truef(t, spec.Special, "%q is in specialLabelIDs but not marked Special in LabelSpecs", id)
	}
	assert.ElementsMatch(t, keysOf(registered), keysOf(listed),
		"specialLabelIDs and the Special label registry drifted; register the label or add it to specialLabelIDs")

	var schema bytes.Buffer
	require.NoError(t, GenerateSchema(&schema))
	rendered := schema.String()
	for id := range registered {
		assert.Containsf(t, rendered, `id="`+id+`"`,
			"special label %q is registered but absent from schema.xml", id)
		assert.Containsf(t, rendered, `value="`+LabelSpecs[id].Value+`"`,
			"special label %q renders without its registry value %q", id, LabelSpecs[id].Value)
	}
}

// TestSchemaDirectiveProseIsSwept proves schema.go's non-marker directive prose
// is in the shared rendering-source sweep. Before this change renderedSources
// read only marker-bearing literals, so a directive such as a follow-up trigger
// was invisible to every literal guard.
func TestSchemaDirectiveProseIsSwept(t *testing.T) {
	t.Parallel()

	const directive = "UAT (Phase 5 or Phase 11) produces user-DEFER'd items"

	sources, err := renderedSources()
	require.NoError(t, err)
	var found bool
	for _, source := range sources {
		if source.Corpus == schemaSourceFile && source.Text == directive {
			found = true
			break
		}
	}
	require.Truef(t, found,
		"schema directive prose %q is not in renderedSources; a directive without a body marker must still be swept", directive)

	// The same sweep's guard catches the retired per-wave rule when it is
	// written as directive prose, not only as registry prose.
	defective := "When code review completes, every wave must close at 0/0/0 before the next wave starts"
	require.NotEmpty(t, findPerWaveCleanRound(defective),
		"the directive-prose corpus must feed the per-wave clean-round guard")
}

func keysOf(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	return keys
}
