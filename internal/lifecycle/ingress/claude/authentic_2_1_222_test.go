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

const authenticClaudeVersion = "2.1.222"

type authenticClaudeFixture struct {
	name               string
	fixture            string
	event              model.ContractEventKind
	bindings           []model.NativeBinding
	semanticIdentities []waist.SemanticIdentity
	semantic           runtime.EventSemantic
	blocking           runtime.BlockingMode
	unresolved         []waist.UnresolvedFact
}

var authenticClaudeEnabledFixtures = []authenticClaudeFixture{
	{
		name: "SessionStart", fixture: "session_start_2_1_222.json", event: registration.EventSessionStart,
		bindings:           []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "3696b790-3973-49f2-b156-9d82146bf7ec"}},
		semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "3696b790-3973-49f2-b156-9d82146bf7ec"}},
		semantic:           runtime.SemanticObservation, blocking: runtime.NonBlocking,
	},
	{
		name: "SessionEnd", fixture: "session_end_2_1_222.json", event: registration.EventSessionEnd,
		bindings:           []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "dc64d2f6-0d5e-4e2a-b880-49c980a6c750"}},
		semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "dc64d2f6-0d5e-4e2a-b880-49c980a6c750"}},
		semantic:           runtime.SemanticObservation, blocking: runtime.NonBlocking,
	},
	{
		name: "PreToolUse", fixture: "pre_tool_use_2_1_222.json", event: registration.EventPreToolUse,
		bindings: []model.NativeBinding{
			{Kind: model.BindingSession, NativeName: "session_id", Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"},
			{Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: "toolu_01BZyDFUsmM5YK5u8ZRkrvcE"},
		},
		semanticIdentities: []waist.SemanticIdentity{
			{Kind: runtime.IdentitySession, Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"},
			{Kind: runtime.IdentityToolCall, Value: "toolu_01BZyDFUsmM5YK5u8ZRkrvcE"},
		},
		semantic: runtime.SemanticGateConsultation, blocking: runtime.Blocking,
	},
	{
		name: "PostToolUse", fixture: "post_tool_use_2_1_222.json", event: registration.EventPostToolUse,
		bindings: []model.NativeBinding{
			{Kind: model.BindingSession, NativeName: "session_id", Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"},
			{Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: "toolu_01G65GiPDVnmxtRrYWq9aZaP"},
		},
		semanticIdentities: []waist.SemanticIdentity{
			{Kind: runtime.IdentitySession, Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"},
			{Kind: runtime.IdentityToolCall, Value: "toolu_01G65GiPDVnmxtRrYWq9aZaP"},
		},
		semantic: runtime.SemanticObservation, blocking: runtime.NonBlocking,
	},
	{
		name: "PostToolUseFailure", fixture: "post_tool_use_failure_2_1_222.json", event: registration.EventPostToolUseFailure,
		bindings: []model.NativeBinding{
			{Kind: model.BindingSession, NativeName: "session_id", Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"},
			{Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: "toolu_01BZyDFUsmM5YK5u8ZRkrvcE"},
		},
		semanticIdentities: []waist.SemanticIdentity{
			{Kind: runtime.IdentitySession, Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"},
			{Kind: runtime.IdentityToolCall, Value: "toolu_01BZyDFUsmM5YK5u8ZRkrvcE"},
		},
		semantic: runtime.SemanticObservation, blocking: runtime.NonBlocking,
	},
	{
		name: "PostToolBatch", fixture: "post_tool_batch_2_1_222.json", event: registration.EventPostToolBatch,
		bindings:           []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"}},
		semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"}},
		semantic:           runtime.SemanticGateConsultation, blocking: runtime.Blocking,
		unresolved: []waist.UnresolvedFact{{Reason: waist.UnresolvedToolCall}},
	},
	{
		name: "PreCompact", fixture: "pre_compact_2_1_222.json", event: registration.EventPreCompact,
		bindings:           []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "2c9a0e31-7f70-4a54-b44a-264444df74a1"}},
		semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "2c9a0e31-7f70-4a54-b44a-264444df74a1"}},
		semantic:           runtime.SemanticGateConsultation, blocking: runtime.Blocking,
	},
	{
		name: "PostCompact", fixture: "post_compact_2_1_222.json", event: registration.EventPostCompact,
		bindings:           []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "2c9a0e31-7f70-4a54-b44a-264444df74a1"}},
		semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "2c9a0e31-7f70-4a54-b44a-264444df74a1"}},
		semantic:           runtime.SemanticObservation, blocking: runtime.NonBlocking,
	},
}

func TestAuthenticClaude2_1_222FixturesThroughProductionWaist(t *testing.T) {
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
			require.Equal(t, testCase.bindings, capture.Delivery.Bindings)

			l1, identities, err := claudefrontend.Bind(capture.Delivery.Event, capture.Delivery.Bindings)
			require.NoError(t, err)
			l2, err := l1.NewEvent(identities)
			require.NoError(t, err)
			require.Equal(t, testCase.name, string(l2.Origin().NativeEventName()))
			require.Equal(t, testCase.semantic, l2.Semantics().Semantic())
			require.Equal(t, testCase.blocking, l2.Semantics().Blocking())
			require.Equal(t, testCase.semanticIdentities, l2.Semantics().Identities())
			require.Equal(t, testCase.unresolved, l2.Semantics().UnresolvedFacts())
		})
	}
}

