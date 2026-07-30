package hostcontract

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// ClaudeCode2_1_210 is the generator-facing typed host contract. The checked-in
// generated manifest is converted here so hostcontractgen can verify every
// ordinal and behavior without exposing this host-only package to consumers.
func ClaudeCode2_1_210() Contract {
	manifest := registration.ClaudeCode2_1_210()
	fields := make([]Field, 0, 41)
	for id := model.NativeFieldID(1); id <= 41; id++ {
		fields = append(fields, Field{ID: id, Name: fieldName(id)})
	}
	events := make([]Event, len(manifest.Events))
	for i, item := range manifest.Events {
		ids := make([]Identity, len(item.Identities))
		for j, identity := range item.Identities {
			ids[j] = Identity{Field: identity.Field, Binding: identity.Binding, Required: identity.Required}
		}
		events[i] = Event{Kind: item.Kind, Name: item.NativeName, Fields: item.Fields(), Identities: ids, Blocking: BlockingMode(item.Blocking), Mutation: MutationMode(item.Mutation), Failure: FailureMode(item.Failure), StopLoop: StopLoopPolicy(item.StopLoop)}
	}
	return Contract{Version: "2.1.210", Fields: fields, Events: events}
}

func fieldName(id model.NativeFieldID) string {
	names := [...]string{"session_id", "transcript_path", "cwd", "permission_mode", "hook_event_name", "effort", "agent_id", "agent_type", "source", "model", "session_title", "trigger", "reason", "prompt", "command_name", "stop_hook_active", "error", "error_type", "tool_name", "tool_input", "tool_use_id", "request_id", "tool_output", "batch_results", "file_path", "config_source", "memory_type", "load_reason", "globs", "trigger_file_path", "parent_file_path", "agent_transcript_path", "teammate_name", "task_id", "message", "notification_type", "title", "content", "fields", "mcp_server_name", "response"}
	if id == 0 || int(id) > len(names) {
		return ""
	}
	return names[int(id)-1]
}
