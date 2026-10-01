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
//   - OpenCode accepts the canonical Pasture proceed response through its
//     generated plugin. Claude uses empty stdout: no hook directive. The
//     canonical Pasture "proceed" token is not a documented Claude enum.
//   - Codex's only lifecycle surface is a command-hook that reads exact bytes on
//     stdin and interprets a JSON continuation object on stdout. Because the ABI
//     is a native byte stream, the typed Go pipeline carries all the way to the
//     final native bytes here rather than delegating encoding to a shell or
//     Python shim.
//
// # Derivation of the exact Codex 0.153.0 native shapes
//
// The two Codex shapes are derived from the pinned Codex 0.153.0 command-hook
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
		if response.Decision() != backend.DecisionProceed {
			return nil, fmt.Errorf("nativeresponse.CodexContinuation: this byte adapter accepts only Proceed; no bytes were emitted; use the capability-checked Outcome encoder for a refusal")
		}
		return append([]byte(nil), codexProceedContinuation...), nil
	}
	return append([]byte(nil), codexObservationContinuation...), nil
}

// CanonicalProceed returns the canonical Pasture host response bytes a host
// reads on standard output (OpenCode): the marshaled host response
// object for a valid gate Proceed decision, or nil (no stdout) for an
// observation that produced no decision. A gate proceed emits the canonical
// Pasture response and an observation emits nothing.
//
// Callers MUST only invoke it after the durable receipt commit has completed,
// so that native bytes never precede persisted evidence.
func CanonicalProceed(response backend.HostResponse) ([]byte, error) {
	if !response.IsValid() {
		return nil, nil
	}
	if response.Decision() != backend.DecisionProceed {
		return nil, fmt.Errorf("nativeresponse.CanonicalProceed: the OpenCode byte adapter accepts only Proceed; no bytes were emitted; use the capability-checked Outcome encoder for a refusal")
	}
	encoded, err := response.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("nativeresponse.CanonicalProceed: marshal canonical host response: %w", err)
	}
	return encoded, nil
}

// ClaudeContinuation is the current Claude dispatch adapter. Both an evaluated
// Proceed and an observation emit empty stdout. Refusals cannot pass through a
// byte-only adapter because their exit status and stderr would be lost.
func ClaudeContinuation(response backend.HostResponse) ([]byte, error) {
	if response.IsValid() && response.Decision() != backend.DecisionProceed {
		return nil, fmt.Errorf("nativeresponse.ClaudeContinuation: a refusal needs exit status and stderr, not a byte-only continuation; no response was emitted; use EncodeClaude and preserve its complete Outcome")
	}
	return nil, nil
}

// UnsupportedResponseError means no source-backed native refusal channel is
// available. It is not a policy decision. The caller must use its fault path,
// not emit a guessed JSON object or treat the zero Outcome as exit 0.
type UnsupportedResponseError struct {
	Event      string
	Surface    pastureruntime.HookSurface
	Capability pastureruntime.ResponseCapability
	Kind       backend.DecisionKind
}

func (e *UnsupportedResponseError) Error() string {
	return fmt.Sprintf("nativeresponse: event %q on %s cannot encode %s with response capability %s; no evidenced native channel is implemented for this response, so nothing was emitted; use the fault continuation and add a source-backed channel with transport proofs before enabling this response", e.Event, e.Surface, e.Kind, e.Capability)
}

func unsupported(mapping pastureruntime.LifecycleEventMapping, kind backend.DecisionKind) error {
	return &UnsupportedResponseError{Event: mapping.NativeName(), Surface: mapping.Surface(), Capability: mapping.Response(), Kind: kind}
}

