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
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
)

const nativeResponseWhere = "Encoding the native host continuation for a committed lifecycle event (internal/lifecycle/nativeresponse/nativeresponse.go in nativeresponse.Encode)."

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

// Encode returns the exact native continuation bytes a host reads on standard
// output for one committed lifecycle event, or nil when the harness emits no
// stdout for the event.
//
// The response argument is the constructor-built backend.HostResponse returned
// by the durable lifecycle path; a valid response denotes a gate Proceed
// decision and an invalid (zero) response denotes an observation that produced
// no decision. Callers MUST only invoke Encode after the durable receipt commit
// has completed, so that native bytes never precede persisted evidence.
func Encode(harness ir.HarnessID, response backend.HostResponse) ([]byte, error) {
	switch harness {
	case ir.HarnessCodex:
		// Codex's command-hook ABI reads a JSON continuation object on stdout for
		// every configured hook, so the typed pipeline emits native bytes here.
		if response.IsValid() {
			return append([]byte(nil), codexProceedContinuation...), nil
		}
		return append([]byte(nil), codexObservationContinuation...), nil
	case ir.HarnessClaudeCode, ir.HarnessOpenCode:
		// Claude and OpenCode carry the canonical Pasture host response object; a
		// gate proceed emits it and an observation emits nothing. These bytes are
		// byte-identical to M2.
		if !response.IsValid() {
			return nil, nil
		}
		encoded, err := response.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("nativeresponse.Encode: marshal canonical host response for harness %q: %w", harness, err)
		}
		return encoded, nil
	default:
		return nil, encodeError(
			fmt.Sprintf("Harness %q has no native response encoder.", harness),
			"Native continuation encoding is defined only for the reviewed lifecycle harnesses.",
			"No native host continuation was produced for this event.",
			"Emit responses only for a harness present in the generated lifecycle support report.",
		)
	}
}

// encodeError builds an actionable structured error for an unsupported harness.
func encodeError(what, why, impact, fix string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     what,
		Why:      why,
		Where:    nativeResponseWhere,
		Impact:   impact,
		Fix:      fix,
	}
}
