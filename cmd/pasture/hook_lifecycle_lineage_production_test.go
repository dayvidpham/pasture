package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/tasks"
)

const lineageLinkEvidenceKind = provenance.EvidenceKind("pasture.lifecycle.link.v1")

// sharedSessionIdentity is the Claude session carried by the PreToolUse,
// PostToolUse, PostToolUseFailure, and PostToolBatch authentic fixtures.
const sharedSessionIdentity = "5f8d1e67-8c33-4d23-a7fe-ffd9eb711e68"

type lineageEdgeOut struct {
	Harness string `json:"harness"`
	Kind    string `json:"kind"`
	Value   string `json:"value"`
	From    int64  `json:"from"`
	To      int64  `json:"to"`
	LinkID  int64  `json:"linkId"`
}

type lineageOut struct {
	Materialized int              `json:"materialized"`
	Links        []lineageEdgeOut `json:"links"`
}

func deliverClaudeBuilt(t *testing.T, binary, dbPath, event string, raw []byte) string {
	t.Helper()
	cmd := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", event, "--host-version", "2.1.222")
	cmd.Stdin = bytes.NewReader(raw)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	require.NoError(t, cmd.Run(), out.String()+errb.String())
	require.Empty(t, errb.String())
	return out.String()
}

func runLineageBuilt(t *testing.T, binary, dbPath, binding, format string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "lineage", "--binding", binding, "--format", format)
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

func countLinkEvidence(t *testing.T, dbPath string) int {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	return len(queryLifecycleEvidence(t, tracker.Journal(), lineageLinkEvidenceKind))
}

// TestLineageMaterializeThenSecondRunIsNoOp drives the built CLI against a real
// store: four deliveries sharing one session produce a per-host chain, the first
// `hook lifecycle lineage` invocation materializes the missing predecessor edges
// (three session edges + one tool-call edge), and an immediate second invocation
// derives and commits NOTHING — the read-side materialize-then-no-op property.
func TestLineageMaterializeThenSecondRunIsNoOp(t *testing.T) {
	dir := t.TempDir()
	binary := lifecycleBinary(t)
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
	firstOut, firstErr, firstCode := runLineageBuilt(t, binary, dbPath, binding, "json")
	require.Empty(t, firstErr)
	require.Equal(t, 0, firstCode)
	var first lineageOut
	require.NoError(t, json.Unmarshal([]byte(firstOut), &first))
	require.Equal(t, 4, first.Materialized, "three session edges + one tool-call edge must be materialized")
	require.Len(t, first.Links, 4)
	// The session chain threads the four occurrences by immediately-preceding
	// occurrence; every edge is on the one enabled harness.
	sessionEdges := 0
	for _, edge := range first.Links {
		require.Equal(t, "claude-code", edge.Harness)
		require.Less(t, edge.From, edge.To, "From must be the earlier occurrence")
		if edge.Kind == "session" {
			require.Equal(t, sharedSessionIdentity, edge.Value)
			sessionEdges++
		}
	}
	require.Equal(t, 3, sessionEdges, "four session occurrences form three predecessor edges")
	require.Equal(t, 4, countLinkEvidence(t, dbPath))

	secondOut, secondErr, secondCode := runLineageBuilt(t, binary, dbPath, binding, "json")
	require.Empty(t, secondErr)
	require.Equal(t, 0, secondCode)
	var second lineageOut
	require.NoError(t, json.Unmarshal([]byte(secondOut), &second))
	require.Equal(t, 0, second.Materialized, "an immediate re-run must materialize nothing (idempotent)")
	require.Equal(t, first.Links, second.Links, "the printed chain must be identical across runs")
	require.Equal(t, 4, countLinkEvidence(t, dbPath), "no duplicate links may be committed on re-run")
}

