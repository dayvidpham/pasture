package audit

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// v7LifecycleSchema is the lifecycle part of a version 7 database as the v7
// migration leaves it: blobs without a written-at stamp.
const v7LifecycleSchema = `CREATE TABLE lifecycle_payload_blobs(digest TEXT PRIMARY KEY,body BLOB NOT NULL,byte_count INTEGER NOT NULL) WITHOUT ROWID; CREATE TABLE lifecycle_occurrences(journal_id INTEGER PRIMARY KEY,contract TEXT NOT NULL,event_kind INTEGER NOT NULL,received_at INTEGER NOT NULL,actor_id TEXT NOT NULL,capture_disposition INTEGER NOT NULL,payload_digest TEXT NOT NULL REFERENCES lifecycle_payload_blobs(digest),envelope_json BLOB NOT NULL,snapshot_journal_id INTEGER NOT NULL); CREATE TABLE lifecycle_occurrence_bindings(journal_id INTEGER NOT NULL REFERENCES lifecycle_occurrences(journal_id) ON DELETE CASCADE,binding_index INTEGER NOT NULL,binding_kind INTEGER NOT NULL,native_name BLOB NOT NULL,binding_value BLOB NOT NULL,PRIMARY KEY(journal_id,binding_index)) STRICT, WITHOUT ROWID; CREATE INDEX lifecycle_occurrence_bindings_lookup ON lifecycle_occurrence_bindings(binding_kind,native_name,binding_value,journal_id);`

