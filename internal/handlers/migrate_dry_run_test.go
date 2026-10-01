package handlers_test

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

// v8AssignmentIndexDDL is the retired assignment-index schema a version-8
// file carries, so the dry run is taken against a file that really has
// something to plan. The names match internal/audit/migrate_v8_v9.go.
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
CREATE TABLE pasture_session_claim (
	harness TEXT NOT NULL, session TEXT NOT NULL, actor TEXT NOT NULL, claimed_at INTEGER NOT NULL,
	PRIMARY KEY (harness, session));
INSERT INTO pasture_session_claim VALUES('claude-code','dry-1','actor-1',1);
`

// makeVersionEightFile writes a version-8 file carrying the retired
// assignment-index schema and returns its path.
func makeVersionEightFile(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE audit_schema_meta (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
		INSERT INTO audit_schema_meta(version,applied_at) VALUES(8,1);` + v8AssignmentIndexDDL)
	require.NoError(t, err)
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

// TestMigrateDryRunLeavesAVersionEightFileUnchanged is the dry-run proof for
// the v8 → v9 step. `pasture migrate --dry-run` on a version-8 file must list
// the step, say what it removes, what it keeps and what the new floor costs an
// older binary, and leave the file's bytes exactly as they were: the whole
// point of a dry run is that the file the operator is being shown is not the
// file that changed.
func TestMigrateDryRunLeavesAVersionEightFileUnchanged(t *testing.T) {
	t.Parallel()
	dbPath := makeVersionEightFile(t)
	before := sha256Of(t, dbPath)

	var out bytes.Buffer
	code, err := handlers.Migrate(&out, handlers.MigrateInput{DBPath: dbPath, DryRun: true}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code, "a dry run exits 0")

	printed := out.String()
	require.Contains(t, printed, "v8->v9", "the plan must list the step:\n%s", printed)
	require.Contains(t, printed, "remove the retired gate assignment-index tables and triggers; session claims are kept",
		"the plan must say what the step removes and what it keeps:\n%s", printed)
	require.Contains(t, printed, "an older pasture binary will then refuse this database for every command until it is upgraded",
		"the plan must name the consequence of the new floor, because the migrator runs on every open "+
			"and the operator is not shown this output before the upgrade happens:\n%s", printed)
	require.Contains(t, printed, "(v8 -> v9)", "the plan must span v8 to v9:\n%s", printed)
	require.Contains(t, printed, "Dry run:", "the output must be marked as a dry run:\n%s", printed)

	require.Equal(t, before, sha256Of(t, dbPath), "a dry run must not write a single byte to the file")
}

// TestMigrateDryRunOnAVersionEightFileLeavesTheRetiredRelationsInPlace is the
// same promise read off the file's schema rather than its bytes: the retired
// relations the step would remove are still on disk after the dry run, and the
// recorded version is still 8. A byte-identical file and a schema-identical
// file are the same guarantee, and this one names the relations.
func TestMigrateDryRunOnAVersionEightFileLeavesTheRetiredRelationsInPlace(t *testing.T) {
	t.Parallel()
	dbPath := makeVersionEightFile(t)

	var out bytes.Buffer
	_, err := handlers.Migrate(&out, handlers.MigrateInput{DBPath: dbPath, DryRun: true}, types.OutputText)
	require.NoError(t, err)

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
		require.Equal(t, 1, count, "a dry run must leave %q on the file", name)
	}

	var version int
	require.NoError(t, db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version))
	require.Equal(t, 8, version, "a dry run must not record a version")

	var claims int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pasture_session_claim`).Scan(&claims))
	require.Equal(t, 1, claims)
}

// TestMigrateApplyUpgradesAVersionEightFile is the apply half, through the
// handler the CLI delegates to. The first run reports v8 → v9, the second
// reports v9 → v9 because an up-to-date file is a no-op, and the retired
// relations are gone after the first.
func TestMigrateApplyUpgradesAVersionEightFile(t *testing.T) {
	t.Parallel()
	dbPath := makeVersionEightFile(t)

	var first bytes.Buffer
	code, err := handlers.Migrate(&first, handlers.MigrateInput{DBPath: dbPath}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Equal(t, "migrated "+dbPath+" from v8 to v9", strings.TrimSpace(first.String()))

	var second bytes.Buffer
	code, err = handlers.Migrate(&second, handlers.MigrateInput{DBPath: dbPath}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Equal(t, "migrated "+dbPath+" from v9 to v9", strings.TrimSpace(second.String()),
		"a second run on an up-to-date file is a no-op that reports the same version twice")

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
		require.Zero(t, count, "retired relation %q survived the apply", name)
	}
	var claims int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pasture_session_claim WHERE session='dry-1'`).Scan(&claims))
	require.Equal(t, 1, claims, "session claims survive the apply")
}
