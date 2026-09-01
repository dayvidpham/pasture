package runtime

import "github.com/dayvidpham/pasture/internal/codegen/ir"

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

// OpenCodeLifecycleEvent is the closed native named-hook and event-bus catalog
// for the pinned OpenCode lifecycle profile.
type OpenCodeLifecycleEvent uint8

const (
	OpenCodeEventCommandExecuted OpenCodeLifecycleEvent = iota + 1
	OpenCodeEventFileEdited
	OpenCodeEventFileWatcherUpdated
	OpenCodeEventInstallationUpdated
	OpenCodeEventInstallationUpdateAvailable
	OpenCodeEventLSPClientDiagnostics
	OpenCodeEventLSPUpdated
	OpenCodeEventMessageUpdated
	OpenCodeEventMessageRemoved
	OpenCodeEventMessagePartUpdated
	OpenCodeEventMessagePartRemoved
	OpenCodeEventPermissionUpdated
	OpenCodeEventPermissionReplied
	OpenCodeEventServerConnected
	OpenCodeEventServerInstanceDisposed
	OpenCodeEventSessionCreated
	OpenCodeEventSessionUpdated
	OpenCodeEventSessionDeleted
	OpenCodeEventSessionCompacted
	OpenCodeEventSessionDiff
	OpenCodeEventSessionError
	OpenCodeEventSessionIdle
	OpenCodeEventSessionStatus
	OpenCodeEventTodoUpdated
	OpenCodeEventTUIPromptAppend
	OpenCodeEventTUICommandExecute
	OpenCodeEventTUIToastShow
	OpenCodeEventPTYCreated
	OpenCodeEventPTYUpdated
	OpenCodeEventPTYExited
	OpenCodeEventPTYDeleted
	OpenCodeEventVCSBranchUpdated
	OpenCodeEventChatMessage
	OpenCodeEventChatParams
	OpenCodeEventChatHeaders
	OpenCodeEventPermissionAsk
	OpenCodeEventCommandExecuteBefore
	OpenCodeEventToolExecuteBefore
	OpenCodeEventShellEnv
	OpenCodeEventToolExecuteAfter
	OpenCodeEventExperimentalChatMessagesTransform
	OpenCodeEventExperimentalChatSystemTransform
	OpenCodeEventExperimentalProviderSmallModel
	OpenCodeEventExperimentalSessionCompacting
	OpenCodeEventExperimentalCompactionAutocontinue
	OpenCodeEventExperimentalTextComplete
	OpenCodeEventToolDefinition
	openCodeLifecycleEventLimit
)

var openCodeLifecycleEventNames = [...]string{
	"command.executed",
	"file.edited",
	"file.watcher.updated",
	"installation.updated",
	"installation.update_available",
	"lsp.client.diagnostics",
	"lsp.updated",
	"message.updated",
	"message.removed",
	"message.part.updated",
	"message.part.removed",
	"permission.updated",
	"permission.replied",
	"server.connected",
	"server.instance.disposed",
	"session.created",
	"session.updated",
	"session.deleted",
	"session.compacted",
	"session.diff",
	"session.error",
	"session.idle",
	"session.status",
	"todo.updated",
	"tui.prompt.append",
	"tui.command.execute",
	"tui.toast.show",
	"pty.created",
	"pty.updated",
	"pty.exited",
	"pty.deleted",
	"vcs.branch.updated",
	"chat.message",
	"chat.params",
	"chat.headers",
	"permission.ask",
	"command.execute.before",
	"tool.execute.before",
	"shell.env",
	"tool.execute.after",
	"experimental.chat.messages.transform",
	"experimental.chat.system.transform",
	"experimental.provider.small_model",
	"experimental.session.compacting",
	"experimental.compaction.autocontinue",
	"experimental.text.complete",
	"tool.definition",
}

func (e OpenCodeLifecycleEvent) IsValid() bool {
	return e > 0 && e < openCodeLifecycleEventLimit
}

func (e OpenCodeLifecycleEvent) NativeName() string {
	if !e.IsValid() {
		return ""
	}
	return openCodeLifecycleEventNames[int(e)-1]
}

func (e OpenCodeLifecycleEvent) String() string { return e.NativeName() }

// OpenCodeLifecycleEvents returns the deterministic native catalog order used
// by codegen. The returned slice is a fresh copy.
func OpenCodeLifecycleEvents() []OpenCodeLifecycleEvent {
	events := make([]OpenCodeLifecycleEvent, 0, int(openCodeLifecycleEventLimit)-1)
	for event := OpenCodeEventCommandExecuted; event < openCodeLifecycleEventLimit; event++ {
		events = append(events, event)
	}
	return events
}

