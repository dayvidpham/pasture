package audit

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openV6Lifecycle(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaMetaDDL + `; INSERT INTO audit_schema_meta(version,applied_at) VALUES(6,1)`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range lifecycleV6Statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func seedV6Occurrence(t *testing.T, db *sql.DB, bindings string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO lifecycle_payload_blobs(digest,body,byte_count) VALUES('sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',X'01',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO lifecycle_occurrences(journal_id,contract,event_kind,received_at,actor_id,capture_disposition,payload_digest,envelope_json,bindings_json,snapshot_journal_id) VALUES(1,'c',1,1,'a',1,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','{}',?,1)`, []byte(bindings)); err != nil {
		t.Fatal(err)
	}
}
func TestMigrateV6ToV7RebuildsAndBackfillsBindings(t *testing.T) {
	db := openV6Lifecycle(t)
	defer db.Close()
	seedV6Occurrence(t, db, `[{"Kind":1,"NativeName":"session_id","Value":"A"},{"Kind":1,"NativeName":"session_id","Value":"A"}]`)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	version, err := readVersion(db)
	if err != nil || version != 7 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	var oldColumn int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('lifecycle_occurrences') WHERE name='bindings_json'`).Scan(&oldColumn); err != nil || oldColumn != 0 {
		t.Fatalf("bindings_json count=%d err=%v", oldColumn, err)
	}
	var count, blobTypes int
	if err := db.QueryRow(`SELECT count(*),sum(typeof(native_name)='blob' AND typeof(binding_value)='blob') FROM lifecycle_occurrence_bindings`).Scan(&count, &blobTypes); err != nil || count != 2 || blobTypes != 2 {
		t.Fatalf("bindings count=%d blobs=%d err=%v", count, blobTypes, err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("idempotent migrate: %v", err)
	}
}
func TestMigrateV6ToV7MalformedBindingsRollBackExactly(t *testing.T) {
	db := openV6Lifecycle(t)
	defer db.Close()
	raw := `[{"Kind":1,"NativeName":"session_id","NativeName":"forged","Value":"A"}]`
	seedV6Occurrence(t, db, raw)
	if err := Migrate(db); err == nil {
		t.Fatal("Migrate accepted duplicate member")
	}
	version, _ := readVersion(db)
	if version != 6 {
		t.Fatalf("version=%d want 6", version)
	}
	var stored []byte
	if err := db.QueryRow(`SELECT bindings_json FROM lifecycle_occurrences WHERE journal_id=1`).Scan(&stored); err != nil || string(stored) != raw {
		t.Fatalf("stored=%q err=%v", stored, err)
	}
	var v7 int
	_ = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='lifecycle_occurrence_bindings'`).Scan(&v7)
	if v7 != 0 {
		t.Fatalf("v7 table survived rollback")
	}
}
