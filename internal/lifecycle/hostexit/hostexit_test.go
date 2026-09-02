package hostexit_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// declaredScanBound is how far the derivations below look for declared members
// of a closed enum.
//
// IT IS SIZED FROM THE UNDERLYING TYPE and not chosen. These enums are uint8,
// so 256 values exhaust them: a bound smaller than that is a promise that
// nobody will ever declare a member above it, which is exactly the kind of
// written-down limit this file has been correcting. Scanning the full domain
// costs nothing at test time and cannot be outgrown.
const declaredScanBound = 1 << 8

// allFailureModes returns EVERY DECLARED failure mode, derived from IsValid
// rather than written down.
//
// IT WAS A LITERAL LIST, AND THAT MADE THE TABLE BELOW UNABLE TO GROW. A
// seventh declared, valid mode left TestForFaultCoversEveryModePolicyAndEvidenceCell
// passing on all twenty-four subtests, and its require.Len could not see the
// gap either BECAUSE THE COUNT CAME FROM THIS SAME LIST. A count derived from
// the thing it counts is not a count; it is the list agreeing with itself.
// This is the second table in the slice to fail that question after the fault
// stages, so the derivation is now the shape both use.
func allFailureModes() []pastureruntime.FailureMode {
	modes := []pastureruntime.FailureMode{}
	for candidate := 0; candidate < declaredScanBound; candidate++ {
		mode := pastureruntime.FailureMode(candidate)
		if mode.IsValid() {
			modes = append(modes, mode)
		}
	}
	return modes
}

