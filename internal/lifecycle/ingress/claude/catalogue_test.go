package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	claudefrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/testutil"
)

const canonicalAuthenticFixtureDigest = "sha256:30d524e5d2cb22d486faad05adbaa1a4b7e0d72cd6301f38fe18ca5e3f167003"

// expectedCase is one corpus row as the product defines it: a name, a
// classification, and the decision and reason the evaluator must return.
type expectedCase struct {
	name           string
	classification activation.Classification
	decision       activation.Decision
	reason         activation.CorpusReason
}

// requiredControls are the must-fail controls the corpus loader requires, one
// per closed withheld reason it demands coverage for (activation.LoadCorpus
// refuses a corpus that lacks any of them), in source order.
var requiredControls = []expectedCase{
	{name: "authored-origin-control", classification: activation.ClassificationMustFail, decision: activation.DecisionWithheld, reason: activation.CorpusReasonNonAuthenticOrigin},
	{name: "digest-mismatch-control", classification: activation.ClassificationMustFail, decision: activation.DecisionWithheld, reason: activation.CorpusReasonDigestMismatch},
	{name: "version-out-of-range-control", classification: activation.ClassificationMustFail, decision: activation.DecisionWithheld, reason: activation.CorpusReasonVersionOutOfRange},
	{name: "path-escape-control", classification: activation.ClassificationMustFail, decision: activation.DecisionWithheld, reason: activation.CorpusReasonPathEscape},
}

// expectedCorpusCases derives the corpus shape from the product: one must-pass
// case per ENABLED Claude target event, in declaration order and named
// "authentic-<event>", then the required controls. A declared target that is
// withheld has no authentic fixture and therefore no corpus case. A target
// enabled later is covered here without an edit to this file, and the list is
// never empty.
func expectedCorpusCases(t *testing.T) []expectedCase {
	t.Helper()
	targets := enabledClaudeTargets(t)
	out := make([]expectedCase, 0, len(targets)+len(requiredControls))
	for _, target := range targets {
		out = append(out, expectedCase{name: "authentic-" + kebabCase(nativeNameOf(t, target)), classification: activation.ClassificationMustPass, decision: activation.DecisionEnabled, reason: activation.CorpusReasonNone})
	}
	return append(out, requiredControls...)
}

// enabledClaudeTargets returns the declared Claude target events whose
// activation entry is Enabled, in declaration order, and refuses an empty set.
func enabledClaudeTargets(t *testing.T) []model.ContractEventKind {
	t.Helper()
	entries, err := activation.ClaudeCode2_1_210()
	require.NoError(t, err)
	state := make(map[model.ContractEventKind]activation.State, len(entries))
	for _, entry := range entries {
		state[entry.Event] = entry.State
	}
	var enabled []model.ContractEventKind
	for _, target := range activation.ClaudeCode2_1_210TargetEvents() {
		if state[target] == activation.Enabled {
			enabled = append(enabled, target)
		}
	}
	require.NotEmpty(t, enabled, "the Claude target table enables at least one event")
	return enabled
}

// nativeNameOf reads the native event name of a registered kind from the
// generated Claude manifest.
func nativeNameOf(t *testing.T, kind model.ContractEventKind) string {
	t.Helper()
	for _, entry := range registration.ClaudeCode2_1_210().Entries() {
		if entry.Kind == kind {
			return entry.NativeName
		}
	}
	t.Fatalf("event kind %d is not in the generated Claude manifest", kind)
	return ""
}

