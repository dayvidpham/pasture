// Package main_test — migrate_v8_v9_cli_test.go
//
// The built-binary proof for the version-8 forward audit migrations (the
// v8 → v9 assignment-index removal and the v9 → v10 identity-format ceiling).
// The audit-package tests drive the migrator through handles they open
// themselves; this file drives it the way an operator does, by running the
// compiled `pasture` binary against a real version-8 file and reading what it
// prints.
//
// Two lines are the whole contract now:
//
//	migrate on a non-NORMAL store  → exit 5, the identity-cutover refusal, and
//	                                 no step writes
//	migrated <db-path> from v10 to v10 (a NORMAL store, nothing left to do)
//
// The version-8 file carries no slash marker and no cutover receipt, so it is
// non-NORMAL and plain `pasture migrate` refuses it; the store's first open
// performs the cutover instead. A user who upgrades pasture and never runs
// `pasture migrate` still gets the upgrade on the next command that opens the
// store, and a script that runs migrate unconditionally on the cut-over store
// sees the same version twice.
package main_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
)

// retiredAssignmentIndexRelations is what the version 8 → version 9 step
// removes, by the names declared in internal/audit/migrate_v8_v9.go.
var retiredAssignmentIndexRelations = []string{
	"pasture_assignment_changed",
	"pasture_assignment_deleted",
	"pasture_assignment_recovery_member",
	"pasture_assignment_recovery_cache",
	"pasture_assignment_recovery_scan",
	"pasture_actor_assignment_state",
	"pasture_actor_assignment_watermark",
	"pasture_actor_assignment",
	"idx_pasture_actor_assignment_actor",
}

// v8EngineStateDDL is the engine-side state a REAL version-8 store carries
// beyond the audit schema: the retired assignment-index relations the v8→v9
// step removes and the session-claim table the step keeps. The audit migrator
// does not create these (internal/tasks owns them), so a fixture built from the
// migrator alone has the audit schema but not this half.
const v8EngineStateDDL = `
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
	INSERT INTO pasture_session_claim VALUES('claude-code','cli-1','actor-1',1);
`

// makeVersionEightStore writes a REAL version-8 file and returns its path. The
// version-8 AUDIT SCHEMA is built through the migrator, so the store carries
// everything version 8 implies — including the lifecycle projection relations
// (lifecycle_payload_blobs, lifecycle_occurrences, lifecycle_occurrence_bindings)
// the v5→v6/v6→v7 steps create and the cutover's replay reads. A hand-written
// `version=8` row on a file that lacks that schema is not a version-8 store: the
// migrator may skip to the v8→v9 step, but every later reader (and the cutover's
// replay) assumes the relations version 8 promises. The engine-side relations
// (the retired assignment index, the session claim) are added on top because the
// migrator does not own them.
func makeVersionEightStore(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	db, err := dbconn.OpenDefaultDB(dbPath)
	if err != nil {
		t.Fatalf("open the fixture: %v", err)
	}
	if err := audit.MigrateContextToCeiling(context.Background(), db, 8); err != nil {
		t.Fatalf("build the version 8 audit schema: %v", err)
	}
	if _, err := db.Exec(v8EngineStateDDL); err != nil {
		t.Fatalf("build the version 8 engine state: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close the fixture: %v", err)
	}
	return dbPath
}

