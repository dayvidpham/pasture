package ir_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportedUserDecisionSelectManyGolden(t *testing.T) {
	t.Parallel()

	prompt := ir.SelectManyPrompt{
		Stimulus: ir.PromptStimulus{
			Question: "Choose ✓", DefinitionShown: "δ", CommandShown: "pasture task",
		},
		Options: []ir.DecisionOption{
			{ID: "z", Label: "Zulu", Description: "last shown first"},
			{ID: "a", Label: "Alpha", Description: "first sorted second"},
		},
		MinSelections: 1,
		MaxSelections: 2,
	}
	scope, err := ir.NewRootScope("golden")
	require.NoError(t, err)
	request, err := ir.NewRequestUserDecision(
		"request-α", ir.HarnessCodex, mustContract(t, ir.HarnessCodex, "0.144.1"),
		mustTaskRef(t, "epoch-1"), mustTaskRef(t, "gate-1"), "implementation-uat", prompt,
		scope,
	)
	require.NoError(t, err)
	assert.Equal(t, scope, request.Scope())
	assert.Empty(t, request.Results())

	selected := []ir.OptionID{"z", "a"}
	report := ir.ReportedUserDecision{
		Schema: ir.ReportedUserDecisionSchema, RequestID: request.RequestID,
		Harness: request.Harness, RuntimeContract: request.RuntimeContract,
		Epoch: request.Epoch, GateTask: request.GateTask, Purpose: request.Purpose,
		Prompt: request.Prompt,
		Result: ir.SelectManyResult{Selected: selected, VerbatimAnswer: "z then a — exactly"},
	}
	input, err := json.Marshal(report)
	require.NoError(t, err)

	decoded, canonical, err := request.DecodeReportedResult(bytes.NewReader(input), int64(len(input)))
	require.NoError(t, err)
	selected[0] = "mutated-after-decode"

	result, ok := decoded.Result.(ir.SelectManyResult)
	require.True(t, ok)
	assert.Equal(t, []ir.OptionID{"a", "z"}, result.Selected)
	assert.Equal(t, "z then a — exactly", result.VerbatimAnswer)

	golden, err := os.ReadFile("testdata/reported_select_many.golden.json")
	require.NoError(t, err)
	assert.True(t, canonical.IsValid())
	assert.Equal(t, strings.TrimSpace(string(golden)), string(canonical.Bytes()))
	assert.Equal(t, goldenWithoutFinalNewline(golden), canonical.Bytes())

	canonicalCopy := canonical.Bytes()
	canonicalCopy[0] = '!'
	assert.Equal(t, byte('{'), canonical.Bytes()[0], "Bytes must return a defensive copy of canonical evidence")
}

func goldenWithoutFinalNewline(value []byte) []byte {
	return bytes.TrimSuffix(value, []byte("\n"))
}

func TestReportedUserDecisionClosedModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prompt ir.UserDecisionPrompt
		result ir.UserDecisionResult
	}{
		{
			name: "select one",
			prompt: ir.SelectOnePrompt{
				Stimulus: ir.PromptStimulus{Question: "Pick one"},
				Options:  []ir.DecisionOption{{ID: "yes", Label: "Yes"}, {ID: "no", Label: "No"}},
			},
			result: ir.SelectOneResult{Selected: "yes", VerbatimAnswer: "yes please"},
		},
		{
			name:   "free text",
			prompt: ir.FreeTextPrompt{Stimulus: ir.PromptStimulus{Question: "Explain"}},
			result: ir.FreeTextResult{Text: "Unicode answer: 水"},
		},
		{
			name: "explicit zero minimum",
			prompt: ir.SelectManyPrompt{
				Stimulus:      ir.PromptStimulus{Question: "Optional choices"},
				Options:       []ir.DecisionOption{{ID: "one", Label: "One"}},
				MinSelections: 0, MaxSelections: 1,
			},
			result: ir.SelectManyResult{Selected: []ir.OptionID{}, VerbatimAnswer: "none"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := decisionRequest(t, test.prompt)
			report := reportFor(request, test.result)
			encoded, err := json.Marshal(report)
			require.NoError(t, err)
			decoded, canonical, err := request.DecodeReportedResult(bytes.NewReader(encoded), 64<<10)
			require.NoError(t, err)
			assert.NotNil(t, decoded.Result)
			assert.True(t, canonical.IsValid())
			assert.True(t, json.Valid(canonical.Bytes()))
		})
	}
}