// kebabCase lowers a CamelCase native event name to the corpus case spelling:
// PostToolUseFailure -> post-tool-use-failure.
func kebabCase(name string) string {
	var b strings.Builder
	for index, r := range name {
		if index > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('-')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

func TestRealCaptureCorpusDrivesStaticActivation(t *testing.T) {
	t.Parallel()

	wantCases := expectedCorpusCases(t)
	corpus, err := activation.LoadCorpus(filepath.Join("testdata", "captures.yaml"))
	require.NoError(t, err)
	cases := corpus.Cases()
	require.Len(t, cases, len(wantCases), "one must-pass case per declared target plus the required controls")

	entries, err := activation.ClaudeCode2_1_210()
	require.NoError(t, err)
	entryByEvent := make(map[model.ContractEventKind]activation.Entry, len(entries))
	enabledEvents := make(map[model.ContractEventKind]struct{})
	for _, entry := range entries {
		require.True(t, entry.IsValid(), "activation entry %d", entry.Event)
		_, duplicate := entryByEvent[entry.Event]
		require.False(t, duplicate, "duplicate activation entry %d", entry.Event)
		entryByEvent[entry.Event] = entry
		if entry.State == activation.Enabled {
			enabledEvents[entry.Event] = struct{}{}
		}
	}

	admittedEvents := make(map[model.ContractEventKind]struct{})
	seenReasons := make(map[activation.CorpusReason]struct{})
	passCount, failCount := 0, 0
	for index, testCase := range cases {
		want := wantCases[index]
		require.True(t, testCase.IsValid(), "case %d", index)
		require.Equal(t, want.name, testCase.Name(), "source-ordered case %d", index)
		require.Equal(t, want.classification, testCase.Classification(), want.name)
		require.Equal(t, want.decision, testCase.ExpectedDecision(), want.name)
		require.Equal(t, want.reason, testCase.ExpectedReason(), want.name)

		evaluation, err := activation.ClaudeCodeEvaluator().Evaluate("testdata", testCase)
		require.NoError(t, err, want.name)
		require.True(t, evaluation.IsValid(), want.name)
		require.Equal(t, testCase.Name(), evaluation.CaseName(), want.name)
		require.Equal(t, want.decision, evaluation.Decision(), want.name)
		require.Equal(t, want.reason, evaluation.Reason(), want.name)

		event, present := evaluation.Event()
		switch want.classification {
		case activation.ClassificationMustPass:
			passCount++
			require.Equal(t, activation.DecisionEnabled, evaluation.Decision())
			require.True(t, present, want.name)
			entry, exists := entryByEvent[event]
			require.True(t, exists, "admitted event %d has no activation entry", event)
			require.True(t, entry.IsValid())
			require.Equal(t, activation.Enabled, entry.State)
			captureEvent, capturePresent := entry.CaptureProof.Event()
			productionEvent, productionPresent := entry.ProductionProof.Event()
			require.True(t, capturePresent)
			require.True(t, productionPresent)
			require.Equal(t, event, captureEvent)
			require.Equal(t, event, productionEvent)
			require.NotEmpty(t, entry.CaptureProof.Name())
			require.NotEmpty(t, entry.ProductionProof.Name())
			admittedEvents[event] = struct{}{}
		case activation.ClassificationMustFail:
			failCount++
			require.Equal(t, activation.DecisionWithheld, evaluation.Decision())
			require.False(t, present, want.name)
			require.Zero(t, event, want.name)
			seenReasons[evaluation.Reason()] = struct{}{}
		default:
			t.Fatalf("case %q has invalid classification %d", testCase.Name(), testCase.Classification())
		}
	}

	require.Equal(t, len(enabledClaudeTargets(t)), passCount, "one must-pass case per enabled target")
	require.Equal(t, len(requiredControls), failCount, "one control per required withheld reason")
	require.Equal(t, map[activation.CorpusReason]struct{}{
		activation.CorpusReasonNonAuthenticOrigin: {},
		activation.CorpusReasonDigestMismatch:     {},
		activation.CorpusReasonVersionOutOfRange:  {},
		activation.CorpusReasonPathEscape:         {},
	}, seenReasons)
	require.Equal(t, map[model.ContractEventKind]struct{}{
		registration.EventSessionStart: {}, registration.EventSessionEnd: {},
		registration.EventPreToolUse: {}, registration.EventPostToolUse: {},
		registration.EventPostToolUseFailure: {}, registration.EventPostToolBatch: {},
		registration.EventPreCompact: {}, registration.EventPostCompact: {},
	}, admittedEvents)
	require.Equal(t, enabledEvents, admittedEvents, "every enabled event needs independent real-corpus admission")

	for _, event := range activation.ClaudeCode2_1_210TargetEvents() {
		entry, exists := entryByEvent[event]
		require.True(t, exists, "target event %d", event)
		if _, enabled := admittedEvents[event]; enabled {
			continue
		}
		require.Equal(t, activation.Withheld, entry.State, "target event %d", event)
		require.Equal(t, activation.WithheldMissingRequestCorrelation, entry.Reason, "target event %d", event)
		require.Zero(t, entry.CaptureProof, "target event %d", event)
		require.Zero(t, entry.ProductionProof, "target event %d", event)
	}
}

// These contract-shaped payloads are parser/frontend breadth inputs only. They
// are not authentic host captures and must never be used as activation proof.
func TestNonAuthenticTargetBreadthThroughProductionWaist(t *testing.T) {
	t.Parallel()

	type targetRow struct {
		name               string
		payload            []byte
		event              model.ContractEventKind
		nativeName         string
		bindings           []model.NativeBinding
		semanticIdentities []waist.SemanticIdentity
		semantic           runtime.EventSemantic
		blocking           runtime.BlockingMode
		unresolved         []waist.UnresolvedFact
	}
	rows := []targetRow{
		{name: "SessionStart", payload: []byte(`{"session_id":"session-breadth-1","hook_event_name":"SessionStart","source":"startup","model":"claude","session_title":"breadth"}`), event: registration.EventSessionStart, nativeName: "SessionStart", bindings: []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "session-breadth-1"}}, semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "session-breadth-1"}}, semantic: runtime.SemanticObservation, blocking: runtime.NonBlocking},
		{name: "SessionEnd", payload: []byte(`{"session_id":"session-breadth-1","hook_event_name":"SessionEnd","reason":"complete"}`), event: registration.EventSessionEnd, nativeName: "SessionEnd", bindings: []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "session-breadth-1"}}, semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "session-breadth-1"}}, semantic: runtime.SemanticObservation, blocking: runtime.NonBlocking},
		{name: "PreToolUse", payload: []byte(`{"session_id":"session-breadth-1","hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"README.md"},"tool_use_id":"tool-call-breadth-1"}`), event: registration.EventPreToolUse, nativeName: "PreToolUse", bindings: []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "session-breadth-1"}, {Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: "tool-call-breadth-1"}}, semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "session-breadth-1"}, {Kind: runtime.IdentityToolCall, Value: "tool-call-breadth-1"}}, semantic: runtime.SemanticGateConsultation, blocking: runtime.Blocking},
		{name: "PostToolUse", payload: []byte(`{"session_id":"session-breadth-1","hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"file_path":"README.md"},"tool_output":"ok","tool_use_id":"tool-call-breadth-1"}`), event: registration.EventPostToolUse, nativeName: "PostToolUse", bindings: []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "session-breadth-1"}, {Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: "tool-call-breadth-1"}}, semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "session-breadth-1"}, {Kind: runtime.IdentityToolCall, Value: "tool-call-breadth-1"}}, semantic: runtime.SemanticObservation, blocking: runtime.NonBlocking},
		{name: "PostToolUseFailure", payload: []byte(`{"session_id":"session-breadth-1","hook_event_name":"PostToolUseFailure","tool_name":"Read","tool_input":{"file_path":"missing"},"error":"not found","tool_use_id":"tool-call-breadth-1"}`), event: registration.EventPostToolUseFailure, nativeName: "PostToolUseFailure", bindings: []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "session-breadth-1"}, {Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: "tool-call-breadth-1"}}, semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "session-breadth-1"}, {Kind: runtime.IdentityToolCall, Value: "tool-call-breadth-1"}}, semantic: runtime.SemanticObservation, blocking: runtime.NonBlocking},
		{name: "PostToolBatch", payload: []byte(`{"session_id":"session-breadth-1","hook_event_name":"PostToolBatch","batch_results":[{"tool_name":"Read","status":"ok"}]}`), event: registration.EventPostToolBatch, nativeName: "PostToolBatch", bindings: []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "session-breadth-1"}}, semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "session-breadth-1"}}, semantic: runtime.SemanticGateConsultation, blocking: runtime.Blocking, unresolved: []waist.UnresolvedFact{{Reason: waist.UnresolvedToolCall}}},
		{name: "PreCompact", payload: []byte(`{"session_id":"session-breadth-1","hook_event_name":"PreCompact","trigger":"manual"}`), event: registration.EventPreCompact, nativeName: "PreCompact", bindings: []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "session-breadth-1"}}, semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "session-breadth-1"}}, semantic: runtime.SemanticGateConsultation, blocking: runtime.Blocking},
		{name: "PostCompact", payload: []byte(`{"session_id":"session-breadth-1","hook_event_name":"PostCompact","trigger":"manual"}`), event: registration.EventPostCompact, nativeName: "PostCompact", bindings: []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "session-breadth-1"}}, semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "session-breadth-1"}}, semantic: runtime.SemanticObservation, blocking: runtime.NonBlocking},
		{name: "Elicitation", payload: []byte(`{"session_id":"session-breadth-1","hook_event_name":"Elicitation","request_id":"request-breadth-1","fields":[{"name":"choice"}],"mcp_server_name":"example"}`), event: registration.EventElicitation, nativeName: "Elicitation", bindings: []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "session-breadth-1"}, {Kind: model.BindingRequest, NativeName: "request_id", Value: "request-breadth-1"}}, semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "session-breadth-1"}, {Kind: runtime.IdentityRequest, Value: "request-breadth-1"}}, semantic: runtime.SemanticGateConsultation, blocking: runtime.Blocking},
		{name: "ElicitationResult", payload: []byte(`{"session_id":"session-breadth-1","hook_event_name":"ElicitationResult","request_id":"request-breadth-1","response":{"choice":"yes"},"mcp_server_name":"example"}`), event: registration.EventElicitationResult, nativeName: "ElicitationResult", bindings: []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "session-breadth-1"}, {Kind: model.BindingRequest, NativeName: "request_id", Value: "request-breadth-1"}}, semanticIdentities: []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "session-breadth-1"}, {Kind: runtime.IdentityRequest, Value: "request-breadth-1"}}, semantic: runtime.SemanticExplicitHumanResponse, blocking: runtime.Blocking},
	}
	require.Len(t, rows, 10)

	for _, row := range rows {
		row := row
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			registered := requireRegistrationEvent(t, row.event)
			require.Equal(t, row.event, registered.Kind)
			require.Equal(t, row.nativeName, registered.NativeName)

			input := append([]byte(nil), row.payload...)
			capture := Parse(input, registered, registration.ClaudeCode2_1_210().Version, model.OccurrenceEnvelopeRef{})
			input[0] = ' '
			require.Equal(t, model.CaptureValid, capture.Disposition)
			require.Equal(t, model.CaptureValid, capture.Delivery.Capture)
			require.Equal(t, registration.ClaudeCode2_1_210().Contract, capture.Delivery.Contract)
			require.Equal(t, row.event, capture.Delivery.Event)
			require.Equal(t, row.payload, capture.Delivery.Body, "delivery retains defensive exact bytes")
			require.Equal(t, row.bindings, capture.Delivery.Bindings, "native binding order/name/kind/value")

			l1, identities, err := claudefrontend.Bind(capture.Delivery.Event, capture.Delivery.Bindings)
			require.NoError(t, err)
			require.True(t, l1.IsValid())
			require.Len(t, identities, len(row.bindings))
			for index, identity := range identities {
				require.True(t, identity.IsValid(), "identity %d", index)
				require.Equal(t, row.bindings[index].NativeName, identity.NativeName(), "identity %d", index)
				require.Equal(t, uint8(row.bindings[index].Kind), uint8(identity.Kind()), "identity %d", index)
				require.Equal(t, row.bindings[index].Value, identity.Value(), "identity %d", index)
			}

			l2, err := l1.NewEvent(identities)
			require.NoError(t, err)
			require.True(t, l2.IsValid())
			require.True(t, l2.Origin().IsValid())
			require.Equal(t, waist.NativeEventName(row.nativeName), l2.Origin().NativeEventName())
			require.True(t, l2.Semantics().IsValid())
			require.Equal(t, row.semantic, l2.Semantics().Semantic())
			require.Equal(t, row.blocking, l2.Semantics().Blocking())
			require.Equal(t, row.semanticIdentities, l2.Semantics().Identities())
			require.Equal(t, row.unresolved, l2.Semantics().UnresolvedFacts())
		})
	}
}

