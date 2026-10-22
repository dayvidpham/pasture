package sessionapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

type fixture struct {
	db    *sql.DB
	epoch string
	now   time.Time
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tracker.Close()) })
	db, err := dbconn.OpenSharedDBWithProfile(path, timeouts.TestProfile())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	epoch, err := tracker.Create("file://sessionapi-test", "Session API epoch", "epoch title", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	now := time.Unix(1700000000, 0).UTC()
	_, err = db.Exec(`
		INSERT INTO pasture_session_api_state(singleton_id, revision, dirty, completed_through_jid)
		VALUES (0, 7, 0, 0)
		ON CONFLICT(singleton_id) DO UPDATE SET revision=excluded.revision, dirty=excluded.dirty, completed_through_jid=excluded.completed_through_jid`)
	require.NoError(t, err)
	return fixture{db: db, epoch: epoch.ID.String(), now: now}
}

func (f fixture) state(t *testing.T, session string, first, last int64, lastEvent string, journal int64) {
	t.Helper()
	_, err := f.db.Exec(`
		INSERT INTO pasture_session_state(harness, session, first_seen, last_seen, last_event, last_journal_id, updated_at)
		VALUES ('claude-code', ?, ?, ?, ?, ?, ?)`,
		session, first, last, lastEvent, journal, last)
	require.NoError(t, err)
}

func (f fixture) binding(t *testing.T, id, session string, boundAt int64, unboundAt sql.NullInt64, reason string) {
	t.Helper()
	var reasonValue any
	if unboundAt.Valid {
		reasonValue = reason
	}
	_, err := f.db.Exec(`
		INSERT INTO pasture_session_epoch_binding(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason)
		VALUES (?, 'claude-code', ?, ?, 'implementer', ?, ?, ?)`,
		id, session, f.epoch, boundAt, unboundAt, reasonValue)
	require.NoError(t, err)
}

func (f fixture) claim(t *testing.T, session string, claimedAt int64) {
	t.Helper()
	_, err := f.db.Exec(`
		INSERT INTO pasture_session_claim(harness, session, actor, claimed_at)
		VALUES ('claude-code', ?, 'file://actor/session-api', ?)`, session, claimedAt)
	require.NoError(t, err)
}

func (f fixture) service() *sessionapi.Service {
	return &sessionapi.Service{DB: f.db, Now: func() time.Time { return f.now }, ActiveWindow: 15 * time.Minute}
}

func seedSessions(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t)
	now := f.now.UnixNano()
	f.state(t, "active", now-1*int64(time.Minute), now-1*int64(time.Minute), "PreToolUse", 1)
	f.binding(t, "binding-active", "active", now-1*int64(time.Minute), sql.NullInt64{}, "")
	f.state(t, "idle", now-20*int64(time.Minute), now-20*int64(time.Minute), "SessionStart", 2)
	f.binding(t, "binding-idle", "idle", now-20*int64(time.Minute), sql.NullInt64{}, "")
	f.state(t, "ended", now-30*int64(time.Minute), now-5*int64(time.Minute), "SessionEnd", 3)
	f.binding(t, "binding-ended", "ended", now-30*int64(time.Minute), sql.NullInt64{Int64: now - 5*int64(time.Minute), Valid: true}, "session_end")
	f.claim(t, "unknown", now-2*int64(time.Minute))
	return f
}

