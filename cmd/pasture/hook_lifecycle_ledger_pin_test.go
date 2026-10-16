package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// readVerifiedLedgerCount returns the full-verification counter from the
// provenance verified-as-of ledger. Only a whole-store verification may move
// it; a sanctioned write that advances the ledger leaves it alone.
func readVerifiedLedgerCount(t *testing.T, dbPath string) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err, "open the store to read the verified ledger")
	defer func() { require.NoError(t, db.Close()) }()
	var count int64
	require.NoError(t, db.QueryRow(`SELECT verified_count FROM provenance_verified_store WHERE id = 1`).Scan(&count),
		"read the verified ledger's full-verification count")
	return count
}

// readLedgerAndStoreIdentity returns the ledger's recorded identity alongside
// the live store identity, so a test can assert the write advanced the ledger
// to the state it committed rather than merely leaving the counter alone.
func readLedgerAndStoreIdentity(t *testing.T, dbPath string) (ledgerGeneration, ledgerTail, storeGeneration, storeTail int64) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err, "open the store to read the ledger and store identity")
	defer func() { require.NoError(t, db.Close()) }()
	require.NoError(t, db.QueryRow(`SELECT generation, journal_id FROM provenance_verified_store WHERE id = 1`).Scan(&ledgerGeneration, &ledgerTail),
		"read the ledger's recorded generation and journal tail")
	require.NoError(t, db.QueryRow(`SELECT generation FROM provenance_store_generation WHERE id = 1`).Scan(&storeGeneration),
		"read the store's live generation counter")
	require.NoError(t, db.QueryRow(`SELECT COALESCE(MAX(journal_id), 0) FROM journal`).Scan(&storeTail),
		"read the journal tail the ledger must name")
	return ledgerGeneration, ledgerTail, storeGeneration, storeTail
}

// TestTheDaemonLaneBatchAdvancesTheVerifiedLedgerSoTheNextOpenIsFast pins the
// daemon lane's commit against the verified-as-of ledger.
//
// WHAT IT DRIVES. A SessionStart hook is sent through a real in-process daemon
// lane over a private socket — the production hook command, the production
// server, the production lane queue, and the lane's one batch commit
// (JournalAppender.AppendBatch, which folds through ApplyBatchContext in one
// transaction). See cmd/pastured/lane.go for the wiring and
// https://github.com/dayvidpham/provenance/pull/60 for the advance it relies
// on: a sanctioned write that finds the ledger matching advances it in its own
// commit, so the next open matches instead of re-verifying the whole store.
//
// WHAT IT ASSERTS. Immediately after the served write the ledger names the
// live store (generation and journal tail) with the full-verification count
// untouched, and a fresh open afterwards — the CLI read that follows a CLI or
// hook write in production — leaves that count untouched too. An out-of-band
// write still moves the generation without advancing the ledger, so the same
// fresh open would then run the full path and the count would move; deleting
// the advance turns both assertions red.
func TestTheDaemonLaneBatchAdvancesTheVerifiedLedgerSoTheNextOpenIsFast(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	// Establish a current ledger first, so the baseline below is the state a
	// verified store opens from rather than the stale state the bootstrap
	// write may have left behind on an older ledger version.
	bootstrap, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err, "open the seeded store to establish a current ledger")
	require.NoError(t, bootstrap.Close(), "close the seeded store after establishing the ledger")
	baseline := readVerifiedLedgerCount(t, dbPath)

	socketPath := startHookTestWriteLane(t, dbPath)

	raw := readProductionClaudeFixture(t, "session_start_2_1_261.json", "SessionStart")
	t.Setenv(hookWriteLaneEnv, string(WriteLaneAuto))
	t.Setenv(hookWriteLaneSocketEnv, socketPath)
	cmd := lifecycleTestCommand(t, "claude-code", "SessionStart", "2.1.261", dbPath)
	cmd.SetIn(strings.NewReader(string(raw)))
	outcome := lifecycleTestOutcome(t, cmd, nil,
		handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile(), context.WithTimeout)
	require.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"the served hook must succeed so the ledger assertions below read a committed write")

	ledgerGeneration, ledgerTail, storeGeneration, storeTail := readLedgerAndStoreIdentity(t, dbPath)
	require.Equal(t, storeGeneration, ledgerGeneration,
		"the served batch must advance the ledger's generation to the store it committed")
	require.Equal(t, storeTail, ledgerTail,
		"the served batch must advance the ledger's journal tail to the batch it committed")
	require.Equal(t, baseline, readVerifiedLedgerCount(t, dbPath),
		"advancing the ledger must not count as a full verification")

	next, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err, "open the store after the served batch")
	rows := queryLifecycleEvidence(t, next.Journal(), occurrenceEvidenceKind)
	require.Len(t, rows, 1, "the served path must have committed exactly one occurrence through the daemon's lane")
	require.NoError(t, next.Close(), "close the store after the next open")

	require.Equal(t, baseline, readVerifiedLedgerCount(t, dbPath),
		"the open after a served batch must take the fast path without a whole-store verification")
}
