package sessionapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/peasantproject"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

func TestTranscriptCatalogHandler(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		source     *stubTranscriptSource
		wantStatus int
		wantCode   string
		wantItems  []string
		truncated  bool
	}{
		{
			name:       "success",
			source:     &stubTranscriptSource{sessions: []string{"ses_a", "ses_b"}},
			wantStatus: http.StatusOK,
			wantItems:  []string{"ses_a", "ses_b"},
		},
		{
			name:       "empty",
			source:     &stubTranscriptSource{sessions: []string{}},
			wantStatus: http.StatusOK,
			wantItems:  []string{},
		},
		{
			name:       "nil items become an empty array",
			source:     &stubTranscriptSource{},
			wantStatus: http.StatusOK,
			wantItems:  []string{},
		},
		{
			name:       "truncated page",
			source:     &stubTranscriptSource{sessions: []string{"ses_a"}, truncated: true},
			wantStatus: http.StatusOK,
			wantItems:  []string{"ses_a"},
			truncated:  true,
		},
		{
			name:       "source error",
			source:     &stubTranscriptSource{catalogErr: fmt.Errorf("%w: not found", ErrTranscriptSourceUnavailable)},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "peasant_unavailable",
		},
		{
			name:       "malformed catalog",
			source:     &stubTranscriptSource{catalogErr: fmt.Errorf("%w: bad json", ErrTranscriptMalformed)},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "transcript_catalog_unavailable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := newTranscriptServer(t, tc.source)

			response, err := http.Get(server.URL + "/api/v1/transcripts")
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, tc.wantStatus, response.StatusCode)
			require.Equal(t, "application/json", response.Header.Get("Content-Type"))
			require.Equal(t, "no-store", response.Header.Get("Cache-Control"))

			if tc.wantStatus != http.StatusOK {
				var envelope errorEnvelope
				require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
				require.Equal(t, tc.wantCode, envelope.Error.Code)
				require.NotEmpty(t, envelope.Error.Message)
				require.Contains(t, envelope.Error.Message, "Peasant")
				return
			}

			var catalog TranscriptCatalog
			require.NoError(t, json.NewDecoder(response.Body).Decode(&catalog))
			require.Equal(t, tc.wantItems, catalog.Items)
			require.Equal(t, tc.truncated, catalog.Truncated)
			require.Equal(t, int64(0), catalog.Revision, "a handler with no session store has no certified revision")
			require.False(t, catalog.AsOf.IsZero())
		})
	}
}

type catalogProjectSource struct {
	ids   []string
	err   error
	calls int
}

func (s *catalogProjectSource) Lookup(context.Context, []peasantproject.Key) (map[peasantproject.Key]peasantproject.Project, error) {
	return map[peasantproject.Key]peasantproject.Project{}, nil
}

func (s *catalogProjectSource) Status() peasantproject.Status {
	return peasantproject.Status{Enabled: true, Source: peasantproject.SourceDB, State: peasantproject.StateOK}
}

func (s *catalogProjectSource) SessionIDs(context.Context) ([]string, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.ids, nil
}

type plainProjectSource struct{}

func (plainProjectSource) Lookup(context.Context, []peasantproject.Key) (map[peasantproject.Key]peasantproject.Project, error) {
	return map[peasantproject.Key]peasantproject.Project{}, nil
}

func (plainProjectSource) Status() peasantproject.Status {
	return peasantproject.Status{Enabled: true, Source: peasantproject.SourceAPI, State: peasantproject.StateOK}
}

