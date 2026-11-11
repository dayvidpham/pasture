package codegen

import (
	"encoding/json"
	"fmt"
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
// findQuestionRecordShape reports a record registry that cannot be read as a
// dataset: an incomplete or duplicate field, a missing required name, or a shape
// that does not serialise.
func findQuestionRecordShape(fields []QuestionRecordField) []string {
	var findings []string
	if len(fields) == 0 {
		return []string{"the question record declares no fields; why: a dataset cannot be derived from nothing; fix: declare the fields"}
	}
	seen := map[string]struct{}{}
	for _, field := range fields {
		if field.Name == "" || field.Phrase == "" {
			findings = append(findings, fmt.Sprintf("a question record field is incomplete: %+v", field))
			continue
		}
		if _, ok := seen[field.Name]; ok {
			findings = append(findings, fmt.Sprintf("the question record declares %q twice", field.Name))
		}
		seen[field.Name] = struct{}{}
	}
	for _, required := range []string{"header", "question", "options", "multi"} {
		if _, ok := seen[required]; !ok {
			findings = append(findings, fmt.Sprintf("the question record omits %q, so a derived dataset cannot read it", required))
		}
	}
	if _, err := json.Marshal(seen); err != nil {
		findings = append(findings, fmt.Sprintf("the question record shape is not serialisable: %v", err))
	}
	return findings
}

func TestQuestionRecordShapeIsStable(t *testing.T) {
	assert.Empty(t, findQuestionRecordShape(QuestionRecordFields), "the shipped question record must be readable as a dataset")

	defective := []QuestionRecordField{{Name: "header", Phrase: "the header"}}
	if got := findQuestionRecordShape(defective); len(got) == 0 {
		t.Fatal("a record registry missing three required fields and serialisable shape checks was accepted")
	}
}

// TestSignalRecordShapeIsStable proves the signal line's declared field order is
// the order the writer emits, and that every declared field is present.
// findSignalRecordShape reports a signal line the writer emits that does not
// match the declared registry field-for-field.
func findSignalRecordShape(fields []SignalLineField, emitted map[string]any) []string {
	var findings []string
	if len(fields) == 0 {
		return []string{"the signal line declares no fields; why: a dataset cannot count what it cannot name; fix: declare the fields"}
	}
	for _, field := range fields {
		if _, ok := emitted[field.Field]; !ok {
			findings = append(findings, fmt.Sprintf("the signal writer omits the declared field %q, so a derived dataset loses it", field.Field))
		}
	}
	if len(emitted) != len(fields) {
		findings = append(findings, fmt.Sprintf("the writer emits %d fields but the registry declares %d", len(emitted), len(fields)))
	}
	return findings
}

func TestSignalRecordShapeIsStable(t *testing.T) {
	line := signalLine{Kind: "confusion", Phase: "elicit", Skill: "user-elicit", Entry: "e", Quote: "q"}
	encoded, err := json.Marshal(line)
	require.NoError(t, err)
	var emitted map[string]any
	require.NoError(t, json.Unmarshal(encoded, &emitted))
	assert.Empty(t, findSignalRecordShape(SignalLineFields, emitted), "the shipped signal line must match its registry")

	var sparse map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"kind":"confusion"}`), &sparse))
	if got := findSignalRecordShape(SignalLineFields, sparse); len(got) == 0 {
		t.Fatal("a line emitting one of five declared fields was accepted")
	}
}
