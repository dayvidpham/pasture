package sessionapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const validTranscriptSession = "6a46bcd8-b365-4608-be7d-5601e2fdd805"

// stubTranscriptSource lets handler tests assert each error mapping without
// starting a process. It returns the same validated-export contract the real
// source returns.
type stubTranscriptSource struct {
	payload []byte
	err     error
	closed  *bool
	export  *TranscriptExport
}

func (s *stubTranscriptSource) ExportTranscript(context.Context, string, string) (*TranscriptExport, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.export != nil {
		return s.export, nil
	}
	export := &TranscriptExport{
		Content: bytes.NewReader(s.payload),
		Size:    int64(len(s.payload)),
		ModTime: time.Unix(1700000000, 0).UTC(),
		CloseFunc: func() error {
			if s.closed != nil {
				*s.closed = true
			}
			return nil
		},
	}
	return export, nil
}

func newTranscriptServer(t *testing.T, source TranscriptSource) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(newHandlerWithProject(nil, nil, nil, source))
	t.Cleanup(server.Close)
	return server
}

func TestSessionTranscriptHandler(t *testing.T) {
	t.Parallel()
	validPayload := []byte(`{"id":"` + validTranscriptSession + `","harness":"claude-code","turns":[]}`)

	cases := []struct {
		name       string
		source     TranscriptSource
		wantStatus int
		wantCode   string
	}{
		{
			name:       "success",
			source:     &stubTranscriptSource{payload: validPayload},
			wantStatus: http.StatusOK,
		},
		{
			name:       "no source",
			source:     nil,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "transcript_unavailable",
		},
		{
			name:       "peasant unavailable",
			source:     &stubTranscriptSource{err: fmt.Errorf("%w: not found", ErrTranscriptSourceUnavailable)},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "peasant_unavailable",
		},
		{
			name:       "no transcript",
			source:     &stubTranscriptSource{err: fmt.Errorf("%w: none", ErrTranscriptNotFound)},
			wantStatus: http.StatusNotFound,
			wantCode:   "transcript_not_found",
		},
		{
			name:       "incomplete attempt",
			source:     &stubTranscriptSource{err: fmt.Errorf("%w: newer", ErrTranscriptIncomplete)},
			wantStatus: http.StatusConflict,
			wantCode:   "transcript_incomplete",
		},
		{
			name:       "unsupported session",
			source:     &stubTranscriptSource{err: fmt.Errorf("%w: no harness", ErrTranscriptUnsupported)},
			wantStatus: http.StatusConflict,
			wantCode:   "transcript_unsupported",
		},
		{
			name:       "identity mismatch",
			source:     &stubTranscriptSource{err: fmt.Errorf("%w: other session", ErrTranscriptIdentityMismatch)},
			wantStatus: http.StatusConflict,
			wantCode:   "transcript_identity_mismatch",
		},
		{
			name:       "bounded wait ended",
			source:     &stubTranscriptSource{err: fmt.Errorf("%w: deadline", ErrTranscriptTimedOut)},
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   "transcript_timeout",
		},
		{
			name:       "oversized document",
			source:     &stubTranscriptSource{err: fmt.Errorf("%w: too big", ErrTranscriptTooLarge)},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "transcript_too_large",
		},
		{
			name:       "malformed document",
			source:     &stubTranscriptSource{err: fmt.Errorf("%w: bad json", ErrTranscriptMalformed)},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "malformed_transcript",
		},
		{
			name:       "unclassified peasant failure",
			source:     &stubTranscriptSource{err: fmt.Errorf("%w: boom", ErrTranscriptExportFailed)},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "peasant_export_failed",
		},
		{
			name:       "empty payload",
			source:     &stubTranscriptSource{payload: nil},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "malformed_transcript",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := newTranscriptServer(t, tc.source)

			response, err := http.Get(server.URL + "/api/v1/sessions/claude-code/" + validTranscriptSession + "/transcript")
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, tc.wantStatus, response.StatusCode)

			if tc.wantStatus == http.StatusOK {
				require.Equal(t, "application/json", response.Header.Get("Content-Type"))
				require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
				body, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				require.Equal(t, validPayload, body)
				return
			}

			var envelope errorEnvelope
			require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
			require.Equal(t, tc.wantCode, envelope.Error.Code)
			require.NotEmpty(t, envelope.Error.Message, "an actionable message is required")
		})
	}
}

func TestSessionTranscriptHandlerClosesTheExport(t *testing.T) {
	t.Parallel()
	closed := false
	source := &stubTranscriptSource{
		payload: []byte(`{"id":"` + validTranscriptSession + `","harness":"claude-code","turns":[]}`),
		closed:  &closed,
	}
	server := newTranscriptServer(t, source)

	response, err := http.Get(server.URL + "/api/v1/sessions/claude-code/" + validTranscriptSession + "/transcript")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.True(t, closed, "the handler must close the streamed export")
}

// TestSessionTranscriptHandlerClosesAnUnusableExport pins the cleanup order:
// once ExportTranscript returns a non-nil export, the handler owns its file and
// private directory even when the export is unusable and the request ends in an
// error before any bytes are streamed.
func TestSessionTranscriptHandlerClosesAnUnusableExport(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		export *TranscriptExport
	}{
		{name: "nil content", export: &TranscriptExport{Size: 10}},
		{name: "zero size", export: &TranscriptExport{Content: bytes.NewReader([]byte("{}")), Size: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			closed := false
			tc.export.CloseFunc = func() error {
				closed = true
				return nil
			}
			server := newTranscriptServer(t, &stubTranscriptSource{export: tc.export})

			response, err := http.Get(server.URL + "/api/v1/sessions/claude-code/" + validTranscriptSession + "/transcript")
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, http.StatusInternalServerError, response.StatusCode)

			var envelope errorEnvelope
			require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
			require.Equal(t, "malformed_transcript", envelope.Error.Code)
			require.True(t, closed, "the handler must close an export it refuses")
		})
	}
}

