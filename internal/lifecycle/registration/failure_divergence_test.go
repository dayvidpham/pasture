package registration_test

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

// This file is one subject: THE COMMITTED MANIFESTS AND THE RUNTIME PROFILE
// DISAGREE ABOUT WHICH EVENTS BLOCK, and the disagreement must not grow or
// shrink by accident.
//
// Two committed artefacts now speak the SAME failure vocabulary and give
// DIFFERENT answers for the same event. The generated registration manifests
// take their failure mode from the ingress host-contract catalogues, which
// hard-code the blocking arm for their gate rows. The runtime profile applies
// the evidence rule: a row may claim a blocking exit code only while it cites
// host evidence for it, and most rows cite none yet, so they run as
// report-and-continue.
//
// The divergence is INERT today, and that is a reason it is not a BLOCKER
// rather than a reason to leave it unwatched. Nothing in production reads
// registration Event.Failure: the two live readers both read the runtime
// mapping. What is wrong is that a false claim sits in a committed artefact and
// nothing fails when it grows.
//
// The shape that removes the class already exists in one of the three
// catalogues: the OpenCode catalogue derives its Failure from the runtime
// mapping, so it cannot diverge at all. Doing the same for Claude and Codex is a
// smaller change than re-deriving those catalogues, and it is the fix the
// harness slices should prefer.
//
// This test pins the divergent set EXACTLY. It turns RED when the set GROWS,
// which is a new silent divergence, and RED when it SHRINKS, which means a
// harness slice fixed a row and must record that here deliberately.

// divergentRows is the MEASURED set: every event whose committed registration
// manifest states a different failure mode from the runtime profile that
// governs behaviour. It is 22 rows, and it is NOT the same set as the rows the
// evidence rule demoted, which is a distinction worth keeping straight:
//
//   - 18 rows OVER-CLAIM BLOCKING: 11 Claude rows and all 7 Codex gates.
//     The manifest says the host refuses on the pasture exit code; the runtime
//     says report-and-continue, because the row cites no host evidence for that
//     claim. Believing the manifest is the dangerous mistake here.
//   - 4 rows differ only between two NON-BLOCKING arms: the Codex SessionStart,
//     SessionEnd, Interrupt and SubagentStart observations read report-and-continue in the manifest
//     and strict-hook-failure in the runtime. That divergence pre-dates the
//     evidence rule and comes from the catalogue simplifying its non-blocking
//     arm. Neither arm can refuse anything, so nothing is over-claimed.
//   - Codex PostCompact is a gate in the runtime profile and an observation in
//     the catalogue, and the two land on the same arm by coincidence. It is
//     therefore absent from this list even though the two artefacts do not
//     agree about what the event IS.
//
// The count differs from the count of rows the evidence rule demoted (11 Claude
// plus all 7 Codex gates). The two sets overlap and are not the same question:
// one asks what the runtime profile changed, the other asks where the two
// committed artefacts disagree today.
var divergentRows = map[ir.HarnessID][]string{
	ir.HarnessClaudeCode: {
		"ConfigChange",
		"Elicitation",
		"ElicitationResult",
		"PermissionRequest",
		"PostToolBatch",
		"PreCompact",
		"TaskCompleted",
		"TaskCreated",
		"TeammateIdle",
		"UserPromptExpansion",
		"WorktreeCreate",
	},
	ir.HarnessCodex: {
		"Interrupt",
		"PermissionRequest",
		"PostToolUse",
		"PreCompact",
		"PreToolUse",
		"SessionEnd",
		"SessionStart",
		"Stop",
		"SubagentStart",
		"SubagentStop",
		"UserPromptSubmit",
	},
	ir.HarnessOpenCode: {},
}

// overClaimsBlocking names the subset of divergentRows where the manifest
// claims a blocking exit code the runtime does not. These are the rows a reader
// can be actively misled by.
var overClaimsBlocking = map[ir.HarnessID][]string{
	ir.HarnessClaudeCode: {
		"ConfigChange", "Elicitation", "ElicitationResult", "PermissionRequest",
		"PostToolBatch", "PreCompact", "TaskCompleted", "TaskCreated",
		"TeammateIdle", "UserPromptExpansion", "WorktreeCreate",
	},
	ir.HarnessCodex: {
		"PermissionRequest", "PostToolUse", "PreCompact", "PreToolUse",
		"Stop", "SubagentStop", "UserPromptSubmit",
	},
	ir.HarnessOpenCode: {},
}

