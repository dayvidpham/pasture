package peasantproject

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDBSourceListsSessionIDs(t *testing.T) {
	t.Parallel()
	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	seedPeasantProject(t, db, validHash('a'), "/work/repo", "")
	seedPeasantSession(t, db, "ses_b", "claude-code", validHash('a'))
	seedPeasantSession(t, db, "ses_a", "codex", validHash('a'))
	seedPeasantSession(t, db, "", "claude-code", validHash('a'))
	require.NoError(t, db.Close())

	source := NewDBSource(path).(*dbSource)
	listed, err := source.SessionIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a", "ses_b"}, listed.IDs)
	require.Empty(t, listed.Subagents)
	require.Equal(t, StateOK, source.Status().State)

	// A caller must not be able to mutate the cached catalog.
	listed.IDs[0] = "mutated"
	listed.Subagents = append(listed.Subagents, "extra")
	again, err := source.SessionIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a", "ses_b"}, again.IDs)
	require.Empty(t, again.Subagents)
}

func TestDBSourceSessionCatalogIsEmptyWhenTheStoreHasNoSessions(t *testing.T) {
	t.Parallel()
	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	require.NoError(t, db.Close())

	listed, err := NewDBSource(path).(*dbSource).SessionIDs(context.Background())
	require.NoError(t, err)
	require.Empty(t, listed.IDs)
	require.NotNil(t, listed.IDs)
	require.Empty(t, listed.Subagents)
	require.NotNil(t, listed.Subagents)
}

func TestDBSourceSessionCatalogReportsAnUnavailableDatabase(t *testing.T) {
	t.Parallel()
	source := NewDBSource(t.TempDir() + "/missing-peasant.db").(*dbSource)
	listed, err := source.SessionIDs(context.Background())
	require.Error(t, err)
	require.Nil(t, listed.IDs)
	require.Nil(t, listed.Subagents)
	require.Contains(t, err.Error(), "could not be opened")
	require.Equal(t, StateUnavailable, source.Status().State)
	require.NotEmpty(t, source.Status().Message)
}

func TestDBSourceSessionCatalogReusesASuccessfulRead(t *testing.T) {
	t.Parallel()
	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	seedPeasantProject(t, db, validHash('a'), "/work/repo", "")
	seedPeasantSession(t, db, "ses_a", "claude-code", validHash('a'))

	source := NewDBSource(path).(*dbSource)
	first, err := source.SessionIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a"}, first.IDs)

	seedPeasantSession(t, db, "ses_new", "claude-code", validHash('a'))
	cached, err := source.SessionIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a"}, cached.IDs, "a read inside the reuse window must not see a later insert")

	source.sessionIDs.mu.Lock()
	source.sessionIDs.at = time.Now().Add(-sessionCatalogCacheTTL - time.Second)
	source.sessionIDs.mu.Unlock()

	refreshed, err := source.SessionIDs(context.Background())
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ses_a", "ses_new"}, refreshed.IDs)
}

func TestDBSourceSessionCatalogDoesNotWriteOrMigrate(t *testing.T) {
	t.Parallel()
	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	seedPeasantProject(t, db, validHash('a'), "/work/repo", "")
	seedPeasantSession(t, db, "ses_a", "claude-code", validHash('a'))
	require.NoError(t, db.Close())

	before, err := os.ReadFile(path)
	require.NoError(t, err)
	beforeHash := sha256.Sum256(before)

	source := NewDBSource(path).(*dbSource)
	listed, err := source.SessionIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a"}, listed.IDs)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, beforeHash, sha256.Sum256(after), "listing session ids must not rewrite the Peasant database")

	var queryOnly int
	require.NoError(t, source.db.QueryRow(`PRAGMA query_only`).Scan(&queryOnly))
	require.Equal(t, 1, queryOnly)
	var tables int
	require.NoError(t, source.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name NOT IN ('projects', 'sessions') AND type = 'table'`).Scan(&tables))
	require.Equal(t, 0, tables, "the catalog read must not create or migrate tables")
}

func TestDBSourceSessionCatalogRefusesAnOversizeStoreAndDoesNotCacheIt(t *testing.T) {
	t.Parallel()
	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	hash := validHash('a')
	seedPeasantProject(t, db, hash, "/work/repo", "")
	seedSessionCatalog(t, db, hash, sessionCatalogLimit+1)

	source := NewDBSource(path).(*dbSource)
	listed, err := source.SessionIDs(context.Background())
	require.Error(t, err)
	require.Nil(t, listed.IDs)
	require.NotErrorIs(t, err, ErrSessionCatalogUnavailable)
	require.Contains(t, err.Error(), fmt.Sprintf("more than %d", sessionCatalogLimit))
	require.Equal(t, StateUnavailable, source.Status().State)
	source.sessionIDs.mu.Lock()
	require.False(t, source.sessionIDs.valid, "an oversize catalog must not be reused")
	source.sessionIDs.mu.Unlock()

	_, err = db.Exec(`DELETE FROM sessions WHERE session_id <> 's000000'`)
	require.NoError(t, err)

	remaining, err := source.SessionIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"s000000"}, remaining.IDs)
	require.Empty(t, remaining.Subagents)
	require.Equal(t, StateOK, source.Status().State)
}

func seedSessionCatalog(t *testing.T, db *sql.DB, hash string, count int) {
	t.Helper()
	tx, err := db.Begin()
	require.NoError(t, err)
	stmt, err := tx.Prepare(`INSERT INTO sessions (session_id, model_harness, project_hash, started_at) VALUES (?, 'claude-code', ?, 1)`)
	require.NoError(t, err)
	for i := 0; i < count; i++ {
		_, err = stmt.Exec(fmt.Sprintf("s%06d", i), hash)
		require.NoError(t, err)
	}
	require.NoError(t, stmt.Close())
	require.NoError(t, tx.Commit())
}

func TestDBSourceSeparatesSubagentsFromRoots(t *testing.T) {
	t.Parallel()
	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	hash := validHash('a')
	seedPeasantProject(t, db, hash, "/work/repo", "")
	seedPeasantSession(t, db, "ses_root", "claude-code", hash)
	_, err := db.Exec(`INSERT INTO sessions (session_id, model_harness, project_hash, parent_id, started_at) VALUES (?, 'claude-code', ?, ?, 1)`, "ses_child", hash, "ses_root")
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO sessions (session_id, model_harness, project_hash, parent_id, started_at) VALUES (?, 'claude-code', ?, ?, 1)`, "ses_blank_parent", hash, "   ")
	require.NoError(t, err)
	require.NoError(t, db.Close())

	listed, err := NewDBSource(path).(*dbSource).SessionIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_blank_parent", "ses_child", "ses_root"}, listed.IDs)
	require.Equal(t, []string{"ses_child"}, listed.Subagents, "only a non-empty parent_id is a subagent; blank and null parents stay roots")
}

func TestSwitchAndCacheDelegateTheSessionCatalog(t *testing.T) {
	t.Parallel()
	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	seedPeasantProject(t, db, validHash('a'), "/work/repo", "")
	seedPeasantSession(t, db, "ses_a", "claude-code", validHash('a'))
	require.NoError(t, db.Close())

	raw := NewDBSource(path)
	cached := NewCached(raw)
	sw := NewSwitch(nil, cached)

	listed, err := sw.SessionIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a"}, listed.IDs)
	require.Empty(t, listed.Subagents)

	off := NewSwitch(nil, nil)
	_, err = off.SessionIDs(context.Background())
	require.ErrorIs(t, err, ErrSessionCatalogUnavailable)
}
