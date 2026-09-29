package dbconn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/timeouts"
)

func TestDSNUsesInjectedProfileBusyTimeout(t *testing.T) {
	t.Parallel()
	if got := SharedDSNWithProfile("db", timeouts.TestProfile()); !strings.Contains(got, "busy_timeout(500)") {
		t.Fatalf("test DSN=%q, want injected 500ms", got)
	}
	if got := SharedDSNWithProfile("db", timeouts.ProductionProfile()); !strings.Contains(got, "busy_timeout(500)") {
		t.Fatalf("production DSN=%q, want injected 500ms", got)
	}
}

func TestDSNEscapesURIDelimitersInPath(t *testing.T) {
	t.Parallel()
	for name, got := range map[string]string{
		"shared":   SharedDSN("a#b?c%d"),
		"readonly": ReadOnlyDSN("a#b?c%d"),
	} {
		if !strings.Contains(got, "file:a%23b%3Fc%25d?") {
			t.Fatalf("%s DSN=%q, want the path percent-encoded so it names the requested file", name, got)
		}
	}
}

// slashedPath returns the "//"-prefixed spelling of dir/name. On Linux that
// spelling names the same file as the one-slash form, which is what makes it a
// legal POSIX path and what the constructors must keep honouring.
func slashedPath(dir, name string) string {
	return "//" + strings.TrimPrefix(filepath.Join(dir, name), "/")
}

// TestDSNNormalizesALeadingSlashRunToTheExactFile proves the DSN builders do
// not emit a "file://..." URI for a "//" path. "file:" + "//tmp/x" parses "tmp"
// as the URI AUTHORITY, which modernc rejects with "invalid uri authority", so
// the DSN must carry the collapsed one-slash file path instead.
func TestDSNNormalizesALeadingSlashRunToTheExactFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exact := filepath.Join(dir, "plain.db")
	slashed := slashedPath(dir, "plain.db")

	for name, dsn := range map[string]string{
		"SharedDSN":              SharedDSN(slashed),
		"ReadOnlyDSN":            ReadOnlyDSN(slashed),
		"SharedDSNWithProfile":   SharedDSNWithProfile(slashed, timeouts.TestProfile()),
		"ReadOnlyDSNWithProfile": ReadOnlyDSNWithProfile(slashed, timeouts.TestProfile()),
	} {
		require.Contains(t, dsn, "file:"+exact,
			"%s must name the one-slash form of the // path, got %q", name, dsn)
		require.NotContains(t, dsn, "file://",
			"%s must not leave an empty-authority // after file:, got %q", name, dsn)
	}
}

// TestOpenConstructorsNameTheExactFileForALeadingSlashRun is the behaviour
// proof for the constructors that actually open a handle. A "//" path must
// reach the very file the caller named, including when the name also carries a
// "?" and a "%" so the normalisation and the escaping compose.
func TestOpenConstructorsNameTheExactFileForALeadingSlashRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exact := filepath.Join(dir, "u?n%25.db")
	slashed := slashedPath(dir, "u?n%25.db")

	db, err := OpenSharedDB(slashed)
	require.NoError(t, err, "OpenSharedDB must open the file the // path names")
	_, err = db.Exec("CREATE TABLE probe (x INTEGER)")
	require.NoError(t, err, "write through the shared handle")
	require.NoError(t, db.Close())

	info, err := os.Stat(exact)
	require.NoError(t, err, "OpenSharedDB must create the exact file the // path names")
	require.NotZero(t, info.Size())

	ro, err := OpenReadOnlyDB(slashed)
	require.NoError(t, err, "OpenReadOnlyDB must reopen the exact file the // path names")
	defer func() { require.NoError(t, ro.Close()) }()
	var count int
	require.NoError(t, ro.QueryRow("SELECT count(*) FROM probe").Scan(&count))
	require.Zero(t, count)
}

// TestOpenDefaultDBNamesTheExactFileAndImposesNoSharedProfile is the direct
// proof for OpenDefaultDB: it names the exact file the caller asked for, even
// through a "//" path carrying URI delimiters, and it imposes none of the
// shared WAL profile (its journal mode stays SQLite's default).
func TestOpenDefaultDBNamesTheExactFileAndImposesNoSharedProfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exact := filepath.Join(dir, "default?p#db.db")
	slashed := slashedPath(dir, "default?p#db.db")

	db, err := OpenDefaultDB(slashed)
	require.NoError(t, err, "OpenDefaultDB must open the file the // path names")
	defer func() { require.NoError(t, db.Close()) }()

	_, err = db.Exec("CREATE TABLE probe (x INTEGER)")
	require.NoError(t, err, "OpenDefaultDB must write through the exact file")
	info, err := os.Stat(exact)
	require.NoError(t, err, "OpenDefaultDB must create the exact file the // path names")
	require.NotZero(t, info.Size())

	// The shared profile opens journal_mode(WAL) via a DSN pragma; the default
	// opener must leave the file in SQLite's default rollback journal.
	var mode string
	require.NoError(t, db.QueryRow("PRAGMA journal_mode").Scan(&mode))
	require.Equal(t, "delete", mode, "OpenDefaultDB must impose none of the shared WAL profile")
}

// TestSharedDSNOpensTheExactFileWhenPathContainsURIDelimiters is the behaviour
// proof, not just a string check: before the escaping, a "#" or "?" in the path
// silently truncated the store at that byte and every caller that asked for the
// real path landed on the same truncated file. The test asks for a path with
// all three escaped bytes — including a literal "%", so the escape of an
// already-escape-looking byte is exercised too — proves the store is written
// there, and reads the schema back through the read-only DSN.
func TestSharedDSNOpensTheExactFileWhenPathContainsURIDelimiters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "pasture#1?2%25.db")

	db, err := OpenSharedDB(path)
	require.NoError(t, err, "open the shared handle at a path carrying URI delimiters")
	_, err = db.Exec("CREATE TABLE probe (x INTEGER)")
	require.NoError(t, err, "write through the shared handle")
	require.NoError(t, db.Close())

	info, err := os.Stat(path)
	require.NoError(t, err, "the exact requested path must be the database file")
	require.NotZero(t, info.Size())

	ro, err := OpenReadOnlyDB(path)
	require.NoError(t, err, "reopen the exact requested path read-only")
	defer func() { require.NoError(t, ro.Close()) }()
	var count int
	require.NoError(t, ro.QueryRow("SELECT count(*) FROM probe").Scan(&count))
	require.Zero(t, count)
}