func requireRegistrationEvent(t *testing.T, kind model.ContractEventKind) registration.Event {
	t.Helper()
	for _, event := range registration.ClaudeCode2_1_210().Entries() {
		if event.Kind == kind {
			return event
		}
	}
	t.Fatalf("generated Claude registration has no event kind %d", kind)
	return registration.Event{}
}

func TestIndependentCatalogueCoversGeneratedManifest(t *testing.T) {
	t.Parallel()
	type row struct {
		Name     string   `yaml:"name"`
		Fields   []string `yaml:"fields"`
		Behavior string   `yaml:"behavior"`
	}
	var catalogue struct {
		Events []row `yaml:"events"`
	}
	raw, err := os.ReadFile("testdata/corpora/claude_2_1_210_catalogue.yaml")
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, &catalogue))
	manifest := registration.ClaudeCode2_1_210()
	require.Len(t, catalogue.Events, len(manifest.Events))
	for i, expected := range catalogue.Events {
		require.Equal(t, expected.Name, manifest.Events[i].NativeName, "independent catalogue order %d", i)
		actualFields := make([]string, 0)
		identityFields := make(map[string]struct{})
		for _, identity := range manifest.Events[i].Identities {
			identityFields[fieldNames[identity.Field]] = struct{}{}
		}
		for _, field := range manifest.Events[i].AllowedFields {
			name := fieldNames[field]
			_, identity := identityFields[name]
			if !isCommonField(name) || (identity && name != "session_id") {
				actualFields = append(actualFields, name)
			}
		}
		sort.Strings(actualFields)
		sort.Strings(expected.Fields)
		require.Equal(t, expected.Fields, actualFields, "independent field catalogue for %s", expected.Name)
		require.Equal(t, expected.Behavior, behaviorName(manifest.Events[i]), "independent behavior catalogue for %s", expected.Name)
	}
}

