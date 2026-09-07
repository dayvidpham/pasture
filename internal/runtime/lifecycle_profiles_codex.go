package runtime

// This file holds every Codex CLI lifecycle row: the closed event catalog, its
// identity fields, the mapping builder, the mappings table and the pinned
// contract constructor. A Codex row lives here and nowhere else, so that one
// harness can be edited without touching another harness or the shared
// helpers in lifecycle_profiles.go. The placement is enforced by a test that
// reads the declarations of every lifecycle_profiles*.go file.

// CodexLifecycleEvent is the closed native event catalog for the pinned Codex
// CLI lifecycle profile.
type CodexLifecycleEvent uint8

const (
	CodexEventSessionStart CodexLifecycleEvent = iota + 1
	CodexEventUserPromptSubmit
	CodexEventPreToolUse
	CodexEventPermissionRequest
	CodexEventPostToolUse
	CodexEventPreCompact
	CodexEventPostCompact
	CodexEventSubagentStart
	CodexEventSubagentStop
	CodexEventStop
	CodexEventSessionEnd
	CodexEventInterrupt
	codexLifecycleEventLimit
)

var codexLifecycleEventNames = [...]string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PermissionRequest",
	"PostToolUse",
	"PreCompact",
	"PostCompact",
	"SubagentStart",
	"SubagentStop",
	"Stop",
	"SessionEnd",
	"Interrupt",
}

func (e CodexLifecycleEvent) IsValid() bool { return e > 0 && e < codexLifecycleEventLimit }

func (e CodexLifecycleEvent) NativeName() string {
	if !e.IsValid() {
		return ""
	}
	return codexLifecycleEventNames[int(e)-1]
}

func (e CodexLifecycleEvent) String() string { return e.NativeName() }

// CodexLifecycleEvents returns the deterministic native catalog order used by
// codegen. The returned slice is a fresh copy.
func CodexLifecycleEvents() []CodexLifecycleEvent {
	events := make([]CodexLifecycleEvent, 0, int(codexLifecycleEventLimit)-1)
	for event := CodexEventSessionStart; event < codexLifecycleEventLimit; event++ {
		events = append(events, event)
	}
	return events
}

var (
	codexSessionIdentity  = nativeIdentity(IdentitySession, "session_id", true)
	codexTurnIdentity     = nativeIdentity(IdentityTurn, "turn_id", true)
	codexToolCallIdentity = nativeIdentity(IdentityToolCall, "tool_use_id", true)
	codexAgentIdentity    = nativeIdentity(IdentityAgent, "agent_id", true)
)

func codexLifecycleMapping(
	event CodexLifecycleEvent,
	semantic EventSemantic,
	blocking BlockingMode,
	mutation MutationMode,
	stopLoop StopLoopPolicy,
	turnScoped bool,
	evidence FailureEvidence,
	extraIdentities ...NativeIdentityField,
) LifecycleEventMapping {
	baseIdentities := []NativeIdentityField{codexSessionIdentity}
	if turnScoped {
		baseIdentities = append(baseIdentities, codexTurnIdentity)
	}
	return LifecycleEventMapping{
		nativeName:      event.NativeName(),
		semantic:        semantic,
		surface:         SurfaceCodexStrictCommandJSON,
		blocking:        blocking,
		identities:      identities(baseIdentities, extraIdentities...),
		mutation:        mutation,
		order:           OrderConcurrentNative,
		reconciliation:  ReconcileNoAdapterMerge,
		failure:         evidenceBoundFailure(blocking, evidence, FailureStrictExitTwoBlocks, FailureStrictHook),
		declaredFailure: declaredFailureArm(blocking, FailureStrictExitTwoBlocks, FailureStrictHook),
		evidence:        evidence,
		stopLoop:        stopLoop,
		preAction:       semantic == SemanticGateConsultation && event != CodexEventPostToolUse && event != CodexEventPostCompact,
	}
}

func codexLifecycleMappings() map[CodexLifecycleEvent]LifecycleEventMapping {
	// Input captures prove occurrence and identity, not a reason-bearing denial
	// channel. No row cites such a channel, so capability remains None. The
	// continue:true encoding is not evidence that continue:false enforces Deny.
	var unevidenced FailureEvidence

	gate := func(event CodexLifecycleEvent, mutation MutationMode, extra ...NativeIdentityField) LifecycleEventMapping {
		return codexLifecycleMapping(event, SemanticGateConsultation, Blocking, mutation, StopLoopNotApplicable, true, unevidenced, extra...)
	}
	return map[CodexLifecycleEvent]LifecycleEventMapping{
		CodexEventSessionStart:      codexLifecycleMapping(CodexEventSessionStart, SemanticObservation, NonBlocking, MutationNone, StopLoopNotApplicable, false, unevidenced),
		CodexEventUserPromptSubmit:  gate(CodexEventUserPromptSubmit, MutationNone),
		CodexEventPreToolUse:        gate(CodexEventPreToolUse, MutationInput, codexToolCallIdentity),
		CodexEventPermissionRequest: gate(CodexEventPermissionRequest, MutationNone),
		CodexEventPostToolUse:       gate(CodexEventPostToolUse, MutationOutput, codexToolCallIdentity),
		CodexEventPreCompact:        gate(CodexEventPreCompact, MutationNone),
		CodexEventPostCompact:       gate(CodexEventPostCompact, MutationNone),
		CodexEventSubagentStart:     codexLifecycleMapping(CodexEventSubagentStart, SemanticObservation, NonBlocking, MutationNone, StopLoopNotApplicable, true, unevidenced, codexAgentIdentity),
		CodexEventSubagentStop:      codexLifecycleMapping(CodexEventSubagentStop, SemanticGateConsultation, Blocking, MutationNone, StopLoopConsultWhenInactive, true, unevidenced, codexAgentIdentity),
		CodexEventStop:              codexLifecycleMapping(CodexEventStop, SemanticGateConsultation, Blocking, MutationNone, StopLoopConsultWhenInactive, true, unevidenced),
		CodexEventSessionEnd:        codexLifecycleMapping(CodexEventSessionEnd, SemanticObservation, NonBlocking, MutationNone, StopLoopNotApplicable, false, unevidenced),
		CodexEventInterrupt:         codexLifecycleMapping(CodexEventInterrupt, SemanticObservation, NonBlocking, MutationNone, StopLoopNotApplicable, true, unevidenced),
	}
}

// Codex0_153_0Lifecycle returns the immutable Codex CLI lifecycle table bound
// to the same exact host version and RuntimeContractID as Codex0_153_0.
func Codex0_153_0Lifecycle() LifecycleContract[CodexLifecycleEvent] {
	return mustLifecycleContract(Codex0_153_0(), CodexLifecycleEvents(), codexLifecycleMappings())
}
