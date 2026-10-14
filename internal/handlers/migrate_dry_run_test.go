package handlers_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// v8AssignmentIndexDDL is the retired assignment-index schema a version-8
// file carries, so the dry run is taken against a file that really has
// something to plan. The names match internal/audit/migrate_v8_v9.go. The
// session-claim table is created IF NOT EXISTS because the migrator already
// created it at an earlier version and the v8→v9 step keeps it.
const v8AssignmentIndexDDL = `
CREATE TABLE pasture_actor_assignment (
	assignment_id TEXT PRIMARY KEY, actor_id TEXT NOT NULL, task_id TEXT NOT NULL,
	role TEXT NOT NULL, authority_journal_id INTEGER NOT NULL, generation INTEGER NOT NULL DEFAULT 0);
CREATE INDEX idx_pasture_actor_assignment_actor ON pasture_actor_assignment (actor_id);
CREATE TABLE pasture_actor_assignment_watermark (
	singleton_id INTEGER PRIMARY KEY, last_indexed_jid INTEGER NOT NULL);
CREATE TABLE pasture_actor_assignment_state (
	singleton_id INTEGER PRIMARY KEY, generation INTEGER NOT NULL, state_revision INTEGER NOT NULL,
	destructive_revision INTEGER NOT NULL, certified_destructive_revision INTEGER NOT NULL,
	coverage_status TEXT NOT NULL, in_progress_snapshot_jid INTEGER NOT NULL,
	in_progress_after_jid INTEGER NOT NULL, completed_through_jid INTEGER NOT NULL, recovery_complete INTEGER NOT NULL);
CREATE TABLE pasture_assignment_recovery_scan (
	generation INTEGER NOT NULL, snapshot_jid INTEGER NOT NULL, kind TEXT NOT NULL, identity TEXT NOT NULL,
	after_jid INTEGER NOT NULL, complete INTEGER NOT NULL, PRIMARY KEY(generation,snapshot_jid,kind,identity));
CREATE TABLE pasture_assignment_recovery_cache (
	generation INTEGER NOT NULL, snapshot_jid INTEGER NOT NULL, kind TEXT NOT NULL, identity TEXT NOT NULL,
	journal_id INTEGER NOT NULL, value BLOB NOT NULL, PRIMARY KEY(generation,snapshot_jid,kind,identity,journal_id));
CREATE TABLE pasture_assignment_recovery_member (
	generation INTEGER NOT NULL, snapshot_jid INTEGER NOT NULL, producer TEXT NOT NULL,
	assignment_id TEXT NOT NULL, task_id TEXT NOT NULL, actor_id TEXT NOT NULL, seen INTEGER NOT NULL,
	PRIMARY KEY(generation,snapshot_jid,producer,assignment_id));
CREATE TRIGGER pasture_assignment_deleted AFTER DELETE ON pasture_actor_assignment BEGIN SELECT 1; END;
CREATE TRIGGER pasture_assignment_changed AFTER UPDATE ON pasture_actor_assignment BEGIN SELECT 1; END;
CREATE TABLE IF NOT EXISTS pasture_session_claim (
	harness TEXT NOT NULL, session TEXT NOT NULL, actor TEXT NOT NULL, claimed_at INTEGER NOT NULL,
	PRIMARY KEY (harness, session));
INSERT OR REPLACE INTO pasture_session_claim VALUES('claude-code','dry-1','actor-1',1);
`

// makeVersionEightFile writes a COMPLETE version-8 file: the real audit schema
// through the v8→v9 step's predecessor, plus the retired assignment-index schema
// the v8→v9 step removes, plus one session claim. It returns its path.
func makeVersionEightFile(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	db, err := dbconn.OpenDefaultDB(dbPath)
	require.NoError(t, err)
	require.NoError(t, audit.MigrateContextToCeiling(context.Background(), db, 8),
		"the version 8 schema must be built through the real migrator")
	_, err = db.Exec(v8AssignmentIndexDDL)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	return dbPath
}

