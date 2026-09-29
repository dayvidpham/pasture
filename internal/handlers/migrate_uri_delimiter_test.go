package handlers_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

// TestMigrateNamesTheExactFileWhenThePathCarriesURIDelimiters is the handler
// proof for the path-escaping fix. With a "?" or "#" in --db, the migrate
// handlers must open the file the operator named; an unescaped DSN splice is
// truncated at that byte, so the run would read and write a file called
// "pasture" beside the real one and report a plan for a database the operator
// never named.
//
// The apply half also exercises the version probe, which opens the file before
// the migrator does; if only the probe were unescaped, it would create the
// truncated file even though the migration itself landed on the right one.
func TestMigrateNamesTheExactFileWhenThePathCarriesURIDelimiters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pasture?probe#db.db")
	truncated := filepath.Join(dir, "pasture")

	// Apply: probeVersionReadOnly opens/creates the named file, then
	// NewSqliteAuditTrail migrates it to the current version.
	var applyOut bytes.Buffer
	code, err := handlers.Migrate(&applyOut, handlers.MigrateInput{DBPath: dbPath}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Equal(t, "migrated "+dbPath+" from v1 to v9", strings.TrimSpace(applyOut.String()))

	info, err := os.Stat(dbPath)
	require.NoError(t, err, "the exact requested path must be the database file")
	require.NotZero(t, info.Size(), "the migrate apply must have written the exact file")
	_, err = os.Stat(truncated)
	require.True(t, os.IsNotExist(err), "no database may appear at the truncated path %q", truncated)

	// Dry run: opens the same file and must see it as already current. An
	// unescaped open would land on a fresh truncated file and print v1 -> v9.
	var dryOut bytes.Buffer
	code, err = handlers.Migrate(&dryOut, handlers.MigrateInput{DBPath: dbPath, DryRun: true}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, dryOut.String(), "Dry run: "+dbPath+" (v9 -> v9)",
		"the dry run must read the exact file the apply upgraded")
	_, err = os.Stat(truncated)
	require.True(t, os.IsNotExist(err), "the dry run must not create a truncated database either")
}
