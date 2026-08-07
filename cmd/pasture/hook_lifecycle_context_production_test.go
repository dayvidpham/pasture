package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/tasks"
)

const (
	disclosurePlanEvidenceKind    = provenance.EvidenceKind("pasture.lifecycle.disclosure.plan.v1")
	disclosureAttemptEvidenceKind = provenance.EvidenceKind("pasture.lifecycle.disclosure.attempt.v1")
	disclosureResultEvidenceKind  = provenance.EvidenceKind("pasture.lifecycle.disclosure.result.v1")
)

type disclosurePlanRow struct {
	Scope      string `json:"scope"`
	Projection string `json:"projection"`
	Policy     string `json:"policy"`
}

type disclosureResultRow struct {
	Disposition string `json:"disposition"`
}

// failingWriter fails on the first byte written, simulating a broken stdout
// pipe AFTER the durable commit has completed.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("simulated stdout failure")
}

func runContextBuilt(t *testing.T, binary, dbPath, binding, format string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "context", "--binding", binding, "--format", format)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	return out.String(), errb.String(), code
}

func disclosureRows(t *testing.T, dbPath string, kind provenance.EvidenceKind) []provenance.EvidenceRow {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	return queryLifecycleEvidence(t, tracker.Journal(), kind)
}

// TestContextDisclosureCommitsOneOpBeforePrint drives the built CLI against a
// real store: four deliveries sharing one session, then `hook lifecycle context`
// commits the plan+attempt+result facts as ONE gated operation BEFORE printing,
// with the DisclosureReleased disposition, and the committed plan's projection
// digest equals the sha256 of the exact projection printed to stdout (read-back
// matches).
func TestContextDisclosureCommitsOneOpBeforePrint(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	for _, tc := range []struct{ event, fixture string }{
		{"PreToolUse", "pre_tool_use_2_1_222.json"},
		{"PostToolUse", "post_tool_use_2_1_222.json"},
		{"PostToolUseFailure", "post_tool_use_failure_2_1_222.json"},
		{"PostToolBatch", "post_tool_batch_2_1_222.json"},
	} {
		deliverClaudeBuilt(t, binary, dbPath, tc.event, readProductionClaudeFixture(t, tc.fixture, tc.event))
	}

	binding := "session:session_id=" + sharedSessionIdentity
	out, errb, code := runContextBuilt(t, binary, dbPath, binding, "json")
	require.Empty(t, errb)
	require.Equal(t, 0, code)
	require.NotEmpty(t, out)

	// Exactly ONE disclosure operation: one plan, one attempt, one result row,
	// all sharing one producing operation identity.
	planRows := disclosureRows(t, dbPath, disclosurePlanEvidenceKind)
	attemptRows := disclosureRows(t, dbPath, disclosureAttemptEvidenceKind)
	resultRows := disclosureRows(t, dbPath, disclosureResultEvidenceKind)
	require.Len(t, planRows, 1, "one disclosure invocation commits exactly one plan fact")
	require.Len(t, attemptRows, 1, "one disclosure invocation commits exactly one attempt fact")
	require.Len(t, resultRows, 1, "one disclosure invocation commits exactly one result fact")
	op := planRows[0].ProducingOperationID
	require.NotEmpty(t, op)
	require.Equal(t, op, attemptRows[0].ProducingOperationID, "plan and attempt must be committed in ONE operation")
	require.Equal(t, op, resultRows[0].ProducingOperationID, "plan and result must be committed in ONE operation")

	// Disposition is the maximal honest DisclosureReleased.
	var result disclosureResultRow
	require.NoError(t, json.Unmarshal(resultRows[0].Payload, &result))
	require.Equal(t, "released", result.Disposition)

	// Read-back: the committed plan's projection digest equals sha256 of the
	// exact projection bytes printed to stdout (commit-before-print binds the
	// released bytes to the durable fact).
	var plan disclosurePlanRow
	require.NoError(t, json.Unmarshal(planRows[0].Payload, &plan))
	require.Equal(t, "released", result.Disposition)
	printed := strings.TrimRight(out, "\n")
	printedDigest := sha256.Sum256([]byte(printed))
	require.Equal(t, hex.EncodeToString(printedDigest[:]), plan.Projection,
		"the committed projection digest must equal the digest of the printed projection")
	require.NotEmpty(t, plan.Scope)
	require.NotEmpty(t, plan.Policy)
}