func TestClaudeCatalogueMatchesRuntimeMappingForAllOrdinals(t *testing.T) {
	t.Parallel()

	manifest := registration.ClaudeCode2_1_210()
	runtimeContract := runtime.ClaudeCode2_1_210Lifecycle()
	// Two roots record the host version: the runtime contract id and the host
	// contract the manifest is generated from. They are one version or nothing
	// downstream agrees on which host it describes.
	require.Equal(t, runtimeContract.Versions().Min().String(), manifest.Version, "the registration manifest and the runtime contract record one host version")
	runtimeEvents := runtime.ClaudeLifecycleEvents()
	require.NotEmpty(t, runtimeEvents)
	require.Len(t, manifest.Events, len(runtimeEvents), "one registration row per runtime profile event")
	require.NotEqual(t, manifest.Contract, runtimeContract.ID(), "occurrence registration and semantic runtime contracts are intentionally distinct")

	for index, registered := range manifest.Events {
		runtimeEvent := runtimeEvents[index]
		require.Equal(t, model.ContractEventKind(index+1), registered.Kind, "registration ordinal %d", index+1)
		require.Equal(t, runtime.ClaudeLifecycleEvent(index+1), runtimeEvent, "runtime ordinal %d", index+1)
		require.Equal(t, registered.NativeName, runtimeEvent.NativeName(), "native event name ordinal %d", index+1)

		runtimeMapping, err := runtimeContract.Mapping(runtimeEvent)
		require.NoError(t, err)
		require.Equal(t, registered.NativeName, runtimeMapping.NativeName(), "mapping native name ordinal %d", index+1)
		require.Len(t, registered.Identities, len(runtimeMapping.Identities()), "identity count ordinal %d", index+1)
		bindings := make([]model.NativeBinding, 0, len(registered.Identities))
		for identityIndex, registeredIdentity := range registered.Identities {
			runtimeIdentity := runtimeMapping.Identities()[identityIndex]
			require.Equal(t, fieldNames[registeredIdentity.Field], runtimeIdentity.NativeName(), "identity native name ordinal %d index %d", index+1, identityIndex)
			require.Equal(t, registeredIdentity.Required, runtimeIdentity.Required(), "identity required flag ordinal %d index %d", index+1, identityIndex)
			require.Equal(t, uint8(registeredIdentity.Binding), uint8(runtimeIdentity.Kind()), "identity kind ordinal %d index %d", index+1, identityIndex)
			bindings = append(bindings, model.NativeBinding{
				Kind:       registeredIdentity.Binding,
				NativeName: fieldNames[registeredIdentity.Field],
				Value:      fmt.Sprintf("catalogue-%d-%d", index, identityIndex),
			})
		}
		bound, identities, err := claudefrontend.Bind(registered.Kind, bindings)
		require.NoError(t, err, "frontend mapping ordinal %d", index+1)
		require.True(t, bound.IsValid(), "frontend L1 ordinal %d", index+1)
		require.Len(t, identities, len(runtimeMapping.Identities()), "frontend identities ordinal %d", index+1)
		for identityIndex, identity := range bound.DeclaredIdentities() {
			runtimeIdentity := runtimeMapping.Identities()[identityIndex]
			require.Equal(t, runtimeIdentity.NativeName(), identity.NativeName(), "frontend native name ordinal %d index %d", index+1, identityIndex)
			require.Equal(t, runtimeIdentity.Kind(), identity.Kind(), "frontend kind ordinal %d index %d", index+1, identityIndex)
			require.Equal(t, runtimeIdentity.Required(), identity.Required(), "frontend required flag ordinal %d index %d", index+1, identityIndex)
		}
		boundEvent, err := bound.NewEvent(identities)
		require.NoError(t, err, "frontend NewEvent ordinal %d", index+1)
		require.True(t, boundEvent.IsValid(), "frontend L2 ordinal %d", index+1)
		require.Equal(t, runtimeContract.ID(), boundEvent.Origin().Contract(), "frontend origin contract ordinal %d", index+1)
		require.Equal(t, registered.NativeName, string(boundEvent.Origin().NativeEventName()), "frontend origin native event ordinal %d", index+1)
	}
}