var (
	claudeSessionIdentity  = nativeIdentity(IdentitySession, "session_id", true)
	claudeRequestIdentity  = nativeIdentity(IdentityRequest, "request_id", true)
	claudeToolCallIdentity = nativeIdentity(IdentityToolCall, "tool_use_id", true)
	claudeAgentIdentity    = nativeIdentity(IdentityAgent, "agent_id", true)

	codexSessionIdentity  = nativeIdentity(IdentitySession, "session_id", true)
	codexTurnIdentity     = nativeIdentity(IdentityTurn, "turn_id", true)
	codexToolCallIdentity = nativeIdentity(IdentityToolCall, "tool_use_id", true)
	codexAgentIdentity    = nativeIdentity(IdentityAgent, "agent_id", true)

	openCodeSessionIdentity         = nativeIdentity(IdentitySession, "sessionID", true)
	openCodeOptionalSessionIdentity = nativeIdentity(IdentitySession, "sessionID", false)
	openCodeCallIdentity            = nativeIdentity(IdentityToolCall, "callID", true)
	openCodeOptionalCallIdentity    = nativeIdentity(IdentityToolCall, "callID", false)
	openCodeMessageIdentity         = nativeIdentity(IdentityMessage, "messageID", false)
)

func identities(base []NativeIdentityField, extra ...NativeIdentityField) []NativeIdentityField {
	result := make([]NativeIdentityField, 0, len(base)+len(extra))
	result = append(result, base...)
	result = append(result, extra...)
	return result
}

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
	}
	postToolBatch := mappings[ClaudeEventPostToolBatch]
	postToolBatch.unresolved = []NativeIdentityKind{IdentityToolCall}
	mappings[ClaudeEventPostToolBatch] = postToolBatch
	return mappings
}

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
	// The evidence itself is NOT missing. The pinned Codex 0.146.0 command-hook
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
	}
}

func openCodeNamedMapping(event OpenCodeLifecycleEvent, eventIdentities ...NativeIdentityField) LifecycleEventMapping {
	return LifecycleEventMapping{
		nativeName:      event.NativeName(),
		semantic:        SemanticGateConsultation,
		surface:         SurfaceOpenCodeNamedOutput,
		blocking:        Blocking,
		identities:      append([]NativeIdentityField(nil), eventIdentities...),
		mutation:        MutationOutputObject,
		order:           OrderSequentialLoad,
		reconciliation:  ReconcileSequentialMutation,
		failure:         FailureThrowFailFast,
		declaredFailure: FailureThrowFailFast,
		stopLoop:        StopLoopNotApplicable,
	}
}

func openCodeObservationMapping(event OpenCodeLifecycleEvent, eventIdentities ...NativeIdentityField) LifecycleEventMapping {
	return LifecycleEventMapping{
		nativeName:      event.NativeName(),
		semantic:        SemanticObservation,
		surface:         SurfaceOpenCodeCatchAllSSE,
		blocking:        NonBlocking,
		identities:      append([]NativeIdentityField(nil), eventIdentities...),
		mutation:        MutationNone,
		order:           OrderObservationStream,
		reconciliation:  ReconcileNone,
		failure:         FailureObserveOnly,
		declaredFailure: FailureObserveOnly,
		stopLoop:        StopLoopNotApplicable,
	}
}