// NormalizeDecision runs the same mechanical capability downgrade as encoding,
// but returns the value that must be persisted BEFORE commit. It evaluates no
// policy or authority. The subsequent encoder must receive this same decision.
func NormalizeDecision(mapping pastureruntime.LifecycleEventMapping, decision backend.Decision) (backend.Decision, error) {
	response, err := backend.NewHostResponse(decision)
	if err != nil {
		return backend.Decision{}, err
	}
	kind, err := nativeDecision(mapping, response, mapping.Surface())
	if err != nil {
		return backend.Decision{}, err
	}
	reason := decision.Reason()
	if kind == backend.DecisionProceed && decision.Kind() != backend.DecisionProceed {
		reason = backend.ReasonUnenforcedDeny
	}
	return backend.NewDecision(kind, reason)
}

// nativeDecision validates the mapping and response before any bytes exist.
// Unenforced policy refusals use the host's Proceed identity. The policy caller
// must normalize their record to Proceed/ReasonUnenforcedDeny BEFORE commit;
// an encoder cannot rewrite durable evidence after the decision is committed.
func nativeDecision(mapping pastureruntime.LifecycleEventMapping, response backend.HostResponse, surfaces ...pastureruntime.HookSurface) (backend.DecisionKind, error) {
	if !mapping.IsValid() {
		return backend.DecisionKindUnset, fmt.Errorf("nativeresponse: the lifecycle mapping is zero or not validated; no response was emitted; obtain the exact event from its pinned runtime LifecycleContract.Mapping")
	}
	matched := false
	for _, surface := range surfaces {
		matched = matched || mapping.Surface() == surface
	}
	if !matched {
		return backend.DecisionKindUnset, fmt.Errorf("nativeresponse: event %q uses surface %s, which does not match this encoder; no bytes were emitted; select the per-harness encoder from the same dispatch row as the mapping", mapping.NativeName(), mapping.Surface())
	}
	if !response.IsValid() {
		if mapping.Semantic() == pastureruntime.SemanticObservation {
			return backend.DecisionProceed, nil
		}
		return backend.DecisionKindUnset, fmt.Errorf("nativeresponse: gate event %q has no constructed response; it was not evaluated, so no decision bytes were emitted; use the fault path or pass a backend.NewHostResponse value", mapping.NativeName())
	}
	kind := response.Decision()
	if response.IsEvaluationFault() && !mapping.Response().AllowsDeny() {
		return backend.DecisionKindUnset, unsupported(mapping, kind)
	}
	if kind == backend.DecisionDeny || kind == backend.DecisionRequireHuman {
		if !mapping.Response().AllowsDeny() {
			return backend.DecisionProceed, nil
		}
		if kind == backend.DecisionRequireHuman && !mapping.Response().AllowsAsk() {
			return backend.DecisionDeny, nil
		}
	}
	return kind, nil
}

// EncodeClaude is a pure Outcome encoder, not the current byte-only dispatch.
// An evidenced denial uses only the documented exit-2/stderr channel. Call it
// on the committed, capability-normalized response and preserve all streams.
func EncodeClaude(mapping pastureruntime.LifecycleEventMapping, response backend.HostResponse) (hostexit.Outcome, error) {
	kind, err := nativeDecision(mapping, response, pastureruntime.SurfaceClaudeCommandJSON)
	if err != nil {
		return hostexit.Outcome{}, err
	}
	switch kind {
	case backend.DecisionProceed:
		return hostexit.ForDecision(nil, hostexit.ExitContinue, ""), nil
	case backend.DecisionDeny:
		// Capability evidence alone cannot substitute for the documented exit
		// channel: only the effective exit mode may choose exit 2.
		if !mapping.Failure().BlocksByExitCode() || !mapping.Evidence().IsPresent() {
			return hostexit.Outcome{}, unsupported(mapping, kind)
		}
		return hostexit.ForDecision(nil, hostexit.ExitBlock, response.Reason().Message()), nil
	default:
		return hostexit.Outcome{}, unsupported(mapping, kind)
	}
}

