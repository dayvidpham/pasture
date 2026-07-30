//go:generate go run ../../cmd/hostcontractgen

package hostcontract

import "github.com/dayvidpham/pasture/internal/lifecycle/model"

const (
	fSessionID model.NativeFieldID = iota + 1
	fTranscriptPath
	fCWD
	fPermissionMode
	fHookEventName
	fEffort
	fAgentID
	fAgentType
	fSource
	fModel
	fSessionTitle
	fTrigger
	fReason
	fPrompt
	fCommandName
	fStopHookActive
	fError
	fErrorType
	fToolName
	fToolInput
	fToolUseID
	fRequestID
	fToolOutput
	fBatchResults
	fFilePath
	fConfigSource
	fMemoryType
	fLoadReason
	fGlobs
	fTriggerFilePath
	fParentFilePath
	fAgentTranscriptPath
	fTeammateName
	fTaskID
	fMessage
	fNotificationType
	fTitle
	fContent
	fFields
	fMCPServerName
	fResponse
)

var claudeFields = []Field{
	{fSessionID, "FieldSessionID", "session_id"}, {fTranscriptPath, "FieldTranscriptPath", "transcript_path"}, {fCWD, "FieldCWD", "cwd"}, {fPermissionMode, "FieldPermissionMode", "permission_mode"},
	{fHookEventName, "FieldHookEventName", "hook_event_name"}, {fEffort, "FieldEffort", "effort"}, {fAgentID, "FieldAgentID", "agent_id"}, {fAgentType, "FieldAgentType", "agent_type"},
	{fSource, "FieldSource", "source"}, {fModel, "FieldModel", "model"}, {fSessionTitle, "FieldSessionTitle", "session_title"}, {fTrigger, "FieldTrigger", "trigger"}, {fReason, "FieldReason", "reason"},
	{fPrompt, "FieldPrompt", "prompt"}, {fCommandName, "FieldCommandName", "command_name"}, {fStopHookActive, "FieldStopHookActive", "stop_hook_active"}, {fError, "FieldError", "error"}, {fErrorType, "FieldErrorType", "error_type"},
	{fToolName, "FieldToolName", "tool_name"}, {fToolInput, "FieldToolInput", "tool_input"}, {fToolUseID, "FieldToolUseID", "tool_use_id"}, {fRequestID, "FieldRequestID", "request_id"}, {fToolOutput, "FieldToolOutput", "tool_output"},
	{fBatchResults, "FieldBatchResults", "batch_results"}, {fFilePath, "FieldFilePath", "file_path"}, {fConfigSource, "FieldConfigSource", "config_source"}, {fMemoryType, "FieldMemoryType", "memory_type"}, {fLoadReason, "FieldLoadReason", "load_reason"},
	{fGlobs, "FieldGlobs", "globs"}, {fTriggerFilePath, "FieldTriggerFilePath", "trigger_file_path"}, {fParentFilePath, "FieldParentFilePath", "parent_file_path"}, {fAgentTranscriptPath, "FieldAgentTranscriptPath", "agent_transcript_path"},
	{fTeammateName, "FieldTeammateName", "teammate_name"}, {fTaskID, "FieldTaskID", "task_id"}, {fMessage, "FieldMessage", "message"}, {fNotificationType, "FieldNotificationType", "notification_type"}, {fTitle, "FieldTitle", "title"},
	{fContent, "FieldContent", "content"}, {fFields, "FieldFields", "fields"}, {fMCPServerName, "FieldMCPServerName", "mcp_server_name"}, {fResponse, "FieldResponse", "response"},
}

var commonFields = []model.NativeFieldID{fSessionID, fTranscriptPath, fCWD, fPermissionMode, fHookEventName, fEffort, fAgentID, fAgentType}

func nativeEvent(kind model.ContractEventKind, symbol, name string, fields []model.NativeFieldID, identities []Identity, blocking BlockingMode, mutation MutationMode, failure FailureMode, stop StopLoopPolicy) Event {
	all := append([]model.NativeFieldID(nil), commonFields...)
	all = append(all, fields...)
	ids := append([]Identity{{Field: fSessionID, Binding: model.BindingSession, Required: true}}, identities...)
	return Event{Kind: kind, Symbol: symbol, Name: name, Fields: all, Identities: ids, Blocking: blocking, Mutation: mutation, Failure: failure, StopLoop: stop}
}

