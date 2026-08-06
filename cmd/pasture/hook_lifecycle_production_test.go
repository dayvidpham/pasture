package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/nativeresponse"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
)

func TestEnabledOpenCodeHandlersToDurableReadBack(t *testing.T) {
	bun, err := exec.LookPath("bun")
	require.NoError(t, err, "Bun is required for the generated OpenCode production proof; enter the flake dev shell")
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	moduleURL := (&url.URL{Scheme: "file", Path: filepath.Join(root, filepath.FromSlash(codegen.OpenCodeHooksModulePath))}).String()
	fixtureDir := filepath.Join(root, "internal", "lifecycle", "ingress", "opencode", "testdata", "fixtures")
	runner := filepath.Join(dir, "enabled-handlers.ts")
	script := fmt.Sprintf(`
import PastureLifecycle from %q;
const sessionCapture = await Bun.file(%q).json();
const toolCapture = await Bun.file(%q).json();
const plugin = await PastureLifecycle({ client: {} });
await plugin.event(sessionCapture.value);
const output = toolCapture.value.output;
const before = JSON.stringify(output.args);
await plugin["tool.execute.before"](toolCapture.value.input, output);
if (JSON.stringify(output.args) !== before) throw new Error("enabled generated handler changed output.args");
console.log(JSON.stringify({argsUnchanged: true}));
`, moduleURL,
		filepath.Join(fixtureDir, "session_created_1_18_10.capture.json"),
		filepath.Join(fixtureDir, "tool_execute_before_1_18_10.capture.json"))
	require.NoError(t, os.WriteFile(runner, []byte(script), 0o600))
	command := exec.Command(bun, runner)
	command.Env = append(os.Environ(), "PASTURE_BIN="+binary, "PASTURE_DB_PATH="+dbPath)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Equal(t, `{"argsUnchanged":true}`, strings.TrimSpace(string(output)))

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	pageSize, err := model.NewPageSize(2)
	require.NoError(t, err)
	page, err := reader.Records(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: pageSize}})
	require.NoError(t, err)
	require.Len(t, page.Records(), 2)

	semantics := make(map[model.ContractEventKind]runtime.EventSemantic, 2)
	identities := make(map[model.ContractEventKind]map[runtime.NativeIdentityKind]string, 2)
	for _, record := range page.Records() {
		require.Equal(t, registration.OpenCode1_18_10().Contract, record.Occurrence.RuntimeContract)
		require.Equal(t, "1.18.10", record.Occurrence.Envelope.HostVersion)
		require.Len(t, record.Interpreted(), 1)
		interpreted := record.Interpreted()[0]
		require.Equal(t, runtime.OpenCode1_18_10().ID(), interpreted.Contract())
		semantics[record.Occurrence.Kind] = interpreted.Semantic()
		identities[record.Occurrence.Kind] = make(map[runtime.NativeIdentityKind]string)
		for _, identity := range interpreted.Identities() {
			identities[record.Occurrence.Kind][identity.Kind] = identity.Value
		}
	}
	require.Equal(t, runtime.SemanticObservation, semantics[registration.EventOpenCodeSessionCreated])
	require.Equal(t, runtime.SemanticGateConsultation, semantics[registration.EventOpenCodeToolExecuteBefore])
	t.Run("session.created", func(t *testing.T) {
		require.Equal(t, runtime.SemanticObservation, semantics[registration.EventOpenCodeSessionCreated])
		require.Equal(t, "ses_038a9e08dffewOKvezW94jg2BO", identities[registration.EventOpenCodeSessionCreated][runtime.IdentitySession])
	})
	t.Run("tool.execute.before", func(t *testing.T) {
		require.Equal(t, runtime.SemanticGateConsultation, semantics[registration.EventOpenCodeToolExecuteBefore])
		require.Equal(t, "ses_038a9e08dffewOKvezW94jg2BO", identities[registration.EventOpenCodeToolExecuteBefore][runtime.IdentitySession])
		require.Equal(t, "call_t7cziiGDQdypG92fXwcgWBUf", identities[registration.EventOpenCodeToolExecuteBefore][runtime.IdentityToolCall])
	})

	interpretedRows := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
	consultationRows := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)
	require.Len(t, interpretedRows, 2)
	require.Len(t, consultationRows, 1)
	consultation := consultationRows[0]
	var gateInterpreted provenance.EvidenceRow
	for _, row := range interpretedRows {
		if row.ProducingOperationID == consultation.ProducingOperationID {
			gateInterpreted = row
		}
	}
	require.NotZero(t, gateInterpreted.JournalID)
	require.Equal(t, gateInterpreted.ProducingOperationJournalID, consultation.ProducingOperationJournalID)
	require.Less(t, gateInterpreted.JournalID, consultation.JournalID, "one durable gate operation must order interpreted before consultation evidence")
	require.Contains(t, string(consultation.Payload), `"response":{"decision":"proceed"}`)

	// Claude is deliberately non-live regression evidence here. Compare only the
	// shared gate semantic, blocking mode, and canonical Proceed decision.
	openCodeGate, err := runtime.OpenCode1_18_10Lifecycle().Mapping(runtime.OpenCodeEventToolExecuteBefore)
	require.NoError(t, err)
	claudeGate, err := runtime.ClaudeCode2_1_210Lifecycle().Mapping(runtime.ClaudeEventPreToolUse)
	require.NoError(t, err)
	require.Equal(t, claudeGate.Semantic(), openCodeGate.Semantic())
	require.Equal(t, claudeGate.Blocking(), openCodeGate.Blocking())
	require.NotEqual(t, runtime.ClaudeCode2_1_210().ID(), runtime.OpenCode1_18_10().ID())
}

const (
	occurrenceLifecycleContract   = "claude-code/2.1.210"
	interpretedLifecycleContract  = "claude-code/claude-code@2.1.210"
	occurrenceEvidenceKind        = provenance.EvidenceKind("pasture.lifecycle.occurrence.v1")
	interpretedEvidenceKind       = provenance.EvidenceKind("pasture.lifecycle.interpreted.v1")
	consultationEvidenceKind      = provenance.EvidenceKind("pasture.lifecycle.consultation.v1")
	expectedSessionIdentity       = "3696b790-3973-49f2-b156-9d82146bf7ec"
	expectedInterpretedIdentities = `[{"kind":1,"value":"3696b790-3973-49f2-b156-9d82146bf7ec"}]`
)

var expectedEnabledClaudeEvents = []model.ContractEventKind{
	registration.EventSessionStart,
	registration.EventSessionEnd,
	registration.EventPreToolUse,
	registration.EventPostToolUse,
	registration.EventPostToolUseFailure,
	registration.EventPostToolBatch,
	registration.EventPreCompact,
	registration.EventPostCompact,
}

type claudeProductionFixture struct {
	name       string
	fixture    string
	event      model.ContractEventKind
	bindings   []lifecycleBindingPayload
	semantic   runtime.EventSemantic
	identities []interpretedIdentityPayload
	unresolved []interpretedUnresolvedPayload
	blocking   bool
}

