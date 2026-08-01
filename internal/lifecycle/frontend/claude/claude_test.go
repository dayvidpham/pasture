package claude_test

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/frontend/claude"
	claudeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

const sessionID = "b3cfe877-feb4-4ba3-9500-414c8bfb51c4"

func TestBindSessionStartBuildsProductionWaistEvent(t *testing.T) {
	t.Parallel()

	l1, identities, err := claude.Bind(registration.EventSessionStart, []model.NativeBinding{
		{Kind: model.BindingSession, NativeName: "session_id", Value: sessionID},
	})
	require.NoError(t, err)
	require.True(t, l1.IsValid())
	require.Len(t, identities, 1)
	require.Equal(t, runtime.IdentitySession, identities[0].Kind())
	require.Equal(t, "session_id", identities[0].NativeName())
	require.Equal(t, sessionID, identities[0].Value())

	l2, err := l1.NewEvent(identities)
	require.NoError(t, err)
	require.True(t, l2.IsValid())
	require.Equal(t, runtime.SemanticObservation, l2.Semantics().Semantic())
	require.Equal(t, runtime.IdentitySession, l2.Semantics().Identities()[0].Kind)
	require.Equal(t, sessionID, l2.Semantics().Identities()[0].Value)
}

func TestBindAuthenticParseRetainsNativeNameAndContractLayers(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../ingress/claude/testdata/fixtures/session_start_2_1_210.json")
	require.NoError(t, err)
	capture := claudeingress.Parse(raw, registration.ClaudeCode2_1_210().Events[0], "2.1.220", model.OccurrenceEnvelopeRef{})
	require.Equal(t, model.CaptureValid, capture.Disposition)
	require.Equal(t, registration.ClaudeCode2_1_210().Contract, capture.Delivery.Contract)
	require.NotEqual(t, capture.Delivery.Contract, runtime.ClaudeCode2_1_210Lifecycle().ID(), "capture-schema and semantic runtime contracts are distinct layers")

	l1, identities, err := claude.Bind(capture.Delivery.Event, capture.Delivery.Bindings)
	require.NoError(t, err)
	require.True(t, l1.IsValid())
	require.Len(t, identities, 1)
	require.Equal(t, "session_id", identities[0].NativeName())
	require.Equal(t, sessionID, identities[0].Value())
}

func TestBindRejectsInvalidNativeBindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		kind     model.ContractEventKind
		bindings []model.NativeBinding
	}{
		{
			name:     "unknown ordinal",
			kind:     model.ContractEventKind(31),
			bindings: nil,
		},
		{
			name: "unknown native name",
			kind: registration.EventSessionStart,
			bindings: []model.NativeBinding{{
				Kind: model.BindingSession, NativeName: "unknown_id", Value: "value",
			}},
		},
		{
			name: "duplicate native name",
			kind: registration.EventSessionStart,
			bindings: []model.NativeBinding{
				{Kind: model.BindingSession, NativeName: "session_id", Value: "one"},
				{Kind: model.BindingSession, NativeName: "session_id", Value: "two"},
			},
		},
		{
			name: "numeric kind mismatch",
			kind: registration.EventSessionStart,
			bindings: []model.NativeBinding{{
				Kind: model.BindingRequest, NativeName: "session_id", Value: "value",
			}},
		},
		{
			name: "invalid value",
			kind: registration.EventSessionStart,
			bindings: []model.NativeBinding{{
				Kind: model.BindingSession, NativeName: "session_id", Value: "",
			}},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			l1, identities, err := claude.Bind(test.kind, test.bindings)
			require.Error(t, err)
			require.False(t, l1.IsValid())
			require.Empty(t, identities)
		})
	}
}

func TestBindDoesNotAcceptOccurrenceKindsWithoutRuntimeFields(t *testing.T) {
	t.Parallel()

	l1, identities, err := claude.Bind(registration.EventSessionStart, []model.NativeBinding{{
		Kind: model.BindingTask, NativeName: "task_id", Value: "task-1",
	}})
	require.Error(t, err)
	require.False(t, l1.IsValid())
	require.Empty(t, identities)
}

func TestBindErrorsRemainActionableStructuredValidation(t *testing.T) {
	t.Parallel()

	_, _, err := claude.Bind(registration.EventSessionStart, []model.NativeBinding{{
		Kind: model.BindingRequest, NativeName: "session_id", Value: sessionID,
	}})
	require.Error(t, err)
	var structured *pasterrors.StructuredError
	require.True(t, errors.As(err, &structured))
	require.Contains(t, structured.What, "session_id")
	require.NotEmpty(t, structured.Why)
	require.NotEmpty(t, structured.Where)
	require.NotEmpty(t, structured.Impact)
	require.NotEmpty(t, structured.Fix)
}

func TestBindReturnsWaistIdentityValuesWithoutReinterpretingThem(t *testing.T) {
	t.Parallel()

	bindings := []model.NativeBinding{{
		Kind: model.BindingSession, NativeName: "session_id", Value: sessionID,
	}}
	l1, identities, err := claude.Bind(registration.EventSessionStart, bindings)
	require.NoError(t, err)
	require.Len(t, identities, 1)

	bindings[0].Value = "other"
	bindings[0].NativeName = "other_id"
	require.Equal(t, sessionID, identities[0].Value())
	require.Equal(t, "session_id", identities[0].NativeName())
	require.True(t, l1.IsValid())
}
