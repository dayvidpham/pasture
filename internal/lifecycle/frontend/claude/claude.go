// Package claude supplies the pinned Claude capture vocabulary as host data for
// the generic lifecycle frontend. All control flow lives in
// internal/lifecycle/frontend; this package is data plus a monomorphic wrapper.
package claude

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/frontend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// claudeEventMappings is the sole Claude frontend event mapping. Keep every
// generated model ordinal explicit here: the registration and runtime
// enumerations are separate contracts even though their current ordinals agree.
// A test holds the mapping total over the registration and each pair correct.
var claudeEventMappings = map[model.ContractEventKind]runtime.ClaudeLifecycleEvent{
	registration.EventSessionStart:        runtime.ClaudeEventSessionStart,
	registration.EventSetup:               runtime.ClaudeEventSetup,
	registration.EventSessionEnd:          runtime.ClaudeEventSessionEnd,
	registration.EventUserPromptSubmit:    runtime.ClaudeEventUserPromptSubmit,
	registration.EventUserPromptExpansion: runtime.ClaudeEventUserPromptExpansion,
	registration.EventStop:                runtime.ClaudeEventStop,
	registration.EventStopFailure:         runtime.ClaudeEventStopFailure,
	registration.EventPreToolUse:          runtime.ClaudeEventPreToolUse,
	registration.EventPermissionRequest:   runtime.ClaudeEventPermissionRequest,
	registration.EventPermissionDenied:    runtime.ClaudeEventPermissionDenied,
	registration.EventPostToolUse:         runtime.ClaudeEventPostToolUse,
	registration.EventPostToolUseFailure:  runtime.ClaudeEventPostToolUseFailure,
	registration.EventPostToolBatch:       runtime.ClaudeEventPostToolBatch,
	registration.EventFileChanged:         runtime.ClaudeEventFileChanged,
	registration.EventCwdChanged:          runtime.ClaudeEventCwdChanged,
	registration.EventConfigChange:        runtime.ClaudeEventConfigChange,
	registration.EventInstructionsLoaded:  runtime.ClaudeEventInstructionsLoaded,
	registration.EventWorktreeCreate:      runtime.ClaudeEventWorktreeCreate,
	registration.EventWorktreeRemove:      runtime.ClaudeEventWorktreeRemove,
	registration.EventSubagentStart:       runtime.ClaudeEventSubagentStart,
	registration.EventSubagentStop:        runtime.ClaudeEventSubagentStop,
	registration.EventTeammateIdle:        runtime.ClaudeEventTeammateIdle,
	registration.EventTaskCreated:         runtime.ClaudeEventTaskCreated,
	registration.EventTaskCompleted:       runtime.ClaudeEventTaskCompleted,
	registration.EventPreCompact:          runtime.ClaudeEventPreCompact,
	registration.EventPostCompact:         runtime.ClaudeEventPostCompact,
	registration.EventNotification:        runtime.ClaudeEventNotification,
	registration.EventMessageDisplay:      runtime.ClaudeEventMessageDisplay,
	registration.EventElicitation:         runtime.ClaudeEventElicitation,
	registration.EventElicitationResult:   runtime.ClaudeEventElicitationResult,
}

// host is the pinned Claude data consumed by the generic frontend engine.
var host = frontend.Host[runtime.ClaudeLifecycleEvent]{
	Label:    "Claude",
	Contract: runtime.ClaudeCode2_1_210Lifecycle,
	Events:   claudeEventMappings,
}

// Bind is the Claude frontend boundary. It delegates to the generic
// strictest-common engine; behaviour and errors are host-labelled but shared.
func Bind(modelKind model.ContractEventKind, bindings []model.NativeBinding) (waist.L1, []waist.Identity, error) {
	return frontend.Bind(host, modelKind, bindings)
}
