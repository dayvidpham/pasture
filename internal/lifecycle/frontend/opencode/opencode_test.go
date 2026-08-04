package opencode_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/frontend/opencode"
	opencodeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/opencode"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

type capturedRecord struct {
	Value json.RawMessage `json:"value"`
}

func TestAuthenticCallbacksProduceProviderCorrectVerifiedL2(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, file string
		kind       model.ContractEventKind
		semantic   runtime.EventSemantic
		identities []runtime.NativeIdentityKind
	}{
		{"session created", "session_created_1_18_10.capture.json", registration.EventOpenCodeSessionCreated, runtime.SemanticObservation, []runtime.NativeIdentityKind{runtime.IdentitySession}},
		{"tool execute before", "tool_execute_before_1_18_10.capture.json", registration.EventOpenCodeToolExecuteBefore, runtime.SemanticGateConsultation, []runtime.NativeIdentityKind{runtime.IdentitySession, runtime.IdentityToolCall}},
	}
	manifest := registration.OpenCode1_18_10()
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile("../../ingress/opencode/testdata/fixtures/" + test.file)
			require.NoError(t, err)
			var record capturedRecord
			require.NoError(t, json.Unmarshal(raw, &record))
			var event registration.Event
			for _, candidate := range manifest.Events {
				if candidate.Kind == test.kind {
					event = candidate
					break
				}
			}
			capture := opencodeingress.Parse(record.Value, event, "1.18.10", model.OccurrenceEnvelopeRef{})
			require.Equal(t, model.CaptureValid, capture.Disposition)
			l1, identities, err := opencode.Bind(capture.Delivery.Event, capture.Delivery.Bindings)
			require.NoError(t, err)
			require.True(t, l1.IsValid())
			l2, err := l1.NewEvent(identities)
			require.NoError(t, err)
			require.True(t, l2.IsValid())
			require.Equal(t, test.semantic, l2.Semantics().Semantic())
			require.Equal(t, test.identities, identityKinds(l2.Semantics().Identities()))
			require.Equal(t, runtime.OpenCode1_18_10Lifecycle().ID(), l2.Origin().Contract())
		})
	}
}

func identityKinds(values []waist.SemanticIdentity) []runtime.NativeIdentityKind {
	out := make([]runtime.NativeIdentityKind, len(values))
	for i := range values {
		out[i] = values[i].Kind
	}
	return out
}
