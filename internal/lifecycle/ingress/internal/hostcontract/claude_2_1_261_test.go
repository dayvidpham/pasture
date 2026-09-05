package hostcontract_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// claudeBlockingModeEvidence is one row of the host evidence that decides the
// blocking mode of one Claude Code event. The host text is the exit-code
// paragraph the installed Claude Code 2.1.261 binary carries for that event in
// its own hook-event table (~/.local/share/claude/versions/2.1.261). A row that
// blocks quotes the sentence that says so; a row that does not block quotes the
// sentence that says the host only shows stderr.
type claudeBlockingModeEvidence struct {
	blocking hostcontract.BlockingMode
	failure  runtime.FailureMode
	hostText string
}

// claudeModelSwitchAndDirectoryEvidence is the evidence for the three events
// Claude Code 2.1.261 added over 2.1.210. They are registered without an
// authentic capture, so the host's own text is the only authority for their
// blocking mode, and this table is where a later reader re-checks it.
var claudeModelSwitchAndDirectoryEvidence = map[string]claudeBlockingModeEvidence{
	"PreModelSwitch": {
		blocking: hostcontract.Blocking,
		failure:  runtime.FailureExitTwoBlocks,
		hostText: "Exit code 2 - block the switch and show stderr to user",
	},
	"PostModelSwitch": {
		blocking: hostcontract.NonBlocking,
		failure:  runtime.FailureReportAndContinue,
		hostText: "Other exit codes - show stderr to user only",
	},
	"DirectoryAdded": {
		blocking: hostcontract.NonBlocking,
		failure:  runtime.FailureReportAndContinue,
		hostText: "Other exit codes - stderr is debug-logged on both paths",
	},
}

// TestClaudeRowsDeclareTheBlockingModeTheHostStates holds every Claude Code
// 2.1.261 catalogue row whose blocking mode was read from the installed binary
// against the host sentence that decided it. The catalogue is the admission
// authority the hook handler reads, so a row that declares the wrong blocking
// mode is a claim about somebody else's program that the cited source denies.
//
// The population is the evidence table above; the control below keeps it from
// passing on an empty intersection.
func TestClaudeRowsDeclareTheBlockingModeTheHostStates(t *testing.T) {
	t.Parallel()

	contract := hostcontract.ClaudeCode2_1_261()
	checked := 0
	for _, event := range contract.Events {
		evidence, ok := claudeModelSwitchAndDirectoryEvidence[event.Name]
		if !ok {
			continue
		}
		checked++
		require.Equalf(t, evidence.blocking, event.Blocking,
			"Claude Code 2.1.261 event %q declares the wrong blocking mode: the installed binary's own hook-event table says %q",
			event.Name, evidence.hostText)
		require.Equalf(t, evidence.failure, event.Failure,
			"Claude Code 2.1.261 event %q declares the wrong failure mode: the installed binary's own hook-event table says %q",
			event.Name, evidence.hostText)
	}
	require.Equal(t, len(claudeModelSwitchAndDirectoryEvidence), checked,
		"every event of the evidence table must be a row of the Claude Code 2.1.261 catalogue; a name that is not a row proves nothing")
}

// TestClaudeBlockingRowsCiteTheirEvidence holds the failure-evidence rule on
// the Claude runtime profile: a row that keeps a blocking exit code cites the
// source it was read from, and a row with no evidence never keeps one. Without
// this, the catalogue could declare a gate that the profile silently demotes.
func TestClaudeBlockingRowsCiteTheirEvidence(t *testing.T) {
	t.Parallel()

	cited := 0
	for name, evidence := range claudeModelSwitchAndDirectoryEvidence {
		policy, ok := runtime.LookupLifecycleFailure(ir.HarnessClaudeCode, name)
		require.Truef(t, ok, "the Claude runtime profile carries no row for %q", name)
		if evidence.failure != runtime.FailureExitTwoBlocks {
			require.Falsef(t, policy.Mode.BlocksByExitCode(),
				"Claude Code 2.1.261 event %q must not block by exit code: the installed binary's own hook-event table says %q",
				name, evidence.hostText)
			continue
		}
		cited++
		require.Truef(t, policy.Mode.BlocksByExitCode(),
			"Claude Code 2.1.261 event %q must block by exit code: the installed binary's own hook-event table says %q",
			name, evidence.hostText)
		require.Truef(t, policy.Evidence.IsPresent(),
			"Claude Code 2.1.261 event %q blocks by exit code and must cite where that was read: the installed binary's own hook-event table says %q",
			name, evidence.hostText)
		require.Containsf(t, policy.Evidence.Source, evidence.hostText,
			"the citation of Claude Code 2.1.261 event %q must quote the host sentence that decided it", name)
	}
	require.Positive(t, cited, "at least one row of the evidence table must block by exit code, or this test proves nothing")
}