var claudeProductionFixtures = []claudeProductionFixture{
	{
		name: "SessionStart", fixture: "session_start_2_1_222.json", event: registration.EventSessionStart,
		bindings:   []lifecycleBindingPayload{{Kind: model.BindingSession, NativeName: "session_id", Value: "3696b790-3973-49f2-b156-9d82146bf7ec"}},
		semantic:   runtime.SemanticObservation,
		identities: []interpretedIdentityPayload{{Kind: uint8(runtime.IdentitySession), Value: "3696b790-3973-49f2-b156-9d82146bf7ec"}},
	},
	{
		name: "SessionEnd", fixture: "session_end_2_1_222.json", event: registration.EventSessionEnd,
		bindings:   []lifecycleBindingPayload{{Kind: model.BindingSession, NativeName: "session_id", Value: "dc64d2f6-0d5e-4e2a-b880-49c980a6c750"}},
		semantic:   runtime.SemanticObservation,
		identities: []interpretedIdentityPayload{{Kind: uint8(runtime.IdentitySession), Value: "dc64d2f6-0d5e-4e2a-b880-49c980a6c750"}},
	},
	{
		name: "PreToolUse", fixture: "pre_tool_use_2_1_222.json", event: registration.EventPreToolUse,
		bindings: []lifecycleBindingPayload{
			{Kind: model.BindingSession, NativeName: "session_id", Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"},
			{Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: "toolu_01BZyDFUsmM5YK5u8ZRkrvcE"},
		},
		semantic: runtime.SemanticGateConsultation,
		identities: []interpretedIdentityPayload{
			{Kind: uint8(runtime.IdentitySession), Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"},
			{Kind: uint8(runtime.IdentityToolCall), Value: "toolu_01BZyDFUsmM5YK5u8ZRkrvcE"},
		},
		blocking: true,
	},
	{
		name: "PostToolUse", fixture: "post_tool_use_2_1_222.json", event: registration.EventPostToolUse,
		bindings: []lifecycleBindingPayload{
			{Kind: model.BindingSession, NativeName: "session_id", Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"},
			{Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: "toolu_01G65GiPDVnmxtRrYWq9aZaP"},
		},
		semantic: runtime.SemanticObservation,
		identities: []interpretedIdentityPayload{
			{Kind: uint8(runtime.IdentitySession), Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"},
			{Kind: uint8(runtime.IdentityToolCall), Value: "toolu_01G65GiPDVnmxtRrYWq9aZaP"},
		},
	},
	{
		name: "PostToolUseFailure", fixture: "post_tool_use_failure_2_1_222.json", event: registration.EventPostToolUseFailure,
		bindings: []lifecycleBindingPayload{
			{Kind: model.BindingSession, NativeName: "session_id", Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"},
			{Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: "toolu_01BZyDFUsmM5YK5u8ZRkrvcE"},
		},
		semantic: runtime.SemanticObservation,
		identities: []interpretedIdentityPayload{
			{Kind: uint8(runtime.IdentitySession), Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"},
			{Kind: uint8(runtime.IdentityToolCall), Value: "toolu_01BZyDFUsmM5YK5u8ZRkrvcE"},
		},
	},
	{
		name: "PostToolBatch", fixture: "post_tool_batch_2_1_222.json", event: registration.EventPostToolBatch,
		bindings:   []lifecycleBindingPayload{{Kind: model.BindingSession, NativeName: "session_id", Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"}},
		semantic:   runtime.SemanticGateConsultation,
		identities: []interpretedIdentityPayload{{Kind: uint8(runtime.IdentitySession), Value: "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"}},
		unresolved: []interpretedUnresolvedPayload{{Reason: uint8(waist.UnresolvedToolCall)}},
		blocking:   true,
	},
	{
		name: "PreCompact", fixture: "pre_compact_2_1_222.json", event: registration.EventPreCompact,
		bindings:   []lifecycleBindingPayload{{Kind: model.BindingSession, NativeName: "session_id", Value: "2c9a0e31-7f70-4a54-b44a-264444df74a1"}},
		semantic:   runtime.SemanticGateConsultation,
		identities: []interpretedIdentityPayload{{Kind: uint8(runtime.IdentitySession), Value: "2c9a0e31-7f70-4a54-b44a-264444df74a1"}},
		blocking:   true,
	},
	{
		name: "PostCompact", fixture: "post_compact_2_1_222.json", event: registration.EventPostCompact,
		bindings:   []lifecycleBindingPayload{{Kind: model.BindingSession, NativeName: "session_id", Value: "2c9a0e31-7f70-4a54-b44a-264444df74a1"}},
		semantic:   runtime.SemanticObservation,
		identities: []interpretedIdentityPayload{{Kind: uint8(runtime.IdentitySession), Value: "2c9a0e31-7f70-4a54-b44a-264444df74a1"}},
	},
}

func TestEnabledClaudeEventToOccurrenceAndInterpretedEvidence(t *testing.T) {
	manifest, err := activation.ClaudeCode2_1_210()
	require.NoError(t, err)
	enabled := make([]model.ContractEventKind, 0, len(expectedEnabledClaudeEvents))
	for _, entry := range manifest {
		if entry.State == activation.Enabled {
			enabled = append(enabled, entry.Event)
		}
	}
	require.Equal(t, expectedEnabledClaudeEvents, enabled, "literal production rows must equal the complete static enabled set")

	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())

	initializeLifecycleTestDatabase(t, dbPath)

	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_222.json"))
	require.NoError(t, err)
	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.222")
	command.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	require.NoError(t, command.Run(), stdout.String()+stderr.String())
	require.Empty(t, stderr.String())

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()

	occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	require.Len(t, occurrences, 1)
	occurrence := occurrences[0]
	occurrencePayload := assertOccurrencePayload(t, occurrence.Payload, raw, model.CaptureValid, registration.EventSessionStart)

	interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
	require.Len(t, interpreted, 1)
	assertInterpretedEvidence(t, interpreted[0].Payload)
	assertSharedOperation(t, occurrence, interpreted[0])

	require.Equal(t, registration.EventSessionStart, occurrencePayload.Event)
	require.Equal(t, "2.1.222", occurrencePayload.Envelope.HostVersion)
	// The occurrence-side binding is retained independently from the waist
	// identity assertion above; it is not a substitute for interpreted evidence.
	require.Len(t, occurrencePayload.Bindings, 1)
	require.Equal(t, model.BindingSession, occurrencePayload.Bindings[0].Kind)
	require.Equal(t, "session_id", occurrencePayload.Bindings[0].NativeName)
	require.Equal(t, expectedSessionIdentity, occurrencePayload.Bindings[0].Value)
	require.NoError(t, tracker.Close())

	list := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json", "--binding", "session:session_id="+expectedSessionIdentity)
	stdout.Reset()
	stderr.Reset()
	list.Stdout = &stdout
	list.Stderr = &stderr
	require.NoError(t, list.Run(), stdout.String()+stderr.String())
	require.Empty(t, stderr.String())
	require.Contains(t, stdout.String(), `"registrationContract":"`+occurrenceLifecycleContract+`"`)
	require.Contains(t, stdout.String(), `"contract":"`+interpretedLifecycleContract+`"`)
	require.Contains(t, stdout.String(), `"interpreted":[`)
	require.NotContains(t, stdout.String(), string(raw))
	require.NotContains(t, stdout.String(), dbPath)

	ingestAgain := func(payload []byte) {
		cmd := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.222")
		cmd.Stdin = bytes.NewReader(payload)
		require.NoError(t, cmd.Run())
	}
	ingestAgain(raw)
	ingestAgain([]byte(`{"session_id":`))
	ingestAgain(raw)
	tracker, err = tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	duplicateOccurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	duplicateInterpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
	require.Len(t, duplicateOccurrences, 4)
	require.Len(t, duplicateInterpreted, 3)
	operationIDs := make(map[string]struct{}, len(duplicateOccurrences))
	journalIDs := make(map[provenance.JournalID]struct{}, len(duplicateOccurrences))
	occurrenceByOperation := make(map[string]provenance.EvidenceRow, len(duplicateOccurrences))
	for _, row := range duplicateOccurrences {
		operationID := string(row.ProducingOperationID)
		operationIDs[operationID] = struct{}{}
		journalIDs[row.JournalID] = struct{}{}
		occurrenceByOperation[operationID] = row
	}
	require.Len(t, operationIDs, 4, "byte-identical deliveries need distinct operation identities")
	require.Len(t, journalIDs, 4, "byte-identical deliveries need distinct occurrence records")
	interpretedOperations := make(map[string]struct{}, len(duplicateInterpreted))
	for _, interpretedRow := range duplicateInterpreted {
		operationID := string(interpretedRow.ProducingOperationID)
		occurrenceRow, found := occurrenceByOperation[operationID]
		require.True(t, found, "every interpreted row must share a producing operation with its occurrence")
		require.Equal(t, occurrenceRow.ProducingOperationJournalID, interpretedRow.ProducingOperationJournalID, "occurrence and interpreted evidence must share one operation journal identity")
		interpretedOperations[operationID] = struct{}{}
	}
	require.Len(t, interpretedOperations, 3, "each valid delivery needs one interpreted operation while the malformed occurrence remains occurrence-only")
	require.NoError(t, tracker.Close())
	pageOne := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json", "--page-size", "1", "--binding", "session:session_id="+expectedSessionIdentity)
	stdout.Reset()
	stderr.Reset()
	pageOne.Stdout = &stdout
	pageOne.Stderr = &stderr
	require.NoError(t, pageOne.Run(), stderr.String())
	var firstPage struct {
		Items []json.RawMessage `json:"items"`
		Next  string            `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &firstPage))
	require.Len(t, firstPage.Items, 1)
	require.NotEmpty(t, firstPage.Next)
	ingestAgain(raw)
	continuation := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json", "--page-size", "10", "--cursor", firstPage.Next, "--binding", "session:session_id="+expectedSessionIdentity, "--binding", "session:session_id="+expectedSessionIdentity)
	stdout.Reset()
	stderr.Reset()
	continuation.Stdout = &stdout
	continuation.Stderr = &stderr
	require.NoError(t, continuation.Run(), stderr.String())
	var rest struct {
		Items []json.RawMessage `json:"items"`
		Next  string            `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &rest))
	require.Len(t, rest.Items, 2, "continuation must exclude malformed nonmatch and post-snapshot append")
	require.Empty(t, rest.Next)
	changed := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json", "--cursor", firstPage.Next, "--binding", "session:session_id=changed")
	err = changed.Run()
	require.Error(t, err)
	require.Equal(t, 1, changed.ProcessState.ExitCode())
}

func TestEnabledClaudeAuthenticFixturesToDurableEvidence(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)

	for _, testCase := range claudeProductionFixtures {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
			initializeLifecycleTestDatabase(t, dbPath)
			raw := readProductionClaudeFixture(t, testCase.fixture, testCase.name)

			command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", testCase.name, "--host-version", "2.1.222")
			command.Stdin = bytes.NewReader(raw)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			require.NoError(t, command.Run(), stdout.String()+stderr.String())
			require.Empty(t, stderr.String())
			if testCase.blocking {
				require.JSONEq(t, `{"decision":"proceed"}`, stdout.String())
			} else {
				require.Empty(t, stdout.String())
			}

			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
			interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
			consultations := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)
			require.Len(t, occurrences, 1)
			require.Len(t, interpreted, 1)
			if testCase.blocking {
				require.Len(t, consultations, 1)
			} else {
				require.Empty(t, consultations)
			}

			occurrencePayload := decodeOccurrencePayload(t, occurrences[0].Payload)
			require.Equal(t, occurrenceLifecycleContract, occurrencePayload.Contract)
			require.Equal(t, testCase.event, occurrencePayload.Event)
			require.Equal(t, occurrenceLifecycleContract, occurrencePayload.Envelope.Runtime.Contract.String())
			require.Equal(t, "2.1.222", occurrencePayload.Envelope.HostVersion)
			require.Equal(t, model.CaptureValid, occurrencePayload.Capture)
			require.Equal(t, digest.FromBytes(raw).String(), occurrencePayload.Body)
			require.Equal(t, testCase.bindings, occurrencePayload.Bindings)

			interpretedPayload := decodeInterpretedPayload(t, interpreted[0].Payload)
			require.Equal(t, uint8(testCase.semantic), interpretedPayload.Semantic)
			require.Equal(t, testCase.identities, interpretedPayload.Identities)
			require.ElementsMatch(t, testCase.unresolved, interpretedPayload.UnresolvedFacts)
			require.Equal(t, interpretedLifecycleContract, interpretedPayload.Contract)
			assertSharedOperation(t, occurrences[0], interpreted[0])
			require.Less(t, occurrences[0].JournalID, interpreted[0].JournalID)
			if testCase.blocking {
				require.Equal(t, interpreted[0].ProducingOperationID, consultations[0].ProducingOperationID)
				require.Equal(t, interpreted[0].ProducingOperationJournalID, consultations[0].ProducingOperationJournalID)
				require.Less(t, interpreted[0].JournalID, consultations[0].JournalID)
				assertProceedConsultation(t, consultations[0].Payload)
			}
			require.NoError(t, tracker.Close())

			binding := testCase.bindings[0]
			list := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json", "--binding", binding.Kind.String()+":"+binding.NativeName+"="+binding.Value)
			stdout.Reset()
			stderr.Reset()
			list.Stdout = &stdout
			list.Stderr = &stderr
			require.NoError(t, list.Run(), stdout.String()+stderr.String())
			require.Empty(t, stderr.String())
			var page struct {
				Items []struct {
					Event                model.ContractEventKind  `json:"event"`
					RegistrationContract string                   `json:"registrationContract"`
					Capture              model.CaptureDisposition `json:"capture"`
					PayloadDigest        string                   `json:"payloadDigest"`
					Interpreted          []struct {
						Semantic   runtime.EventSemantic    `json:"semantic"`
						Identities []waist.SemanticIdentity `json:"identities"`
						Unresolved []waist.UnresolvedFact   `json:"unresolved"`
						Contract   string                   `json:"contract"`
					} `json:"interpreted"`
				} `json:"items"`
			}
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &page))
			require.Len(t, page.Items, 1)
			require.Equal(t, testCase.event, page.Items[0].Event)
			require.Equal(t, occurrenceLifecycleContract, page.Items[0].RegistrationContract)
			require.Equal(t, model.CaptureValid, page.Items[0].Capture)
			require.Equal(t, digest.FromBytes(raw).String(), page.Items[0].PayloadDigest)
			require.Len(t, page.Items[0].Interpreted, 1)
			require.Equal(t, testCase.semantic, page.Items[0].Interpreted[0].Semantic)
			require.Equal(t, testCase.identities, publicIdentityPayloads(page.Items[0].Interpreted[0].Identities))
			require.ElementsMatch(t, testCase.unresolved, publicUnresolvedPayloads(page.Items[0].Interpreted[0].Unresolved))
			require.Equal(t, interpretedLifecycleContract, page.Items[0].Interpreted[0].Contract)
			for _, privateValue := range []string{string(raw), dbPath, "/home/user", "authentic-capture", "tools/capture-claude-hook.sh", "home-path-v1"} {
				require.NotContains(t, stdout.String(), privateValue)
			}
		})
	}
}

