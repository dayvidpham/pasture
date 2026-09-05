package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	claudefrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// authenticClaudeVersion is the recorded host version; the corpus was captured at it.
var authenticClaudeVersion = registration.ClaudeCode2_1_261().Version

type authenticClaudeFixture struct {
	name    string
	fixture string
	event   model.ContractEventKind
	// identities names, in binding order, the payload member each identity is
	// read from; the expected values come from the committed fixture itself.
	identities []authenticIdentity
	semantic   runtime.EventSemantic
	blocking   runtime.BlockingMode
	unresolved []waist.UnresolvedFact
}

type authenticIdentity struct {
	binding  model.NativeBindingKind
	semantic runtime.NativeIdentityKind
	member   string
}

var (
	sessionIdentity  = authenticIdentity{model.BindingSession, runtime.IdentitySession, "session_id"}
	toolCallIdentity = authenticIdentity{model.BindingToolCall, runtime.IdentityToolCall, "tool_use_id"}
)

var authenticClaudeEnabledFixtures = []authenticClaudeFixture{
	{name: "SessionStart", fixture: "session_start_2_1_261.json", event: registration.EventSessionStart, identities: []authenticIdentity{sessionIdentity}, semantic: runtime.SemanticObservation, blocking: runtime.NonBlocking},
	{name: "SessionEnd", fixture: "session_end_2_1_261.json", event: registration.EventSessionEnd, identities: []authenticIdentity{sessionIdentity}, semantic: runtime.SemanticObservation, blocking: runtime.NonBlocking},
	{name: "PreToolUse", fixture: "pre_tool_use_2_1_261.json", event: registration.EventPreToolUse, identities: []authenticIdentity{sessionIdentity, toolCallIdentity}, semantic: runtime.SemanticGateConsultation, blocking: runtime.Blocking},
	{name: "PostToolUse", fixture: "post_tool_use_2_1_261.json", event: registration.EventPostToolUse, identities: []authenticIdentity{sessionIdentity, toolCallIdentity}, semantic: runtime.SemanticObservation, blocking: runtime.NonBlocking},
	{name: "PostToolUseFailure", fixture: "post_tool_use_failure_2_1_261.json", event: registration.EventPostToolUseFailure, identities: []authenticIdentity{sessionIdentity, toolCallIdentity}, semantic: runtime.SemanticObservation, blocking: runtime.NonBlocking},
	{name: "PostToolBatch", fixture: "post_tool_batch_2_1_261.json", event: registration.EventPostToolBatch, identities: []authenticIdentity{sessionIdentity}, semantic: runtime.SemanticGateConsultation, blocking: runtime.Blocking, unresolved: []waist.UnresolvedFact{{Reason: waist.UnresolvedToolCall}}},
	{name: "PreCompact", fixture: "pre_compact_2_1_261.json", event: registration.EventPreCompact, identities: []authenticIdentity{sessionIdentity}, semantic: runtime.SemanticGateConsultation, blocking: runtime.Blocking},
	{name: "PostCompact", fixture: "post_compact_2_1_261.json", event: registration.EventPostCompact, identities: []authenticIdentity{sessionIdentity}, semantic: runtime.SemanticObservation, blocking: runtime.NonBlocking},
}

// expectedIdentities reads the identity values the fixture carries under the
// members the table names, so the proof follows the capture.
func expectedIdentities(t *testing.T, raw []byte, identities []authenticIdentity) ([]model.NativeBinding, []waist.SemanticIdentity) {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	bindings := make([]model.NativeBinding, 0, len(identities))
	semantic := make([]waist.SemanticIdentity, 0, len(identities))
	for _, identity := range identities {
		value, ok := payload[identity.member].(string)
		require.True(t, ok, "fixture carries no string member %q", identity.member)
		require.NotEmpty(t, value)
		bindings = append(bindings, model.NativeBinding{Kind: identity.binding, NativeName: identity.member, Value: value})
		semantic = append(semantic, waist.SemanticIdentity{Kind: identity.semantic, Value: value})
	}
	return bindings, semantic
}

