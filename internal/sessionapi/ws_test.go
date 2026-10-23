package sessionapi_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/sessionapi"
)

type testWSFrame struct {
	Type     string          `json:"type"`
	Revision int64           `json:"revision"`
	AsOf     time.Time       `json:"asOf"`
	Topics   []string        `json:"topics"`
	Payload  json.RawMessage `json:"payload"`
	Error    *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func readWSFrame(t *testing.T, conn *websocket.Conn) testWSFrame {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, data, err := conn.ReadMessage()
	require.NoError(t, err)
	var frame testWSFrame
	require.NoError(t, json.Unmarshal(data, &frame))
	return frame
}

func readWSFrameOfType(t *testing.T, conn *websocket.Conn, want string) testWSFrame {
	t.Helper()
	for i := 0; i < 8; i++ {
		frame := readWSFrame(t, conn)
		if frame.Type == want {
			return frame
		}
	}
	t.Fatalf("no %q frame arrived", want)
	return testWSFrame{}
}

func TestWebSocketSnapshotsAndLatestFrame(t *testing.T) {
	t.Parallel()
	f := seedSessions(t)
	server := httptest.NewServer(sessionapi.NewHandler(f.service()))
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ready := readWSFrame(t, conn)
	require.Equal(t, "ready", ready.Type)
	require.Equal(t, []string{"sessions", "epochs"}, ready.Topics)

	require.NoError(t, conn.WriteJSON(map[string]string{"type": "subscribe", "topic": "sessions"}))
	sessions := readWSFrameOfType(t, conn, "sessions")
	require.Equal(t, int64(7), sessions.Revision)
	require.Equal(t, f.now, sessions.AsOf)
	var sessionPayload struct {
		Items []sessionapi.SessionView `json:"items"`
		Next  string                   `json:"next,omitempty"`
	}
	require.NoError(t, json.Unmarshal(sessions.Payload, &sessionPayload))
	require.Len(t, sessionPayload.Items, 4)

	require.NoError(t, conn.WriteJSON(map[string]string{"type": "subscribe", "topic": "epochs"}))
	epochs := readWSFrameOfType(t, conn, "epochs")
	var epochPayload struct {
		Items []sessionapi.EpochView `json:"items"`
	}
	require.NoError(t, json.Unmarshal(epochs.Payload, &epochPayload))
	require.Len(t, epochPayload.Items, 1)
	require.Equal(t, f.epoch, epochPayload.Items[0].ID)

	now := f.now.UnixNano()
	f.state(t, "new", now, now, "PreToolUse", 4)
	f.binding(t, "binding-new", "new", now, sql.NullInt64{}, "")
	updated := readWSFrameOfType(t, conn, "sessions")
	require.NoError(t, json.Unmarshal(updated.Payload, &sessionPayload))
	require.Len(t, sessionPayload.Items, 5)

	require.NoError(t, conn.WriteJSON(map[string]string{"type": "subscribe", "topic": "bogus"}))
	bad := readWSFrameOfType(t, conn, "error")
	require.NotNil(t, bad.Error)
	require.Equal(t, "invalid_topic", bad.Error.Code)
}

func TestWebSocketRejectsNonLoopbackOrigin(t *testing.T) {
	t.Parallel()
	f := seedSessions(t)
	server := httptest.NewServer(sessionapi.NewHandler(f.service()))
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws"
	header := http.Header{"Origin": []string{"https://example.com"}}
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, header)
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err)
	require.NotNil(t, response)
	require.Equal(t, http.StatusForbidden, response.StatusCode)
}

// TestWebSocketStaleCacheErrorIsDeduplicated pins two things at once: a dirty
// cache reaches the client as the documented stale_session_cache error frame,
// and the per-subscription error suppression means the one-second refresh does
// not repeat the same frame forever.
func TestWebSocketStaleCacheErrorIsDeduplicated(t *testing.T) {
	t.Parallel()
	f := seedSessions(t)
	_, err := f.db.Exec(`UPDATE pasture_session_api_state SET dirty = 1 WHERE singleton_id = 0`)
	require.NoError(t, err)
	server := httptest.NewServer(sessionapi.NewHandler(f.service()))
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.Equal(t, "ready", readWSFrame(t, conn).Type)
	require.NoError(t, conn.WriteJSON(map[string]string{"type": "subscribe", "topic": "sessions"}))
	first := readWSFrameOfType(t, conn, "error")
	require.NotNil(t, first.Error)
	require.Equal(t, "stale_session_cache", first.Error.Code)
	require.Contains(t, first.Error.Message, "pasture session list",
		"the remediation must name the command that rebuilds the cache")
	require.Contains(t, first.Error.Message, "pasture serve --backfill",
		"the remediation must name the rebuild flag; a plain restart does not repair it")

	// The refresh ticker fires once a second. Waiting past one tick must not
	// produce a duplicate frame because publishError suppresses the same
	// code+message per subscription.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(1600*time.Millisecond)))
	_, _, readErr := conn.ReadMessage()
	require.Error(t, readErr, "a duplicate error frame arrived")
	var netErr net.Error
	require.True(t, errors.As(readErr, &netErr) && netErr.Timeout(),
		"expected the read deadline to expire with no frame, got %v", readErr)
}

// TestWebSocketStorageFaultFrameIsDeduplicated pins the WS side of the shared
// HTTP mapping: a storage failure becomes one storage_fault frame, and the next
// refresh tick does not repeat it. The api-state table is dropped after the
// server exists, which is the same kind of fault the HTTP handler reports as
// 500 storage_fault.
func TestWebSocketStorageFaultFrameIsDeduplicated(t *testing.T) {
	t.Parallel()
	f := seedSessions(t)
	server := httptest.NewServer(sessionapi.NewHandler(f.service()))
	t.Cleanup(server.Close)
	_, err := f.db.Exec(`DROP TABLE pasture_session_api_state`)
	require.NoError(t, err)

	// The HTTP 500 body for the same broken store is the reference wording; the
	// WS frame must carry exactly it.
	response, err := http.Get(server.URL + "/api/v1/sessions")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusInternalServerError, response.StatusCode)
	var httpFault apiErrorEnvelope
	require.NoError(t, json.NewDecoder(response.Body).Decode(&httpFault))
	require.Equal(t, "storage_fault", httpFault.Error.Code)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.Equal(t, "ready", readWSFrame(t, conn).Type)
	require.NoError(t, conn.WriteJSON(map[string]string{"type": "subscribe", "topic": "sessions"}))
	first := readWSFrameOfType(t, conn, "error")
	require.NotNil(t, first.Error)
	require.Equal(t, "storage_fault", first.Error.Code)
	require.Equal(t, httpFault.Error.Message, first.Error.Message,
		"the WS storage_fault must carry the same wording the HTTP storage_fault carries")
	require.Contains(t, first.Error.Message, "could not be read")

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(1600*time.Millisecond)))
	_, _, readErr := conn.ReadMessage()
	require.Error(t, readErr, "a duplicate storage_fault frame arrived")
	var netErr net.Error
	require.True(t, errors.As(readErr, &netErr) && netErr.Timeout(),
		"expected the read deadline to expire with no frame, got %v", readErr)
}