func manifests() map[ir.HarnessID]registration.Manifest {
	return map[ir.HarnessID]registration.Manifest{
		ir.HarnessClaudeCode: registration.ClaudeCode2_1_261(),
		ir.HarnessCodex:      registration.Codex0_153_0(),
		ir.HarnessOpenCode:   registration.OpenCode1_18_29(),
	}
}

func TestTheManifestAndTheRuntimeDisagreeOnExactlyTheseRows(t *testing.T) {
	t.Parallel()

	total := 0
	for harness, manifest := range manifests() {
		harness, manifest := harness, manifest
		t.Run(string(harness), func(t *testing.T) {
			t.Parallel()

			var diverged []string
			for _, event := range manifest.Entries() {
				runtimeRow, declared := pastureruntime.LookupLifecycleFailure(harness, event.NativeName)
				if !declared {
					t.Errorf("manifest event %q of harness %q is not declared by the runtime profile at all, "+
						"so nothing governs its behaviour", event.NativeName, harness)
					continue
				}
				if event.Failure != runtimeRow.Mode {
					diverged = append(diverged, event.NativeName)
				}
			}
			sort.Strings(diverged)

			want := append([]string(nil), divergentRows[harness]...)
			sort.Strings(want)
			if len(diverged) != len(want) {
				t.Fatalf("harness %q diverges on %d rows %v, want exactly %d %v; "+
					"a LARGER set is a new silent divergence, a SMALLER one means a row was aligned and this list must be updated deliberately",
					harness, len(diverged), diverged, len(want), want)
			}
			for index := range want {
				if diverged[index] != want[index] {
					t.Fatalf("harness %q divergent rows = %v, want %v", harness, diverged, want)
				}
			}
		})
		total += len(divergentRows[harness])
	}

	if total != 22 {
		t.Fatalf("the recorded divergence covers %d rows, want 22: 11 Claude rows plus 11 Codex rows", total)
	}
}

// TestTheOverClaimingRowsAreNamedSeparately states the DIRECTION of the
// disagreement for the rows where direction matters. The manifest claims a
// blocking exit code the runtime does not, so a reader who believes the
// manifest thinks pasture can stop a user's action on a row where it cannot.
// The other two divergent rows sit between two non-blocking arms and can
// mislead nobody about refusal, which is why they are listed apart.
func TestTheOverClaimingRowsAreNamedSeparately(t *testing.T) {
	t.Parallel()

	all := manifests()
	overClaiming := 0
	for harness, names := range overClaimsBlocking {
		byName := map[string]registration.Event{}
		for _, event := range all[harness].Entries() {
			byName[event.NativeName] = event
		}
		for _, name := range names {
			event, present := byName[name]
			if !present {
				t.Fatalf("the recorded over-claiming row %q is not in the %q manifest", name, harness)
			}
			if !event.Failure.BlocksByExitCode() {
				t.Errorf("%q row %q is listed as over-claiming but its manifest arm does not block by exit code; "+
					"the direction of the disagreement changed and the note in the host-contract catalogue must change with it",
					harness, name)
			}
			runtimeRow, _ := pastureruntime.LookupLifecycleFailure(harness, name)
			if runtimeRow.Mode.BlocksByExitCode() {
				t.Errorf("%q row %q claims a blocking exit code in BOTH artefacts, so it is not divergent", harness, name)
			}
		}
		overClaiming += len(names)
	}
	if overClaiming != 18 {
		t.Fatalf("%d rows over-claim blocking, want 18 of the 22 divergent rows", overClaiming)
	}
}

// codexDivergenceCounts is every number the two committed Codex doc comments
// state about the divergence, derived from the tree at head.
type codexDivergenceCounts struct {
	registered      int
	withoutCapture  int
	failureDiverged int
	overClaiming    int
	catalogueGates  int
}

// deriveCodexDivergenceCounts measures the counts from the product's own
// sources: the generated Codex registration manifest, the Codex activation
// manifest (which refuses an entry with no event-bound capture proof) and the
// runtime Codex profile. Nothing here is typed by hand, so a new registration
// or a new capture moves every number at once.
func deriveCodexDivergenceCounts(t *testing.T) codexDivergenceCounts {
	t.Helper()

	manifest := registration.Codex0_153_0()
	counts := codexDivergenceCounts{registered: len(manifest.Entries())}
	for _, event := range manifest.Entries() {
		runtimeRow, declared := pastureruntime.LookupLifecycleFailure(ir.HarnessCodex, event.NativeName)
		if !declared {
			t.Fatalf("manifest event %q of harness %q is not declared by the runtime profile", event.NativeName, ir.HarnessCodex)
		}
		if event.Failure != runtimeRow.Mode {
			counts.failureDiverged++
		}
		if event.Failure.BlocksByExitCode() {
			counts.catalogueGates++
			if !runtimeRow.Mode.BlocksByExitCode() {
				counts.overClaiming++
			}
		}
	}

	entries, err := activation.Codex0_153_0()
	if err != nil {
		t.Fatalf("the Codex activation manifest does not derive: %v", err)
	}
	if len(entries) != counts.registered {
		t.Fatalf("the Codex activation manifest holds %d entries for %d registered events; the two must be exhaustive over each other",
			len(entries), counts.registered)
	}
	for _, entry := range entries {
		if !entry.CaptureProof.IsValid() {
			counts.withoutCapture++
		}
	}
	return counts
}