// sha256Of returns the file's digest as hex, so a dry run that wrote a single
// byte is caught.
func sha256Of(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// TestMigrateDryRunRefusesANonNormalStoreAndLeavesTheFileUnchanged: plain
// `pasture migrate` keeps its schema meaning only for a NORMAL store (complete
// fence evidence). A version-8 file carries no slash marker and no cutover
// receipt, so the read-only precheck refuses it before any step writes, and the
// file's bytes are untouched.
func TestMigrateDryRunRefusesANonNormalStoreAndLeavesTheFileUnchanged(t *testing.T) {
	t.Parallel()
	dbPath := makeVersionEightFile(t)
	before := sha256Of(t, dbPath)

	var out bytes.Buffer
	code, err := handlers.Migrate(&out, handlers.MigrateInput{DBPath: dbPath, DryRun: true}, types.OutputText)
	require.Error(t, err, "a non-NORMAL store must be refused")
	require.Equal(t, 5, code, "the refusal is a storage error")
	require.Contains(t, err.Error(), "has not been cut over to the slash identity format",
		"the refusal must name the identity cutover:\n%s", err)

	require.Equal(t, before, sha256Of(t, dbPath), "a refused dry run must not write a single byte to the file")
}

// TestMigrateRefusesANonNormalStoreLeavingTheRetiredRelationsInPlace is the
// same refusal read off the file's schema rather than its bytes: the retired
// relations the v8→v9 step would remove are still on disk, and the recorded
// version is still 8.
func TestMigrateRefusesANonNormalStoreLeavingTheRetiredRelationsInPlace(t *testing.T) {
	t.Parallel()
	dbPath := makeVersionEightFile(t)

	var out bytes.Buffer
	_, err := handlers.Migrate(&out, handlers.MigrateInput{DBPath: dbPath, DryRun: true}, types.OutputText)
	require.Error(t, err)

	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

	for _, name := range []string{
		"pasture_assignment_changed", "pasture_assignment_deleted",
		"pasture_assignment_recovery_member", "pasture_assignment_recovery_cache",
		"pasture_assignment_recovery_scan", "pasture_actor_assignment_state",
		"pasture_actor_assignment_watermark", "pasture_actor_assignment",
		"idx_pasture_actor_assignment_actor",
	} {
		var count int
		require.NoError(t, db.QueryRow(
			`SELECT count(*) FROM sqlite_schema WHERE name = ? AND type IN ('table','index','trigger')`, name,
		).Scan(&count), "probe %q", name)
		require.Equal(t, 1, count, "a refused run must leave %q on the file", name)
	}

	var version int
	require.NoError(t, db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version))
	require.Equal(t, 8, version, "a refused run must not record a version")

	var claims int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pasture_session_claim`).Scan(&claims))
	require.Equal(t, 1, claims)
}

// TestMigrateApplyRefusesANonNormalStoreAndTheCutoverRunsTheStep: the apply
// half. Plain apply refuses a version-8 file before any write; the v8→v9 step
// still runs, but through the identity cutover's audit catch-up, which the
// production opener performs. After the cutover the retired relations are gone,
// the recorded version is the ceiling, and the session claim survives.
func TestMigrateApplyRefusesANonNormalStoreAndTheCutoverRunsTheStep(t *testing.T) {
	t.Parallel()
	dbPath := makeVersionEightFile(t)

	var first bytes.Buffer
	code, err := handlers.Migrate(&first, handlers.MigrateInput{DBPath: dbPath}, types.OutputText)
	require.Error(t, err, "plain apply must refuse a non-NORMAL store")
	require.Equal(t, 5, code)

	// The production opener cuts the store over: its catch-up runs the v8→v9
	// step and its conversion publishes the marker, receipt and ceiling.
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())

	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()
	for _, name := range []string{
		"pasture_assignment_changed", "pasture_assignment_deleted",
		"pasture_assignment_recovery_member", "pasture_assignment_recovery_cache",
		"pasture_assignment_recovery_scan", "pasture_actor_assignment_state",
		"pasture_actor_assignment_watermark", "pasture_actor_assignment",
		"idx_pasture_actor_assignment_actor",
	} {
		var count int
		require.NoError(t, db.QueryRow(
			`SELECT count(*) FROM sqlite_schema WHERE name = ? AND type IN ('table','index','trigger')`, name,
		).Scan(&count), "probe %q", name)
		require.Zero(t, count, "retired relation %q survived the cutover", name)
	}
	var version int
	require.NoError(t, db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version))
	require.Equal(t, audit.MaxKnownSchemaVersion, version, "the cutover raises the ceiling")
	var claims int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pasture_session_claim WHERE session='dry-1'`).Scan(&claims))
	require.Equal(t, 1, claims, "session claims survive the cutover")
}