func TestAuthenticClaude2_1_261FixturesThroughProductionWaist(t *testing.T) {
	t.Parallel()

	for _, testCase := range authenticClaudeEnabledFixtures {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			raw := readAuthenticClaudeFixture(t, testCase.fixture, testCase.name)
			registered := requireRegistrationEvent(t, testCase.event)
			capture := Parse(raw, registered, authenticClaudeVersion, model.OccurrenceEnvelopeRef{})
			require.Equal(t, model.CaptureValid, capture.Disposition)
			require.Equal(t, digest.FromBytes(raw), capture.Digest)
			require.Equal(t, raw, capture.Delivery.Body)
			bindings, semanticIdentities := expectedIdentities(t, raw, testCase.identities)
			require.Equal(t, bindings, capture.Delivery.Bindings)

			l1, identities, err := claudefrontend.Bind(capture.Delivery.Event, capture.Delivery.Bindings)
			require.NoError(t, err)
			l2, err := l1.NewEvent(identities)
			require.NoError(t, err)
			require.Equal(t, testCase.name, string(l2.Origin().NativeEventName()))
			require.Equal(t, testCase.semantic, l2.Semantics().Semantic())
			require.Equal(t, testCase.blocking, l2.Semantics().Blocking())
			require.Equal(t, semanticIdentities, l2.Semantics().Identities())
			require.Equal(t, testCase.unresolved, l2.Semantics().UnresolvedFacts())
		})
	}
}

// TestAuthenticClaude2_1_261ElicitationRemainsUncorrelatedAndWithheld: no
// Elicitation or ElicitationResult payload was captured at 2.1.261 (the pair
// needs an MCP elicitation the capture session did not drive), so the corpus
// holds no fixture for either and both rows stay withheld for want of a
// request correlation. The runtime mapping still requires the request identity
// a future capture must prove.
func TestAuthenticClaude2_1_261ElicitationRemainsUncorrelatedAndWithheld(t *testing.T) {
	t.Parallel()

	for _, stem := range []string{"elicitation_", "elicitation_result_"} {
		matches, err := filepath.Glob(filepath.Join("testdata", "fixtures", stem+"*.json"))
		require.NoError(t, err)
		require.Empty(t, matches, "no %s fixture was captured at this version; a fixture here means the pair must be re-evaluated", stem)
	}

	entries, err := activation.ClaudeCode2_1_261()
	require.NoError(t, err)
	for _, kind := range []model.ContractEventKind{registration.EventElicitation, registration.EventElicitationResult} {
		entry, found := activationEntry(entries, kind)
		require.True(t, found)
		require.Equal(t, activation.Withheld, entry.State)
		require.Equal(t, activation.WithheldMissingRequestCorrelation, entry.Reason)
		require.Zero(t, entry.CaptureProof)
		require.Zero(t, entry.ProductionProof)
	}
	resultMapping, err := runtime.ClaudeCode2_1_261Lifecycle().Mapping(runtime.ClaudeEventElicitationResult)
	require.NoError(t, err)
	require.Equal(t, runtime.SemanticExplicitHumanResponse, resultMapping.Semantic())
	require.Len(t, resultMapping.Identities(), 2)
	require.Equal(t, runtime.IdentityRequest, resultMapping.Identities()[1].Kind())
	require.True(t, resultMapping.Identities()[1].Required())
}

