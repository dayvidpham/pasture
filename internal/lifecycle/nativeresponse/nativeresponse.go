// Package nativeresponse is the per-target emission backend for the lifecycle
// pipeline: it maps the provider-neutral, constructor-built backend.HostResponse
// into the exact native continuation bytes a host reads from a command-hook's
// standard output.
//
// # Why a per-target backend (compiler lesson)
//
// Semantic operation selection is decided once, in Go, at and after the shared
// middle-end (middleend.Derive). Everything downstream of that decision is a
// mechanical, typed emission step — the analogue of a compiler backend lowering
// one shared IR to per-target machine code. The native shape a host expects is
// therefore a property of the host's mandated extension medium:
//
//   - OpenCode and Claude accept the canonical Pasture host response
//     {"decision":"proceed"} because their transports already carry a typed
//     object protocol (OpenCode: an in-process TypeScript plugin object return;
//     Claude: the documented hook JSON). Their bytes are unchanged from M2.
//   - Codex's only lifecycle surface is a command-hook that reads exact bytes on
//     stdin and interprets a JSON continuation object on stdout. Because the ABI
//     is a native byte stream, the typed Go pipeline carries all the way to the
//     final native bytes here rather than delegating encoding to a shell or
//     Python shim.
//
// # Derivation of the exact Codex 0.146.0 native shapes
//
// The two Codex shapes are derived from the pinned Codex 0.146.0 command-hook
// output contract (inspected source revision
// d6407d735942c7cfc996aa2bc7d0f97fc8f0e4bf):
//
//   - hooks/src/schema.rs: HookUniversalOutputWire carries `continue` (a bool
//     that defaults to true via default_continue) plus optional stopReason,
//     suppressOutput, and systemMessage; the wire denies unknown fields.
//   - hooks/src/engine/output_parser.rs: an empty or non-object stdout parses to
//     "no directives"; a blocking PreToolUse hook is rejected unless
//     continue==true (unsupported_pre_tool_use_universal), and a bare
//     {"continue":true} yields decision=None, updatedInput=None → Proceed.
//
// From those facts:
//
//   - PreToolUse (blocking gate, SemanticGateConsultation): a Proceed decision
//     is the minimal universal-continue object {"continue":true}. It carries no
//     block decision, no permissionDecision, and no updatedInput, so the host
//     proceeds with the tool call unchanged.
//   - SessionStart (non-blocking observation, SemanticObservation): the native
//     default continuation object {}. Every universal field defaults (continue
//     defaults to true), and an observation contributes no hookSpecificOutput,
//     so the empty object is the well-formed "observed; continue; no directives"
//     value the host applies its defaults to.
//
// The gate-versus-observation distinction is carried by the HostResponse itself:
// only a gate consultation produces a valid Proceed response, so a valid
// response selects the Proceed continuation and an invalid (zero) response
// selects the observation default.
package nativeresponse

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

// Native continuation byte shapes. These are the authoritative golden bytes;
// the golden-byte tests pin them and record the contract derivation above.
var (
	// codexProceedContinuation is the Codex native Proceed continuation for a
	// blocking gate (PreToolUse): continue with the tool call unchanged.
	codexProceedContinuation = []byte(`{"continue":true}`)
	// codexObservationContinuation is the Codex native default continuation for a
	// non-blocking observation hook (SessionStart): apply host defaults.
	codexObservationContinuation = []byte(`{}`)
)

// CodexContinuation returns the exact Codex native continuation bytes a host
// reads on standard output for one committed lifecycle event: {"continue":true}
// for a valid gate Proceed response and {} for an observation default. Codex's
// command-hook ABI reads a JSON continuation object on stdout for every
// configured hook, so the typed pipeline emits native bytes here. It is total
// over its input (no harness argument, no error path for a well-formed
// response), so the registry row references it directly for the Codex harness.
//
// Callers MUST only invoke it after the durable receipt commit has completed,
// so that native bytes never precede persisted evidence.
func CodexContinuation(response backend.HostResponse) ([]byte, error) {
	if response.IsValid() {
		return append([]byte(nil), codexProceedContinuation...), nil
	}
	return append([]byte(nil), codexObservationContinuation...), nil
}