// TestEveryDeclaredFailureModeNamesItself requires a derived mode to have a
// name, which is what the stage sweep beside it requires of a stage.
//
// WHY IT WAS MISSING. Deriving the SET from IsValid made the table grow with
// the type, and stopped there: a seventh mode declared VALID but not added to
// String() would enter every cell of the fault table and write an EMPTY STRING
// into the durable fault record and into the operator's diagnostic, where the
// mode is printed by name. The stage sweep already refused that for stages;
// this is the same question asked of the neighbouring enum, and it had the same
// answer missing.
//
// MUTATION: extend FailureMode.IsValid to admit one more value without adding
// it to String(). This test turns RED naming the number.
func TestEveryDeclaredFailureModeNamesItself(t *testing.T) {
	t.Parallel()

	modes := allFailureModes()
	require.NotEmpty(t, modes,
		"the derivation must find the declared modes; an empty set would make every assertion "+
			"below vacuous, which is the failure this derivation replaced")
	for _, mode := range modes {
		assert.NotEmpty(t, mode.String(),
			"failure mode %d is declared VALID and has no name. It reaches every cell of the fault "+
				"table and is printed by name into the operator's diagnostic and into the durable "+
				"fault record, where an empty string cannot be told from a member the writer forgot",
			uint8(mode))
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
// on a host that reads the exit code. On OpenCode an empty body MADE the
// generated plugin throw inside the callback chain, so "fail open" stopped the
// user's tool call. The plugin this build generates now reports and continues
// instead, and the cells below do not change for it, because PASTURE CANNOT
// KNOW WHICH PLUGIN IS INSTALLED and an ALREADY-INSTALLED OLDER ONE STILL
// THROWS.
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
	// THE EXPECTED COUNT IS COMPUTED FROM THE AXES, not from the list that
	// produced the cells. Taking it from len(allFailureModes()) would have been
	// the list agreeing with itself again.
	require.Len(t, cells, len(allFailureModes())*2*2, "the table must cover 6 modes x 2 policies x 2 evidence states")

	blocked := 0
	for _, c := range cells {
		c := c
		name := c.mode.String() + "/" + c.policy.String() + "/evidence=" + evidenceName(c.evidence)
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			outcome, ok := hostexit.ForFault(hostexit.Fault{
				Mode: c.mode,
				// The cell exercises the EXIT table, which reads the
				// effective mode alone. A row whose declaration differs is
				// covered where the difference matters, in the fault text.
				DeclaredMode: c.mode,
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
				DeclaredMode: pastureruntime.FailureThrowFailFast,
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
		DeclaredMode: pastureruntime.FailureThrowFailFast,
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
//
// It also pins the EXPLANATION of each refusal, because the caller prints it.
//
// MUTATION: make an arm of UnusableInputs name a different input — for example
// let the declared-mode arm report the effective mode — or make an arm report
// on a valid value. This test turns RED on the case for that input.
func TestForFaultRefusesWhenThereIsNothingToMap(t *testing.T) {
	t.Parallel()

	fault := errors.New("boom")
	evidence := pastureruntime.FailureEvidence{Source: citedSource}
	continuation := hostexit.ContinuationOf(openCodeProceed)
	blocks := pastureruntime.FailureExitTwoBlocks

	tests := []struct {
		name string
		// named is the sentence UnusableInputs must produce for this case. It
		// is written per case because the whole point of the list is that the
		// caller can say WHICH input was wrong: six conditions produce one
		// refusal, and a message that names the wrong one sends the reader to a
		// field that was fine.
		named string
		fault hostexit.Fault
	}{
		{name: "nil cause", named: "the cause is nil, so there is no fault to report", fault: hostexit.Fault{Mode: blocks, DeclaredMode: blocks, Evidence: evidence, Policy: hostexit.FaultFailClosed, Stage: hostexit.FaultStageNotRecorded, Continuation: continuation}},
		{name: "unset mode", named: "the effective failure mode is unset or not a known mode", fault: hostexit.Fault{DeclaredMode: blocks, Evidence: evidence, Policy: hostexit.FaultFailClosed, Stage: hostexit.FaultStageNotRecorded, Continuation: continuation, Cause: fault}},
		{name: "mode above the last arm", named: "the effective failure mode is unset or not a known mode", fault: hostexit.Fault{Mode: modeAboveTheLastArm(), DeclaredMode: blocks, Evidence: evidence, Policy: hostexit.FaultFailOpen, Stage: hostexit.FaultStageNotRecorded, Continuation: continuation, Cause: fault}},
		// The DECLARED mode is refused on the same terms as the effective one.
		// It is required because a defaulted declaration is exactly how the
		// fault text came to call a demoted row's mode "declared": a zero value
		// there would silently pick the wrong sentence for a blocking gate, and
		// nothing downstream could tell.
		{name: "unset declared mode", named: "the declared failure mode is unset or not a known mode", fault: hostexit.Fault{Mode: blocks, Evidence: evidence, Policy: hostexit.FaultFailClosed, Stage: hostexit.FaultStageNotRecorded, Continuation: continuation, Cause: fault}},
		{name: "declared mode above the last arm", named: "the declared failure mode is unset or not a known mode", fault: hostexit.Fault{Mode: blocks, DeclaredMode: modeAboveTheLastArm(), Evidence: evidence, Policy: hostexit.FaultFailOpen, Stage: hostexit.FaultStageNotRecorded, Continuation: continuation, Cause: fault}},
		{name: "unset policy", named: "the fault policy is unset or not a known policy", fault: hostexit.Fault{Mode: blocks, DeclaredMode: blocks, Evidence: evidence, Stage: hostexit.FaultStageNotRecorded, Continuation: continuation, Cause: fault}},
		{name: "policy above the last arm", named: "the fault policy is unset or not a known policy", fault: hostexit.Fault{Mode: blocks, DeclaredMode: blocks, Evidence: evidence, Policy: policyAboveTheLastArm(), Stage: hostexit.FaultStageNotRecorded, Continuation: continuation, Cause: fault}},
		{name: "unset stage", named: "the fault stage is unset or not a known stage", fault: hostexit.Fault{Mode: blocks, DeclaredMode: blocks, Evidence: evidence, Policy: hostexit.FaultFailOpen, Continuation: continuation, Cause: fault}},
		{name: "stage above the last arm", named: "the fault stage is unset or not a known stage", fault: hostexit.Fault{Mode: blocks, DeclaredMode: blocks, Evidence: evidence, Policy: hostexit.FaultFailOpen, Stage: stageAboveTheLastArm(), Continuation: continuation, Cause: fault}},
		{name: "continuation never set", named: "the host continuation was never set, so there are no proceed bytes to emit", fault: hostexit.Fault{Mode: blocks, DeclaredMode: blocks, Evidence: evidence, Policy: hostexit.FaultFailOpen, Stage: hostexit.FaultStageNotRecorded, Cause: fault}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			outcome, ok := hostexit.ForFault(test.fault)
			assert.False(t, ok, "there is nothing to map, so the result must be refused")
			assert.Equal(t, hostexit.ExitStatusUnset, outcome.Exit,
				"a refused mapping must not hand back a usable exit status")

			// The refusal and its explanation come from ONE function, so a
			// caller can print the reason and cannot describe a condition
			// other than the one that happened.
			unusable := test.fault.UnusableInputs()
			assert.Equal(t, []string{test.named}, unusable,
				"the refusal must name the one input that was not usable, and no other")
		})
	}
}

// TestAUsableFaultNamesNoUnusableInput is the other half of the refusal
// contract: a fault ForFault CAN map must name nothing, or the caller would
// print a reason for a fault that was fine.
//
// MUTATION: make any arm of UnusableInputs report on a valid value — for
// example append when the stage IS valid. This test turns RED.
func TestAUsableFaultNamesNoUnusableInput(t *testing.T) {
	t.Parallel()

	usable := hostexit.Fault{
		Mode:         pastureruntime.FailureExitTwoBlocks,
		DeclaredMode: pastureruntime.FailureExitTwoBlocks,
		Evidence:     pastureruntime.FailureEvidence{Source: citedSource},
		Policy:       hostexit.FaultFailClosed,
		Stage:        hostexit.FaultStageNotRecorded,
		Continuation: hostexit.ContinuationOf(openCodeProceed),
		Cause:        errors.New("boom"),
	}
	_, ok := hostexit.ForFault(usable)
	require.True(t, ok, "this fault carries every input, so it must map")
	assert.Empty(t, usable.UnusableInputs(),
		"a fault that maps has no unusable input to name")
	assert.NotNil(t, usable.UnusableInputs(),
		"the empty result must be a NON-NIL slice: the caller writes it straight into a durable "+
			"JSON record, where a nil slice becomes null and null cannot be told apart from a "+
			"member the writer forgot")
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
// aboveTheLastArm returns the first value above every declared member of a
// closed enum, given its validity predicate.
//
// IT IS THE SHAPE stageAboveTheLastArm ESTABLISHED, generalised to the other
// three closed enums this file probes. Those probes still named
// FailureObserveOnly+1, FaultFailClosed+1 and ExitBlock+1. Unlike the stage
// case, none of them goes quiet when a member is appended — they are
// range-shaped or equality-shaped and fail loudly — so this is a MAINTENANCE
// COST rather than an untruth, and it is corrected on that ground and no other.
// Whoever appends a member should not also have to remember three sentinels,
// and the failure that would remind them points at the probe rather than at
// what they changed.
func aboveTheLastArm(isValid func(int) bool) int {
	last := 0
	for candidate := 1; candidate < declaredScanBound; candidate++ {
		if isValid(candidate) {
			last = candidate
		}
	}
	return last + 1
}

// modeAboveTheLastArm, policyAboveTheLastArm and exitAboveTheLastArm are the
// three sentinels, each derived from its own type's predicate.
func modeAboveTheLastArm() pastureruntime.FailureMode {
	return pastureruntime.FailureMode(aboveTheLastArm(func(candidate int) bool {
		return pastureruntime.FailureMode(candidate).IsValid()
	}))
}

func policyAboveTheLastArm() hostexit.FaultPolicy {
	return hostexit.FaultPolicy(aboveTheLastArm(func(candidate int) bool {
		return hostexit.FaultPolicy(candidate).IsValid()
	}))
}

func exitAboveTheLastArm() hostexit.ExitStatus {
	return hostexit.ExitStatus(aboveTheLastArm(func(candidate int) bool {
		return hostexit.ExitStatus(candidate).IsValid()
	}))
}

// stageAboveTheLastArm returns the first value ABOVE every declared stage.
//
// IT IS COMPUTED AND NOT WRITTEN DOWN, AND THE REASON RECORDED HERE WAS FALSE.
// It said the literal sentinels "went on passing while testing nothing". THEY
// DO NOT. Restoring FaultStageRecordUnknown+1 on this revision makes FIVE
// assertions across TWO tests FAIL, loudly and at once, because that expression
// now names a DECLARED stage while both tests assert it is invalid. Nothing
// goes quiet, and no probe retires in silence.
//
// The true reason is smaller and is worth stating accurately: a written-down
// sentinel is a MAINTENANCE COST, not an untruth. Whoever appends a stage must
// remember to move it, and the failure that reminds them reads "should be
// false" about a stage they have just declared — a message that points at the
// probe rather than at what they changed. Deriving it removes the errand and
// lets the probe describe itself.
//
// A TRUE FIX RESTING ON A FALSE REASON IS A TRAP FOR WHOEVER READS THE REASON
// NEXT, which is why this is corrected in place rather than quietly edited: it
// is the second time in this slice that a justification claimed more than the
// thing it justified, after a pin that cited its own premise as its warrant.
func stageAboveTheLastArm() hostexit.FaultStage {
	last := hostexit.FaultStage(0)
	for candidate := 1; candidate < declaredScanBound; candidate++ {
		stage := hostexit.FaultStage(candidate)
		if stage.IsValid() {
			last = stage
		}
	}
	return last + 1
}

func TestFaultStageZeroValueRefuses(t *testing.T) {
	t.Parallel()

	assert.False(t, hostexit.FaultStageUnset.IsValid())
	assert.Empty(t, hostexit.FaultStageUnset.String())
	assert.True(t, hostexit.FaultStageNotRecorded.IsValid())
	assert.True(t, hostexit.FaultStageRecordUnknown.IsValid())
	assert.True(t, hostexit.FaultStageRecorded.IsValid())
	assert.False(t, stageAboveTheLastArm().IsValid())
	assert.Empty(t, stageAboveTheLastArm().String(),
		"a value above every declared stage has no name, so it cannot be written into a fault record")
	assert.Equal(t, "not-recorded", hostexit.FaultStageNotRecorded.String())
	assert.Equal(t, "record-unknown", hostexit.FaultStageRecordUnknown.String())
	assert.Equal(t, "recorded", hostexit.FaultStageRecorded.String())
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
			DeclaredMode: pastureruntime.FailureExitTwoBlocks,
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
			DeclaredMode: pastureruntime.FailureExitTwoBlocks,
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

	// An operator who HAS taken the opt-in and whose event continued anyway must
	// not be told to take it. Why it continued is a property of the ROW, not of
	// the environment, so advice about the environment sends the reader to hunt
	// for a typo in their own shell instead of learning the real reason.
	//
	// There are TWO such rows and they continue for DIFFERENT reasons, so both
	// are pinned. Measured through the built binary, BOTH KINDS are met today,
	// and an earlier note here said otherwise: it called all four measured rows
	// "declared report-and-continue". Two of them are not. claude-code
	// PreCompact and codex PreToolUse are DECLARED BLOCKING gates that carry no
	// citation, so the failure-evidence rule demotes them; claude-code
	// Notification and PostToolUse are declared non-blocking. The first kind
	// belongs to the unevidenced row, the second to the mode row.
	//
	// WHICH KIND A ROW IS CANNOT BE READ FROM THE EFFECTIVE MODE, because the
	// demotion makes the two look identical. That is why the arm keys on the
	// DECLARED mode, and why the delivery of this decision is proven on the
	// built binary rather than here: see
	// TestTheFailClosedReasonFollowsTheDeclaredModeThroughTheBuiltBinary.
	//
	// MUTATION: delete the `exit != ExitBlock` arm of failClosedAdvice in
	// hostexit.faultDiagnostic, so the default clause is used again, and both
	// subtests turn RED on "already set" and on the forbidden
	// "set PASTURE_HOOK_FAIL_CLOSED=1".
	t.Run("fail closed but the declared mode does not refuse by exit code", func(t *testing.T) {
		t.Parallel()
		outcome, ok := hostexit.ForFault(hostexit.Fault{
			Mode:         pastureruntime.FailureReportAndContinue,
			DeclaredMode: pastureruntime.FailureReportAndContinue,
			Evidence:     pastureruntime.FailureEvidence{},
			Policy:       hostexit.FaultFailClosed,
			Stage:        hostexit.FaultStageNotRecorded,
			Continuation: hostexit.EmptyContinuation(),
			Cause:        cause,
		})
		require.True(t, ok)
		require.Equal(t, hostexit.ExitContinue, outcome.Exit,
			"a mode that does not refuse by exit code has no exit code the opt-in could use")
		assertActionable(t, outcome.Stderr, cause)
		assert.Contains(t, outcome.Stderr, "PASTURE_HOOK_FAIL_CLOSED is already set",
			"the reader has taken the opt-in, so the text must start from that fact")
		assert.Contains(t, outcome.Stderr, "does not refuse through a process exit code",
			"the reader must be told the REAL reason: this event's declared mode has no exit code "+
				"for the opt-in to turn into a refusal")
		assert.NotContains(t, outcome.Stderr, "set PASTURE_HOOK_FAIL_CLOSED=1",
			"telling an operator to do what they have already done reads as 'you did nothing'")
	})

	t.Run("fail closed but the row cites no host evidence", func(t *testing.T) {
		t.Parallel()
		outcome, ok := hostexit.ForFault(hostexit.Fault{
			// THE SHAPE PRODUCTION REALLY BUILDS. The failure-evidence rule has
			// already demoted this row, so the effective mode is
			// report-and-continue while the DECLARATION still blocks by exit
			// code. An earlier version of this subtest set the effective mode to
			// the blocking arm, which no uncited row can carry, and the arm it
			// proved was therefore dead on the production path.
			Mode:         pastureruntime.FailureReportAndContinue,
			DeclaredMode: pastureruntime.FailureExitTwoBlocks,
			Evidence:     pastureruntime.FailureEvidence{},
			Policy:       hostexit.FaultFailClosed,
			Stage:        hostexit.FaultStageNotRecorded,
			Continuation: hostexit.EmptyContinuation(),
			Cause:        cause,
		})
		require.True(t, ok)
		require.Equal(t, hostexit.ExitContinue, outcome.Exit,
			"an unevidenced row may not refuse a user's operation, whatever the policy says")
		assertActionable(t, outcome.Stderr, cause)
		assert.Contains(t, outcome.Stderr, "PASTURE_HOOK_FAIL_CLOSED is already set",
			"the reader has taken the opt-in, so the text must start from that fact")
		assert.Contains(t, outcome.Stderr, "carries no host evidence",
			"the reader must be told the REAL reason the event continued, which is a property of "+
				"this row and not of their environment")
		assert.Contains(t, outcome.Stderr, "add the host documentation or a committed capture",
			"the reader must be given the action that would actually change this outcome")
		assert.NotContains(t, outcome.Stderr, "set PASTURE_HOOK_FAIL_CLOSED=1",
			"telling an operator to do what they have already done reads as 'you did nothing', and "+
				"sends them hunting for a typo in their own environment")
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
		DeclaredMode: pastureruntime.FailureReportAndContinue,
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
		DeclaredMode: pastureruntime.FailureReportAndContinue,
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
	// THESE TWO REQUIRED THE WRITER REMEDY OF THE STAGE, AND THAT PINNED A
	// SENTENCE TO THE WRONG THING. "A long-running writer holding the pasture
	// store is the usual reason" is true of the ABANDONED DEADLINE and of
	// nothing else, and this stage has three producers: the deadline, a panic
	// raised after the work began, and any error the caller's table does not
	// recognise. Requiring it HERE required a panicking invocation to be sent
	// hunting for a writer that is not there.
	//
	// The remedy is not lost. It lives on the deadline route's own cause, which
	// is where it was true all along, and a caller-side pair drives both
	// producers to show it appears for one and not the other. What this arm
	// must still carry is what is true of the STAGE: that the row may or may
	// not exist, and both places to look — asserted above.
	assert.NotContains(t, unknown.Stderr, "a long-running writer holding the pasture store",
		"the remedy for the deadline cause must not be attached to a stage that three different "+
			"causes reach; a panic is not fixed by finding a writer")
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
	assert.False(t, exitAboveTheLastArm().IsValid())
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
	assert.False(t, policyAboveTheLastArm().IsValid())
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
// package was changed to remove: on OpenCode an empty body at exit 0 MADE the
// generated plugin throw and stop the user's tool call. The plugin this build
// generates no longer throws; the rule stands anyway, because PASTURE CANNOT
// KNOW WHICH PLUGIN IS INSTALLED and an ALREADY-INSTALLED OLDER ONE STILL
// THROWS. A later reader who
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

// declaredDoc returns the doc comment Go ATTACHES to one declaration of
// hostexit.go, whitespace-normalized. The name is either a plain function name
// ("ForFault") or a method written as receiver and method ("Fault.UnusableInputs").
//
// It reads the doc through the same rule the go tool uses — the comment group
// the parser binds to the declaration node — so what it returns is what
// "go doc <name>" prints. A guard built on file.Comments cannot do this: that
// slice holds every comment group in lexical order and says nothing about
// which declaration each group belongs to.
func declaredDoc(t *testing.T, name string) string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "hostexit.go", nil, parser.ParseComments)
	require.NoError(t, err, "the package source must be readable beside its test")

	for _, declaration := range file.Decls {
		function, isFunction := declaration.(*ast.FuncDecl)
		if !isFunction {
			continue
		}
		if declaredName(function) != name {
			continue
		}
		if function.Doc == nil {
			return ""
		}
		return oneLine(function.Doc.Text())
	}

	require.FailNowf(t, "declaration not found",
		"hostexit.go declares no %s, so this guard is pinned to a name that no longer exists; "+
			"rename the pin or restore the declaration", name)
	return ""
}

// declaredName renders a function declaration as the name go doc is asked for:
// "Fault.UnusableInputs" for a method, "ForFault" for a plain function.
func declaredName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	receiver := function.Recv.List[0].Type
	if pointer, isPointer := receiver.(*ast.StarExpr); isPointer {
		receiver = pointer.X
	}
	identifier, isIdentifier := receiver.(*ast.Ident)
	if !isIdentifier {
		return function.Name.Name
	}
	return identifier.Name + "." + function.Name.Name
}

// TestTheFaultTableIsDocumentedOnTheExitAuthorityItself asserts that the
// normative fault table is ATTACHED TO ForFault, and not to a neighbouring
// helper.
//
// It exists because of a defect no other guard in this tree could see. A method
// was inserted between the ForFault doc comment and "func ForFault" with no
// blank line, so the parser read ONE comment group and bound the whole exit
// contract to the method: "go doc ForFault" printed the signature and nothing
// else, while "go doc Fault.UnusableInputs" printed the fail-open / fail-closed
// table and the warning against a silent exit 0. The sole exit authority of
// this tree had no documentation, and the warning that guards the silent-exit-0
// defect was filed under a helper that lists field names. gofmt does not flag
// that, go vet does not flag it, and the package-doc guard beside this one
// cannot: it reads comments AS TEXT — the package doc, then every comment group
// in the file concatenated — so neither of its checks knows WHICH declaration a
// comment belongs to. This guard reads them AS ATTACHED DOCUMENTATION instead,
// which is the only way the difference is visible.
//
// MUTATION: move Fault.UnusableInputs back between the ForFault doc block and
// "func ForFault", or delete the blank line between the two blocks. The
// Contains assertions turn RED because ForFault's attached doc becomes empty,
// and the NotContains assertions turn RED because the helper inherits the exit
// contract.
func TestTheFaultTableIsDocumentedOnTheExitAuthorityItself(t *testing.T) {
	t.Parallel()

	authority := declaredDoc(t, "ForFault")
	for phrase, why := range map[string]string{
		"must not fall through to a silent exit 0": "the warning against the founding defect of " +
			"this package must be attached to the function that decides the exit, because that is " +
			"where a caller of it reads",
		"fail-closed, a mode that blocks by exit code, WITH evidence: block": "the fault table is " +
			"the only normative statement of when this tree blocks a host, and it must document " +
			"the function that applies it",
		"A Deny is an evaluated answer": "the rule that keeps a policy Deny out of the fault path " +
			"must be attached to the fault path it is a rule about",
	} {
		assert.Contains(t, authority, phrase, why)
	}

	helper := declaredDoc(t, "Fault.UnusableInputs")
	assert.Contains(t, helper, "names EVERY member of the fault that ForFault cannot use",
		"the helper must keep its own documentation, so that moving the exit contract off it does "+
			"not leave it undocumented in turn")
	for phrase, why := range map[string]string{
		"must not fall through to a silent exit 0": "the helper reports which inputs were wrong; " +
			"it decides no exit, so a reader who meets this warning here is sent to the wrong symbol",
		"fail-closed, a mode that blocks by exit code, WITH evidence: block": "the helper applies " +
			"no part of the fault table, and documenting the table on it is exactly the defect " +
			"this guard exists to catch",
	} {
		assert.NotContains(t, helper, phrase, why)
	}
}

// TestTheUnusableInputTriggerIsWrittenWhereAParserAuthorMeetsIt holds the
// de-duplication of the revisit trigger shut.
//
// The trigger — "a reader that groups faults by cause is what turns the English
// sentences of unusableFaultInputs into a typed member" — stood in TWO places:
// the doc comment of Fault.UnusableInputs, and the "The lifecycle fault record"
// section of AGENTS.md. Nothing held the two together, so either could be
// reworded or deleted while the other went on claiming to be the statement. The
// AGENTS.md placement is the right one: the reader it addresses is an author of
// a parser for lifecycle-faults.jsonl, and that person opens the record's
// section, not this package's source.
//
// The doc comment now POINTS at that section instead of restating it, and this
// test pins both ends of the pointer, so neither can leave without the other.
//
// IT PASSED VACUOUSLY AT BOTH ENDS AND NOW DOES NOT.
//
//   - The section was sliced from its heading TO THE END OF FILE — line 208 of
//     890 — so moving the trigger paragraph out of its section, to anywhere
//     later in the document, stayed green. PLACEMENT is this test's whole
//     claim, and placement is what it could not see. The slice is now bounded
//     at the next heading.
//   - The doc-comment end asserted only that the word "AGENTS.md" occurs. A
//     SECOND, unrelated sentence of the same comment also names AGENTS.md, so
//     deleting the pointer sentence stayed green too. The pointer is now
//     matched as the whole phrase that makes it a pointer.
//
// It also pins ITS OWN NAME, which two shipped documents cite: AGENTS.md tells
// a maintainer that this test holds the pointer, and the doc comment of
// Fault.UnusableInputs names it as well. Renaming the test used to leave both
// documents citing a test that does not exist, with this package, the guard and
// cmd/pasture all green. t.Name() is read at run time, so the citation cannot
// survive a rename.
//
// MUTATION: delete the "de facto schema" paragraph from the AGENTS.md section,
// or drop the pointer sentence from the UnusableInputs doc comment, or move the
// trigger paragraph out of its AGENTS.md section, or rename this test. This
// test turns RED on each.
// MUTATION: rename this test to a PREFIX of its own name, such as
// TestTheUnusableInputTrigger, and leave both documents citing the long name.
// This test turns RED at both citations. It did not while the citation was
// matched with assert.Contains, because a prefix IS a substring of the name the
// documents still carried.
func TestTheUnusableInputTriggerIsWrittenWhereAParserAuthorMeetsIt(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err, "resolve the repository root from internal/lifecycle/hostexit")
	raw, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err, "read AGENTS.md, which is where the revisit trigger is written")
	agents := string(raw)

	const heading = "### The lifecycle fault record"
	require.Contains(t, agents, heading,
		"the doc comment of Fault.UnusableInputs sends a parser author to this section by name; "+
			"renaming or removing it leaves that pointer dangling")

	section := markdownSection(t, agents, heading)
	require.NotEqual(t, agents[strings.Index(agents, heading):], section,
		"the section must END at the next heading; sliced to the end of the file it accepts the "+
			"trigger written anywhere below, and placement is the whole of this test's claim")

	for phrase, why := range map[string]string{
		"`unusableFaultInputs` MEMBER IS ENGLISH, NOT A STABLE KEY": "the section must still say what the member is, or the trigger below it has no subject",
		"must not key on them": "the refusal is the whole instruction to a parser author, and it is written only here",
		"typed member":         "the trigger is the promotion to a typed member; without it the section states a limitation and no way out",
	} {
		assert.Contains(t, section, phrase,
			"%s; the phrase must stand INSIDE the \"lifecycle fault record\" section, because a "+
				"parser author opens that section and reads no further", why)
	}

	comment := declaredDoc(t, "Fault.UnusableInputs")
	assert.Contains(t, comment, `"The lifecycle fault record" section of AGENTS.md`,
		"the doc comment must POINT at the section by NAME rather than restate it. Matching the "+
			"bare word \"AGENTS.md\" was vacuous: a second, unrelated sentence of this comment "+
			"names the file too, so the pointer sentence could be deleted with nothing noticing")
	assert.NotContains(t, comment, "de facto schema",
		"the restatement was deleted on purpose; two copies of one trigger drift, and only one "+
			"of them is where its reader looks")

	// Both documents CITE this test by name. An identifier written into a
	// document and held by nothing is a citation of something that may not
	// exist; renaming the test left both of them stale and every package green.
	//
	// THE MATCH IS THE WHOLE IDENTIFIER AND NOT A SUBSTRING. assert.Contains
	// stood here and PASSED UNDER ANY PREFIX RENAME: shorten the name to
	// TestTheUnusableInputTrigger and t.Name() is still a substring of the
	// longer identifier both documents go on citing, so the guard stayed green
	// while the citations pointed at a test that no longer existed — and the
	// AGENTS.md sentence beside them claims that exact rename fails this guard.
	// The pattern below is anchored on word boundaries, so the character after
	// the name must not continue the identifier.
	cited := regexp.MustCompile(`\b` + regexp.QuoteMeta(t.Name()) + `\b`)
	for where, text := range map[string]string{
		"AGENTS.md":                            section,
		"the Fault.UnusableInputs doc comment": comment,
	} {
		assert.Regexp(t, cited, text,
			"%s names the test that holds the pointer shut, and this run is that test; a citation "+
				"of a test that does not exist sends a maintainer looking for a guard that is "+
				"not there. The name must appear WHOLE: a citation of a LONGER identifier that "+
				"merely starts with this name is a citation of something else", where)
	}
}

// markdownSection returns the text of one heading's section: from the heading
// itself up to the next heading of the SAME OR A HIGHER level, or the end of the
// document when it is the last one.
//
// It exists because the assertion above is about PLACEMENT. A slice that runs to
// the end of the file is satisfied by the same phrase written anywhere below the
// heading, which is exactly the mutation the guard has to catch.
func markdownSection(t *testing.T, document, heading string) string {
	t.Helper()

	start := strings.Index(document, heading)
	require.NotEqual(t, -1, start, "the document must contain %q", heading)

	level := len(heading) - len(strings.TrimLeft(heading, "#"))
	rest := document[start+len(heading):]

	end := len(rest)
	for offset := 0; offset < len(rest); {
		next := strings.Index(rest[offset:], "\n#")
		if next == -1 {
			break
		}
		at := offset + next + 1
		candidate := rest[at:]
		if depth := len(candidate) - len(strings.TrimLeft(candidate, "#")); depth <= level {
			end = at
			break
		}
		offset = at + 1
	}
	return heading + rest[:end]
}

// impactClauseOf returns the diagnostic's impact clause, which is the only
// place a durable-state claim is made.
func impactClauseOf(t *testing.T, stderr string) string {
	t.Helper()
	const opens = "impact: "
	start := strings.Index(stderr, opens)
	require.NotEqual(t, -1, start,
		"the diagnostic must carry an impact clause; this check reads that clause and would pass "+
			"vacuously without it")
	clause := stderr[start+len(opens):]
	if ends := strings.Index(clause, "; fix: "); ends != -1 {
		clause = clause[:ends]
	}
	return clause
}

// expectedImpactClause composes the impact clause a given fault MUST render,
// from the same four inputs the renderer reads.
//
// WHY EQUALITY, AND WHY THE WORD LIST IS GONE. Three rules have now been
// written to hold this claim, and all three recognised a WORDING: first each
// other stage's exact sentence, then a distinctive phrase of it, then a list of
// four nouns "a durable-state claim cannot be made without". Every one was a
// written-down list wearing a different coat, and a reviewer walked through all
// three — the last with "a row for it may or may not exist in the lifecycle
// store", which names none of the four words and makes the claim perfectly well.
//
// A CLAIM CANNOT BE RECOGNISED BY ITS VOCABULARY. It can be recognised by
// EXHAUSTION: this clause is composed from the stage, the mode, the policy and
// the exit, so the test composes it too and requires equality. Anything added,
// reworded or moved fails, whatever words it uses, because the clause is no
// longer the expected one. The population is the whole impact clause and the
// rule is total over it — which is the first time that sentence is true here.
//
// WHAT IT DOES NOT COVER, STATED: the FIX clause beside it, and every other
// part of the diagnostic. A durable-state claim smuggled into the fix line is
// not read here.
func expectedImpactClause(fault hostexit.Fault, exit hostexit.ExitStatus, recordClause string) string {
	switch {
	case exit == hostexit.ExitBlock:
		return "the host refuses the operation, because this event is configured to fail closed and " +
			"the host documents that it blocks on this exit code"
	case fault.Policy == hostexit.FaultFailClosed && fault.Mode == pastureruntime.FailureThrowFailFast:
		return "the host continues with its own default answer, and " + recordClause + "; " +
			"fail-closed has no channel on OpenCode named callbacks until the typed refusal object " +
			"exists; this invocation continued"
	case fault.Stage == hostexit.FaultStageRecordUnknown:
		return "the host continues with its own default answer; " + recordClause +
			", and read the fault record file beside the database for this invocation"
	}
	return "the host continues with its own default answer, and " + recordClause
}

// durableStateSentences is the durable-state sentence of every declared stage,
// in ONE place, so the sweep and the blocking-arm pin cannot disagree about
// what the set is. A stage added without a sentence here is caught by the
// coverage check in the sweep, which reads the declared set from IsValid.
func durableStateSentences() map[hostexit.FaultStage]string {
	// THE WHOLE CLAUSE, NOT A FRAGMENT OF IT. The claim check below removes the
	// expected sentence and then requires the remainder to be silent about the
	// occurrence; a fragment leaves the rest of the stage's own sentence behind
	// and the stage accuses itself. Each of these is the exact recordClause its
	// stage renders.
	return map[hostexit.FaultStage]string{
		hostexit.FaultStageNotRecorded: "no occurrence was recorded for it",
		hostexit.FaultStageRecorded: "the delivery for it IS committed in the lifecycle occurrence journal, " +
			"so look for it there rather than concluding it was lost",
		hostexit.FaultStageRecordUnknown: "pasture stopped before it learned whether this event was recorded, " +
			"so an occurrence for it MAY OR MAY NOT exist — look for it in the lifecycle occurrence journal",
	}
}

// TestEveryFaultStageRendersItsOwnDurableStateOnEveryArm is the SWEEP over the
// two things this diagnostic can say at once: which STAGE the caller declared,
// and which ARM of the impact switch the fault lands in.
//
// WHY IT IS A SWEEP AND NOT A CASE. The durable-state sentence was written out
// THREE times — the default arm, the OpenCode fail-closed arm, and the
// record-unknown arm — and two of those copies hard-coded "no occurrence was
// recorded for it" REGARDLESS OF STAGE. So correcting the default arm would
// have left the fail-closed arm going on saying it, and only a reader who
// happened to set PASTURE_HOOK_FAIL_CLOSED on a throwing host would ever have
// seen the false one. That is the N-1 sweep this slice has produced ten times,
// and a table is the only shape that answers it.
//
// The pairs are enumerated rather than remembered: every declared stage against
// every arm a fail-open and a fail-closed caller can reach.
//
// MUTATION: hard-code any durable-state sentence into one arm of the impact
// switch instead of taking the derived clause. This test turns RED on the pair
// that reaches that arm.
func TestEveryFaultStageRendersItsOwnDurableStateOnEveryArm(t *testing.T) {
	t.Parallel()

	// EACH STAGE DECLARES ONLY WHAT IT MUST SAY. What it must NOT say is
	// DERIVED — it is every other stage's sentence — because the forbidden
	// lists were written out and therefore could not grow: a fourth stage's
	// sentence leaking into an arm passed, since no list mentioned it. The
	// asked set was already derived from IsValid; the forbidden set is the same
	// question one level down, and it had the same answer missing.
	says := durableStateSentences()
	forbids := func(stage hostexit.FaultStage) []string {
		other := []string{}
		for candidate, sentence := range says {
			if candidate != stage {
				other = append(other, sentence)
			}
		}
		sort.Strings(other)
		return other
	}
	stages := map[hostexit.FaultStage]struct {
		Says    string
		Forbids []string
	}{}
	for stage, sentence := range says {
		stages[stage] = struct {
			Says    string
			Forbids []string
		}{Says: sentence, Forbids: forbids(stage)}
	}

	// The arms of the impact switch a caller can reach, named by what selects
	// them. The blocking arm is excluded on purpose and pinned below: it speaks
	// of a refusal and makes no durable-state claim at all.
	arms := []struct {
		Name     string
		Mode     pastureruntime.FailureMode
		Declared pastureruntime.FailureMode
		Policy   hostexit.FaultPolicy
	}{
		{Name: "mode report-and-continue, fail-open", Mode: pastureruntime.FailureReportAndContinue, Declared: pastureruntime.FailureReportAndContinue, Policy: hostexit.FaultFailOpen},
		{Name: "mode report-and-continue, fail-closed with no exit-code channel", Mode: pastureruntime.FailureReportAndContinue, Declared: pastureruntime.FailureReportAndContinue, Policy: hostexit.FaultFailClosed},
		{Name: "mode throw-fail-fast, fail-closed", Mode: pastureruntime.FailureThrowFailFast, Declared: pastureruntime.FailureThrowFailFast, Policy: hostexit.FaultFailClosed},
		{Name: "mode throw-fail-fast, fail-open", Mode: pastureruntime.FailureThrowFailFast, Declared: pastureruntime.FailureThrowFailFast, Policy: hostexit.FaultFailOpen},
	}

	// THE TABLE MUST COVER EVERY DECLARED STAGE, and this check was missing from
	// the first version of this sweep — a sweep with an N-1 hole of exactly the
	// kind it exists to catch. Appending a stage and marking it valid left this
	// test green, because the table was WRITTEN DOWN rather than derived, so the
	// new stage was simply never asked about. The declared set is now read from
	// IsValid, so a stage cannot enter the type without entering the sweep.
	for candidate := 1; candidate < declaredScanBound; candidate++ {
		stage := hostexit.FaultStage(candidate)
		if !stage.IsValid() {
			continue
		}
		_, covered := stages[stage]
		assert.True(t, covered,
			"stage %q is declared and this sweep does not ask about it. Add the sentence it must "+
				"render and the sentences it must not, or the arms below go unchecked for it",
			stage.String())
	}

	for stage, expected := range stages {
		require.True(t, stage.IsValid(),
			"every stage in this table must be a declared stage; %d is not", uint8(stage))
		require.NotEmpty(t, stage.String(),
			"a declared stage must name itself for the diagnostic and the durable fault record")

		for _, arm := range arms {
			t.Run(stage.String()+" on "+arm.Name, func(t *testing.T) {
				outcome, ok := hostexit.ForFault(hostexit.Fault{
					Mode:         arm.Mode,
					DeclaredMode: arm.Declared,
					Policy:       arm.Policy,
					Stage:        stage,
					Continuation: hostexit.EmptyContinuation(),
					Cause:        errors.New("the sweep's cause"),
				})
				require.True(t, ok, "every pair in this table must map to an outcome")
				require.NotEmpty(t, outcome.Stderr, "a fault is never silent")

				assert.Contains(t, outcome.Stderr, expected.Says,
					"the diagnostic must state the durable state THIS CALLER DECLARED. The stage is "+
						"the caller's answer to one question — was the delivery written? — and an arm "+
						"that answers it differently tells the operator something the caller never said")
				// THE RULE RECOGNISES THE CLAIM, NOT A WORDING.
				//
				// Forbidding each other stage's SENTENCE recognised only that
				// sentence; reducing it to a distinctive PHRASE recognised only
				// the one paraphrase that had caught it. Both are rules tuned
				// to their own example: two further paraphrases of that same
				// leak passed tree-wide, one of them nothing but a change of
				// case.
				//
				// A durable-state claim cannot be made without naming what it
				// is about — the occurrence, the journal, or the recording of
				// one. So the impact clause has ITS OWN stage's sentence
				// removed, and whatever remains may not talk about those things
				// at all. Any rewording of any other stage's claim is caught,
				// because a rewording that drops every one of those words has
				// stopped making the claim.
				// THE WHOLE CLAUSE, BY EQUALITY. Three predecessors recognised
				// a wording and a reviewer walked through all three; this
				// composes what the clause MUST be from the same inputs the
				// renderer reads, so any rewording of any stage's claim fails
				// because the clause is no longer the expected one.
				assert.Equal(t,
					expectedImpactClause(hostexit.Fault{
						Mode: arm.Mode, DeclaredMode: arm.Declared, Policy: arm.Policy, Stage: stage,
					}, outcome.Exit, expected.Says),
					impactClauseOf(t, outcome.Stderr),
					"the impact clause must be EXACTLY the one this stage and this arm compose. A "+
						"durable-state claim cannot be recognised by its vocabulary — three rules "+
						"tried and a reviewer walked through all three — so it is recognised by "+
						"exhaustion instead: anything added, reworded or moved makes the clause "+
						"differ from the one that belongs here")
				assert.Contains(t, outcome.Stderr, "durable state "+stage.String(),
					"the machine-readable stage must agree with the sentence beside it")
			})
		}
	}
}

// TestTheBlockingArmMakesNoDurableStateClaim pins the one arm deliberately left
// out of the sweep above.
//
// It speaks of a REFUSAL, and it says nothing about whether the delivery was
// written. That is correct and must stay correct: a durable-state sentence
// added here would have to be true for every stage that can reach it, and the
// arm does not read the stage.
func TestTheBlockingArmMakesNoDurableStateClaim(t *testing.T) {
	t.Parallel()

	outcome, ok := hostexit.ForFault(hostexit.Fault{
		Mode:         pastureruntime.FailureExitTwoBlocks,
		DeclaredMode: pastureruntime.FailureExitTwoBlocks,
		Evidence:     pastureruntime.FailureEvidence{Source: citedSource},
		Policy:       hostexit.FaultFailClosed,
		Stage:        hostexit.FaultStageRecorded,
		Continuation: hostexit.EmptyContinuation(),
		Cause:        errors.New("the blocking arm's cause"),
	})
	require.True(t, ok)
	require.Equal(t, hostexit.ExitBlock, outcome.Exit, "this pair must reach the blocking arm")

	// The blocking arm may make NO durable-state claim at all, so nothing is
	// removed before the check: the whole impact clause must be free of them.
	assert.Equal(t,
		expectedImpactClause(hostexit.Fault{
			Mode:         pastureruntime.FailureExitTwoBlocks,
			DeclaredMode: pastureruntime.FailureExitTwoBlocks,
			Policy:       hostexit.FaultFailClosed,
			Stage:        hostexit.FaultStageRecorded,
		}, outcome.Exit, ""),
		impactClauseOf(t, outcome.Stderr),
		"the blocking arm's impact clause must be EXACTLY the refusal sentence. It does not read "+
			"the stage, so any durable-state claim written into it would be made on behalf of "+
			"every stage that can reach it — and equality forbids one however it is worded")
}

// TestTheEvidenceWordAgreesWithTheEvidenceAndWithTheAdviceBesideIt guards the
// "host evidence" clause, which every fault line carries and which nothing read.
//
// TWO MUTATIONS WERE ENTIRELY GREEN TREE-WIDE. Inventing a citation rendered a
// line that CONTRADICTS ITSELF — it cited Claude documentation on a Codex row
// while the advice beside it said the row carries no evidence. Erasing a
// present citation told a Claude operator the row can NEVER block, on the same
// line that invites the opt-in WHICH WILL BLOCK IT. The shipped bytes are
// correct today, so this is a MISSING GUARD and not an untruth: nothing anywhere
// named the word, asserted the absent case, or tied it to the advice.
//
// THE CLAIM IS AGREEMENT, NOT WORDING. The evidence word and the fail-closed
// advice are two statements about one fact, rendered from the same Fault by
// different code, so the guard requires them to say the same thing rather than
// requiring either to say a particular thing.
//
// MUTATION: render the evidence word from anything but fault.Evidence, or make
// the fail-closed advice ignore it. This test turns RED on the pair that
// disagrees.
func TestTheEvidenceWordAgreesWithTheEvidenceAndWithTheAdviceBesideIt(t *testing.T) {
	t.Parallel()

	// A blocking row under the opt-in is the pair where the two statements meet:
	// with evidence it BLOCKS, without evidence it continues and must say why.
	render := func(t *testing.T, evidence pastureruntime.FailureEvidence) hostexit.Outcome {
		t.Helper()
		outcome, ok := hostexit.ForFault(hostexit.Fault{
			Mode:         pastureruntime.FailureExitTwoBlocks,
			DeclaredMode: pastureruntime.FailureExitTwoBlocks,
			Evidence:     evidence,
			Policy:       hostexit.FaultFailClosed,
			Stage:        hostexit.FaultStageNotRecorded,
			Continuation: hostexit.EmptyContinuation(),
			Cause:        errors.New("the evidence guard's cause"),
		})
		require.True(t, ok)
		return outcome
	}

	cited := render(t, pastureruntime.FailureEvidence{Source: citedSource})
	assert.Contains(t, cited.Stderr, "host evidence "+citedSource,
		"a row that CITES its host evidence must render that citation, so a reader can go and "+
			"check the claim pasture is refusing on")
	assert.NotContains(t, cited.Stderr, "host evidence none",
		"and it must not also say it has none: the word is rendered from the evidence, so it "+
			"cannot disagree with it")
	assert.Equal(t, hostexit.ExitBlock, cited.Exit,
		"this pair must reach the blocking arm, or the agreement below is about the wrong line")
	assert.NotContains(t, cited.Stderr, "carries no host evidence for it",
		"THE TWO STATEMENTS MUST AGREE. This row cites evidence AND blocks; telling the reader it "+
			"carries none, on the line that just refused their operation, leaves them unable to "+
			"tell which half to believe")

	absent := render(t, pastureruntime.FailureEvidence{})
	assert.Contains(t, absent.Stderr, "host evidence none",
		"a row with NO citation must say so; nothing anywhere asserted the absent case, so the "+
			"word could have rendered anything at all for it")
	assert.NotContains(t, absent.Stderr, citedSource,
		"and it must not cite a document it does not have — an invented citation sends a reader to "+
			"another harness's documentation to explain a refusal it does not describe")
	assert.NotEqual(t, hostexit.ExitBlock, absent.Exit,
		"an unevidenced row may not refuse a user's operation, which is the rule the word exists "+
			"to report")
	assert.Contains(t, absent.Stderr, "carries no host evidence for it",
		"and the advice must agree with the word: this row continued BECAUSE it has no citation, "+
			"and that is the one action its operator has")
}
