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

	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
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
