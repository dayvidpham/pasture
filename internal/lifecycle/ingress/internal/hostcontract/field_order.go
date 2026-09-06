package hostcontract

// FieldAllocationOrder owns the numeric identity of generated native fields.
// Append new symbols here, even when their contract appears earlier in the
// generation walk. Never reorder or remove existing entries: their position
// plus one is the field ID. Contract-local Field.ID values only resolve field
// references within a contract and do not allocate these shared IDs.
// The generator checks this list against the complete contract population.
func FieldAllocationOrder() []string {
	return []string{
		"FieldSessionID", "FieldScratchpadDir", "FieldTranscriptPath", "FieldCWD", "FieldPermissionMode",
		"FieldHookEventName", "FieldEffort", "FieldAgentID", "FieldAgentType", "FieldSource",
		"FieldModel", "FieldSessionTitle", "FieldTrigger", "FieldReason", "FieldPrompt",
		"FieldCommandName", "FieldStopHookActive", "FieldError", "FieldErrorType", "FieldToolName",
		"FieldToolInput", "FieldToolUseID", "FieldRequestID", "FieldToolOutput", "FieldBatchResults",
		"FieldFilePath", "FieldConfigSource", "FieldMemoryType", "FieldLoadReason", "FieldGlobs",
		"FieldTriggerFilePath", "FieldParentFilePath", "FieldAgentTranscriptPath", "FieldTeammateName", "FieldTaskID",
		"FieldMessage", "FieldNotificationType", "FieldTitle", "FieldContent", "FieldFields",
		"FieldMCPServerName", "FieldResponse", "FieldPromptID", "FieldToolResponse", "FieldDurationMS",
		"FieldIsInterrupt", "FieldToolCalls", "FieldCustomInstructions", "FieldCompactSummary", "FieldMode",
		"FieldRequestedSchema", "FieldAction", "FieldOpenCodeSessionID", "FieldOpenCodeCallID", "FieldCodexSessionID",
		"FieldCodexTurnID", "FieldCodexToolUseID",
		"FieldFileEvent",
	}
}
