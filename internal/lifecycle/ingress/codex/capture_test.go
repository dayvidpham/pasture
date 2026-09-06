package codex_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/codex"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

const codexFixtureDir = "testdata/fixtures"

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

// TestProductionIngressPreservesExactBytesAndCodexIdentities drives both cleared
// fixtures through the PRODUCTION codex.Parse and asserts the durable delivery
// carries the exact raw stdin bytes (nothing flattened) plus the provider's
// native correlation identities. FAILS until the L3 implementation lands.
func TestProductionIngressPreservesExactBytesAndCodexIdentities(t *testing.T) {
	t.Parallel()
	manifest := registration.Codex0_153_0()
	tests := []struct {
		name, file string
		event      model.ContractEventKind
		bindings   []model.NativeBinding
	}{
		{
			name:  "SessionStart",
			file:  "session_start_0_153_0.json",
			event: registration.EventCodexSessionStart,
			bindings: []model.NativeBinding{
				{Kind: model.BindingSession, NativeName: "session_id", Value: sessionStartID},
			},
		},
		{
			name:  "PreToolUse",
			file:  "pre_tool_use_0_153_0.json",
			event: registration.EventCodexPreToolUse,
			bindings: []model.NativeBinding{
				{Kind: model.BindingSession, NativeName: "session_id", Value: preToolSessionID},
				{Kind: model.BindingTurn, NativeName: "turn_id", Value: preToolTurnID},
				{Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: preToolUseID},
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(filepath.Join(codexFixtureDir, tc.file))
			require.NoError(t, err)
			sidecar := codexSidecar(t, tc.file)
			require.NoError(t, sidecar.ValidateFixture("testdata", "fixtures/"+tc.file), "the committed bytes are the cleared bytes the sidecar records")
			require.Equal(t, eventByKind(t, manifest, tc.event).NativeName, sidecar.Event)

			event := eventByKind(t, manifest, tc.event)
			capture := codex.Parse(raw, event, manifest.Version, model.OccurrenceEnvelopeRef{})

			require.Equal(t, model.CaptureValid, capture.Disposition, "cleared authentic payload must parse as valid")
			require.Equal(t, digest.FromBytes(raw), capture.Digest, "digest must be taken over the exact stdin bytes")
			require.Equal(t, manifest.Contract, capture.Delivery.Contract)
			require.Equal(t, tc.event, capture.Delivery.Event)
			require.Equal(t, manifest.Version, capture.Delivery.Envelope.HostVersion)
			require.Equal(t, model.CaptureValid, capture.Delivery.Capture)
			// The command-hook stdin bytes ARE the payload; nothing is unwrapped
			// or reformatted, so the durable body is byte-identical to the raw
			// capture (tool name/input, permission mode, cwd all preserved).
			require.Equal(t, raw, capture.Delivery.Body)
			require.Equal(t, tc.bindings, capture.Delivery.Bindings)
		})
	}
}

// TestCodexCatalogIsSourceDerivedAndOnlySelectedEventsAreProven mirrors the
// OpenCode guarantee: the generated Codex manifest is a source-reprofiled closed
// catalog, and ONLY the two authentically observed events (SessionStart,
// PreToolUse) carry native identities. Source-derived catalog entries must never
// imply authentic runtime proof. Passes on the L1 generated surface.
func TestCodexCatalogIsSourceDerivedAndOnlySelectedEventsAreProven(t *testing.T) {
	t.Parallel()
	manifest := registration.Codex0_153_0()
	// Two roots record the host version: the runtime contract id and the host
	// contract the manifest is generated from. They are one version.
	require.Equal(t, runtime.Codex0_153_0().Versions().Min().String(), manifest.Version, "the registration manifest and the runtime contract record one host version")
	source := hostcontract.Codex0_153_0().Events
	require.NotEmpty(t, source, "the Codex host contract declares at least one event")
	require.Len(t, manifest.Events, len(source), "the generated manifest is the host contract's whole catalogue")
	proved := map[string]bool{"SessionStart": true, "PreToolUse": true}
	proofCount := 0
	for _, event := range manifest.Events {
		if proved[event.NativeName] {
			proofCount++
			require.NotEmpty(t, event.Identities, "authentic event %s must declare identities", event.NativeName)
		} else {
			require.Empty(t, event.Identities, "source-derived catalog entry %s must not imply authentic proof", event.NativeName)
		}
	}
	require.Equal(t, 2, proofCount)
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

// codexSidecar reads a fixture's provenance sidecar in the shape every
// committed capture carries.
func codexSidecar(t *testing.T, file string) acceptance.CaptureProvenance {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(codexFixtureDir, strings.TrimSuffix(file, ".json")+".provenance.json"))
	require.NoError(t, err)
	var sidecar acceptance.CaptureProvenance
	require.NoError(t, json.Unmarshal(raw, &sidecar))
	require.Equal(t, registration.Codex0_153_0().Version, sidecar.HarnessVersion, "captured at the recorded host version")
	require.Equal(t, "internal/lifecycle/ingress/codex/testdata/fixtures/CLEARANCE.md", sidecar.Clearance)
	return sidecar
}

