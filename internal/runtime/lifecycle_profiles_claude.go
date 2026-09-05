package runtime

// This file holds every Claude Code lifecycle row: the closed event catalog,
// its identity fields, the mapping builder, the mappings table and the pinned
// contract constructor. A Claude row lives here and nowhere else, so that one
// harness can be edited without touching another harness or the shared
// helpers in lifecycle_profiles.go. The placement is enforced by a test that
// reads the declarations of every lifecycle_profiles*.go file.

// ClaudeLifecycleEvent is the closed native event catalog for the pinned Claude
// Code lifecycle profile. Its numeric representation prevents arbitrary native
// event strings from entering the semantic mapping API.
type ClaudeLifecycleEvent uint8

const (
	ClaudeEventSessionStart ClaudeLifecycleEvent = iota + 1
	ClaudeEventSetup
	ClaudeEventSessionEnd
	ClaudeEventUserPromptSubmit
	ClaudeEventUserPromptExpansion
	ClaudeEventStop
	ClaudeEventStopFailure
	ClaudeEventPreToolUse
	ClaudeEventPermissionRequest
	ClaudeEventPermissionDenied
	ClaudeEventPostToolUse
	ClaudeEventPostToolUseFailure
	ClaudeEventPostToolBatch
	ClaudeEventFileChanged
	ClaudeEventCwdChanged
	ClaudeEventConfigChange
	ClaudeEventInstructionsLoaded
	ClaudeEventWorktreeCreate
	ClaudeEventWorktreeRemove
	ClaudeEventSubagentStart
	ClaudeEventSubagentStop
	ClaudeEventTeammateIdle
	ClaudeEventTaskCreated
	ClaudeEventTaskCompleted
	ClaudeEventPreCompact
	ClaudeEventPostCompact
	ClaudeEventNotification
	ClaudeEventMessageDisplay
	ClaudeEventElicitation
	ClaudeEventElicitationResult
	ClaudeEventPreModelSwitch
	ClaudeEventPostModelSwitch
	ClaudeEventDirectoryAdded
	claudeLifecycleEventLimit
)

var claudeLifecycleEventNames = [...]string{
	"SessionStart",
	"Setup",
	"SessionEnd",
	"UserPromptSubmit",
	"UserPromptExpansion",
	"Stop",
	"StopFailure",
	"PreToolUse",
	"PermissionRequest",
	"PermissionDenied",
	"PostToolUse",
	"PostToolUseFailure",
	"PostToolBatch",
	"FileChanged",
	"CwdChanged",
	"ConfigChange",
	"InstructionsLoaded",
	"WorktreeCreate",
	"WorktreeRemove",
	"SubagentStart",
	"SubagentStop",
	"TeammateIdle",
	"TaskCreated",
	"TaskCompleted",
	"PreCompact",
	"PostCompact",
	"Notification",
	"MessageDisplay",
	"Elicitation",
	"ElicitationResult",
	"PreModelSwitch",
	"PostModelSwitch",
	"DirectoryAdded",
}

func (e ClaudeLifecycleEvent) IsValid() bool {
	return e > 0 && e < claudeLifecycleEventLimit
}

func (e ClaudeLifecycleEvent) NativeName() string {
	if !e.IsValid() {
		return ""
	}
	return claudeLifecycleEventNames[int(e)-1]
}

func (e ClaudeLifecycleEvent) String() string { return e.NativeName() }

// ClaudeLifecycleEvents returns the deterministic native catalog order used by
// codegen. The returned slice is a fresh copy.
func ClaudeLifecycleEvents() []ClaudeLifecycleEvent {
	events := make([]ClaudeLifecycleEvent, 0, int(claudeLifecycleEventLimit)-1)
	for event := ClaudeEventSessionStart; event < claudeLifecycleEventLimit; event++ {
		events = append(events, event)
	}
	return events
}

