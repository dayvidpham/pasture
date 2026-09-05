// Package opencode supplies the pinned OpenCode callback vocabulary as host data
// for the generic lifecycle frontend. All control flow lives in
// internal/lifecycle/frontend; this package is data plus a monomorphic wrapper.
package opencode

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/frontend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// eventMappings pairs every generated OpenCode registration ordinal with its
// runtime profile event, by native name. The registration and runtime
// enumerations are separate contracts, so every ordinal is explicit here and a
// test holds the pairing total and correct. A complete mapping is not an
// enabled event: the activation table decides admission before any payload is
// read, so an event without an authentic fixture stays withheld upstream.
var eventMappings = map[model.ContractEventKind]runtime.OpenCodeLifecycleEvent{
	registration.EventOpenCodeCommandExecuted:                    runtime.OpenCodeEventCommandExecuted,                    // command.executed
	registration.EventOpenCodeFileEdited:                         runtime.OpenCodeEventFileEdited,                         // file.edited
	registration.EventOpenCodeFileWatcherUpdated:                 runtime.OpenCodeEventFileWatcherUpdated,                 // file.watcher.updated
	registration.EventOpenCodeInstallationUpdated:                runtime.OpenCodeEventInstallationUpdated,                // installation.updated
	registration.EventOpenCodeInstallationUpdate_available:       runtime.OpenCodeEventInstallationUpdateAvailable,        // installation.update-available
	registration.EventOpenCodeLspClientDiagnostics:               runtime.OpenCodeEventLSPClientDiagnostics,               // lsp.client.diagnostics
	registration.EventOpenCodeLspUpdated:                         runtime.OpenCodeEventLSPUpdated,                         // lsp.updated
	registration.EventOpenCodeMessageUpdated:                     runtime.OpenCodeEventMessageUpdated,                     // message.updated
	registration.EventOpenCodeMessageRemoved:                     runtime.OpenCodeEventMessageRemoved,                     // message.removed
	registration.EventOpenCodeMessagePartUpdated:                 runtime.OpenCodeEventMessagePartUpdated,                 // message.part.updated
	registration.EventOpenCodeMessagePartRemoved:                 runtime.OpenCodeEventMessagePartRemoved,                 // message.part.removed
	registration.EventOpenCodePermissionUpdated:                  runtime.OpenCodeEventPermissionUpdated,                  // permission.updated
	registration.EventOpenCodePermissionReplied:                  runtime.OpenCodeEventPermissionReplied,                  // permission.replied
	registration.EventOpenCodeServerConnected:                    runtime.OpenCodeEventServerConnected,                    // server.connected
	registration.EventOpenCodeServerInstanceDisposed:             runtime.OpenCodeEventServerInstanceDisposed,             // server.instance.disposed
	registration.EventOpenCodeSessionCreated:                     runtime.OpenCodeEventSessionCreated,                     // session.created
	registration.EventOpenCodeSessionUpdated:                     runtime.OpenCodeEventSessionUpdated,                     // session.updated
	registration.EventOpenCodeSessionDeleted:                     runtime.OpenCodeEventSessionDeleted,                     // session.deleted
	registration.EventOpenCodeSessionCompacted:                   runtime.OpenCodeEventSessionCompacted,                   // session.compacted
	registration.EventOpenCodeSessionDiff:                        runtime.OpenCodeEventSessionDiff,                        // session.diff
	registration.EventOpenCodeSessionError:                       runtime.OpenCodeEventSessionError,                       // session.error
	registration.EventOpenCodeSessionIdle:                        runtime.OpenCodeEventSessionIdle,                        // session.idle
	registration.EventOpenCodeSessionStatus:                      runtime.OpenCodeEventSessionStatus,                      // session.status
	registration.EventOpenCodeTodoUpdated:                        runtime.OpenCodeEventTodoUpdated,                        // todo.updated
	registration.EventOpenCodeTuiPromptAppend:                    runtime.OpenCodeEventTUIPromptAppend,                    // tui.prompt.append
	registration.EventOpenCodeTuiCommandExecute:                  runtime.OpenCodeEventTUICommandExecute,                  // tui.command.execute
	registration.EventOpenCodeTuiToastShow:                       runtime.OpenCodeEventTUIToastShow,                       // tui.toast.show
	registration.EventOpenCodePtyCreated:                         runtime.OpenCodeEventPTYCreated,                         // pty.created
	registration.EventOpenCodePtyUpdated:                         runtime.OpenCodeEventPTYUpdated,                         // pty.updated
	registration.EventOpenCodePtyExited:                          runtime.OpenCodeEventPTYExited,                          // pty.exited
	registration.EventOpenCodePtyDeleted:                         runtime.OpenCodeEventPTYDeleted,                         // pty.deleted
	registration.EventOpenCodeVcsBranchUpdated:                   runtime.OpenCodeEventVCSBranchUpdated,                   // vcs.branch.updated
	registration.EventOpenCodeChatMessage:                        runtime.OpenCodeEventChatMessage,                        // chat.message
	registration.EventOpenCodeChatParams:                         runtime.OpenCodeEventChatParams,                         // chat.params
	registration.EventOpenCodeChatHeaders:                        runtime.OpenCodeEventChatHeaders,                        // chat.headers
	registration.EventOpenCodePermissionAsk:                      runtime.OpenCodeEventPermissionAsk,                      // permission.ask
	registration.EventOpenCodeCommandExecuteBefore:               runtime.OpenCodeEventCommandExecuteBefore,               // command.execute.before
	registration.EventOpenCodeToolExecuteBefore:                  runtime.OpenCodeEventToolExecuteBefore,                  // tool.execute.before
	registration.EventOpenCodeShellEnv:                           runtime.OpenCodeEventShellEnv,                           // shell.env
	registration.EventOpenCodeToolExecuteAfter:                   runtime.OpenCodeEventToolExecuteAfter,                   // tool.execute.after
	registration.EventOpenCodeExperimentalChatMessagesTransform:  runtime.OpenCodeEventExperimentalChatMessagesTransform,  // experimental.chat.messages.transform
	registration.EventOpenCodeExperimentalChatSystemTransform:    runtime.OpenCodeEventExperimentalChatSystemTransform,    // experimental.chat.system.transform
	registration.EventOpenCodeExperimentalProviderSmall_model:    runtime.OpenCodeEventExperimentalProviderSmallModel,     // experimental.provider.small_model
	registration.EventOpenCodeExperimentalSessionCompacting:      runtime.OpenCodeEventExperimentalSessionCompacting,      // experimental.session.compacting
	registration.EventOpenCodeExperimentalCompactionAutocontinue: runtime.OpenCodeEventExperimentalCompactionAutocontinue, // experimental.compaction.autocontinue
	registration.EventOpenCodeExperimentalTextComplete:           runtime.OpenCodeEventExperimentalTextComplete,           // experimental.text.complete
	registration.EventOpenCodeToolDefinition:                     runtime.OpenCodeEventToolDefinition,                     // tool.definition
}

// host is the pinned OpenCode data consumed by the generic frontend engine.
var host = frontend.Host[runtime.OpenCodeLifecycleEvent]{
	Label:    "OpenCode",
	Contract: runtime.OpenCode1_18_29Lifecycle,
	Events:   eventMappings,
}

// Bind creates L1 and typed identities for a registered OpenCode callback. It
// delegates to the generic strictest-common frontend engine.
func Bind(modelKind model.ContractEventKind, bindings []model.NativeBinding) (waist.L1, []waist.Identity, error) {
	return frontend.Bind(host, modelKind, bindings)
}