func TestAuthenticClaude2_1_222ElicitationRemainsUncorrelatedAndWithheld(t *testing.T) {
	t.Parallel()

	type elicitationContext struct {
		SessionID string         `json:"session_id"`
		PromptID  string         `json:"prompt_id"`
		Event     string         `json:"hook_event_name"`
		Server    string         `json:"mcp_server_name"`
		Mode      string         `json:"mode"`
		Action    string         `json:"action"`
		Content   map[string]any `json:"content"`
		RequestID string         `json:"request_id"`
	}
	cases := []struct {
		fixture string
		event   model.ContractEventKind
	}{
		{fixture: "elicitation_2_1_222.json", event: registration.EventElicitation},
		{fixture: "elicitation_result_2_1_222.json", event: registration.EventElicitationResult},
	}
	contexts := make([]elicitationContext, 0, len(cases))
	for _, testCase := range cases {
		raw := readAuthenticClaudeFixture(t, testCase.fixture, requireRegistrationEvent(t, testCase.event).NativeName)
		capture := Parse(raw, requireRegistrationEvent(t, testCase.event), authenticClaudeVersion, model.OccurrenceEnvelopeRef{})
		require.Equal(t, model.CaptureUnsupportedSchema, capture.Disposition)
		require.Empty(t, capture.Delivery.Bindings, "missing request identity must discard the session binding")
		var context elicitationContext
		require.NoError(t, json.Unmarshal(raw, &context))
		require.Empty(t, context.RequestID, "Claude 2.1.222 did not expose an authoritative request identity")
		contexts = append(contexts, context)
	}
	require.Equal(t, contexts[0].SessionID, contexts[1].SessionID)
	require.Equal(t, contexts[0].PromptID, contexts[1].PromptID)
	require.Equal(t, contexts[0].Server, contexts[1].Server)
	require.Equal(t, contexts[0].Mode, contexts[1].Mode)
	require.Equal(t, "accept", contexts[1].Action)
	require.Equal(t, "capture-approved", contexts[1].Content["capture_code"])

	entries, err := activation.ClaudeCode2_1_210()
	require.NoError(t, err)
	for _, kind := range []model.ContractEventKind{registration.EventElicitation, registration.EventElicitationResult} {
		entry, found := activationEntry(entries, kind)
		require.True(t, found)
		require.Equal(t, activation.Withheld, entry.State)
		require.Equal(t, activation.WithheldMissingRequestCorrelation, entry.Reason)
		require.Zero(t, entry.CaptureProof)
		require.Zero(t, entry.ProductionProof)
	}
	resultMapping, err := runtime.ClaudeCode2_1_210Lifecycle().Mapping(runtime.ClaudeEventElicitationResult)
	require.NoError(t, err)
	require.Equal(t, runtime.SemanticExplicitHumanResponse, resultMapping.Semantic())
	require.Len(t, resultMapping.Identities(), 2)
	require.Equal(t, runtime.IdentityRequest, resultMapping.Identities()[1].Kind())
	require.True(t, resultMapping.Identities()[1].Required())
}

func TestAuthenticClaude2_1_222FixtureInventoryAndPrivacy(t *testing.T) {
	t.Parallel()

	events := map[string]string{
		"elicitation_2_1_222.json":           "Elicitation",
		"elicitation_result_2_1_222.json":    "ElicitationResult",
		"post_compact_2_1_222.json":          "PostCompact",
		"post_tool_batch_2_1_222.json":       "PostToolBatch",
		"post_tool_use_2_1_222.json":         "PostToolUse",
		"post_tool_use_failure_2_1_222.json": "PostToolUseFailure",
		"pre_compact_2_1_222.json":           "PreCompact",
		"pre_tool_use_2_1_222.json":          "PreToolUse",
		"session_end_2_1_222.json":           "SessionEnd",
		"session_start_2_1_222.json":         "SessionStart",
	}
	fixtures, err := filepath.Glob(filepath.Join("testdata", "fixtures", "*_2_1_222.json"))
	require.NoError(t, err)
	provenanceFiles, err := filepath.Glob(filepath.Join("testdata", "fixtures", "*_2_1_222.provenance.json"))
	require.NoError(t, err)
	require.Len(t, fixtures, len(events), "every reviewed payload must be in the closed 2.1.222 inventory")
	require.Len(t, provenanceFiles, len(events), "every reviewed payload must have exactly one sidecar")

	actualNames := make([]string, 0, len(fixtures))
	for _, fixture := range fixtures {
		name := filepath.Base(fixture)
		actualNames = append(actualNames, name)
		event, expected := events[name]
		require.True(t, expected, "unexpected 2.1.222 fixture %s", name)
		raw := readAuthenticClaudeFixture(t, name, event)
		body := strings.ToLower(string(raw))
		require.NotContains(t, body, "/home/minttea")
		require.Contains(t, body, "/home/user")
		for _, forbidden := range []string{"api_key", "access_token", "refresh_token", "authorization", "bearer ", "private_key", "begin private key", "password", "secret"} {
			require.NotContains(t, body, forbidden, "%s contains privacy-sensitive value marker %q", name, forbidden)
		}
	}
	expectedNames := make([]string, 0, len(events))
	for name := range events {
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
		[]string{"origin", "harness", "harnessVersion", "captureSource", "rawFileDigest", "capturedAt", "redaction", "event"},
		authenticMapKeys(fields),
	)
	var sidecar struct {
		acceptance.CaptureProvenance
		Redaction string `json:"redaction"`
		Event     string `json:"event"`
	}
	require.NoError(t, json.Unmarshal(provenanceBytes, &sidecar))
	require.Equal(t, acceptance.OriginAuthenticCapture, sidecar.Origin)
	require.Equal(t, acceptance.HarnessClaudeCode, sidecar.Harness)
	require.Equal(t, authenticClaudeVersion, sidecar.HarnessVersion)
	require.Equal(t, "tools/capture-claude-hook.sh", sidecar.CaptureSource)
	require.Equal(t, "home-path-v1", sidecar.Redaction)
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
