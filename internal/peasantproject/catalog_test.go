package peasantproject

import (
	"context"
	"crypto/sha256"
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
	ids, err := source.SessionIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a", "ses_b"}, ids)
	require.Equal(t, StateOK, source.Status().State)

	// A caller must not be able to mutate the cached catalog.
	ids[0] = "mutated"
	again, err := source.SessionIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a", "ses_b"}, again)
}

func TestDBSourceSessionCatalogIsEmptyWhenTheStoreHasNoSessions(t *testing.T) {
	t.Parallel()
	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	require.NoError(t, db.Close())

	ids, err := NewDBSource(path).(*dbSource).SessionIDs(context.Background())
	require.NoError(t, err)
	require.Empty(t, ids)
	require.NotNil(t, ids)
}

func TestDBSourceSessionCatalogReportsAnUnavailableDatabase(t *testing.T) {
	t.Parallel()
	source := NewDBSource(t.TempDir() + "/missing-peasant.db").(*dbSource)
	ids, err := source.SessionIDs(context.Background())
	require.Error(t, err)
	require.Nil(t, ids)
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
	require.Equal(t, []string{"ses_a"}, first)

	seedPeasantSession(t, db, "ses_new", "claude-code", validHash('a'))
	cached, err := source.SessionIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a"}, cached, "a read inside the reuse window must not see a later insert")

	source.sessionIDs.mu.Lock()
	source.sessionIDs.at = time.Now().Add(-sessionCatalogCacheTTL - time.Second)
	source.sessionIDs.mu.Unlock()

	refreshed, err := source.SessionIDs(context.Background())
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ses_a", "ses_new"}, refreshed)
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
	ids, err := source.SessionIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a"}, ids)

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

	ids, err := sw.SessionIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a"}, ids)

	off := NewSwitch(nil, nil)
	_, err = off.SessionIDs(context.Background())
	require.ErrorIs(t, err, ErrSessionCatalogUnavailable)
}
