package peasantproject

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ─── fixtures ─────────────────────────────────────────────────────────────────

func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peasant.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(500)")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db, path
}

func createPeasantSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		CREATE TABLE projects (
			project_hash     TEXT PRIMARY KEY,
			canonical_cwd    TEXT NOT NULL,
			canonical_remote TEXT NOT NULL
		);
		CREATE TABLE sessions (
			session_id    TEXT PRIMARY KEY,
			model_harness TEXT NOT NULL,
			project_hash  TEXT NOT NULL,
			started_at    INTEGER
		)`)
	require.NoError(t, err)
}

func seedPeasantProject(t *testing.T, db *sql.DB, hash, cwd, remote string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO projects (project_hash, canonical_cwd, canonical_remote) VALUES (?, ?, ?)`, hash, cwd, remote)
	require.NoError(t, err)
}

func seedPeasantSession(t *testing.T, db *sql.DB, sessionID, harness, hash string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO sessions (session_id, model_harness, project_hash, started_at) VALUES (?, ?, ?, 1)`, sessionID, harness, hash)
	require.NoError(t, err)
}

func validHash(ch rune) string { return strings.Repeat(string(ch), 64) }

// ─── DB source ────────────────────────────────────────────────────────────────

func TestDBSourceMapsProjectsBySessionAndHarness(t *testing.T) {
	t.Parallel()
	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	seedPeasantProject(t, db, validHash('a'), "/work/repo", "git@github.com:org/repo.git")
	seedPeasantProject(t, db, validHash('b'), "/work/local", "")
	seedPeasantProject(t, db, validHash('c'), "", "")
	seedPeasantProject(t, db, "not-a-hash", "/work/bad", "")
	seedPeasantSession(t, db, "s-remote", "claude-code", validHash('a'))
	seedPeasantSession(t, db, "s-cwd", "codex", validHash('b'))
	seedPeasantSession(t, db, "s-hash", "claude-code", validHash('c'))
	seedPeasantSession(t, db, "s-mismatch", "codex", validHash('a'))
	seedPeasantSession(t, db, "s-badhash", "claude-code", "not-a-hash")

	source := NewDBSource(path)
	require.NotNil(t, source)
	status := source.Status()
	require.True(t, status.Enabled)
	require.Equal(t, SourceDB, status.Source)
	require.Equal(t, StateOK, status.State, status.Message)

	projects, err := source.Lookup(context.Background(), []Key{
		{Harness: "claude-code", Session: "s-remote"},
		{Harness: "codex", Session: "s-cwd"},
		{Harness: "claude-code", Session: "s-hash"},
		{Harness: "claude-code", Session: "s-mismatch"},
		{Harness: "claude-code", Session: "s-badhash"},
		{Harness: "claude-code", Session: "s-missing"},
	})
	require.NoError(t, err)
	require.Equal(t, map[Key]Project{
		{Harness: "claude-code", Session: "s-remote"}: {Name: "git@github.com:org/repo.git", Hash: validHash('a')},
		{Harness: "codex", Session: "s-cwd"}:          {Name: "/work/local", Hash: validHash('b')},
		{Harness: "claude-code", Session: "s-hash"}:   {Name: validHash('c'), Hash: validHash('c')},
	}, projects)
	// The harness mismatch, the malformed hash, and the unknown session are
	// absent rather than guessed from the stored row.
	require.NotContains(t, projects, Key{Harness: "claude-code", Session: "s-mismatch"})
	require.NotContains(t, projects, Key{Harness: "claude-code", Session: "s-badhash"})
	require.NotContains(t, projects, Key{Harness: "claude-code", Session: "s-missing"})
}

func TestDBSourceMissingDatabaseIsUnavailable(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing.db")
	source := NewDBSource(path)

	status := source.Status()
	require.True(t, status.Enabled)
	require.Equal(t, SourceDB, status.Source)
	require.Equal(t, StateUnavailable, status.State)
	require.Contains(t, status.Message, "Peasant database")

	_, err := source.Lookup(context.Background(), []Key{{Harness: "claude-code", Session: "any"}})
	require.Error(t, err)
	_, statErr := os.Stat(path)
	require.True(t, os.IsNotExist(statErr), "a missing Peasant database must never be created")
}

func TestDBSourceRejectsIncompatibleSchema(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		ddl          string
		wantContains []string
	}{
		{
			name: "missing sessions table",
			ddl: `CREATE TABLE projects (
				project_hash TEXT PRIMARY KEY, canonical_cwd TEXT NOT NULL, canonical_remote TEXT NOT NULL)`,
			wantContains: []string{"has no", "sessions"},
		},
		{
			name: "projects missing canonical_remote",
			ddl: `CREATE TABLE projects (
				project_hash TEXT PRIMARY KEY, canonical_cwd TEXT NOT NULL);
				CREATE TABLE sessions (
					session_id TEXT PRIMARY KEY, model_harness TEXT NOT NULL, project_hash TEXT NOT NULL)`,
			wantContains: []string{"projects", "canonical_remote"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			db, path := openTestDB(t)
			_, err := db.Exec(testCase.ddl)
			require.NoError(t, err)

			source := NewDBSource(path)
			status := source.Status()
			require.Equal(t, StateUnavailable, status.State)
			for _, want := range testCase.wantContains {
				require.Contains(t, status.Message, want)
			}
			_, err = source.Lookup(context.Background(), []Key{{Harness: "claude-code", Session: "any"}})
			require.Error(t, err)
		})
	}
}

func TestDBSourceIsReadOnly(t *testing.T) {
	t.Parallel()
	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	source := NewDBSource(path)
	concrete, ok := source.(*dbSource)
	require.True(t, ok)
	require.NotNil(t, concrete.db)

	var queryOnly int
	require.NoError(t, concrete.db.QueryRow(`PRAGMA query_only`).Scan(&queryOnly))
	require.Equal(t, 1, queryOnly, "the read-only source must set query_only")

	_, err := concrete.db.Exec(`
		INSERT INTO projects (project_hash, canonical_cwd, canonical_remote)
		VALUES (?, '/tmp/x', '')`, validHash('d'))
	require.Error(t, err, "a read-only Peasant source must refuse writes")
}

// ─── API source ───────────────────────────────────────────────────────────────

func TestAPISourceBatchesAndVerifiesSummaries(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var requestedIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/session-summaries" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		requestedIDs = append(requestedIDs, r.URL.Query().Get("ids"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiEnvelope{Sessions: []apiSummary{
			{ID: "s1", Harness: "claude-code", Project: "org/repo", ProjectHash: validHash('a')},
			{ID: "s2", Harness: "codex", Project: "wrong harness", ProjectHash: validHash('b')},
			{ID: "s3", Harness: "claude-code", Project: "bad hash", ProjectHash: "nope"},
			{ID: "s4", Harness: "claude-code", ProjectHash: validHash('d')},
			{ID: "s-not-requested", Harness: "claude-code", Project: "extra", ProjectHash: validHash('e')},
		}})
	}))
	t.Cleanup(server.Close)

	source, err := NewAPISource(server.URL)
	require.NoError(t, err)
	projects, err := source.Lookup(context.Background(), []Key{
		{Harness: "claude-code", Session: "s1"},
		{Harness: "claude-code", Session: "s2"},
		{Harness: "claude-code", Session: "s3"},
		{Harness: "claude-code", Session: "s4"},
		{Harness: "claude-code", Session: "s5"},
	})
	require.NoError(t, err)
	require.Equal(t, map[Key]Project{
		{Harness: "claude-code", Session: "s1"}: {Name: "org/repo", Hash: validHash('a')},
		{Harness: "claude-code", Session: "s4"}: {Name: validHash('d'), Hash: validHash('d')},
	}, projects)
	require.NotContains(t, projects, Key{Harness: "claude-code", Session: "s2"}, "a mismatched harness is never applied")
	require.NotContains(t, projects, Key{Harness: "claude-code", Session: "s3"}, "an invalid project hash is skipped")
	require.Equal(t, StateOK, source.Status().State)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, requestedIDs, 1, "one batch request for the page")
	require.ElementsMatch(t, []string{"s1", "s2", "s3", "s4", "s5"}, strings.Split(requestedIDs[0], ","))
}

func TestAPISourceUnavailableAndMalformed(t *testing.T) {
	t.Parallel()
	t.Run("http error", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		}))
		t.Cleanup(server.Close)
		source, err := NewAPISource(server.URL)
		require.NoError(t, err)
		_, err = source.Lookup(context.Background(), []Key{{Harness: "claude-code", Session: "s1"}})
		require.Error(t, err)
		status := source.Status()
		require.Equal(t, StateUnavailable, status.State)
		require.Contains(t, status.Message, "HTTP 500")
	})
	t.Run("malformed body", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"sessions":`))
		}))
		t.Cleanup(server.Close)
		source, err := NewAPISource(server.URL)
		require.NoError(t, err)
		_, err = source.Lookup(context.Background(), []Key{{Harness: "claude-code", Session: "s1"}})
		require.Error(t, err)
		require.Equal(t, StateUnavailable, source.Status().State)
	})
	t.Run("unreachable", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
		url := server.URL
		server.Close()
		source, err := NewAPISource(url)
		require.NoError(t, err)
		_, err = source.Lookup(context.Background(), []Key{{Harness: "claude-code", Session: "s1"}})
		require.Error(t, err)
		require.Equal(t, StateUnavailable, source.Status().State)
	})
}

