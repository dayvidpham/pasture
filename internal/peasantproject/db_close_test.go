package peasantproject

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDBSourceCloseLatchesAndLookupDoesNotReopen(t *testing.T) {
	t.Parallel()

	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	seedPeasantProject(t, db, validHash('a'), "/work/repo", "github.com/org/repo")
	seedPeasantSession(t, db, "s-one", "claude-code", validHash('a'))

	source := NewDBSource(path)
	key := Key{Harness: "claude-code", Session: "s-one"}
	projects, err := source.Lookup(context.Background(), []Key{key})
	require.NoError(t, err)
	require.Contains(t, projects, key)

	raw, ok := source.(*dbSource)
	require.True(t, ok)
	require.NoError(t, source.(interface{ Close() error }).Close())
	raw.mu.Lock()
	require.True(t, raw.closed, "Close must latch the source")
	require.Nil(t, raw.db, "Close must clear the handle")
	raw.mu.Unlock()

	// A different key forces a cache-free lookup path through the source; it
	// must fail and stay closed instead of reopening the database.
	projects, err = source.Lookup(context.Background(), []Key{{Harness: "claude-code", Session: "s-two"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "closed")
	require.Empty(t, projects)
	raw.mu.Lock()
	require.Nil(t, raw.db, "a lookup after Close must not reopen the handle")
	raw.mu.Unlock()

	require.NoError(t, source.(interface{ Close() error }).Close(), "a second Close is harmless")
}

func TestCachedSourceCloseComposesWithTheDBSourceLatch(t *testing.T) {
	t.Parallel()

	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	seedPeasantProject(t, db, validHash('a'), "/work/repo", "github.com/org/repo")
	seedPeasantSession(t, db, "s-one", "claude-code", validHash('a'))

	raw := NewDBSource(path)
	cached := NewCached(raw)
	key := Key{Harness: "claude-code", Session: "s-one"}
	_, err := cached.Lookup(context.Background(), []Key{key})
	require.NoError(t, err)
	require.NoError(t, cached.Close())

	dbSource, ok := raw.(*dbSource)
	require.True(t, ok)
	dbSource.mu.Lock()
	require.True(t, dbSource.closed)
	require.Nil(t, dbSource.db)
	dbSource.mu.Unlock()

	// A cache miss after Close must reach the latched source, fail, and leave
	// the handle nil rather than reopening Peasant.
	_, err = cached.Lookup(context.Background(), []Key{{Harness: "claude-code", Session: "s-two"}})
	require.Error(t, err)
	dbSource.mu.Lock()
	require.Nil(t, dbSource.db)
	dbSource.mu.Unlock()
}

func TestDBSourceConcurrentLookupAndCloseDoesNotLeakAHandle(t *testing.T) {
	t.Parallel()

	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	seedPeasantProject(t, db, validHash('a'), "/work/repo", "github.com/org/repo")
	seedPeasantSession(t, db, "s-one", "claude-code", validHash('a'))

	source := NewDBSource(path)
	key := Key{Harness: "claude-code", Session: "s-one"}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = source.Lookup(context.Background(), []Key{key})
		}()
		go func() {
			defer wg.Done()
			_ = source.(interface{ Close() error }).Close()
		}()
	}
	wg.Wait()

	raw := source.(*dbSource)
	raw.mu.Lock()
	defer raw.mu.Unlock()
	require.True(t, raw.closed)
	require.Nil(t, raw.db, "no concurrently opened handle may survive Close")
}