func TestTranscriptCatalogPrefersTheDatabaseAndFallsBackToTheCLI(t *testing.T) {
	t.Parallel()

	t.Run("database ids win and are complete", func(t *testing.T) {
		t.Parallel()
		cli := &stubTranscriptSource{sessions: []string{"cli-only"}}
		db := &catalogProjectSource{ids: []string{"db-a", "db-b"}}
		service := &Service{ProjectSource: db}
		server := httptest.NewServer(newHandlerWithProject(service, nil, nil, cli))
		t.Cleanup(server.Close)

		response, err := http.Get(server.URL + "/api/v1/transcripts")
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode)
		var catalog TranscriptCatalog
		require.NoError(t, json.NewDecoder(response.Body).Decode(&catalog))
		require.Equal(t, []string{"db-a", "db-b"}, catalog.Items)
		require.False(t, catalog.Truncated)
		require.Equal(t, 1, db.calls)
		require.Equal(t, 0, cli.catalogCalls, "a database catalog must not also spawn the CLI list")
	})

	t.Run("a source that cannot list ids uses the CLI", func(t *testing.T) {
		t.Parallel()
		cli := &stubTranscriptSource{sessions: []string{"cli-only"}, truncated: true}
		service := &Service{ProjectSource: plainProjectSource{}}
		server := httptest.NewServer(newHandlerWithProject(service, nil, nil, cli))
		t.Cleanup(server.Close)

		response, err := http.Get(server.URL + "/api/v1/transcripts")
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode)
		var catalog TranscriptCatalog
		require.NoError(t, json.NewDecoder(response.Body).Decode(&catalog))
		require.Equal(t, []string{"cli-only"}, catalog.Items)
		require.True(t, catalog.Truncated)
		require.Equal(t, 1, cli.catalogCalls)
	})

	t.Run("an unavailable database catalog falls back to the CLI", func(t *testing.T) {
		t.Parallel()
		cli := &stubTranscriptSource{sessions: []string{"cli-fallback"}}
		db := &catalogProjectSource{err: fmt.Errorf("%w: enrichment is off", peasantproject.ErrSessionCatalogUnavailable)}
		service := &Service{ProjectSource: db}
		server := httptest.NewServer(newHandlerWithProject(service, nil, nil, cli))
		t.Cleanup(server.Close)

		response, err := http.Get(server.URL + "/api/v1/transcripts")
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode)
		var catalog TranscriptCatalog
		require.NoError(t, json.NewDecoder(response.Body).Decode(&catalog))
		require.Equal(t, []string{"cli-fallback"}, catalog.Items)
		require.Equal(t, 1, cli.catalogCalls)
	})

	t.Run("a database read failure is not hidden behind the CLI", func(t *testing.T) {
		t.Parallel()
		cli := &stubTranscriptSource{sessions: []string{"cli-only"}}
		db := &catalogProjectSource{err: fmt.Errorf("reading session ids from the Peasant database at %q: disk I/O error; retry", "/tmp/peasant.db")}
		service := &Service{ProjectSource: db}
		server := httptest.NewServer(newHandlerWithProject(service, nil, nil, cli))
		t.Cleanup(server.Close)

		response, err := http.Get(server.URL + "/api/v1/transcripts")
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
		var envelope errorEnvelope
		require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
		require.Equal(t, "transcript_catalog_unavailable", envelope.Error.Code)
		require.Contains(t, envelope.Error.Message, "Peasant database")
		require.Equal(t, 0, cli.catalogCalls)
	})
}

func TestTranscriptCatalogHandlerRejectsAMissingSource(t *testing.T) {
	t.Parallel()
	server := newTranscriptServer(t, nil)

	response, err := http.Get(server.URL + "/api/v1/transcripts")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)

	var envelope errorEnvelope
	require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
	require.Equal(t, "transcript_unavailable", envelope.Error.Code)
	require.Contains(t, envelope.Error.Message, "Peasant")
}

func TestTranscriptCatalogUsesTheSessionReadStamp(t *testing.T) {
	t.Parallel()
	db := openCatalogStateDB(t, 11, 0, audit.SessionCacheVersion)
	asOf := time.Unix(1700000000, 0).UTC()
	service := NewService(db)
	service.Now = func() time.Time { return asOf }
	source := &stubTranscriptSource{sessions: []string{"ses_fixture"}}
	server := httptest.NewServer(newHandlerWithProject(service, nil, nil, source))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/v1/transcripts")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var catalog TranscriptCatalog
	require.NoError(t, json.NewDecoder(response.Body).Decode(&catalog))
	require.Equal(t, int64(11), catalog.Revision)
	require.True(t, catalog.AsOf.Equal(asOf), "asOf %s, want %s", catalog.AsOf, asOf)
	require.Equal(t, []string{"ses_fixture"}, catalog.Items)
	require.False(t, catalog.Truncated)
}

func TestTranscriptCatalogRefusesAStaleCacheWithoutListing(t *testing.T) {
	t.Parallel()
	db := openCatalogStateDB(t, 11, 1, audit.SessionCacheVersion)
	service := NewService(db)
	source := &stubTranscriptSource{sessions: []string{"ses_fixture"}}
	server := httptest.NewServer(newHandlerWithProject(service, nil, nil, source))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/v1/transcripts")
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode, "body: %s", body)

	var envelope errorEnvelope
	require.NoError(t, json.Unmarshal(body, &envelope))
	require.Equal(t, "stale_session_cache", envelope.Error.Code)
	require.Equal(t, 0, source.catalogCalls, "a stale session cache must be refused before Peasant is asked for a catalog")
}

func openCatalogStateDB(t *testing.T, revision, dirty, cacheVersion int64) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	db, err := dbconn.OpenSharedDBWithProfile(path, timeouts.TestProfile())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE pasture_session_api_state (
		singleton_id INTEGER PRIMARY KEY,
		revision INTEGER NOT NULL,
		dirty INTEGER NOT NULL,
		cache_version INTEGER NOT NULL
	)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO pasture_session_api_state(singleton_id, revision, dirty, cache_version) VALUES (0, ?, ?, ?)`,
		revision, dirty, cacheVersion)
	require.NoError(t, err)
	return db
}
