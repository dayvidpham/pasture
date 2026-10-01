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
	"installation.update-available", // spelled as OpenCode emits it: packages/schema/src/installation-event.ts, identical at 1.18.10 and 1.18.29
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
		preAction:       event != OpenCodeEventToolExecuteAfter,
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

// OpenCode1_18_29Lifecycle returns the immutable OpenCode lifecycle table bound
// to the same exact host version and RuntimeContractID as OpenCode1_18_29.
func OpenCode1_18_29Lifecycle() LifecycleContract[OpenCodeLifecycleEvent] {
	return mustLifecycleContract(OpenCode1_18_29(), OpenCodeLifecycleEvents(), openCodeLifecycleMappings())
}

// OpenCode2LifecycleEvent is the closed Plugin.define hook catalog for OpenCode
// 2.0.20. It covers the hook domains the 2.0.20 source declares and the
// generated v2 transport registers: twelve session hooks, two tool hooks, one
// permission hook and one shell hook. The provider-SDK hooks (aisdk sdk,
// language) are provider wiring rather than lifecycle gates and are not
// modeled here. The server event stream (ctx.event.subscribe) is the v1 SSE
// observation surface under a new subscription call and carries no gate; no
// v2 row models it until an authentic capture proves a distinct occurrence
// shape.
//
// Native names are the dotted hook coordinates the host triggers: the domain
// the plugin registers through, a dot, and the hook name from the 2.0.20
// plugin types. Session coordinates come from SessionHooks in
// packages/plugin/src/promise/session.ts, tool coordinates from ToolHooks in
// packages/plugin/src/promise/tool.ts, the permission coordinate from
// PermissionHooks in packages/plugin/src/promise/permission.ts, and the shell
// coordinate from ShellHooks in packages/plugin/src/promise/shell.ts (all at
// the v2.0.20 tag). Two coordinates repeat v1 native names
// ("tool.execute.before", "tool.execute.after"); they are distinct typed
// events here with version-qualified symbols because their v2 payload shapes
// differ (v2 carries tool/sessionID/agent/messageID/id/input where v1 carried
// tool/sessionID/callID plus output args).
//
// The permission evaluate hook is the one v2 channel through which a plugin
// mutation reaches the host decision. The 2.0.20 host computes the ruleset
// effect, yields hooks.trigger("permission", "evaluate", {... effect}) and
// returns { effect: event.effect, message: event.message, rules } from
// evaluateInput in packages/core/src/permission.ts, so a plugin that sets
// event.effect to "deny" is honoured on the path where no saved or configured
// rule already denied (that path returns early without firing the hook). The
// live refusal bytes and exit for that denial are not recorded here; they
// await an authentic v2.0.20 capture. Every row below therefore carries no
// response-channel evidence and derives CapabilityNone, exactly like the v1
// rows, until that capture lands.
type OpenCode2LifecycleEvent uint8

const (
	OpenCode2EventSessionPrompt OpenCode2LifecycleEvent = iota + 1
	OpenCode2EventSessionContext
	OpenCode2EventSessionCompaction
	OpenCode2EventSessionGenerate
	OpenCode2EventSessionTitle
	OpenCode2EventSessionModelRequest
	OpenCode2EventSessionHTTPRequest
	OpenCode2EventSessionHTTPResponse
	OpenCode2EventSessionExperimentalWSHandshake
	OpenCode2EventSessionExperimentalWSSend
	OpenCode2EventSessionExperimentalWSReceive
	OpenCode2EventSessionRetry
	OpenCode2EventToolExecuteBefore
	OpenCode2EventToolExecuteAfter
	OpenCode2EventPermissionEvaluate
	OpenCode2EventShellCreateBefore
	openCode2LifecycleEventLimit
)

var openCode2LifecycleEventNames = [...]string{
	"session.prompt",
	"session.context",
	"session.compaction",
	"session.generate",
	"session.title",
	"session.model.request",
	"session.http.request",
	"session.http.response",
	"session.experimental.ws.handshake",
	"session.experimental.ws.send",
	"session.experimental.ws.receive",
	"session.retry",
	"tool.execute.before",
	"tool.execute.after",
	"permission.evaluate",
	"shell.create.before",
}