const (
	referencedDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	orphanDigest     = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// openV7WithOneReferencedAndOneOrphanBlob builds a version 7 store holding a
// blob an occurrence names and a blob nothing names.
func openV7WithOneReferencedAndOneOrphanBlob(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "pasture.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ddl := schemaMetaDDL + `; INSERT INTO audit_schema_meta(version,applied_at) VALUES(7,1); ` + v7LifecycleSchema +
		` INSERT INTO lifecycle_payload_blobs VALUES('` + referencedDigest + `',X'01',1); INSERT INTO lifecycle_payload_blobs VALUES('` + orphanDigest + `',X'02',1);` +
		` INSERT INTO lifecycle_occurrences VALUES(1,'c',1,1,'a',1,'` + referencedDigest + `','{}',1);`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return db
}

func writtenAtOf(t *testing.T, db *sql.DB, digest string) int64 {
	t.Helper()
	var writtenAt int64
	if err := db.QueryRow(`SELECT written_at FROM lifecycle_payload_blobs WHERE digest=?`, digest).Scan(&writtenAt); err != nil {
		t.Fatalf("read written_at of %s: %v", digest, err)
	}
	return writtenAt
}

// TestMigrateV7ToV8StampsEveryPreExistingBlobWithTheMigrationInstant: after
// the upgrade every blob that existed before the column did carries the
// MIGRATION INSTANT, the same unix-nanosecond value the step recorded as the
// applied_at of version 8, whether an occurrence names it or not, and none
// carries the column default 0. The reference survives. A blob inserted
// without a stamp AFTER the upgrade reads the column default 0 (the value the
// reclaim treats as an unknown age) and one inserted with a stamp keeps it.
// MUTATION: drop the UPDATE from the step and this is RED at the first stamp
// read ("carries 0"), because a pre-existing row would then be indistinguishable
// from one written long ago.
func TestMigrateV7ToV8StampsEveryPreExistingBlobWithTheMigrationInstant(t *testing.T) {
	db := openV7WithOneReferencedAndOneOrphanBlob(t)
	before := time.Now().UTC().UnixNano()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC().UnixNano()
	version, _ := readVersion(db)
	if version != 8 {
		t.Fatalf("version=%d want=8", version)
	}
	var appliedAt int64
	if err := db.QueryRow(`SELECT applied_at FROM audit_schema_meta WHERE version=8`).Scan(&appliedAt); err != nil {
		t.Fatalf("read the applied_at of version 8: %v", err)
	}
	if appliedAt < before || appliedAt > after {
		t.Fatalf("the migration instant %d must lie inside the run [%d, %d]", appliedAt, before, after)
	}
	for _, legacy := range []struct{ name, digest string }{{"referenced", referencedDigest}, {"orphan", orphanDigest}} {
		got := writtenAtOf(t, db, legacy.digest)
		if got == 0 {
			t.Fatalf("the %s pre-existing blob carries 0 after the upgrade; every pre-existing row must carry the migration instant, or the first read command would treat a moments-old blob as older than any bound", legacy.name)
		}
		if got != appliedAt {
			t.Fatalf("the %s pre-existing blob carries written_at=%d, want the migration instant %d recorded as applied_at of version 8", legacy.name, got, appliedAt)
		}
	}
	var named int
	if err := db.QueryRow(`SELECT count(*) FROM lifecycle_occurrences o JOIN lifecycle_payload_blobs b ON b.digest=o.payload_digest`).Scan(&named); err != nil || named != 1 {
		t.Fatalf("the referenced blob must still be named after the upgrade: named=%d err=%v", named, err)
	}
	if _, err := db.Exec(`INSERT INTO lifecycle_payload_blobs(digest,body,byte_count) VALUES('sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc',X'03',1)`); err != nil {
		t.Fatal(err)
	}
	if got := writtenAtOf(t, db, "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"); got != 0 {
		t.Fatalf("an unstamped insert after the upgrade must read the column default 0, the unknown-age value, got %d", got)
	}
	if _, err := db.Exec(`INSERT INTO lifecycle_payload_blobs(digest,body,byte_count,written_at) VALUES('sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd',X'04',1,1234567890)`); err != nil {
		t.Fatal(err)
	}
	if got := writtenAtOf(t, db, "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"); got != 1234567890 {
		t.Fatalf("a stamped insert must keep its stamp, got %d", got)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("foreign key violation after the upgrade")
	}
	rows.Close()
}

// TestMigrateV7ToV8IsIdempotentOnAVersion8Database: a second Migrate on the
// upgraded store changes nothing and adds no column.
func TestMigrateV7ToV8IsIdempotentOnAVersion8Database(t *testing.T) {
	db := openV7WithOneReferencedAndOneOrphanBlob(t)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	version, _ := readVersion(db)
	if version != 8 {
		t.Fatalf("version=%d want=8", version)
	}
	var columns int
	rows, err := db.Query(`PRAGMA table_info(lifecycle_payload_blobs)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		columns++
	}
	rows.Close()
	if columns != 4 {
		t.Fatalf("lifecycle_payload_blobs has %d columns, want 4 (digest, body, byte_count, written_at)", columns)
	}
}

// TestMigrateV7ToV8RefusesToRecordTheVersionWhileAPreExistingBlobIsUnstamped
// drives the stamp verification directly: a transaction in which the column
// was added but the pre-existing rows were not stamped must not be recorded
// as version 8, so the ALTER alone can never claim the version.
func TestMigrateV7ToV8RefusesToRecordTheVersionWhileAPreExistingBlobIsUnstamped(t *testing.T) {
	db := openV7WithOneReferencedAndOneOrphanBlob(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(lifecycleV8Statements[0]); err != nil {
		t.Fatal(err)
	}
	err = requireNoUnstampedBlob(tx)
	if err == nil || err.Error() != "verify lifecycle v8 payload blob stamps: 2 pre-existing payload blobs still carry written_at 0 after the upgrade statements ran; every pre-existing row must carry the migration instant" {
		t.Fatalf("two unstamped pre-existing blobs must be refused by count, got %v", err)
	}
	if _, err := tx.Exec(lifecycleV8StampStatement, int64(42)); err != nil {
		t.Fatal(err)
	}
	if err := requireNoUnstampedBlob(tx); err != nil {
		t.Fatalf("after the stamp statement nothing is unstamped, got %v", err)
	}
}

// TestMigrateV7ToV8RefusesToRecordTheVersionWithoutTheColumn drives the step
// verification directly: a transaction in which the column was not added must
// not be recorded as version 8.
func TestMigrateV7ToV8RefusesToRecordTheVersionWithoutTheColumn(t *testing.T) {
	db := openV7WithOneReferencedAndOneOrphanBlob(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	err = requireWrittenAtColumn(tx)
	if err == nil || err.Error() != "verify lifecycle v8 payload blob columns: the written_at column is missing after the upgrade statements ran" {
		t.Fatalf("a v7 table must be refused as missing the column, got %v", err)
	}
}