func TestClaudePayloadEventCannotOverrideRegisteredCLIEvent(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	authentic := readProductionClaudeFixture(t, "session_start_2_1_222.json", "SessionStart")
	raw := bytes.Replace(authentic, []byte(`"hook_event_name":"SessionStart"`), []byte(`"hook_event_name":"SessionEnd"`), 1)
	require.NotEqual(t, authentic, raw, "the negative control must change only the payload's event claim")

	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.222")
	command.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	require.NoError(t, command.Run(), stdout.String()+stderr.String())
	require.Empty(t, stdout.String())
	require.Empty(t, stderr.String())

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	require.Len(t, occurrences, 1)
	payload := decodeOccurrencePayload(t, occurrences[0].Payload)
	require.Equal(t, registration.EventSessionStart, payload.Event, "the generated CLI coordinate remains authoritative")
	require.Equal(t, model.CaptureEventMismatch, payload.Capture)
	require.Empty(t, payload.Bindings)
	require.Equal(t, digest.FromBytes(raw).String(), payload.Body)
	require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind))
	require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind))
	require.NoError(t, tracker.Close())

	list := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json")
	stdout.Reset()
	stderr.Reset()
	list.Stdout = &stdout
	list.Stderr = &stderr
	require.NoError(t, list.Run(), stdout.String()+stderr.String())
	require.Empty(t, stderr.String())
	require.Contains(t, stdout.String(), `"capture":8`)
	require.Contains(t, stdout.String(), `"interpreted":[]`)
	require.NotContains(t, stdout.String(), string(raw))
}

func TestWithheldClaudeElicitationIsNotAdmittedByBuiltCLI(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	cases := []struct{ event, fixture string }{
		{event: "Elicitation", fixture: "elicitation_2_1_222.json"},
		{event: "ElicitationResult", fixture: "elicitation_result_2_1_222.json"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.event, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())
			raw := readProductionClaudeFixture(t, testCase.fixture, testCase.event)
			command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", testCase.event, "--host-version", "2.1.222")
			command.Stdin = bytes.NewReader(raw)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			require.NoError(t, command.Run(), stdout.String()+stderr.String())
			require.Empty(t, stdout.String(), "withheld elicitation must emit no host response")
			require.Contains(t, stderr.String(), `Claude event "`+testCase.event+`" is withheld (reason missing-request-correlation)`)
			require.NotContains(t, stderr.String(), "capture-approved")
			_, statErr := os.Stat(dbPath)
			require.ErrorIs(t, statErr, os.ErrNotExist, "withheld elicitation must create no durable or public authority")
		})
	}
}

