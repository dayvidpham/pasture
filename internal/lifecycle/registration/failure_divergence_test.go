package registration_test

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
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
// registration Event.Failure on the hook path; it reads the runtime
// mapping. What is wrong is that a false claim sits in a committed artefact and
// nothing fails when it grows.
//
// Derive the catalogue failure arm from the runtime mapping instead of
// declaring it independently. Regenerate the manifest after an input changes.
//
// This test pins the divergent set EXACTLY. Rule out a stale generated
// manifest before changing the recorded set to match a measurement.

// divergentRows is the MEASURED set: every event whose committed registration
// manifest states a different failure mode from the runtime profile that
// governs behaviour. It is 11 rows, and it is NOT the same set as the rows the
// evidence rule demoted, which is a distinction worth keeping straight:
//
//   - 11 Claude rows OVER-CLAIM BLOCKING. The manifest says the host refuses on
//     the pasture exit code; the runtime says report-and-continue, because the
//     row cites no host evidence for that claim. Believing the manifest is the
//     dangerous mistake here.
//   - The Codex catalogue reads its failure arm from the runtime profile in
//     internal/lifecycle/ingress/internal/hostcontract/codex_0_153_0.go.
//     The committed generated manifest follows only after make generate runs.
//     Other catalogue properties remain independently declared.
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
					"regenerate before judging whether the recorded set needs a deliberate update."+staleManifestFirst,
					harness, len(diverged), diverged, len(want), want)
			}
			for index := range want {
				if diverged[index] != want[index] {
					t.Fatalf("harness %q divergent rows = %v, want %v; regenerate before updating the recorded set."+staleManifestFirst, harness, diverged, want)
				}
			}
		})
		total += len(divergentRows[harness])
	}

	if total != 11 {
		t.Fatalf("the recorded divergence total changed to %d; inspect the per-harness differences before changing the total guard", total)
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
				t.Fatalf("the recorded over-claiming row %q is not in the %q manifest."+staleManifestFirst, name, harness)
			}
			if !event.Failure.BlocksByExitCode() {
				t.Errorf("%q row %q is listed as over-claiming but its manifest arm does not block by exit code; "+
					"regenerate before judging the direction of the disagreement or changing the catalogue note."+staleManifestFirst,
					harness, name)
			}
			runtimeRow, _ := pastureruntime.LookupLifecycleFailure(harness, name)
			if runtimeRow.Mode.BlocksByExitCode() {
				t.Errorf("%q row %q claims a blocking exit code in the manifest and the runtime profile, so it is not divergent."+staleManifestFirst, harness, name)
			}
		}
		overClaiming += len(names)
	}
	if overClaiming != 11 {
		t.Fatalf("the recorded over-claiming total changed to %d; inspect the row assertions before changing the total guard", overClaiming)
	}
}

// staleManifestFirst is the state to rule out FIRST whenever a Codex
// measurement in this file disagrees with a committed sentence or a recorded
// set. A measurement against the COMMITTED generated manifest can be stale: a
// change to the runtime profile or to the source catalogue moves one side of a
// comparison at once and the other side only after generation. A reader who is
// not told this edits a sentence, or widens a recorded set, to match a number
// the next generation takes back.
//
// Keep the repair instruction shared so its callers do not drift apart.
const staleManifestFirst = " The measured side reads the COMMITTED generated manifest internal/lifecycle/registration/codex_0_153_0.gen.go. " +
	"So if internal/runtime/lifecycle_profiles_codex.go or the source catalogue has moved and make generate has not run since, RUN IT FIRST: " +
	"until it runs this measurement is taken against a stale manifest, and a repair made by hand is undone by the next generation."