func TestAPISourceRejectsMalformedURL(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "   ", "://bad", "ftp://example.com", "http://"} {
		_, err := NewAPISource(raw)
		require.Error(t, err, "URL %q must be refused", raw)
	}
	source, err := NewAPISource("http://127.0.0.1:8787/")
	require.NoError(t, err)
	require.Equal(t, SourceAPI, source.Status().Source)
}

// ─── cache/coalescing ─────────────────────────────────────────────────────────

type countingSource struct {
	mu       sync.Mutex
	projects map[Key]Project
	err      error
	status   Status
	calls    int
	started  chan struct{}
	release  chan struct{}
}

func (s *countingSource) Lookup(ctx context.Context, keys []Key) (map[Key]Project, error) {
	s.mu.Lock()
	s.calls++
	started := s.started
	release := s.release
	err := s.err
	projects := make(map[Key]Project, len(s.projects))
	for key, project := range s.projects {
		projects[key] = project
	}
	s.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	out := make(map[Key]Project, len(keys))
	for _, key := range keys {
		if project, ok := projects[key]; ok {
			out[key] = project
		}
	}
	return out, nil
}

func (s *countingSource) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *countingSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestCachedSourcePositiveNegativeAndExpiry(t *testing.T) {
	t.Parallel()
	key := Key{Harness: "claude-code", Session: "s1"}
	missing := Key{Harness: "claude-code", Session: "missing"}
	inner := &countingSource{
		projects: map[Key]Project{key: {Name: "org/repo", Hash: validHash('a')}},
		status:   Status{Enabled: true, Source: SourceAPI, State: StateOK},
	}
	now := time.Unix(1700000000, 0)
	source := newCached(inner, 30*time.Second, func() time.Time { return now })

	projects, err := source.Lookup(context.Background(), []Key{key})
	require.NoError(t, err)
	require.Equal(t, Project{Name: "org/repo", Hash: validHash('a')}, projects[key])
	require.Equal(t, 1, inner.callCount())

	_, err = source.Lookup(context.Background(), []Key{key})
	require.NoError(t, err)
	require.Equal(t, 1, inner.callCount(), "a cached positive result must not call the source again")

	_, err = source.Lookup(context.Background(), []Key{missing})
	require.NoError(t, err)
	require.Equal(t, 2, inner.callCount())
	_, err = source.Lookup(context.Background(), []Key{missing})
	require.NoError(t, err)
	require.Equal(t, 2, inner.callCount(), "a cached absence must not call the source again")

	now = now.Add(31 * time.Second)
	_, err = source.Lookup(context.Background(), []Key{missing})
	require.NoError(t, err)
	require.Equal(t, 3, inner.callCount(), "an expired entry must be refreshed")
}

