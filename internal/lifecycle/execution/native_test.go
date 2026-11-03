package execution_test

import (
	"os"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

func TestNativeOpenCodeCapturedContextAndTitle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file, event, model string
		kind               execution.Kind
	}{
		{"opencode_session_context_2_0_20.1.json", "session.context", "deepseek-v4.1-flash", execution.Primary},
		{"opencode_session_model_request_2_0_20.2.json", "session.model.request", "gpt-6-luna", execution.Title},
	} {
		t.Run(tc.event, func(t *testing.T) {
			body, err := os.ReadFile("../ingress/opencode/testdata/fixtures/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			in, ok := execution.Native(ir.HarnessOpenCode, map[string]model.ContractEventKind{"session.context": registration.EventOpenCode2SessionContext, "session.model.request": registration.EventOpenCode2SessionModelRequest}[tc.event], body)
			if !ok {
				t.Fatal("capture not recognized")
			}
			got, err := execution.Normalize(in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != tc.kind || got.Model.Value != tc.model || got.Provider.Value != "opencode-go" {
				t.Fatalf("unexpected execution metadata: %+v", got)
			}
		})
	}
}

func TestNativeClaudeAndCodexCaptures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		harness, dir, file, event string
		kind                      execution.Kind
		model, role, agent, turn  string
	}{
		{"claude-code", "claude", "session_start_2_1_261.json", "SessionStart", execution.Selection, "claude-fable-5-1", "", "", ""},
		{"claude-code", "claude", "post_model_switch_2_1_261.json", "PostModelSwitch", execution.Selection, "claude-haiku-4-5-20251001", "", "", ""},
		{"claude-code", "claude", "subagent_start_2_1_261.json", "SubagentStart", execution.Child, "", "general-purpose", "a04d0f62f96abb951", ""},
		{"codex", "codex", "session_start_0_153_0.json", "SessionStart", execution.Selection, "gpt-6-astra", "", "", ""},
		{"codex", "codex", "user_prompt_submit_0_153_0.json", "UserPromptSubmit", execution.Selection, "gpt-6-astra", "", "", "01a0736d-6431-7730-9321-53425562ab77"},
		{"codex", "codex", "subagent_start_0_153_0.json", "SubagentStart", execution.Child, "gpt-6-astra", "default", "01a0736f-5370-7d02-9eb9-7a28a3563b32", "01a0736f-5397-7b20-b1a0-ca343c2d5327"},
	} {
		t.Run(tc.harness+"/"+tc.event, func(t *testing.T) {
			body, err := os.ReadFile("../ingress/" + tc.dir + "/testdata/fixtures/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			in, ok := execution.Native(ir.HarnessID(tc.harness), map[string]model.ContractEventKind{"SessionStart": registration.EventSessionStart, "UserPromptSubmit": registration.EventUserPromptSubmit, "PostModelSwitch": registration.EventPostModelSwitch, "SubagentStart": registration.EventSubagentStart}[tc.event], body)
			if !ok {
				t.Fatal("capture not recognized")
			}
			got, err := execution.Normalize(in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != tc.kind || got.Model.Value != tc.model || got.Provider.Value != "" || got.Role.Value != tc.role || got.AgentID != tc.agent || got.TurnID != tc.turn || got.RequestID != "" {
				t.Fatalf("unexpected metadata: %+v", got)
			}
			if tc.event == "PostModelSwitch" && (in.Host.Model.Member != execution.HostToModel || got.Model.Value == "haiku") {
				t.Fatalf("must use to_model: %+v", got.Model)
			}
		})
	}
	if _, ok := execution.Native(ir.HarnessClaudeCode, model.ContractEventKind(65535), []byte(`{}`)); ok {
		t.Fatal("unlisted event accepted")
	}
}

func TestHasEvidence(t *testing.T) {
	t.Parallel()
	m, err := execution.Normalize(execution.Inputs{Kind: execution.Selection})
	if err != nil {
		t.Fatal(err)
	}
	if execution.HasEvidence(m) {
		t.Fatal("empty observation is not evidence")
	}
	m.Model = execution.AttributionValue{Status: execution.Invalid}
	if !execution.HasEvidence(m) {
		t.Fatal("invalid evidence was lost")
	}
	m.Model = execution.AttributionValue{Status: execution.Unavailable, Candidates: []execution.Candidate{{Value: "candidate"}}}
	if !execution.HasEvidence(m) {
		t.Fatal("candidate is evidence")
	}
}
