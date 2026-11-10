package codegen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findValidationCaseSteps reports a validation-cases fragment that omits one of
// the four steps the requirement names. The steps are the capability, not the
// wording: each is matched by a phrase that only that step uses.
func findValidationCaseSteps(text string) []string {
	steps := []struct {
		name  string
		needs []string
	}{
		{"elicit", []string{"elicit"}},
		{"confirm at UAT", []string{"UAT"}},
		{"evaluate in implementation", []string{"evaluat"}},
		{"failing case becomes a fixture", []string{"fixture"}},
	}
	var findings []string
	lower := strings.ToLower(text)
	for _, step := range steps {
		missing := false
		for _, need := range step.needs {
			if !strings.Contains(lower, strings.ToLower(need)) {
				missing = true
			}
		}
		if missing {
			findings = append(findings, "validation cases: the guidance omits the "+step.name+" step — why: a case that is never confirmed, evaluated or turned into a fixture is not a validation case; where: internal/codegen/specs_data_fragments.go FragValidationCases; fix: state the step, or remove the claim that the others cover it")
		}
	}
	return findings
}

func TestValidationCasesStateEveryStep(t *testing.T) {
	fragment := SharedFragmentSpecs[FragValidationCases]
	require.NotNil(t, fragment)
	require.NotNil(t, fragment.Behavior)
	text := fragment.Behavior.Given + " " + fragment.Behavior.When + " " + fragment.Behavior.Then
	assert.Empty(t, findValidationCaseSteps(text), "the shipped validation-cases guidance must state every step")

	defective := "Elicit validation cases and confirm them in UAT."
	if got := findValidationCaseSteps(defective); len(got) == 0 {
		t.Fatal("guidance without an evaluation step and without a fixture step was accepted")
	}
}

// TestQuestionRecordShapeIsStable proves a question's recorded fields are
// declared once and can be serialised, so a dataset can be derived from what the
// skills write.
func TestQuestionRecordShapeIsStable(t *testing.T) {
	require.NotEmpty(t, QuestionRecordFields, "the question record must declare its fields")
	seen := map[string]struct{}{}
	for _, field := range QuestionRecordFields {
		if field.Name == "" || field.Phrase == "" {
			t.Errorf("a question record field is incomplete: %+v", field)
		}
		if _, ok := seen[field.Name]; ok {
			t.Errorf("the question record declares %q twice", field.Name)
		}
		seen[field.Name] = struct{}{}
	}
	for _, required := range []string{"header", "question", "options", "multi"} {
		if _, ok := seen[required]; !ok {
			t.Errorf("the question record omits %q, so a derived dataset cannot read it", required)
		}
	}
	if _, err := json.Marshal(seen); err != nil {
		t.Fatalf("the question record shape is not serialisable: %v", err)
	}
}

// TestSignalRecordShapeIsStable proves the signal line's declared field order is
// the order the writer emits, and that every declared field is present.
func TestSignalRecordShapeIsStable(t *testing.T) {
	require.NotEmpty(t, SignalLineFields, "the signal line must declare its fields")
	line := signalLine{Kind: "confusion", Phase: "elicit", Skill: "user-elicit", Entry: "e", Quote: "q"}
	encoded, err := json.Marshal(line)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	for _, field := range SignalLineFields {
		if _, ok := decoded[field.Field]; !ok {
			t.Errorf("the signal writer omits the declared field %q, so a derived dataset loses it", field.Field)
		}
	}
	if len(decoded) != len(SignalLineFields) {
		t.Errorf("the writer emits %d fields but the registry declares %d", len(decoded), len(SignalLineFields))
	}
}
