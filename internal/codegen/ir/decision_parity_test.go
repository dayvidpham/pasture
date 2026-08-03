package ir_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decisionParityFixture is one (decision mode, enabled harness) combination
// exercised by TestDecisionNativeAndParentMediatedRepresentationsAgree.
type decisionParityFixture struct {
	mode    string
	harness ir.HarnessID
	version string
	prompt  ir.UserDecisionPrompt
	result  ir.UserDecisionResult
}

func decisionParityFixtures() []decisionParityFixture {
	selectOnePrompt := ir.SelectOnePrompt{
		Stimulus: ir.PromptStimulus{
			Question: "Approve the plan?", DefinitionShown: "the ratified implementation plan",
			CommandShown: "pasture uat approve",
		},
		Options: []ir.DecisionOption{
			{ID: "approve", Label: "Approve", Description: "accept the plan as written"},
			{ID: "revise", Label: "Revise", Description: "request changes before proceeding"},
		},
	}
	selectOneResult := ir.SelectOneResult{Selected: "approve", VerbatimAnswer: "approve"}

	selectManyPrompt := ir.SelectManyPrompt{
		Stimulus: ir.PromptStimulus{
			Question: "Which items need follow-up?", DefinitionShown: "deferred UAT items",
			CommandShown: "pasture uat defer",
		},
		Options: []ir.DecisionOption{
			{ID: "docs", Label: "Documentation", Description: "update user-facing docs"},
			{ID: "tests", Label: "Tests", Description: "add regression coverage"},
		},
		MinSelections: 0, MaxSelections: 2,
	}
	selectManyResult := ir.SelectManyResult{Selected: []ir.OptionID{"docs"}, VerbatimAnswer: "just docs"}

	freeTextPrompt := ir.FreeTextPrompt{
		Stimulus: ir.PromptStimulus{
			Question: "What changed in this release?", DefinitionShown: "release summary",
			CommandShown: "pasture worker report",
		},
	}
	freeTextResult := ir.FreeTextResult{Text: "Renamed two config keys and fixed a validation bug."}

	harnesses := []struct {
		harness ir.HarnessID
		version string
	}{
		{ir.HarnessClaudeCode, "2.1.210"},
		{ir.HarnessOpenCode, "1.17.18"},
		{ir.HarnessCodex, "0.144.1"},
	}

	var fixtures []decisionParityFixture
	for _, h := range harnesses {
		fixtures = append(fixtures,
			decisionParityFixture{mode: "select_one", harness: h.harness, version: h.version, prompt: selectOnePrompt, result: selectOneResult},
			decisionParityFixture{mode: "select_many", harness: h.harness, version: h.version, prompt: selectManyPrompt, result: selectManyResult},
			decisionParityFixture{mode: "free_text", harness: h.harness, version: h.version, prompt: freeTextPrompt, result: freeTextResult},
		)
	}
	return fixtures
}

// parityView is the checked-in golden shape for one fixture: the exact bytes
// a native-lowering-input consumer, a parent-mediated protocol consumer, and
// a decoded-report consumer each see for the identical logical decision.
type parityView struct {
	Native         json.RawMessage `json:"native"`
	ParentMediated json.RawMessage `json:"parent_mediated"`
	Reported       json.RawMessage `json:"reported"`
}

// TestDecisionNativeAndParentMediatedRepresentationsAgree is the required
// parity test: for every decision mode (select_one, select_many, free_text)
// and every enabled harness contract, a target's native-lowering input
// (CanonicalSemanticOperation) must carry the exact same request payload as
// the harness-neutral parent-mediated protocol form (RequestUserDecision's
// own MarshalJSON) — CanonicalSemanticOperation wraps that same payload in an
// operation envelope (kind/id/scope/results), so a native renderer and a
// parent-mediated consumer can never silently diverge on what request they
// are looking at. Each combination is also golden-checked for regression
// stability.
func TestDecisionNativeAndParentMediatedRepresentationsAgree(t *testing.T) {
	t.Parallel()

	for _, fixture := range decisionParityFixtures() {
		fixture := fixture
		name := fixture.mode + "_" + string(fixture.harness)
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			scope, err := ir.NewRootScope("golden")
			require.NoError(t, err)
			contract, err := ir.NewRuntimeContractID(fixture.harness, fixture.version)
			require.NoError(t, err)
			request, err := ir.NewRequestUserDecision(
				ir.UserDecisionRequestID("request-"+name), fixture.harness, contract,
				mustTaskRef(t, "epoch-1"), mustTaskRef(t, "gate-1"), "impl-uat", fixture.prompt,
				scope,
			)
			require.NoError(t, err)

			parentMediated, err := json.Marshal(request)
			require.NoError(t, err)

			native, err := ir.CanonicalSemanticOperation(request)
			require.NoError(t, err)

			var envelope struct {
				Kind    string          `json:"kind"`
				Payload json.RawMessage `json:"payload"`
			}
			require.NoError(t, json.Unmarshal(native, &envelope))
			assert.Equal(t, string(ir.OperationRequestUserDecision), envelope.Kind)
			assert.JSONEq(t, string(parentMediated), string(envelope.Payload),
				"native lowering input and the parent-mediated protocol form must carry the exact same request payload")

			report := reportFor(request, fixture.result)
			encoded, err := json.Marshal(report)
			require.NoError(t, err)
			_, canonicalReport, err := request.DecodeReportedResult(bytes.NewReader(encoded), 64<<10)
			require.NoError(t, err)
			require.True(t, canonicalReport.IsValid())

			actual := parityView{
				Native:         compactJSON(t, native),
				ParentMediated: compactJSON(t, parentMediated),
				Reported:       compactJSON(t, canonicalReport.Bytes()),
			}
			assertParityGolden(t, name, actual)
		})
	}
}

func compactJSON(t testing.TB, data []byte) json.RawMessage {
	t.Helper()
	var compacted bytes.Buffer
	require.NoError(t, json.Compact(&compacted, data))
	return json.RawMessage(compacted.Bytes())
}

func parityGoldenPath(name string) string {
	return filepath.Join("testdata", "decision_parity", name+".golden.json")
}

// assertParityGolden compares actual against the checked-in golden file. Set
// UPDATE_GOLDEN=1 to (re)write the golden files from actual — used once to
// generate the fixtures in this commit, and available for a deliberate
// future update of the fixture content.
func assertParityGolden(t testing.TB, name string, actual parityView) {
	t.Helper()
	path := parityGoldenPath(name)

	if os.Getenv("UPDATE_GOLDEN") != "" {
		encoded, err := json.MarshalIndent(actual, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, append(encoded, '\n'), 0o644))
	}

	golden, err := os.ReadFile(path)
	require.NoError(t, err, "missing golden file %s (run with UPDATE_GOLDEN=1 to generate it)", path)
	var expected parityView
	require.NoError(t, json.Unmarshal(golden, &expected))

	assert.JSONEq(t, string(expected.Native), string(actual.Native), "native view drifted for %s", name)
	assert.JSONEq(t, string(expected.ParentMediated), string(actual.ParentMediated), "parent-mediated view drifted for %s", name)
	assert.JSONEq(t, string(expected.Reported), string(actual.Reported), "reported view drifted for %s", name)
}