type captureCorpus struct {
	Cases []captureCase `yaml:"cases"`
}

type captureCase struct {
	Name           string                `yaml:"name"`
	Input          captureInput          `yaml:"input"`
	Expected       captureExpected       `yaml:"expected"`
	Classification string                `yaml:"classification"`
	Provenance     captureCaseProvenance `yaml:"provenance"`
	Mutation       captureMutation       `yaml:"mutation"`
}

type captureInput struct {
	Fixture string `yaml:"fixture"`
}

type captureExpected struct {
	Decision string `yaml:"decision"`
	Reason   string `yaml:"reason"`
}

type captureCaseProvenance struct {
	Source string `yaml:"source"`
	Ref    string `yaml:"ref"`
}

type captureMutation struct {
	Description string `yaml:"description"`
}

// TestCaptureCorpusShapeFollowsTheTargetTable reads the committed corpus file
// as data and requires its shape to be the one the product derives: the
// must-pass cases in target-declaration order, then the required controls, each
// must-pass fixture carrying a sidecar for the event it stands for.
func TestCaptureCorpusShapeFollowsTheTargetTable(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/captures.yaml")
	require.NoError(t, err)
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	var corpus captureCorpus
	require.NoError(t, decoder.Decode(&corpus))
	want := expectedCorpusCases(t)
	require.Len(t, corpus.Cases, len(want), "one must-pass case per declared target plus the required controls")
	for index, w := range want {
		got := corpus.Cases[index]
		require.Equal(t, w.name, got.Name, "corpus case %d name", index)
		require.Equal(t, w.classification.String(), got.Classification, "corpus case %d classification", index)
		require.Equal(t, w.decision.String(), got.Expected.Decision, "corpus case %d decision", index)
		if w.reason == activation.CorpusReasonNone {
			require.Empty(t, got.Expected.Reason, "corpus case %d reason", index)
		} else {
			require.Equal(t, w.reason.String(), got.Expected.Reason, "corpus case %d reason", index)
		}
		if w.classification == activation.ClassificationMustPass {
			sidecar, err := os.ReadFile(filepath.Join("testdata", activation.ProvenancePath(got.Input.Fixture)))
			require.NoError(t, err, "must-pass case %q has a provenance sidecar", got.Name)
			var record acceptance.CaptureProvenance
			require.NoError(t, json.Unmarshal(sidecar, &record))
			require.Equal(t, "authentic-"+kebabCase(record.Event), got.Name, "the case is named after the event its fixture recorded")
		}
	}

	wantReasons := map[string]struct{}{
		"non-authentic-origin": {},
		"digest-mismatch":      {},
		"version-out-of-range": {},
		"path-escape":          {},
	}
	wantSources := map[string]struct{}{
		"requirement": {},
		"bug":         {},
		"enum":        {},
		"boundary":    {},
		"manual":      {},
	}
	seenReasons := make(map[string]struct{}, len(wantReasons))
	seenNames := make(map[string]struct{}, len(corpus.Cases))
	passCount, failCount := 0, 0
	for _, testCase := range corpus.Cases {
		require.NotEmpty(t, testCase.Name)
		require.NotEmpty(t, testCase.Input.Fixture)
		require.False(t, filepath.IsAbs(testCase.Input.Fixture), "fixture paths must be relative to testdata")
		require.NotEmpty(t, testCase.Provenance.Source)
		require.NotEmpty(t, testCase.Provenance.Ref)
		require.NotEmpty(t, testCase.Mutation.Description)
		require.Contains(t, wantSources, testCase.Provenance.Source)
		_, duplicate := seenNames[testCase.Name]
		require.False(t, duplicate, "corpus case names must be unique")
		seenNames[testCase.Name] = struct{}{}

		switch testCase.Classification {
		case "must-pass":
			passCount++
			require.Equal(t, "enabled", testCase.Expected.Decision)
			require.Empty(t, testCase.Expected.Reason)
		case "must-fail":
			failCount++
			require.Equal(t, "withheld", testCase.Expected.Decision)
			require.Contains(t, wantReasons, testCase.Expected.Reason)
			_, duplicate = seenReasons[testCase.Expected.Reason]
			require.False(t, duplicate, "each closed withheld reason must have one case")
			seenReasons[testCase.Expected.Reason] = struct{}{}
		default:
			t.Fatalf("case %q has unknown classification %q", testCase.Name, testCase.Classification)
		}
	}
	require.Equal(t, len(enabledClaudeTargets(t)), passCount, "one must-pass case per enabled target")
	require.Equal(t, len(requiredControls), failCount, "one control per required withheld reason")
	require.Equal(t, wantReasons, seenReasons)
}

