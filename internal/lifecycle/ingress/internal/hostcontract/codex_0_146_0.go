package hostcontract

import "github.com/dayvidpham/pasture/internal/lifecycle/model"

// Codex native identity field IDs. Only the correlation fields consumed by the
// two authentically observed Codex 0.146.0 events are declared here; all other
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

// Codex0_146_0 is the source-reprofiled selected-contract for Codex 0.146.0.
//
// It mirrors the self-contained Claude host-contract shape (a closed native
// catalog defined in source) rather than the runtime-derived OpenCode shape.
// This keeps the compile-time generator independent of the parallel M3-SLICE-1
// runtime profile replacement (IP-1): the registration surface consumed by
// ingress/frontend is generated from this source alone.
//
// Only SessionStart and PreToolUse carry native identities and are eligible for
// authentic-evidence proof; the remaining closed-catalog entries are
// source-derived metadata and declare no identities, exactly as the OpenCode
// catalog leaves unproven events identity-free. SessionStart is a
// configured-hook ingress smoke observation and is never treated as
// semantically identical to the OpenCode session.created aggregate.
//
// Deliberate decoupling (do NOT unify): the Codex 0.146.0 event catalog is
// defined here AND, separately, in the runtime Codex profile that SLICE-1 owns
// and that surfaces per-event semantics into .codex lifecycle METADATA. The two
// intentionally disagree on the 8 non-proven events (this source simplifies
// their modes and declares no identities). That divergence is inert: the Codex
// frontend (internal/lifecycle/frontend/codex) binds ONLY the 2 authenticity-
// proven events and rejects the other 8, so their metadata is never ingested.
// The runtime profile is the authority for non-ingress event semantics; these 8
// source-derived entries carry no authenticity claim and may lag the runtime
// profile without any effect. Keeping this catalog self-contained (like Claude)
// is what isolates SLICE-2 ingress from SLICE-1's runtime profile (IP-1); a
// later wave may re-derive it from the runtime profile once IP-1 is settled.
func Codex0_146_0() Contract {
	// observe builds a non-blocking, report-and-continue catalog event with no
	// declared identities (source-derived metadata only).
	observe := func(kind model.ContractEventKind, symbol, name string) Event {
		return Event{
			Kind: kind, Symbol: symbol, Name: name,
			Blocking: NonBlocking, Mutation: MutationNone,
			Failure: FailureReportAndContinue, StopLoop: StopLoopNotApplicable,
		}
	}
	// gate builds a blocking, exit-two-blocks catalog event with no declared
	// identities (source-derived metadata only).
	gate := func(kind model.ContractEventKind, symbol, name string, mutation MutationMode, stop StopLoopPolicy) Event {
		return Event{
			Kind: kind, Symbol: symbol, Name: name,
			Blocking: Blocking, Mutation: mutation,
			Failure: FailureExitTwoBlocks, StopLoop: stop,
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
	}
	return Contract{Version: "0.146.0", Fields: append([]Field(nil), codexFields...), Events: events}
}
