package hostcontract

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

// This file is one subject: THE CODEX CATALOG READS ITS FAILURE MODE FROM THE
// RUNTIME PROFILE, AND READS NOTHING ELSE FROM IT.
//
// The catalog is what a maintainer reads to learn what the Codex host does, and
// what code generation writes into the registration manifest. The runtime
// profile is what every behavior obeys. While the failure mode was written in
// both, the catalog claimed a blocking exit code on every gate row and the
// profile ran all of those rows as report-and-continue, because a blocking exit
// code needs a citation and the Codex rows carried none.
//
// Nothing below states how many Codex rows cite host evidence. That number
// MOVES: a citation promotes a row and the catalog follows it. Every check here
// reads the state it is about, so the first citation keeps this file green.

// TestEveryCodexCatalogRowCarriesTheRuntimeFailureMode walks EVERY row of the
// catalog, taken from the contract itself so a row added later is covered with
// no edit here, and requires the failure mode to be the one the runtime profile
// holds. It reaches the profile through the same by-name lookup the rest of the
// tree uses, not through the constructor the catalog reads, so a catalog that
// went back to a hand-written arm turns this RED naming the row.
//
// TWO controls keep it from passing on nothing. WIDTH: the catalog must name the
// same rows as the profile it reads from, so a catalog that LOST a row cannot
// pass by never being asked about it. ARMS: the rows must carry more than one
// distinct arm, which a constant read could not produce.
func TestEveryCodexCatalogRowCarriesTheRuntimeFailureMode(t *testing.T) {
	t.Parallel()

	contract := Codex0_153_0()
	require.NotEmpty(t, contract.Events, "the Codex catalog declares no row, so nothing below is checked")

	// WIDTH CONTROL. The derivation looks each catalog row up in the profile BY
	// NAME, so a row the catalog dropped is simply never asked about and every
	// check below stays green while the two artefacts describe different
	// populations. Holding the two row sets equal is the other half of the
	// invariant this subject exists for.
	catalogRows := make([]string, 0, len(contract.Events))
	for _, event := range contract.Events {
		catalogRows = append(catalogRows, event.Name)
	}
	profileContract := pastureruntime.Codex0_153_0Lifecycle()
	profileRows := make([]string, 0, len(pastureruntime.CodexLifecycleEvents()))
	for _, event := range pastureruntime.CodexLifecycleEvents() {
		mapping, err := profileContract.Mapping(event)
		require.NoErrorf(t, err, "the runtime Codex profile holds no mapping for its own event %v, so its row set cannot be measured", event)
		profileRows = append(profileRows, mapping.NativeName())
	}
	sort.Strings(catalogRows)
	sort.Strings(profileRows)
	require.Equalf(t, profileRows, catalogRows,
		"the Codex catalog names %d rows and the runtime Codex profile names %d, and the two sets differ; "+
			"catalog rows %v, profile rows %v. A row MISSING FROM THE CATALOG is never looked up, so every check below would pass while the two artefacts describe different populations. "+
			"A row MISSING FROM THE PROFILE stops the catalog being built at all. Add the row to whichever side lacks it, in "+
			"internal/lifecycle/ingress/internal/hostcontract/codex_0_153_0.go or internal/runtime/lifecycle_profiles_codex.go",
		len(catalogRows), len(profileRows), catalogRows, profileRows)

	arms := map[pastureruntime.FailureMode]int{}
	for _, event := range contract.Events {
		policy, declared := pastureruntime.LookupLifecycleFailure(ir.HarnessCodex, event.Name)
		require.Truef(t, declared,
			"the Codex catalog declares the row %q and the runtime profile declares no row of that native name, so this by-name lookup answers for nothing. "+
				"Codex0_153_0 refuses that state first, so reaching this line means the by-name lookup and the catalog's own read disagree about the profile", event.Name)
		require.Equalf(t, policy.Mode, event.Failure,
			"the Codex catalog row %q states the failure mode %v and the runtime profile holds %v; "+
				"the catalog must READ this field from the profile, because a mode written twice can be demoted in one artefact and kept in the other",
			event.Name, event.Failure, policy.Mode)
		arms[event.Failure]++
		if policy.DeclaredMode.BlocksByExitCode() && !policy.Mode.BlocksByExitCode() {
			require.Falsef(t, event.Failure.BlocksByExitCode(),
				"the Codex catalog row %q claims a blocking exit code that the evidence rule took away from the runtime profile; "+
					"a reader of the catalog would learn a refusal the product cannot perform", event.Name)
		}
	}

	// ARMS CONTROL, and its role is narrower than an earlier wording claimed.
	// MEASURED, in all three citation worlds: a read that returns one CONSTANT
	// is caught by the row equality above, which fails first and stops the test,
	// so this control never fires on that break. What it does catch is a
	// DEGENERATE PROFILE: a profile that gives every Codex row the same arm. The
	// row equality cannot tell a read from a constant there, because every
	// answer is the same value, and this control is what says so.
	//
	// A control that demanded a DEMOTED gate stood here. It is gone, for two
	// measured reasons. It would turn RED on the day every Codex row cites host
	// evidence, and it would then say the case is absent when the case has only
	// changed direction. And it could never fire on its own: a row is demoted or
	// promoted only if it is BLOCKING, a non-blocking row always carries the
	// strict-hook arm, so a tree with no moved row carries ONE arm and this
	// control fails first. It becomes reachable again if a later change gives a
	// NON-blocking row a demotable mode, and it should return then.
	require.Greaterf(t, len(arms), 1,
		"every Codex catalog row carries the same failure arm %v, so the runtime Codex profile gives every row one arm. "+
			"The row equality above cannot tell a read from a constant in that profile, because every answer is the same value. "+
			"Widen the profile in internal/runtime/lifecycle_profiles_codex.go, or say here why one arm is now correct",
		arms)
}

