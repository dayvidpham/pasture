// Package claude is the Claude Code frontend: it translates Claude's own
// native hook payloads into Pasture's target-agnostic lifecycle IR.
//
// It is a frontend in the compiler sense — part of Pasture, written in Go,
// linked into the binary, emitting IR. Reading Claude's JSON here no more
// makes that JSON a Pasture API than parsing C makes arbitrary text a
// compiler API. The generated hook registration on Claude's side stays
// trivial: it forwards the payload unchanged and does nothing else.
//
// This package owns exactly one kind of knowledge: what Claude's payload
// LOOKS LIKE. What a Claude event MEANS is owned by internal/runtime's pinned
// contract table and is never restated here — a frontend that duplicated
// target semantics would be the "every frontend knows the backend" mistake
// this architecture exists to remove.
package claude

import (
	"slices"

	"github.com/dayvidpham/pasture/internal/runtime"
)

// hookEventNameField is the field every Claude command-hook payload carries
// naming the event that fired. It is the frontend's entry point into the
// pinned contract: the payload's own claim about what happened, which is then
// checked against the hook registration that invoked Pasture.
const hookEventNameField = "hook_event_name"

// claudeCommonFields are present on every Claude command-hook payload for the
// pinned host contract, regardless of which event fired.
//
// transcript_path deserves specific mention: it is ALLOWED to appear (refusing
// a field Claude always sends would make every real invocation fail) but it is
// never read. It is not a declared correlation field in the pinned contract,
// so it cannot become an identity, and its contents are never opened, parsed,
// or forwarded. A conversation transcript is not evidence and is not identity.
var claudeCommonFields = []string{
	"agent_id",
	"agent_type",
	"cwd",
	"effort",
	hookEventNameField,
	"permission_mode",
	"session_id",
	"transcript_path",
}

// claudeEventFields are the additional fields each pinned Claude event may
// carry beyond the common set.
//
// This table describes Claude's payload SHAPE for the pinned host version. It
// deliberately does not describe what any event means; that lives once, in the
// reviewed contract table, and is read from there.
//
// A field being listed here grants permission to appear, not permission to be
// used. Only fields the pinned contract declares as correlation identities are
// ever read; everything else is accepted and discarded. That asymmetry is what
// lets Pasture tolerate Claude adding payload detail without letting new
// payload detail silently acquire meaning.
//
// This table is per-event-union equivalent to claudeNativeFields in
// internal/codegen/claude_hooks.go, which pins the same host payload shape for
// the generated hook adapter, across all thirty pinned events. The two are
// separate because that one is keyed by native name for a code generator and
// this one is keyed by the typed catalogue value for a parser, and because
// this package must not import internal/codegen. Nothing mechanically holds
// them equal: claudeNativeFields is unexported, so a drift test here cannot
// reach it. A field added to one must be added to the other.
var claudeEventFields = map[runtime.ClaudeLifecycleEvent][]string{
	runtime.ClaudeEventSessionStart:        {"model", "session_title", "source"},
	runtime.ClaudeEventSetup:               {"trigger"},
	runtime.ClaudeEventSessionEnd:          {"reason"},
	runtime.ClaudeEventUserPromptSubmit:    {"prompt"},
	runtime.ClaudeEventUserPromptExpansion: {"command_name", "prompt"},
	runtime.ClaudeEventStop:                {"stop_hook_active"},
	runtime.ClaudeEventStopFailure:         {"error", "error_type"},
	runtime.ClaudeEventPreToolUse:          {"tool_input", "tool_name", "tool_use_id"},
	runtime.ClaudeEventPermissionRequest:   {"request_id", "tool_input", "tool_name"},
	runtime.ClaudeEventPermissionDenied:    {"tool_input", "tool_name"},
	runtime.ClaudeEventPostToolUse:         {"tool_input", "tool_name", "tool_output", "tool_use_id"},
	runtime.ClaudeEventPostToolUseFailure:  {"error", "tool_input", "tool_name", "tool_use_id"},
	runtime.ClaudeEventPostToolBatch:       {"batch_results"},
	runtime.ClaudeEventFileChanged:         {"file_path"},
	runtime.ClaudeEventCwdChanged:          nil,
	runtime.ClaudeEventConfigChange:        {"config_source"},
	runtime.ClaudeEventInstructionsLoaded:  {"file_path", "globs", "load_reason", "memory_type", "parent_file_path", "trigger_file_path"},
	runtime.ClaudeEventWorktreeCreate:      nil,
	runtime.ClaudeEventWorktreeRemove:      nil,
	runtime.ClaudeEventSubagentStart:       nil,
	runtime.ClaudeEventSubagentStop:        {"agent_transcript_path", "stop_hook_active"},
	runtime.ClaudeEventTeammateIdle:        {"teammate_name"},
	runtime.ClaudeEventTaskCreated:         {"task_id"},
	runtime.ClaudeEventTaskCompleted:       {"task_id"},
	runtime.ClaudeEventPreCompact:          {"trigger"},
	runtime.ClaudeEventPostCompact:         {"trigger"},
	runtime.ClaudeEventNotification:        {"message", "notification_type", "title"},
	runtime.ClaudeEventMessageDisplay:      {"content", "message"},
	runtime.ClaudeEventElicitation:         {"fields", "mcp_server_name", "request_id"},
	runtime.ClaudeEventElicitationResult:   {"mcp_server_name", "request_id", "response"},
}

// allowedFields returns the complete sorted set of payload fields one pinned
// Claude event may carry: the common fields, the event's own extras, and every
// correlation field the pinned contract declares for it.
//
// Deriving the identity fields from the contract rather than restating them
// keeps the two tables from drifting: a correlation field the contract adds is
// automatically admissible, and one it removes automatically stops being read.
func allowedFields(event runtime.ClaudeLifecycleEvent, identities []runtime.NativeIdentityField) []string {
	extras := claudeEventFields[event]

	fields := make([]string, 0, len(claudeCommonFields)+len(extras)+len(identities))
	fields = append(fields, claudeCommonFields...)
	fields = append(fields, extras...)
	for _, identity := range identities {
		fields = append(fields, identity.NativeName())
	}

	slices.Sort(fields)
	return slices.Compact(fields)
}
