package runtime

// This file holds every OpenCode lifecycle row: the closed named-hook and
// event-bus catalog, its identity fields, the mapping builders, the mappings
// table and the pinned contract constructor. An OpenCode row lives here and
// nowhere else, so that one harness can be edited without touching another
// harness or the shared helpers in lifecycle_profiles.go. The placement is
// enforced by a test that reads the declarations of every
// lifecycle_profiles*.go file.

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
	openCodeSessionIdentity         = nativeIdentity(IdentitySession, "sessionID", true)
	openCodeOptionalSessionIdentity = nativeIdentity(IdentitySession, "sessionID", false)
	openCodeCallIdentity            = nativeIdentity(IdentityToolCall, "callID", true)
	openCodeOptionalCallIdentity    = nativeIdentity(IdentityToolCall, "callID", false)
	openCodeMessageIdentity         = nativeIdentity(IdentityMessage, "messageID", false)
)

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

// OpenCode1_18_10Lifecycle returns the immutable OpenCode lifecycle table bound
// to the same exact host version and RuntimeContractID as OpenCode1_18_10.
func OpenCode1_18_10Lifecycle() LifecycleContract[OpenCodeLifecycleEvent] {
	return mustLifecycleContract(OpenCode1_18_10(), OpenCodeLifecycleEvents(), openCodeLifecycleMappings())
}
