package hostexit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const citedSource = "https://docs.claude.com/en/docs/claude-code/hooks"

func allFailureModes() []pastureruntime.FailureMode {
	return []pastureruntime.FailureMode{
		pastureruntime.FailureReportAndContinue,
		pastureruntime.FailureExitTwoBlocks,
		pastureruntime.FailureStrictHook,
		pastureruntime.FailureStrictExitTwoBlocks,
		pastureruntime.FailureThrowFailFast,
		pastureruntime.FailureObserveOnly,
	}
}

// TestForFaultCoversEveryModePolicyAndEvidenceCell is the whole fault table:
// six failure modes x two fault policies x {evidence, none} = 24 cells.
//
// Exactly two cells block. They are the two exit-code arms, under the opt-in
// fail-closed policy, with host evidence for the blocking claim. Every other
// cell lets the host continue and says why on stderr. A fault must not stop a
// user working unless the user asked for that AND the host is documented to
// block on the exit code.
func TestForFaultCoversEveryModePolicyAndEvidenceCell(t *testing.T) {
	t.Parallel()

	fault := errors.New("the task store refused the write while handling PreToolUse")

	type cell struct {
		mode     pastureruntime.FailureMode
		policy   hostexit.FaultPolicy
		evidence pastureruntime.FailureEvidence
	}
	cells := []cell{}
	for _, mode := range allFailureModes() {
		for _, policy := range []hostexit.FaultPolicy{hostexit.FaultFailOpen, hostexit.FaultFailClosed} {
			for _, evidence := range []pastureruntime.FailureEvidence{
				{Source: citedSource},
				{},
			} {
				cells = append(cells, cell{mode: mode, policy: policy, evidence: evidence})
			}
		}
	}
	require.Len(t, cells, 24, "the table must cover 6 modes x 2 policies x 2 evidence states")

	blocked := 0
	for _, c := range cells {
		c := c
		name := c.mode.String() + "/" + c.policy.String() + "/evidence=" + evidenceName(c.evidence)
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			outcome, ok := hostexit.ForFault(c.mode, c.evidence, c.policy, fault)
			require.True(t, ok, "a real fault with a declared mode and policy must map")

			wantBlock := c.policy == hostexit.FaultFailClosed &&
				c.mode.BlocksByExitCode() &&
				c.evidence.IsPresent()

			if wantBlock {
				assert.Equal(t, hostexit.ExitBlock, outcome.Exit,
					"an evidenced blocking mode under fail-closed must refuse the operation")
			} else {
				assert.Equal(t, hostexit.ExitContinue, outcome.Exit,
					"every other cell must let the host continue")
			}
			assert.Empty(t, outcome.Stdout,
				"a fault never writes a native continuation, because pasture did not evaluate the event")
			assert.NotEmpty(t, outcome.Stderr, "a fault is never silent")
		})
		if c.policy == hostexit.FaultFailClosed && c.mode.BlocksByExitCode() && c.evidence.IsPresent() {
			blocked++
		}
	}
	assert.Equal(t, 2, blocked,
		"exactly two of the 24 cells may block: the two exit-code arms, under fail-closed, with evidence")
}

// TestForFaultRefusesWhenThereIsNothingToMap pins the false result. A false
// result is a programming error at the call site, so it must be distinguishable
// from a real continue outcome; a caller that reads it as "exit 0" would
// recreate the silent proceed.
func TestForFaultRefusesWhenThereIsNothingToMap(t *testing.T) {
	t.Parallel()

	fault := errors.New("boom")
	evidence := pastureruntime.FailureEvidence{Source: citedSource}

	tests := []struct {
		name     string
		mode     pastureruntime.FailureMode
		policy   hostexit.FaultPolicy
		err      error
		evidence pastureruntime.FailureEvidence
	}{
		{name: "nil error", mode: pastureruntime.FailureExitTwoBlocks, policy: hostexit.FaultFailClosed, err: nil, evidence: evidence},
		{name: "unset mode", mode: 0, policy: hostexit.FaultFailClosed, err: fault, evidence: evidence},
		{name: "mode above the last arm", mode: pastureruntime.FailureObserveOnly + 1, policy: hostexit.FaultFailOpen, err: fault, evidence: evidence},
		{name: "unset policy", mode: pastureruntime.FailureExitTwoBlocks, policy: hostexit.FaultPolicyUnset, err: fault, evidence: evidence},
		{name: "policy above the last arm", mode: pastureruntime.FailureExitTwoBlocks, policy: hostexit.FaultFailClosed + 1, err: fault, evidence: evidence},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			outcome, ok := hostexit.ForFault(test.mode, test.evidence, test.policy, test.err)
			assert.False(t, ok, "there is nothing to map, so the result must be refused")
			assert.Equal(t, hostexit.ExitStatusUnset, outcome.Exit,
				"a refused mapping must not hand back a usable exit status")
		})
	}
}