// EncodeCodex preserves the observation and gate continuation identities.
// The current source contract does not prove a policy Deny channel. In
// particular, parser rejection of continue:false is not policy enforcement.
func EncodeCodex(mapping pastureruntime.LifecycleEventMapping, response backend.HostResponse) (hostexit.Outcome, error) {
	kind, err := nativeDecision(mapping, response, pastureruntime.SurfaceCodexStrictCommandJSON)
	if err != nil {
		return hostexit.Outcome{}, err
	}
	if kind != backend.DecisionProceed {
		return hostexit.Outcome{}, unsupported(mapping, kind)
	}
	if mapping.Semantic() == pastureruntime.SemanticObservation {
		return hostexit.ForDecision(append([]byte(nil), codexObservationContinuation...), hostexit.ExitContinue, ""), nil
	}
	return hostexit.ForDecision(append([]byte(nil), codexProceedContinuation...), hostexit.ExitContinue, ""), nil
}

// EncodeOpenCode emits the canonical proceed body for an unenforced gate and
// the typed refusal for an evidenced named-surface row. A policy Deny on a
// row whose Response().AllowsDeny() is emitted as the canonical
// {decision:deny,reason} body at exit 0, which the generated permission hook
// enforces through the host effect mutation
// (internal/codegen/opencode_hooks.go). The capability alone does not enable
// the channel: this encoder is its required companion, and an evaluation
// fault is never dressed as a refusal — it carries no policy verdict, so it
// is rejected even where a denial would be emitted.
func EncodeOpenCode(mapping pastureruntime.LifecycleEventMapping, response backend.HostResponse) (hostexit.Outcome, error) {
	kind, err := nativeDecision(mapping, response, pastureruntime.SurfaceOpenCodeNamedOutput, pastureruntime.SurfaceOpenCodeCatchAllSSE)
	if err != nil {
		return hostexit.Outcome{}, err
	}
	switch kind {
	case backend.DecisionProceed:
		if mapping.Semantic() == pastureruntime.SemanticObservation {
			return hostexit.ForDecision(nil, hostexit.ExitContinue, ""), nil
		}
		return hostexit.ForDecision(append([]byte(nil), canonicalProceedContinuation...), hostexit.ExitContinue, ""), nil
	case backend.DecisionDeny:
		if response.IsEvaluationFault() {
			return hostexit.Outcome{}, unsupported(mapping, kind)
		}
		if !mapping.Response().AllowsDeny() {
			return hostexit.Outcome{}, unsupported(mapping, kind)
		}
		encoded, err := response.MarshalJSON()
		if err != nil {
			return hostexit.Outcome{}, fmt.Errorf("nativeresponse.EncodeOpenCode: marshal evidenced deny response for event %q: %w", mapping.NativeName(), err)
		}
		return hostexit.ForDecision(encoded, hostexit.ExitContinue, ""), nil
	default:
		return hostexit.Outcome{}, unsupported(mapping, kind)
	}
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
//     continuation of a gate or an explicit human response is
//     {"decision":"proceed"}. An OpenCode OBSERVATION takes NO bytes, the same
//     as its successful path: its callbacks never read standard output, and a
//     decision word for an event class that has no decision would say more
//     after a failure than after a success.
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
// byte-identical to a Proceed backend.HostResponse.MarshalJSON. The OpenCode
// generated plugin accepts exactly these bytes; Claude emits no directive.
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
		if semantic == pastureruntime.SemanticObservation {
			// An observation has no decision to report, and the OpenCode
			// observation callbacks never read standard output. Saying nothing
			// is what a SUCCESSFUL observation does, so a failed one says the
			// same. It is also safe on both sides of the plugin version skew
			// the belt exists for: an older plugin ignores observation output
			// entirely, and a newer one reads an empty body at exit 0 as "not
			// evaluated, continue".
			return hostexit.EmptyContinuation(), nil
		}
		// Gates, explicit human responses and undeclared events take the
		// canonical object, because a named callback of the generated plugin
		// accepts exactly those bytes.
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