func TestAuthenticProvenanceValidatesUnchangedFixture(t *testing.T) {
	t.Parallel()

	provenanceBytes, err := os.ReadFile("testdata/fixtures/session_start_2_1_210.provenance.json")
	require.NoError(t, err)
	raw, err := os.ReadFile("testdata/fixtures/session_start_2_1_210.json")
	require.NoError(t, err)
	require.Equal(t, canonicalAuthenticFixtureDigest, digest.FromBytes(raw).String())
	var provenanceRecord acceptance.CaptureProvenance
	// Decode through encoding/json without DisallowUnknownFields: redaction and
	// event are intentional provenance metadata outside CaptureProvenance.
	require.NoError(t, json.Unmarshal(provenanceBytes, &provenanceRecord))
	require.Equal(t, canonicalAuthenticFixtureDigest, provenanceRecord.RawFileDigest)
	require.NoError(t, provenanceRecord.ValidateFixture("testdata", "fixtures/session_start_2_1_210.json"))
}

func TestCaptureCorpusMetadataControlsHaveSiblingProvenance(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/captures.yaml")
	require.NoError(t, err)
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	var corpus captureCorpus
	require.NoError(t, decoder.Decode(&corpus))
	for _, testCase := range corpus.Cases {
		if strings.HasPrefix(filepath.Clean(testCase.Input.Fixture), "..") {
			_, err := os.Stat(filepath.Join("testdata", testCase.Input.Fixture))
			require.Error(t, err, "path-escape control must not add a fake host capture")
			continue
		}

		provenancePath := strings.TrimSuffix(testCase.Input.Fixture, ".json") + ".provenance.json"
		provenanceBytes, err := os.ReadFile(filepath.Join("testdata", provenancePath))
		require.NoError(t, err, "sibling provenance for %s", testCase.Input.Fixture)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(provenanceBytes, &fields))
		require.Len(t, fields, 8, "provenance for %s", testCase.Input.Fixture)
		for _, field := range []string{"origin", "harness", "harnessVersion", "captureSource", "rawFileDigest", "capturedAt", "redaction", "event"} {
			_, present := fields[field]
			require.True(t, present, "provenance %s has %s", provenancePath, field)
		}

		var provenanceRecord acceptance.CaptureProvenance
		require.NoError(t, json.Unmarshal(provenanceBytes, &provenanceRecord))
		switch testCase.Expected.Reason {
		case "":
			require.Equal(t, acceptance.OriginAuthenticCapture, provenanceRecord.Origin)
		case "non-authentic-origin":
			require.Equal(t, acceptance.OriginAuthored, provenanceRecord.Origin)
		case "digest-mismatch":
			require.NotEqual(t, canonicalAuthenticFixtureDigest, provenanceRecord.RawFileDigest)
		case "version-out-of-range":
			// The control sits exactly one patch release below the floor the
			// evaluator admits, so it proves the floor is exclusive there and it
			// follows the contract when the contract moves.
			controlVersion, err := runtime.ParseHostVersion(provenanceRecord.HarnessVersion)
			require.NoError(t, err)
			admission := activation.ClaudeCodeEvaluator().Admission()
			require.False(t, admission.Allows(controlVersion), "the control version must be refused by the contract")
			require.Equal(t, testutil.BelowFloor(t, admission.Min()).String(), provenanceRecord.HarnessVersion, "the control is the floor minus one patch release")
		default:
			t.Fatalf("unexpected corpus reason %q", testCase.Expected.Reason)
		}
	}
}

