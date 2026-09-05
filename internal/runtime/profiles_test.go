package runtime_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// classify returns the runtime class every pinned contract assigns to a core
// operation, exercising the real lookup path (unsupported yields an error and
// therefore no binding).
func classify(t *testing.T, contract runtime.RuntimeContract, kind ir.OperationKind) (effects.RuntimeClass, string) {
	t.Helper()
	descriptor, ok := runtime.CoreOperationDescriptorFor(kind)
	require.True(t, ok)
	binding, err := runtime.LookupOperationBinding(contract, descriptor)
	if err != nil {
		return effects.RuntimeClassUnsupported, nativeCallName(binding)
	}
	return binding.Class(), nativeCallName(binding)
}

func nativeCallName(binding runtime.RuntimeBinding[runtime.OrchestrationRequest, runtime.OrchestrationResult]) string {
	if binding == nil {
		return ""
	}
	if call, ok := binding.Native(); ok {
		return call.CallName()
	}
	return ""
}

func TestPinnedContractsClassifyEveryCoreOperation(t *testing.T) {
	t.Parallel()
	for _, contract := range runtime.PinnedContracts() {
		contract := contract
		t.Run(contract.ID().String(), func(t *testing.T) {
			t.Parallel()
			for _, kind := range ir.AllOperationKinds() {
				class, _ := classify(t, contract, kind)
				assert.True(t, class.IsValid(), "operation %q has a valid classification", kind)
			}
		})
	}
}

func TestClaudeContractNamesNoRemovedTeamLifecycleCalls(t *testing.T) {
	t.Parallel()
	claude := runtime.ClaudeCode2_1_261()
	for _, kind := range ir.AllOperationKinds() {
		_, callName := classify(t, claude, kind)
		lowered := strings.ToLower(callName)
		assert.NotContains(t, lowered, "teamcreate", "operation %q must not name a removed team-lifecycle call", kind)
		assert.NotContains(t, lowered, "teamdelete", "operation %q must not name a removed team-lifecycle call", kind)
	}
}

func TestOpenCodeContractInventsNoTools(t *testing.T) {
	t.Parallel()
	opencode := runtime.OpenCode1_18_29()

	// No invented persistent-message / follow-up / wait native tools.
	forbidden := []string{"task_agent_message", "follow_up", "followup", "wait", "task_close"}
	for _, kind := range ir.AllOperationKinds() {
		_, callName := classify(t, opencode, kind)
		lowered := strings.ToLower(callName)
		for _, name := range forbidden {
			assert.NotContains(t, lowered, name, "operation %q must not invent OpenCode tool %q", kind, name)
		}
	}

	// Stopping an assignment is explicitly unsupported, not a fabricated close.
	class, _ := classify(t, opencode, ir.OperationStopAssignment)
	assert.Equal(t, effects.RuntimeClassUnsupported, class)

	// Only documented surfaces appear as native calls.
	skillClass, skillCall := classify(t, opencode, ir.OperationInvokeSkill)
	assert.Equal(t, effects.RuntimeClassNative, skillClass)
	assert.Equal(t, "skill", skillCall)
	taskClass, taskCall := classify(t, opencode, ir.OperationDelegateAssignment)
	assert.Equal(t, effects.RuntimeClassNative, taskClass)
	assert.Equal(t, "task", taskCall)
	questionClass, questionCall := classify(t, opencode, ir.OperationRequestUserDecision)
	assert.Equal(t, effects.RuntimeClassNative, questionClass)
	assert.Equal(t, "question", questionCall)
}