func (e OpenCode2LifecycleEvent) IsValid() bool {
	return e > 0 && e < openCode2LifecycleEventLimit
}

func (e OpenCode2LifecycleEvent) NativeName() string {
	if !e.IsValid() {
		return ""
	}
	return openCode2LifecycleEventNames[int(e)-1]
}

func (e OpenCode2LifecycleEvent) String() string { return e.NativeName() }

// OpenCode2LifecycleEvents returns the deterministic native catalog order used
// by codegen. The returned slice is a fresh copy.
func OpenCode2LifecycleEvents() []OpenCode2LifecycleEvent {
	events := make([]OpenCode2LifecycleEvent, 0, int(openCode2LifecycleEventLimit)-1)
	for event := OpenCode2EventSessionPrompt; event < openCode2LifecycleEventLimit; event++ {
		events = append(events, event)
	}
	return events
}

var (
	openCode2SessionIdentity = nativeIdentity(IdentitySession, "sessionID", true)
	openCode2CallIdentity    = nativeIdentity(IdentityToolCall, "id", true)
)

func openCode2NamedMapping(event OpenCode2LifecycleEvent, eventIdentities ...NativeIdentityField) LifecycleEventMapping {
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
		preAction:       event != OpenCode2EventToolExecuteAfter,
		declaredFailure: FailureThrowFailFast,
		stopLoop:        StopLoopNotApplicable,
	}
}

func openCode2LifecycleMappings() map[OpenCode2LifecycleEvent]LifecycleEventMapping {
	named := openCode2NamedMapping
	return map[OpenCode2LifecycleEvent]LifecycleEventMapping{
		OpenCode2EventSessionPrompt:                  named(OpenCode2EventSessionPrompt),
		OpenCode2EventSessionContext:                 named(OpenCode2EventSessionContext),
		OpenCode2EventSessionCompaction:              named(OpenCode2EventSessionCompaction),
		OpenCode2EventSessionGenerate:                named(OpenCode2EventSessionGenerate),
		OpenCode2EventSessionTitle:                   named(OpenCode2EventSessionTitle),
		OpenCode2EventSessionModelRequest:            named(OpenCode2EventSessionModelRequest),
		OpenCode2EventSessionHTTPRequest:             named(OpenCode2EventSessionHTTPRequest),
		OpenCode2EventSessionHTTPResponse:            named(OpenCode2EventSessionHTTPResponse),
		OpenCode2EventSessionExperimentalWSHandshake: named(OpenCode2EventSessionExperimentalWSHandshake),
		OpenCode2EventSessionExperimentalWSSend:      named(OpenCode2EventSessionExperimentalWSSend),
		OpenCode2EventSessionExperimentalWSReceive:   named(OpenCode2EventSessionExperimentalWSReceive),
		OpenCode2EventSessionRetry:                   named(OpenCode2EventSessionRetry),
		OpenCode2EventToolExecuteBefore:              named(OpenCode2EventToolExecuteBefore, openCode2SessionIdentity, openCode2CallIdentity),
		OpenCode2EventToolExecuteAfter:               named(OpenCode2EventToolExecuteAfter),
		OpenCode2EventPermissionEvaluate:             named(OpenCode2EventPermissionEvaluate, openCode2SessionIdentity),
		OpenCode2EventShellCreateBefore:              named(OpenCode2EventShellCreateBefore),
	}
}

// OpenCode2_0_20Lifecycle returns the immutable OpenCode 2.0.20 lifecycle
// table bound to the same exact host version and RuntimeContractID as
// OpenCode2_0_20.
func OpenCode2_0_20Lifecycle() LifecycleContract[OpenCode2LifecycleEvent] {
	return mustLifecycleContract(OpenCode2_0_20(), OpenCode2LifecycleEvents(), openCode2LifecycleMappings())
}