var (
	claudeSessionIdentity  = nativeIdentity(IdentitySession, "session_id", true)
	claudeRequestIdentity  = nativeIdentity(IdentityRequest, "request_id", true)
	claudeToolCallIdentity = nativeIdentity(IdentityToolCall, "tool_use_id", true)
	claudeAgentIdentity    = nativeIdentity(IdentityAgent, "agent_id", true)
)

// claudeHooksReference is the Claude Code hook reference. It documents that a
// hook process which exits with code 2 blocks the operation and shows its
// stderr to the model. It is the citation for every Claude row that keeps a
// blocking exit code.
const claudeHooksReference = "https://docs.claude.com/en/docs/claude-code/hooks"

func claudeLifecycleMapping(
	event ClaudeLifecycleEvent,
	semantic EventSemantic,
	blocking BlockingMode,
	mutation MutationMode,
	stopLoop StopLoopPolicy,
	evidence FailureEvidence,
	extraIdentities ...NativeIdentityField,
) LifecycleEventMapping {
	reconciliation := ReconcileNone
	if semantic != SemanticObservation {
		reconciliation = ReconcileHostNative
	}
	return LifecycleEventMapping{
		nativeName:      event.NativeName(),
		semantic:        semantic,
		surface:         SurfaceClaudeCommandJSON,
		blocking:        blocking,
		identities:      identities([]NativeIdentityField{claudeSessionIdentity}, extraIdentities...),
		mutation:        mutation,
		order:           OrderConcurrentNative,
		reconciliation:  reconciliation,
		failure:         evidenceBoundFailure(blocking, evidence, FailureExitTwoBlocks, FailureReportAndContinue),
		declaredFailure: declaredFailureArm(blocking, FailureExitTwoBlocks, FailureReportAndContinue),
		evidence:        evidence,
		stopLoop:        stopLoop,
	}
}