// Every runtime contract admits a FLOOR at its recorded host version: that
// version and every later release are admitted; the release below it, a
// prerelease of it and an unparsed host are refused. The population is the
// contracts themselves, so a contract added later is covered without an edit.
func TestPinnedContractVersionBoundaries(t *testing.T) {
	t.Parallel()
	contracts := runtime.PinnedContracts()
	require.Len(t, contracts, 3, "one runtime contract per enabled harness")
	harnesses := make(map[ir.HarnessID]int, len(contracts))
	for _, contract := range contracts {
		harnesses[contract.Harness()]++
	}
	for harness, count := range harnesses {
		require.Equal(t, 1, count, "harness %s has one runtime contract", harness)
	}
	for _, contract := range contracts {
		contract := contract
		t.Run(contract.ID().String(), func(t *testing.T) {
			t.Parallel()
			min := contract.Versions().Min()
			assert.False(t, contract.Versions().HasUpperBound(), "admission is a floor with no upper bound")
			assert.True(t, contract.Supports(min), "the recorded version is admitted")
			assert.True(t, contract.Supports(mustParse(t, min.String()+"+build.5")), "build metadata does not change precedence")
			assert.True(t, contract.Supports(testutil.Bump(t, min, 0, 0, 1)), "the next patch release is admitted")
			assert.True(t, contract.Supports(testutil.Bump(t, min, 0, 1, 0)), "a later minor release is admitted")
			assert.True(t, contract.Supports(testutil.Bump(t, min, 1, 0, 0)), "a later major release is admitted")
			assert.False(t, contract.Supports(testutil.BelowFloor(t, min)), "the release below the recorded version is refused")
			assert.False(t, contract.Supports(mustParse(t, min.String()+"-rc.1")), "a prerelease of the recorded version is refused")
			assert.False(t, contract.Supports(runtime.HostVersion{}), "unparsed host rejected")
		})
	}
}

func TestPinnedContractHarnessBinding(t *testing.T) {
	t.Parallel()
	assert.Equal(t, ir.HarnessClaudeCode, runtime.ClaudeCode2_1_261().Harness())
	assert.Equal(t, ir.HarnessOpenCode, runtime.OpenCode1_18_29().Harness())
	assert.Equal(t, ir.HarnessCodex, runtime.Codex0_153_0().Harness())
}

// lifecycleProfileRow is one pinned lifecycle row rendered as strings, so the
// committed table below reads as a table and can be searched by row, rather
// than as an opaque golden blob. Identities render as kind:name:requirement,
// separated by one space; unresolved kinds render the same way.
type lifecycleProfileRow struct {
	Harness         string
	NativeName      string
	Semantic        string
	Surface         string
	Blocking        string
	Mutation        string
	Order           string
	Reconciliation  string
	Failure         string
	DeclaredFailure string
	EvidenceSource  string
	StopLoop        string
	Identities      string
	Unresolved      string
}

func lifecycleProfileRowOf(harness ir.HarnessID, mapping runtime.LifecycleEventMapping) lifecycleProfileRow {
	identities := make([]string, 0, len(mapping.Identities()))
	for _, field := range mapping.Identities() {
		requirement := "optional"
		if field.Required() {
			requirement = "required"
		}
		identities = append(identities, fmt.Sprintf("%s:%s:%s", field.Kind(), field.NativeName(), requirement))
	}
	unresolved := make([]string, 0, len(mapping.UnresolvedIdentities()))
	for _, kind := range mapping.UnresolvedIdentities() {
		unresolved = append(unresolved, kind.String())
	}
	return lifecycleProfileRow{
		Harness:         string(harness),
		NativeName:      mapping.NativeName(),
		Semantic:        mapping.Semantic().String(),
		Surface:         mapping.Surface().String(),
		Blocking:        mapping.Blocking().String(),
		Mutation:        mapping.Mutation().String(),
		Order:           mapping.Order().String(),
		Reconciliation:  mapping.Reconciliation().String(),
		Failure:         mapping.Failure().String(),
		DeclaredFailure: mapping.DeclaredFailure().String(),
		EvidenceSource:  mapping.Evidence().Source,
		StopLoop:        mapping.StopLoop().String(),
		Identities:      strings.Join(identities, " "),
		Unresolved:      strings.Join(unresolved, " "),
	}
}

// collectLifecycleProfileRows renders every row of one pinned contract. The
// population is the contract's own event list, so a row added to a profile is
// covered without an edit here; the table below then has to grow, and the
// test says so.
func collectLifecycleProfileRows[E comparable](t *testing.T, contract runtime.LifecycleContract[E], into map[string]lifecycleProfileRow) int {
	t.Helper()
	events := contract.Events()
	for _, event := range events {
		mapping, err := contract.Mapping(event)
		require.NoError(t, err)
		row := lifecycleProfileRowOf(contract.Harness(), mapping)
		into[row.Harness+"/"+row.NativeName] = row
	}
	return len(events)
}

