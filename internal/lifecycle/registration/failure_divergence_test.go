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
// Two committed artefacts speak the SAME failure vocabulary and can give
// DIFFERENT answers for the same event. The generated registration manifests
// take their failure mode from the ingress host-contract catalogues. The
// runtime profile applies the evidence rule: a row may claim a blocking exit
// code only while it cites host evidence for it, and most rows cite none yet,
// so they run as report-and-continue.
//
// The divergence is INERT today, and that is a reason it is not a BLOCKER
// rather than a reason to leave it unwatched. Nothing in production reads
// registration Event.Failure: the two live readers both read the runtime
// mapping. What is wrong is that a false claim sits in a committed artefact and
// nothing fails when it grows.
//
// The shape that removes the class exists in two of the three catalogues now.
// The OpenCode catalogue derives its whole row from the runtime mapping, and
// the Codex catalogue READS the failure mode from it, so neither can diverge on
// that field at all. Doing the same for Claude is a smaller change than
// re-deriving that catalogue, and it is the fix the Claude work should prefer.
//
// This test pins the divergent set EXACTLY. It turns RED when the set GROWS,
// which is a new silent divergence, and RED when it SHRINKS, which means a
// harness fixed a row and must record that here deliberately.

// divergentRows is the MEASURED set: every event whose committed registration
// manifest states a different failure mode from the runtime profile that
// governs behaviour. It is 11 rows, and it is NOT the same set as the rows the
// evidence rule demoted, which is a distinction worth keeping straight:
//
//   - 11 Claude rows OVER-CLAIM BLOCKING. The manifest says the host refuses on
//     the pasture exit code; the runtime says report-and-continue, because the
//     row cites no host evidence for that claim. Believing the manifest is the
//     dangerous mistake here.
//   - The Codex list is EMPTY, and it is empty BY CONSTRUCTION: the Codex
//     catalogue reads its failure mode from the runtime profile, in
//     internal/lifecycle/ingress/internal/hostcontract/codex_0_153_0.go, so the
//     two artefacts cannot state different modes for one Codex row. That list
//     held 11 rows while the field was written twice, 7 of them over-claiming a
//     blocking exit code and 4 sitting between two non-blocking arms.
//   - The Codex read copies the failure arm and NEVER the gate-or-observation
//     semantic, the mutation mode or the identities. Those axes still disagree,
//     and they are pinned by name below rather than left unwatched.
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
	ir.HarnessCodex:    {},
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
	ir.HarnessCodex:    {},
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

	if total != 11 {
		t.Fatalf("the recorded divergence covers %d rows, want 11: 11 Claude rows, no Codex row and no OpenCode row", total)
	}
}

// TestTheOverClaimingRowsAreNamedSeparately states the DIRECTION of the
// disagreement for the rows where direction matters. The manifest claims a
// blocking exit code the runtime does not, so a reader who believes the
// manifest thinks pasture can stop a user's action on a row where it cannot.
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
	if overClaiming != 11 {
		t.Fatalf("%d rows over-claim blocking, want 11 of the 11 divergent rows", overClaiming)
	}
}

// codexDivergenceCounts is every number and every name the two committed Codex
// doc comments state about the two artefacts, derived from the tree at head.
type codexDivergenceCounts struct {
	registered      int
	withoutCapture  int
	failureDiverged int
	overClaiming    int
	catalogueGates  int
	// demotedGates counts the rows whose runtime row DECLARES a blocking exit
	// code and does not keep it, for want of a citation. It is the control that
	// keeps the agreement above from passing on an empty question: while it is
	// positive, the evidence rule really is moving Codex rows, and the
	// catalogue is following the moved arm rather than a constant.
	demotedGates int
	// identityAbsent names the rows where the profile declares correlation
	// identities and the catalogue declares none.
	identityAbsent []string
	// semanticDiverged names the rows the catalogue and the profile classify
	// differently as a gate or an observation.
	semanticDiverged []string
	// mutationDiverged names the rows the two state a different mutation mode
	// for.
	mutationDiverged []string
}