// constructedCodexRow is a runtime Codex row BUILT FOR THIS TEST, not read from
// the profile. It is the whole point of the control below: the state that tells
// a right read from a wrong one is a row whose two failure arms DIFFER, and the
// tree holds such a row only while some declared Codex gate cites no host
// evidence. That is a state the product is designed to leave. A control that
// waited for it would go quiet on the day every Codex row is cited, which is
// the day the catalogue's central claim most needs a guard.
type constructedCodexRow struct {
	name     string
	failure  pastureruntime.FailureMode
	declared pastureruntime.FailureMode
}

func (r constructedCodexRow) NativeName() string                          { return r.name }
func (r constructedCodexRow) Failure() pastureruntime.FailureMode         { return r.failure }
func (r constructedCodexRow) DeclaredFailure() pastureruntime.FailureMode { return r.declared }

// TestTheCodexFailureReadTakesTheEvidenceBoundArmAndNotTheDeclaredOne CONSTRUCTS
// the state that discriminates instead of waiting for the tree to be in it.
//
// The catalogue must take the arm the failure-evidence rule PRODUCED and never
// the arm the profile row DECLARES before that rule runs. A declared arm claims
// a refusal no code path performs, and a reader of the catalogue then believes
// pasture can stop a user's action where it cannot.
//
// WHY THIS CANNOT BE ASKED OF THE LIVE PROFILE. The two arms differ only on a
// row the rule MOVED, which is a declared gate that cites no host evidence. A
// cited row carries the same value in both arms. So in a fully cited profile
// every comparison of the two artefacts passes under EITHER read, and no test
// that reads the tree can tell them apart. The row below is built here, so this
// control is equally sharp with no Codex row cited, with one cited, and with
// every one cited.
func TestTheCodexFailureReadTakesTheEvidenceBoundArmAndNotTheDeclaredOne(t *testing.T) {
	t.Parallel()

	row := constructedCodexRow{
		name:     "ConstructedDemotedCodexGate",
		failure:  pastureruntime.FailureStrictHook,
		declared: pastureruntime.FailureStrictExitTwoBlocks,
	}
	require.NotEqualf(t, row.declared, row.failure,
		"the constructed row must hold two DIFFERENT arms, or this control cannot tell the evidence-bound read from the declared one; it holds %v twice",
		row.failure)

	got := codexFailureReaderOver([]codexRuntimeRow{row})(row.name)
	require.Equalf(t, row.failure, got,
		"the Codex catalogue read answered %v for a row whose evidence-bound arm is %v and whose declared arm is %v. "+
			"The catalogue must take the arm the failure-evidence rule PRODUCED, because a declared blocking arm claims a refusal the product cannot perform. "+
			"The row is built in this test and not read from the tree, so this holds whether or not any live Codex row cites host evidence. "+
			"Repair the read in codexFailureReaderOver in internal/lifecycle/ingress/internal/hostcontract/codex_0_153_0.go",
		got, row.failure, row.declared)
}

