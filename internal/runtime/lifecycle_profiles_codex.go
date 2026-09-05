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
	}
}

func codexLifecycleMappings() map[CodexLifecycleEvent]LifecycleEventMapping {
	// No Codex row cites evidence in THIS revision, because the citation work is
	// limited here to the four documented Claude rows. Every Codex gate
	// therefore runs as report-and-continue until the Codex coverage work fills
	// the citation in.
	//
	// The evidence itself is NOT missing. The Codex command-hook
	// output contract IS committed in this repository, with its inspected source
	// revision, in internal/lifecycle/nativeresponse/nativeresponse.go: it
	// records that a blocking hook is rejected unless continue == true. That
	// file is the citation the Codex coverage work is expected to use. Do not
	// read this comment as a reason to run a live capture campaign for a fact
	// the repository already holds.
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
		CodexEventSessionEnd:        codexUnprovenObservationMapping(CodexEventSessionEnd, unevidenced),
		CodexEventInterrupt:         codexUnprovenObservationMapping(CodexEventInterrupt, unevidenced),
	}
}

// codexUnprovenObservationMapping builds a non-blocking observation row that
// declares NO identity, so its IdentityPolicy is None. A declared identity is a
// claim the product acts on (the L2 content guard, the frontend Bind), and this
// tree derives such claims from authentic captures only. SessionEnd's emitter
// names session_id, but no capture has shown what the host writes on the wire,
// so the row stays identity-free until one does; the same rule gave the other
// unproven Codex rows their shape in the host contract.
func codexUnprovenObservationMapping(event CodexLifecycleEvent, evidence FailureEvidence) LifecycleEventMapping {
	return LifecycleEventMapping{
		nativeName:      event.NativeName(),
		semantic:        SemanticObservation,
		surface:         SurfaceCodexStrictCommandJSON,
		blocking:        NonBlocking,
		identities:      identities(nil),
		mutation:        MutationNone,
		order:           OrderConcurrentNative,
		reconciliation:  ReconcileNoAdapterMerge,
		failure:         evidenceBoundFailure(NonBlocking, evidence, FailureStrictExitTwoBlocks, FailureStrictHook),
		declaredFailure: declaredFailureArm(NonBlocking, FailureStrictExitTwoBlocks, FailureStrictHook),
		evidence:        evidence,
		stopLoop:        StopLoopNotApplicable,
	}
}

// Codex0_153_0Lifecycle returns the immutable Codex CLI lifecycle table bound
// to the same exact host version and RuntimeContractID as Codex0_153_0.
func Codex0_153_0Lifecycle() LifecycleContract[CodexLifecycleEvent] {
	return mustLifecycleContract(Codex0_153_0(), CodexLifecycleEvents(), codexLifecycleMappings())
}