func claudeLifecycleMappings() map[ClaudeLifecycleEvent]LifecycleEventMapping {
	// documented is the evidence every Claude row cites while the host
	// reference states that its hook blocks on exit 2.
	documented := FailureEvidence{Source: claudeHooksReference}
	// unevidenced marks a gate whose blocking behavior is NOT stated by the
	// host reference yet. The row still consults the gate, but it runs as
	// report-and-continue, so a failure cannot refuse the user's operation.
	var unevidenced FailureEvidence

	observe := func(event ClaudeLifecycleEvent, extra ...NativeIdentityField) LifecycleEventMapping {
		return claudeLifecycleMapping(event, SemanticObservation, NonBlocking, MutationNone, StopLoopNotApplicable, unevidenced, extra...)
	}
	gate := func(event ClaudeLifecycleEvent, mutation MutationMode, extra ...NativeIdentityField) LifecycleEventMapping {
		return claudeLifecycleMapping(event, SemanticGateConsultation, Blocking, mutation, StopLoopNotApplicable, unevidenced, extra...)
	}
	evidencedGate := func(event ClaudeLifecycleEvent, mutation MutationMode, extra ...NativeIdentityField) LifecycleEventMapping {
		return claudeLifecycleMapping(event, SemanticGateConsultation, Blocking, mutation, StopLoopNotApplicable, documented, extra...)
	}
	mappings := map[ClaudeLifecycleEvent]LifecycleEventMapping{
		ClaudeEventSessionStart:        observe(ClaudeEventSessionStart),
		ClaudeEventSetup:               observe(ClaudeEventSetup),
		ClaudeEventSessionEnd:          observe(ClaudeEventSessionEnd),
		ClaudeEventUserPromptSubmit:    evidencedGate(ClaudeEventUserPromptSubmit, MutationNone),
		ClaudeEventUserPromptExpansion: gate(ClaudeEventUserPromptExpansion, MutationNone),
		ClaudeEventStop:                claudeLifecycleMapping(ClaudeEventStop, SemanticGateConsultation, Blocking, MutationNone, StopLoopConsultWhenInactive, documented),
		ClaudeEventStopFailure:         observe(ClaudeEventStopFailure),
		ClaudeEventPreToolUse:          evidencedGate(ClaudeEventPreToolUse, MutationInput, claudeToolCallIdentity),
		ClaudeEventPermissionRequest:   gate(ClaudeEventPermissionRequest, MutationNone, claudeRequestIdentity),
		ClaudeEventPermissionDenied:    observe(ClaudeEventPermissionDenied),
		ClaudeEventPostToolUse:         observe(ClaudeEventPostToolUse, claudeToolCallIdentity),
		ClaudeEventPostToolUseFailure:  observe(ClaudeEventPostToolUseFailure, claudeToolCallIdentity),
		ClaudeEventPostToolBatch:       gate(ClaudeEventPostToolBatch, MutationNone),
		ClaudeEventFileChanged:         observe(ClaudeEventFileChanged),
		ClaudeEventCwdChanged:          observe(ClaudeEventCwdChanged),
		ClaudeEventConfigChange:        claudeLifecycleMapping(ClaudeEventConfigChange, SemanticGateConsultation, ConditionallyBlocking, MutationNone, StopLoopNotApplicable, unevidenced),
		ClaudeEventInstructionsLoaded:  observe(ClaudeEventInstructionsLoaded),
		ClaudeEventWorktreeCreate:      gate(ClaudeEventWorktreeCreate, MutationNone),
		ClaudeEventWorktreeRemove:      observe(ClaudeEventWorktreeRemove),
		ClaudeEventSubagentStart:       observe(ClaudeEventSubagentStart, claudeAgentIdentity),
		ClaudeEventSubagentStop:        claudeLifecycleMapping(ClaudeEventSubagentStop, SemanticGateConsultation, Blocking, MutationNone, StopLoopConsultWhenInactive, documented, claudeAgentIdentity),
		ClaudeEventTeammateIdle:        gate(ClaudeEventTeammateIdle, MutationNone),
		ClaudeEventTaskCreated:         gate(ClaudeEventTaskCreated, MutationNone),
		ClaudeEventTaskCompleted:       gate(ClaudeEventTaskCompleted, MutationNone),
		ClaudeEventPreCompact:          gate(ClaudeEventPreCompact, MutationNone),
		ClaudeEventPostCompact:         observe(ClaudeEventPostCompact),
		ClaudeEventNotification:        observe(ClaudeEventNotification),
		ClaudeEventMessageDisplay:      observe(ClaudeEventMessageDisplay),
		ClaudeEventElicitation:         gate(ClaudeEventElicitation, MutationNone, claudeRequestIdentity),
		ClaudeEventElicitationResult:   claudeLifecycleMapping(ClaudeEventElicitationResult, SemanticExplicitHumanResponse, Blocking, MutationNone, StopLoopNotApplicable, unevidenced, claudeRequestIdentity),
		// Registered at 2.1.261 without an authentic capture: observations,
		// non-blocking, report-and-continue, no evidence, no identity beyond the
		// session every Claude payload carries.
		ClaudeEventPreModelSwitch:  observe(ClaudeEventPreModelSwitch),
		ClaudeEventPostModelSwitch: observe(ClaudeEventPostModelSwitch),
		ClaudeEventDirectoryAdded:  observe(ClaudeEventDirectoryAdded),
	}
	postToolBatch := mappings[ClaudeEventPostToolBatch]
	postToolBatch.unresolved = []NativeIdentityKind{IdentityToolCall}
	mappings[ClaudeEventPostToolBatch] = postToolBatch
	return mappings
}

// ClaudeCode2_1_261Lifecycle returns the immutable Claude Code lifecycle table
// bound to the same exact host version and RuntimeContractID as
// ClaudeCode2_1_261.
func ClaudeCode2_1_261Lifecycle() LifecycleContract[ClaudeLifecycleEvent] {
	return mustLifecycleContract(ClaudeCode2_1_261(), ClaudeLifecycleEvents(), claudeLifecycleMappings())
}