// pinnedLifecycleProfileRows is EVERY row of the three pinned lifecycle
// profiles, captured from the single-file profile before it was split into one
// file per harness (pasture commit d3edb79). The split moves rows between files
// and changes none of them, and this table is what holds that: a row that
// gains, loses or changes any column during a move turns the test below RED,
// naming the harness and the event.
//
// It is a table of strings and not a golden file so that a reader can read
// one row and check it against the profile source directly.
var pinnedLifecycleProfileRows = []lifecycleProfileRow{
	{"claude-code", "SessionStart", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "Setup", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "SessionEnd", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "UserPromptSubmit", "gate-consultation", "claude-command-json", "blocking", "none", "concurrent-native", "host-native", "exit-2-blocks", "exit-2-blocks", "https://docs.claude.com/en/docs/claude-code/hooks", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "UserPromptExpansion", "gate-consultation", "claude-command-json", "blocking", "none", "concurrent-native", "host-native", "report-and-continue", "exit-2-blocks", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "Stop", "gate-consultation", "claude-command-json", "blocking", "none", "concurrent-native", "host-native", "exit-2-blocks", "exit-2-blocks", "https://docs.claude.com/en/docs/claude-code/hooks", "consult-when-inactive", "session:session_id:required", ""},
	{"claude-code", "StopFailure", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "PreToolUse", "gate-consultation", "claude-command-json", "blocking", "input", "concurrent-native", "host-native", "exit-2-blocks", "exit-2-blocks", "https://docs.claude.com/en/docs/claude-code/hooks", "not-applicable", "session:session_id:required tool-call:tool_use_id:required", ""},
	{"claude-code", "PermissionRequest", "gate-consultation", "claude-command-json", "blocking", "none", "concurrent-native", "host-native", "report-and-continue", "exit-2-blocks", "", "not-applicable", "session:session_id:required request:request_id:required", ""},
	{"claude-code", "PermissionDenied", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "PostToolUse", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required tool-call:tool_use_id:required", ""},
	{"claude-code", "PostToolUseFailure", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required tool-call:tool_use_id:required", ""},
	{"claude-code", "PostToolBatch", "gate-consultation", "claude-command-json", "blocking", "none", "concurrent-native", "host-native", "report-and-continue", "exit-2-blocks", "", "not-applicable", "session:session_id:required", "tool-call"},
	{"claude-code", "FileChanged", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "CwdChanged", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "ConfigChange", "gate-consultation", "claude-command-json", "conditionally-blocking", "none", "concurrent-native", "host-native", "report-and-continue", "exit-2-blocks", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "InstructionsLoaded", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "WorktreeCreate", "gate-consultation", "claude-command-json", "blocking", "none", "concurrent-native", "host-native", "report-and-continue", "exit-2-blocks", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "WorktreeRemove", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "SubagentStart", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required agent:agent_id:required", ""},
	{"claude-code", "SubagentStop", "gate-consultation", "claude-command-json", "blocking", "none", "concurrent-native", "host-native", "exit-2-blocks", "exit-2-blocks", "https://docs.claude.com/en/docs/claude-code/hooks", "consult-when-inactive", "session:session_id:required agent:agent_id:required", ""},
	{"claude-code", "TeammateIdle", "gate-consultation", "claude-command-json", "blocking", "none", "concurrent-native", "host-native", "report-and-continue", "exit-2-blocks", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "TaskCreated", "gate-consultation", "claude-command-json", "blocking", "none", "concurrent-native", "host-native", "report-and-continue", "exit-2-blocks", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "TaskCompleted", "gate-consultation", "claude-command-json", "blocking", "none", "concurrent-native", "host-native", "report-and-continue", "exit-2-blocks", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "PreCompact", "gate-consultation", "claude-command-json", "blocking", "none", "concurrent-native", "host-native", "report-and-continue", "exit-2-blocks", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "PostCompact", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "Notification", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "MessageDisplay", "observation", "claude-command-json", "nonblocking", "none", "concurrent-native", "none", "report-and-continue", "report-and-continue", "", "not-applicable", "session:session_id:required", ""},
	{"claude-code", "Elicitation", "gate-consultation", "claude-command-json", "blocking", "none", "concurrent-native", "host-native", "report-and-continue", "exit-2-blocks", "", "not-applicable", "session:session_id:required request:request_id:required", ""},
	{"claude-code", "ElicitationResult", "explicit-human-response", "claude-command-json", "blocking", "none", "concurrent-native", "host-native", "report-and-continue", "exit-2-blocks", "", "not-applicable", "session:session_id:required request:request_id:required", ""},
	{"codex", "SessionStart", "observation", "codex-strict-command-json", "nonblocking", "none", "concurrent-native", "no-adapter-merge", "strict-hook-failure", "strict-hook-failure", "", "not-applicable", "session:session_id:required", ""},
	{"codex", "UserPromptSubmit", "gate-consultation", "codex-strict-command-json", "blocking", "none", "concurrent-native", "no-adapter-merge", "report-and-continue", "strict-output-exit-2-blocks", "", "not-applicable", "session:session_id:required turn:turn_id:required", ""},
	{"codex", "PreToolUse", "gate-consultation", "codex-strict-command-json", "blocking", "input", "concurrent-native", "no-adapter-merge", "report-and-continue", "strict-output-exit-2-blocks", "", "not-applicable", "session:session_id:required turn:turn_id:required tool-call:tool_use_id:required", ""},
	{"codex", "PermissionRequest", "gate-consultation", "codex-strict-command-json", "blocking", "none", "concurrent-native", "no-adapter-merge", "report-and-continue", "strict-output-exit-2-blocks", "", "not-applicable", "session:session_id:required turn:turn_id:required", ""},
	{"codex", "PostToolUse", "gate-consultation", "codex-strict-command-json", "blocking", "output", "concurrent-native", "no-adapter-merge", "report-and-continue", "strict-output-exit-2-blocks", "", "not-applicable", "session:session_id:required turn:turn_id:required tool-call:tool_use_id:required", ""},
	{"codex", "PreCompact", "gate-consultation", "codex-strict-command-json", "blocking", "none", "concurrent-native", "no-adapter-merge", "report-and-continue", "strict-output-exit-2-blocks", "", "not-applicable", "session:session_id:required turn:turn_id:required", ""},
	{"codex", "PostCompact", "gate-consultation", "codex-strict-command-json", "blocking", "none", "concurrent-native", "no-adapter-merge", "report-and-continue", "strict-output-exit-2-blocks", "", "not-applicable", "session:session_id:required turn:turn_id:required", ""},
	{"codex", "SubagentStart", "observation", "codex-strict-command-json", "nonblocking", "none", "concurrent-native", "no-adapter-merge", "strict-hook-failure", "strict-hook-failure", "", "not-applicable", "session:session_id:required turn:turn_id:required agent:agent_id:required", ""},
	{"codex", "SubagentStop", "gate-consultation", "codex-strict-command-json", "blocking", "none", "concurrent-native", "no-adapter-merge", "report-and-continue", "strict-output-exit-2-blocks", "", "consult-when-inactive", "session:session_id:required turn:turn_id:required agent:agent_id:required", ""},
	{"codex", "Stop", "gate-consultation", "codex-strict-command-json", "blocking", "none", "concurrent-native", "no-adapter-merge", "report-and-continue", "strict-output-exit-2-blocks", "", "consult-when-inactive", "session:session_id:required turn:turn_id:required", ""},
	{"codex", "SessionEnd", "observation", "codex-strict-command-json", "nonblocking", "none", "concurrent-native", "no-adapter-merge", "strict-hook-failure", "strict-hook-failure", "", "not-applicable", "", ""},
	{"opencode", "command.executed", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "session:sessionID:required", ""},
	{"opencode", "file.edited", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "file.watcher.updated", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "installation.updated", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "installation.update-available", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "lsp.client.diagnostics", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "lsp.updated", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "message.updated", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "message.removed", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "message.part.updated", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "message.part.removed", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "permission.updated", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "permission.replied", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "server.connected", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "server.instance.disposed", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "session.created", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "session:sessionID:required", ""},
	{"opencode", "session.updated", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "session.deleted", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "session.compacted", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "session.diff", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "session.error", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "session.idle", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "session.status", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "todo.updated", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "tui.prompt.append", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "tui.command.execute", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "tui.toast.show", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "pty.created", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "pty.updated", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "pty.exited", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "pty.deleted", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "vcs.branch.updated", "observation", "opencode-catch-all-sse", "nonblocking", "none", "observation-stream", "none", "observe-only", "observe-only", "", "not-applicable", "", ""},
	{"opencode", "chat.message", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "session:sessionID:required message:messageID:optional", ""},
	{"opencode", "chat.params", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "session:sessionID:required", ""},
	{"opencode", "chat.headers", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "session:sessionID:required", ""},
	{"opencode", "permission.ask", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "", ""},
	{"opencode", "command.execute.before", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "session:sessionID:required", ""},
	{"opencode", "tool.execute.before", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "session:sessionID:required tool-call:callID:required", ""},
	{"opencode", "shell.env", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "session:sessionID:optional tool-call:callID:optional", ""},
	{"opencode", "tool.execute.after", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "session:sessionID:required tool-call:callID:required", ""},
	{"opencode", "experimental.chat.messages.transform", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "", ""},
	{"opencode", "experimental.chat.system.transform", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "session:sessionID:optional", ""},
	{"opencode", "experimental.provider.small_model", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "", ""},
	{"opencode", "experimental.session.compacting", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "session:sessionID:required", ""},
	{"opencode", "experimental.compaction.autocontinue", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "session:sessionID:required", ""},
	{"opencode", "experimental.text.complete", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "session:sessionID:required message:messageID:optional", ""},
	{"opencode", "tool.definition", "gate-consultation", "opencode-named-output", "blocking", "output-object", "sequential-load-order", "sequential-mutation", "throw-fail-fast", "throw-fail-fast", "", "not-applicable", "", ""},
}

// TestPinnedLifecycleProfileRowsMatchTheCommittedTable checks every row of every
// pinned lifecycle profile against the committed table, in both directions.
//
// The population is DERIVED from the three contracts' own event lists, so a
// row added to a profile is covered the moment it exists, and the table then
// has to be extended in the same change. The non-vacuity control is the row
// count: the derived population must be non-empty for every harness and its
// size must equal the table's, so a contract that silently returned no events,
// or a table that silently lost rows, cannot pass.
func TestPinnedLifecycleProfileRowsMatchTheCommittedTable(t *testing.T) {
	t.Parallel()

	observed := make(map[string]lifecycleProfileRow, len(pinnedLifecycleProfileRows))
	perHarness := map[string]int{
		string(ir.HarnessClaudeCode): collectLifecycleProfileRows(t, runtime.ClaudeCode2_1_261Lifecycle(), observed),
		string(ir.HarnessCodex):      collectLifecycleProfileRows(t, runtime.Codex0_153_0Lifecycle(), observed),
		string(ir.HarnessOpenCode):   collectLifecycleProfileRows(t, runtime.OpenCode1_18_29Lifecycle(), observed),
	}
	total := 0
	for harness, count := range perHarness {
		require.Positive(t, count, "the %s lifecycle contract declared no events, so this test would check nothing for it", harness)
		total += count
	}
	require.Len(t, pinnedLifecycleProfileRows, total,
		"the committed table has %d rows but the three contracts declare %d events (%v); a row added to or removed from a profile must be added to or removed from the table in the same change",
		len(pinnedLifecycleProfileRows), total, perHarness)

	expected := make(map[string]lifecycleProfileRow, len(pinnedLifecycleProfileRows))
	for _, row := range pinnedLifecycleProfileRows {
		key := row.Harness + "/" + row.NativeName
		_, duplicate := expected[key]
		require.False(t, duplicate, "the committed table lists %s twice", key)
		expected[key] = row
	}
	for key, want := range expected {
		got, found := observed[key]
		require.True(t, found, "the committed table has a row for %s but no pinned lifecycle profile declares that event", key)
		assert.Equal(t, want, got, "the pinned lifecycle row for %s differs from the committed table", key)
	}
	for key := range observed {
		_, found := expected[key]
		assert.True(t, found, "the pinned lifecycle profile declares %s but the committed table has no row for it", key)
	}
}

// lifecycleProfileFileMarkers is the closed set of lifecycle profile source
// files and the harness each one may declare. The shared helper file has the
// empty marker: it may declare nothing that names a harness.
var lifecycleProfileFileMarkers = map[string]string{
	"lifecycle_profiles.go":          "",
	"lifecycle_profiles_claude.go":   "claude",
	"lifecycle_profiles_codex.go":    "codex",
	"lifecycle_profiles_opencode.go": "opencode",
}

// lifecycleProfileDeclarationNames returns the name of every top-level
// declaration in one Go source file. A method contributes the name of its
// receiver type, because the type is what places it.
func lifecycleProfileDeclarationNames(t *testing.T, path string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	names := []string{}
	for _, declaration := range file.Decls {
		switch decl := declaration.(type) {
		case *ast.FuncDecl:
			if decl.Recv == nil || len(decl.Recv.List) == 0 {
				names = append(names, decl.Name.Name)
				continue
			}
			receiver := decl.Recv.List[0].Type
			if star, ok := receiver.(*ast.StarExpr); ok {
				receiver = star.X
			}
			if ident, ok := receiver.(*ast.Ident); ok {
				names = append(names, ident.Name)
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					names = append(names, spec.Name.Name)
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						names = append(names, name.Name)
					}
				}
			}
		}
	}
	return names
}

// TestEveryHarnessRowLivesInItsOwnProfileFile reads the declarations of every
// lifecycle_profiles*.go file and refuses a harness-named declaration in the
// shared helper file, a declaration in a harness file that does not name its
// own harness, and a declaration that names another harness.
//
// The rule is what lets three people edit three harnesses at once without
// touching one another's file: every row of one harness lives in exactly one
// file, and the shared file holds only helpers every harness uses. The file
// set is read from the package directory and compared with the closed marker
// table, so a fifth profile file, or a missing one, is refused rather than
// skipped; every file must declare at least one name, so an empty file cannot
// pass as clean.
func TestEveryHarnessRowLivesInItsOwnProfileFile(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	found := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "lifecycle_profiles") && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			found = append(found, name)
		}
	}
	expected := make([]string, 0, len(lifecycleProfileFileMarkers))
	for name := range lifecycleProfileFileMarkers {
		expected = append(expected, name)
	}
	require.ElementsMatch(t, expected, found,
		"the lifecycle profile files on disk are not the closed set this guard reads; a new profile file needs a row in lifecycleProfileFileMarkers")

	for _, path := range found {
		owned := lifecycleProfileFileMarkers[path]
		names := lifecycleProfileDeclarationNames(t, path)
		require.NotEmpty(t, names, "%s declares nothing, so nothing here can be checked", path)
		for _, name := range names {
			lowered := strings.ToLower(name)
			for _, marker := range lifecycleProfileFileMarkers {
				if marker == "" {
					continue
				}
				names := strings.Contains(lowered, marker)
				switch {
				case owned == "" && names:
					assert.Fail(t, "harness row in the shared helper file",
						"%s declares %s, which names harness %s; the shared helper file may hold no harness row, so move it to lifecycle_profiles_%s.go", path, name, marker, marker)
				case owned != "" && marker == owned && !names:
					assert.Fail(t, "declaration does not name its harness",
						"%s declares %s, which does not name harness %s; every declaration in a harness file is a row or helper of that harness and carries its name, or it belongs in lifecycle_profiles.go", path, name, owned)
				case owned != "" && marker != owned && names:
					assert.Fail(t, "declaration names another harness",
						"%s declares %s, which names harness %s; a row of that harness belongs in lifecycle_profiles_%s.go", path, name, marker, marker)
				}
			}
		}
	}
}
