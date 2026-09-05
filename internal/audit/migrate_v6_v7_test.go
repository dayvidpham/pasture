package audit

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openV6Lifecycle(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "pasture.db")+"?_pragma=foreign_keys(1)")
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
	if err != nil || version != MaxKnownSchemaVersion {
		t.Fatalf("version=%d want=%d err=%v", version, MaxKnownSchemaVersion, err)
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
	cases := map[string]string{"duplicate member": `[{"Kind":1,"NativeName":"session_id","NativeName":"forged","Value":"A"}]`, "null": `null`, "unknown": `[{"Kind":1,"NativeName":"session_id","Value":"A","Other":1}]`, "missing name": `[{"Kind":1,"Value":"A"}]`, "invalid kind": `[{"Kind":9,"NativeName":"session_id","Value":"A"}]`, "nul": `[{"Kind":1,"NativeName":"session_id","Value":"A\u0000"}]`, "control": `[{"Kind":1,"NativeName":"session_id","Value":"A\n"}]`, "padding": `[{"Kind":1,"NativeName":" session_id","Value":"A"}]`, "oversized": `[{"Kind":1,"NativeName":"session_id","Value":"` + strings.Repeat("x", 513) + `"}]`}
	for name, raw := range cases {
		name, raw := name, raw
		t.Run(name, func(t *testing.T) {
			db := openV6Lifecycle(t)
			defer db.Close()
			seedV6Occurrence(t, db, raw)
			if err := Migrate(db); err == nil {
				t.Fatal("Migrate accepted malformed bindings")
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
			var column int
			_ = db.QueryRow(`SELECT count(*) FROM pragma_table_info('lifecycle_occurrences') WHERE name='bindings_json'`).Scan(&column)
			if column != 1 {
				t.Fatal("v6 schema was not restored")
			}
		})
	}
}

func TestMigrateV6ToV7PreservesExactBlobOrderAndCascade(t *testing.T) {
	db := openV6Lifecycle(t)
	defer db.Close()
	raw := `[{"Kind":1,"NativeName":"session_id","Value":"é"},{"Kind":4,"NativeName":"tool_use_id","Value":"Case"},{"Kind":4,"NativeName":"tool_use_id","Value":"Cas"}]`
	seedV6Occurrence(t, db, raw)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT binding_index,native_name,binding_value,typeof(native_name),typeof(binding_value) FROM lifecycle_occurrence_bindings ORDER BY binding_index`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantNames := [][]byte{[]byte("session_id"), []byte("tool_use_id"), []byte("tool_use_id")}
	wantValues := [][]byte{[]byte("é"), []byte("Case"), []byte("Cas")}
	i := 0
	for rows.Next() {
		var index int
		var name, value []byte
		var nt, vt string
		if err := rows.Scan(&index, &name, &value, &nt, &vt); err != nil {
			t.Fatal(err)
		}
		if index != i || !bytes.Equal(name, wantNames[i]) || !bytes.Equal(value, wantValues[i]) || nt != "blob" || vt != "blob" {
			t.Fatalf("row %d index=%d name=%q value=%q types=%s/%s", i, index, name, value, nt, vt)
		}
		i++
	}
	if i != 3 {
		t.Fatalf("rows=%d", i)
	}
	if _, err := db.Exec(`DELETE FROM lifecycle_occurrences WHERE journal_id=1`); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = db.QueryRow(`SELECT count(*) FROM lifecycle_occurrence_bindings`).Scan(&count)
	if count != 0 {
		t.Fatalf("cascade left %d bindings", count)
	}
}

func TestMigrateV6ToV7AcceptsEmptyBindings(t *testing.T) {
	db := openV6Lifecycle(t)
	defer db.Close()
	seedV6Occurrence(t, db, `[]`)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = db.QueryRow(`SELECT count(*) FROM lifecycle_occurrence_bindings`).Scan(&count)
	if count != 0 {
		t.Fatalf("bindings=%d", count)
	}
}

func TestMigrateV5RunsOrderedChainToTheCurrentVersion(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "pasture.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schemaMetaDDL + `; INSERT INTO audit_schema_meta(version,applied_at) VALUES(5,1)`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	version, _ := readVersion(db)
	if version != MaxKnownSchemaVersion {
		t.Fatalf("version=%d want=%d", version, MaxKnownSchemaVersion)
	}
	for _, table := range []string{"lifecycle_payload_blobs", "lifecycle_occurrences", "lifecycle_occurrence_bindings"} {
		var count int
		_ = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
		if count != 1 {
			t.Fatalf("missing %s", table)
		}
	}
	if _, err := db.Exec(`INSERT INTO lifecycle_payload_blobs(digest,body,byte_count) VALUES('sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',X'01',1); INSERT INTO lifecycle_occurrences(journal_id,contract,event_kind,received_at,actor_id,capture_disposition,payload_digest,envelope_json,snapshot_journal_id) VALUES(1,'c',1,1,'a',1,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','{}',1); INSERT INTO lifecycle_occurrence_bindings VALUES(1,0,1,X'73657373696f6e5f6964',X'43617365')`); err != nil {
		t.Fatal(err)
	}
	var value []byte
	if err := db.QueryRow(`SELECT binding_value FROM lifecycle_occurrence_bindings WHERE binding_kind=1 AND native_name=X'73657373696f6e5f6964'`).Scan(&value); err != nil || !bytes.Equal(value, []byte("Case")) {
		t.Fatalf("lookup=%q err=%v", value, err)
	}
}

func TestV7DatabaseIsPromotedAndStillCascades(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "pasture.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ddl := schemaMetaDDL + `; INSERT INTO audit_schema_meta(version,applied_at) VALUES(7,1); CREATE TABLE lifecycle_payload_blobs(digest TEXT PRIMARY KEY,body BLOB NOT NULL,byte_count INTEGER NOT NULL) WITHOUT ROWID; CREATE TABLE lifecycle_occurrences(journal_id INTEGER PRIMARY KEY,contract TEXT NOT NULL,event_kind INTEGER NOT NULL,received_at INTEGER NOT NULL,actor_id TEXT NOT NULL,capture_disposition INTEGER NOT NULL,payload_digest TEXT NOT NULL REFERENCES lifecycle_payload_blobs(digest),envelope_json BLOB NOT NULL,snapshot_journal_id INTEGER NOT NULL); CREATE TABLE lifecycle_occurrence_bindings(journal_id INTEGER NOT NULL REFERENCES lifecycle_occurrences(journal_id) ON DELETE CASCADE,binding_index INTEGER NOT NULL,binding_kind INTEGER NOT NULL,native_name BLOB NOT NULL,binding_value BLOB NOT NULL,PRIMARY KEY(journal_id,binding_index)) STRICT, WITHOUT ROWID; CREATE INDEX lifecycle_occurrence_bindings_lookup ON lifecycle_occurrence_bindings(binding_kind,native_name,binding_value,journal_id); INSERT INTO lifecycle_payload_blobs VALUES('sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',X'01',1); INSERT INTO lifecycle_occurrences VALUES(1,'c',1,1,'a',1,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','{}',1); INSERT INTO lifecycle_occurrence_bindings VALUES(1,0,1,X'73657373696f6e5f6964',X'78');`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var value []byte
	if err := db.QueryRow(`SELECT binding_value FROM lifecycle_occurrence_bindings WHERE binding_kind=1 AND native_name=X'73657373696f6e5f6964'`).Scan(&value); err != nil || string(value) != "x" {
		t.Fatalf("lookup=%q err=%v", value, err)
	}
	var violations int
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations != 0 {
		t.Fatalf("foreign key violations=%d", violations)
	}
	if _, err := db.Exec(`DELETE FROM lifecycle_occurrences WHERE journal_id=1`); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = db.QueryRow(`SELECT count(*) FROM lifecycle_occurrence_bindings`).Scan(&count)
	if count != 0 {
		t.Fatalf("cascade left %d", count)
	}
}
