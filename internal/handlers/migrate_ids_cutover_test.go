package handlers_test

import (
	"bytes"
	"database/sql"
	stderrors "errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// copyLegacyAuditFixture copies the repository's legacy audit-only fixture.
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

// TestMigrateIDsLegacyFixtureEndToEnd pins the command frontend end to end: the
// legacy fixture converts (seven slash-minted agents, 768 epoch attachments
// converted, marker + receipt + floor), the store reopens through the production
// tracker, and a second run is a no-op that creates no new archive.
func TestMigrateIDsLegacyFixtureEndToEnd(t *testing.T) {
	t.Parallel()
	storeDir := t.TempDir()
	path := copyLegacyAuditFixture(t)

	var out bytes.Buffer
	code, err := handlers.MigrateIDs(&out, handlers.MigrateIDsInput{DBPath: path, BackupDir: storeDir}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "reopen proof:")
	require.Contains(t, out.String(), "reclaimed: 0 raw payload blob(s)")

	firstArchive := countArchives(t, storeDir)
	require.Equal(t, 1, firstArchive, "the conversion makes exactly one archive")

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	var version int
	require.NoError(t, db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version))
	require.Equal(t, 10, version)
	var markerRows, receiptRows, agents, converted int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM provenance_id_format WHERE singleton=1`).Scan(&markerRows))
	require.Equal(t, 1, markerRows)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pasture_id_cutover WHERE singleton_id=0 AND floor_version=10`).Scan(&receiptRows))
	require.Equal(t, 1, receiptRows)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM agents_software WHERE name LIKE 'pasture/legacy-role/%'`).Scan(&agents))
	require.Equal(t, 7, agents)
	require.NoError(t, db.QueryRow(`SELECT count(DISTINCT context_id) FROM context_edges WHERE context_id LIKE 'aura-plugins/%'`).Scan(&converted))
	require.Equal(t, 768, converted)

	// Reopen through the production tracker, then a second command run is a
	// front-loaded no-op: no second archive, no writes.
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())

	var second bytes.Buffer
	code, err = handlers.MigrateIDs(&second, handlers.MigrateIDsInput{DBPath: path, BackupDir: storeDir}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, second.String(), "already current")
	require.Equal(t, firstArchive, countArchives(t, storeDir), "a second run must not make another archive")
}

func countArchives(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	n := 0
	for _, e := range entries {
		if matched, _ := filepath.Match("*.id-backup-*", e.Name()); matched {
			n++
		}
	}
	return n
}

// TestRefusalTextIsIdenticalOnGuardAndCommand pins the parity requirement: the
// normal-open guard and the command render the SAME refusal text for an unbacked
// mark, because the text has one owner.
func TestRefusalTextIsIdenticalOnGuardAndCommand(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pasture.db")

	// Build a normal store, then remove the marker so the receipt is unbacked.
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM provenance_id_format`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	guardErr := func() *pasterrors.StructuredError {
		_, err := tasks.OpenTaskTracker(path)
		require.Error(t, err)
		var se *pasterrors.StructuredError
		require.True(t, stderrors.As(err, &se))
		return se
	}()

	var out bytes.Buffer
	code, cmdErr := handlers.MigrateIDs(&out, handlers.MigrateIDsInput{DBPath: path, DryRun: true}, types.OutputText)
	require.Error(t, cmdErr)
	require.Equal(t, 5, code)
	var cmdSE *pasterrors.StructuredError
	require.True(t, stderrors.As(cmdErr, &cmdSE))

	require.Equal(t, pasterrors.CategoryStorage, guardErr.Category)
	require.Equal(t, guardErr.Category, cmdSE.Category)
	require.Equal(t, guardErr.What, cmdSE.What, "the guard and command must render one refusal")
	require.Equal(t, guardErr.Why, cmdSE.Why)
	require.Equal(t, guardErr.Impact, cmdSE.Impact)
	require.Equal(t, guardErr.Fix, cmdSE.Fix)
}
