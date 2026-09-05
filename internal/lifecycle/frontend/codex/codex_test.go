package codex_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/frontend/codex"
	codexingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/codex"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/testutil"
)

// Authentic Codex identity facts from the two cleared command-hook payloads.
const codexFixtureDir = "../../ingress/codex/testdata/fixtures"

// The Codex identity values are read from the committed fixtures, so the
// proofs follow the capture instead of restating its ids.
var (
	preToolSessionID = codexFixtureMember("pre_tool_use_0_153_0.json", "session_id")
	preToolTurnID    = codexFixtureMember("pre_tool_use_0_153_0.json", "turn_id")
	preToolUseID     = codexFixtureMember("pre_tool_use_0_153_0.json", "tool_use_id")
	sessionStartID   = codexFixtureMember("session_start_0_153_0.json", "session_id")
)

func codexFixtureMember(file, member string) string {
	raw, err := os.ReadFile(filepath.Join(codexFixtureDir, file))
	if err != nil {
		panic(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		panic(err)
	}
	value, ok := payload[member].(string)
	if !ok || value == "" {
		panic("fixture " + file + " carries no string member " + member)
	}
	return value
}

// TestAuthenticCodexPayloadsProduceProviderCorrectVerifiedL2 drives both cleared
// fixtures through the PRODUCTION ingress and frontend to verified waist L2. It
// asserts Codex-specific semantics and identity facts:
//   - SessionStart is an observation smoke event carrying session_id only. It is
//     NEVER treated as semantically identical to OpenCode session.created.
//   - PreToolUse is a blocking gate carrying session_id, turn_id, and
//     tool_use_id — the full Codex correlation triple.
//
// FAILS until the L3 frontend implementation lands.
func TestAuthenticCodexPayloadsProduceProviderCorrectVerifiedL2(t *testing.T) {
	t.Parallel()
	manifest := registration.Codex0_153_0()
	tests := []struct {
		name, file, nativeName string
		kind                   model.ContractEventKind
		semantic               runtime.EventSemantic
		blocking               runtime.BlockingMode
		identities             []waist.SemanticIdentity
	}{
		{
			name:       "SessionStart observation smoke",
			file:       "session_start_0_153_0.json",
			nativeName: "SessionStart",
			kind:       registration.EventCodexSessionStart,
			semantic:   runtime.SemanticObservation,
			blocking:   runtime.NonBlocking,
			identities: []waist.SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: sessionStartID},
			},
		},
		{
			name:       "PreToolUse gate",
			file:       "pre_tool_use_0_153_0.json",
			nativeName: "PreToolUse",
			kind:       registration.EventCodexPreToolUse,
			semantic:   runtime.SemanticGateConsultation,
			blocking:   runtime.Blocking,
			// Sorted by (Kind, Value): session(1), turn(2), toolCall(4).
			identities: []waist.SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: preToolSessionID},
				{Kind: runtime.IdentityTurn, Value: preToolTurnID},
				{Kind: runtime.IdentityToolCall, Value: preToolUseID},
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(filepath.Join(codexFixtureDir, tc.file))
			require.NoError(t, err)

			event := eventByKind(t, manifest, tc.kind)
			capture := codexingress.Parse(raw, event, manifest.Version, model.OccurrenceEnvelopeRef{})
			require.Equal(t, model.CaptureValid, capture.Disposition)

			l1, identities, err := codex.Bind(capture.Delivery.Event, capture.Delivery.Bindings)
			require.NoError(t, err)
			require.True(t, l1.IsValid())

			l2, err := l1.NewEvent(identities)
			require.NoError(t, err)
			require.True(t, l2.IsValid())

			require.Equal(t, tc.semantic, l2.Semantics().Semantic())
			require.Equal(t, tc.blocking, l2.Semantics().Blocking())
			require.Equal(t, tc.identities, l2.Semantics().Identities())

			require.Equal(t, ir.HarnessCodex, l2.Origin().Harness())
			require.Equal(t, waist.NativeEventName(tc.nativeName), l2.Origin().NativeEventName())
			// IP-1 (resolved at M3-WAVE-1 consolidation): origin must carry the
			// pinned codex@0.153.0 contract owned by M3-SLICE-1.
			require.Equal(t, runtime.Codex0_153_0Lifecycle().ID(), l2.Origin().Contract())
		})
	}
}

// TestEventMappingsCoverEveryRegisteredCodexEvent holds the Codex frontend
// mapping total over the generated registration and each pair correct by native
// name. A mapped event is not an enabled one: admission is decided by the
// activation table before any payload is read (internal/handlers), and the
// built-binary proofs hold that a withheld event is refused there.
func TestEventMappingsCoverEveryRegisteredCodexEvent(t *testing.T) {
	t.Parallel()
	testutil.AssertEventMappingsCoverRegistration(t, registration.Codex0_153_0(), runtime.Codex0_153_0Lifecycle(), codex.Bind)
}

func eventByKind(t *testing.T, manifest registration.Manifest, kind model.ContractEventKind) registration.Event {
	t.Helper()
	for _, event := range manifest.Events {
		if event.Kind == kind {
			return event
		}
	}
	t.Fatalf("generated Codex manifest does not contain event kind %d", kind)
	return registration.Event{}
}