// CanonicalProceed returns the canonical Pasture host response bytes a host
// reads on standard output (Claude and OpenCode): the marshaled host response
// object for a valid gate Proceed decision, or nil (no stdout) for an
// observation that produced no decision. Claude and OpenCode carry the canonical
// Pasture host response object; a gate proceed emits it and an observation emits
// nothing. These bytes are byte-identical to M2.
//
// Callers MUST only invoke it after the durable receipt commit has completed,
// so that native bytes never precede persisted evidence.
func CanonicalProceed(response backend.HostResponse) ([]byte, error) {
	if !response.IsValid() {
		return nil, nil
	}
	encoded, err := response.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("nativeresponse.CanonicalProceed: marshal canonical host response: %w", err)
	}
	return encoded, nil
}

// # The fault continuation: what "fail open" costs in bytes
//
// A pasture fault means the event was NOT evaluated. Under the fail-open
// default the host must still proceed, and on two of the three harnesses
// "proceed" is a byte shape, not an exit code:
//
//   - Claude Code reads an EMPTY standard output as "the hook has nothing to
//     say", so its fault continuation is no bytes at all.
//   - Codex parses a JSON continuation object. A blocking gate is refused
//     unless continue == true, so a gate's fault continuation is
//     {"continue":true}; an observation contributes no directives, so its
//     fault continuation is the default object {}.
//   - OpenCode's named callbacks are validated by the pasture-generated plugin,
//     which accepts exactly the canonical response object, so the fault
//     continuation is {"decision":"proceed"}. Its observation callbacks ignore
//     standard output, so the same bytes are harmless there and are used for
//     every OpenCode event.
//
// These are the SAME bytes an evaluated proceed emits. That is the point: the
// host cannot be asked to distinguish them, because the only channel it reads
// is the continuation. The distinction is kept where it can be kept truthfully
// — the diagnostic on standard error says the event was not evaluated, and the
// durable fault record classifies the invocation as a FAULT. Nothing in this
// path writes a decision record, so a fault never becomes an evaluated answer.
//
// An event the build does not declare has no semantic. Such an invocation comes
// from a generated hook that does not match this binary, so the harness's
// UNIVERSALLY accepted continuation is used: a gate must never be stopped
// because pasture could not name its event.

// canonicalProceedContinuation is the canonical Pasture host response body,
// byte-identical to backend.HostResponse.MarshalJSON. Claude and OpenCode read
// this object; the OpenCode generated plugin accepts exactly these bytes.
var canonicalProceedContinuation = []byte(`{"decision":"proceed"}`)

// FaultContinuation returns the continuation a host reads as "you may continue"
// when pasture could NOT evaluate the event, for one harness and one declared
// event semantic. Pass the zero EventSemantic for an event this build does not
// declare.
//
// The error names an unsupported harness. A caller that cannot name the host
// has no bytes to write, so it must fall back to the empty continuation rather
// than guess a shape.
func FaultContinuation(harness ir.HarnessID, semantic pastureruntime.EventSemantic) (hostexit.Continuation, error) {
	switch harness {
	case ir.HarnessClaudeCode:
		// Claude's hook contract reads empty stdout as "nothing to say", which
		// is exactly the fail-open claim.
		return hostexit.EmptyContinuation(), nil
	case ir.HarnessCodex:
		if semantic == pastureruntime.SemanticObservation {
			return hostexit.ContinuationOf(codexObservationContinuation), nil
		}
		// Gates, explicit human responses and undeclared events all take the
		// universal continue object: it is accepted for every Codex hook and is
		// the only shape a blocking gate accepts.
		return hostexit.ContinuationOf(codexProceedContinuation), nil
	case ir.HarnessOpenCode:
		return hostexit.ContinuationOf(canonicalProceedContinuation), nil
	default:
		return hostexit.Continuation{}, fmt.Errorf(
			"nativeresponse.FaultContinuation: harness %q has no fail-open continuation, because this build has no native response contract for it; "+
				"this happened while mapping a lifecycle hook fault, after the event coordinates were read and before the host was answered; "+
				"the caller must fall back to an empty continuation rather than guess a byte shape the host would reject; "+
				"invoke the hook with a harness listed in the generated lifecycle support report",
			harness)
	}
}
