package idmigrate

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/stretchr/testify/require"
)

// Add the pre-watermark session layout to the existing lifecycle-bearing
// populated conversion fixture, to exercise real post-identity DDL.
func retiredStoreBeforeSessionMigration(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path, db := lifecycleBearingStore(t)
	for _, statement := range []string{
		`CREATE TABLE pasture_session_state (harness TEXT NOT NULL, session TEXT NOT NULL, first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, last_event TEXT NOT NULL, last_journal_id INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY(harness,session))`,
		`CREATE TABLE pasture_session_api_state (singleton_id INTEGER PRIMARY KEY, revision INTEGER NOT NULL, dirty INTEGER NOT NULL, completed_through_jid INTEGER NOT NULL)`,
		`INSERT INTO pasture_session_api_state VALUES (0,7,0,42)`,
	} {
		_, err := db.Exec(statement)
		require.NoError(t, err)
	}
	return path, db
}

func TestPopulatedConversionAppliesRealSessionMigrationAfterIdentityFloor(t *testing.T) {
	t.Parallel()
	path, db := retiredStoreBeforeSessionMigration(t)
	report, err := Convert(context.Background(), db, ConvertOptions{StorePath: path})
	require.NoError(t, err)
	require.Equal(t, audit.IdentityFormatSchemaVersion, report.FloorVersion)
	state, err := EvaluateCutoverOnHandle(context.Background(), db, path, true)
	require.NoError(t, err)
	require.Equal(t, OutcomeNormal, state.Outcome)
	require.Equal(t, audit.MaxKnownSchemaVersion, state.RecordedVersion)
	var cacheVersion, dirty int
	require.NoError(t, db.QueryRow(`SELECT cache_version,dirty FROM pasture_session_api_state WHERE singleton_id=0`).Scan(&cacheVersion, &dirty))
	require.Equal(t, audit.SessionCacheVersion, cacheVersion)
	require.Equal(t, 1, dirty)
	_, err = db.Exec(`SELECT last_prompt_at,host_end_journal_id,last_resume_at FROM pasture_session_state`)
	require.NoError(t, err, "later migration must run, not merely stamp the latest version")
}

func TestPostIdentitySessionMigrationRollsBackWithConversion(t *testing.T) {
	t.Parallel()
	path, db := retiredStoreBeforeSessionMigration(t)
	sentinel := errors.New("constructed failure after all migrations")
	opts := ConvertOptions{StorePath: path}
	opts.injectInTx = injectionSeam(injectFinal, sentinel)
	_, err := Convert(context.Background(), db, opts)
	require.ErrorContains(t, err, sentinel.Error())
	state, err := EvaluateCutoverOnHandle(context.Background(), db, path, true)
	require.NoError(t, err)
	require.Equal(t, audit.IdentityFormatSchemaVersion-1, state.RecordedVersion)
	require.False(t, state.MarkerPresent)
	require.False(t, state.ReceiptPresent)
	_, err = db.Exec(`SELECT last_prompt_at FROM pasture_session_state`)
	require.Error(t, err, "cache DDL must roll back together with marker and receipt")
	_, err = db.Exec(`SELECT cache_version FROM pasture_session_api_state`)
	require.Error(t, err)
}