func relationCountOn(t *testing.T, dbPath, name string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_schema WHERE name = ? AND type IN ('table','index','trigger')`, name,
	).Scan(&count); err != nil {
		t.Fatalf("probe relation %q: %v", name, err)
	}
	return count
}

// TestCLI_Migrate_RefusesANonNormalStoreAndNoOpsOnANormalOne runs the built
// binary against a real version-8 file and then against the same file once it
// has been cut over.
//
// The version-8 file is non-NORMAL (no slash marker, no cutover receipt), so
// plain `pasture migrate` refuses it before any step writes and leaves the
// retired relations in place — the refusal is the ratified contract, and the
// upgrade it used to print now happens on the store's first open instead. Once
// a normal command has cut the store over — raising the ceiling and dropping
// the retired relations — plain `pasture migrate` keeps its schema meaning and
// is a no-op, printing the same version twice.
func TestCLI_Migrate_RefusesANonNormalStoreAndNoOpsOnANormalOne(t *testing.T) {
	t.Parallel()
	dbPath := makeVersionEightStore(t)

	first := runCLI(t, "migrate", "--db", dbPath, "--format", "text")
	if first.exitCode != 5 {
		t.Fatalf("migrate exit %d; stdout=%q stderr=%q", first.exitCode, first.stdout, first.stderr)
	}
	if !strings.Contains(first.stderr, "has not been cut over to the slash identity format") {
		t.Errorf("the refusal must name the identity cutover; stderr=%q", first.stderr)
	}
	for _, name := range retiredAssignmentIndexRelations {
		if got := relationCountOn(t, dbPath, name); got != 1 {
			t.Errorf("a refused migrate left %d copies of %q, want 1 (no step may write)", got, name)
		}
	}

	// A normal command cuts the store over: the first open archives it, runs
	// the deferred catch-up (v8→v9 drops the retired relations), and publishes
	// the marker, receipt and ceiling.
	seed := runCLI(t, "--db", dbPath, "task", "list", "--format", "json")
	if seed.exitCode != 0 {
		t.Fatalf("cutting the store over exit %d; stdout=%q stderr=%q", seed.exitCode, seed.stdout, seed.stderr)
	}
	for _, name := range retiredAssignmentIndexRelations {
		if got := relationCountOn(t, dbPath, name); got != 0 {
			t.Errorf("retired relation %q survived the cutover (count=%d)", name, got)
		}
	}

	// Plain migrate on the NORMAL store is the no-op it now means.
	for run := 0; run < 2; run++ {
		out := runCLI(t, "migrate", "--db", dbPath, "--format", "text")
		if out.exitCode != 0 {
			t.Fatalf("migrate on a NORMAL store exit %d; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
		}
		if want := fmt.Sprintf("migrated %s from v%d to v%d", dbPath, audit.MaxKnownSchemaVersion, audit.MaxKnownSchemaVersion); strings.TrimSpace(out.stdout) != want {
			t.Errorf("migrate no-op printed %q, want %q", strings.TrimSpace(out.stdout), want)
		}
	}
}

// TestCLI_Migrate_DryRun_LeavesTheFileUntouched is the built binary's refusal
// path for a non-NORMAL store: plain `pasture migrate` keeps its schema meaning
// only for a NORMAL store, so a version-8 file (no slash marker, no cutover
// receipt) is refused before any step writes and the file's bytes are untouched.
func TestCLI_Migrate_DryRun_LeavesTheFileUntouched(t *testing.T) {
	t.Parallel()
	dbPath := makeVersionEightStore(t)
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}

	out := runCLI(t, "migrate", "--dry-run", "--db", dbPath, "--format", "text")
	if out.exitCode != 5 {
		t.Fatalf("migrate --dry-run exit %d; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}
	if !strings.Contains(out.stderr, "has not been cut over to the slash identity format") {
		t.Errorf("the refusal must name the identity cutover; stderr=%q", out.stderr)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("re-read the fixture: %v", err)
	}
	if string(after) != string(raw) {
		t.Error("a refused dry run must not change the database file")
	}
	for _, name := range retiredAssignmentIndexRelations {
		if got := relationCountOn(t, dbPath, name); got != 1 {
			t.Errorf("a refused dry run left %d copies of %q, want 1", got, name)
		}
	}
}

// TestCLI_Migrate_DryRun_NamesTheExactFileForALeadingSlashRun is the built
// binary's repro for POSIX paths that begin with "//". On Linux that spelling
// names the same file as the one-slash form, but "file:" + "//tmp/x" parses
// "tmp" as the URI AUTHORITY, which modernc rejects with "invalid uri
// authority"; before the normalisation this dry run exited non-zero and created
// nothing. The store is seeded through a real command first, so the dry run has
// a NORMAL store to read; the normalisation runs before percent-encoding, so the
// "?" and "#" in the file name are still escaped exactly once.
func TestCLI_Migrate_DryRun_NamesTheExactFileForALeadingSlashRun(t *testing.T) {
	t.Parallel()
	exact := filepath.Join(t.TempDir(), "slash?probe#db.db")
	dbPath := "//" + strings.TrimPrefix(exact, "/")

	seed := runCLI(t, "--db", dbPath, "task", "list", "--format", "json")
	if seed.exitCode != 0 {
		t.Fatalf("seeding the store exit %d; stdout=%q stderr=%q", seed.exitCode, seed.stdout, seed.stderr)
	}
	if _, err := os.Stat(exact); err != nil {
		t.Fatalf("the // path must name the exact file %q: %v", exact, err)
	}

	out := runCLI(t, "migrate", "--dry-run", "--db", dbPath, "--format", "text")
	if out.exitCode != 0 {
		t.Fatalf("migrate --dry-run exit %d; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}
	want := fmt.Sprintf("Dry run: %s (v%d -> v%d)", dbPath, audit.MaxKnownSchemaVersion, audit.MaxKnownSchemaVersion)
	if !strings.Contains(out.stdout, want) {
		t.Errorf("dry run stdout=%q, want it to contain %q", out.stdout, want)
	}
}

// TestCLI_AnyCommand_UpgradesAVersionEightFileOnOpen is the auto-on-open half.
// An operator who never runs `pasture migrate` must still land on version 10
// the first time any command opens the store: the open detects the non-NORMAL
// store, archives it, runs the deferred audit catch-up and the identity
// conversion, and raises the ceiling.
func TestCLI_AnyCommand_UpgradesAVersionEightFileOnOpen(t *testing.T) {
	t.Parallel()
	dbPath := makeVersionEightStore(t)

	out := runCLI(t, "--db", dbPath, "--namespace", "demo", "--format", "json",
		"task", "create", "v9 auto-open probe")
	if out.exitCode != 0 {
		t.Fatalf("task create exit %d; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("read the recorded version: %v", err)
	}
	if version != 10 {
		t.Errorf("recorded version=%d after an ordinary command opened the store, want 10", version)
	}
}
