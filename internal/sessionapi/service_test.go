package sessionapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

type fixture struct {
	db      *sql.DB
	tracker protocol.TaskTracker
	epoch   string
	now     time.Time
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
	now := time.Unix(1700000000, 0).UTC()
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS epoch_state_projection (
			epoch_id      TEXT PRIMARY KEY,
			current_phase TEXT NOT NULL,
			state_json    TEXT NOT NULL,
			updated_at    INTEGER NOT NULL
		)`)
	require.NoError(t, err)
	f := fixture{db: db, tracker: tracker, now: now}
	f.epoch = f.addEpoch(t, "Session API epoch")
	_, err = db.Exec(`
		INSERT INTO pasture_session_api_state(singleton_id, revision, dirty, completed_through_jid)
		VALUES (0, 7, 0, 0)
		ON CONFLICT(singleton_id) DO UPDATE SET revision=excluded.revision, dirty=excluded.dirty, completed_through_jid=excluded.completed_through_jid`)
	require.NoError(t, err)
	return f
}

// addEpoch creates a second epoch with a projection row, so interval tests can
// move a session across epochs without hand-writing task rows.
func (f fixture) addEpoch(t *testing.T, title string) string {
	t.Helper()
	epoch, err := f.tracker.Create("file://sessionapi-test", title, "epoch title", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	state := protocol.EpochState{EpochId: epoch.ID.String(), CurrentPhase: protocol.PhaseId("p3-plan"), CurrentRole: protocol.RoleId("architect")}
	stateJSON, err := json.Marshal(state)
	require.NoError(t, err)
	_, err = f.db.Exec(`
		INSERT INTO epoch_state_projection(epoch_id, current_phase, state_json, updated_at)
		VALUES (?, ?, ?, ?)`, epoch.ID.String(), string(state.CurrentPhase), string(stateJSON), f.now.UnixNano())
	require.NoError(t, err)
	return epoch.ID.String()
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
	f.bindingEpoch(t, id, session, f.epoch, boundAt, unboundAt, reason)
}

func (f fixture) bindingEpoch(t *testing.T, id, session, epochID string, boundAt int64, unboundAt sql.NullInt64, reason string) {
	t.Helper()
	var reasonValue any
	if unboundAt.Valid {
		reasonValue = reason
	}
	_, err := f.db.Exec(`
		INSERT INTO pasture_session_epoch_binding(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason)
		VALUES (?, 'claude-code', ?, ?, 'implementer', ?, ?, ?)`,
		id, session, epochID, boundAt, unboundAt, reasonValue)
	require.NoError(t, err)
}

// inferredTable creates the inference table S3 owns. The read path must work
// with or without it.
func (f fixture) inferredTable(t *testing.T) {
	t.Helper()
	_, err := f.db.Exec(`
		CREATE TABLE pasture_session_inferred_binding (
			binding_id    TEXT PRIMARY KEY,
			harness       TEXT NOT NULL,
			session       TEXT NOT NULL,
			epoch_id      TEXT NOT NULL,
			role          TEXT NOT NULL,
			bound_at      INTEGER NOT NULL,
			unbound_at    INTEGER,
			unbound_reason TEXT,
			inferred_at   INTEGER NOT NULL,
			reason        TEXT NOT NULL
		)`)
	require.NoError(t, err)
}

func (f fixture) inferredBinding(t *testing.T, id, session, epochID string, boundAt int64, unboundAt sql.NullInt64, reason string) {
	t.Helper()
	var reasonValue any
	if unboundAt.Valid {
		reasonValue = reason
	}
	_, err := f.db.Exec(`
		INSERT INTO pasture_session_inferred_binding(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason, inferred_at, reason)
		VALUES (?, 'claude-code', ?, ?, 'implementer', ?, ?, ?, ?, 'claim ownership')`,
		id, session, epochID, boundAt, unboundAt, reasonValue, f.now.UnixNano())
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
	f.binding(t, "binding-active-old", "active", now-40*int64(time.Minute), sql.NullInt64{Int64: now - 25*int64(time.Minute), Valid: true}, "session_end")
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
	require.Len(t, page.Items[2].Bindings, 2, "historical intervals are nested, not extra page rows")
	require.Equal(t, []string{"binding-active-old", "binding-active"}, []string{
		page.Items[2].Bindings[0].BindingID, page.Items[2].Bindings[1].BindingID,
	})
	require.Equal(t, sessionapi.BindingSourceExplicit, page.Items[2].Bindings[0].Source)
	require.NotNil(t, page.Items[2].Bindings[0].UnboundAt)
	require.Equal(t, "session_end", page.Items[2].Bindings[0].EndReason)
	require.Nil(t, page.Items[2].Bindings[1].UnboundAt)
	require.Empty(t, page.Items[2].Bindings[1].EndReason)
	require.Equal(t, page.Items[2].Binding.BindingID, page.Items[2].Bindings[1].BindingID)
	require.Empty(t, page.Items[3].Bindings, "a claim-only session has no binding interval")
	require.NotNil(t, page.Items[3].Bindings)

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

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func TestHTTPBindAndUnbind(t *testing.T) {
	t.Parallel()
	f := seedSessions(t)
	clock := fixedClock{now: f.now}
	store := tasks.NewSessionAPIStoreWithDB(f.db, timeouts.TestProfile(), clock)
	server := httptest.NewServer(sessionapi.NewHandler(f.service(), store))
	t.Cleanup(server.Close)

	bind := func(session, body string) *http.Response {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/sessions/claude-code/"+session+"/bind", strings.NewReader(body))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		return response
	}
	response := bind("new-session", `{"epochId":"`+f.epoch+`","role":"worker"}`)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var detail sessionapi.Detail
	require.NoError(t, json.NewDecoder(response.Body).Decode(&detail))
	require.NotNil(t, detail.Session.Binding)
	require.Equal(t, sessionapi.StateActive, detail.Session.Activity.State)
	require.Nil(t, detail.Session.End)

	response = bind("new-session", `{"epochId":"`+f.epoch+`","role":"worker","expectedBinding":"missing"}`)
	defer response.Body.Close()
	require.Equal(t, http.StatusConflict, response.StatusCode)

	response = bind("new-session", `{"epochId":"file://missing","role":"worker"}`)
	defer response.Body.Close()
	require.Equal(t, http.StatusNotFound, response.StatusCode)

	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/sessions/claude-code/new-session/unbind", strings.NewReader(`{}`))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.NoError(t, json.NewDecoder(response.Body).Decode(&detail))
	require.NotNil(t, detail.Session.End)
	require.Equal(t, "unbind", detail.Session.End.EndReason)
	require.Equal(t, sessionapi.StateEnded, detail.Session.Activity.State)
}

func TestEpochEndpointsAndInspect(t *testing.T) {
	t.Parallel()
	f := seedSessions(t)
	server := httptest.NewServer(sessionapi.NewHandler(f.service()))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/v1/epochs")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var page sessionapi.EpochPage
	require.NoError(t, json.NewDecoder(response.Body).Decode(&page))
	require.Len(t, page.Items, 1)
	require.Equal(t, f.epoch, page.Items[0].ID)
	require.Equal(t, "Session API epoch", page.Items[0].Title)
	require.Equal(t, "p3-plan", page.Items[0].Phase)
	require.Equal(t, "architect", page.Items[0].Role)
	require.Equal(t, 3, page.Items[0].SessionCount)
	require.Equal(t, 1, page.Items[0].ActiveCount)

	detailURL := server.URL + "/api/v1/epochs/" + url.PathEscape(f.epoch)
	response, err = http.Get(detailURL)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var detail sessionapi.EpochDetail
	require.NoError(t, json.NewDecoder(response.Body).Decode(&detail))
	require.Equal(t, f.epoch, detail.Epoch.ID)

	response, err = http.Get(detailURL + "/sessions")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var sessions sessionapi.Page
	require.NoError(t, json.NewDecoder(response.Body).Decode(&sessions))
	require.Len(t, sessions.Items, 3)

	response, err = http.Get(server.URL + "/api/v1/inspect")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var inspect sessionapi.InspectPayload
	require.NoError(t, json.NewDecoder(response.Body).Decode(&inspect))
	require.Len(t, inspect.Epochs, 1)
	require.Len(t, inspect.Sessions, 4)
	require.False(t, inspect.Truncated)

	response, err = http.Get(server.URL + "/api/v1/epochs/" + url.PathEscape("file://missing"))
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusNotFound, response.StatusCode)
}

func TestBindingsChronologicalAcrossEpochs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	now := f.now.UnixNano()
	secondEpoch := f.addEpoch(t, "Second epoch")
	f.state(t, "rebound", now-40*int64(time.Minute), now-2*int64(time.Minute), "PreToolUse", 1)
	f.bindingEpoch(t, "binding-a", "rebound", f.epoch, now-40*int64(time.Minute), sql.NullInt64{Int64: now - 25*int64(time.Minute), Valid: true}, "unbind")
	f.bindingEpoch(t, "binding-b", "rebound", secondEpoch, now-10*int64(time.Minute), sql.NullInt64{}, "")

	detail, err := f.service().Get(context.Background(), "claude-code", "rebound")
	require.NoError(t, err)
	require.NotNil(t, detail.Session.Binding)
	require.Equal(t, "binding-b", detail.Session.Binding.BindingID, "the latest binding is the current one")
	require.NotNil(t, detail.Session.Epoch)
	require.Equal(t, secondEpoch, detail.Session.Epoch.ID)
	require.Equal(t, "Second epoch", detail.Session.Epoch.Title)
	require.Len(t, detail.Session.Bindings, 2)

	first, second := detail.Session.Bindings[0], detail.Session.Bindings[1]
	require.Equal(t, "binding-a", first.BindingID)
	require.Equal(t, sessionapi.BindingSourceExplicit, first.Source)
	require.NotNil(t, first.Epoch)
	require.Equal(t, f.epoch, first.Epoch.ID)
	require.Equal(t, time.Unix(0, now-40*int64(time.Minute)).UTC(), first.BoundAt)
	require.NotNil(t, first.UnboundAt)
	require.Equal(t, time.Unix(0, now-25*int64(time.Minute)).UTC(), *first.UnboundAt)
	require.Equal(t, "unbind", first.EndReason)

	require.Equal(t, "binding-b", second.BindingID)
	require.Equal(t, time.Unix(0, now-10*int64(time.Minute)).UTC(), second.BoundAt)
	require.Nil(t, second.UnboundAt, "the current binding stays open")
	require.Equal(t, sessionapi.StateActive, detail.Session.Activity.State)
}

func TestBindingsWithoutInferredTable(t *testing.T) {
	t.Parallel()
	f := seedSessions(t)
	detail, err := f.service().Get(context.Background(), "claude-code", "active")
	require.NoError(t, err, "an absent inferred table is not an error")
	require.Len(t, detail.Session.Bindings, 2)
	for _, interval := range detail.Session.Bindings {
		require.Equal(t, sessionapi.BindingSourceExplicit, interval.Source)
	}
}

func TestInferredBindingsMergeWithoutOverridingExplicit(t *testing.T) {
	t.Parallel()
	f := seedSessions(t)
	f.inferredTable(t)
	now := f.now.UnixNano()
	// The inferred open interval is newer than the explicit one, so it sorts
	// last; the current binding must still come from the explicit table.
	f.inferredBinding(t, "inferred-active", "active", f.epoch, now-int64(30*time.Second), sql.NullInt64{}, "")
	// An inferred-only session never becomes a page row.
	f.inferredBinding(t, "inferred-ghost", "ghost", f.epoch, now-int64(time.Minute), sql.NullInt64{}, "")

	page, err := f.service().List(context.Background(), sessionapi.ListOptions{Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 4, "inferred rows must not add page rows")
	for _, item := range page.Items {
		require.NotEqual(t, "ghost", item.NativeSessionID)
	}

	detail, err := f.service().Get(context.Background(), "claude-code", "active")
	require.NoError(t, err)
	require.NotNil(t, detail.Session.Binding)
	require.Equal(t, "binding-active", detail.Session.Binding.BindingID, "the explicit binding stays current")
	require.Len(t, detail.Session.Bindings, 3)
	last := detail.Session.Bindings[2]
	require.Equal(t, "inferred-active", last.BindingID)
	require.Equal(t, sessionapi.BindingSourceInferred, last.Source)
	require.Nil(t, last.UnboundAt)
	require.NotNil(t, last.Epoch)
	require.Equal(t, f.epoch, last.Epoch.ID)
	require.Equal(t, sessionapi.StateActive, detail.Session.Activity.State)
}

func TestBindingIntervalClampsInvertedEnd(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	now := f.now.UnixNano()
	f.binding(t, "binding-inverted", "inverted", now-10*int64(time.Minute), sql.NullInt64{Int64: now - 20*int64(time.Minute), Valid: true}, "unbind")

	detail, err := f.service().Get(context.Background(), "claude-code", "inverted")
	require.NoError(t, err)
	require.Len(t, detail.Session.Bindings, 1)
	interval := detail.Session.Bindings[0]
	require.NotNil(t, interval.UnboundAt)
	require.Equal(t, interval.BoundAt, *interval.UnboundAt, "an inverted end clamps to a zero-length interval")
	require.Equal(t, "unbind", interval.EndReason)
	require.NotNil(t, detail.Session.End)
	require.Equal(t, interval.BoundAt, detail.Session.End.EndedAt)
	require.Equal(t, sessionapi.StateEnded, detail.Session.Activity.State)
}

func TestPaginationCountsSessionsNotIntervals(t *testing.T) {
	t.Parallel()
	f := seedSessions(t)
	now := f.now.UnixNano()
	for i := 0; i < 5; i++ {
		boundAt := now - int64(60-i*5)*int64(time.Minute)
		f.binding(t, fmt.Sprintf("binding-history-%d", i), "active", boundAt, sql.NullInt64{Int64: boundAt + int64(time.Minute), Valid: true}, "unbind")
	}
	service := f.service()
	ctx := context.Background()

	first, err := service.List(ctx, sessionapi.ListOptions{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Items, 2)
	require.NotEmpty(t, first.Next)
	second, err := service.List(ctx, sessionapi.ListOptions{Limit: 2, Cursor: first.Next})
	require.NoError(t, err)
	require.Len(t, second.Items, 2)
	require.Empty(t, second.Next)
	require.Equal(t, []string{"active", "unknown"}, []string{second.Items[0].NativeSessionID, second.Items[1].NativeSessionID})
	require.Len(t, second.Items[0].Bindings, 7, "history stays nested under one session row")
}

func TestEpochCountsStayCurrentOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	now := f.now.UnixNano()
	historical := f.addEpoch(t, "Historical epoch")
	f.state(t, "moving", now-30*int64(time.Minute), now-1*int64(time.Minute), "PreToolUse", 1)
	f.bindingEpoch(t, "binding-old", "moving", historical, now-30*int64(time.Minute), sql.NullInt64{Int64: now - 20*int64(time.Minute), Valid: true}, "unbind")
	f.bindingEpoch(t, "binding-new", "moving", f.epoch, now-10*int64(time.Minute), sql.NullInt64{}, "")

	page, err := f.service().Epochs(context.Background())
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	counts := map[string]sessionapi.EpochView{}
	for _, epoch := range page.Items {
		counts[epoch.ID] = epoch
	}
	require.Equal(t, 1, counts[f.epoch].SessionCount)
	require.Equal(t, 1, counts[f.epoch].ActiveCount)
	require.Equal(t, 0, counts[historical].SessionCount, "an ended historical binding is not an ever-bound count")
}