func TestMalformedClaudeEventToOccurrenceOnly(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	raw := []byte(`{"session_id":`)
	for _, testCase := range []struct {
		name  string
		event model.ContractEventKind
	}{
		{name: "SessionStart", event: registration.EventSessionStart},
		{name: "PreToolUse", event: registration.EventPreToolUse},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
			initializeLifecycleTestDatabase(t, dbPath)
			command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", testCase.name, "--host-version", "2.1.222")
			command.Stdin = bytes.NewReader(raw)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			require.NoError(t, command.Run(), stdout.String()+stderr.String())
			require.Empty(t, stdout.String(), "malformed lifecycle input must never emit a host decision")
			require.Empty(t, stderr.String())

			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
			require.Len(t, occurrences, 1)
			occurrencePayload := assertOccurrencePayload(t, occurrences[0].Payload, raw, model.CaptureMalformed, testCase.event)
			require.Empty(t, occurrencePayload.Bindings)
			require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind))
			require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind))
			require.NoError(t, tracker.Close())

			list := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json")
			var output bytes.Buffer
			list.Stdout = &output
			require.NoError(t, list.Run())
			require.Contains(t, output.String(), `"interpreted":[]`)
		})
	}
}

func TestLifecycleLeafFaultsExitZeroAndReport(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	fixture, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_210.json"))
	require.NoError(t, err)
	base := []string{databaseFlagName.Argument(), filepath.Join(dir, tasks.DefaultDBFilename.String()), "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.220"}
	databaseDirectory := filepath.Join(dir, "database-directory")
	require.NoError(t, os.Mkdir(databaseDirectory, 0o700))
	cases := []struct {
		name string
		args []string
		in   []byte
		want string
	}{
		{name: "unwritable database", args: append([]string{databaseFlagName.Argument(), databaseDirectory}, base[2:]...), in: fixture, want: "open"},
		{name: "missing flag", args: []string{databaseFlagName.Argument(), filepath.Join(dir, "missing.db"), "hook", "lifecycle", "--harness", "claude-code", "--host-version", "2.1.220"}, in: fixture, want: "not in the generated Claude registration"},
		{name: "unknown flag", args: append(append([]string(nil), base...), "--unknown-lifecycle-flag"), in: fixture, want: "flag error"},
		{name: "extra positional", args: append(append([]string(nil), base...), "unexpected"), in: fixture, want: "unexpected positional arguments"},
		{name: "oversized payload", args: base, in: []byte(strings.Repeat("x", model.MaxNativePayloadBytes+1)), want: "exceeds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			command := exec.Command(binary, tc.args...)
			command.Stdin = bytes.NewReader(tc.in)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			require.NoError(t, command.Run(), stderr.String())
			require.Contains(t, stderr.String(), tc.want)
		})
	}

	list := exec.Command(binary, databaseFlagName.Argument(), filepath.Join(dir, "list.db"), "hook", "lifecycle", "list", "--unknown-list-flag")
	err = list.Run()
	require.Error(t, err)
	require.Equal(t, 1, list.ProcessState.ExitCode(), "human list command must retain standard non-zero flag errors")
}

func TestInvalidLifecycleInvocationCreatesNoDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing", tasks.DefaultDBFilename.String())
	err := handlers.HookLifecycle(context.Background(), handlers.HookLifecycleInput{DBPath: dbPath, Harness: "claude-code", Event: "Unknown", HostVersion: "2.1.220", Input: bytes.NewBufferString("{}"), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{}})
	require.Error(t, err)
	_, statErr := os.Stat(dbPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestWithheldOpenCodeEventIsNotAdmittedByBuiltCLI(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "opencode", "--event", "session.updated", "--host-version", "1.18.10")
	command.Stdin = bytes.NewBufferString(`{"event":{"type":"session.updated"}}`)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	require.NoError(t, command.Run(), stderr.String())
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), `OpenCode event "session.updated" is withheld (reason outside-target-set)`)
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind), "withheld direct CLI ingress must persist no occurrence evidence")
	require.NoError(t, tracker.Close())

	list := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json")
	stdout.Reset()
	stderr.Reset()
	list.Stdout = &stdout
	list.Stderr = &stderr
	require.NoError(t, list.Run(), stderr.String())
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &page))
	require.Empty(t, page.Items, "withheld direct CLI ingress must persist no public lifecycle record")
}

func TestLifecycleListRejectsCursorBeforeDatabaseOpen(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, "missing", tasks.DefaultDBFilename.String())
	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--cursor", "not-base64!")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	require.Error(t, err)
	require.Equal(t, 1, command.ProcessState.ExitCode())
	require.Contains(t, stderr.String(), "invalid cursor before database open")
	_, statErr := os.Stat(dbPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestLifecycleListStandardExitCategories(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	connectionPath := filepath.Join(dir, "database-directory")
	storagePath := filepath.Join(dir, "future.db")
	cases := []struct {
		name, path string
		prepare    func()
		want       int
	}{{"connection", connectionPath, func() { require.NoError(t, os.Mkdir(connectionPath, 0o700)) }, 2}, {"storage", storagePath, func() {
		initializeLifecycleTestDatabase(t, storagePath)
		db, err := sql.Open("sqlite", storagePath)
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM audit_schema_meta; INSERT INTO audit_schema_meta(version,applied_at) VALUES(8,1)`)
		require.NoError(t, err)
		require.NoError(t, db.Close())
	}, 5}}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tc.prepare()
			command := exec.Command(binary, databaseFlagName.Argument(), tc.path, "hook", "lifecycle", "list")
			err := command.Run()
			require.Error(t, err)
			require.Equal(t, tc.want, command.ProcessState.ExitCode())
		})
	}
}

func TestLifecycleProjectionRebuildOccurrenceAndBindingsAreAtomic(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_210.json"))
	require.NoError(t, err)
	ingest := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.220")
	ingest.Stdin = bytes.NewReader(raw)
	require.NoError(t, ingest.Run())
	ingest = exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.220")
	ingest.Stdin = bytes.NewReader(raw)
	require.NoError(t, ingest.Run())
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER reject_lifecycle_binding BEFORE INSERT ON lifecycle_occurrence_bindings BEGIN SELECT RAISE(ABORT,'binding fault'); END`)
	require.NoError(t, err)
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	err = tasks.RebuildLifecycleOccurrences(context.Background(), tracker)
	require.Error(t, err)
	require.NoError(t, tracker.Close())
	var occurrences, bindings int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&occurrences))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrence_bindings`).Scan(&bindings))
	require.Zero(t, occurrences)
	require.Zero(t, bindings)
	_, err = db.Exec(`DROP TRIGGER reject_lifecycle_binding`)
	require.NoError(t, err)
	tracker, err = tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	ref := digest.FromBytes(raw)
	body, err := reader.Payload(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, raw, body)
	body[0] ^= 0xff
	again, err := reader.Payload(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, raw, again)
	_, err = reader.Payload(context.Background(), digest.FromString("missing"))
	require.Error(t, err)
	_, err = db.Exec(`PRAGMA ignore_check_constraints=ON; UPDATE lifecycle_payload_blobs SET byte_count=byte_count+1 WHERE digest=?`, ref.String())
	require.NoError(t, err)
	_, err = reader.Payload(context.Background(), ref)
	require.Error(t, err)
	_, err = db.Exec(`UPDATE lifecycle_payload_blobs SET byte_count=?,body=zeroblob(?) WHERE digest=?`, len(raw), len(raw), ref.String())
	require.NoError(t, err)
	_, err = reader.Payload(context.Background(), ref)
	require.Error(t, err)
	require.NoError(t, tracker.Close())
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&occurrences))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrence_bindings`).Scan(&bindings))
	require.Equal(t, 2, occurrences)
	require.Equal(t, 2, bindings)
}

