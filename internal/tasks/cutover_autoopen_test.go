package tasks_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/tasks"
)

// copyLegacyAuditFixture copies the repository's legacy audit-only fixture
// (1,024 events, seven legacy roles, 768 retired-shaped epoch attachments) to a
// temp file.
func copyLegacyAuditFixture(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "walked to the filesystem root without finding go.mod")
		dir = parent
	}
	raw, err := os.ReadFile(filepath.Join(dir, "testdata", "legacy_audit_v1.db"))
	require.NoError(t, err)
	dst := filepath.Join(t.TempDir(), "legacy.db")
	require.NoError(t, os.WriteFile(dst, raw, 0o600))
	return dst
}

// TestAuditOnlyLegacyStoreIsCutOverOnFirstOpen pins the audit-only auto-open: a
// store with no Provenance identity rows is CUTOVER at P0, its deferred catch-up
// mints the seven legacy-role slash agents and moves the epoch attachments into
// context_edges, the conversion publishes marker + receipt + floor, and the
// second open is a no-op.
func TestAuditOnlyLegacyStoreIsCutOverOnFirstOpen(t *testing.T) {
	t.Parallel()
	path := copyLegacyAuditFixture(t)

	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err, "the first open of an audit-only legacy store must cut it over")
	require.NoError(t, tracker.Close())

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()

	var version int
	require.NoError(t, db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version))
	require.Equal(t, 10, version, "the cutover raises the ceiling")

	var markerRows int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM provenance_id_format WHERE singleton=1 AND format=?`,
		string(provenance.IDFormatSlash)).Scan(&markerRows))
	require.Equal(t, 1, markerRows, "the slash marker must be published")

	var receiptRows int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pasture_id_cutover WHERE singleton_id=0 AND marker_format=? AND floor_version=?`,
		string(provenance.IDFormatSlash), 10).Scan(&receiptRows))
	require.Equal(t, 1, receiptRows, "the cutover receipt must be written")

	var agents int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM agents_software WHERE name LIKE 'pasture/legacy-role/%'`).Scan(&agents))
	require.Equal(t, 7, agents, "the catch-up mints exactly seven legacy-role agents")

	var retiredAgents int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM agents WHERE id LIKE '%--%'`).Scan(&retiredAgents))
	require.Zero(t, retiredAgents, "no retired-shaped agent id may remain")

	var converted int
	require.NoError(t, db.QueryRow(`SELECT count(DISTINCT context_id) FROM context_edges WHERE context_id LIKE 'aura-plugins/%'`).Scan(&converted))
	require.Equal(t, 768, converted, "the 768 retired epoch attachments convert to the slash form")

	var retiredContexts int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM context_edges WHERE context_id LIKE 'aura-plugins--%'`).Scan(&retiredContexts))
	require.Zero(t, retiredContexts, "no retired epoch attachment may remain")

	// Reopen NORMAL; the second open is a no-op.
	second, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err, "the cut-over store must reopen normally")
	require.NoError(t, second.Close())

	var versionAfter int
	require.NoError(t, db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&versionAfter))
	require.Equal(t, 10, versionAfter, "the second open is a no-op")
	var agentsAfter int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM agents_software WHERE name LIKE 'pasture/legacy-role/%'`).Scan(&agentsAfter))
	require.Equal(t, 7, agentsAfter, "the second open must not duplicate agents")
}
