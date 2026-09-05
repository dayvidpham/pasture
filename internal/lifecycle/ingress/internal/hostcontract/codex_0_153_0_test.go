package hostcontract

import (
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
// code needs a citation and no Codex row carries one yet.

// TestEveryCodexCatalogRowCarriesTheRuntimeFailureMode walks EVERY row of the
// catalog, taken from the contract itself so a row added later is covered with
// no edit here, and requires the failure mode to be the one the runtime profile
// holds. It reaches the profile through the same by-name lookup the rest of the
// tree uses, not through the constructor the catalog reads, so a catalog that
// went back to a hand-written arm turns this RED naming the row.
//
// Two controls keep it from passing on nothing: the rows must carry more than
// one distinct arm, which a constant could not do, and at least one row must be
// a gate the evidence rule demoted, which is the exact case that used to
// diverge.
func TestEveryCodexCatalogRowCarriesTheRuntimeFailureMode(t *testing.T) {
	t.Parallel()

	contract := Codex0_153_0()
	require.NotEmpty(t, contract.Events, "the Codex catalog declares no row, so nothing below is checked")

	arms := map[pastureruntime.FailureMode]int{}
	demotedGates := 0
	for _, event := range contract.Events {
		policy, declared := pastureruntime.LookupLifecycleFailure(ir.HarnessCodex, event.Name)
		require.Truef(t, declared,
			"the Codex catalog declares the row %q and the runtime profile declares no row of that native name, "+
				"so nothing governs the behavior of that event", event.Name)
		require.Equalf(t, policy.Mode, event.Failure,
			"the Codex catalog row %q states the failure mode %v and the runtime profile holds %v; "+
				"the catalog must READ this field from the profile, because a mode written twice can be demoted in one artefact and kept in the other",
			event.Name, event.Failure, policy.Mode)
		arms[event.Failure]++
		if policy.DeclaredMode.BlocksByExitCode() && !policy.Mode.BlocksByExitCode() {
			demotedGates++
			require.Falsef(t, event.Failure.BlocksByExitCode(),
				"the Codex catalog row %q claims a blocking exit code that the evidence rule took away from the runtime profile; "+
					"a reader of the catalog would learn a refusal the product cannot perform", event.Name)
		}
	}

	require.Greaterf(t, len(arms), 1,
		"every Codex catalog row carries the same failure arm %v; a read that returned one constant would satisfy every check above, so this control fails first",
		arms)
	require.Positivef(t, demotedGates,
		"no Codex row is a gate the evidence rule demoted, so the case this subject exists for is not present and the checks above prove nothing; "+
			"the arms measured are %v", arms)
}

// TestTheCodexFailureReaderRefusesAnEventTheProfileDoesNotDeclare pins the
// production diagnostic of the read. The catalog cannot return an error, so a
// name the profile does not hold stops the build of the contract; the message
// must name the event and both files, because the repair is in one of them.
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
		"no Codex manifest can be generated and no Codex hook can be admitted",
	} {
		require.Containsf(t, message, phrase,
			"the refusal must carry %q, so the reader learns which event failed, where it failed, what it costs and where the repair goes", phrase)
	}
	require.False(t, strings.Contains(message, "invalid"),
		"the refusal must describe the state it found, not label it")
}
