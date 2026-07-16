package ir_test

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReportedUserDecisionOmissionMatrix is the exhaustive omission matrix
// required by the presence-aware decoding fix: for every mode, every
// report/envelope/data/stimulus field must be explicitly present. Deleting
// any one of them — even a field whose Go zero value (e.g. min_selections:
// 0, or an empty verbatim_answer) would otherwise look like a legitimately
// supplied value — must be rejected, not silently defaulted.
func TestReportedUserDecisionOmissionMatrix(t *testing.T) {
	t.Parallel()

	type fixture struct {
		name   string
		prompt ir.UserDecisionPrompt
		result ir.UserDecisionResult
		paths  [][]string
	}

	commonReportPaths := [][]string{
		{"schema"}, {"request_id"}, {"harness"}, {"runtime_contract"},
		{"epoch"}, {"gate_task"}, {"purpose"}, {"prompt"}, {"result"},
	}
	commonPromptEnvelopePaths := [][]string{{"prompt", "mode"}, {"prompt", "data"}}
	commonResultEnvelopePaths := [][]string{{"result", "mode"}, {"result", "data"}}
	stimulusPaths := func(root string) [][]string {
		return [][]string{
			{root, "data", "stimulus", "question"},
			{root, "data", "stimulus", "definition_shown"},
			{root, "data", "stimulus", "command_shown"},
		}
	}
	// optionPaths indexes into the first entry of the prompt's options
	// array, proving DecisionOption's presence-awareness (id/label/
	// description) is enforced the same way stimulus fields are — this is
	// what teaching omitPath to index into an array (not just walk maps)
	// makes possible.
	optionPaths := func() [][]string {
		return [][]string{
			{"prompt", "data", "options", "0", "id"},
			{"prompt", "data", "options", "0", "label"},
			{"prompt", "data", "options", "0", "description"},
		}
	}

	fixtures := []fixture{
		{
			name: "select_one",
			prompt: ir.SelectOnePrompt{
				Stimulus: ir.PromptStimulus{Question: "Pick one", DefinitionShown: "def", CommandShown: "cmd"},
				Options:  []ir.DecisionOption{{ID: "yes", Label: "Yes"}, {ID: "no", Label: "No"}},
			},
			result: ir.SelectOneResult{Selected: "yes", VerbatimAnswer: "yes please"},
			paths: append(append(append(append([][]string{}, commonReportPaths...), commonPromptEnvelopePaths...), commonResultEnvelopePaths...),
				append(append(stimulusPaths("prompt"), optionPaths()...),
					[]string{"prompt", "data", "options"},
					[]string{"result", "data", "selected"},
					[]string{"result", "data", "verbatim_answer"},
				)...,
			),
		},
		{
			name: "select_many",
			prompt: ir.SelectManyPrompt{
				Stimulus:      ir.PromptStimulus{Question: "Pick some", DefinitionShown: "def", CommandShown: "cmd"},
				Options:       []ir.DecisionOption{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
				MinSelections: 1, MaxSelections: 2,
			},
			result: ir.SelectManyResult{Selected: []ir.OptionID{"a"}, VerbatimAnswer: "just a"},
			paths: append(append(append(append([][]string{}, commonReportPaths...), commonPromptEnvelopePaths...), commonResultEnvelopePaths...),
				append(append(stimulusPaths("prompt"), optionPaths()...),
					[]string{"prompt", "data", "options"},
					[]string{"prompt", "data", "min_selections"},
					[]string{"prompt", "data", "max_selections"},
					[]string{"result", "data", "selected"},
					[]string{"result", "data", "verbatim_answer"},
				)...,
			),
		},
		{
			name:   "free_text",
			prompt: ir.FreeTextPrompt{Stimulus: ir.PromptStimulus{Question: "Explain", DefinitionShown: "def", CommandShown: "cmd"}},
			result: ir.FreeTextResult{Text: "an answer"},
			paths: append(append(append(append([][]string{}, commonReportPaths...), commonPromptEnvelopePaths...), commonResultEnvelopePaths...),
				append(stimulusPaths("prompt"),
					[]string{"result", "data", "text"},
				)...,
			),
		},
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			request := decisionRequest(t, fixture.prompt)
			report := reportFor(request, fixture.result)
			valid, err := json.Marshal(report)
			require.NoError(t, err)

			for _, path := range fixture.paths {
				path := path
				t.Run(pathName(path), func(t *testing.T) {
					t.Parallel()
					var value map[string]any
					require.NoError(t, json.Unmarshal(valid, &value))
					require.True(t, omitPath(value, path), "test setup: path %v not found in valid fixture", path)
					mutated, err := json.Marshal(value)
					require.NoError(t, err)

					_, canonical, decodeErr := request.DecodeReportedResult(bytes.NewReader(mutated), 64<<10)
					require.Error(t, decodeErr, "omitting %v must be rejected, not defaulted to a zero value", path)
					assert.False(t, canonical.IsValid())
					for _, field := range []string{"what:", "why:", "where:", "phase:", "impact:", "fix:"} {
						assert.Contains(t, decodeErr.Error(), field)
					}
				})
			}
		})
	}
}

