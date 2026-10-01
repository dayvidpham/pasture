package tasks

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/audit"
)

// retiredAssignmentIndexRelations is the set of relations the version 8 to
// version 9 audit step removes, by the names declared in
// internal/audit/migrate_v8_v9.go. The names are repeated here because the
// assertion below is about what the real opener leaves on a file the step has
// just processed, and it is that file, not the migrator's own bookkeeping,
// that has to be empty.
var retiredAssignmentIndexRelations = []string{
	"pasture_assignment_changed",
	"pasture_assignment_deleted",
	"pasture_assignment_recovery_member",
	"pasture_assignment_recovery_cache",
	"pasture_assignment_recovery_scan",
	"pasture_actor_assignment_state",
	"pasture_actor_assignment_watermark",
	"pasture_actor_assignment",
	"idx_pasture_actor_assignment_actor",
}

// openFreshStore opens a brand-new unified store through the production
// opener and returns the implementation, whose audit handle is the same one
// the migrator ran against.
func openFreshStore(t *testing.T) *trackerImpl {
	t.Helper()
	opened, err := OpenTaskTracker(filepath.Join(t.TempDir(), "pasture.db"))
	require.NoError(t, err, "a fresh store must open")
	t.Cleanup(func() { require.NoError(t, opened.Close()) })
	impl, ok := opened.(*trackerImpl)
	require.True(t, ok, "OpenTaskTracker must return the *trackerImpl this test inspects")
	return impl
}

// TestFreshStoreReachesVersionNineThroughTheRealOpener is the fresh-store
// proof for the v8 → v9 step, taken on the real path a user takes: opening a
// store that has never existed. The audit migrator runs inside that open, so
// the recorded version is the chain's own answer and not a hand-seated one.
func TestFreshStoreReachesVersionNineThroughTheRealOpener(t *testing.T) {
	t.Parallel()
	impl := openFreshStore(t)

	var version int
	require.NoError(t, impl.auditDB.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version))
	require.Equal(t, audit.MaxKnownSchemaVersion, version, "a fresh store must open at the version the migrator records")
	require.Equal(t, 9, version, "the ceiling this step raises the store to is 9")

	// One row per applied step on a file that started at version 1. Version 1
	// is never written, so the chain's eight steps record 2 through 9 and the
	// table holds eight rows; a fresh file stamped once at the top would hold
	// one.
	var rows int
	require.NoError(t, impl.auditDB.QueryRow(`SELECT count(*) FROM audit_schema_meta`).Scan(&rows))
	require.Equal(t, audit.MaxKnownSchemaVersion-1, rows, "a file that started at version 1 records one row per step")

	// Session claims are kept, and the table the v8 → v9 step must not touch
	// is on the file and usable.
	_, err := impl.auditDB.Exec(
		`INSERT INTO pasture_session_claim(harness,session,actor,claimed_at) VALUES('claude-code','fresh-1','actor-1',1)`)
	require.NoError(t, err, "pasture_session_claim must survive the v8 to v9 step and stay writable")
	var claims int
	require.NoError(t, impl.auditDB.QueryRow(`SELECT count(*) FROM pasture_session_claim WHERE session='fresh-1'`).Scan(&claims))
	require.Equal(t, 1, claims)
}

// TestFreshStoreHasNoRetiredAssignmentIndexRelations is the counterpart of
// the v8 → v9 step on a file that never had the retired relations. The step's
// IF EXISTS drops are a no-op there, so what the fresh file must show is the
// absence of every retired relation — the step is only half a removal if the
// opener keeps handing them out.
func TestFreshStoreHasNoRetiredAssignmentIndexRelations(t *testing.T) {
	t.Parallel()
	impl := openFreshStore(t)

	var version int
	require.NoError(t, impl.auditDB.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version))
	require.Equal(t, 9, version, "run this assertion against a store the v8 to v9 step has already processed")

	for _, name := range retiredAssignmentIndexRelations {
		var count int
		require.NoError(t, impl.auditDB.QueryRow(
			`SELECT count(*) FROM sqlite_schema WHERE name = ? AND type IN ('table','index','trigger')`, name,
		).Scan(&count), "probe %q", name)
		require.Zero(t, count, "retired relation %q is on a store that never needed it", name)
	}
}