// codexDivergenceCounts holds the populations used by the sentence pins below.
type codexDivergenceCounts struct {
	registered      int
	withoutCapture  int
	failureDiverged int
	overClaiming    int
	catalogueGates  int
	// demotedGates counts the rows whose runtime row DECLARES a blocking exit
	// code and does not keep it, for want of a citation. This describes the
	// current profile; it does not prove that the catalogue uses the read.
	demotedGates int
	// catalogueGateRows names the rows whose COMMITTED manifest arm blocks by
	// exit code. It can differ from the profile when the manifest is stale.
	catalogueGateRows []string
	// evidencedGateRows names the runtime rows that DECLARE a blocking exit code
	// AND cite host evidence for it, so the evidence rule keeps the arm. It is
	// the INPUT side of the rule whose OUTPUT catalogueGateRows is, so the two
	// lists must name the same rows in every citation state.
	evidencedGateRows []string
	// identityAbsent names the rows where the profile declares correlation
	// identities and the catalogue declares none.
	identityAbsent []string
	// identityAbsentWithoutCapture counts the identityAbsent rows that also have
	// no authentic capture. The frontend comment states this number, so it is
	// derived rather than typed.
	identityAbsentWithoutCapture int
	// captureFreeWithIdentity counts the rows with no authentic capture on which
	// the CATALOGUE declares an identity. The catalogue comment states this
	// number, so it is derived rather than typed.
	captureFreeWithIdentity int
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
// runtime Codex profile. The predicates below determine which changes move
// each population.
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
			counts.catalogueGateRows = append(counts.catalogueGateRows, event.NativeName)
			if !runtimeRow.Mode.BlocksByExitCode() {
				counts.overClaiming++
			}
		}
		if runtimeRow.DeclaredMode.BlocksByExitCode() && runtimeRow.Evidence.IsPresent() {
			counts.evidencedGateRows = append(counts.evidencedGateRows, event.NativeName)
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
			t.Errorf("the committed Codex manifest declares %d identities on row %q and the runtime profile declares %d; "+
				"the doc comments describe the identity difference as rows where the manifest declares NONE, which does not describe this row."+
				staleManifestFirst,
				len(event.Identities), event.NativeName, len(mapping.Identities()))
		}
	}
	sort.Strings(counts.identityAbsent)
	sort.Strings(counts.semanticDiverged)
	sort.Strings(counts.mutationDiverged)
	sort.Strings(counts.catalogueGateRows)
	sort.Strings(counts.evidencedGateRows)

	entries, err := activation.Codex0_153_0()
	if err != nil {
		t.Fatalf("the Codex activation manifest does not derive: %v", err)
	}
	if len(entries) != counts.registered {
		t.Fatalf("the Codex activation manifest holds %d entries for %d registered events; the two must be exhaustive over each other",
			len(entries), counts.registered)
	}
	// nameByKind lets the capture state be read BY ROW, so the sentences
	// that cross the capture axis with the identity axis are derived and not
	// typed. A kind the manifest does not name would leave a capture-free row
	// uncounted, so it stops the derivation instead.
	nameByKind := map[model.ContractEventKind]string{}
	catalogueIdentities := map[string]int{}
	for _, event := range manifest.Entries() {
		nameByKind[event.Kind] = event.NativeName
		catalogueIdentities[event.NativeName] = len(event.Identities)
	}
	captureFree := map[string]bool{}
	for _, entry := range entries {
		if entry.CaptureProof.IsValid() {
			continue
		}
		counts.withoutCapture++
		name, named := nameByKind[entry.Event]
		if !named {
			t.Fatalf("the Codex activation manifest holds an entry for event kind %d that the registration manifest does not name, "+
				"so the capture state of that row cannot be crossed with its identities", uint16(entry.Event))
		}
		captureFree[name] = true
		if catalogueIdentities[name] > 0 {
			counts.captureFreeWithIdentity++
		}
	}
	for _, name := range counts.identityAbsent {
		if captureFree[name] {
			counts.identityAbsentWithoutCapture++
		}
	}
	return counts
}

