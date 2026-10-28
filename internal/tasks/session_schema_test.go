package tasks_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/stretchr/testify/require"
)

func TestFreshSessionCacheLayoutSurvivesConcurrentOpen(t *testing.T) {
	t.Parallel()
	_, db, path := claimStore(t)
	_, err := db.Exec(`INSERT INTO pasture_session_state (harness,session,first_seen,last_seen,last_event,last_journal_id,updated_at) VALUES ('claude-code','fresh',1,1,'SessionStart',1,1)`)
	require.NoError(t, err)
	var prompt, promptID, end, endID, resume, resumeID int64
	require.NoError(t, db.QueryRow(`SELECT last_prompt_at,last_prompt_journal_id,host_end_at,host_end_journal_id,last_resume_at,last_resume_journal_id FROM pasture_session_state`).Scan(&prompt, &promptID, &end, &endID, &resume, &resumeID))
	require.Zero(t, prompt|promptID|end|endID|resume|resumeID)
	_, err = db.Exec(`UPDATE pasture_session_api_state SET revision=17,dirty=0,completed_through_jid=23 WHERE singleton_id=0`)
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			tracker, err := tasks.OpenTaskTracker(path)
			if err == nil {
				err = tracker.Close()
			}
			results <- err
		}()
	}
	close(start)
	for range 2 {
		require.NoError(t, <-results)
	}
	var revision, dirty, cursor, version int
	require.NoError(t, db.QueryRow(`SELECT revision,dirty,completed_through_jid,cache_version FROM pasture_session_api_state`).Scan(&revision, &dirty, &cursor, &version))
	require.Equal(t, 17, revision)
	require.Zero(t, dirty)
	require.Equal(t, 23, cursor)
	require.Equal(t, audit.SessionCacheVersion, version)
}

func TestExistingSlashStoreAtIdentityFloorAppliesSessionMigration(t *testing.T) {
	t.Parallel()
	tracker, db, path := claimStore(t)
	require.NoError(t, tracker.Close())
	// Preserve the real slash marker, fixed identities and production tables;
	// only put the disposable session layout back at its pre-upgrade shape.
	for _, column := range []string{"last_prompt_at", "last_prompt_journal_id", "host_end_at", "host_end_journal_id", "last_resume_at", "last_resume_journal_id"} {
		_, err := db.Exec(`ALTER TABLE pasture_session_state DROP COLUMN ` + column)
		require.NoError(t, err)
	}
	_, err := db.Exec(`ALTER TABLE pasture_session_api_state DROP COLUMN cache_version`)
	require.NoError(t, err)
	_, err = db.Exec(`DROP TABLE pasture_default_llm_initialization`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM audit_schema_meta WHERE version > ?`, audit.IdentityFormatSchemaVersion)
	require.NoError(t, err)
	reopened, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	require.NoError(t, reopened.Close())
	var version, cacheVersion, dirty int
	require.NoError(t, db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version))
	require.Equal(t, audit.MaxKnownSchemaVersion, version)
	require.NoError(t, db.QueryRow(`SELECT cache_version,dirty FROM pasture_session_api_state`).Scan(&cacheVersion, &dirty))
	require.Equal(t, audit.SessionCacheVersion, cacheVersion)
	require.Equal(t, 1, dirty)
	_, err = db.Exec(`SELECT last_prompt_at,last_resume_journal_id,host_end_at FROM pasture_session_state`)
	require.NoError(t, err)
}