func TestAuthenticClaude2_1_261FixtureInventoryAndPrivacy(t *testing.T) {
	t.Parallel()

	events := map[string]string{
		"post_compact_2_1_261.json":          "PostCompact",
		"post_tool_batch_2_1_261.json":       "PostToolBatch",
		"post_tool_use_2_1_261.json":         "PostToolUse",
		"post_tool_use_failure_2_1_261.json": "PostToolUseFailure",
		"pre_compact_2_1_261.json":           "PreCompact",
		"pre_tool_use_2_1_261.json":          "PreToolUse",
		"session_end_2_1_261.json":           "SessionEnd",
		"session_start_2_1_261.json":         "SessionStart",
	}
	// The three controls carry the SessionStart bytes with one sidecar member
	// changed each; they are part of the closed inventory and are checked by
	// the corpus tests, not as authentic fixtures here.
	controls := map[string]struct{}{
		"session_start_2_1_261_digest_mismatch.json":      {},
		"session_start_2_1_261_origin_authored.json":      {},
		"session_start_2_1_261_version_out_of_range.json": {},
	}
	fixtures, err := filepath.Glob(filepath.Join("testdata", "fixtures", "*_2_1_261*.json"))
	require.NoError(t, err)
	var payloads, provenanceFiles []string
	for _, fixture := range fixtures {
		if strings.HasSuffix(fixture, ".provenance.json") {
			provenanceFiles = append(provenanceFiles, fixture)
		} else {
			payloads = append(payloads, fixture)
		}
	}
	require.Len(t, payloads, len(events)+len(controls), "every reviewed payload must be in the closed 2.1.261 inventory")
	require.Len(t, provenanceFiles, len(events)+len(controls), "every reviewed payload must have exactly one sidecar")

	actualNames := make([]string, 0, len(payloads))
	for _, fixture := range payloads {
		name := filepath.Base(fixture)
		actualNames = append(actualNames, name)
		var raw []byte
		if _, control := controls[name]; control {
			raw, err = os.ReadFile(fixture)
			require.NoError(t, err)
		} else {
			event, expected := events[name]
			require.True(t, expected, "unexpected 2.1.261 fixture %s", name)
			raw = readAuthenticClaudeFixture(t, name, event)
		}
		body := strings.ToLower(string(raw))
		require.NotContains(t, body, "minttea", "the capturing user's name must not survive clearance in any spelling")
		require.Contains(t, body, "/home/user")
		for _, forbidden := range []string{"api_key", "access_token", "refresh_token", "authorization", "bearer ", "private_key", "begin private key", "password", "secret"} {
			require.NotContains(t, body, forbidden, "%s contains privacy-sensitive value marker %q", name, forbidden)
		}
	}
	expectedNames := make([]string, 0, len(events)+len(controls))
	for name := range events {
		expectedNames = append(expectedNames, name)
	}
	for name := range controls {
		expectedNames = append(expectedNames, name)
	}
	sort.Strings(expectedNames)
	sort.Strings(actualNames)
	require.Equal(t, expectedNames, actualNames)
}

func readAuthenticClaudeFixture(t *testing.T, fixture, expectedEvent string) []byte {
	t.Helper()
	relativeFixture := filepath.Join("fixtures", fixture)
	raw, err := os.ReadFile(filepath.Join("testdata", relativeFixture))
	require.NoError(t, err)
	require.True(t, json.Valid(raw))
	provenancePath := strings.TrimSuffix(relativeFixture, ".json") + ".provenance.json"
	provenanceBytes, err := os.ReadFile(filepath.Join("testdata", provenancePath))
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(provenanceBytes, &fields))
	require.ElementsMatch(t,
		[]string{"origin", "harness", "harnessVersion", "captureSource", "rawFileDigest", "capturedAt", "redaction", "event", "clearance"},
		authenticMapKeys(fields),
	)
	var sidecar acceptance.CaptureProvenance
	require.NoError(t, json.Unmarshal(provenanceBytes, &sidecar))
	require.Equal(t, acceptance.OriginAuthenticCapture, sidecar.Origin)
	require.Equal(t, acceptance.HarnessClaudeCode, sidecar.Harness)
	require.Equal(t, registration.ClaudeCode2_1_261().Version, sidecar.HarnessVersion, "captured at the recorded host version")
	require.Equal(t, "internal/handlers/capture_sink.go (PASTURE_CAPTURE_DIR)", sidecar.CaptureSource, "every fixture of this corpus came through the in-binary capture sink")
	rules, err := acceptance.ParseRedaction(sidecar.Redaction)
	require.NoError(t, err)
	require.Equal(t, acceptance.RedactionHomePath, rules[0], "every Claude payload carries the home path, so home-path-v1 is applied first")
	require.Equal(t, "internal/lifecycle/ingress/claude/testdata/fixtures/CLEARANCE.md", sidecar.Clearance)
	require.Equal(t, expectedEvent, sidecar.Event)
	require.Equal(t, digest.FromBytes(raw).String(), sidecar.RawFileDigest)
	require.NoError(t, sidecar.ValidateFixture("testdata", relativeFixture))
	return raw
}

func activationEntry(entries []activation.Entry, event model.ContractEventKind) (activation.Entry, bool) {
	for _, entry := range entries {
		if entry.Event == event {
			return entry, true
		}
	}
	return activation.Entry{}, false
}

func authenticMapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
