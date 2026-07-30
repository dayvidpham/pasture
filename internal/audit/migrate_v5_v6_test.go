package audit

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateV5toV6CreatesLifecycleSchema(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schemaMetaDDL + `; INSERT INTO audit_schema_meta(version, applied_at) VALUES (5, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, table := range []string{"lifecycle_payload_blobs", "lifecycle_occurrences"} {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
	version, err := readVersion(db)
	if err != nil || version != 6 {
		t.Fatalf("version=%d err=%v, want 6", version, err)
	}
}

func TestLifecycleBlobMustExistBeforeOccurrence(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schemaMetaDDL + `; INSERT INTO audit_schema_meta(version, applied_at) VALUES (5, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO lifecycle_occurrences(journal_id, contract, event_kind, received_at, actor_id, capture_disposition, payload_digest, envelope_json, bindings_json, snapshot_journal_id) VALUES(1,'c',1,1,'a',1,'sha256:missing','{}','[]',1)`)
	if err == nil {
		t.Fatal("occurrence insert without a committed blob succeeded")
	}
}
