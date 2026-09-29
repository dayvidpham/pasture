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

// TestSharedDSNOpensTheExactFileWhenPathContainsURIDelimiters is the behaviour
// proof, not just a string check: before the escaping, a "#" or "?" in the path
// silently truncated the store at that byte and every caller that asked for the
// real path landed on the same truncated file. The test asks for a path with
// all three escaped bytes, proves the store is written there, proves no store
// appears at the truncated prefix, and reads the schema back through the
// read-only DSN.
func TestSharedDSNOpensTheExactFileWhenPathContainsURIDelimiters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "pasture#1?2%3.db")

	db, err := OpenSharedDB(path)
	require.NoError(t, err, "open the shared handle at a path carrying URI delimiters")
	_, err = db.Exec("CREATE TABLE probe (x INTEGER)")
	require.NoError(t, err, "write through the shared handle")
	require.NoError(t, db.Close())

	info, err := os.Stat(path)
	require.NoError(t, err, "the exact requested path must be the database file")
	require.NotZero(t, info.Size())

	prefix := path[:strings.IndexAny(path, "#?%")]
	_, err = os.Stat(prefix)
	require.True(t, os.IsNotExist(err), "no database may be created at the truncated prefix %q", prefix)

	ro, err := OpenReadOnlyDB(path)
	require.NoError(t, err, "reopen the exact requested path read-only")
	defer func() { require.NoError(t, ro.Close()) }()
	var count int
	require.NoError(t, ro.QueryRow("SELECT count(*) FROM probe").Scan(&count))
	require.Zero(t, count)
}