type lifecycleOccurrencePayload struct {
	Contract string                      `json:"contract"`
	Event    model.ContractEventKind     `json:"event"`
	Envelope model.OccurrenceEnvelopeRef `json:"envelope"`
	Bindings []lifecycleBindingPayload   `json:"bindings"`
	Capture  model.CaptureDisposition    `json:"capture"`
	Body     string                      `json:"body_digest"`
}

type lifecycleBindingPayload struct {
	Kind       model.NativeBindingKind `json:"Kind"`
	NativeName string                  `json:"NativeName"`
	Value      string                  `json:"Value"`
}

type interpretedIdentityPayload struct {
	Kind  uint8  `json:"kind"`
	Value string `json:"value"`
}

type interpretedUnresolvedPayload struct {
	Reason uint8 `json:"reason"`
}

type interpretedEvidencePayload struct {
	Semantic        uint8                          `json:"semantic"`
	Identities      []interpretedIdentityPayload   `json:"identities"`
	UnresolvedFacts []interpretedUnresolvedPayload `json:"unresolved_facts"`
	Contract        string                         `json:"contract"`
}

func buildLifecycleBinary(t *testing.T, binary string) {
	t.Helper()
	build := exec.Command("go", "build", "-race", "-o", binary, ".")
	build.Dir = "."
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
}

func initializeLifecycleTestDatabase(t *testing.T, dbPath string) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	_, err = tracker.Create("file://lifecycle-production-test", "bootstrap", "initialize the persisted ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())
}

func queryLifecycleEvidence(t *testing.T, journal provenance.Journal, kind provenance.EvidenceKind) []provenance.EvidenceRow {
	t.Helper()
	page, err := journal.Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  []provenance.EvidenceKind{kind},
		Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
	})
	require.NoError(t, err)
	return page.Rows
}

func readProductionClaudeFixture(t *testing.T, fixture, expectedEvent string) []byte {
	t.Helper()
	root := filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata")
	relativeFixture := filepath.Join("fixtures", fixture)
	raw, err := os.ReadFile(filepath.Join(root, relativeFixture))
	require.NoError(t, err)
	require.True(t, json.Valid(raw))

	provenancePath := filepath.Join(root, "fixtures", strings.TrimSuffix(fixture, ".json")+".provenance.json")
	provenanceBytes, err := os.ReadFile(provenancePath)
	require.NoError(t, err)
	var members map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(provenanceBytes, &members))
	require.ElementsMatch(t, []string{"origin", "harness", "harnessVersion", "captureSource", "rawFileDigest", "capturedAt", "redaction", "event"}, mapKeys(members))
	var sidecar struct {
		acceptance.CaptureProvenance
		Redaction string `json:"redaction"`
		Event     string `json:"event"`
	}
	require.NoError(t, json.Unmarshal(provenanceBytes, &sidecar))
	require.Equal(t, acceptance.OriginAuthenticCapture, sidecar.Origin)
	require.Equal(t, acceptance.HarnessClaudeCode, sidecar.Harness)
	require.Equal(t, "2.1.222", sidecar.HarnessVersion)
	require.Equal(t, "tools/capture-claude-hook.sh", sidecar.CaptureSource)
	require.Equal(t, "home-path-v1", sidecar.Redaction)
	require.Equal(t, expectedEvent, sidecar.Event)
	require.Equal(t, digest.FromBytes(raw).String(), sidecar.RawFileDigest)
	require.NoError(t, sidecar.ValidateFixture(root, relativeFixture))
	return raw
}

func decodeOccurrencePayload(t *testing.T, raw []byte) lifecycleOccurrencePayload {
	t.Helper()
	members := decodeJSONObject(t, raw)
	require.ElementsMatch(t, []string{"contract", "event", "envelope", "bindings", "capture", "body_digest"}, mapKeys(members))
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload lifecycleOccurrencePayload
	require.NoError(t, decoder.Decode(&payload))
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	return payload
}

func decodeInterpretedPayload(t *testing.T, raw []byte) interpretedEvidencePayload {
	t.Helper()
	members := decodeJSONObject(t, raw)
	require.ElementsMatch(t, []string{"semantic", "identities", "unresolved_facts", "contract"}, mapKeys(members))
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload interpretedEvidencePayload
	require.NoError(t, decoder.Decode(&payload))
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	return payload
}

func assertProceedConsultation(t *testing.T, raw []byte) {
	t.Helper()
	members := decodeJSONObject(t, raw)
	require.ElementsMatch(t, []string{"legalized", "response", "interpreted"}, mapKeys(members))
	require.JSONEq(t, `{"decision":"proceed"}`, string(members["response"]))
	interpreted := decodeJSONObject(t, members["interpreted"])
	require.ElementsMatch(t, []string{"result_slot", "content_digest"}, mapKeys(interpreted))
	require.JSONEq(t, `"interpreted"`, string(interpreted["result_slot"]))
}

func publicIdentityPayloads(values []waist.SemanticIdentity) []interpretedIdentityPayload {
	out := make([]interpretedIdentityPayload, len(values))
	for index, value := range values {
		out[index] = interpretedIdentityPayload{Kind: uint8(value.Kind), Value: value.Value}
	}
	return out
}

func publicUnresolvedPayloads(values []waist.UnresolvedFact) []interpretedUnresolvedPayload {
	out := make([]interpretedUnresolvedPayload, len(values))
	for index, value := range values {
		out[index] = interpretedUnresolvedPayload{Reason: uint8(value.Reason)}
	}
	return out
}

func assertOccurrencePayload(t *testing.T, raw []byte, body []byte, capture model.CaptureDisposition, event model.ContractEventKind) lifecycleOccurrencePayload {
	t.Helper()
	members := decodeJSONObject(t, raw)
	require.ElementsMatch(t, []string{"contract", "event", "envelope", "bindings", "capture", "body_digest"}, mapKeys(members))
	require.JSONEq(t, `"claude-code/2.1.210"`, string(members["contract"]))
	require.JSONEq(t, fmt.Sprintf("%d", event), string(members["event"]))
	if capture == model.CaptureMalformed {
		require.JSONEq(t, `2`, string(members["capture"]))
	} else {
		require.JSONEq(t, `1`, string(members["capture"]))
	}
	assertOccurrenceEnvelope(t, members["envelope"])
	if capture == model.CaptureMalformed {
		require.JSONEq(t, `null`, string(members["bindings"]))
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload lifecycleOccurrencePayload
	require.NoError(t, decoder.Decode(&payload))
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	require.Equal(t, occurrenceLifecycleContract, payload.Contract)
	require.Equal(t, event, payload.Event)
	require.Equal(t, occurrenceLifecycleContract, payload.Envelope.Runtime.Contract.String())
	require.Equal(t, capture, payload.Capture)
	require.Equal(t, "2.1.222", payload.Envelope.HostVersion)
	sum := sha256.Sum256(body)
	require.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), payload.Body)
	return payload
}

func decodeJSONObject(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var members map[string]json.RawMessage
	require.NoError(t, decoder.Decode(&members))
	require.NotNil(t, members)
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	return members
}

func assertOccurrenceEnvelope(t *testing.T, raw json.RawMessage) {
	t.Helper()
	members := decodeJSONObject(t, raw)
	require.ElementsMatch(t, []string{"Runtime", "HostVersion", "Schema", "Implementation", "Retention"}, mapKeys(members))
	require.JSONEq(t, `"2.1.222"`, string(members["HostVersion"]))

	runtime := decodeJSONObject(t, members["Runtime"])
	require.ElementsMatch(t, []string{"Definition", "Contract"}, mapKeys(runtime))
	require.JSONEq(t, `"claude-code/2.1.210"`, string(runtime["Contract"]))
	assertZeroDefinitionRef(t, runtime["Definition"])

	for _, wrapper := range []string{"Schema", "Implementation", "Retention"} {
		definition := decodeJSONObject(t, members[wrapper])
		require.ElementsMatch(t, []string{"Definition"}, mapKeys(definition))
		assertZeroDefinitionRef(t, definition["Definition"])
	}
}