func TestListStatesOrderingAndPaging(t *testing.T) {
	t.Parallel()
	f := seedSessions(t)
	service := f.service()
	ctx := context.Background()

	page, err := service.List(ctx, sessionapi.ListOptions{Limit: 10})
	require.NoError(t, err)
	require.Equal(t, int64(7), page.Revision)
	require.Equal(t, f.now, page.AsOf)
	require.Len(t, page.Items, 4)
	require.Equal(t, []string{"ended", "idle", "active", "unknown"}, []string{
		page.Items[0].NativeSessionID, page.Items[1].NativeSessionID, page.Items[2].NativeSessionID, page.Items[3].NativeSessionID,
	})
	require.Equal(t, sessionapi.StateEnded, page.Items[0].Activity.State)
	require.NotNil(t, page.Items[0].End)
	require.Equal(t, "session_end", page.Items[0].End.EndReason)
	require.Equal(t, sessionapi.StateIdle, page.Items[1].Activity.State)
	require.Equal(t, sessionapi.StateActive, page.Items[2].Activity.State)
	require.Equal(t, sessionapi.StateUnknown, page.Items[3].Activity.State)
	require.NotNil(t, page.Items[2].Epoch)
	require.Equal(t, "Session API epoch", page.Items[2].Epoch.Title)

	first, err := service.List(ctx, sessionapi.ListOptions{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Items, 2)
	require.NotEmpty(t, first.Next)
	second, err := service.List(ctx, sessionapi.ListOptions{Limit: 2, Cursor: first.Next})
	require.NoError(t, err)
	require.Len(t, second.Items, 2)
	require.Empty(t, second.Next)
	require.Equal(t, []string{"active", "unknown"}, []string{second.Items[0].NativeSessionID, second.Items[1].NativeSessionID})
}

func TestListFiltersAndErrors(t *testing.T) {
	t.Parallel()
	f := seedSessions(t)
	service := f.service()
	ctx := context.Background()

	active, err := service.List(ctx, sessionapi.ListOptions{State: sessionapi.StateActive})
	require.NoError(t, err)
	require.Len(t, active.Items, 1)
	require.Equal(t, "active", active.Items[0].NativeSessionID)

	byEpoch, err := service.List(ctx, sessionapi.ListOptions{Epoch: f.epoch})
	require.NoError(t, err)
	require.Len(t, byEpoch.Items, 3, "claim-only sessions have no epoch and are excluded by the filter")

	_, err = service.List(ctx, sessionapi.ListOptions{State: "bogus"})
	require.True(t, errors.Is(err, sessionapi.ErrInvalidFilter))
	_, err = service.List(ctx, sessionapi.ListOptions{Limit: 101})
	require.True(t, errors.Is(err, sessionapi.ErrInvalidFilter))
	_, err = service.List(ctx, sessionapi.ListOptions{Cursor: "not-base64"})
	require.True(t, errors.Is(err, sessionapi.ErrInvalidPageToken))

	_, err = f.db.Exec(`UPDATE pasture_session_api_state SET dirty = 1 WHERE singleton_id = 0`)
	require.NoError(t, err)
	_, err = service.List(ctx, sessionapi.ListOptions{})
	require.True(t, errors.Is(err, sessionapi.ErrStaleCache))
}

func TestGetAndNotFound(t *testing.T) {
	t.Parallel()
	f := seedSessions(t)
	service := f.service()
	detail, err := service.Get(context.Background(), "claude-code", "active")
	require.NoError(t, err)
	require.Equal(t, int64(7), detail.Revision)
	require.Equal(t, "active", detail.Session.NativeSessionID)
	require.Equal(t, sessionapi.StateActive, detail.Session.Activity.State)
	_, err = service.Get(context.Background(), "claude-code", "missing")
	require.True(t, errors.Is(err, sessionapi.ErrNotFound))
}

func TestHTTPHandlerLoopbackAndErrors(t *testing.T) {
	t.Parallel()
	f := seedSessions(t)
	server := httptest.NewServer(sessionapi.NewHandler(f.service()))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/v1/sessions?limit=1")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var page sessionapi.Page
	require.NoError(t, json.NewDecoder(response.Body).Decode(&page))
	require.Len(t, page.Items, 1)
	require.NotEmpty(t, page.Next)

	response, err = http.Get(server.URL + "/api/v1/sessions/claude-code/active")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	response, err = http.Get(server.URL + "/api/v1/sessions/claude-code/missing")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusNotFound, response.StatusCode)

	response, err = http.Get(server.URL + "/openapi.json")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/sessions", nil)
	require.NoError(t, err)
	request.Host = "example.com"
	response, err = http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusForbidden, response.StatusCode)
}