func TestSessionTranscriptHandlerRejectsAnInvalidSessionKey(t *testing.T) {
	t.Parallel()
	server := newTranscriptServer(t, &stubTranscriptSource{})

	response, err := http.Get(server.URL + "/api/v1/sessions/claude-code/a%2Fb/transcript")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusBadRequest, response.StatusCode)

	var envelope errorEnvelope
	require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
	require.Equal(t, "invalid_session_key", envelope.Error.Code)
}

// TestSessionTranscriptHandlerValidatesTheSharedIdentity pins the shared
// schema contract at the route boundary: only a harness and native session id
// the schema package can express reach Peasant.
func TestSessionTranscriptHandlerValidatesTheSharedIdentity(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"id":"` + validTranscriptSession + `","harness":"claude-code","turns":[]}`)

	accepted := []struct {
		name    string
		harness string
		session string
	}{
		{name: "uuid claude session", harness: "claude-code", session: validTranscriptSession},
		{name: "claude subagent", harness: "claude-code", session: "agent-a083060"},
		{name: "opencode session", harness: "opencode", session: "ses_3cd91f52effeXd3QAJ54jOyzv5"},
		{name: "acp session", harness: "opencode", session: "sess_3cd91f52effeXd3QAJ54jOyzv5"},
		{name: "opencode message", harness: "opencode", session: "msg_001abc"},
		{name: "strike bare id", harness: "strike", session: "ABCDEFGHIJKLMNOPQRST234567"},
		{name: "strike timestamped id", harness: "strike", session: "20260728T123456.123456789Z-ABCDEFGHIJKLMNOPQRST234567"},
		{name: "codex uuid", harness: "codex", session: validTranscriptSession},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := newTranscriptServer(t, &stubTranscriptSource{payload: payload})
			response, err := http.Get(server.URL + "/api/v1/sessions/" + tc.harness + "/" + tc.session + "/transcript")
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, http.StatusOK, response.StatusCode)
		})
	}

	rejected := []struct {
		name    string
		harness string
		session string
		code    string
	}{
		{name: "free-form session", harness: "claude-code", session: "session-1", code: "invalid_native_session_id"},
		{name: "unknown session form", harness: "claude-code", session: "not-a-session", code: "invalid_native_session_id"},
		{name: "lowercase base32", harness: "claude-code", session: "abcdefghijklmnopqrstuvwxyz", code: "invalid_native_session_id"},
		{name: "legacy claude harness", harness: "claude", session: validTranscriptSession, code: "invalid_harness"},
		{name: "legacy gemini harness", harness: "gemini", session: validTranscriptSession, code: "invalid_harness"},
		{name: "unknown harness", harness: "not-a-harness", session: validTranscriptSession, code: "invalid_harness"},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := newTranscriptServer(t, &stubTranscriptSource{payload: payload})
			response, err := http.Get(server.URL + "/api/v1/sessions/" + tc.harness + "/" + tc.session + "/transcript")
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, http.StatusBadRequest, response.StatusCode)

			var envelope errorEnvelope
			require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
			require.Equal(t, tc.code, envelope.Error.Code)
			require.NotEmpty(t, envelope.Error.Message)
			if tc.code == "invalid_harness" {
				require.Contains(t, envelope.Error.Message, "claude-code",
					"the refusal must name the canonical harness vocabulary")
			}
		})
	}
}

// TestNewHandlerGuardsAndWiresTheTranscriptRoute exercises the production
// constructor. The non-loopback requests prove the route lives inside the
// guarded mux; the loopback request proves NewHandler wires the real Peasant
// source (an empty PATH must make it report peasant_unavailable, not 404).
//
// This test mutates PATH, so it must stay serial.
func TestNewHandlerGuardsAndWiresTheTranscriptRoute(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	server := httptest.NewServer(NewHandler(nil))
	t.Cleanup(server.Close)
	url := server.URL + "/api/v1/sessions/claude-code/" + validTranscriptSession + "/transcript"

	loopback, err := http.Get(url)
	require.NoError(t, err)
	defer loopback.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, loopback.StatusCode)
	var loopbackEnvelope errorEnvelope
	require.NoError(t, json.NewDecoder(loopback.Body).Decode(&loopbackEnvelope))
	require.Equal(t, "peasant_unavailable", loopbackEnvelope.Error.Code,
		"a loopback request must reach the transcript route and its production source")

	hostRequest, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	hostRequest.Host = "evil.example"
	hostResponse, err := http.DefaultClient.Do(hostRequest)
	require.NoError(t, err)
	defer hostResponse.Body.Close()
	require.Equal(t, http.StatusForbidden, hostResponse.StatusCode)
	var hostEnvelope errorEnvelope
	require.NoError(t, json.NewDecoder(hostResponse.Body).Decode(&hostEnvelope))
	require.Equal(t, "forbidden_host", hostEnvelope.Error.Code)

	originRequest, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	originRequest.Header.Set("Origin", "https://evil.example")
	originResponse, err := http.DefaultClient.Do(originRequest)
	require.NoError(t, err)
	defer originResponse.Body.Close()
	require.Equal(t, http.StatusForbidden, originResponse.StatusCode)
	var originEnvelope errorEnvelope
	require.NoError(t, json.NewDecoder(originResponse.Body).Decode(&originEnvelope))
	require.Equal(t, "forbidden_origin", originEnvelope.Error.Code)
}
