package hostexit_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const citedSource = "https://docs.claude.com/en/docs/claude-code/hooks"

// openCodeProceed is the byte body the pasture-generated OpenCode plugin
// accepts as a proceed. It is written here as a literal on purpose: this
// package must stay pure and must not import the harness table, so the table
// test proves the bytes it is GIVEN reach the host untouched.
var openCodeProceed = []byte(`{"decision":"proceed"}`)

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
// cell lets the host continue, EMITS THE HOST'S CONTINUE BYTES, and says why on
// stderr. A fault must not stop a user working unless the user asked for that
// AND the host is documented to block on the exit code.
//
// The continue bytes are the fix for a defect this table used to assert as
// correct: it required an EMPTY stdout for every fault, which is a proceed only
// on a host that reads the exit code. On OpenCode an empty body makes the
// generated plugin throw inside the callback chain, so "fail open" stopped the
// user's tool call.
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

			outcome, ok := hostexit.ForFault(hostexit.Fault{
				Mode:         c.mode,
				Evidence:     c.evidence,
				Policy:       c.policy,
				Stage:        hostexit.FaultStageNotRecorded,
				Continuation: hostexit.ContinuationOf(openCodeProceed),
				Cause:        fault,
			})
			require.True(t, ok, "a real fault with a declared mode, policy, stage and continuation must map")

			wantBlock := c.policy == hostexit.FaultFailClosed &&
				c.mode.BlocksByExitCode() &&
				c.evidence.IsPresent()

			if wantBlock {
				assert.Equal(t, hostexit.ExitBlock, outcome.Exit,
					"an evidenced blocking mode under fail-closed must refuse the operation")
				assert.Empty(t, outcome.Stdout,
					"a host that is being refused is not also told to continue")
			} else {
				assert.Equal(t, hostexit.ExitContinue, outcome.Exit,
					"every other cell must let the host continue")
				assert.Equal(t, openCodeProceed, outcome.Stdout,
					"a fail-open fault must emit the host's CONTINUE BYTES, or the host stops the user's action")
			}
			assert.NotEmpty(t, outcome.Stderr, "a fault is never silent")
		})
		if c.policy == hostexit.FaultFailClosed && c.mode.BlocksByExitCode() && c.evidence.IsPresent() {
			blocked++
		}
	}
	assert.Equal(t, 2, blocked,
		"exactly two of the 24 cells may block: the two exit-code arms, under fail-closed, with evidence")
}

// TestFailOpenEmitsTheContinuationItWasGiven pins the three real host shapes
// through the same door: an empty body (a host that reads the exit code), and
// the two object bodies (a host that parses stdout). The bytes are carried
// verbatim, so the harness table can live at the encoder boundary and this
// package can stay pure.
func TestFailOpenEmitsTheContinuationItWasGiven(t *testing.T) {
	t.Parallel()

	cause := errors.New("the pasture store could not be opened")
	tests := []struct {
		name         string
		continuation hostexit.Continuation
		want         []byte
	}{
		{name: "a host that reads the exit code takes no bytes", continuation: hostexit.EmptyContinuation(), want: nil},
		{name: "a plugin-validated host takes the canonical proceed object", continuation: hostexit.ContinuationOf(openCodeProceed), want: openCodeProceed},
		{name: "a command-hook host takes the universal continue object", continuation: hostexit.ContinuationOf([]byte(`{"continue":true}`)), want: []byte(`{"continue":true}`)},
		{name: "an observation of a command-hook host takes the default object", continuation: hostexit.ContinuationOf([]byte(`{}`)), want: []byte(`{}`)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			outcome, ok := hostexit.ForFault(hostexit.Fault{
				Mode:         pastureruntime.FailureThrowFailFast,
				Policy:       hostexit.FaultFailOpen,
				Stage:        hostexit.FaultStageNotRecorded,
				Continuation: test.continuation,
				Cause:        cause,
			})
			require.True(t, ok)
			assert.Equal(t, hostexit.ExitContinue, outcome.Exit)
			assert.Equal(t, test.want, outcome.Stdout)
		})
	}
}