// codexDocComments are the two committed files that state the divergence in
// prose. Both are read by a maintainer to learn what diverges and what stops
// it, so both are held to the tree they describe.
var codexDocComments = []string{
	"../ingress/internal/hostcontract/codex_0_153_0.go",
	"../frontend/codex/codex.go",
}

// readCodexDocComment reads one source file and flattens its comment text, so a
// sentence that wraps over several comment lines is one string to search.
func readCodexDocComment(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the committed file %q cannot be read, so its counts cannot be held: %v", path, err)
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(string(raw), "//", " ")), " ")
}

// TestTheCodexDocCommentsStateTheDerivedCounts holds every count the two Codex
// doc comments state against the count derived from the tree. A count in prose
// is a claim about the tree, and this repository has already watched one of
// them rot: the "events without an authentic capture" number was swept when a
// row was added and not swept when the next row was added.
//
// The pin is a READ, not a second hand-maintained number: each expected
// sentence is built from the derived count, so a new registration or a new
// capture turns this test RED naming the file and the sentence it must carry.
func TestTheCodexDocCommentsStateTheDerivedCounts(t *testing.T) {
	t.Parallel()

	counts := deriveCodexDivergenceCounts(t)
	catalogue := readCodexDocComment(t, codexDocComments[0])
	frontend := readCodexDocComment(t, codexDocComments[1])

	pins := []struct {
		path     string
		text     string
		sentence string
	}{
		{codexDocComments[0], catalogue, fmt.Sprintf("Of the %d registered Codex events, %d have no authentic capture", counts.registered, counts.withoutCapture)},
		{codexDocComments[0], catalogue, fmt.Sprintf("the two artefacts disagree on %d rows, and on %d of those this source declares a BLOCKING failure mode", counts.failureDiverged, counts.overClaiming)},
		{codexDocComments[1], frontend, fmt.Sprintf("the failure mode on %d of the %d", counts.failureDiverged, counts.registered)},
		{codexDocComments[1], frontend, fmt.Sprintf("identities on the %d events without an authentic capture", counts.withoutCapture)},
	}
	for _, pin := range pins {
		if !strings.Contains(pin.text, pin.sentence) {
			t.Errorf("the committed file %s does not state the count the tree derives; it must carry the sentence %q. "+
				"A count in a doc comment is a claim about the tree: sweep it in the commit that moves it",
				pin.path, pin.sentence)
		}
	}

	// The mechanism sentence, and the claim it replaced. The frontend mapping is
	// complete over every registered event, so the frontend rejects nothing; the
	// activation table is what withholds an event without a capture proof.
	if !strings.Contains(catalogue, "the mechanism that stops it is the ACTIVATION TABLE") {
		t.Errorf("the committed file %s must name the activation table as the mechanism that withholds an event without a capture proof",
			codexDocComments[0])
	}
	for _, retired := range []string{"and why it is inert", "rejects the other"} {
		if strings.Contains(catalogue, retired) {
			t.Errorf("the committed file %s still carries the retired claim %q", codexDocComments[0], retired)
		}
	}

	// Non-vacuity: a derivation that measures nothing would pass every pin above.
	if counts.registered == 0 || counts.withoutCapture == 0 || counts.failureDiverged == 0 || counts.overClaiming == 0 {
		t.Fatalf("the derived Codex counts are %+v; a zero means the derivation reads nothing and the pins above prove nothing", counts)
	}
	if counts.catalogueGates != len(overClaimsBlocking[ir.HarnessCodex]) {
		t.Errorf("the Codex catalogue declares %d gate rows and %d of them over-claim blocking; while the runtime profile cites no Codex evidence the two counts are equal, and the notes that say so must be swept together",
			counts.catalogueGates, len(overClaimsBlocking[ir.HarnessCodex]))
	}
}
