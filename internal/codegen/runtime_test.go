package codegen

import (
	"strings"
	"testing"
)

func TestClaudeRuntimeProjectionIsByteNeutral(t *testing.T) {
	t.Parallel()

	input := "Call Skill(/pasture:worker), spawn Task({}), and use SendMessage or AskUserQuestion."
	got, err := ClaudeRuntimeDialect.Project(input)
	if err != nil {
		t.Fatalf("Claude runtime projection: %v", err)
	}
	if got != input {
		t.Fatalf("Claude runtime projection changed canonical content:\n got: %q\nwant: %q", got, input)
	}
}

func TestOpenCodeRuntimeProjectionUsesNativeTerms(t *testing.T) {
	t.Parallel()

	input := "Call Skill(/pasture:worker), then Task({subagent_type: \"general-purpose\"}); use SendMessage and AskUserQuestion."
	got, err := OpenCodeRuntimeDialect.Project(input)
	if err != nil {
		t.Fatalf("OpenCode runtime projection: %v", err)
	}
	for _, want := range []string{`skill("worker")`, "task({agent_type", "task_agent_message", "interactive_user_prompt"} {
		if !strings.Contains(got, want) {
			t.Errorf("OpenCode projection %q does not contain %q", got, want)
		}
	}
	for _, foreign := range []string{"Skill(", "Task(", "SendMessage", "AskUserQuestion", "subagent_type"} {
		if strings.Contains(got, foreign) {
			t.Errorf("OpenCode projection %q retained foreign syntax %q", got, foreign)
		}
	}
}

func TestRuntimeDialectUnknownOperationIsActionable(t *testing.T) {
	t.Parallel()

	_, err := OpenCodeRuntimeDialect.Render(RuntimeOperation{ID: RuntimeOperationID("unknown")})
	if err == nil {
		t.Fatal("unknown operation returned nil error")
	}
	for _, want := range []string{"opencode", "unknown", "mapping"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestRuntimeLiteralFilesBypassProjection(t *testing.T) {
	t.Parallel()

	root := testModuleRoot(t)
	out := t.TempDir()
	files, err := EmitHarness(root, out, OpenCodeTarget, "", GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("EmitHarness(%s): %v", HarnessOpenCode, err)
	}
	found := false
	for _, file := range files {
		if !file.RuntimeLiteral || !strings.HasSuffix(file.Path, "protocol/PROCESS.md") {
			continue
		}
		found = true
		if !strings.Contains(file.Content, "Skill(") {
			t.Fatalf("literal protocol documentation was unexpectedly projected: %s", file.Path)
		}
	}
	if !found {
		t.Fatal("did not find the explicitly literal protocol/PROCESS.md output")
	}
}
