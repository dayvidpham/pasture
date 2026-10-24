package sessionapi_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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

// apiProjectClock advances one second per observation so interval order is
// deterministic across the API fixture.
type apiProjectClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *apiProjectClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.at
	c.at = c.at.Add(time.Second)
	return current
}

type projectAPIFixture struct {
	server   *httptest.Server
	db       *sql.DB
	store    *tasks.ProjectAPIStore
	epochOne string
	epochTwo string
}

func newProjectAPIFixture(t *testing.T) *projectAPIFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tracker.Close()) })

	epochOne := createProjectEpoch(t, tracker, "Epoch one")
	epochTwo := createProjectEpoch(t, tracker, "Epoch two")

	db, err := dbconn.OpenSharedDBWithProfile(path, timeouts.TestProfile())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	store := tasks.NewProjectAPIStoreWithDB(db, timeouts.TestProfile(), &apiProjectClock{at: time.Unix(1700000000, 0).UTC()})
	server := httptest.NewServer(sessionapi.NewHandlerWithProjectStore(nil, nil, store))
	t.Cleanup(server.Close)
	return &projectAPIFixture{server: server, db: db, store: store, epochOne: epochOne, epochTwo: epochTwo}
}

func createProjectEpoch(t *testing.T, tracker protocol.TaskTracker, title string) string {
	t.Helper()
	epoch, err := tracker.Create("file://project-api", title, "epoch", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	return epoch.ID.String()
}

func projectAPIHash(seed string) string { return strings.Repeat(seed, 64/len(seed)) }

func postProjectAPI(t *testing.T, url, body string) *http.Response {
	t.Helper()
	response, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func getProjectAPI(t *testing.T, url string) *http.Response {
	t.Helper()
	response, err := http.Get(url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func decodeProjectView(t *testing.T, response *http.Response) sessionapi.ProjectView {
	t.Helper()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var view sessionapi.ProjectView
	require.NoError(t, json.NewDecoder(response.Body).Decode(&view))
	return view
}

func decodeProjectPage(t *testing.T, response *http.Response) sessionapi.ProjectPage {
	t.Helper()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var page sessionapi.ProjectPage
	require.NoError(t, json.NewDecoder(response.Body).Decode(&page))
	return page
}

func decodeProjectError(t *testing.T, response *http.Response) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
	return envelope.Error.Code
}

func TestProjectAPIListEmptyAndUnavailable(t *testing.T) {
	t.Parallel()
	fixture := newProjectAPIFixture(t)

	page := decodeProjectPage(t, getProjectAPI(t, fixture.server.URL+"/api/v1/projects"))
	require.NotNil(t, page.Items, "an empty project list is an array, not null")
	require.Empty(t, page.Items)

	// A handler without a project store still serves the session API, but the
	// project routes say so instead of silently answering an empty list.
	noStore := httptest.NewServer(sessionapi.NewHandler(nil))
	t.Cleanup(noStore.Close)
	response := getProjectAPI(t, noStore.URL+"/api/v1/projects")
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	require.Equal(t, "project_unavailable", decodeProjectError(t, response))
}

func TestProjectAPIBindShowRebindUnbind(t *testing.T) {
	t.Parallel()
	fixture := newProjectAPIFixture(t)
	hash := projectAPIHash("a")
	base := fixture.server.URL + "/api/v1/projects/" + hash

	first := decodeProjectView(t, postProjectAPI(t, base+"/bind", `{"epochId":`+quoteJSON(fixture.epochOne)+`}`))
	require.Equal(t, hash, first.ProjectHash)
	require.NotNil(t, first.Binding)
	require.Equal(t, "worker", first.Binding.Role)
	require.Equal(t, sessionapi.ProjectBindingSourceProject, first.Binding.Source)
	require.Equal(t, sessionapi.ConfidenceVerified, first.Binding.Confidence)
	require.NotNil(t, first.Epoch)
	require.Equal(t, fixture.epochOne, first.Epoch.ID)
	require.Equal(t, "Epoch one", first.Epoch.Title)
	require.Len(t, first.Intervals, 1)
	require.Nil(t, first.Intervals[0].UnboundAt)

	list := decodeProjectPage(t, getProjectAPI(t, fixture.server.URL+"/api/v1/projects"))
	require.Len(t, list.Items, 1)
	require.Equal(t, hash, list.Items[0].ProjectHash)
	require.NotNil(t, list.Items[0].Binding)
	require.Empty(t, list.Items[0].Intervals, "list rows omit interval history")

	detail := decodeProjectView(t, getProjectAPI(t, base))
	require.Len(t, detail.Intervals, 1)

	rebound := decodeProjectView(t, postProjectAPI(t, base+"/bind", `{"epochId":`+quoteJSON(fixture.epochTwo)+`,"role":"reviewer"}`))
	require.NotNil(t, rebound.Binding)
	require.Equal(t, fixture.epochTwo, rebound.Epoch.ID)
	require.Equal(t, "reviewer", rebound.Binding.Role)
	require.Len(t, rebound.Intervals, 2)
	require.NotNil(t, rebound.Intervals[0].UnboundAt)
	require.Equal(t, "rebind", rebound.Intervals[0].EndReason)
	require.Nil(t, rebound.Intervals[1].UnboundAt)

	stale := postProjectAPI(t, base+"/unbind", `{"expectedBinding":"stale-binding"}`)
	require.Equal(t, http.StatusConflict, stale.StatusCode)
	require.Equal(t, "binding_conflict", decodeProjectError(t, stale))

	ended := decodeProjectView(t, postProjectAPI(t, base+"/unbind", `{"expectedBinding":`+quoteJSON(rebound.Binding.BindingID)+`}`))
	require.Nil(t, ended.Binding, "unbind closes the active mapping")
	require.Len(t, ended.Intervals, 2)
	require.NotNil(t, ended.Intervals[1].UnboundAt)
	require.Equal(t, "unbind", ended.Intervals[1].EndReason)

	require.Empty(t, decodeProjectPage(t, getProjectAPI(t, fixture.server.URL+"/api/v1/projects")).Items)
	history := decodeProjectView(t, getProjectAPI(t, base))
	require.Nil(t, history.Binding)
	require.Len(t, history.Intervals, 2)

	noOpen := postProjectAPI(t, base+"/unbind", `{}`)
	require.Equal(t, http.StatusConflict, noOpen.StatusCode)
	require.Equal(t, "no_open_binding", decodeProjectError(t, noOpen))

	missing := getProjectAPI(t, fixture.server.URL+"/api/v1/projects/"+projectAPIHash("b"))
	require.Equal(t, http.StatusNotFound, missing.StatusCode)
	require.Equal(t, "project_not_found", decodeProjectError(t, missing))

	// A project mapping is not a session binding: the separate session table
	// stays untouched.
	var sessionBindings int
	require.NoError(t, fixture.db.QueryRow(`SELECT COUNT(*) FROM pasture_session_epoch_binding`).Scan(&sessionBindings))
	require.Zero(t, sessionBindings)
}

func TestProjectAPIRejectsBadHashAndRequest(t *testing.T) {
	t.Parallel()
	fixture := newProjectAPIFixture(t)
	base := fixture.server.URL + "/api/v1/projects/"

	for _, hash := range []string{"not-a-hash", strings.Repeat("A", 64), "abc"} {
		response := getProjectAPI(t, base+hash)
		require.Equal(t, http.StatusBadRequest, response.StatusCode, "hash %q", hash)
		require.Equal(t, "invalid_project_hash", decodeProjectError(t, response))
	}

	hash := projectAPIHash("c")
	unknown := postProjectAPI(t, base+hash+"/bind", `{"epochId":"file://missing"}`)
	require.Equal(t, http.StatusNotFound, unknown.StatusCode)
	require.Equal(t, "unknown_epoch", decodeProjectError(t, unknown))

	missing := postProjectAPI(t, base+hash+"/bind", `{}`)
	require.Equal(t, http.StatusBadRequest, missing.StatusCode)
	require.Equal(t, "invalid_body", decodeProjectError(t, missing))

	badRole := postProjectAPI(t, base+hash+"/bind", `{"epochId":`+quoteJSON(fixture.epochOne)+`,"role":"implementer"}`)
	require.Equal(t, http.StatusBadRequest, badRole.StatusCode)
	require.Equal(t, "invalid_body", decodeProjectError(t, badRole))

	malformed := postProjectAPI(t, base+hash+"/bind", `{"epochId":`)
	require.Equal(t, http.StatusBadRequest, malformed.StatusCode)
	require.Equal(t, "invalid_body", decodeProjectError(t, malformed))

	var total int
	require.NoError(t, fixture.db.QueryRow(`SELECT COUNT(*) FROM pasture_project_epoch_binding`).Scan(&total))
	require.Zero(t, total, "refused requests must not write a mapping")
}

// quoteJSON renders a string as a JSON string literal for request bodies.
func quoteJSON(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
