package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// This file drives `pasture prune` through the BUILT BINARY, which is the
// command a user runs. Nothing here calls the handler directly.

// pruneReportView is the JSON shape the prune prints.
type pruneReportView struct {
	OrphanPayloadBlobsReclaimed     int64    `json:"orphanPayloadBlobsReclaimed"`
	OrphanPayloadBlobBytesReclaimed int64    `json:"orphanPayloadBlobBytesReclaimed"`
	Batches                         int      `json:"batches"`
	Truncated                       bool     `json:"truncated"`
	VacuumRequested                 bool     `json:"vacuumRequested"`
	Vacuumed                        bool     `json:"vacuumed"`
	VacuumFailure                   string   `json:"vacuumFailure"`
	Note                            string   `json:"note"`
	Advice                          []string `json:"advice"`
}

func runPrune(t *testing.T, binary, dbPath string, extra ...string) (string, string) {
	t.Helper()
	args := append([]string{databaseFlagName.Argument(), dbPath, "prune"}, extra...)
	command := exec.Command(binary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	require.NoError(t, command.Run(), stdout.String()+stderr.String())
	return stdout.String(), stderr.String()
}

// TestPruneThroughTheBuiltBinaryReclaimsOrphansAndKeepsRecordedEvidence is the
// end-to-end proof of the operator verb: a store holds one blob a recorded
// occurrence names and several orphans an abandoned invocation left, and the
// prune deletes exactly the orphans, keeps the recorded blob, and says what it
// kept and what was lost. A read command afterwards proves the recorded
// occurrence's digest still resolves, so the store is still readable.
func TestPruneThroughTheBuiltBinaryReclaimsOrphansAndKeepsRecordedEvidence(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_261.json"))
	require.NoError(t, err)
	ingest := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle",
		"--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.261")
	ingest.Stdin = bytes.NewReader(raw)
	require.NoError(t, ingest.Run())
	_, recorded := payloadBlobRows(t, dbPath)
	require.Len(t, recorded, 1, "one recorded occurrence names one blob")

	const abandoned = 5
	seedLegacyOrphans(t, dbPath, abandoned)

	stdout, stderr := runPrune(t, binary, dbPath)
	assert.Empty(t, stderr, "a prune that reclaimed orphans reports nothing on standard error")
	assert.Contains(t, stdout, "orphan payload blobs reclaimed: 5\n",
		"every orphan stamped an hour ago is older than the writer window, so the run reclaims all five")
	assert.Contains(t, stdout, handlers.PruneRetentionNote,
		"the sentence that says what was deleted and what was kept must reach the operator who ran a delete")

	count, digests := payloadBlobRows(t, dbPath)
	assert.Equal(t, 1, count, "only the blob a recorded occurrence names survives")
	assert.Contains(t, digests, recorded[0], "the recorded occurrence's blob is never pruned")

	// The read command rebuilds the projection and re-inserts the occurrence's
	// digest against the blob table's foreign key, so this succeeds only while
	// the recorded blob is still present.
	list := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list")
	var listOut, listErr bytes.Buffer
	list.Stdout = &listOut
	list.Stderr = &listErr
	require.NoError(t, list.Run(), listOut.String()+listErr.String())
	assert.Empty(t, listErr.String(), "the store is still readable after a prune")
}

// TestPruneJSONCarriesTheSameFactsAndSaysTheFileHasNotShrunk: the machine reader
// gets the reclaimed counts and the honest note that the file only shrinks with
// VACUUM.
func TestPruneJSONCarriesTheSameFactsAndSaysTheFileHasNotShrunk(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	seedLegacyOrphans(t, dbPath, 3)

	stdout, _ := runPrune(t, binary, dbPath, "--format", "json")
	var view pruneReportView
	require.NoError(t, json.Unmarshal([]byte(stdout), &view))
	// The three seeded rows share one stamp, and the prune never deletes the
	// newest row, so exactly one of the tied rows survives and two are
	// reclaimed. A one-row store is never emptied by a prune.
	assert.Equal(t, int64(2), view.OrphanPayloadBlobsReclaimed)
	assert.Greater(t, view.OrphanPayloadBlobBytesReclaimed, int64(0))
	assert.False(t, view.Truncated)
	assert.False(t, view.VacuumRequested)
	assert.False(t, view.Vacuumed)
	assert.Equal(t, handlers.PruneRetentionNote, view.Note,
		"the meaning travels with the numbers in JSON too")
	assert.Contains(t, view.Advice, handlers.PruneVacuumAdvice,
		"a run without --vacuum must say the file has not shrunk yet")
}

// TestPruneWithVacuumRunsVacuum is the space-reclamation path: --vacuum runs
// VACUUM after the reclaim and reports it succeeded.
func TestPruneWithVacuumRunsVacuum(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	seedLegacyOrphans(t, dbPath, 3)

	stdout, stderr := runPrune(t, binary, dbPath, "--vacuum", "--format", "json")
	assert.Empty(t, stderr, "a successful VACUUM says nothing on standard error: %s", stderr)
	var view pruneReportView
	require.NoError(t, json.Unmarshal([]byte(stdout), &view))
	assert.True(t, view.VacuumRequested)
	assert.True(t, view.Vacuumed, "VACUUM must run when asked, got failure %q", view.VacuumFailure)
	assert.Empty(t, view.VacuumFailure)
}

// TestPruneRefusesAnUnknownFormatNamesTheValueAndTheAcceptedValues pins the
// refusal an operator meets when they mistype --format.
func TestPruneRefusesAnUnknownFormat(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "prune", "--format", "yamll")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	require.Error(t, err, "a format this command cannot print must be refused, not printed as text")
	assert.Empty(t, stdout.String(), "a refused prune deletes nothing and prints no report")
	assert.Contains(t, stderr.String(), `"yamll"`, "the refusal must echo the value it refused")
	assert.Contains(t, stderr.String(), "accepted values are text and json")
}

// TestPruneHelpSaysWhatIsKeptAndWhatIsLost pins the help text, because it is
// the operator's contract for a destructive command: the command deletes
// orphan payload blobs and nothing a recorded occurrence names.
func TestPruneHelpSaysWhatIsKeptAndWhatIsLost(t *testing.T) {
	t.Parallel()
	command := exec.Command(lifecycleBinary(t), "prune", "--help")
	var stdout bytes.Buffer
	command.Stdout = &stdout
	require.NoError(t, command.Run())
	help := stdout.String()
	assert.Contains(t, help, "Reclaim the orphan payload blobs",
		"the first line must say the command reclaims")
	assert.Contains(t, help, "recorded occurrence names",
		"the help must state what is kept, or a reader cannot tell a delete from data loss")
	assert.Contains(t, help, "never deletes", "the help must say the command refuses to delete recorded evidence")
	assert.Contains(t, help, "--vacuum",
		"the help must name the flag that actually shrinks the file")
	assert.Contains(t, help, "VACUUM",
		"the help must say a file only shrinks with VACUUM")
	assert.Contains(t, help, "HYGIENE RECLAIM AND NOT A CAPACITY FIX",
		"the help must not let a reader believe this bounds a store whose blobs are all referenced")
	assert.Contains(t, help, "reclaims nothing",
		"the help must say plainly that a fully-referenced store loses nothing to this command")
}