// sameRows reports whether two derived row lists name the same rows. Both are
// sorted by the derivation, so order is meaning here and not an accident.
func sameRows(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// axisClause returns the sentence of a doc comment that OPENS with marker. A
// name pin runs against that sentence and never against the whole file: a pin
// that searched the file could accept a name written under the wrong axis.
func axisClause(text, marker string) (string, bool) {
	start := strings.Index(text, marker)
	if start < 0 {
		return "", false
	}
	sentence := text[start:]
	if end := strings.Index(sentence, ". "); end >= 0 {
		sentence = sentence[:end]
	}
	return sentence, true
}

// codexDocComments locates the source text read by the sentence pins.
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

// TestTheCodexDocCommentsStateTheDerivedCounts checks the sentences listed in
// pins against the populations computed by deriveCodexDivergenceCounts.
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
		{codexDocComments[0], catalogue, fmt.Sprintf("the committed manifest rendered from this source and that profile disagree on the failure mode of %d of the %d rows", counts.failureDiverged, counts.registered)},
		{codexDocComments[0], catalogue, fmt.Sprintf("and this source declares identities for %d of those %d", counts.captureFreeWithIdentity, counts.withoutCapture)},
		{codexDocComments[0], catalogue, fmt.Sprintf("is complete over all %d registered events", counts.registered)},
		{codexDocComments[1], frontend, fmt.Sprintf("the same failure mode on all %d of the %d registered events", counts.registered-counts.failureDiverged, counts.registered)},
		{codexDocComments[1], frontend, fmt.Sprintf("the profile declares on %d rows where the catalogue declares none", len(counts.identityAbsent))},
		{codexDocComments[1], frontend, fmt.Sprintf("and %d of those %d are events with no authentic capture", counts.identityAbsentWithoutCapture, len(counts.identityAbsent))},
	}
	for _, pin := range pins {
		if !strings.Contains(pin.text, pin.sentence) {
			t.Errorf("the committed file %s does not state the count the tree derives; it must carry the sentence %q. "+
				"A count in a doc comment is a claim about the tree: sweep it in the commit that moves it."+
				staleManifestFirst,
				pin.path, pin.sentence)
		}
	}

	// The axes the doc comments name row by row. The names are derived, so a
	// row that starts or stops diverging on one of them turns this RED naming
	// the file that must say so.
	//
	// Each name is held INSIDE the sentence about its own axis, not anywhere
	// in the comment. A name on the wrong axis must not satisfy this pin.
	axes := []struct {
		marker string
		axis   string
		names  []string
		detail string
	}{
		{"The gate-or-observation semantic differs on", "the gate-or-observation semantic", counts.semanticDiverged, ", an observation in the catalogue and a gate in the profile"},
		{"The mutation mode differs on", "the mutation mode", counts.mutationDiverged, ", which mutates the tool OUTPUT in the profile and has no output arm to be spelled with in the catalogue vocabulary"},
	}
	for position, named := range axes {
		clause, present := axisClause(frontend, named.marker)
		if !present {
			t.Errorf("the committed file %s must open its sentence about %s with %q; the row names are held inside THAT sentence, so the pin cannot run without it",
				codexDocComments[1], named.axis, named.marker)
			continue
		}
		wantClause := named.marker + " " + strings.Join(named.names, ", ") + named.detail
		if clause != wantClause {
			t.Errorf("the committed file %s must state the derived row list in its sentence about %s: want %q, got %q; an added or omitted name must not borrow a neighbouring pin", codexDocComments[1], named.axis, wantClause, clause)
		}
		for _, name := range named.names {
			if !strings.Contains(clause, name) {
				t.Errorf("the committed file %s does not name %q in its sentence about %s, and the tree says that row differs there. "+
					"The sentence it carries is %q. The pin reads that sentence and not the whole comment, so a name written under the wrong axis is RED here",
					codexDocComments[1], name, named.axis, clause)
			}
		}
		// A row that the tree says differs on the OTHER axis, and not on this
		// one, must not stand in this sentence. This is the half that catches a
		// name MOVED between the axes rather than dropped.
		other := axes[(position+1)%len(axes)]
		for _, name := range other.names {
			if contains(named.names, name) {
				continue
			}
			if strings.Contains(clause, name) {
				t.Errorf("the committed file %s names %q in its sentence about %s, and the tree says that row differs on %s instead. "+
					"The sentence it carries is %q; move the name to the sentence about the axis it belongs to",
					codexDocComments[1], name, named.axis, other.axis, clause)
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

	// Non-vacuity. The population must not be empty, and this clause is not a
	// statement about today's Codex citation or capture state: the first real
	// citation and the first authentic capture both move those states.
	if counts.registered == 0 {
		t.Fatalf("the derivation walked an empty Codex population, so these measurements cannot verify the catalogue; the derived Codex counts are %+v", counts)
	}

	// WHICH FIELD THE CATALOGUE READS CANNOT BE DECIDED HERE IN EVERY WORLD, and
	// a control that waited for the tree to be in the world where it can be is
	// silent in the world this work is heading for.
	//
	// THE MEASUREMENT. A cited row is a row the failure-evidence rule does NOT
	// move: the rule keeps the declared blocking arm while a citation stands, so
	// the effective arm and the declared arm are equal on that row. In a profile
	// where every declared Codex gate cites host evidence, the two arms are
	// equal on EVERY row, both candidate reads render the same manifest, and
	// nothing this subject compares can tell them apart. A control keyed on a
	// count of demoted or cited rows says nothing exactly there, and the count of
	// cited rows is a number the citation work is designed to raise.
	//
	// So the proof of WHICH field is read is CONSTRUCTED rather than observed,
	// and it lives beside the read: the row it needs holds two different arms
	// whatever the tree holds, so it is equally sharp with no Codex row cited,
	// with one cited and with every one cited. The pointer is guarded here, so a
	// renamed or deleted proof is RED rather than silent.
	const (
		fieldReadProofFile = "../ingress/internal/hostcontract/codex_0_153_0_test.go"
		fieldReadProofTest = "TestTheCodexFailureReadTakesTheEvidenceBoundArmAndNotTheDeclaredOne"
	)
	proof, err := os.ReadFile(fieldReadProofFile)
	if err != nil {
		t.Fatalf("the constructed proof that the Codex catalogue reads the evidence-bound failure arm cannot be read at %q: %v. "+
			"This subject compares two artefacts and cannot tell the two candidate reads apart once every Codex row cites host evidence, "+
			"so that proof is the only one that holds in every citation state", fieldReadProofFile, err)
	}
	if !strings.Contains(string(proof), "func "+fieldReadProofTest+"(") {
		t.Fatalf("the file %q no longer declares %s. That test builds a row whose declared arm and evidence-bound arm DIFFER, "+
			"and it is what proves the Codex catalogue reads the arm the evidence rule produced. This subject cannot prove it: "+
			"once every Codex row cites host evidence the two arms are equal on every row and both reads render the same manifest. "+
			"Restore that test, or move the constructed proof and name it here", fieldReadProofFile, fieldReadProofTest)
	}
	if counts.failureDiverged != 0 || counts.overClaiming != 0 {
		t.Errorf("the committed Codex registration manifest and the runtime profile state a different failure mode on %d rows, %d of them claiming a blocking exit code the profile does not hold, want none. "+
			"The value compared here is the COMMITTED internal/lifecycle/registration/codex_0_153_0.gen.go, and the source it is rendered from, "+
			"internal/lifecycle/ingress/internal/hostcontract/codex_0_153_0.go, READS this field from internal/runtime/lifecycle_profiles_codex.go. "+
			"First rule out a stale manifest: run make generate. If the difference remains, inspect the source catalogue for a hand-written arm and restore the read in codexFailureReaderOver before regenerating",
			counts.failureDiverged, counts.overClaiming)
	}
	// The blocking population, DERIVED on both sides. The catalogue's blocking
	// rows are the OUTPUT of the evidence rule and the profile's cited gates are
	// its INPUT, so the two lists must name the same rows whatever the citation
	// state is after generation. Regenerate after a citation changes before
	// interpreting a difference as a defect in the source catalogue.
	if !sameRows(counts.catalogueGateRows, counts.evidencedGateRows) {
		t.Errorf("the committed Codex registration manifest states a blocking exit code on %d of its %d rows %v, and the runtime profile holds %d rows that declare a blocking exit code AND cite host evidence for it %v; "+
			"these two lists must name the same rows, because the manifest is rendered from a source catalogue that READS the arm the evidence rule produced, and the rule keeps a blocking arm only where a citation stands. "+
			"The profile demotes %d declared gates for want of a citation. "+
			"First rule out a stale manifest: run make generate. A citation added to or taken from "+
			"internal/runtime/lifecycle_profiles_codex.go moves the SECOND list at once and the FIRST list only after generation, so the two lists part until the generator runs. "+
			"If the difference remains, inspect internal/lifecycle/ingress/internal/hostcontract/codex_0_153_0.go for a hand-written arm: restore the read in codexFailureReaderOver, then run make generate. "+
			"After ruling that out, a row in the manifest list only is a refusal the product cannot perform, and a row in the profile list only is an arm the manifest has not taken up",
			len(counts.catalogueGateRows), counts.registered, counts.catalogueGateRows,
			len(counts.evidencedGateRows), counts.evidencedGateRows, counts.demotedGates)
	}
}

// contains reports whether names holds one name.
func contains(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
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
			why:  "aligning the semantic needs the host emission site read, not a failure-mode read; failure-arm agreement does not establish semantic agreement",
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
			t.Errorf("the two Codex artefacts disagree on %s for %d rows %v, want exactly %d %v; %s."+
				staleManifestFirst,
				axis.name, len(axis.got), axis.got, len(axis.want), axis.want, axis.why)
			continue
		}
		for index := range axis.want {
			if axis.got[index] != axis.want[index] {
				t.Errorf("the two Codex artefacts disagree on %s for rows %v, want %v; %s."+
					staleManifestFirst,
					axis.name, axis.got, axis.want, axis.why)
				break
			}
		}
	}
}