// clearedNotEnabledCodexFixtures are the ten cleared Codex captures whose
// events are registered but not yet enabled: the ingress parser has no
// binding arm for them, and the activation target set does not name them. The
// map is the closed set for this state, so the leaf that adds an arm must move
// a row out of it deliberately rather than let a silent expectation drift.
var clearedNotEnabledCodexFixtures = map[string]model.ContractEventKind{
	"user_prompt_submit_0_153_0.json": registration.EventCodexUserPromptSubmit,
	"permission_request_0_153_0.json": registration.EventCodexPermissionRequest,
	"post_tool_use_0_153_0.json":      registration.EventCodexPostToolUse,
	"pre_compact_0_153_0.json":        registration.EventCodexPreCompact,
	"post_compact_0_153_0.json":       registration.EventCodexPostCompact,
	"subagent_start_0_153_0.json":     registration.EventCodexSubagentStart,
	"subagent_stop_0_153_0.json":      registration.EventCodexSubagentStop,
	"stop_0_153_0.json":               registration.EventCodexStop,
	"session_end_0_153_0.json":        registration.EventCodexSessionEnd,
	"interrupt_0_153_0.json":          registration.EventCodexInterrupt,
}

// TestClearedCodexCapturesAreAdmittedAndCarryNoUnsubstitutedFreeText holds the
// cleared captures whose events are not yet enabled to what a cleared capture
// must be, and to what it must NOT yet claim.
//
// What it must be: the shared refusals admit the bytes; the production parser
// retains them byte for byte; the sidecar validates against the committed
// bytes and names this harness's clearance record by path; the payload carries
// no refused class; and free-text-v1 has nothing left to substitute, which is
// the structural signature of a cleared payload and holds on any machine
// because it names no user.
//
// What it must not claim: a committed fixture is evidence of a capture, never
// of an enabled row. The production parser has no binding arm for these ten
// events, so it refuses them as an unsupported schema and binds no identity.
// The leaf that adds an arm moves the event out of the closed map above and
// gives it a real binding expectation.
func TestClearedCodexCapturesAreAdmittedAndCarryNoUnsubstitutedFreeText(t *testing.T) {
	t.Parallel()
	manifest := registration.Codex0_153_0()
	require.Len(t, clearedNotEnabledCodexFixtures, 10, "ten Codex events are cleared and not yet enabled")
	for file, kind := range clearedNotEnabledCodexFixtures {
		file, kind := file, kind
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(filepath.Join(codexFixtureDir, file))
			require.NoError(t, err)

			event := eventByKind(t, manifest, kind)
			sidecar := codexSidecar(t, file)
			require.NoError(t, sidecar.ValidateFixture("testdata", "fixtures/"+file), "the committed bytes are the cleared bytes the sidecar records")
			require.Equal(t, event.NativeName, sidecar.Event, "the sidecar names the native event the host declared")

			require.Equal(t, model.CaptureValid, ingress.Validate(raw).Disposition, "a cleared capture still passes the refusals every harness shares")

			refusals, err := ingress.RefusedFields(raw)
			require.NoError(t, err)
			require.Empty(t, refusals, "a refused class is never committed, whatever the substitution")

			// The check is the BYTES, not the flag list. free-text-v1 writes a
			// placeholder of the same raw length, and a long placeholder is
			// still classed as free text, so a cleared payload can still flag
			// a field. What a cleared payload cannot do is CHANGE when the
			// rule runs again: a surviving free-text value would be rewritten
			// to a placeholder and the bytes would move. A committed fixture
			// is therefore a fixed point of the rule, and only a placeholder
			// is one.
			substituted, _, err := ingress.SubstituteFreeText(raw)
			require.NoError(t, err)
			require.Equal(t, raw, substituted, "free-text-v1 rewrote a committed fixture, so free text survived the clearance")

			require.Empty(t, event.Identities, "a cleared capture is evidence of a capture, never of a proven identity")
			capture := codex.Parse(raw, event, manifest.Version, model.OccurrenceEnvelopeRef{})
			require.Equal(t, digest.FromBytes(raw), capture.Digest, "digest must be taken over the exact stdin bytes")
			require.Equal(t, raw, capture.Delivery.Body, "the retained body is the exact bytes, for an unsupported event as much as a supported one")
			require.Equal(t, model.CaptureUnsupportedSchema, capture.Disposition, "the parser has no binding arm for this event yet; the leaf that adds one moves this row")
			require.Empty(t, capture.Delivery.Bindings)
		})
	}
}