func assertZeroDefinitionRef(t *testing.T, raw json.RawMessage) {
	t.Helper()
	members := decodeJSONObject(t, raw)
	require.ElementsMatch(t, []string{"Definition", "Kind", "Content"}, mapKeys(members))
	require.JSONEq(t, `0`, string(members["Definition"]))
	require.JSONEq(t, `0`, string(members["Kind"]))
	require.JSONEq(t, `[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]`, string(members["Content"]))
}

func assertInterpretedEvidence(t *testing.T, raw []byte) {
	t.Helper()
	var members map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	require.NoError(t, decoder.Decode(&members))
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	require.ElementsMatch(t, []string{"semantic", "identities", "unresolved_facts", "contract"}, mapKeys(members))
	require.Equal(t, json.RawMessage(`1`), members["semantic"])
	require.Equal(t, json.RawMessage(expectedInterpretedIdentities), members["identities"])
	require.Equal(t, json.RawMessage(`"claude-code/claude-code@2.1.210"`), members["contract"])

	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload interpretedEvidencePayload
	require.NoError(t, decoder.Decode(&payload))
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	require.Equal(t, uint8(runtime.SemanticObservation), payload.Semantic)
	require.Len(t, payload.Identities, 1)
	require.Equal(t, uint8(runtime.IdentitySession), payload.Identities[0].Kind)
	require.Equal(t, expectedSessionIdentity, payload.Identities[0].Value)
	require.Empty(t, payload.UnresolvedFacts)
	require.Equal(t, interpretedLifecycleContract, payload.Contract)
}

func mapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func assertSharedOperation(t *testing.T, occurrence, interpreted provenance.EvidenceRow) {
	t.Helper()
	require.NotEmpty(t, occurrence.ProducingOperationID)
	require.NotEmpty(t, interpreted.ProducingOperationID)
	require.Equal(t, occurrence.ProducingOperationID, interpreted.ProducingOperationID)
	require.NotZero(t, occurrence.ProducingOperationJournalID)
	require.Equal(t, occurrence.ProducingOperationJournalID, interpreted.ProducingOperationJournalID)
}

// --- M3-SLICE-5: activation-last integrated Codex production proof ---------
//
// The committed Codex handler dispatch was default-off through implementation and
// review (ratified proposal step 6, "activation last"): the two selected events
// became enabled in the committed default only after M3 Implementation UAT. The
// proofs below exercise the enabled path NOW, on the real production handler
// path, by injecting the committed activation catalog activation.Codex0_146_0()
// through the sanctioned HookLifecycleInput.Activations pre-activation seam
// (documented in internal/handlers/hook_lifecycle.go as "not a separate
// test-only code path"). Native continuation bytes are produced by the exact
// per-target encoder the CLI RunE invokes (nativeresponse.Encode).

type codexProductionFixture struct {
	name            string // subtest name; must equal activation.ProductionProofCodex*.Name()'s test suffix
	fixture         string
	event           string
	kind            model.ContractEventKind
	semantic        runtime.EventSemantic
	wantResponse    bool
	wantNative      []byte
	wantEvidence    []provenance.EvidenceKind
	identities      map[runtime.NativeIdentityKind]string
	captureProof    activation.CaptureProof    // must cite the fixture this proof actually reads
	productionProof activation.ProductionProof // must cite this exact running test
}

var codexProductionFixtures = []codexProductionFixture{
	{
		name: "SessionStart", fixture: "session_start_0_146_0.json", event: "SessionStart",
		kind: registration.EventCodexSessionStart, semantic: runtime.SemanticObservation,
		wantResponse: false, wantNative: []byte(`{}`),
		wantEvidence:    []provenance.EvidenceKind{occurrenceEvidenceKind, interpretedEvidenceKind},
		identities:      map[runtime.NativeIdentityKind]string{runtime.IdentitySession: "019fc756-217c-7233-81f7-b5e979279345"},
		captureProof:    activation.CaptureProofCodexSessionStart,
		productionProof: activation.ProductionProofCodexSessionStart,
	},
	{
		name: "PreToolUse", fixture: "pre_tool_use_0_146_0.json", event: "PreToolUse",
		kind: registration.EventCodexPreToolUse, semantic: runtime.SemanticGateConsultation,
		wantResponse: true, wantNative: []byte(`{"continue":true}`),
		wantEvidence: []provenance.EvidenceKind{occurrenceEvidenceKind, interpretedEvidenceKind, consultationEvidenceKind},
		identities: map[runtime.NativeIdentityKind]string{
			runtime.IdentitySession:  "019fc756-217c-7233-81f7-b5e979279345",
			runtime.IdentityTurn:     "019fc756-21b7-7f63-b8e2-4f4cd1ce0184",
			runtime.IdentityToolCall: "exec-fe2dea40-82a3-410f-891e-a7f9e6295c6b",
		},
		captureProof:    activation.CaptureProofCodexPreToolUse,
		productionProof: activation.ProductionProofCodexPreToolUse,
	},
}

// TestEnabledCodexHandlersToDurableReadBack is the M3-P1 (SessionStart ingress
// smoke) and M3-P2 (PreToolUse gate) integrated production proof. For each
// authentic Codex 0.146.0 fixture it drives the real durable handler path with
// the committed activation catalog injected, proves the durable receipt commits
// before the native continuation bytes are available, and proves the persisted
// evidence is provider-correct on bounded public read-back. It mirrors
// TestEnabledOpenCodeHandlersToDurableReadBack for the Codex provider.
func TestEnabledCodexHandlersToDurableReadBack(t *testing.T) {
	activations, err := activation.Codex0_146_0()
	require.NoError(t, err)
	for _, tc := range codexProductionFixtures {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Production-proof linkage (constant -> running test): the activation
			// catalog's ProductionProof referent must name this exact test, so a
			// rename of the function or subtest breaks this assertion immediately
			// instead of leaving the constant silently stale.
			require.Equal(t, "cmd/pasture/hook_lifecycle_production_test.go:"+t.Name(), tc.productionProof.Name(),
				"ProductionProof.Name() must cite this exact running production test")

			dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
			initializeLifecycleTestDatabase(t, dbPath)
			raw := readCodexProductionFixture(t, tc.fixture, tc.event, tc.captureProof)

			response, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
				DBPath: dbPath, Harness: ir.HarnessCodex, Event: tc.event, HostVersion: "0.146.0",
				Input: bytes.NewReader(raw), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{},
				Activations: activations,
			})
			require.NoError(t, err)
			require.Equal(t, tc.wantResponse, response.IsValid())

			// Native continuation bytes are the exact host stdout for this event,
			// through the same per-target encoder the CLI RunE invokes.
			native, err := nativeresponse.Encode(ir.HarnessCodex, response)
			require.NoError(t, err)
			require.Equal(t, tc.wantNative, native, "native continuation bytes must equal the pinned golden shape")

			// Returning from the handler must imply every expected effect is
			// already durably readable (commit precedes response/native encoding).
			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			for _, kind := range tc.wantEvidence {
				require.Len(t, queryLifecycleEvidence(t, tracker.Journal(), kind), 1, "durable %s evidence must be committed before the handler returns", kind)
			}
			if tc.wantResponse {
				interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)[0]
				consultation := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)[0]
				require.Equal(t, interpreted.ProducingOperationJournalID, consultation.ProducingOperationJournalID, "one durable operation groups interpreted and consultation evidence")
				require.Less(t, interpreted.JournalID, consultation.JournalID, "interpreted evidence precedes consultation evidence")
				require.Contains(t, string(consultation.Payload), `"response":{"decision":"proceed"}`)
			} else {
				require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind), "an observation produces no consultation evidence")
			}
			require.NoError(t, tracker.Close())

			// Bounded provider-correct public read-back.
			identities, semantic := readBackGate(t, dbPath, registration.Codex0_146_0().Contract, runtime.Codex0_146_0().ID(), tc.kind)
			require.Equal(t, tc.semantic, semantic)
			require.Equal(t, tc.identities, identities, "read-back identities must be the provider-correct Codex correlation set")
		})
	}
}