func openCodeLifecycleMappings() map[OpenCodeLifecycleEvent]LifecycleEventMapping {
	observe := openCodeObservationMapping
	named := openCodeNamedMapping
	return map[OpenCodeLifecycleEvent]LifecycleEventMapping{
		OpenCodeEventCommandExecuted:                    observe(OpenCodeEventCommandExecuted, openCodeSessionIdentity),
		OpenCodeEventFileEdited:                         observe(OpenCodeEventFileEdited),
		OpenCodeEventFileWatcherUpdated:                 observe(OpenCodeEventFileWatcherUpdated),
		OpenCodeEventInstallationUpdated:                observe(OpenCodeEventInstallationUpdated),
		OpenCodeEventInstallationUpdateAvailable:        observe(OpenCodeEventInstallationUpdateAvailable),
		OpenCodeEventLSPClientDiagnostics:               observe(OpenCodeEventLSPClientDiagnostics),
		OpenCodeEventLSPUpdated:                         observe(OpenCodeEventLSPUpdated),
		OpenCodeEventMessageUpdated:                     observe(OpenCodeEventMessageUpdated),
		OpenCodeEventMessageRemoved:                     observe(OpenCodeEventMessageRemoved),
		OpenCodeEventMessagePartUpdated:                 observe(OpenCodeEventMessagePartUpdated),
		OpenCodeEventMessagePartRemoved:                 observe(OpenCodeEventMessagePartRemoved),
		OpenCodeEventPermissionUpdated:                  observe(OpenCodeEventPermissionUpdated),
		OpenCodeEventPermissionReplied:                  observe(OpenCodeEventPermissionReplied),
		OpenCodeEventServerConnected:                    observe(OpenCodeEventServerConnected),
		OpenCodeEventServerInstanceDisposed:             observe(OpenCodeEventServerInstanceDisposed),
		OpenCodeEventSessionCreated:                     observe(OpenCodeEventSessionCreated, openCodeSessionIdentity),
		OpenCodeEventSessionUpdated:                     observe(OpenCodeEventSessionUpdated),
		OpenCodeEventSessionDeleted:                     observe(OpenCodeEventSessionDeleted),
		OpenCodeEventSessionCompacted:                   observe(OpenCodeEventSessionCompacted),
		OpenCodeEventSessionDiff:                        observe(OpenCodeEventSessionDiff),
		OpenCodeEventSessionError:                       observe(OpenCodeEventSessionError),
		OpenCodeEventSessionIdle:                        observe(OpenCodeEventSessionIdle),
		OpenCodeEventSessionStatus:                      observe(OpenCodeEventSessionStatus),
		OpenCodeEventTodoUpdated:                        observe(OpenCodeEventTodoUpdated),
		OpenCodeEventTUIPromptAppend:                    observe(OpenCodeEventTUIPromptAppend),
		OpenCodeEventTUICommandExecute:                  observe(OpenCodeEventTUICommandExecute),
		OpenCodeEventTUIToastShow:                       observe(OpenCodeEventTUIToastShow),
		OpenCodeEventPTYCreated:                         observe(OpenCodeEventPTYCreated),
		OpenCodeEventPTYUpdated:                         observe(OpenCodeEventPTYUpdated),
		OpenCodeEventPTYExited:                          observe(OpenCodeEventPTYExited),
		OpenCodeEventPTYDeleted:                         observe(OpenCodeEventPTYDeleted),
		OpenCodeEventVCSBranchUpdated:                   observe(OpenCodeEventVCSBranchUpdated),
		OpenCodeEventChatMessage:                        named(OpenCodeEventChatMessage, openCodeSessionIdentity, openCodeMessageIdentity),
		OpenCodeEventChatParams:                         named(OpenCodeEventChatParams, openCodeSessionIdentity),
		OpenCodeEventChatHeaders:                        named(OpenCodeEventChatHeaders, openCodeSessionIdentity),
		OpenCodeEventPermissionAsk:                      named(OpenCodeEventPermissionAsk),
		OpenCodeEventCommandExecuteBefore:               named(OpenCodeEventCommandExecuteBefore, openCodeSessionIdentity),
		OpenCodeEventToolExecuteBefore:                  named(OpenCodeEventToolExecuteBefore, openCodeSessionIdentity, openCodeCallIdentity),
		OpenCodeEventShellEnv:                           named(OpenCodeEventShellEnv, openCodeOptionalSessionIdentity, openCodeOptionalCallIdentity),
		OpenCodeEventToolExecuteAfter:                   named(OpenCodeEventToolExecuteAfter, openCodeSessionIdentity, openCodeCallIdentity),
		OpenCodeEventExperimentalChatMessagesTransform:  named(OpenCodeEventExperimentalChatMessagesTransform),
		OpenCodeEventExperimentalChatSystemTransform:    named(OpenCodeEventExperimentalChatSystemTransform, openCodeOptionalSessionIdentity),
		OpenCodeEventExperimentalProviderSmallModel:     named(OpenCodeEventExperimentalProviderSmallModel),
		OpenCodeEventExperimentalSessionCompacting:      named(OpenCodeEventExperimentalSessionCompacting, openCodeSessionIdentity),
		OpenCodeEventExperimentalCompactionAutocontinue: named(OpenCodeEventExperimentalCompactionAutocontinue, openCodeSessionIdentity),
		OpenCodeEventExperimentalTextComplete:           named(OpenCodeEventExperimentalTextComplete, openCodeSessionIdentity, openCodeMessageIdentity),
		OpenCodeEventToolDefinition:                     named(OpenCodeEventToolDefinition),
	}
}

// ClaudeCode2_1_210Lifecycle returns the immutable Claude Code lifecycle table
// bound to the same exact host version and RuntimeContractID as
// ClaudeCode2_1_210.
func ClaudeCode2_1_210Lifecycle() LifecycleContract[ClaudeLifecycleEvent] {
	return mustLifecycleContract(ClaudeCode2_1_210(), ClaudeLifecycleEvents(), claudeLifecycleMappings())
}

// Codex0_146_0Lifecycle returns the immutable Codex CLI lifecycle table bound
// to the same exact host version and RuntimeContractID as Codex0_146_0.
func Codex0_146_0Lifecycle() LifecycleContract[CodexLifecycleEvent] {
	return mustLifecycleContract(Codex0_146_0(), CodexLifecycleEvents(), codexLifecycleMappings())
}

