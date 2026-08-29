// Package hostexit decides what the pasture lifecycle hook tells its host.
//
// It is the SOLE exit authority of the hook command: every path out of that
// command, including a panic, produces one Outcome, and the process exits with
// that Outcome and nothing else. Before this package existed the command
// printed the error and returned nil, so a hook that could not evaluate a gate
// exited 0 with empty stdout, which every host reads as "proceed". A refusal
// that reads as a proceed is worse than no hook at all.
//
// The package is pure: it reads no environment, no clock and no store, so the
// whole decision table is testable as a table.
//
// Two paths lead here and they must never be confused.
//
//   - ForFault maps an INTERNAL fault: a storage error, a validation refusal, a
//     deadline expiry or a recovered panic. Pasture could not evaluate the
//     event. The default is to fail OPEN, so a broken hook does not stop the
//     user working. Only an explicit fail-closed policy turns a fault into a
//     block, and only for a row that cites host evidence for its blocking exit
//     code.
//   - ForDecision carries an evaluated policy decision, including a Deny. A
//     Deny is an ANSWER, not a fault, so it never passes through ForFault and
//     is never weakened by the fault policy or by missing evidence.
package hostexit

import (
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

// ExitStatus is the closed set of process exit statuses the hook may leave
// with. The zero value is unset and has no process exit code, so a caller that
// forgets to decide cannot silently exit 0 and be read as a proceed.
type ExitStatus uint8

const (
	// ExitStatusUnset is the zero value. It is not a status and has no code.
	ExitStatusUnset ExitStatus = iota
	// ExitContinue leaves the host free to continue. Process exit code 0.
	ExitContinue
	// ExitNonBlockingError reports a hook problem that must not stop the host.
	// Process exit code 1.
	ExitNonBlockingError
	// ExitBlock tells the host to refuse the operation. Process exit code 2.
	ExitBlock
)

// IsValid reports whether the status is one of the three declared statuses.
func (s ExitStatus) IsValid() bool {
	return s >= ExitContinue && s <= ExitBlock
}

// Code returns the process exit code of the status. The second result is false
// for the unset zero value, which has no code.
func (s ExitStatus) Code() (int, bool) {
	switch s {
	case ExitContinue:
		return 0, true
	case ExitNonBlockingError:
		return 1, true
	case ExitBlock:
		return 2, true
	default:
		return 0, false
	}
}

// String names the status for a diagnostic. The unset value has no name.
func (s ExitStatus) String() string {
	switch s {
	case ExitContinue:
		return "continue"
	case ExitNonBlockingError:
		return "non-blocking-error"
	case ExitBlock:
		return "block"
	default:
		return ""
	}
}

// Outcome is everything the hook process gives its host: the exit status, the
// bytes written to stdout, and the diagnostic written to stderr.
//
// Stdout carries a native continuation only when the host contract asks for
// one. A fault never writes a continuation, because pasture has nothing
// truthful to say about an event it could not evaluate.
type Outcome struct {
	Exit   ExitStatus
	Stdout []byte
	Stderr string
}

// FaultPolicy says what an internal fault does to the host. The zero value is
// unset and is refused, so the policy is always a deliberate choice made where
// the environment is parsed.
type FaultPolicy uint8

const (
	// FaultPolicyUnset is the zero value. It is not a policy.
	FaultPolicyUnset FaultPolicy = iota
	// FaultFailOpen lets the host continue when pasture cannot evaluate the
	// event. This is the default: a broken hook must not stop a user working.
	FaultFailOpen
	// FaultFailClosed blocks the host when pasture cannot evaluate an event
	// whose row cites host evidence for a blocking exit code. It is opt-in,
	// for a user who would rather stop than proceed unevaluated.
	FaultFailClosed
)

// IsValid reports whether the policy is one of the two declared policies.
func (p FaultPolicy) IsValid() bool {
	return p == FaultFailOpen || p == FaultFailClosed
}

// String names the policy for a diagnostic. The unset value has no name.
func (p FaultPolicy) String() string {
	switch p {
	case FaultFailOpen:
		return "fail-open"
	case FaultFailClosed:
		return "fail-closed"
	default:
		return ""
	}
}

// ForFault maps an internal fault to the Outcome the host receives.
//
// The table is small and total:
//
//   - fail-open, any mode: continue, with the diagnostic on stderr.
//   - fail-closed, a mode that blocks by exit code, WITH evidence: block.
//   - fail-closed, anything else: continue, with the diagnostic on stderr.
//
// The second result is false when there is nothing to map: a nil error, an
// unset or invalid failure mode, or an unset or invalid policy. A false result
// is a programming error at the call site, never a host outcome, so the caller
// must treat it as one and must not fall through to a silent exit 0.
//
// ForFault never encodes a policy Deny. A Deny is an evaluated answer and goes
// through ForDecision, where neither the fault policy nor missing evidence can
// weaken it.
func ForFault(
	mode pastureruntime.FailureMode,
	evidence pastureruntime.FailureEvidence,
	policy FaultPolicy,
	err error,
) (Outcome, bool) {
	if err == nil || !mode.IsValid() || !policy.IsValid() {
		return Outcome{}, false
	}

	blocks := policy == FaultFailClosed && mode.BlocksByExitCode() && evidence.IsPresent()
	exit := ExitContinue
	if blocks {
		exit = ExitBlock
	}

	return Outcome{
		Exit:   exit,
		Stdout: nil,
		Stderr: faultDiagnostic(mode, evidence, policy, exit, err).Error(),
	}, true
}

// faultDiagnostic composes the text the user reads on stderr. It states what
// went wrong, why it matters, where in pasture it was decided, which step of
// the hook it happened in, what it means for the host, and how to change the
// outcome. The cause is preserved verbatim, and the caller puts the hook event
// and the failing step into it.
func faultDiagnostic(
	mode pastureruntime.FailureMode,
	evidence pastureruntime.FailureEvidence,
	policy FaultPolicy,
	exit ExitStatus,
	cause error,
) *ir.Diagnostic {
	impact := "the host continues, and this lifecycle event is not evaluated"
	if exit == ExitBlock {
		impact = "the host refuses the operation, because this event is configured to fail closed and the host documents that it blocks on this exit code"
	}

	fix := "read the cause below and fix the reported condition; to make an evaluation fault of an evidenced blocking event stop the host instead of letting it continue, set PASTURE_HOOK_FAIL_CLOSED=1"
	if exit == ExitBlock {
		fix = "read the cause below and fix the reported condition; to let the host continue through an evaluation fault, unset PASTURE_HOOK_FAIL_CLOSED"
	}

	return &ir.Diagnostic{
		What: "pasture could not evaluate this lifecycle hook event",
		Why: "an internal fault stopped the evaluation before it produced a decision, and a hook that cannot evaluate must say so rather than stay silent, " +
			"because silence reads as a proceed",
		Where: "internal/lifecycle/hostexit.ForFault",
		Phase: "hook lifecycle fault handling, after the event was identified and before any decision was written; " +
			"declared failure mode " + mode.String() + ", fault policy " + policy.String() +
			", host evidence " + evidenceState(evidence) + ", exit " + exit.String(),
		Impact: impact,
		Fix:    fix,
		Cause:  cause,
	}
}

func evidenceState(evidence pastureruntime.FailureEvidence) string {
	if evidence.IsPresent() {
		return evidence.Source
	}
	return "none"
}

// ForDecision carries an evaluated decision to the host. The caller supplies
// the native bytes the host contract asks for, the exit status the decision
// maps to, and the text the user reads.
//
// A decision is never re-judged here: the fault policy and the failure evidence
// do not appear, so an evaluated Deny cannot be turned into a proceed by a
// missing citation or by the fail-open default.
func ForDecision(native []byte, exit ExitStatus, stderr string) Outcome {
	return Outcome{Exit: exit, Stdout: native, Stderr: stderr}
}