func TestDerivedCorpusFixturesAreByteIdenticalMetadataControls(t *testing.T) {
	t.Parallel()

	base, err := os.ReadFile("testdata/fixtures/session_start_2_1_210.json")
	require.NoError(t, err)
	for _, name := range []string{
		"session_start_2_1_210_origin_authored.json",
		"session_start_2_1_210_digest_mismatch.json",
		"session_start_2_1_210_version_out_of_range.json",
	} {
		derived, err := os.ReadFile(filepath.Join("testdata/fixtures", name))
		require.NoError(t, err)
		require.Equal(t, base, derived, "derived control %s must not claim a new host capture", name)
	}
}

func TestCaptureScriptEmitsMatchingDeterministicSiblings(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is unavailable in the development shell")
	}

	out := t.TempDir()
	home := "/home/capture-user"
	script := filepath.Join("..", "..", "..", "..", "tools", "capture-claude-hook.sh")
	body := `{"session_id":"session-1","cwd":"/home/capture-user/project","hook_event_name":"SessionStart"}`
	command := exec.Command("bash", script)
	command.Env = append(os.Environ(),
		"PASTURE_CAPTURE_DIR="+out,
		"HOME="+home,
		"CLAUDE_CODE_VERSION=2.1.220",
	)
	command.Stdin = strings.NewReader(body)
	require.NoError(t, command.Run())

	fixture := filepath.Join(out, "session_start_2_1_220.json")
	provenance := filepath.Join(out, "session_start_2_1_220.provenance.json")
	fixtureBytes, err := os.ReadFile(fixture)
	require.NoError(t, err)
	provenanceBytes, err := os.ReadFile(provenance)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(out, "SessionStart.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.True(t, json.Valid(fixtureBytes))
	require.Contains(t, string(fixtureBytes), `"cwd":"/home/user/project"`)
	var provenanceFields map[string]any
	require.NoError(t, json.Unmarshal(provenanceBytes, &provenanceFields))
	require.Equal(t, "2.1.220", provenanceFields["harnessVersion"])
}

func isCommonField(name string) bool {
	for _, common := range []string{"session_id", "transcript_path", "cwd", "permission_mode", "hook_event_name", "effort", "agent_id", "agent_type", "prompt_id"} {
		if name == common {
			return true
		}
	}
	return false
}

func behaviorName(event registration.Event) string {
	if event.NativeName == "ElicitationResult" {
		return "human-response"
	}
	if event.StopLoop == registration.StopLoopConsultWhenInactive {
		return "stop-gate"
	}
	if event.Blocking == registration.ConditionallyBlocking {
		return "conditional-gate"
	}
	if event.Blocking == registration.NonBlocking {
		return "observation"
	}
	return "gate"
}
