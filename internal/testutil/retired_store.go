package testutil

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// RetiredPopulatedStoreWithClaim builds a current production store, strips its
// fence evidence (the slash marker, the cutover receipt and the deferred ceiling
// step) so it presents as an unmarked, populated store, and installs the
// authentic opaque claimant row ("pasture-system" over the ordinal range
// [0,1023]). It is the store shape the operator's live database has when the
// offline cutover meets it. It is shared by the idmigrate and handlers tests so
// their copies cannot drift.
//
// The returned *sql.DB is registered for cleanup; callers that reopen the store
// (for example to prove activation after conversion) must close it first.
func RetiredPopulatedStoreWithClaim(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "live.db")
	tracker, err := tasks.OpenTaskTracker(path)
	if err != nil {
		t.Fatalf("open production store: %v", err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close production store: %v", err)
	}

	db, err := dbconn.OpenSharedDB(path)
	if err != nil {
		t.Fatalf("open shared handle: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`DELETE FROM provenance_id_format`); err != nil {
		t.Fatalf("strip slash marker: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS pasture_id_cutover`); err != nil {
		t.Fatalf("strip cutover receipt: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM audit_schema_meta WHERE version >= ?`, audit.MaxKnownSchemaVersion); err != nil {
		t.Fatalf("strip deferred ceiling: %v", err)
	}
	minBody := make([]byte, 16)
	maxBody := make([]byte, 16)
	maxBody[14], maxBody[15] = 0x03, 0xff // 1023, big-endian
	if _, err := db.Exec(
		`INSERT INTO actor_namespace_claims (namespace, claimant_id, range_min, range_max, codec) VALUES (?,?,?,?,?)`,
		"pasture-system", "pasture-system", minBody, maxBody, "provenance.ordinal.v1"); err != nil {
		t.Fatalf("install authentic claim row: %v", err)
	}
	return path, db
}
