package hostcontract

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

// Codex native identity field IDs. Only the correlation fields consumed by the
// two authentically observed Codex 0.153.0 events are declared here; all other
// payload content is preserved byte-exact in the retained evidence body rather
// than being lifted into typed native fields.
const (
	fCodexSessionID model.NativeFieldID = 5001 + iota
	fCodexTurnID
	fCodexToolUseID
)

var codexFields = []Field{
	{fCodexSessionID, "FieldCodexSessionID", "session_id"},
	{fCodexTurnID, "FieldCodexTurnID", "turn_id"},
	{fCodexToolUseID, "FieldCodexToolUseID", "tool_use_id"},
}

// Codex0_153_0 is the source-reprofiled selected-contract for Codex 0.153.0.
//
// It mirrors the self-contained Claude host-contract shape (a closed native
// catalog defined in source) rather than the runtime-derived OpenCode shape.
//
// Only SessionStart and PreToolUse carry native identities and are eligible for
// authentic-evidence proof; the remaining closed-catalog entries are
// source-derived metadata and declare no identities, exactly as the OpenCode
// catalog leaves unproven events identity-free. SessionStart is a
// configured-hook ingress smoke observation and is never treated as
// semantically identical to the OpenCode session.created aggregate.
//
// Known divergence, and why it is inert. This catalog and the runtime Codex
// profile (internal/runtime/lifecycle_profiles.go) both describe the Codex
// event set, and they disagree on the 9 events that have no authentic capture,
// which are every Codex gate plus the SessionEnd and SubagentStart observations. This source declares no identities for them,
// and it declares a BLOCKING failure mode for all 7 of its gate rows while the
// runtime profile now declares none: a blocking exit code needs a citation, and
// the Codex rows carry none yet. So this source OVER-CLAIMS blocking relative
// to the runtime profile; it does not merely simplify it. The Codex frontend
// (internal/lifecycle/frontend/codex) binds ONLY the 2 authenticity-proven
// events and rejects the other 9, so the diverging metadata never reaches
// ingest. The runtime profile is the authority for non-ingress event semantics,
// and it is the one to believe when the two disagree.
//
// Re-derive this catalog from the runtime profile once every Codex event has an
// authentic capture and a production proof. Until then, keep the two
// definitions separate and keep this note truthful.
func Codex0_153_0() Contract {
	// observe builds a non-blocking, report-and-continue catalog event with no
	// declared identities (source-derived metadata only).
	observe := func(kind model.ContractEventKind, symbol, name string) Event {
		return Event{
			Kind: kind, Symbol: symbol, Name: name,
			Blocking: NonBlocking, Mutation: MutationNone,
			Failure: pastureruntime.FailureReportAndContinue, StopLoop: StopLoopNotApplicable,
		}
	}
	// gate builds a blocking, exit-two-blocks catalog event with no declared
	// identities (source-derived metadata only).
	gate := func(kind model.ContractEventKind, symbol, name string, mutation MutationMode, stop StopLoopPolicy) Event {
		return Event{
			Kind: kind, Symbol: symbol, Name: name,
			Blocking: Blocking, Mutation: mutation,
			Failure: pastureruntime.FailureExitTwoBlocks, StopLoop: stop,
		}
	}

	sessionStart := observe(1, "EventCodexSessionStart", "SessionStart")
	sessionStart.Fields = []model.NativeFieldID{fCodexSessionID}
	sessionStart.Identities = []Identity{
		{Field: fCodexSessionID, Binding: model.BindingSession, Required: true},
	}

	preToolUse := gate(3, "EventCodexPreToolUse", "PreToolUse", MutationInput, StopLoopNotApplicable)
	preToolUse.Fields = []model.NativeFieldID{fCodexSessionID, fCodexTurnID, fCodexToolUseID}
	preToolUse.Identities = []Identity{
		{Field: fCodexSessionID, Binding: model.BindingSession, Required: true},
		{Field: fCodexTurnID, Binding: model.BindingTurn, Required: true},
		{Field: fCodexToolUseID, Binding: model.BindingToolCall, Required: true},
	}

	// SessionEnd is emitted by the host at 0.153.0 (codex-rs/core/src/hook_runtime.rs:378-392,
	// root session only) and still at 0.153.0 (:464-478). It was absent from this catalogue
	// although the host emitted it all along, so the catalogue was 10 of 11. The emitter
	// declares session_id, transcript_path, cwd, hook_event_name and reason
	// (codex-rs/hooks/src/events/session_end.rs:64-68): that is the cited payload SHAPE, not a
	// declared identity. Like every other unproven Codex row, this row declares no identity and
	// no payload field until an authentic capture proves what the host writes on the wire.
	sessionEnd := observe(11, "EventCodexSessionEnd", "SessionEnd")

	events := []Event{
		sessionStart,
		gate(2, "EventCodexUserPromptSubmit", "UserPromptSubmit", MutationNone, StopLoopNotApplicable),
		preToolUse,
		gate(4, "EventCodexPermissionRequest", "PermissionRequest", MutationNone, StopLoopNotApplicable),
		gate(5, "EventCodexPostToolUse", "PostToolUse", MutationNone, StopLoopNotApplicable),
		gate(6, "EventCodexPreCompact", "PreCompact", MutationNone, StopLoopNotApplicable),
		observe(7, "EventCodexPostCompact", "PostCompact"),
		observe(8, "EventCodexSubagentStart", "SubagentStart"),
		gate(9, "EventCodexSubagentStop", "SubagentStop", MutationNone, StopLoopConsultWhenInactive),
		gate(10, "EventCodexStop", "Stop", MutationNone, StopLoopConsultWhenInactive),
		sessionEnd,
	}
	return Contract{Version: "0.153.0", Fields: append([]Field(nil), codexFields...), Events: events}
}