// TestFaultDiagnosticIsActionable pins the six parts a reader needs: what went
// wrong, why it matters, where in pasture it was decided, when in the hook it
// happened, what it means for the host, and how to change it. It also pins that
// the cause survives verbatim, because the cause is what names the event and
// the failing step.
func TestFaultDiagnosticIsActionable(t *testing.T) {
	t.Parallel()

	cause := errors.New("the task store refused the write while handling PreToolUse")

	t.Run("fail open", func(t *testing.T) {
		t.Parallel()
		outcome, ok := hostexit.ForFault(
			pastureruntime.FailureExitTwoBlocks,
			pastureruntime.FailureEvidence{Source: citedSource},
			hostexit.FaultFailOpen,
			cause,
		)
		require.True(t, ok)
		assertActionable(t, outcome.Stderr, cause)
		assert.Contains(t, outcome.Stderr, "the host continues",
			"the reader must be told the operation was not stopped")
		assert.Contains(t, outcome.Stderr, "PASTURE_HOOK_FAIL_CLOSED=1",
			"the reader must be told how to make this stop the host instead")
	})

	t.Run("fail closed and blocking", func(t *testing.T) {
		t.Parallel()
		outcome, ok := hostexit.ForFault(
			pastureruntime.FailureExitTwoBlocks,
			pastureruntime.FailureEvidence{Source: citedSource},
			hostexit.FaultFailClosed,
			cause,
		)
		require.True(t, ok)
		require.Equal(t, hostexit.ExitBlock, outcome.Exit)
		assertActionable(t, outcome.Stderr, cause)
		assert.Contains(t, outcome.Stderr, "the host refuses the operation",
			"the reader must be told the operation was stopped")
		assert.Contains(t, outcome.Stderr, "unset PASTURE_HOOK_FAIL_CLOSED",
			"the reader must be told how to let the host continue instead")
	})
}

func assertActionable(t *testing.T, stderr string, cause error) {
	t.Helper()

	for _, part := range []string{"what: ", "why: ", "where: ", "phase: ", "impact: ", "fix: "} {
		assert.Contains(t, stderr, part, "the diagnostic must carry the %q part", strings.TrimSuffix(part, ": "))
	}
	assert.Contains(t, stderr, "internal/lifecycle/hostexit.ForFault",
		"the reader must be told WHERE in pasture the outcome was decided")
	assert.Contains(t, stderr, "hook lifecycle fault handling",
		"the reader must be told WHEN in the hook the fault happened")
	assert.Contains(t, stderr, cause.Error(),
		"the cause names the event and the failing step, so it must survive verbatim")

	for _, internal := range []string{"SLICE-", "PROPOSAL-", "aura-plugins-"} {
		assert.NotContains(t, stderr, internal,
			"host-visible text must not carry an internal process reference")
	}
}

// TestExitStatusCodesAreTheHostContract pins the three process exit codes. The
// numbers are the whole contract with the host: 0 proceeds, 1 reports, 2
// blocks. The unset zero value has NO code, so a forgotten decision cannot
// become a silent exit 0.
func TestExitStatusCodesAreTheHostContract(t *testing.T) {
	t.Parallel()

	for status, want := range map[hostexit.ExitStatus]int{
		hostexit.ExitContinue:         0,
		hostexit.ExitNonBlockingError: 1,
		hostexit.ExitBlock:            2,
	} {
		code, ok := status.Code()
		assert.True(t, ok, "declared status %q must have a process exit code", status)
		assert.Equal(t, want, code, "process exit code of %q", status)
		assert.True(t, status.IsValid())
	}

	code, ok := hostexit.ExitStatusUnset.Code()
	assert.False(t, ok, "the unset status must have no process exit code")
	assert.Equal(t, 0, code)
	assert.False(t, hostexit.ExitStatusUnset.IsValid())
	assert.Empty(t, hostexit.ExitStatusUnset.String())
	assert.False(t, (hostexit.ExitBlock + 1).IsValid())
}

// TestForDecisionIsNotRejudged pins the split between an answer and a fault. An
// evaluated Deny is carried through unchanged: it does not consult the fault
// policy and it does not consult the failure evidence, so a missing citation
// or the fail-open default can never turn a Deny into a proceed.
func TestForDecisionIsNotRejudged(t *testing.T) {
	t.Parallel()

	native := []byte(`{"decision":"block","reason":"the assignment does not cover this file"}`)
	outcome := hostexit.ForDecision(native, hostexit.ExitBlock, "the assignment does not cover this file")

	assert.Equal(t, hostexit.ExitBlock, outcome.Exit)
	assert.Equal(t, native, outcome.Stdout)
	assert.Equal(t, "the assignment does not cover this file", outcome.Stderr)

	proceed := hostexit.ForDecision(nil, hostexit.ExitContinue, "")
	assert.Equal(t, hostexit.ExitContinue, proceed.Exit)
	assert.Empty(t, proceed.Stdout, "a Claude proceed writes no bytes to stdout")
	assert.Empty(t, proceed.Stderr)
}

// TestFaultPolicyZeroValueRefuses pins the policy enum. An unset policy is a
// forgotten decision, not a default, because a default hidden in a zero value
// is invisible at the call site.
func TestFaultPolicyZeroValueRefuses(t *testing.T) {
	t.Parallel()

	assert.False(t, hostexit.FaultPolicyUnset.IsValid())
	assert.Empty(t, hostexit.FaultPolicyUnset.String())
	assert.True(t, hostexit.FaultFailOpen.IsValid())
	assert.True(t, hostexit.FaultFailClosed.IsValid())
	assert.False(t, (hostexit.FaultFailClosed + 1).IsValid())
	assert.Equal(t, "fail-open", hostexit.FaultFailOpen.String())
	assert.Equal(t, "fail-closed", hostexit.FaultFailClosed.String())
}

func evidenceName(evidence pastureruntime.FailureEvidence) string {
	if evidence.IsPresent() {
		return "cited"
	}
	return "none"
}