// OpenCode1_18_10Lifecycle returns the immutable OpenCode lifecycle table bound
// to the same exact host version and RuntimeContractID as OpenCode1_18_10.
func OpenCode1_18_10Lifecycle() LifecycleContract[OpenCodeLifecycleEvent] {
	return mustLifecycleContract(OpenCode1_18_10(), OpenCodeLifecycleEvents(), openCodeLifecycleMappings())
}

// Pi intentionally has no lifecycle contract constructor. Its extension and RPC
// research informed the semantic split, but no Pi adapter is shipped.

// ValidatePinnedLifecycleProfiles rebuilds every pinned lifecycle profile and
// returns the first row that fails contract validation, WITHOUT panicking.
//
// The three profile constructors panic on an invalid row, which is right for a
// program that is already running: an invalid contract must never reach code
// generation. A generator, though, has to report the offending row before it
// writes anything, so it calls this instead and prints the six-part diagnostic.
func ValidatePinnedLifecycleProfiles() error {
	if _, err := newLifecycleContract(
		ClaudeCode2_1_210(), ClaudeLifecycleEvents(), claudeLifecycleMappings(),
	); err != nil {
		return err
	}
	if _, err := newLifecycleContract(
		Codex0_146_0(), CodexLifecycleEvents(), codexLifecycleMappings(),
	); err != nil {
		return err
	}
	if _, err := newLifecycleContract(
		OpenCode1_18_10(), OpenCodeLifecycleEvents(), openCodeLifecycleMappings(),
	); err != nil {
		return err
	}
	return nil
}

// LifecycleFailurePolicy is the declared failure behaviour of one native event:
// the mode the host contract says applies, the citation for a blocking exit
// code, and the event class the host reads the answer as.
type LifecycleFailurePolicy struct {
	// Mode is the EFFECTIVE failure mode of the row, after the failure-evidence
	// rule. Every behaviour obeys this one.
	Mode FailureMode
	// DeclaredMode is the mode the host contract DECLARES for the row, before
	// the evidence rule demotes an uncited blocking row to report-and-continue.
	// It equals Mode on every row the rule does not demote.
	//
	// It is carried so that an EXPLANATION can tell an operator the truth about
	// the declaration. A blocking row with no citation and a row that is
	// declared report-and-continue are indistinguishable once the demotion has
	// happened, and they need opposite advice: the first becomes blockable when
	// somebody supplies the citation, the second can never block. Read this
	// field only to explain the row; never to choose an exit status.
	DeclaredMode FailureMode
	Evidence     FailureEvidence
	// Semantic is the declared class of the event. It is carried here because
	// the bytes a host reads as "proceed" depend on the class as well as on the
	// harness: a Codex gate is refused unless the continuation says continue,
	// while a Codex observation contributes no directives. The zero value means
	// the event is not declared by this build, and the caller must not guess a
	// class from it.
	Semantic EventSemantic
}

// LookupLifecycleFailure returns the declared failure behaviour of one native
// event, by the harness and the exact native NAME.
//
// The typed lifecycle contracts have no string lookup on purpose, so a caller
// cannot invent an event. This function is the ONE narrow exception, and it
// exists because a real host really does hand the hook a harness and an event
// NAME on the command line. It answers only this one question, it cannot reach
// identities, payload fields or ordering, and it returns false for a name no
// pinned profile declares, so the caller must decide what an unknown event
// means rather than receive a guess.
func LookupLifecycleFailure(harness ir.HarnessID, nativeName string) (LifecycleFailurePolicy, bool) {
	switch harness {
	case ir.HarnessClaudeCode:
		return lookupLifecycleFailure(ClaudeCode2_1_210Lifecycle(), ClaudeLifecycleEvents(), nativeName)
	case ir.HarnessCodex:
		return lookupLifecycleFailure(Codex0_146_0Lifecycle(), CodexLifecycleEvents(), nativeName)
	case ir.HarnessOpenCode:
		return lookupLifecycleFailure(OpenCode1_18_10Lifecycle(), OpenCodeLifecycleEvents(), nativeName)
	default:
		return LifecycleFailurePolicy{}, false
	}
}

func lookupLifecycleFailure[E comparable](
	contract LifecycleContract[E],
	events []E,
	nativeName string,
) (LifecycleFailurePolicy, bool) {
	for _, event := range events {
		mapping, err := contract.Mapping(event)
		if err != nil {
			continue
		}
		if mapping.NativeName() == nativeName {
			return LifecycleFailurePolicy{
				Mode:         mapping.Failure(),
				DeclaredMode: mapping.DeclaredFailure(),
				Evidence:     mapping.Evidence(),
				Semantic:     mapping.Semantic(),
			}, true
		}
	}
	return LifecycleFailurePolicy{}, false
}