// omitPath deletes the key named by the last element of path from the
// container reached by walking the earlier elements, and reports whether it
// found (and deleted) that key. A path segment that parses as a nonnegative
// integer indexes into a JSON array at that position (e.g. "options", "0",
// "id" reaches the first option's "id" member); every other segment is a
// map key.
func omitPath(root map[string]any, path []string) bool {
	var current any = root
	for _, segment := range path[:len(path)-1] {
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[segment]
			if !ok {
				return false
			}
			current = next
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(node) {
				return false
			}
			current = node[index]
		default:
			return false
		}
	}
	container, ok := current.(map[string]any)
	if !ok {
		return false
	}
	last := path[len(path)-1]
	if _, ok := container[last]; !ok {
		return false
	}
	delete(container, last)
	return true
}

func pathName(path []string) string {
	name := path[0]
	for _, segment := range path[1:] {
		name += "/" + segment
	}
	return name
}

// TestReportedUserDecisionRejectsDuplicateJSONMembersAtEveryLevel proves
// duplicate-member rejection at the report, envelope, data, and nested
// stimulus levels — encoding/json's own decoder silently keeps the last
// occurrence of a repeated key, which this package must not accept. Each
// case duplicates one real member's key (with a forged sibling value)
// immediately before its real occurrence, which stays valid JSON syntax
// while making that one member ambiguous.
func TestReportedUserDecisionRejectsDuplicateJSONMembersAtEveryLevel(t *testing.T) {
	t.Parallel()

	request := decisionRequest(t, ir.SelectOnePrompt{
		Stimulus: ir.PromptStimulus{Question: "Pick one", DefinitionShown: "def", CommandShown: "cmd"},
		Options:  []ir.DecisionOption{{ID: "yes", Label: "Yes"}, {ID: "no", Label: "No"}},
	})
	report := reportFor(request, ir.SelectOneResult{Selected: "yes", VerbatimAnswer: "yes please"})
	valid, err := json.Marshal(report)
	require.NoError(t, err)
	original := string(valid)

	tests := map[string]struct{ old, new string }{
		"report level (schema)": {
			old: `{"schema":`, new: `{"schema":"forged","schema":`,
		},
		"prompt envelope level (mode)": {
			old: `"prompt":{"mode":`, new: `"prompt":{"mode":"forged","mode":`,
		},
		"prompt data level, nested inside stimulus (question)": {
			old: `"stimulus":{"question":`, new: `"stimulus":{"question":"forged","question":`,
		},
		"result envelope level (mode)": {
			old: `"result":{"mode":`, new: `"result":{"mode":"forged","mode":`,
		},
		"result data level (selected)": {
			old: `"data":{"selected":`, new: `"data":{"selected":"forged","selected":`,
		},
	}
	for name, replacement := range tests {
		name, replacement := name, replacement
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, 1, strings.Count(original, replacement.old), "test setup: marker must be unique in the fixture")
			mutated := strings.Replace(original, replacement.old, replacement.new, 1)
			require.True(t, json.Valid([]byte(mutated)), "test setup produced invalid JSON:\n%s", mutated)
			require.NotEqual(t, original, mutated)

			_, canonical, err := request.DecodeReportedResult(bytes.NewReader([]byte(mutated)), 64<<10)
			require.Error(t, err, "a duplicate JSON member must be rejected:\n%s", mutated)
			assert.False(t, canonical.IsValid())
			assert.Contains(t, err.Error(), "duplicate member", "a genuine duplicate must be diagnosed as a duplicate member, not a generic malformed-input error")
		})
	}
}
