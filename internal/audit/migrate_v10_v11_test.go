package audit

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// Constructed legacy cache schema, not captured host delivery evidence.
func legacySessionCache(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := MigrateContextToCeiling(context.Background(), db, 10); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE pasture_session_state (harness TEXT, session TEXT, last_journal_id INTEGER)`,
		`INSERT INTO pasture_session_state VALUES ('claude-code','s',42)`,
		`CREATE TABLE pasture_session_api_state (singleton_id INTEGER PRIMARY KEY, revision INTEGER, dirty INTEGER, completed_through_jid INTEGER)`,
		`INSERT INTO pasture_session_api_state VALUES (0,7,0,42)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestSessionWatermarkMigrationInvalidatesOnce(t *testing.T) {
	t.Parallel()
	db := legacySessionCache(t)
	for range 2 {
		if err := Migrate(db); err != nil {
			t.Fatal(err)
		}
	}
	var revision, dirty, cursor, version int
	if err := db.QueryRow(`SELECT revision,dirty,completed_through_jid,cache_version FROM pasture_session_api_state`).Scan(&revision, &dirty, &cursor, &version); err != nil {
		t.Fatal(err)
	}
	if revision != 9 || dirty != 1 || cursor != 0 || version != SessionCacheVersion {
		t.Fatalf("migration state = %d/%d/%d/%d", revision, dirty, cursor, version)
	}
	var prompt, promptID, end, endID, resume, resumeID int64
	if err := db.QueryRow(`SELECT last_prompt_at,last_prompt_journal_id,host_end_at,host_end_journal_id,last_resume_at,last_resume_journal_id FROM pasture_session_state`).Scan(&prompt, &promptID, &end, &endID, &resume, &resumeID); err != nil {
		t.Fatal(err)
	}
	if prompt|promptID|end|endID|resume|resumeID != 0 {
		t.Fatal("legacy watermarks are not zero")
	}
}

func TestSessionWatermarkMigrationRollsBack(t *testing.T) {
	t.Parallel()
	db := legacySessionCache(t)
	if _, err := db.Exec(`CREATE TRIGGER refuse_cache_upgrade BEFORE UPDATE ON pasture_session_api_state BEGIN SELECT RAISE(ABORT,'constructed migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err == nil {
		t.Fatal("migration unexpectedly succeeded")
	}
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 10 {
		t.Fatalf("schema version = %d", version)
	}
	if _, err := db.Exec(`SELECT last_prompt_at FROM pasture_session_state`); err == nil {
		t.Fatal("partial schema survived rollback")
	}
	if _, err := db.Exec(`DROP TRIGGER refuse_cache_upgrade`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
}

func TestSessionWatermarkConcurrentUpgrade(t *testing.T) {
	t.Parallel()
	db := legacySessionCache(t)
	db.SetMaxOpenConns(1)
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() { <-start; results <- Migrate(db) }()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var revision, version int
	if err := db.QueryRow(`SELECT revision,cache_version FROM pasture_session_api_state`).Scan(&revision, &version); err != nil {
		t.Fatal(err)
	}
	if revision != 9 || version != SessionCacheVersion {
		t.Fatalf("concurrent upgrade invalidated more than once: revision=%d version=%d", revision, version)
	}
}