// TestFailClosedHasNoChannelOnTheThrowingHost pins the limit of the opt-in.
// PASTURE_HOOK_FAIL_CLOSED refuses through the process EXIT CODE. The OpenCode
// named callbacks are throw-fail-fast: that host's refusal channel is a typed
// deny object returned at exit 0, and a non-zero exit is read there as a broken
// installation, not as a refusal. So an OpenCode row cannot block by exit code
// and the opt-in reaches nothing on that harness today.
//
// The day the typed deny object exists, this test fails and somebody has to
// decide DELIBERATELY whether an unevaluated event may use the refusal channel.
// That decision is the point of the pin.
func TestFailClosedHasNoChannelOnTheThrowingHost(t *testing.T) {
	t.Parallel()

	require.False(t, pastureruntime.FailureThrowFailFast.BlocksByExitCode(),
		"the throwing host does not refuse by process exit code")

	outcome, ok := hostexit.ForFault(hostexit.Fault{
		Mode:         pastureruntime.FailureThrowFailFast,
		Evidence:     pastureruntime.FailureEvidence{Source: citedSource},
		Policy:       hostexit.FaultFailClosed,
		Stage:        hostexit.FaultStageNotRecorded,
		Continuation: hostexit.ContinuationOf(openCodeProceed),
		Cause:        errors.New("the pasture store could not be opened"),
	})
	require.True(t, ok)
	assert.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"fail-closed has no channel on a host that refuses by throwing, so the host still continues")
	assert.Equal(t, openCodeProceed, outcome.Stdout,
		"and it continues with the host's proceed bytes, not with a body the host would reject")
	assert.Contains(t, outcome.Stderr,
		"fail-closed has no channel on OpenCode named callbacks until the typed refusal object exists; this invocation continued",
		"a user who opted into fail-closed must be told, in the diagnostic itself, that it did not apply here")
	assert.Contains(t, outcome.Stderr, "which this host does not read as a refusal",
		"the reader must be told WHY the opt-in did not apply")
}

// TestForFaultRefusesWhenThereIsNothingToMap pins the false result. A false
// result is a programming error at the call site, so it must be distinguishable
// from a real continue outcome; a caller that reads it as "exit 0" would
// recreate the silent proceed.
func TestForFaultRefusesWhenThereIsNothingToMap(t *testing.T) {
	t.Parallel()

	fault := errors.New("boom")
	evidence := pastureruntime.FailureEvidence{Source: citedSource}
	continuation := hostexit.ContinuationOf(openCodeProceed)

	tests := []struct {
		name  string
		fault hostexit.Fault
	}{
		{name: "nil cause", fault: hostexit.Fault{Mode: pastureruntime.FailureExitTwoBlocks, Evidence: evidence, Policy: hostexit.FaultFailClosed, Stage: hostexit.FaultStageNotRecorded, Continuation: continuation}},
		{name: "unset mode", fault: hostexit.Fault{Evidence: evidence, Policy: hostexit.FaultFailClosed, Stage: hostexit.FaultStageNotRecorded, Continuation: continuation, Cause: fault}},
		{name: "mode above the last arm", fault: hostexit.Fault{Mode: pastureruntime.FailureObserveOnly + 1, Evidence: evidence, Policy: hostexit.FaultFailOpen, Stage: hostexit.FaultStageNotRecorded, Continuation: continuation, Cause: fault}},
		{name: "unset policy", fault: hostexit.Fault{Mode: pastureruntime.FailureExitTwoBlocks, Evidence: evidence, Stage: hostexit.FaultStageNotRecorded, Continuation: continuation, Cause: fault}},
		{name: "policy above the last arm", fault: hostexit.Fault{Mode: pastureruntime.FailureExitTwoBlocks, Evidence: evidence, Policy: hostexit.FaultFailClosed + 1, Stage: hostexit.FaultStageNotRecorded, Continuation: continuation, Cause: fault}},
		{name: "unset stage", fault: hostexit.Fault{Mode: pastureruntime.FailureExitTwoBlocks, Evidence: evidence, Policy: hostexit.FaultFailOpen, Continuation: continuation, Cause: fault}},
		{name: "stage above the last arm", fault: hostexit.Fault{Mode: pastureruntime.FailureExitTwoBlocks, Evidence: evidence, Policy: hostexit.FaultFailOpen, Stage: hostexit.FaultStageRecordUnknown + 1, Continuation: continuation, Cause: fault}},
		{name: "continuation never set", fault: hostexit.Fault{Mode: pastureruntime.FailureExitTwoBlocks, Evidence: evidence, Policy: hostexit.FaultFailOpen, Stage: hostexit.FaultStageNotRecorded, Cause: fault}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			outcome, ok := hostexit.ForFault(test.fault)
			assert.False(t, ok, "there is nothing to map, so the result must be refused")
			assert.Equal(t, hostexit.ExitStatusUnset, outcome.Exit,
				"a refused mapping must not hand back a usable exit status")
		})
	}
}