func ClaudeCode2_1_210() Contract {
	o := func(k model.ContractEventKind, s, n string, f ...model.NativeFieldID) Event {
		return nativeEvent(k, s, n, f, nil, NonBlocking, MutationNone, FailureReportAndContinue, StopLoopNotApplicable)
	}
	g := func(k model.ContractEventKind, s, n string, f ...model.NativeFieldID) Event {
		return nativeEvent(k, s, n, f, nil, Blocking, MutationNone, FailureExitTwoBlocks, StopLoopNotApplicable)
	}
	tool := []Identity{{Field: fToolUseID, Binding: model.BindingToolCall, Required: true}}
	request := []Identity{{Field: fRequestID, Binding: model.BindingRequest, Required: true}}
	agent := []Identity{{Field: fAgentID, Binding: model.BindingAgent, Required: true}}
	events := []Event{
		o(1, "EventSessionStart", "SessionStart", fSource, fModel, fSessionTitle), o(2, "EventSetup", "Setup", fTrigger), o(3, "EventSessionEnd", "SessionEnd", fReason), g(4, "EventUserPromptSubmit", "UserPromptSubmit", fPrompt), g(5, "EventUserPromptExpansion", "UserPromptExpansion", fPrompt, fCommandName),
		nativeEvent(6, "EventStop", "Stop", []model.NativeFieldID{fStopHookActive}, nil, Blocking, MutationNone, FailureExitTwoBlocks, StopLoopConsultWhenInactive), o(7, "EventStopFailure", "StopFailure", fError, fErrorType),
		nativeEvent(8, "EventPreToolUse", "PreToolUse", []model.NativeFieldID{fToolName, fToolInput, fToolUseID}, tool, Blocking, MutationInput, FailureExitTwoBlocks, StopLoopNotApplicable), nativeEvent(9, "EventPermissionRequest", "PermissionRequest", []model.NativeFieldID{fToolName, fToolInput, fRequestID}, request, Blocking, MutationNone, FailureExitTwoBlocks, StopLoopNotApplicable),
		o(10, "EventPermissionDenied", "PermissionDenied", fToolName, fToolInput), nativeEvent(11, "EventPostToolUse", "PostToolUse", []model.NativeFieldID{fToolName, fToolInput, fToolOutput, fToolUseID}, tool, NonBlocking, MutationNone, FailureReportAndContinue, StopLoopNotApplicable), nativeEvent(12, "EventPostToolUseFailure", "PostToolUseFailure", []model.NativeFieldID{fToolName, fToolInput, fError, fToolUseID}, tool, NonBlocking, MutationNone, FailureReportAndContinue, StopLoopNotApplicable),
		g(13, "EventPostToolBatch", "PostToolBatch", fBatchResults), o(14, "EventFileChanged", "FileChanged", fFilePath), o(15, "EventCwdChanged", "CwdChanged"), nativeEvent(16, "EventConfigChange", "ConfigChange", []model.NativeFieldID{fConfigSource}, nil, ConditionallyBlocking, MutationNone, FailureExitTwoBlocks, StopLoopNotApplicable), o(17, "EventInstructionsLoaded", "InstructionsLoaded", fFilePath, fMemoryType, fLoadReason, fGlobs, fTriggerFilePath, fParentFilePath), g(18, "EventWorktreeCreate", "WorktreeCreate"), o(19, "EventWorktreeRemove", "WorktreeRemove"),
		nativeEvent(20, "EventSubagentStart", "SubagentStart", nil, agent, NonBlocking, MutationNone, FailureReportAndContinue, StopLoopNotApplicable), nativeEvent(21, "EventSubagentStop", "SubagentStop", []model.NativeFieldID{fAgentTranscriptPath, fStopHookActive}, agent, Blocking, MutationNone, FailureExitTwoBlocks, StopLoopConsultWhenInactive), g(22, "EventTeammateIdle", "TeammateIdle", fTeammateName), g(23, "EventTaskCreated", "TaskCreated", fTaskID), g(24, "EventTaskCompleted", "TaskCompleted", fTaskID), g(25, "EventPreCompact", "PreCompact", fTrigger), o(26, "EventPostCompact", "PostCompact", fTrigger), o(27, "EventNotification", "Notification", fMessage, fNotificationType, fTitle), o(28, "EventMessageDisplay", "MessageDisplay", fMessage, fContent),
		nativeEvent(29, "EventElicitation", "Elicitation", []model.NativeFieldID{fRequestID, fFields, fMCPServerName}, request, Blocking, MutationNone, FailureExitTwoBlocks, StopLoopNotApplicable), nativeEvent(30, "EventElicitationResult", "ElicitationResult", []model.NativeFieldID{fRequestID, fResponse, fMCPServerName}, request, Blocking, MutationNone, FailureExitTwoBlocks, StopLoopNotApplicable),
	}
	return Contract{Version: "2.1.210", Fields: append([]Field(nil), claudeFields...), Events: events}
}
