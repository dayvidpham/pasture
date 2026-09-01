package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// This file is the orphan-count read surface: the number an operator gets from
// `pasture hook lifecycle orphans`, and the sentence that ships with it.
//
// The count is the LEGAL side of the boundary this command's failure-mode
// tests prove. Those tests prove that no occurrence ever names an absent blob,
// which is the corrupting state. This one reports the other side: a blob that
// no occurrence names, which is legal, expected, and reclaimable. Both numbers
// come from one predicate, so the count can never disagree with the invariant.

// runLifecycleOrphans runs the orphan report through the BUILT BINARY, which is
// the command a user runs. Nothing here calls the handler directly.
func runLifecycleOrphans(t *testing.T, binary, dbPath, format string) string {
	t.Helper()
	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "orphans", "--format", format)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	require.NoError(t, command.Run(), stdout.String()+stderr.String())
	require.Empty(t, stderr.String(), "the orphan report is an ordinary read; it must not warn")
	return stdout.String()
}

// leaveOrphanBlobs reproduces what an ABANDONED INVOCATION leaves behind, using
// the production write itself. receipt.SQLiteBlobStore.Put is the FIRST of the
// two durable writes a hook performs; an invocation abandoned after it and
// before the journal append leaves exactly this state, one blob per abandoned
// invocation. Nothing here is a fixture of the count: the count reads the store
// afterwards and does not know how the blob arrived.
func leaveOrphanBlobs(t *testing.T, dbPath string, count int) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	blobs, err := tasks.NewLifecycleBlobStore(tracker)
	require.NoError(t, err)
	for i := 0; i < count; i++ {
		body := []byte(fmt.Sprintf(`{"abandoned-invocation":%d}`, i))
		require.NoError(t, blobs.Put(context.Background(), digest.FromBytes(body), body))
	}
}

// TestOrphanCountIsZeroOnACleanStoreAndTrueAfterAbandonedInvocations is the
// measurement itself, taken through the built binary in both renderers.
//
// A SUCCESSFUL invocation is ingested first, on purpose. Its blob IS named by
// an occurrence, so it must not be counted; a count that reported it would tell
// an operator that ordinary successful work leaves debris behind.
func TestOrphanCountIsZeroOnACleanStoreAndTrueAfterAbandonedInvocations(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "orphan-cli")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	assert.Contains(t, runLifecycleOrphans(t, binary, dbPath, "text"), "orphan payload blobs: 0\n",
		"a clean store has no unnamed payload blob, and the report must say so rather than stay silent")

	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_222.json"))
	require.NoError(t, err)
	ingest := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle",
		"--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.222")
	ingest.Stdin = bytes.NewReader(raw)
	require.NoError(t, ingest.Run())

	assert.Contains(t, runLifecycleOrphans(t, binary, dbPath, "text"), "orphan payload blobs: 0\n",
		"a COMMITTED invocation names its own blob, so successful work must never be counted as an orphan")

	const abandoned = 3
	leaveOrphanBlobs(t, dbPath, abandoned)

	assert.Contains(t, runLifecycleOrphans(t, binary, dbPath, "text"), "orphan payload blobs: 3\n",
		"one orphan arises per abandoned invocation, so three abandonments must read as three")

	var view struct {
		Count int64  `json:"orphanPayloadBlobs"`
		Note  string `json:"note"`
	}
	require.NoError(t, json.Unmarshal([]byte(runLifecycleOrphans(t, binary, dbPath, "json")), &view))
	assert.Equal(t, int64(abandoned), view.Count, "both renderers must report one number")
	assert.Equal(t, handlers.OrphanPayloadNote, view.Note,
		"the meaning travels with the number in JSON too: a dashboard that renders the count alone "+
			"reproduces the misreading the sentence exists to prevent")
}

