package handlers_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

// maxVersion builds the "v<MaxKnownSchemaVersion>" text the migrate handlers
// print, so the assertions below cannot drift when the ceiling is bumped.
func maxVersion() string {
	return "v" + strconv.Itoa(audit.MaxKnownSchemaVersion)
}

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
	require.Equal(t, "migrated "+dbPath+" from v1 to "+maxVersion(), strings.TrimSpace(applyOut.String()))

	info, err := os.Stat(dbPath)
	require.NoError(t, err, "the exact requested path must be the database file")
	require.NotZero(t, info.Size(), "the migrate apply must have written the exact file")
	_, err = os.Stat(truncated)
	require.True(t, os.IsNotExist(err), "no database may appear at the truncated path %q", truncated)

	// Dry run: opens the same file and must see it as already current. An
	// unescaped open would land on a fresh truncated file and print v1 -> vN.
	//
	// The dry run's byte-identity promise is checked HERE as its own SHA-256
	// before/after proof rather than inferred from the version line: a dry run
	// that reopened the file in a write mode could leave the version line
	// correct and still change the bytes on disk, so this assertion can fail
	// independently of the "vN -> vN" one above.
	before := sha256Of(t, dbPath)
	var dryOut bytes.Buffer
	code, err = handlers.Migrate(&dryOut, handlers.MigrateInput{DBPath: dbPath, DryRun: true}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, dryOut.String(), "Dry run: "+dbPath+" ("+maxVersion()+" -> "+maxVersion()+")",
		"the dry run must read the exact file the apply upgraded")
	require.Equal(t, before, sha256Of(t, dbPath), "a dry run must leave the exact file byte-identical")
}

// TestMigrateNameTheExactFileForALeadingSlashRun is the handler-level repro for
// POSIX paths that begin with "//". On Linux "//tmp/x" names the same file as
// "/tmp/x", but a DSN built as "file:" + "//tmp/x" parses "tmp" as the URI
// AUTHORITY, which modernc rejects ("invalid uri authority"), so before the
// normalisation both the apply and the dry run failed without touching the
// named file.
func TestMigrateNameTheExactFileForALeadingSlashRun(t *testing.T) {
	t.Parallel()
	exact := filepath.Join(t.TempDir(), "slash.db")
	dbPath := "//" + strings.TrimPrefix(exact, "/")

	var applyOut bytes.Buffer
	code, err := handlers.Migrate(&applyOut, handlers.MigrateInput{DBPath: dbPath}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Equal(t, fmt.Sprintf("migrated %s from v1 to %s", dbPath, maxVersion()), strings.TrimSpace(applyOut.String()))

	info, err := os.Stat(exact)
	require.NoError(t, err, "the // path must name the exact file")
	require.NotZero(t, info.Size(), "the migrate apply must have written the exact file")

	var dryOut bytes.Buffer
	code, err = handlers.Migrate(&dryOut, handlers.MigrateInput{DBPath: dbPath, DryRun: true}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, dryOut.String(), fmt.Sprintf("Dry run: %s (%s -> %s)", dbPath, maxVersion(), maxVersion()),
		"the dry run must read the exact file the // path names")
}
