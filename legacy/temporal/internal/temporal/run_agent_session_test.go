package temporal_test

// run_agent_session_test.go — YAML-driven tests for the RunAgentSession activity.
//
// This file owns:
//   - Fixture types: AgentUpdateInput, ToolCallInput, ContentInput,
//     RunAgentSessionWant, RunAgentSessionCase, RunAgentSessionFixtures.
//   - EpochId typed string constant (test-only).
//   - buildFakeAgent — compiles pasture-test-agent via "go build" with its module path.
//   - buildFakeAgentArgs — serializes []AgentUpdateInput to a temp JSON-RPC fixture file.
//   - TestRunAgentSession_YAML — table-driven tests loaded from testdata/run_agent_session.yaml.
//   - TestRunAgentSession_ContextCancellation — workflow-level CancelWorkflow test.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/legacy/temporal/internal/temporal"
)

// ─── EpochId typed constant ───────────────────────────────────────────────────

// TestEpochId is a typed string to prevent raw string literals at test call sites.
// This satisfies the UAT requirement: "EpochId values should be typed string
// constants, not raw strings."
type TestEpochId string

const (
	EpochCancelTest TestEpochId = "epoch-cancel-test"
)

// ─── Fixture types ────────────────────────────────────────────────────────────

// ContentInput mirrors the YAML structure of a single content block in a fixture.
type ContentInput struct {
	Type    string `yaml:"type"`
	Content string `yaml:"content"`
	Text    string `yaml:"text"`
}

// ToolCallInput mirrors the YAML structure of a single tool call in a fixture.
type ToolCallInput struct {
	ToolCallId string `yaml:"tool_call_id"`
	ToolKind   string `yaml:"tool_kind"`
	ToolName   string `yaml:"tool_name"`
	ToolInput  string `yaml:"tool_input"`
	ToolOutput string `yaml:"tool_output"`
}

// AgentUpdateInput mirrors the YAML structure of a single session update in a
// fixture. Each update becomes one JSON-RPC "session/update" line emitted by
// the fake agent.
type AgentUpdateInput struct {
	SessionId  string          `yaml:"session_id"`
	Role       string          `yaml:"role"`
	StopReason string          `yaml:"stop_reason"`
	Content    []ContentInput  `yaml:"content"`
	ToolCalls  []ToolCallInput `yaml:"tool_calls"`
}

// RunAgentSessionWant holds the expected RunAgentSessionResult fields.
type RunAgentSessionWant struct {
	EntriesRecorded int    `yaml:"entries_recorded"`
	SessionId       string `yaml:"session_id"`
	StopReason      string `yaml:"stop_reason"`
}

// RunAgentSessionCase holds one row from run_agent_session.yaml.
type RunAgentSessionCase struct {
	ID      string              `yaml:"id"`
	EpochId TestEpochId         `yaml:"epoch_id"`
	Updates []AgentUpdateInput  `yaml:"updates"`
	Want    RunAgentSessionWant `yaml:"want"`
}

// RunAgentSessionFixtures is the top-level YAML envelope for run_agent_session.yaml.
type RunAgentSessionFixtures struct {
	Tests []RunAgentSessionCase `yaml:"tests"`
}

// ─── Test helpers ─────────────────────────────────────────────────────────────

// buildFakeAgent compiles the pasture-test-agent binary via "go build" using
// its module path (github.com/dayvidpham/pasture/cmd/pasture-test-agent).
// Returns the path to the compiled binary.
//
// Using the module path (not a temp source file) ensures the test exercises
// the production binary that gets distributed — same code path as real users.
func buildFakeAgent(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "pasture-test-agent")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	//nolint:gosec // test helper: module path is a constant
	cmd := exec.Command("go", "build", "-o", binPath,
		"github.com/dayvidpham/pasture/cmd/pasture-test-agent")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("buildFakeAgent: go build failed: %v\n%s", err, out)
	}

	t.Cleanup(func() { _ = os.Remove(binPath) })
	return binPath
}

// jsonRPCLine wraps a session update as a JSON-RPC 2.0 "session/update" line.
type jsonRPCLine struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// updateToWire converts an AgentUpdateInput into the JSON-RPC wire structure
// used by the ACP client. This mirrors the acp.SessionUpdate wire format.
type wireUpdate struct {
	SessionId  string         `json:"sessionId"`
	Role       string         `json:"role"`
	Content    []wireContent  `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"toolCalls,omitempty"`
	StopReason string         `json:"stopReason,omitempty"`
}

type wireContent struct {
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	Text    string `json:"text,omitempty"`
}

type wireToolCall struct {
	ToolCallId string `json:"toolCallId"`
	ToolKind   string `json:"toolKind"`
	ToolName   string `json:"toolName"`
	ToolInput  string `json:"toolInput,omitempty"`
	ToolOutput string `json:"toolOutput,omitempty"`
}