// TestOrphanCountAgreesWithTheEnumerationTheInvariantTestUses is the agreement
// this number owes. The abandonment invariant enumerates the orphan set with
// receipt.SQLiteBlobStore.Reclaimable; if the count answered a different
// question, the operator surface and the proof would describe different stores
// and the operator's would be the one nobody checked.
func TestOrphanCountAgreesWithTheEnumerationTheInvariantTestUses(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	leaveOrphanBlobs(t, dbPath, 4)

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
	blobs, err := tasks.NewLifecycleBlobStore(tracker)
	require.NoError(t, err)

	enumerated, err := blobs.Reclaimable(context.Background(), receipt.MaxReclaimablePayloads)
	require.NoError(t, err)
	counted, err := blobs.ReclaimableCount(context.Background())
	require.NoError(t, err)

	assert.Equal(t, int64(len(enumerated)), counted,
		"the count and the enumeration read one predicate; disagreeing means the operator-facing "+
			"number describes a set no proof covers")
	assert.Equal(t, int64(4), counted)
}

// TestOrphanCountWordingSaysWhatItIsThatItIsExpectedAndWhatALargeNumberMeans
// pins the operator text, because the number is a TRUTH CLAIM and a correct
// number can still teach a false lesson.
//
// The misreading it prevents is specific and expensive: an operator who reads
// a large count and nothing else concludes the store is damaged and starts
// hunting for corruption — for the one state this command's sibling proofs
// show CANNOT exist. Meanwhile the real signal, repeated abandonment caused by
// store contention, goes uninvestigated.
//
// Each of the three claims below is pinned by a phrase, so an edit that
// quietly turns "expected and reclaimable" into something that reads as a fault
// turns this test RED.
func TestOrphanCountWordingSaysWhatItIsThatItIsExpectedAndWhatALargeNumberMeans(t *testing.T) {
	t.Parallel()

	note := handlers.OrphanPayloadNote

	// 1. WHAT AN ORPHAN IS.
	assert.Contains(t, note, "a payload blob that no recorded occurrence names",
		"the reader must be told what the thing being counted IS")
	assert.Contains(t, note, "abandoned between its two durable writes",
		"and where it comes from: the gap between the blob write and the journal append")
	assert.Contains(t, note, "at most one arises per abandoned invocation",
		"the per-invocation bound is what makes the number readable as a count of abandonments")

	// 2. THAT IT IS EXPECTED AND RECLAIMABLE, NOT DAMAGE.
	assert.Contains(t, note, "expected and reclaimable, not damage",
		"without this the count reads as a defect report")
	assert.Contains(t, note, "a journal row naming an absent blob could not be repaired at all",
		"the reader must be told WHY this order was chosen, or 'not damage' is an assertion to take on trust")

	// 3. WHAT A LARGE NUMBER MEANS.
	assert.Contains(t, note, "does not mean the store is corrupt",
		"the misreading must be refused by name, not merely left unstated")
	assert.Contains(t, note, "the thing to investigate is the store contention that caused the abandonment",
		"the reader must leave with the action to take, which is the whole value of the number")

	// The words that would make it a fault report must not appear.
	for _, forbidden := range []string{"corruption detected", "damaged", "data loss", "inconsistent"} {
		assert.NotContains(t, strings.ToLower(note), forbidden,
			"the note must not read as a fault report: %q sends the operator hunting for a state that cannot exist", forbidden)
	}
}

// TestTheOrphanCountIsNotOnTheHookPath is the placement guard.
//
// It is structural because the hazard is structural. On the hook path the count
// would run inside the hook-invocation deadline, and it READS THE STORE, which
// is the resource that contends. So it would be slowest under exactly the
// condition that produces orphans, and a slow enough count would push the
// invocation into its deadline and leave ONE MORE ORPHAN. The counter would
// become a cause of the thing it counts, and worst precisely when it matters
// most. The read command is the only home where the measurement cannot perturb
// the measurement.
func TestTheOrphanCountIsNotOnTheHookPath(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"hook_lifecycle.go", "hook_lifecycle_raw.go", "hook_lifecycle_context.go", "hook_lifecycle_lineage.go"} {
		body, err := os.ReadFile(name)
		require.NoError(t, err, "every hook-path source must be readable")
		assert.NotContains(t, string(body), "ReclaimableCount",
			"%s is on the hook path: counting orphans there spends the invocation deadline reading the "+
				"very store that contends, and a slow enough count leaves one more orphan behind", name)
	}

	handler, err := os.ReadFile(filepath.Join("..", "..", "internal", "handlers", "hook_lifecycle.go"))
	require.NoError(t, err)
	assert.NotContains(t, string(handler), "ReclaimableCount",
		"the lifecycle dispatch handler is the hook path; the orphan count belongs on the read surface")
}