// TestCodexAndOpenCodeGateDifferentialPreservesProviderFacts is the M3-P3
// two-live-provider differential. It drives the authentic Codex PreToolUse gate
// and the authentic OpenCode tool.execute.before gate through their real
// production handler paths, then asserts their common Proceed gate semantics
// agree while provider-specific identity, contract, event-name, and native
// continuation facts stay distinct. It never asserts whole-payload identity.
func TestCodexAndOpenCodeGateDifferentialPreservesProviderFacts(t *testing.T) {
	// --- Codex PreToolUse gate: live production path, injected committed catalog.
	codexActivations, err := activation.Codex0_146_0()
	require.NoError(t, err)
	codexRaw := readCodexProductionFixture(t, "pre_tool_use_0_146_0.json", "PreToolUse", activation.CaptureProofCodexPreToolUse)
	codexDB := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, codexDB)
	codexResponse, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
		DBPath: codexDB, Harness: ir.HarnessCodex, Event: "PreToolUse", HostVersion: "0.146.0",
		Input: bytes.NewReader(codexRaw), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{},
		Activations: codexActivations,
	})
	require.NoError(t, err)
	codexIdentities, codexSemantic := readBackGate(t, codexDB, registration.Codex0_146_0().Contract, runtime.Codex0_146_0().ID(), registration.EventCodexPreToolUse)
	codexNative, err := nativeresponse.Encode(ir.HarnessCodex, codexResponse)
	require.NoError(t, err)

	// --- OpenCode tool.execute.before gate: live production path, default-enabled.
	openCodeWire := openCodeToolExecuteBeforeWire(t)
	openCodeDB := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, openCodeDB)
	openCodeResponse, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
		DBPath: openCodeDB, Harness: ir.HarnessOpenCode, Event: "tool.execute.before", HostVersion: "1.18.10",
		Input: bytes.NewReader(openCodeWire), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{},
	})
	require.NoError(t, err)
	openCodeIdentities, openCodeSemantic := readBackGate(t, openCodeDB, registration.OpenCode1_18_10().Contract, runtime.OpenCode1_18_10().ID(), registration.EventOpenCodeToolExecuteBefore)
	openCodeNative, err := nativeresponse.Encode(ir.HarnessOpenCode, openCodeResponse)
	require.NoError(t, err)

	// EQUAL: the common Proceed gate semantic agrees across both live providers.
	require.True(t, codexResponse.IsValid(), "the Codex gate must produce a valid Proceed response")
	require.True(t, openCodeResponse.IsValid(), "the OpenCode gate must produce a valid Proceed response")
	require.Equal(t, runtime.SemanticGateConsultation, codexSemantic)
	require.Equal(t, codexSemantic, openCodeSemantic, "both live providers derive the same gate-consultation semantic")
	codexGate, err := runtime.Codex0_146_0Lifecycle().Mapping(runtime.CodexEventPreToolUse)
	require.NoError(t, err)
	openCodeGate, err := runtime.OpenCode1_18_10Lifecycle().Mapping(runtime.OpenCodeEventToolExecuteBefore)
	require.NoError(t, err)
	require.Equal(t, codexGate.Semantic(), openCodeGate.Semantic(), "the shared gate semantic is provider-neutral")
	require.Equal(t, codexGate.Blocking(), openCodeGate.Blocking(), "both gate mappings share the same blocking mode")

	// DISTINCT: provider-specific facts stay separate; no whole-payload identity.
	require.NotEqual(t, runtime.Codex0_146_0().ID(), runtime.OpenCode1_18_10().ID(), "interpreted runtime contracts remain provider-correct")
	require.NotEqual(t, registration.Codex0_146_0().Contract, registration.OpenCode1_18_10().Contract, "registration contracts remain provider-correct")
	require.Equal(t, []byte(`{"continue":true}`), codexNative)
	require.Equal(t, []byte(`{"decision":"proceed"}`), openCodeNative)
	require.NotEqual(t, codexNative, openCodeNative, "provider-specific native continuation shapes remain distinct")
	require.Equal(t, "PreToolUse", codexGate.NativeName())
	require.Equal(t, "tool.execute.before", openCodeGate.NativeName())

	// Provider-correct correlation: distinct native identity paths and values.
	require.Equal(t, []string{"session_id", "turn_id", "tool_use_id"}, gateIdentityNativeNames(codexGate))
	require.Equal(t, []string{"sessionID", "callID"}, gateIdentityNativeNames(openCodeGate))
	require.Contains(t, codexIdentities, runtime.IdentityTurn, "only the Codex gate carries a turn correlation")
	require.NotContains(t, openCodeIdentities, runtime.IdentityTurn, "the OpenCode gate carries no turn correlation")
	require.NotEqual(t, codexIdentities[runtime.IdentitySession], openCodeIdentities[runtime.IdentitySession], "session identity values are provider-specific")
	require.NotEqual(t, codexIdentities[runtime.IdentityToolCall], openCodeIdentities[runtime.IdentityToolCall], "tool-call identity values are provider-specific")
}