// TestContinuationZeroValueIsUnusable pins the reason Continuation is a value
// with a SET flag and not a byte slice. An empty body IS a legitimate proceed on
// one host, so "no bytes" cannot also mean "the caller forgot": a forgotten
// continuation must be a loud refusal, not a silent return to the defect where
// a fail-open fault emitted nothing on a host that parses stdout.
func TestContinuationZeroValueIsUnusable(t *testing.T) {
	t.Parallel()

	var unset hostexit.Continuation
	assert.False(t, unset.IsSet(), "the zero value is not a continuation")
	assert.Nil(t, unset.Bytes())

	empty := hostexit.EmptyContinuation()
	assert.True(t, empty.IsSet(), "an empty body is a real continuation on a host that reads the exit code")
	assert.Nil(t, empty.Bytes())

	body := hostexit.ContinuationOf(openCodeProceed)
	assert.True(t, body.IsSet())
	assert.Equal(t, openCodeProceed, body.Bytes())

	// The bytes are copied in and out, so a caller cannot reach into a decided
	// continuation and change what the host will read.
	mutable := []byte(`{"decision":"proceed"}`)
	held := hostexit.ContinuationOf(mutable)
	mutable[2] = 'X'
	assert.Equal(t, openCodeProceed, held.Bytes(), "the continuation copies its bytes in")
	held.Bytes()[2] = 'X'
	assert.Equal(t, openCodeProceed, held.Bytes(), "the continuation copies its bytes out")
}

// TestFaultStageZeroValueRefuses pins the stage enum. The stage decides whether
// the operator is told the event was not recorded or that the durable state is
// unknown, so an unset stage must never fall through to the confident claim.
func TestFaultStageZeroValueRefuses(t *testing.T) {
	t.Parallel()

	assert.False(t, hostexit.FaultStageUnset.IsValid())
	assert.Empty(t, hostexit.FaultStageUnset.String())
	assert.True(t, hostexit.FaultStageNotRecorded.IsValid())
	assert.True(t, hostexit.FaultStageRecordUnknown.IsValid())
	assert.False(t, (hostexit.FaultStageRecordUnknown + 1).IsValid())
	assert.Equal(t, "not-recorded", hostexit.FaultStageNotRecorded.String())
	assert.Equal(t, "record-unknown", hostexit.FaultStageRecordUnknown.String())
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
		outcome, ok := hostexit.ForFault(hostexit.Fault{
			Mode:         pastureruntime.FailureExitTwoBlocks,
			Evidence:     pastureruntime.FailureEvidence{Source: citedSource},
			Policy:       hostexit.FaultFailOpen,
			Stage:        hostexit.FaultStageNotRecorded,
			Continuation: hostexit.EmptyContinuation(),
			Cause:        cause,
		})
		require.True(t, ok)
		assertActionable(t, outcome.Stderr, cause)
		assert.Contains(t, outcome.Stderr, "the host continues",
			"the reader must be told the operation was not stopped")
		assert.Contains(t, outcome.Stderr, "PASTURE_HOOK_FAIL_CLOSED=1",
			"the reader must be told how to make this stop the host instead")
	})

	t.Run("fail closed and blocking", func(t *testing.T) {
		t.Parallel()
		outcome, ok := hostexit.ForFault(hostexit.Fault{
			Mode:         pastureruntime.FailureExitTwoBlocks,
			Evidence:     pastureruntime.FailureEvidence{Source: citedSource},
			Policy:       hostexit.FaultFailClosed,
			Stage:        hostexit.FaultStageNotRecorded,
			Continuation: hostexit.EmptyContinuation(),
			Cause:        cause,
		})
		require.True(t, ok)
		require.Equal(t, hostexit.ExitBlock, outcome.Exit)
		assertActionable(t, outcome.Stderr, cause)
		assert.Contains(t, outcome.Stderr, "the host refuses the operation",
			"the reader must be told the operation was stopped")
		assert.Contains(t, outcome.Stderr, "unset PASTURE_HOOK_FAIL_CLOSED",
			"the reader must be told how to let the host continue instead")
	})
}