// TestLineageDeliveryPathStaysByteEquivalent proves the ratified provisional
// decision: lineage is read-side and the hook delivery path is byte-equivalent.
// A delivery produces the SAME native bytes and commits ZERO link effects
// whether or not committed lineage links already exist in the store.
func TestLineageDeliveryPathStaysByteEquivalent(t *testing.T) {
	dir := t.TempDir()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	preToolUse := readProductionClaudeFixture(t, "pre_tool_use_2_1_222.json", "PreToolUse")

	// Delivery BEFORE any lineage exists: capture the native continuation bytes.
	bytesBefore := deliverClaudeBuilt(t, binary, dbPath, "PreToolUse", preToolUse)
	require.JSONEq(t, `{"decision":"proceed"}`, bytesBefore)
	// Deliveries never write link evidence — the delivery path has zero lineage
	// reads or effects.
	require.Zero(t, countLinkEvidence(t, dbPath), "a delivery must never commit a lineage link")

	// Build a committed chain and materialize it, so the store now holds links.
	for _, tc := range []struct{ event, fixture string }{
		{"PostToolUse", "post_tool_use_2_1_222.json"},
		{"PostToolUseFailure", "post_tool_use_failure_2_1_222.json"},
		{"PostToolBatch", "post_tool_batch_2_1_222.json"},
	} {
		deliverClaudeBuilt(t, binary, dbPath, tc.event, readProductionClaudeFixture(t, tc.fixture, tc.event))
	}
	_, lineageErr, lineageCode := runLineageBuilt(t, binary, dbPath, "session:session_id="+sharedSessionIdentity, "json")
	require.Empty(t, lineageErr)
	require.Equal(t, 0, lineageCode)
	linksAfterMaterialize := countLinkEvidence(t, dbPath)
	require.Positive(t, linksAfterMaterialize)

	// Delivery AFTER lineage links exist: byte-identical native output, and the
	// link count is unchanged (the delivery added no link).
	bytesAfter := deliverClaudeBuilt(t, binary, dbPath, "PreToolUse", preToolUse)
	require.Equal(t, bytesBefore, bytesAfter, "the delivery path must be byte-equivalent regardless of committed lineage")
	require.Equal(t, linksAfterMaterialize, countLinkEvidence(t, dbPath), "a delivery must add no lineage link even when links already exist")
}

// TestLineageOverCapRefusesAndCommitsNothing exercises the over-cap refusal on
// the production handler path against a real store: sixty-six occurrences share
// one session, so the derivation yields sixty-five edges — above the
// per-operation cap. The command refuses actionably and commits NO links
// (read-back empty), rather than paginating (a deferred follow-up).
func TestLineageOverCapRefusesAndCommitsNothing(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	raw := readProductionClaudeFixture(t, "session_start_2_1_222.json", "SessionStart")

	const deliveries = 66 // 66 occurrences -> 65 predecessor edges > 64 cap
	for i := 0; i < deliveries; i++ {
		require.NoError(t, handlers.HookLifecycle(context.Background(), handlers.HookLifecycleInput{
			DBPath: dbPath, Harness: "claude-code", Event: "SessionStart", HostVersion: "2.1.222",
			Input: bytes.NewReader(raw), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{},
		}))
	}

	var out bytes.Buffer
	code, err := handlers.HookLifecycleLineage(context.Background(), &out, handlers.HookLifecycleLineageInput{
		DBPath:     dbPath,
		Binding:    "session:session_id=3696b790-3973-49f2-b156-9d82146bf7ec",
		Clock:      lifecycleCLIClock{},
		Operations: lifecycleCLIOperations{},
	}, "text")
	require.Error(t, err)
	require.NotEqual(t, 0, code)
	require.NotEqual(t, 2, code, "no lifecycle path may emit exit code 2")
	require.Contains(t, err.Error(), "narrow the scope with --binding")
	require.Empty(t, out.String(), "an over-cap refusal must print nothing")
	require.Zero(t, countLinkEvidence(t, dbPath), "an over-cap refusal must commit no links (read-back empty)")
}