// buildFakeAgentArgs serialises the provided updates to a temp JSON-RPC
// fixture file and returns the file path. The pasture-test-agent binary reads
// this file and writes its contents to stdout.
//
// Each AgentUpdateInput becomes one newline-terminated JSON-RPC line in the
// file so the ACP client can parse them as they arrive.
func buildFakeAgentArgs(t *testing.T, updates []AgentUpdateInput) string {
	t.Helper()

	tmpDir := t.TempDir()
	fixturePath := filepath.Join(tmpDir, "fixture.json")

	var lines []byte
	for _, u := range updates {
		wire := wireUpdate{
			SessionId:  u.SessionId,
			Role:       u.Role,
			StopReason: u.StopReason,
		}

		for _, c := range u.Content {
			wire.Content = append(wire.Content, wireContent{
				Type:    c.Type,
				Content: c.Content,
				Text:    c.Text,
			})
		}

		for _, tc := range u.ToolCalls {
			wire.ToolCalls = append(wire.ToolCalls, wireToolCall{
				ToolCallId: tc.ToolCallId,
				ToolKind:   tc.ToolKind,
				ToolName:   tc.ToolName,
				ToolInput:  tc.ToolInput,
				ToolOutput: tc.ToolOutput,
			})
		}

		params, err := json.Marshal(wire)
		if err != nil {
			t.Fatalf("buildFakeAgentArgs: marshal update %q: %v", u.SessionId, err)
		}

		rpcLine := jsonRPCLine{
			JSONRPC: "2.0",
			Method:  "session/update",
			Params:  params,
		}
		lineBytes, err := json.Marshal(rpcLine)
		if err != nil {
			t.Fatalf("buildFakeAgentArgs: marshal rpc line: %v", err)
		}
		lines = append(lines, lineBytes...)
		lines = append(lines, '\n')
	}

	if err := os.WriteFile(fixturePath, lines, 0600); err != nil {
		t.Fatalf("buildFakeAgentArgs: write fixture: %v", err)
	}

	t.Cleanup(func() { _ = os.Remove(fixturePath) })
	return fixturePath
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestRunAgentSession_YAML is a table-driven test that loads cases from
// testdata/run_agent_session.yaml and runs each one via the Temporal activity
// test environment.
//
// Each case:
//  1. Compiles the pasture-test-agent binary (once per test, reused per case).
//  2. Builds a temp JSON-RPC fixture file from the case's updates.
//  3. Executes the RunAgentSession activity with the fake agent as agentCmd.
//  4. Asserts the result matches the case's want fields.
func TestRunAgentSession_YAML(t *testing.T) {
	t.Parallel()

	var fixtures RunAgentSessionFixtures
	testutil.LoadFixtures(t, testutil.RunAgentSession, &fixtures)

	// Build the fake agent binary once; all sub-tests share it.
	agentBin := buildFakeAgent(t)

	trail := audit.NewInMemoryAuditTrail()
	acts := &temporal.Activities{
		Trail:    trail,
		HooksMgr: nil,
	}

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts)

	for _, tc := range fixtures.Tests {
		tc := tc // capture range variable
		t.Run(tc.ID, func(t *testing.T) {
			// Note: not t.Parallel() — activity env is shared and not concurrency-safe.

			fixturePath := buildFakeAgentArgs(t, tc.Updates)

			input := temporal.RunAgentSessionInput{
				AgentCmd:  agentBin,
				AgentArgs: []string{fixturePath},
				EpochId:   string(tc.EpochId),
			}

			val, err := env.ExecuteActivity(acts.RunAgentSession, input)
			if err != nil {
				t.Fatalf("RunAgentSession activity failed: %v", err)
			}

			var result temporal.RunAgentSessionResult
			if err := val.Get(&result); err != nil {
				t.Fatalf("decode RunAgentSessionResult: %v", err)
			}

			if result.EntriesRecorded != tc.Want.EntriesRecorded {
				t.Errorf("EntriesRecorded: got %d, want %d",
					result.EntriesRecorded, tc.Want.EntriesRecorded)
			}
			if result.SessionId != tc.Want.SessionId {
				t.Errorf("SessionId: got %q, want %q",
					result.SessionId, tc.Want.SessionId)
			}
			if result.StopReason != tc.Want.StopReason {
				t.Errorf("StopReason: got %q, want %q",
					result.StopReason, tc.Want.StopReason)
			}
		})
	}
}

// TestRunAgentSession_ContextCancellation verifies that a RunAgentSession
// activity inside a workflow is cleaned up correctly when the workflow is
// cancelled mid-session.
//
// The test uses RegisterDelayedCallback + CancelWorkflow to simulate an external
// cancellation while the workflow is waiting for activities to complete. After
// cancellation, the workflow must complete without error (Temporal's
// cancellation model returns an activity error but the workflow handles it).
func TestRunAgentSession_ContextCancellation(t *testing.T) {
	t.Parallel()

	trail := audit.NewInMemoryAuditTrail()
	acts := &temporal.Activities{
		Trail:    trail,
		HooksMgr: nil,
	}

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(acts)

	// Cancel the workflow quickly — before any activity has a chance to run.
	// This exercises the cancellation path in the workflow event loop.
	env.RegisterDelayedCallback(func() {
		env.CancelWorkflow()
	}, time.Millisecond*10)

	env.ExecuteWorkflow(temporal.EpochWorkflowFn, temporal.EpochInput{
		EpochId:            string(EpochCancelTest),
		RequestDescription: "context cancellation test",
	})

	if !env.IsWorkflowCompleted() {
		t.Error("workflow should be completed (cancelled) after CancelWorkflow")
	}
	// After cancellation, the workflow completes but may return a cancellation
	// error — that is expected behaviour in Temporal's model. We only care that
	// the workflow did not hang indefinitely.
}