// TestTheImpactFollowsWhatPastureKnows is the honesty pin on the fault text.
//
// One fixed sentence used to be told to every reader: "this lifecycle event is
// not evaluated". On the abandonment path that can be FALSE, because the
// durable receipt commits before the native bytes are produced, so an expiry can
// land after the commit. Telling a maintainer nothing was recorded, and leaving
// an occurrence in the journal, is a contradiction between two artefacts the
// later work reads.
func TestTheImpactFollowsWhatPastureKnows(t *testing.T) {
	t.Parallel()

	cause := errors.New("the hook stopped waiting at its 5s hook-invocation deadline")

	notRecorded, ok := hostexit.ForFault(hostexit.Fault{
		Mode:         pastureruntime.FailureReportAndContinue,
		Policy:       hostexit.FaultFailOpen,
		Stage:        hostexit.FaultStageNotRecorded,
		Continuation: hostexit.EmptyContinuation(),
		Cause:        cause,
	})
	require.True(t, ok)
	assert.Contains(t, notRecorded.Stderr, "no occurrence was recorded for it",
		"a fault that stopped before the durable write may say so")
	assert.NotContains(t, notRecorded.Stderr, "MAY OR MAY NOT")

	unknown, ok := hostexit.ForFault(hostexit.Fault{
		Mode:         pastureruntime.FailureReportAndContinue,
		Policy:       hostexit.FaultFailOpen,
		Stage:        hostexit.FaultStageRecordUnknown,
		Continuation: hostexit.EmptyContinuation(),
		Cause:        cause,
	})
	require.True(t, ok)
	assert.Contains(t, unknown.Stderr, "MAY OR MAY NOT exist",
		"an abandoned invocation must not claim the event was never recorded")
	assert.NotContains(t, unknown.Stderr, "no occurrence was recorded for it")
	assert.Contains(t, unknown.Stderr, "lifecycle occurrence journal",
		"the reader must be told WHERE to look for the occurrence that may exist")
	assert.Contains(t, unknown.Stderr, "fault record file beside the database",
		"the reader must be told the other place to look")
	assert.Contains(t, unknown.Stderr, "a long-running writer holding the pasture store",
		"the reader must be told the usual cause")
	assert.Contains(t, unknown.Stderr, "retry once it releases the store",
		"the reader must be told how to recover")
	assert.Contains(t, unknown.Stderr, "durable state record-unknown",
		"the machine-readable stage travels with the text")
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

// TestForDecisionCarriesTheDecisionVerbatim proves what the body does: the
// bytes, the exit status and the text reach the host unchanged.
//
// The name says only that, because that is all a call can show. The stronger
// property — that neither the fault policy nor a missing citation can weaken an
// evaluated Deny — is enforced by the SIGNATURE, which has no such parameter, so
// no input can exercise it. TestForDecisionCannotSeeThePolicyOrTheEvidence
// pins the structure instead.
func TestForDecisionCarriesTheDecisionVerbatim(t *testing.T) {
	t.Parallel()

	native := []byte(`{"decision":"block","reason":"the assignment does not cover this file"}`)
	outcome := hostexit.ForDecision(native, hostexit.ExitBlock, "the assignment does not cover this file")

	assert.Equal(t, hostexit.ExitBlock, outcome.Exit)
	assert.Equal(t, native, outcome.Stdout)
	assert.Equal(t, "the assignment does not cover this file", outcome.Stderr)

	proceed := hostexit.ForDecision(nil, hostexit.ExitContinue, "")
	assert.Equal(t, hostexit.ExitContinue, proceed.Exit)
	assert.Empty(t, proceed.Stdout, "a host that reads the exit code takes no proceed bytes")
	assert.Empty(t, proceed.Stderr)
}

// TestForDecisionCannotSeeThePolicyOrTheEvidence makes the fault-versus-decision
// split structural rather than argued. It reads the declaration of ForDecision
// and refuses any mention of the fault policy or the failure evidence anywhere
// in its signature or its body.
//
// This is the one assertion in this package that a value cannot exercise: while
// ForDecision takes neither, no input can distinguish a correct implementation
// from one that ignores them. Reading the source is what turns a future edit
// that ADDS one into a RED test on the day it is written.
func TestForDecisionCannotSeeThePolicyOrTheEvidence(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "hostexit.go", nil, 0)
	require.NoError(t, err, "the package source must be readable beside its test")

	var decl *ast.FuncDecl
	for _, node := range file.Decls {
		function, isFunction := node.(*ast.FuncDecl)
		if isFunction && function.Name.Name == "ForDecision" {
			decl = function
			break
		}
	}
	require.NotNil(t, decl, "ForDecision must exist to be the decision door")

	forbidden := map[string]string{
		"FaultPolicy":     "an evaluated decision must not be re-judged by the fault policy",
		"FaultFailOpen":   "an evaluated decision must not be re-judged by the fault policy",
		"FaultFailClosed": "an evaluated decision must not be re-judged by the fault policy",
		"FailureEvidence": "an evaluated decision must not be weakened by a missing citation",
		"IsPresent":       "an evaluated decision must not be weakened by a missing citation",
		"BlocksByExitCode": "an evaluated decision must not be re-judged by the blocking channel " +
			"of the event's failure mode",
	}
	ast.Inspect(decl, func(node ast.Node) bool {
		identifier, isIdentifier := node.(*ast.Ident)
		if !isIdentifier {
			return true
		}
		if reason, isForbidden := forbidden[identifier.Name]; isForbidden {
			t.Errorf("ForDecision refers to %s: %s", identifier.Name, reason)
		}
		return true
	})
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

// TestTheDocumentedFaultRuleCannotBeSilentlyReverted pins two sentences of this
// package's own documentation, because both are load-bearing and neither can be
// asserted through a value.
//
// The first is the rule a fail-open fault follows. An earlier rule said a fault
// writes NO native continuation on every harness. That rule IS the defect this
// package was changed to remove: on OpenCode an empty body at exit 0 makes the
// generated plugin throw and stops the user's tool call. A later reader who
// finds the fault bytes surprising, and who does not find the replaced rule
// written down, would "restore" it and reopen the defect. So the replaced rule
// and its replacement are both stated in the package doc, and this test fails
// if either disappears.
//
// The second is the forward hazard on the record-unknown arm: once an evaluated
// decision can share the commit the deadline lands in, a fail-open continuation
// can contradict a recorded refusal. Nothing in the code can carry that warning
// today, because no decision exists yet to contradict.
func TestTheDocumentedFaultRuleCannotBeSilentlyReverted(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "hostexit.go", nil, parser.ParseComments)
	require.NoError(t, err, "the package source must be readable beside its test")

	// The comparison is on whitespace-normalized text, because a doc comment is
	// line-wrapped and a sentence that must survive is a sentence, not a line.
	documentation := oneLine(file.Doc.Text())
	for phrase, why := range map[string]string{
		"a fault writes NO native continuation": "the replaced rule must stay named, " +
			"or a later reader restores it as a fix and stops the user's tool call again",
		"A FAIL-OPEN FAULT EMITS THAT HARNESS'S CONTINUE BYTES AND A DIAGNOSTIC; NEVER NOTHING": "" +
			"the rule that replaced it must be stated where a reader of this package meets it",
		"On Claude Code those continue bytes ARE the empty body": "a reader must be told that only " +
			"the two byte-shaped hosts changed, or the change reads as a change to every host",
	} {
		assert.Contains(t, documentation, phrase, why)
	}

	source, err := parser.ParseFile(token.NewFileSet(), "hostexit.go", nil, parser.ParseComments)
	require.NoError(t, err)
	var stageDocumentation strings.Builder
	for _, group := range source.Comments {
		stageDocumentation.WriteString(oneLine(group.Text()) + " ")
	}
	for phrase, why := range map[string]string{
		"PROCEED PAST A REFUSAL": "the record-unknown arm must warn that the hazard direction is " +
			"failing open past a decision that was recorded",
		"one atomic step": "the reader must be given the two answers to choose between, " +
			"because inheriting the arm unexamined is what the warning exists to prevent",
	} {
		assert.Contains(t, stageDocumentation.String(), phrase, why)
	}
}

// oneLine collapses every run of whitespace to one space, so a pinned sentence
// is compared as a sentence and not as a set of wrapped lines.
func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }
