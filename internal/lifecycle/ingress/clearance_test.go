package ingress_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearanceHeadings are the sections every harness CLEARANCE.md carries, in
// this order. A missing or reordered section is a step the clearance skipped.
var clearanceHeadings = []string{
	"# Clearance record",
	"## Harness and pinned version",
	"## Capture",
	"## Inventory",
	"## Rules applied, in order",
	"## Secret scan",
	"## Refused classes",
	"## Fixtures",
	"## User acceptance",
	"## Pull request",
}

// clearancePins are the load-bearing phrases of the template body, each
// proved by mutation in the slice report.
var clearancePins = []string{
	"outside the repository",
	"`home-path-v1`, then `free-text-v1`",
	"Structure, keys, types and nulls are unchanged",
	"Nothing in this directory reaches a remote before this section is filled",
	"Appended by the integrator in the landing commit",
}

// whitespace collapses every run of whitespace to one space, so a pinned
// phrase matches wherever the prose is wrapped.
var whitespace = regexp.MustCompile(`\s+`)

func singleSpaced(text string) string { return whitespace.ReplaceAllString(text, " ") }

// TestEveryHarnessFixtureDirectoryCarriesTheClearanceTemplate derives the
// fixture directories from disk and requires each to hold a CLEARANCE.md with
// the sections above in order and the pinned phrases present.
func TestEveryHarnessFixtureDirectoryCarriesTheClearanceTemplate(t *testing.T) {
	t.Parallel()
	dirs, err := filepath.Glob(filepath.Join("*", "testdata", "fixtures"))
	require.NoError(t, err)
	require.Len(t, dirs, 3, "three harness fixture directories are expected beneath this package; a fourth harness adds one and this count moves with it")
	for _, dir := range dirs {
		dir := dir
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(filepath.Join(dir, "CLEARANCE.md"))
			require.NoError(t, err, "%s has no CLEARANCE.md; every fixture directory carries the clearance record its fixtures cite", dir)
			text := string(raw)
			last := -1
			for _, heading := range clearanceHeadings {
				index := strings.Index(text, heading+"\n")
				assert.Greater(t, index, last, "%s/CLEARANCE.md is missing the section %q or has it out of order", dir, heading)
				if index > last {
					last = index
				}
			}
			flat := singleSpaced(text)
			for _, pin := range clearancePins {
				assert.Contains(t, flat, pin, "%s/CLEARANCE.md lost the phrase %q", dir, pin)
			}
		})
	}
}

// agentsCaptureHeading opens the section of AGENTS.md this file pins.
const agentsCaptureHeading = "### Capturing host payloads and clearing them into fixtures"

// agentsCapturePins are the load-bearing phrases of that section, each a
// mechanism a reader acts on and each proved by mutation in the slice report.
var agentsCapturePins = []string{
	"`PASTURE_CAPTURE_DIR`",
	"ABSOLUTE path",
	"OUTSIDE the repository",
	"already EXIST",
	"never creates the directory",
	"never overwrites a file",
	"capture mode is recording this session to <dir>",
	"the host outcome is unchanged",
	"LIVE session",
	"is read-only for every agent and is not a fixture source",
	"`home-path-v1`",
	"`free-text-v1`",
	"same raw length",
	"Keys, nesting, types and nulls are unchanged",
	"UNCLEARABLE",
	"every file beneath every `testdata` directory",
	"`CLEARANCE.md`",
	"nothing captured reaches any remote before the user's acceptance",
	"The integrator pushes, opens the pull request",
	"above 4096 bytes",
	"FAILS OPEN by default",
	"`PASTURE_HOOK_FAIL_CLOSED=1`",
	"FAILS CLOSED",
	"`FailureEvidence`",
	"EMPTY standard output",
}

// agentsSection returns the text of one AGENTS.md section: from its heading
// to the next heading of the same or a higher level.
func agentsSection(t *testing.T, heading string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(moduleRoot(t), "AGENTS.md"))
	require.NoError(t, err)
	text := string(raw)
	start := strings.Index(text, heading+"\n")
	require.GreaterOrEqual(t, start, 0, "AGENTS.md has no section %q", heading)
	rest := text[start+len(heading):]
	end := len(rest)
	for _, marker := range []string{"\n### ", "\n## "} {
		if index := strings.Index(rest, marker); index >= 0 && index < end {
			end = index
		}
	}
	return rest[:end]
}

// TestAgentsDocumentsTheCaptureClearanceAndGatePolicyRules pins the
// load-bearing phrases of the capture, clearance and gate-policy section of
// AGENTS.md. Each phrase is a rule a person acts on; a phrase that disappears
// is a rule that disappears with it.
func TestAgentsDocumentsTheCaptureClearanceAndGatePolicyRules(t *testing.T) {
	t.Parallel()
	section := singleSpaced(agentsSection(t, agentsCaptureHeading))
	require.NotEmpty(t, section)
	for _, pin := range agentsCapturePins {
		assert.Contains(t, section, pin, "AGENTS.md section %q lost the phrase %q", agentsCaptureHeading, pin)
	}
	assert.NotContains(t, section, "the worker pushes", "the worker never pushes; the integrator does")
}
