package opencode_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/frontend/opencode"
	opencodeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/opencode"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/testutil"
)

func TestAuthenticCallbacksProduceProviderCorrectVerifiedL2(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, file string
		kind       model.ContractEventKind
		semantic   runtime.EventSemantic
		identities []runtime.NativeIdentityKind
	}{
		{"session created", "session_created_1_18_29.json", registration.EventOpenCodeSessionCreated, runtime.SemanticObservation, []runtime.NativeIdentityKind{runtime.IdentitySession}},
		{"tool execute before", "tool_execute_before_1_18_29.json", registration.EventOpenCodeToolExecuteBefore, runtime.SemanticGateConsultation, []runtime.NativeIdentityKind{runtime.IdentitySession, runtime.IdentityToolCall}},
	}
	manifest := registration.OpenCode1_18_29()
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile("../../ingress/opencode/testdata/fixtures/" + test.file)
			require.NoError(t, err)
			var event registration.Event
			for _, candidate := range manifest.Events {
				if candidate.Kind == test.kind {
					event = candidate
					break
				}
			}
			capture := opencodeingress.Parse(raw, event, manifest.Version, model.OccurrenceEnvelopeRef{})
			require.Equal(t, model.CaptureValid, capture.Disposition)
			l1, identities, err := opencode.Bind(capture.Delivery.Event, capture.Delivery.Bindings)
			require.NoError(t, err)
			require.True(t, l1.IsValid())
			l2, err := l1.NewEvent(identities)
			require.NoError(t, err)
			require.True(t, l2.IsValid())
			require.Equal(t, test.semantic, l2.Semantics().Semantic())
			require.Equal(t, test.identities, identityKinds(l2.Semantics().Identities()))
			require.Equal(t, runtime.OpenCode1_18_29Lifecycle().ID(), l2.Origin().Contract())
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

// TestEventMappingsCoverEveryRegisteredOpenCodeEvent holds the OpenCode
// frontend mapping total over the generated registration and each pair correct
// by native name. A mapped event is not an enabled one: admission is decided by
// the activation table before any payload is read (internal/handlers).
func TestEventMappingsCoverEveryRegisteredOpenCodeEvent(t *testing.T) {
	t.Parallel()
	testutil.AssertEventMappingsCoverRegistration(t, registration.OpenCode1_18_29(), runtime.OpenCode1_18_29Lifecycle(), opencode.Bind)
}

// TestV2EventMappingsCoverEveryRegisteredEvent holds the OpenCode 2.0.20
// frontend mapping total over the generated registration and each pair correct
// by native name. A mapped event is not an enabled one: admission is decided by
// the activation table before any payload is read (internal/handlers).
func TestV2EventMappingsCoverEveryRegisteredEvent(t *testing.T) {
	t.Parallel()
	testutil.AssertEventMappingsCoverRegistration(t, registration.OpenCode2_0_20(), runtime.OpenCode2_0_20Lifecycle(), opencode.BindV2)
}

// TestBindV2ProducesVerifiedL2 drives constructed 2.0.20 bindings through the
// frontend engine. The payloads are constructed admission shapes, not
// authentic captures; the v2 capture sitting supplies those.
func TestBindV2ProducesVerifiedL2(t *testing.T) {
	t.Parallel()
	tool, toolIdentities, err := opencode.BindV2(registration.EventOpenCode2ToolExecuteBefore, []model.NativeBinding{
		{Kind: model.BindingSession, NativeName: "sessionID", Value: "ses_constructed"},
		{Kind: model.BindingToolCall, NativeName: "id", Value: "call_constructed"},
	})
	require.NoError(t, err)
	require.True(t, tool.IsValid())
	toolEvent, err := tool.NewEvent(toolIdentities)
	require.NoError(t, err)
	require.True(t, toolEvent.IsValid())
	require.Equal(t, runtime.SemanticGateConsultation, toolEvent.Semantics().Semantic())

	created, createdIdentities, err := opencode.BindV2(registration.EventOpenCode2SessionCreated, []model.NativeBinding{
		{Kind: model.BindingSession, NativeName: "sessionID", Value: "ses_constructed"},
	})
	require.NoError(t, err)
	createdEvent, err := created.NewEvent(createdIdentities)
	require.NoError(t, err)
	require.Equal(t, runtime.SemanticObservation, createdEvent.Semantics().Semantic())

	shell, shellIdentities, err := opencode.BindV2(registration.EventOpenCode2ShellCreateBefore, nil)
	require.NoError(t, err)
	shellEvent, err := shell.NewEvent(shellIdentities)
	require.NoError(t, err)
	require.True(t, shellEvent.IsValid())
}
