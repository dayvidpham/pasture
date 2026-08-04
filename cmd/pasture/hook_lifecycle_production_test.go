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

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
)

func TestEnabledOpenCodeHandlersToDurableReadBack(t *testing.T) {
	bun, err := exec.LookPath("bun")
	require.NoError(t, err, "Bun is required for the generated OpenCode production proof; enter the flake dev shell")
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, "pasture.db")
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
	expectedSessionIdentity       = "b3cfe877-feb4-4ba3-9500-414c8bfb51c4"
	expectedInterpretedIdentities = `[{"kind":1,"value":"b3cfe877-feb4-4ba3-9500-414c8bfb51c4"}]`
)

func TestEnabledClaudeEventToOccurrenceAndInterpretedEvidence(t *testing.T) {
	manifest, err := activation.ClaudeCode2_1_210()
	require.NoError(t, err)
	enabled := make([]model.ContractEventKind, 0, 1)
	for _, entry := range manifest {
		if entry.State == activation.Enabled {
			enabled = append(enabled, entry.Event)
		}
	}
	require.Equal(t, []model.ContractEventKind{registration.EventSessionStart}, enabled, "literal production rows must equal the complete static enabled set")

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
	require.NoError(t, tracker.Close())

	list := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "list", "--format", "json", "--binding", "session:session_id="+expectedSessionIdentity)
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
		cmd := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.220")
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
	pageOne := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "list", "--format", "json", "--page-size", "1", "--binding", "session:session_id="+expectedSessionIdentity)
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
	continuation := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "list", "--format", "json", "--page-size", "10", "--cursor", firstPage.Next, "--binding", "session:session_id="+expectedSessionIdentity, "--binding", "session:session_id="+expectedSessionIdentity)
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
	changed := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "list", "--format", "json", "--cursor", firstPage.Next, "--binding", "session:session_id=changed")
	err = changed.Run()
	require.Error(t, err)
	require.Equal(t, 1, changed.ProcessState.ExitCode())
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
	occurrencePayload := assertOccurrencePayload(t, occurrences[0].Payload, raw, model.CaptureMalformed)
	require.Empty(t, occurrencePayload.Bindings)

	interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
	require.Empty(t, interpreted)
	consultations := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)
	require.Empty(t, consultations)
	require.NoError(t, tracker.Close())
	list := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "list", "--format", "json")
	var output bytes.Buffer
	list.Stdout = &output
	require.NoError(t, list.Run())
	require.Contains(t, output.String(), `"interpreted":[]`)
}

func TestLifecycleLeafFaultsExitZeroAndReport(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	fixture, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_210.json"))
	require.NoError(t, err)
	base := []string{"--db", filepath.Join(dir, "pasture.db"), "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.220"}
	databaseDirectory := filepath.Join(dir, "database-directory")
	require.NoError(t, os.Mkdir(databaseDirectory, 0o700))
	cases := []struct {
		name string
		args []string
		in   []byte
		want string
	}{
		{name: "unwritable database", args: append([]string{"--db", databaseDirectory}, base[2:]...), in: fixture, want: "open"},
		{name: "missing flag", args: []string{"--db", filepath.Join(dir, "missing.db"), "hook", "lifecycle", "--harness", "claude-code", "--host-version", "2.1.220"}, in: fixture, want: "not in the generated Claude registration"},
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

	list := exec.Command(binary, "--db", filepath.Join(dir, "list.db"), "hook", "lifecycle", "list", "--unknown-list-flag")
	err = list.Run()
	require.Error(t, err)
	require.Equal(t, 1, list.ProcessState.ExitCode(), "human list command must retain standard non-zero flag errors")
}

func TestInvalidLifecycleInvocationCreatesNoDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing", "pasture.db")
	err := handlers.HookLifecycle(context.Background(), handlers.HookLifecycleInput{DBPath: dbPath, Harness: "claude-code", Event: "Unknown", HostVersion: "2.1.220", Input: bytes.NewBufferString("{}"), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{}})
	require.Error(t, err)
	_, statErr := os.Stat(dbPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestLifecycleListRejectsCursorBeforeDatabaseOpen(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, "missing", "pasture.db")
	command := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "list", "--cursor", "not-base64!")
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
			command := exec.Command(binary, "--db", tc.path, "hook", "lifecycle", "list")
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
	dbPath := filepath.Join(dir, "pasture.db")
	initializeLifecycleTestDatabase(t, dbPath)
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_210.json"))
	require.NoError(t, err)
	ingest := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.220")
	ingest.Stdin = bytes.NewReader(raw)
	require.NoError(t, ingest.Run())
	ingest = exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.220")
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

func assertOccurrencePayload(t *testing.T, raw []byte, body []byte, capture model.CaptureDisposition) lifecycleOccurrencePayload {
	t.Helper()
	members := decodeJSONObject(t, raw)
	require.ElementsMatch(t, []string{"contract", "event", "envelope", "bindings", "capture", "body_digest"}, mapKeys(members))
	require.JSONEq(t, `"claude-code/2.1.210"`, string(members["contract"]))
	require.JSONEq(t, `1`, string(members["event"]))
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
	require.Equal(t, registration.EventSessionStart, payload.Event)
	require.Equal(t, occurrenceLifecycleContract, payload.Envelope.Runtime.Contract.String())
	require.Equal(t, capture, payload.Capture)
	require.Equal(t, "2.1.220", payload.Envelope.HostVersion)
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
	require.JSONEq(t, `"2.1.220"`, string(members["HostVersion"]))

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
