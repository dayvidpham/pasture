// Package main_test — migrate_v8_v9_cli_test.go
//
// The built-binary proof for the version 8 → version 9 audit migration. The
// audit-package tests drive the migrator through handles they open themselves;
// this file drives it the way an operator does, by running the compiled
// `pasture` binary against a real version-8 file and reading what it prints.
//
// Two lines are the whole contract:
//
//	migrated <db-path> from v8 to v9   (first run, the upgrade happens)
//	migrated <db-path> from v9 to v9   (second run, nothing left to do)
//
// A user who upgrades pasture and never runs `pasture migrate` still gets the
// upgrade on the next command that opens the store, so the no-op line matters
// as much as the first one: it is what a script that runs migrate
// unconditionally sees on every run after the first.
package main_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
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

// makeVersionEightStore writes a version-8 file carrying the retired
// assignment-index schema and the session-claim table the step keeps, and
// returns its path.
func makeVersionEightStore(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ddl := `
		CREATE TABLE audit_schema_meta (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
		INSERT INTO audit_schema_meta(version,applied_at) VALUES(8,1);
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
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("build the version 8 fixture: %v", err)
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

// TestCLI_Migrate_UpgradesAVersionEightFile_AndIsANoopAfterward runs the built
// binary twice over one version-8 file. The first run prints the upgrade line
// and leaves no retired relation behind; the second prints the same version
// twice, because an already-current file is a no-op rather than an error.
func TestCLI_Migrate_UpgradesAVersionEightFile_AndIsANoopAfterward(t *testing.T) {
	t.Parallel()
	dbPath := makeVersionEightStore(t)

	first := runCLI(t, "migrate", "--db", dbPath, "--format", "text")
	if first.exitCode != 0 {
		t.Fatalf("migrate exit %d; stdout=%q stderr=%q", first.exitCode, first.stdout, first.stderr)
	}
	if want := "migrated " + dbPath + " from v8 to v9"; strings.TrimSpace(first.stdout) != want {
		t.Errorf("first migrate printed %q, want %q", strings.TrimSpace(first.stdout), want)
	}
	for _, name := range retiredAssignmentIndexRelations {
		if got := relationCountOn(t, dbPath, name); got != 0 {
			t.Errorf("retired relation %q survived the upgrade (count=%d)", name, got)
		}
	}

	second := runCLI(t, "migrate", "--db", dbPath, "--format", "text")
	if second.exitCode != 0 {
		t.Fatalf("second migrate exit %d; stdout=%q stderr=%q", second.exitCode, second.stdout, second.stderr)
	}
	if want := "migrated " + dbPath + " from v9 to v9"; strings.TrimSpace(second.stdout) != want {
		t.Errorf("second migrate printed %q, want %q", strings.TrimSpace(second.stdout), want)
	}
}

// TestCLI_Migrate_DryRun_LeavesTheFileUntouched is the built binary's dry run.
// It must print the version 8 → version 9 plan, name both halves of what the
// step does, and change the file's bytes not at all.
func TestCLI_Migrate_DryRun_LeavesTheFileUntouched(t *testing.T) {
	t.Parallel()
	dbPath := makeVersionEightStore(t)
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}

	out := runCLI(t, "migrate", "--dry-run", "--db", dbPath, "--format", "text")
	if out.exitCode != 0 {
		t.Fatalf("migrate --dry-run exit %d; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}
	if !strings.Contains(out.stdout, "v8->v9: remove the retired gate assignment-index tables and triggers; session claims are kept") {
		t.Errorf("dry run must print the version 8 to 9 step and its description; stdout=%q", out.stdout)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("re-read the fixture: %v", err)
	}
	if string(after) != string(raw) {
		t.Error("a dry run must not change the database file")
	}
	for _, name := range retiredAssignmentIndexRelations {
		if got := relationCountOn(t, dbPath, name); got != 1 {
			t.Errorf("a dry run left %d copies of %q, want 1", got, name)
		}
	}
}

// TestCLI_AnyCommand_UpgradesAVersionEightFileOnOpen is the auto-on-open half.
// An operator who never runs `pasture migrate` must still land on version 9
// the first time any command opens the store, because the audit migrator runs
// inside the open.
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
	if version != 9 {
		t.Errorf("recorded version=%d after an ordinary command opened the store, want 9", version)
	}
}
