package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
)

const (
	occurrenceLifecycleContract   = "claude-code/2.1.210"
	interpretedLifecycleContract  = "claude-code/claude-code@2.1.210"
	occurrenceEvidenceKind        = provenance.EvidenceKind("pasture.lifecycle.occurrence.v1")
	interpretedEvidenceKind       = provenance.EvidenceKind("pasture.lifecycle.interpreted.v1")
	expectedSessionIdentity       = "b3cfe877-feb4-4ba3-9500-414c8bfb51c4"
	expectedInterpretedIdentities = `[{"kind":1,"value":"b3cfe877-feb4-4ba3-9500-414c8bfb51c4"}]`
)

func TestEnabledClaudeEventToOccurrenceAndInterpretedEvidence(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, "pasture.db")

	initializeLifecycleTestDatabase(t, dbPath)

	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_210.json"))
	require.NoError(t, err)
	command := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.220")
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
	occurrencePayload := assertOccurrencePayload(t, occurrence.Payload, raw, model.CaptureValid)

	interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
	require.Len(t, interpreted, 1)
	assertInterpretedEvidence(t, interpreted[0].Payload)
	assertSharedOperation(t, occurrence, interpreted[0])

	require.Equal(t, registration.EventSessionStart, occurrencePayload.Event)
	require.Equal(t, "2.1.220", occurrencePayload.Envelope.HostVersion)
	// The occurrence-side binding is retained independently from the waist
	// identity assertion above; it is not a substitute for interpreted evidence.
	require.Len(t, occurrencePayload.Bindings, 1)
	require.Equal(t, model.BindingSession, occurrencePayload.Bindings[0].Kind)
	require.Equal(t, "session_id", occurrencePayload.Bindings[0].NativeName)
	require.Equal(t, expectedSessionIdentity, occurrencePayload.Bindings[0].Value)
}

func TestMalformedClaudeEventToOccurrenceOnly(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, "pasture.db")
	initializeLifecycleTestDatabase(t, dbPath)

	raw := []byte(`{"session_id":`)
	command := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.220")
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
	assertOccurrencePayload(t, occurrences[0].Payload, raw, model.CaptureMalformed)

	interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
	require.Empty(t, interpreted)
}

func TestInvalidLifecycleInvocationCreatesNoDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing", "pasture.db")
	err := handlers.HookLifecycle(context.Background(), handlers.HookLifecycleInput{DBPath: dbPath, Harness: "claude-code", Event: "Unknown", HostVersion: "2.1.220", Input: bytes.NewBufferString("{}"), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{}})
	require.Error(t, err)
	_, statErr := os.Stat(dbPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
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

type interpretedEvidencePayload struct {
	Semantic   uint8 `json:"semantic"`
	Identities []struct {
		Kind  uint8  `json:"kind"`
		Value string `json:"value"`
	} `json:"identities"`
	UnresolvedFacts []struct {
		Reason uint8 `json:"reason"`
	} `json:"unresolved_facts"`
	Contract string `json:"contract"`
}

func buildLifecycleBinary(t *testing.T, binary string) {
	t.Helper()
	build := exec.Command("go", "build", "-o", binary, ".")
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

func assertOccurrencePayload(t *testing.T, raw []byte, body []byte, capture model.CaptureDisposition) lifecycleOccurrencePayload {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload lifecycleOccurrencePayload
	require.NoError(t, decoder.Decode(&payload))
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	require.Equal(t, occurrenceLifecycleContract, payload.Contract)
	require.Equal(t, registration.EventSessionStart, payload.Event)
	require.Equal(t, occurrenceLifecycleContract, payload.Envelope.Runtime.Contract.String())
	require.Equal(t, capture, payload.Capture)
	require.Equal(t, "2.1.220", payload.Envelope.HostVersion)
	sum := sha256.Sum256(body)
	require.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), payload.Body)
	return payload
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
