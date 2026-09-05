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

	// ARMS CONTROL. It is the one non-vacuity control the subject keeps, and its
	// message is true in EVERY state it can fire in.
	//
	// A control that demanded a DEMOTED gate stood here. It is gone, for two
	// measured reasons. It would turn RED on the day every Codex row cites host
	// evidence, and it would then say the case is absent when the case has only
	// changed direction. And it could never fire on its own: a row is demoted or
	// promoted only if it is BLOCKING, a non-blocking row always carries the
	// strict-hook arm, so a tree with no moved row carries ONE arm and this
	// control fails first. The other two checks cover what it claimed to: a read
	// that returned the DECLARED field turns the row equality above RED naming
	// the row, and a read that returned a constant turns this RED.
	require.Greaterf(t, len(arms), 1,
		"every Codex catalog row carries the same failure arm %v; a read that returned one constant would satisfy every check above, so this control fails first",
		arms)
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
