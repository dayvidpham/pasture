package ingress_test

import (
	"crypto/sha256"
	"encoding/hex"
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
// proved by mutation: reword the phrase in the template and the test that
// reads it turns RED naming the phrase.
var clearancePins = []string{
	"outside the repository",
	"`home-path-v1`, then `free-text-v1`",
	"Structure, keys, types and nulls are unchanged",
	"Nothing in this directory reaches a remote before this section is filled",
	"a fixture may name this file only after this section holds the acceptance",
	"finds the grant recorded and never a blank form",
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
// mechanism a reader acts on and each proved by mutation: reword the phrase
// in AGENTS.md and the test that reads it turns RED naming the section and
// the phrase.
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
	"every spelling of the capturing user's home directory",
	"the absolute `/home/<user>`, the relative `home/<user>/`",
	"`-home-<user>-`",
	"any occurrence inside free text",
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

// clearanceTransports names, per harness fixture directory, the committed
// transport files a capture of that harness ran through. A transport is the
// program the host executes to reach the lifecycle hook, so it decides what
// bytes a capture holds; a record that quotes a digest for one of these files
// is making a claim about the file this repository ships.
//
// The paths are relative to the repository root. They are the same artefacts
// the transport parity guard reads.
var clearanceTransports = map[string][]string{
	"claude": {
		"hooks/hooks.json",
	},
	"codex": {
		".codex/hooks.json",
		".codex/hooks/events/SessionStart.sh",
		".codex/hooks/events/PreToolUse.sh",
	},
	"opencode": {
		".opencode/plugins/pasture-lifecycle.ts",
	},
}

// sha256Hex is the digest spelling every clearance record uses.
func sha256Hex(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoErrorf(t, err, "the committed transport %q cannot be read, so no record can be checked against it", path)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// TestEveryClearanceRecordQuotesTheCommittedTransportDigest holds every
// clearance record against the transports this repository ships.
//
// A record exists to answer one question: were these fixtures captured with
// the transport pasture ships, or with something else? A record that quotes
// only a build-kit digest cannot answer it. A reader who hashes the shipped
// file and gets a different number learns nothing about which of the two is
// right, and the record can drift from the tree without anything failing —
// which is how one of these records did drift.
//
// The rule is therefore: for every committed transport of a harness, the
// harness record quotes that file's sha256. A record MAY also quote another
// digest (a build kit that differs), as long as it states the committed one
// beside it. Population: the three records and every transport of their
// harness. Non-vacuity: every harness declares at least one transport, and
// every record quotes at least one digest.
func TestEveryClearanceRecordQuotesTheCommittedTransportDigest(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	dirs, err := filepath.Glob(filepath.Join("*", "testdata", "fixtures"))
	require.NoError(t, err)
	require.Len(t, dirs, 3, "three harness fixture directories are expected beneath this package; a fourth harness adds one and this count moves with it")
	require.Len(t, clearanceTransports, len(dirs), "every harness fixture directory declares the transports its captures ran through")

	digest := regexp.MustCompile(`[0-9a-f]{64}`)
	for _, dir := range dirs {
		dir := dir
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			harness := strings.Split(dir, string(filepath.Separator))[0]
			transports := clearanceTransports[harness]
			require.NotEmptyf(t, transports, "harness %q declares no transport, so this guard would hold nothing for it", harness)

			raw, err := os.ReadFile(filepath.Join(dir, "CLEARANCE.md"))
			require.NoError(t, err, "%s has no CLEARANCE.md", dir)
			quoted := map[string]struct{}{}
			for _, found := range digest.FindAllString(string(raw), -1) {
				quoted[found] = struct{}{}
			}
			require.NotEmptyf(t, quoted, "%s/CLEARANCE.md quotes no digest at all, so it records no evidence this guard can check", dir)

			for _, transport := range transports {
				committed := sha256Hex(t, filepath.Join(root, transport))
				_, ok := quoted[committed]
				assert.Truef(t, ok,
					"%s/CLEARANCE.md quotes no digest equal to the committed %s (sha256 %s). "+
						"A reader who hashes the shipped file gets that number, so the record must carry it, "+
						"beside the build-kit digest if the two differ and with the difference stated",
					dir, transport, committed)
			}
		})
	}
}