func TestReportedUserDecisionRejectsMalformedOrMismatchedEvidence(t *testing.T) {
	t.Parallel()

	prompt := ir.SelectManyPrompt{
		Stimulus:      ir.PromptStimulus{Question: "Pick one or two"},
		Options:       []ir.DecisionOption{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
		MinSelections: 1, MaxSelections: 2,
	}
	request := decisionRequest(t, prompt)
	valid, err := json.Marshal(reportFor(request, ir.SelectManyResult{Selected: []ir.OptionID{"a"}, VerbatimAnswer: "A"}))
	require.NoError(t, err)

	mutate := func(path func(map[string]any)) []byte {
		var value map[string]any
		require.NoError(t, json.Unmarshal(valid, &value))
		path(value)
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		return encoded
	}
	tests := map[string][]byte{
		"unknown top-level field": mutate(func(value map[string]any) { value["actor_id"] = "forbidden" }),
		"wrong request":           mutate(func(value map[string]any) { value["request_id"] = "other" }),
		"reordered prompt options": mutate(func(value map[string]any) {
			data := value["prompt"].(map[string]any)["data"].(map[string]any)
			options := data["options"].([]any)
			data["options"] = []any{options[1], options[0]}
		}),
		"duplicate selected": mutate(func(value map[string]any) {
			value["result"].(map[string]any)["data"].(map[string]any)["selected"] = []any{"a", "a"}
		}),
		"unknown selected": mutate(func(value map[string]any) {
			value["result"].(map[string]any)["data"].(map[string]any)["selected"] = []any{"unknown"}
		}),
		"below minimum": mutate(func(value map[string]any) {
			value["result"].(map[string]any)["data"].(map[string]any)["selected"] = []any{}
		}),
		"wrong result mode": mutate(func(value map[string]any) {
			value["result"] = map[string]any{"mode": "free_text", "data": map[string]any{"text": "A"}}
		}),
		"contradictory result field": mutate(func(value map[string]any) {
			value["result"].(map[string]any)["data"].(map[string]any)["text"] = "also text"
		}),
		"trailing JSON": append(append([]byte(nil), valid...), []byte(` {}`)...),
		"invalid UTF-8": append(append([]byte(nil), valid...), 0xff),
	}
	for name, input := range tests {
		name, input := name, input
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, canonical, err := request.DecodeReportedResult(bytes.NewReader(input), 64<<10)
			require.Error(t, err)
			assert.False(t, canonical.IsValid())
			assert.Zero(t, canonical)
			for _, field := range []string{"what:", "why:", "where:", "phase:", "impact:", "fix:"} {
				assert.Contains(t, err.Error(), field)
			}
		})
	}

	_, canonical, err := request.DecodeReportedResult(bytes.NewReader(valid), int64(len(valid)-1))
	require.Error(t, err)
	assert.False(t, canonical.IsValid())
	assert.Zero(t, canonical)
}

func decisionRequest(t testing.TB, prompt ir.UserDecisionPrompt) ir.RequestUserDecision {
	t.Helper()
	scope, err := ir.NewRootScope("decision")
	require.NoError(t, err)
	request, err := ir.NewRequestUserDecision(
		"request-1", ir.HarnessClaudeCode, mustContract(t, ir.HarnessClaudeCode, "2.1.210"),
		mustTaskRef(t, "epoch-1"), mustTaskRef(t, "gate-1"), "test-purpose", prompt,
		scope,
	)
	require.NoError(t, err)
	return request
}

func reportFor(request ir.RequestUserDecision, result ir.UserDecisionResult) ir.ReportedUserDecision {
	return ir.ReportedUserDecision{
		Schema: ir.ReportedUserDecisionSchema, RequestID: request.RequestID,
		Harness: request.Harness, RuntimeContract: request.RuntimeContract,
		Epoch: request.Epoch, GateTask: request.GateTask, Purpose: request.Purpose,
		Prompt: request.Prompt, Result: result,
	}
}

func FuzzDecodeReportedUserDecision(f *testing.F) {
	request := decisionRequest(f, ir.FreeTextPrompt{Stimulus: ir.PromptStimulus{Question: "Explain"}})
	valid, err := json.Marshal(reportFor(request, ir.FreeTextResult{Text: "answer"}))
	require.NoError(f, err)
	f.Add(valid)
	f.Add([]byte(`{}`))
	f.Add([]byte{0xff})

	f.Fuzz(func(t *testing.T, input []byte) {
		decoded, canonical, err := request.DecodeReportedResult(bytes.NewReader(input), 4096)
		if err != nil {
			assert.False(t, canonical.IsValid())
			return
		}
		assert.Equal(t, ir.ReportedUserDecisionSchema, decoded.Schema)
		assert.True(t, canonical.IsValid())
		assert.True(t, json.Valid(canonical.Bytes()))
		decodedAgain, canonicalAgain, err := request.DecodeReportedResult(bytes.NewReader(canonical.Bytes()), 4096)
		require.NoError(t, err)
		assert.Equal(t, decoded.RequestID, decodedAgain.RequestID)
		assert.Equal(t, canonical.Bytes(), canonicalAgain.Bytes())
	})
}
