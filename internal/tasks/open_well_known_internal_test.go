package tasks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/require"
)

// Count only descriptors naming this isolated database, not unrelated parallel
// tests' handles. Linux exposes real driver descriptors, including WAL files.
func databaseDescriptorCount(t *testing.T, path string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if os.IsNotExist(err) {
		t.Log("resource descriptor evidence unavailable without procfs; error/recovery assertions still run")
		return -1
	}
	require.NoError(t, err)
	count := 0
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil && (target == path || strings.HasPrefix(target, path+"-")) {
			count++
		}
	}
	return count
}

func TestDurableOpenRegistrationFailurePreservesCauseAndClosesResources(t *testing.T) {
	t.Parallel()
	for _, open := range []func(string) (protocol.TaskTracker, error){OpenTaskTracker, func(path string) (protocol.TaskTracker, error) {
		return OpenTaskTrackerWithOptions(path, WithSkipMigrations())
	}} {
		path := filepath.Join(t.TempDir(), "pasture.db")
		fixture, _ := openFreshTracker(t, path)
		require.NoError(t, fixture.Close())
		db := openIdentityAssertionDB(t, path)
		name := WellKnownAgents()[1].Name
		_, err := db.Exec(`INSERT INTO pasture_well_known_agents(agent_id, name) VALUES('malformed-saved-id', ?)`, name)
		require.NoError(t, err)
		require.NoError(t, db.Close())
		before := databaseDescriptorCount(t, path)
		tracker, err := open(path)
		if tracker != nil {
			_ = tracker.Close()
		}
		require.Nil(t, tracker)
		var outer *pasterrors.StructuredError
		require.ErrorAs(t, err, &outer)
		require.Equal(t, pasterrors.CategoryStorage, outer.Category)
		require.Contains(t, outer.What, "built-in")
		require.Contains(t, outer.Where, path)
		require.Contains(t, outer.Where, "openTaskTrackerWithOptions")
		require.Contains(t, outer.Where, "registration")
		require.Contains(t, outer.Impact, "No tracker")
		require.Contains(t, outer.Impact, "remain")
		require.Contains(t, outer.Fix, "retry the same CLI command")
		var registration *pasterrors.StructuredError
		require.True(t, errors.As(outer.Cause, &registration))
		require.Equal(t, registration.Category, outer.Category)
		require.Contains(t, registration.What, name)
		require.NotNil(t, registration.Cause, "the ID parse error must remain traversable")
		if before >= 0 {
			require.Equal(t, before, databaseDescriptorCount(t, path), "failed construction must close its SQL pools")
		}
		db = openIdentityAssertionDB(t, path)
		_, err = db.Exec(`DELETE FROM pasture_well_known_agents WHERE name = ?`, name)
		require.NoError(t, err)
		require.NoError(t, db.Close())
		tracker, err = open(path)
		require.NoError(t, err, "repair permits CLI retry without a daemon")
		require.NoError(t, tracker.Close())
	}
}

func TestDurableOpenPreservesRegistrationWorkflowFailure(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pasture.db")
	fixture, db := openFreshTracker(t, path)
	_, err := db.Exec(`CREATE TRIGGER block_builtin_mint BEFORE INSERT ON agents_software
		BEGIN SELECT RAISE(ABORT, 'built-in mint blocked by fixture'); END`)
	require.NoError(t, err)
	require.NoError(t, fixture.Close())
	before := databaseDescriptorCount(t, path)
	tracker, err := OpenTaskTracker(path)
	if tracker != nil {
		_ = tracker.Close()
	}
	require.Nil(t, tracker)
	var outer, registration *pasterrors.StructuredError
	require.ErrorAs(t, err, &outer)
	require.Equal(t, pasterrors.CategoryWorkflow, outer.Category)
	require.ErrorAs(t, outer.Cause, &registration)
	require.Equal(t, registration.Category, outer.Category)
	require.ErrorContains(t, registration.Cause, "built-in mint blocked by fixture")
	if before >= 0 {
		require.Equal(t, before, databaseDescriptorCount(t, path))
	}
	db = openIdentityAssertionDB(t, path)
	_, err = db.Exec(`DROP TRIGGER block_builtin_mint`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	tracker, err = OpenTaskTracker(path)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())
}
