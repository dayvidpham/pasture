package claude_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/frontend/claude"
	claudeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/testutil"
)

// sessionID is the session_id the committed SessionStart fixture carries, read
// from the corpus so the frontend proof follows the capture.
var sessionID = func() string {
	raw, err := os.ReadFile("../../ingress/claude/testdata/fixtures/session_start_2_1_261.json")
	if err != nil {
		panic(err)
	}
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.SessionID == "" {
		panic("the committed SessionStart fixture carries no session_id")
	}
	return payload.SessionID
}()

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

	raw, err := os.ReadFile("../../ingress/claude/testdata/fixtures/session_start_2_1_261.json")
	require.NoError(t, err)
	capture := claudeingress.Parse(raw, registration.ClaudeCode2_1_261().Events[0], registration.ClaudeCode2_1_261().Version, model.OccurrenceEnvelopeRef{})
	require.Equal(t, model.CaptureValid, capture.Disposition)
	require.Equal(t, registration.ClaudeCode2_1_261().Contract, capture.Delivery.Contract)
	require.NotEqual(t, capture.Delivery.Contract, runtime.ClaudeCode2_1_261Lifecycle().ID(), "capture-schema and semantic runtime contracts are distinct layers")

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
		name         string
		kind         model.ContractEventKind
		bindings     []model.NativeBinding
		whatFragment string
		fixFragment  string
	}{
		{
			name:         "unknown ordinal",
			kind:         registration.EventOpenCodeCommandExecuted, // declared by another harness, never by Claude
			bindings:     nil,
			whatFragment: fmt.Sprintf("ordinal %d", registration.EventOpenCodeCommandExecuted),
			fixFragment:  "generated Claude event ordinals",
		},
		{
			name: "unknown native name",
			kind: registration.EventSessionStart,
			bindings: []model.NativeBinding{{
				Kind: model.BindingSession, NativeName: "unknown_id", Value: "value",
			}},
			whatFragment: "unknown_id",
			fixFragment:  "exact NativeName",
		},
		{
			name: "duplicate native name",
			kind: registration.EventSessionStart,
			bindings: []model.NativeBinding{
				{Kind: model.BindingSession, NativeName: "session_id", Value: "one"},
				{Kind: model.BindingSession, NativeName: "session_id", Value: "two"},
			},
			whatFragment: "repeats native field \"session_id\"",
			fixFragment:  "one value for each native identity field",
		},
		{
			name: "numeric kind mismatch",
			kind: registration.EventSessionStart,
			bindings: []model.NativeBinding{{
				Kind: model.BindingRequest, NativeName: "session_id", Value: "value",
			}},
			whatFragment: "classifies native field \"session_id\" as kind 3",
			fixFragment:  "numeric value matches",
		},
		{
			name: "invalid value",
			kind: registration.EventSessionStart,
			bindings: []model.NativeBinding{{
				Kind: model.BindingSession, NativeName: "session_id", Value: "",
			}},
			whatFragment: "native field \"session_id\" has an invalid value",
			fixFragment:  "non-empty UTF-8",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			l1, identities, err := claude.Bind(test.kind, test.bindings)
			requireStructuredValidation(t, err, test.whatFragment, test.fixFragment)
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
	requireStructuredValidation(t, err, "task_id", "exact NativeName")
	require.False(t, l1.IsValid())
	require.Empty(t, identities)
}

func requireStructuredValidation(t *testing.T, err error, whatFragment, fixFragment string) {
	t.Helper()
	require.Error(t, err)
	var structured *pasterrors.StructuredError
	require.True(t, errors.As(err, &structured))
	require.Equal(t, pasterrors.CategoryValidation, structured.Category)
	require.Contains(t, structured.What, whatFragment)
	require.NotEmpty(t, structured.Why)
	require.NotEmpty(t, structured.Where)
	require.NotEmpty(t, structured.Impact)
	require.NotEmpty(t, structured.Fix)
	require.Contains(t, structured.Fix, fixFragment)
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

// TestEventMappingsCoverEveryRegisteredClaudeEvent holds the Claude frontend
// mapping total over the generated registration and each pair correct by
// native name.
func TestEventMappingsCoverEveryRegisteredClaudeEvent(t *testing.T) {
	t.Parallel()
	testutil.AssertEventMappingsCoverRegistration(t, registration.ClaudeCode2_1_261(), runtime.ClaudeCode2_1_261Lifecycle(), claude.Bind)
}
