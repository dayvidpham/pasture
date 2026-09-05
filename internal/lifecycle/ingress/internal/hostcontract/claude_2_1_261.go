//go:generate go run ../../cmd/hostcontractgen

package hostcontract

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

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
	fPromptID
	fToolResponse
	fDurationMS
	fIsInterrupt
	fToolCalls
	fCustomInstructions
	fCompactSummary
	fMode
	fRequestedSchema
	fAction
	fScratchpadDir
)

var claudeFields = []Field{
	{fSessionID, "FieldSessionID", "session_id"}, {fScratchpadDir, "FieldScratchpadDir", "scratchpad_dir"}, {fTranscriptPath, "FieldTranscriptPath", "transcript_path"}, {fCWD, "FieldCWD", "cwd"}, {fPermissionMode, "FieldPermissionMode", "permission_mode"},
	{fHookEventName, "FieldHookEventName", "hook_event_name"}, {fEffort, "FieldEffort", "effort"}, {fAgentID, "FieldAgentID", "agent_id"}, {fAgentType, "FieldAgentType", "agent_type"},
	{fSource, "FieldSource", "source"}, {fModel, "FieldModel", "model"}, {fSessionTitle, "FieldSessionTitle", "session_title"}, {fTrigger, "FieldTrigger", "trigger"}, {fReason, "FieldReason", "reason"},
	{fPrompt, "FieldPrompt", "prompt"}, {fCommandName, "FieldCommandName", "command_name"}, {fStopHookActive, "FieldStopHookActive", "stop_hook_active"}, {fError, "FieldError", "error"}, {fErrorType, "FieldErrorType", "error_type"},
	{fToolName, "FieldToolName", "tool_name"}, {fToolInput, "FieldToolInput", "tool_input"}, {fToolUseID, "FieldToolUseID", "tool_use_id"}, {fRequestID, "FieldRequestID", "request_id"}, {fToolOutput, "FieldToolOutput", "tool_output"},
	{fBatchResults, "FieldBatchResults", "batch_results"}, {fFilePath, "FieldFilePath", "file_path"}, {fConfigSource, "FieldConfigSource", "config_source"}, {fMemoryType, "FieldMemoryType", "memory_type"}, {fLoadReason, "FieldLoadReason", "load_reason"},
	{fGlobs, "FieldGlobs", "globs"}, {fTriggerFilePath, "FieldTriggerFilePath", "trigger_file_path"}, {fParentFilePath, "FieldParentFilePath", "parent_file_path"}, {fAgentTranscriptPath, "FieldAgentTranscriptPath", "agent_transcript_path"},
	{fTeammateName, "FieldTeammateName", "teammate_name"}, {fTaskID, "FieldTaskID", "task_id"}, {fMessage, "FieldMessage", "message"}, {fNotificationType, "FieldNotificationType", "notification_type"}, {fTitle, "FieldTitle", "title"},
	{fContent, "FieldContent", "content"}, {fFields, "FieldFields", "fields"}, {fMCPServerName, "FieldMCPServerName", "mcp_server_name"}, {fResponse, "FieldResponse", "response"},
	{fPromptID, "FieldPromptID", "prompt_id"}, {fToolResponse, "FieldToolResponse", "tool_response"}, {fDurationMS, "FieldDurationMS", "duration_ms"}, {fIsInterrupt, "FieldIsInterrupt", "is_interrupt"}, {fToolCalls, "FieldToolCalls", "tool_calls"},
	{fCustomInstructions, "FieldCustomInstructions", "custom_instructions"}, {fCompactSummary, "FieldCompactSummary", "compact_summary"}, {fMode, "FieldMode", "mode"}, {fRequestedSchema, "FieldRequestedSchema", "requested_schema"}, {fAction, "FieldAction", "action"},
}

// scratchpad_dir is present on every payload the 2.1.261 host wrote in the
// committed captures (all eight enabled events); it was absent at 2.1.210, so a
// registration without it refuses every 2.1.261 payload as carrying a member it
// does not allow. It is a path, never an identity.
var commonFields = []model.NativeFieldID{fSessionID, fTranscriptPath, fCWD, fPermissionMode, fHookEventName, fEffort, fAgentID, fAgentType, fPromptID, fScratchpadDir}