// TestEveryCodexCatalogRowTakesItsFailureArmFromTheRead proves that the catalog
// is a FUNCTION of the rows it reads, and it constructs the case rather than
// waiting for the tree to hold it.
//
// WHY THE ROW-BY-ROW EQUALITY ABOVE IS NOT ENOUGH. That check compares the
// catalog with the live profile. A row that carries a HAND-WRITTEN arm passes it
// whenever the value written by hand equals the value the read would answer, and
// in a profile where every declared gate cites host evidence that is true of
// every blocking row at once. MEASURED: with every Codex row cited and every
// gate row given a hand-written blocking arm, the row equality, the arms
// control, the divergence subject and the two doc-comment subjects all report
// ok. Nothing sees it.
//
// THE CONSTRUCTED CASE. The catalog is built TWICE over the same row names with
// DIFFERENT arms, and every row must follow the arm it was built over. No
// literal can satisfy both builds, whatever value somebody writes, and no
// citation state changes that.
func TestEveryCodexCatalogRowTakesItsFailureArmFromTheRead(t *testing.T) {
	t.Parallel()

	first := pastureruntime.FailureStrictHook
	second := pastureruntime.FailureStrictExitTwoBlocks
	require.NotEqualf(t, first, second,
		"the two constructed builds must use DIFFERENT arms, or a hand-written literal satisfies both and this control proves nothing; both are %v", first)

	names := make([]string, 0, len(pastureruntime.CodexLifecycleEvents()))
	for _, row := range codexProfileRows() {
		names = append(names, row.NativeName())
	}
	require.NotEmpty(t, names, "the runtime Codex profile names no row, so the catalog cannot be built over a constructed row set")

	for _, arm := range []pastureruntime.FailureMode{first, second} {
		rows := make([]codexRuntimeRow, 0, len(names))
		for _, name := range names {
			rows = append(rows, constructedCodexRow{name: name, failure: arm, declared: arm})
		}
		contract := codex0_153_0Over(rows)
		require.Lenf(t, contract.Events, len(names),
			"the catalog built over %d constructed rows declares %d events, so some row was not asked for its arm", len(names), len(contract.Events))
		for _, event := range contract.Events {
			require.Equalf(t, arm, event.Failure,
				"the Codex catalog row %q carries the failure arm %v while the read answers %v for EVERY row of this build. "+
					"That row does not take its arm from the read: it carries an arm written into internal/lifecycle/ingress/internal/hostcontract/codex_0_153_0.go by hand. "+
					"A hand-written arm is invisible to a comparison with the live profile whenever the value written happens to equal what the read would answer, "+
					"which is true of every blocking row at once once every declared Codex gate cites host evidence. "+
					"Give that row Failure: failure(name), as the observe and gate builders do",
				event.Name, event.Failure, arm)
		}
	}
}

// TestTheCodexFailureReaderRefusesAnEventTheProfileDoesNotDeclare pins the
// production diagnostic of the read. The catalog cannot return an error, so a
// name the profile does not hold stops the build of the contract; the message
// must name the event and both files, because the repair is in one of them.
//
// It also pins the COST the message states, and the cost is measured from the
// import graph: "go list -deps ./cmd/..." names this package zero times, with
// and without the recovery build tag, so no pasture binary links it and a
// refusal here stops code generation and NEVER admission. A Codex hook is still
// admitted from the committed generated manifest. A message that told the
// reader the product was down for Codex would send that person to look for an
// outage that is not there, so the phrase is held here.
//
// It ALSO pins that the message names no exclusive caller. FOUR packages build
// this catalog in their tests, so a driven refusal reddens several packages in
// one run. While the message called the generator the only caller outside this
// package's own tests, a reader of one of the other red packages was told by
// the message itself that it was not a caller, and had to decide whether that
// red was a second, separate defect. The retired exclusivity phrasings are
// refused below, so the class cannot come back in a new wording.
func TestTheCodexFailureReaderRefusesAnEventTheProfileDoesNotDeclare(t *testing.T) {
	t.Parallel()

	read := codexFailureReader()
	require.NotPanics(t, func() { read("SessionStart") },
		"the runtime profile holds SessionStart, so the read must answer for it")

	var message string
	func() {
		defer func() {
			raised := recover()
			require.NotNil(t, raised, "a name the runtime profile does not declare must stop the build of the contract")
			text, ok := raised.(string)
			require.True(t, ok, "the refusal must carry a message a reader can act on")
			message = text
		}()
		read("NoSuchCodexEvent")
	}()

	for _, phrase := range []string{
		`"NoSuchCodexEvent"`,
		"internal/lifecycle/ingress/internal/hostcontract/codex_0_153_0.go",
		"internal/runtime/lifecycle_profiles_codex.go",
		"code generation stops here and admission does not",
		"a Codex hook is still admitted from the committed internal/lifecycle/registration/codex_0_153_0.gen.go",
		"Anything else that builds this catalog stops with this same message, so expect more than one red package in one run",
		"then run make generate",
	} {
		require.Containsf(t, message, phrase,
			"the refusal must carry %q, so the reader learns which event failed, where it failed, what it costs and where the repair goes", phrase)
	}
	for _, retired := range []string{
		"the only caller of this catalog",
		"exactly one caller",
		"one caller outside its own tests",
	} {
		require.NotContainsf(t, message, retired,
			"the refusal must not name an exclusive caller, and it carries the retired phrase %q. FOUR packages build this catalog in their tests, "+
				"so a driven refusal reddens several packages in one run; a reader of one of the others is then told by this message that it is not a caller, "+
				"and goes hunting a cause that is not there. State the cost and say that several packages go red, and count no callers", retired)
	}
	require.NotContains(t, message, "no Codex hook can be admitted",
		"the refusal must not claim admission stops: no pasture binary links this package, and the Codex hook path admits with the committed generated manifest, "+
			"so a reader who believes that phrase looks for an outage that is not there")
	require.False(t, strings.Contains(message, "invalid"),
		"the refusal must describe the state it found, not label it")
}