// codexRuntimeMappings returns the runtime Codex rows by native name. It walks
// the profile's own event catalog, so a row added there is measured with no
// edit here.
func codexRuntimeMappings(t *testing.T) map[string]pastureruntime.LifecycleEventMapping {
	t.Helper()

	contract := pastureruntime.Codex0_153_0Lifecycle()
	rows := map[string]pastureruntime.LifecycleEventMapping{}
	for _, event := range pastureruntime.CodexLifecycleEvents() {
		mapping, err := contract.Mapping(event)
		if err != nil {
			t.Fatalf("the runtime Codex profile holds no mapping for its own event %v: %v", event, err)
		}
		rows[mapping.NativeName()] = mapping
	}
	return rows
}

// mutationAgrees reports whether the catalogue's mutation arm and the runtime's
// are the same behaviour. The catalogue vocabulary has no output arm, so a
// runtime row that mutates the tool OUTPUT can never agree with it.
func mutationAgrees(catalogue registration.MutationMode, runtimeMode pastureruntime.MutationMode) bool {
	switch catalogue {
	case registration.MutationNone:
		return runtimeMode == pastureruntime.MutationNone
	case registration.MutationInput:
		return runtimeMode == pastureruntime.MutationInput
	default:
		return false
	}
}

// deriveCodexDivergenceCounts measures the counts from the product's own
// sources: the generated Codex registration manifest, the Codex activation
// manifest (which refuses an entry with no event-bound capture proof) and the
// runtime Codex profile. Nothing here is typed by hand, so a new registration
// or a new capture moves every number at once.
func deriveCodexDivergenceCounts(t *testing.T) codexDivergenceCounts {
	t.Helper()

	runtimeRows := codexRuntimeMappings(t)
	manifest := registration.Codex0_153_0()
	counts := codexDivergenceCounts{registered: len(manifest.Entries())}
	for _, event := range manifest.Entries() {
		runtimeRow, declared := pastureruntime.LookupLifecycleFailure(ir.HarnessCodex, event.NativeName)
		if !declared {
			t.Fatalf("manifest event %q of harness %q is not declared by the runtime profile", event.NativeName, ir.HarnessCodex)
		}
		mapping, present := runtimeRows[event.NativeName]
		if !present {
			t.Fatalf("the runtime Codex profile holds no row named %q, so its axes cannot be compared", event.NativeName)
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
		if runtimeRow.DeclaredMode.BlocksByExitCode() && !runtimeRow.Mode.BlocksByExitCode() {
			counts.demotedGates++
		}
		if (event.Blocking == registration.Blocking) != (runtimeRow.Semantic == pastureruntime.SemanticGateConsultation) {
			counts.semanticDiverged = append(counts.semanticDiverged, event.NativeName)
		}
		if !mutationAgrees(event.Mutation, mapping.Mutation()) {
			counts.mutationDiverged = append(counts.mutationDiverged, event.NativeName)
		}
		switch {
		case len(event.Identities) == 0 && len(mapping.Identities()) > 0:
			counts.identityAbsent = append(counts.identityAbsent, event.NativeName)
		case len(event.Identities) != len(mapping.Identities()):
			t.Errorf("the Codex catalogue declares %d identities on row %q and the runtime profile declares %d; "+
				"the doc comments describe the identity difference as rows where the catalogue declares NONE, and this row is a third case they do not cover",
				len(event.Identities), event.NativeName, len(mapping.Identities()))
		}
	}
	sort.Strings(counts.identityAbsent)
	sort.Strings(counts.semanticDiverged)
	sort.Strings(counts.mutationDiverged)

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
		{codexDocComments[0], catalogue, fmt.Sprintf("The failure mode of every one of the %d rows is read from the runtime Codex profile", counts.registered)},
		{codexDocComments[0], catalogue, fmt.Sprintf("the two artefacts disagree on the failure mode of %d of the %d rows", counts.failureDiverged, counts.registered)},
		{codexDocComments[1], frontend, fmt.Sprintf("the same failure mode on all %d of the %d registered events", counts.registered-counts.failureDiverged, counts.registered)},
		{codexDocComments[1], frontend, fmt.Sprintf("the profile declares on %d rows where the catalogue declares none", len(counts.identityAbsent))},
	}
	for _, pin := range pins {
		if !strings.Contains(pin.text, pin.sentence) {
			t.Errorf("the committed file %s does not state the count the tree derives; it must carry the sentence %q. "+
				"A count in a doc comment is a claim about the tree: sweep it in the commit that moves it",
				pin.path, pin.sentence)
		}
	}

	// The two axes the doc comments name row by row. The names are derived, so a
	// row that starts or stops diverging on one of them turns this RED naming
	// the file that must say so.
	for _, named := range []struct {
		axis  string
		names []string
	}{
		{"the gate-or-observation semantic", counts.semanticDiverged},
		{"the mutation mode", counts.mutationDiverged},
	} {
		for _, name := range named.names {
			if !strings.Contains(frontend, name) {
				t.Errorf("the committed file %s states which rows differ on %s and does not name %q, which the tree says differs there",
					codexDocComments[1], named.axis, name)
			}
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

	// Non-vacuity. The agreement the sentences state is worth nothing if the
	// evidence rule is moving no Codex row: both artefacts would then hold the
	// same arm because neither has anything to say. While demotedGates is
	// positive, the profile IS demoting declared Codex gates, and the catalogue
	// is carrying the demoted arm rather than a constant of its own.
	if counts.registered == 0 || counts.withoutCapture == 0 || counts.demotedGates == 0 {
		t.Fatalf("the derived Codex counts are %+v; a zero in the registered, capture-free or demoted-gate count means the derivation reads nothing and the pins above prove nothing", counts)
	}
	if counts.failureDiverged != 0 || counts.overClaiming != 0 {
		t.Errorf("the Codex catalogue and the runtime profile disagree on the failure mode of %d rows, %d of them over-claiming a blocking exit code, want none; "+
			"the catalogue READS that field from the profile, so a disagreement means a row went back to a hand-written arm",
			counts.failureDiverged, counts.overClaiming)
	}
	if counts.catalogueGates != 0 {
		t.Errorf("the Codex catalogue states a blocking exit code on %d rows while the runtime profile cites no Codex evidence for one; "+
			"a reader of the catalogue would learn a refusal the product cannot perform", counts.catalogueGates)
	}
}

// TestTheCodexArtefactsDisagreeOnExactlyTheseOtherAxes pins what the failure-
// mode read deliberately did NOT align. The read copies one field. The
// gate-or-observation semantic, the mutation mode and the identities stay
// declared in the catalogue, so the rows below still hold two descriptions.
//
// The lists are pinned EXACTLY, for the same reason the failure list above is:
// a row that starts diverging is a new silent difference, and a row that stops
// is a deliberate alignment somebody must record here.
func TestTheCodexArtefactsDisagreeOnExactlyTheseOtherAxes(t *testing.T) {
	t.Parallel()

	counts := deriveCodexDivergenceCounts(t)

	for _, axis := range []struct {
		name string
		got  []string
		want []string
		why  string
	}{
		{
			name: "the gate-or-observation semantic",
			got:  counts.semanticDiverged,
			want: []string{"PostCompact"},
			why: "PostCompact is an observation in the catalogue and a gate in the runtime profile, and the two land on the same failure arm only " +
				"because the profile's gate cites no evidence; aligning that semantic needs the host emission site read, not a failure-mode read",
		},
		{
			name: "the mutation mode",
			got:  counts.mutationDiverged,
			want: []string{"PostToolUse"},
			why: "the catalogue vocabulary has no output arm, so a runtime row that mutates the tool OUTPUT cannot be spelled there; " +
				"closing this needs the catalogue vocabulary widened, not a failure-mode read",
		},
		{
			name: "the declared identities",
			got:  counts.identityAbsent,
			want: []string{
				"PermissionRequest", "PostCompact", "PostToolUse", "PreCompact",
				"Stop", "SubagentStart", "SubagentStop", "UserPromptSubmit",
			},
			why: "the catalogue declares an identity only from an authentic capture, and these rows have none; " +
				"a capture is what removes a row from this list",
		},
	} {
		if len(axis.got) != len(axis.want) {
			t.Errorf("the two Codex artefacts disagree on %s for %d rows %v, want exactly %d %v; %s",
				axis.name, len(axis.got), axis.got, len(axis.want), axis.want, axis.why)
			continue
		}
		for index := range axis.want {
			if axis.got[index] != axis.want[index] {
				t.Errorf("the two Codex artefacts disagree on %s for rows %v, want %v; %s",
					axis.name, axis.got, axis.want, axis.why)
				break
			}
		}
	}
}
