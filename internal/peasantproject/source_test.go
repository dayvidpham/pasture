package peasantproject

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

func openTestDBAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(500)")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peasant.db")
	return openTestDBAt(t, path), path
}

// createPeasantSchema mirrors the Peasant-owned projects/sessions shape: both
// canonical project columns are nullable, because a project may be identified
// only by its hash.
func createPeasantSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		CREATE TABLE projects (
			project_hash     TEXT PRIMARY KEY,
			canonical_cwd    TEXT,
			canonical_remote TEXT
		);
		CREATE TABLE sessions (
			session_id    TEXT PRIMARY KEY,
			model_harness TEXT NOT NULL,
			project_hash  TEXT NOT NULL,
			started_at    INTEGER
		)`)
	require.NoError(t, err)
}

func seedPeasantProject(t *testing.T, db *sql.DB, hash string, cwd, remote any) {
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
	seedPeasantProject(t, db, validHash('a'), "/work/repo", "github.com/org/repo")
	seedPeasantProject(t, db, validHash('b'), "/work/local", "")
	seedPeasantProject(t, db, validHash('c'), "", "")
	seedPeasantProject(t, db, validHash('d'), nil, "github.com/org/null-cwd")
	seedPeasantProject(t, db, validHash('e'), "/work/null-remote", nil)
	seedPeasantProject(t, db, validHash('f'), nil, nil)
	seedPeasantProject(t, db, "not-a-hash", "/work/bad", "")
	seedPeasantSession(t, db, "s-remote", "claude-code", validHash('a'))
	seedPeasantSession(t, db, "s-cwd", "codex", validHash('b'))
	seedPeasantSession(t, db, "s-hash", "claude-code", validHash('c'))
	seedPeasantSession(t, db, "s-null-cwd", "claude-code", validHash('d'))
	seedPeasantSession(t, db, "s-null-remote", "claude-code", validHash('e'))
	seedPeasantSession(t, db, "s-null-both", "claude-code", validHash('f'))
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
		{Harness: "claude-code", Session: "s-null-cwd"},
		{Harness: "claude-code", Session: "s-null-remote"},
		{Harness: "claude-code", Session: "s-null-both"},
		{Harness: "claude-code", Session: "s-mismatch"},
		{Harness: "claude-code", Session: "s-badhash"},
		{Harness: "claude-code", Session: "s-missing"},
	})
	require.NoError(t, err)
	require.Equal(t, map[Key]Project{
		// The stored bare remote normalizes to Peasant's host:owner/repo label.
		{Harness: "claude-code", Session: "s-remote"}:      {Name: "github.com:org/repo", Hash: validHash('a')},
		{Harness: "codex", Session: "s-cwd"}:               {Name: "/work/local", Hash: validHash('b')},
		{Harness: "claude-code", Session: "s-hash"}:        {Name: validHash('c'), Hash: validHash('c')},
		{Harness: "claude-code", Session: "s-null-cwd"}:    {Name: "github.com:org/null-cwd", Hash: validHash('d')},
		{Harness: "claude-code", Session: "s-null-remote"}: {Name: "/work/null-remote", Hash: validHash('e')},
		{Harness: "claude-code", Session: "s-null-both"}:   {Name: validHash('f'), Hash: validHash('f')},
	}, projects)
	// The harness mismatch, the malformed hash, and the unknown session are
	// absent rather than guessed from the stored row.
	require.NotContains(t, projects, Key{Harness: "claude-code", Session: "s-mismatch"})
	require.NotContains(t, projects, Key{Harness: "claude-code", Session: "s-badhash"})
	require.NotContains(t, projects, Key{Harness: "claude-code", Session: "s-missing"})
}

// TestDBSourceProjectLabelNormalization pins the DB label chain Peasant uses:
// canonical remote label, else canonical cwd, else hash, including the remote
// spellings Peasant normalizes.
func TestDBSourceProjectLabelNormalization(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		cwd    any
		remote any
		want   string // empty means the project hash
	}{
		{name: "scp remote", cwd: "/work/repo", remote: "git@github.com:org/repo.git", want: "github.com:org/repo"},
		{name: "https remote", cwd: "/work/repo", remote: "https://github.com/org/repo.git", want: "github.com:org/repo"},
		{name: "null remote falls back to cwd", cwd: "/work/repo", remote: nil, want: "/work/repo"},
		{name: "null remote and cwd fall back to hash", cwd: nil, remote: nil, want: ""},
	}
	for i, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			db, path := openTestDB(t)
			createPeasantSchema(t, db)
			hash := validHash(rune('a' + i))
			seedPeasantProject(t, db, hash, testCase.cwd, testCase.remote)
			seedPeasantSession(t, db, "s1", "claude-code", hash)

			source := NewDBSource(path)
			key := Key{Harness: "claude-code", Session: "s1"}
			projects, err := source.Lookup(context.Background(), []Key{key})
			require.NoError(t, err)
			want := testCase.want
			if want == "" {
				want = hash
			}
			require.Equal(t, want, projects[key].Name)
			require.Equal(t, hash, projects[key].Hash)
		})
	}
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

// TestDBSourceRecoversWhenDatabaseAppearsAfterStartup pins the non-latching
// rule: the constructor's failed probe must not be permanent, because Peasant
// may create its database after serve starts.
func TestDBSourceRecoversWhenDatabaseAppearsAfterStartup(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "peasant.db")
	source := NewDBSource(path)
	require.Equal(t, StateUnavailable, source.Status().State)

	db := openTestDBAt(t, path)
	createPeasantSchema(t, db)
	seedPeasantProject(t, db, validHash('a'), "/work/repo", "github.com/org/repo")
	seedPeasantSession(t, db, "s1", "claude-code", validHash('a'))

	key := Key{Harness: "claude-code", Session: "s1"}
	projects, err := source.Lookup(context.Background(), []Key{key})
	require.NoError(t, err)
	require.Equal(t, Project{Name: "github.com:org/repo", Hash: validHash('a')}, projects[key])
	require.Equal(t, StateOK, source.Status().State)
}

// TestDBSourceCanceledBeforeEntryLeavesStatusUnchanged pins the pre-entry
// cancellation path: the caller is already done when Lookup starts, so the
// handle/query work never begins and the status is untouched. The mid-query
// cancellation path is covered by TestDBSourceMidQueryCancellationLeavesStatusUnchanged.
func TestDBSourceCanceledBeforeEntryLeavesStatusUnchanged(t *testing.T) {
	t.Parallel()
	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	seedPeasantProject(t, db, validHash('a'), "/work/repo", "github.com/org/repo")
	seedPeasantSession(t, db, "s1", "claude-code", validHash('a'))

	source := NewDBSource(path)
	require.Equal(t, StateOK, source.Status().State)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	key := Key{Harness: "claude-code", Session: "s1"}
	_, err := source.Lookup(ctx, []Key{key})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, StateOK, source.Status().State, "a caller cancellation must not mark the source unavailable")

	projects, err := source.Lookup(context.Background(), []Key{key})
	require.NoError(t, err)
	require.Equal(t, Project{Name: "github.com:org/repo", Hash: validHash('a')}, projects[key])
	require.Equal(t, StateOK, source.Status().State)
}

// TestStatusHolderMarkFailureIgnoresCanceledContext pins the shared status
// rule directly: a canceled caller cannot label the source unavailable.
func TestStatusHolderMarkFailureIgnoresCanceledContext(t *testing.T) {
	t.Parallel()
	holder := newStatusHolder(SourceDB)
	require.Equal(t, StateOK, holder.get().State)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	holder.markFailure(ctx, "boom")
	status := holder.get()
	require.Equal(t, StateOK, status.State)
	require.Empty(t, status.Message)

	holder.markFailure(context.Background(), "boom")
	status = holder.get()
	require.Equal(t, StateUnavailable, status.State)
	require.Equal(t, "boom", status.Message)
}

// TestDBSourceMidQueryCancellationLeavesStatusUnchanged drives the ctx.Err()
// branch inside Lookup: the read-only query is blocked by another connection's
// exclusive lock when the caller's deadline fires.
func TestDBSourceMidQueryCancellationLeavesStatusUnchanged(t *testing.T) {
	t.Parallel()
	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	seedPeasantProject(t, db, validHash('a'), "/work/repo", "github.com/org/repo")
	seedPeasantSession(t, db, "s1", "claude-code", validHash('a'))

	source := NewDBSource(path)
	require.Equal(t, StateOK, source.Status().State)

	// Pin one writer connection so the exclusive lock and its rollback stay on
	// the same connection.
	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = conn.ExecContext(context.Background(), "BEGIN EXCLUSIVE")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	key := Key{Harness: "claude-code", Session: "s1"}
	_, err = source.Lookup(ctx, []Key{key})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, StateOK, source.Status().State, "a caller deadline must not mark the source unavailable")
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
				project_hash TEXT PRIMARY KEY, canonical_cwd TEXT, canonical_remote TEXT)`,
			wantContains: []string{"has no", "sessions"},
		},
		{
			name: "projects missing canonical_remote",
			ddl: `CREATE TABLE projects (
				project_hash TEXT PRIMARY KEY, canonical_cwd TEXT);
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
	require.Equal(t, StateUnavailable, source.Status().State, "a configured API source starts uncontacted")
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

// TestAPISourceCanceledBeforeEntryLeavesStatusUnchanged pins the pre-entry
// cancellation path for the HTTP backend, including the uncontacted initial
// state. The mid-flight path is covered by
// TestAPISourceMidFlightCancellationLeavesStatusUnchanged.
func TestAPISourceCanceledBeforeEntryLeavesStatusUnchanged(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiEnvelope{})
	}))
	t.Cleanup(server.Close)
	source, err := NewAPISource(server.URL)
	require.NoError(t, err)
	initial := source.Status()
	require.Equal(t, StateUnavailable, initial.State)
	require.Contains(t, initial.Message, "not been contacted")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	key := Key{Harness: "claude-code", Session: "s1"}
	_, err = source.Lookup(ctx, []Key{key})
	require.ErrorIs(t, err, context.Canceled)
	status := source.Status()
	require.Equal(t, StateUnavailable, status.State)
	require.Equal(t, initial.Message, status.Message, "a caller cancellation must not rewrite the status message")

	_, err = source.Lookup(context.Background(), []Key{key})
	require.NoError(t, err)
	require.Equal(t, StateOK, source.Status().State)
}

// TestAPISourceMidFlightCancellationLeavesStatusUnchanged drives cancellation
// after the request has reached Peasant: the handler sends headers, then blocks
// until the caller's deadline tears the request down.
func TestAPISourceMidFlightCancellationLeavesStatusUnchanged(t *testing.T) {
	t.Parallel()
	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	source, err := NewAPISource(server.URL)
	require.NoError(t, err)
	initial := source.Status()
	require.Equal(t, StateUnavailable, initial.State)
	require.Contains(t, initial.Message, "not been contacted")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = source.Lookup(ctx, []Key{{Harness: "claude-code", Session: "s1"}})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	status := source.Status()
	require.Equal(t, StateUnavailable, status.State)
	require.Equal(t, initial.Message, status.Message, "a caller deadline must not rewrite the status message")
	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the test handler did not observe the canceled request")
	}
}

// TestAPISourceEnforcesSharedKeyBound pins the same batch bound the DB source
// uses, and that a caller error does not masquerade as a source failure.
func TestAPISourceEnforcesSharedKeyBound(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(apiEnvelope{})
	}))
	t.Cleanup(server.Close)
	source, err := NewAPISource(server.URL)
	require.NoError(t, err)

	keys := make([]Key, maxLookupKeys+1)
	for i := range keys {
		keys[i] = Key{Harness: "claude-code", Session: "s" + strings.Repeat("x", i+1)}
	}
	_, err = source.Lookup(context.Background(), keys)
	require.Error(t, err)
	require.Contains(t, err.Error(), "above the 1000-session bound")
	require.Equal(t, StateUnavailable, source.Status().State)
	require.Contains(t, source.Status().Message, "not been contacted")
	mu.Lock()
	require.Zero(t, requests, "an over-bound request must not reach Peasant")
	mu.Unlock()
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

// TestAPILookupErrorMarksOnlySourceOwnedTimeouts pins the sentinel boundary:
// an expired lookup context marks the error cacheable; a live context does not.
func TestAPILookupErrorMarksOnlySourceOwnedTimeouts(t *testing.T) {
	t.Parallel()
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := apiLookupError("reaching the Peasant API", expired, context.DeadlineExceeded)
	require.ErrorIs(t, err, errSourceLookupTimeout)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	live, liveCancel := context.WithCancel(context.Background())
	defer liveCancel()
	err = apiLookupError("reaching the Peasant API", live, fmt.Errorf("connection refused"))
	require.NotErrorIs(t, err, errSourceLookupTimeout)
}

// TestDBSourceLabelMatchesPeasantRemoteLabelRule pins the DB label rule against
// Peasant's documented remote-label output. The API side copies Peasant's
// already-resolved label, so the cross-check also proves the DB fallback does
// not invent a different spelling.
func TestDBSourceLabelMatchesPeasantRemoteLabelRule(t *testing.T) {
	t.Parallel()
	db, path := openTestDB(t)
	createPeasantSchema(t, db)
	seedPeasantProject(t, db, validHash('a'), "/work/repo", "github.com/org/repo")
	seedPeasantSession(t, db, "s1", "claude-code", validHash('a'))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiEnvelope{Sessions: []apiSummary{
			{ID: "s1", Harness: "claude-code", Project: "github.com:org/repo", ProjectHash: validHash('a')},
		}})
	}))
	t.Cleanup(server.Close)

	key := Key{Harness: "claude-code", Session: "s1"}
	dbSource := NewDBSource(path)
	dbProjects, err := dbSource.Lookup(context.Background(), []Key{key})
	require.NoError(t, err)

	apiSource, err := NewAPISource(server.URL)
	require.NoError(t, err)
	apiProjects, err := apiSource.Lookup(context.Background(), []Key{key})
	require.NoError(t, err)

	require.Equal(t, "github.com:org/repo", dbProjects[key].Name)
	require.Equal(t, dbProjects[key].Name, apiProjects[key].Name)
	require.Equal(t, dbProjects[key].Hash, apiProjects[key].Hash)
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

// TestCachedSourceDoesNotCacheCallerCancellation pins that a canceled caller
// neither poisons the cache nor strands the coalesced waiter.
func TestCachedSourceDoesNotCacheCallerCancellation(t *testing.T) {
	t.Parallel()
	key := Key{Harness: "claude-code", Session: "s1"}
	inner := &countingSource{
		projects: map[Key]Project{key: {Name: "org/repo", Hash: validHash('a')}},
		status:   Status{Enabled: true, Source: SourceAPI, State: StateOK},
		started:  make(chan struct{}, 1),
		release:  make(chan struct{}),
	}
	source := newCached(inner, time.Minute, time.Now)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := source.Lookup(ctx, []Key{key})
		result <- err
	}()
	select {
	case <-inner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the cache never reached the underlying source")
	}
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)

	close(inner.release)
	projects, err := source.Lookup(context.Background(), []Key{key})
	require.NoError(t, err)
	require.Equal(t, Project{Name: "org/repo", Hash: validHash('a')}, projects[key])
	require.Equal(t, 2, inner.callCount(), "a canceled result must not be served from the cache")
}

// TestCachedSourcePrunesExpiredEntriesAndHonorsBound pins the bounded cache:
// expired entries are dropped on write and the entry count never exceeds the
// configured limit.
func TestCachedSourcePrunesExpiredEntriesAndHonorsBound(t *testing.T) {
	t.Parallel()
	inner := &countingSource{status: Status{Enabled: true, Source: SourceAPI, State: StateOK}}
	now := time.Unix(1700000000, 0)
	source := newCachedWithLimit(inner, 30*time.Second, func() time.Time { return now }, 4)

	for i, key := range []Key{
		{Harness: "claude-code", Session: "k1"},
		{Harness: "claude-code", Session: "k2"},
		{Harness: "claude-code", Session: "k3"},
		{Harness: "claude-code", Session: "k4"},
	} {
		_, err := source.Lookup(context.Background(), []Key{key})
		require.NoError(t, err)
		require.Len(t, source.entries, i+1)
	}

	now = now.Add(31 * time.Second)
	k5 := Key{Harness: "claude-code", Session: "k5"}
	_, err := source.Lookup(context.Background(), []Key{k5})
	require.NoError(t, err)
	require.Len(t, source.entries, 1, "expired entries must be pruned on write")
	require.Contains(t, source.entries, k5)

	for i := 0; i < 6; i++ {
		key := Key{Harness: "claude-code", Session: "bound-" + strings.Repeat("x", i+1)}
		_, err := source.Lookup(context.Background(), []Key{key})
		require.NoError(t, err)
		require.LessOrEqual(t, len(source.entries), source.limit)
	}
}

// TestCachedSourceEvictsWholeBatchOverLimit pins the snapshot in
// evictToLimitLocked: a single batch insert of three keys over a limit of four
// must evict all three excess entries, not half of them.
func TestCachedSourceEvictsWholeBatchOverLimit(t *testing.T) {
	t.Parallel()
	inner := &countingSource{status: Status{Enabled: true, Source: SourceAPI, State: StateOK}}
	source := newCachedWithLimit(inner, time.Minute, time.Now, 4)

	for i := 0; i < 4; i++ {
		key := Key{Harness: "claude-code", Session: "prefill-" + strings.Repeat("x", i+1)}
		_, err := source.Lookup(context.Background(), []Key{key})
		require.NoError(t, err)
	}
	require.Len(t, source.entries, 4)

	batch := []Key{
		{Harness: "claude-code", Session: "batch-1"},
		{Harness: "claude-code", Session: "batch-2"},
		{Harness: "claude-code", Session: "batch-3"},
	}
	_, err := source.Lookup(context.Background(), batch)
	require.NoError(t, err)
	require.Len(t, source.entries, 4, "a three-key batch on a four-entry cache must evict all three excess entries")
}

// TestCachedSourceRecoversAfterTTL pins negative caching as bounded: a cached
// failure is replayed inside the TTL and refreshed after it.
func TestCachedSourceRecoversAfterTTL(t *testing.T) {
	t.Parallel()
	key := Key{Harness: "claude-code", Session: "s1"}
	inner := &countingSource{
		err:    os.ErrDeadlineExceeded,
		status: Status{Enabled: true, Source: SourceDB, State: StateUnavailable},
	}
	now := time.Unix(1700000000, 0)
	source := newCached(inner, 30*time.Second, func() time.Time { return now })

	_, err := source.Lookup(context.Background(), []Key{key})
	require.Error(t, err)

	inner.mu.Lock()
	inner.err = nil
	inner.projects = map[Key]Project{key: {Name: "org/repo", Hash: validHash('a')}}
	inner.mu.Unlock()

	_, err = source.Lookup(context.Background(), []Key{key})
	require.Error(t, err, "the cached failure must still be replayed inside the TTL")
	require.Equal(t, 1, inner.callCount())

	now = now.Add(31 * time.Second)
	projects, err := source.Lookup(context.Background(), []Key{key})
	require.NoError(t, err)
	require.Equal(t, Project{Name: "org/repo", Hash: validHash('a')}, projects[key])
	require.Equal(t, 2, inner.callCount(), "the entry must be refreshed after the TTL")
}

// TestCachedSourceCachesSourceOwnedDeadline pins that a timeout at the source's
// own bound is cached, so a hung Peasant is not retried every refresh tick.
func TestCachedSourceCachesSourceOwnedDeadline(t *testing.T) {
	t.Parallel()
	inner := &countingSource{
		err:    fmt.Errorf("%s: %w: %w", "reaching the Peasant API", errSourceLookupTimeout, context.DeadlineExceeded),
		status: Status{Enabled: true, Source: SourceAPI, State: StateUnavailable},
	}
	source := newCached(inner, time.Minute, time.Now)
	key := Key{Harness: "claude-code", Session: "s1"}

	_, err := source.Lookup(context.Background(), []Key{key})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, err, errSourceLookupTimeout)

	_, err = source.Lookup(context.Background(), []Key{key})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, inner.callCount(), "a source-owned deadline must be cached")
}

// TestCachedSourceDoesNotCacheCallerDeadline pins that the caller's own
// deadline still bypasses the cache, unlike a source-owned timeout.
func TestCachedSourceDoesNotCacheCallerDeadline(t *testing.T) {
	t.Parallel()
	key := Key{Harness: "claude-code", Session: "s1"}
	inner := &countingSource{
		projects: map[Key]Project{key: {Name: "org/repo", Hash: validHash('a')}},
		status:   Status{Enabled: true, Source: SourceAPI, State: StateOK},
		release:  make(chan struct{}),
	}
	source := newCached(inner, time.Minute, time.Now)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := source.Lookup(ctx, []Key{key})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	close(inner.release)
	projects, err := source.Lookup(context.Background(), []Key{key})
	require.NoError(t, err)
	require.Equal(t, Project{Name: "org/repo", Hash: validHash('a')}, projects[key])
	require.Equal(t, 2, inner.callCount(), "a caller deadline must not be cached")
}

// TestCachedSourceCanceledLeaderDoesNotStrandWaiter pins the coalescing
// outcome: a live waiter behind a canceled leader gets the leader error (or a
// real result if it re-enters), never a false empty success.
func TestCachedSourceCanceledLeaderDoesNotStrandWaiter(t *testing.T) {
	t.Parallel()
	key := Key{Harness: "claude-code", Session: "s1"}
	inner := &countingSource{
		projects: map[Key]Project{key: {Name: "org/repo", Hash: validHash('a')}},
		status:   Status{Enabled: true, Source: SourceAPI, State: StateOK},
		started:  make(chan struct{}, 2),
		release:  make(chan struct{}),
	}
	source := newCached(inner, time.Minute, time.Now)

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := source.Lookup(leaderCtx, []Key{key})
		leaderDone <- err
	}()
	select {
	case <-inner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the leader never reached the underlying source")
	}

	type waiterResult struct {
		projects map[Key]Project
		err      error
	}
	waiterDone := make(chan waiterResult, 1)
	go func() {
		projects, err := source.Lookup(context.Background(), []Key{key})
		waiterDone <- waiterResult{projects: projects, err: err}
	}()
	require.Eventually(t, func() bool {
		source.mu.Lock()
		defer source.mu.Unlock()
		for _, pending := range source.pending {
			if pending.waiters == 1 {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond, "the waiter never joined the leader's pending group")

	cancelLeader()
	require.ErrorIs(t, <-leaderDone, context.Canceled)
	close(inner.release)

	result := <-waiterDone
	require.True(t, result.err != nil || len(result.projects) > 0,
		"a live waiter must not receive a false empty success, got projects=%v err=%v", result.projects, result.err)
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