// TestContextDisclosureHostResponseByteIdenticalProceed proves R3.2: disclosure
// changes nothing hosts see. A delivery produces the SAME native bytes and
// commits ZERO disclosure facts, whether or not a disclosure has been recorded.
func TestContextDisclosureHostResponseByteIdenticalProceed(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	preToolUse := readProductionClaudeFixture(t, "pre_tool_use_2_1_222.json", "PreToolUse")
	bytesBefore := deliverClaudeBuilt(t, binary, dbPath, "PreToolUse", preToolUse)
	require.JSONEq(t, `{"decision":"proceed"}`, bytesBefore)
	require.Empty(t, disclosureRows(t, dbPath, disclosurePlanEvidenceKind), "a delivery must never commit a disclosure fact")

	// Record a disclosure so the store now holds disclosure facts.
	_, errb, code := runContextBuilt(t, binary, dbPath, "session:session_id="+sharedSessionIdentity, "json")
	require.Empty(t, errb)
	require.Equal(t, 0, code)
	require.Len(t, disclosureRows(t, dbPath, disclosurePlanEvidenceKind), 1)

	// Delivery AFTER a disclosure exists: byte-identical native output, and no
	// new disclosure fact (the delivery path is untouched by disclosure).
	bytesAfter := deliverClaudeBuilt(t, binary, dbPath, "PreToolUse", preToolUse)
	require.Equal(t, bytesBefore, bytesAfter, "the delivery path must be byte-identical regardless of committed disclosures")
	require.Len(t, disclosureRows(t, dbPath, disclosurePlanEvidenceKind), 1, "a delivery must add no disclosure fact")
}

// TestContextDisclosurePostCommitStdoutFailureReportsStderrWithTrailIntact
// exercises the UAT-resolution-6 stdout-failure path on the production handler
// against a real store: after the disclosure operation is durably committed, a
// broken stdout writer makes the print fail. The handler reports an actionable
// error (routed to stderr, never exit code 2) and the durable trail — the one
// plan/attempt/result operation — remains intact.
func TestContextDisclosurePostCommitStdoutFailureReportsStderrWithTrailIntact(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	raw := readProductionClaudeFixture(t, "session_start_2_1_222.json", "SessionStart")
	for i := 0; i < 3; i++ {
		require.NoError(t, handlers.HookLifecycle(context.Background(), handlers.HookLifecycleInput{
			DBPath: dbPath, Harness: "claude-code", Event: "SessionStart", HostVersion: "2.1.222",
			Input: bytes.NewReader(raw), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{},
		}))
	}

	code, err := handlers.HookLifecycleContext(context.Background(), failingWriter{}, handlers.HookLifecycleContextInput{
		DBPath:     dbPath,
		Binding:    "session:session_id=3696b790-3973-49f2-b156-9d82146bf7ec",
		Clock:      lifecycleCLIClock{},
		Operations: lifecycleCLIOperations{},
	}, "json")
	require.Error(t, err, "a post-commit stdout failure must surface an error")
	require.NotEqual(t, 0, code)
	require.NotEqual(t, 2, code, "no lifecycle path may emit exit code 2")

	// The durable trail is intact: the disclosure operation committed BEFORE the
	// failed print, so all three facts persist.
	require.Len(t, disclosureRows(t, dbPath, disclosurePlanEvidenceKind), 1, "the plan fact must persist despite the stdout failure")
	require.Len(t, disclosureRows(t, dbPath, disclosureAttemptEvidenceKind), 1, "the attempt fact must persist despite the stdout failure")
	require.Len(t, disclosureRows(t, dbPath, disclosureResultEvidenceKind), 1, "the result fact must persist despite the stdout failure")
}