func TestCachedSourceCachesFailures(t *testing.T) {
	t.Parallel()
	inner := &countingSource{
		err:    os.ErrDeadlineExceeded,
		status: Status{Enabled: true, Source: SourceDB, State: StateUnavailable, Message: "unavailable"},
	}
	source := newCached(inner, time.Minute, time.Now)
	key := Key{Harness: "claude-code", Session: "s1"}
	_, err := source.Lookup(context.Background(), []Key{key})
	require.Error(t, err)
	_, err = source.Lookup(context.Background(), []Key{key})
	require.Error(t, err)
	require.Equal(t, 1, inner.callCount(), "a cached failure must not be retried on every refresh tick")
	require.Equal(t, StateUnavailable, source.Status().State)
}

func TestCachedSourceCoalescesConcurrentLookups(t *testing.T) {
	t.Parallel()
	key := Key{Harness: "claude-code", Session: "s1"}
	inner := &countingSource{
		projects: map[Key]Project{key: {Name: "org/repo", Hash: validHash('a')}},
		status:   Status{Enabled: true, Source: SourceAPI, State: StateOK},
		started:  make(chan struct{}, 1),
		release:  make(chan struct{}),
	}
	source := newCached(inner, time.Minute, time.Now)

	const callers = 8
	results := make(chan map[Key]Project, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			projects, err := source.Lookup(context.Background(), []Key{key})
			results <- projects
			errs <- err
		}()
	}
	// Wait until the leader is inside the source, then let it finish. A caller
	// that arrives after completion reads the cache, so either path proves one
	// call into the source.
	select {
	case <-inner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the cache never reached the underlying source")
	}
	close(inner.release)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	for projects := range results {
		require.Equal(t, Project{Name: "org/repo", Hash: validHash('a')}, projects[key])
	}
	require.Equal(t, 1, inner.callCount(), "concurrent lookups for the same set must coalesce")
}