// TestCodexActivationLeavesClaudeAndOpenCodeArtifactsIsolated is the M3-P4
// activation-isolation obligation at the committed-artifact layer. Codex now
// publishes its OWN committed activation audit report at
// .codex/pasture-codex-activation.json (Stage 1 #24, mirroring the Claude
// precedent): its presence is asserted here, but landing it must not write
// Codex provenance into the Claude or OpenCode activation artifacts, and it must
// not resurrect the legacy .codex/pasture-activation.json filename. The
// byte-identity of the Claude and OpenCode artifacts across regeneration is
// additionally guaranteed by the L3 zero-diff `make generate` gate.
func TestCodexActivationLeavesClaudeAndOpenCodeArtifactsIsolated(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	claudeActivation, err := os.ReadFile(filepath.Join(root, "hooks", "pasture-activation.json"))
	require.NoError(t, err)
	require.Contains(t, string(claudeActivation), `"harness": "claude-code"`, "the shared activation report remains the Claude-only artifact")
	require.NotContains(t, string(claudeActivation), "ingress/codex", "no Codex capture proof may leak into the Claude activation artifact")
	require.NotContains(t, string(claudeActivation), "TestEnabledCodexHandlersToDurableReadBack", "no Codex production proof may leak into the Claude activation artifact")

	openCodeManifest, err := os.ReadFile(filepath.Join(root, ".opencode", "pasture-opencode.json"))
	require.NoError(t, err)
	require.Contains(t, string(openCodeManifest), `"target": "opencode"`)
	require.NotContains(t, string(openCodeManifest), "ingress/codex", "no Codex capture proof may leak into the OpenCode manifest")
	require.NotContains(t, string(openCodeManifest), "codex", "the OpenCode manifest carries no Codex activation entry")

	// Codex now emits its own committed activation audit report, unconditionally,
	// derived from registration.Codex0_146_0() + activation.Codex0_146_0().
	codexReportPath := filepath.Join(root, ".codex", "pasture-codex-activation.json")
	codexReport, err := os.ReadFile(codexReportPath)
	require.NoError(t, err, "the Codex activation audit report must be a committed artifact at %s", codexReportPath)

	// Content equality against a freshly derived report — no golden literals for
	// content, so catalog drift in registration.Codex0_146_0() or
	// activation.Codex0_146_0() is caught here rather than silently accepted.
	wantReport := deriveCodexActivationReport(t)
	require.Equal(t, string(wantReport), string(codexReport), "the committed Codex activation report must equal the report freshly derived from the pinned Codex registration + activation catalogs")

	// Literal invariants on the committed artifact: exactly 10 exhaustive
	// entries, and exactly the two authentically-proven events enabled.
	var parsed struct {
		Harness string `json:"harness"`
		Events  []struct {
			Event string `json:"event"`
			State string `json:"state"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal(codexReport, &parsed))
	require.Equal(t, "codex", parsed.Harness, "the Codex audit report is the Codex-only artifact")
	require.Len(t, parsed.Events, 10, "the Codex activation report is exhaustive over all 10 generated Codex events")
	enabled := make([]string, 0, 2)
	for _, entry := range parsed.Events {
		if entry.State == "enabled" {
			enabled = append(enabled, entry.Event)
		}
	}
	require.Equal(t, []string{"SessionStart", "PreToolUse"}, enabled, "exactly the two authentically-proven Codex events (SessionStart, PreToolUse) are enabled; the other 8 are withheld")

	// The legacy Codex activation filename must never be emitted: the report
	// lives only at pasture-codex-activation.json.
	legacy := filepath.Join(root, ".codex", "pasture-activation.json")
	_, statErr := os.Stat(legacy)
	require.ErrorIs(t, statErr, os.ErrNotExist, "the legacy Codex activation filename must not be emitted at %s", legacy)
}

// deriveCodexActivationReport recomputes the exact bytes the Codex activation
// audit report must contain, straight from the pinned registration manifest and
// activation catalog — mirroring codexManifestEmitter.Emit (which mirrors
// emitClaudeHooks). It carries no golden literals: every event name, state,
// reason, and proof is read from the live catalogs, so a catalog change forces
// the committed artifact to change in lockstep or this test fails.
func deriveCodexActivationReport(t *testing.T) []byte {
	t.Helper()
	manifest := registration.Codex0_146_0()
	states, err := activation.Codex0_146_0()
	require.NoError(t, err)
	byKind := make(map[model.ContractEventKind]activation.Entry, len(states))
	for _, state := range states {
		byKind[state.Event] = state
	}
	type reportEntry struct {
		Event           string `json:"event"`
		State           string `json:"state"`
		Reason          string `json:"reason,omitempty"`
		CaptureProof    string `json:"captureProof,omitempty"`
		ProductionProof string `json:"productionProof,omitempty"`
	}
	report := struct {
		Harness  string        `json:"harness"`
		Contract string        `json:"contract"`
		Events   []reportEntry `json:"events"`
	}{Harness: string(manifest.Harness), Contract: manifest.Contract.String()}
	for _, event := range manifest.Events {
		state, ok := byKind[event.Kind]
		require.True(t, ok, "activation catalog must cover generated Codex event %q", event.NativeName)
		entry := reportEntry{Event: event.NativeName, State: state.State.String(), Reason: state.Reason.String()}
		if state.State == activation.Enabled {
			entry.CaptureProof = state.CaptureProof.Name()
			entry.ProductionProof = state.ProductionProof.Name()
		}
		report.Events = append(report.Events, entry)
	}
	wire, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	return append(wire, '\n')
}

// readBackGate rebuilds and reads back the single committed occurrence for a
// live gate, asserts its provider-correct registration/runtime contract and
// event kind, and returns its interpreted correlation identities and semantic.
func readBackGate(t *testing.T, dbPath string, wantRegistrationContract ir.RuntimeContractID, wantRuntimeContract any, wantKind model.ContractEventKind) (map[runtime.NativeIdentityKind]string, runtime.EventSemantic) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	pageSize, err := model.NewPageSize(4)
	require.NoError(t, err)
	records, err := reader.Records(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: pageSize}})
	require.NoError(t, err)
	require.Len(t, records.Records(), 1, "each provider's live gate must persist exactly one occurrence")
	record := records.Records()[0]
	require.Equal(t, wantRegistrationContract, record.Occurrence.RuntimeContract)
	require.Equal(t, wantKind, record.Occurrence.Kind)
	require.Len(t, record.Interpreted(), 1)
	interpreted := record.Interpreted()[0]
	require.Equal(t, wantRuntimeContract, interpreted.Contract())
	out := make(map[runtime.NativeIdentityKind]string, len(interpreted.Identities()))
	for _, identity := range interpreted.Identities() {
		out[identity.Kind] = identity.Value
	}
	return out, interpreted.Semantic()
}

func gateIdentityNativeNames(mapping runtime.LifecycleEventMapping) []string {
	names := make([]string, 0)
	for _, identity := range mapping.Identities() {
		names = append(names, identity.NativeName())
	}
	return names
}

// openCodeToolExecuteBeforeWire reconstructs the exact stdin bytes the generated
// OpenCode plugin sends the CLI for tool.execute.before —
// JSON.stringify({ input, output: { args } }) — from the authentic capture.
func openCodeToolExecuteBeforeWire(t *testing.T) []byte {
	t.Helper()
	root := filepath.Join("..", "..", "internal", "lifecycle", "ingress", "opencode", "testdata", "fixtures")
	raw, err := os.ReadFile(filepath.Join(root, "tool_execute_before_1_18_10.capture.json"))
	require.NoError(t, err)
	var capture struct {
		Value struct {
			Input  json.RawMessage `json:"input"`
			Output struct {
				Args json.RawMessage `json:"args"`
			} `json:"output"`
		} `json:"value"`
	}
	require.NoError(t, json.Unmarshal(raw, &capture))
	type outputArgs struct {
		Args json.RawMessage `json:"args"`
	}
	wire, err := json.Marshal(struct {
		Input  json.RawMessage `json:"input"`
		Output outputArgs      `json:"output"`
	}{Input: capture.Value.Input, Output: outputArgs{Args: capture.Value.Output.Args}})
	require.NoError(t, err)
	return wire
}

func readCodexProductionFixture(t *testing.T, fixture, expectedEvent string, captureProof activation.CaptureProof) []byte {
	t.Helper()
	relDir := filepath.Join("internal", "lifecycle", "ingress", "codex", "testdata", "fixtures")
	root := filepath.Join("..", "..", relDir)
	raw, err := os.ReadFile(filepath.Join(root, fixture))
	require.NoError(t, err)
	require.True(t, json.Valid(raw))
	require.Contains(t, string(raw), `"hook_event_name":"`+expectedEvent+`"`, "the authentic Codex fixture must carry its native event name")

	provenanceBytes, err := os.ReadFile(filepath.Join(root, strings.TrimSuffix(fixture, ".json")+".provenance.json"))
	require.NoError(t, err)
	var sidecar struct {
		Provider               string `json:"provider"`
		ObservedRuntimeVersion string `json:"observedRuntimeVersion"`
		Origin                 string `json:"origin"`
		Redaction              string `json:"redaction"`
		RawBytes               int    `json:"rawBytes"`
		RawSHA256              string `json:"rawSHA256"`
		ClearanceAuthority     string `json:"clearanceAuthority"`
	}
	require.NoError(t, json.Unmarshal(provenanceBytes, &sidecar))
	require.Equal(t, "codex", sidecar.Provider)
	require.Equal(t, "0.146.0", sidecar.ObservedRuntimeVersion)
	require.Equal(t, "authentic-capture", sidecar.Origin)
	require.Equal(t, "none", sidecar.Redaction)
	require.Equal(t, "aura-plugins-a6h3d", sidecar.ClearanceAuthority)
	require.Equal(t, len(raw), sidecar.RawBytes, "authentic Codex fixture byte count must match its provenance sidecar")
	sum := sha256.Sum256(raw)
	require.Equal(t, hex.EncodeToString(sum[:]), sidecar.RawSHA256, "authentic Codex fixture digest must match the cleared digest exactly")

	// Capture-proof linkage (constant -> fixture, path -> bytes -> digest): the
	// activation catalog's CaptureProof referent must cite the EXACT fixture this
	// production proof reads, and the bytes at that cited path must reproduce the
	// cleared digest the proof enforces (sidecar.RawSHA256, the single source of
	// truth — no duplicated digest literal). A moved fixture or an edited referent
	// string breaks this immediately instead of leaving the constant stale.
	citedPath, _, found := strings.Cut(captureProof.Name(), " (")
	require.True(t, found, "CaptureProof.Name() must be 'relative/path (description)'; got %q", captureProof.Name())
	require.Equal(t, filepath.ToSlash(filepath.Join(relDir, fixture)), citedPath,
		"CaptureProof.Name() must cite the exact fixture path this production proof reads")
	citedBytes, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(citedPath)))
	require.NoError(t, err, "the fixture path cited by CaptureProof.Name() must resolve to a real file")
	require.Equal(t, raw, citedBytes, "the fixture cited by CaptureProof.Name() must be exactly the bytes this proof reads")
	citedSum := sha256.Sum256(citedBytes)
	require.Equal(t, sidecar.RawSHA256, hex.EncodeToString(citedSum[:]),
		"the fixture cited by CaptureProof.Name() must digest-match the cleared SHA-256 the proof enforces")
	return raw
}