func nativeEvent(kind model.ContractEventKind, symbol, name string, fields []model.NativeFieldID, identities []Identity, blocking BlockingMode, mutation MutationMode, failure pastureruntime.FailureMode, stop StopLoopPolicy) Event {
	all := append([]model.NativeFieldID(nil), commonFields...)
	all = append(all, fields...)
	ids := append([]Identity{{Field: fSessionID, Binding: model.BindingSession, Required: true}}, identities...)
	return Event{Kind: kind, Symbol: symbol, Name: name, Fields: all, Identities: ids, Blocking: blocking, Mutation: mutation, Failure: failure, StopLoop: stop}
}

// ClaudeCode2_1_261 is the Claude Code host contract at the recorded version
// (Contract.Version, one of the two version roots; a test binds it to the
// runtime contract id). Optional non-identity fields include names observed in
// the authentic captures committed under
// internal/lifecycle/ingress/claude/testdata; required identity fields remain
// the authority boundary.
func ClaudeCode2_1_261() Contract {
	o := func(k model.ContractEventKind, s, n string, f ...model.NativeFieldID) Event {
		return nativeEvent(k, s, n, f, nil, NonBlocking, MutationNone, pastureruntime.FailureReportAndContinue, StopLoopNotApplicable)
	}
	g := func(k model.ContractEventKind, s, n string, f ...model.NativeFieldID) Event {
		return nativeEvent(k, s, n, f, nil, Blocking, MutationNone, pastureruntime.FailureExitTwoBlocks, StopLoopNotApplicable)
	}
	tool := []Identity{{Field: fToolUseID, Binding: model.BindingToolCall, Required: true}}
	request := []Identity{{Field: fRequestID, Binding: model.BindingRequest, Required: true}}
	agent := []Identity{{Field: fAgentID, Binding: model.BindingAgent, Required: true}}
	events := []Event{
		o(1, "EventSessionStart", "SessionStart", fSource, fModel, fSessionTitle), o(2, "EventSetup", "Setup", fTrigger), o(3, "EventSessionEnd", "SessionEnd", fReason), g(4, "EventUserPromptSubmit", "UserPromptSubmit", fPrompt), g(5, "EventUserPromptExpansion", "UserPromptExpansion", fPrompt, fCommandName),
		nativeEvent(6, "EventStop", "Stop", []model.NativeFieldID{fStopHookActive}, nil, Blocking, MutationNone, pastureruntime.FailureExitTwoBlocks, StopLoopConsultWhenInactive), o(7, "EventStopFailure", "StopFailure", fError, fErrorType),
		nativeEvent(8, "EventPreToolUse", "PreToolUse", []model.NativeFieldID{fToolName, fToolInput, fToolUseID}, tool, Blocking, MutationInput, pastureruntime.FailureExitTwoBlocks, StopLoopNotApplicable), nativeEvent(9, "EventPermissionRequest", "PermissionRequest", []model.NativeFieldID{fToolName, fToolInput, fRequestID}, request, Blocking, MutationNone, pastureruntime.FailureExitTwoBlocks, StopLoopNotApplicable),
		o(10, "EventPermissionDenied", "PermissionDenied", fToolName, fToolInput), nativeEvent(11, "EventPostToolUse", "PostToolUse", []model.NativeFieldID{fToolName, fToolInput, fToolOutput, fToolResponse, fDurationMS, fToolUseID}, tool, NonBlocking, MutationNone, pastureruntime.FailureReportAndContinue, StopLoopNotApplicable), nativeEvent(12, "EventPostToolUseFailure", "PostToolUseFailure", []model.NativeFieldID{fToolName, fToolInput, fError, fIsInterrupt, fDurationMS, fToolUseID}, tool, NonBlocking, MutationNone, pastureruntime.FailureReportAndContinue, StopLoopNotApplicable),
		g(13, "EventPostToolBatch", "PostToolBatch", fBatchResults, fToolCalls), o(14, "EventFileChanged", "FileChanged", fFilePath), o(15, "EventCwdChanged", "CwdChanged"), nativeEvent(16, "EventConfigChange", "ConfigChange", []model.NativeFieldID{fConfigSource}, nil, ConditionallyBlocking, MutationNone, pastureruntime.FailureExitTwoBlocks, StopLoopNotApplicable), o(17, "EventInstructionsLoaded", "InstructionsLoaded", fFilePath, fMemoryType, fLoadReason, fGlobs, fTriggerFilePath, fParentFilePath), g(18, "EventWorktreeCreate", "WorktreeCreate"), o(19, "EventWorktreeRemove", "WorktreeRemove"),
		nativeEvent(20, "EventSubagentStart", "SubagentStart", nil, agent, NonBlocking, MutationNone, pastureruntime.FailureReportAndContinue, StopLoopNotApplicable), nativeEvent(21, "EventSubagentStop", "SubagentStop", []model.NativeFieldID{fAgentTranscriptPath, fStopHookActive}, agent, Blocking, MutationNone, pastureruntime.FailureExitTwoBlocks, StopLoopConsultWhenInactive), g(22, "EventTeammateIdle", "TeammateIdle", fTeammateName), g(23, "EventTaskCreated", "TaskCreated", fTaskID), g(24, "EventTaskCompleted", "TaskCompleted", fTaskID), g(25, "EventPreCompact", "PreCompact", fTrigger, fCustomInstructions), o(26, "EventPostCompact", "PostCompact", fTrigger, fCompactSummary), o(27, "EventNotification", "Notification", fMessage, fNotificationType, fTitle), o(28, "EventMessageDisplay", "MessageDisplay", fMessage, fContent),
		nativeEvent(29, "EventElicitation", "Elicitation", []model.NativeFieldID{fRequestID, fFields, fMCPServerName, fMessage, fMode, fRequestedSchema}, request, Blocking, MutationNone, pastureruntime.FailureExitTwoBlocks, StopLoopNotApplicable), nativeEvent(30, "EventElicitationResult", "ElicitationResult", []model.NativeFieldID{fRequestID, fResponse, fMCPServerName, fMode, fAction, fContent}, request, Blocking, MutationNone, pastureruntime.FailureExitTwoBlocks, StopLoopNotApplicable),
		// Registered at Claude Code 2.1.261 from the installed binary's hook-event
		// enumeration and the hooks reference (https://code.claude.com/docs/en/hooks);
		// absent at 2.1.210. No authentic capture of these events exists, so each
		// row declares the common fields only and no identity beyond them; a
		// capture decides its optional fields. They stay withheld until a capture
		// and a production proof land.
		//
		// The blocking mode of each of the three rows is decided by the exit-code
		// paragraph the installed 2.1.261 binary carries for that event in its own
		// hook-event table. The paragraphs are quoted in the pinning test
		// (claude_2_1_261_test.go) and in the runtime profile's citation:
		//   - PreModelSwitch is a GATE: "Exit code 2 - block the switch and show
		//     stderr to user". The same binary also carries "model switch blocked
		//     by a PreModelSwitch hook" and "A PreModelSwitch hook asked you to
		//     confirm", so a hook can refuse the operator's model switch.
		//   - PostModelSwitch is an OBSERVATION: its paragraph names exit code 0
		//     and "Other exit codes - show stderr to user only". The binary carries
		//     no blocking string for it.
		//   - DirectoryAdded is an OBSERVATION: its paragraph names exit code 0 and
		//     debug-logged stderr on other exit codes. The binary carries no
		//     blocking string for it, only "DirectoryAdded hook execution failed"
		//     and "DirectoryAdded hook failed".
		g(31, "EventPreModelSwitch", "PreModelSwitch"), o(32, "EventPostModelSwitch", "PostModelSwitch"), o(33, "EventDirectoryAdded", "DirectoryAdded"),
	}
	return Contract{Version: "2.1.261", Fields: append([]Field(nil), claudeFields...), Events: events}
}
