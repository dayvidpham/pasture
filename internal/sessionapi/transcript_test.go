package sessionapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/timeouts"
)

// fakePeasantSource is the helper program the source tests run instead of the
// real Peasant CLI. It is compiled once per test process from the Go source
// below and selected by session id, so no test mutates PATH or the environment.
var fakePeasant struct {
	once sync.Once
	dir  string
	path string
	err  error
}

func fakePeasantBinary(t *testing.T) string {
	t.Helper()
	fakePeasant.once.Do(func() {
		dir, err := os.MkdirTemp("", "pasture-fake-peasant-*")
		if err != nil {
			fakePeasant.err = err
			return
		}
		fakePeasant.dir = dir
		src := filepath.Join(dir, "main.go")
		if err := os.WriteFile(src, []byte(fakePeasantProgram), 0o600); err != nil {
			fakePeasant.err = err
			return
		}
		binary := filepath.Join(dir, "peasant")
		build := exec.Command("go", "build", "-o", binary, src)
		if out, err := build.CombinedOutput(); err != nil {
			fakePeasant.err = fmt.Errorf("build fake peasant: %v: %s", err, out)
			return
		}
		fakePeasant.path = binary
	})
	require.NoError(t, fakePeasant.err)
	return fakePeasant.path
}

// TestMain removes the compiled fake Peasant after the package's tests finish.
// It owns no other process state.
func TestMain(m *testing.M) {
	code := m.Run()
	if fakePeasant.dir != "" {
		_ = os.RemoveAll(fakePeasant.dir)
	}
	os.Exit(code)
}

const fakePeasantProgram = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func main() {
	args := os.Args[1:]
	var session, outDir string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session":
			if i+1 < len(args) {
				session = args[i+1]
				i++
			}
		case "--output-dir":
			if i+1 < len(args) {
				outDir = args[i+1]
				i++
			}
		}
	}
	payload := map[string]any{"id": session, "harness": "claude-code", "turns": []any{}, "session": session}
	if info, err := os.Stat(outDir); err == nil {
		payload["dirMode"] = fmt.Sprintf("%o", info.Mode().Perm())
		payload["outputDir"] = outDir
	}
	write := func(name string, value any) {
		data, err := json.Marshal(value)
		if err != nil {
			fmt.Fprintln(os.Stderr, "marshal:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(filepath.Join(outDir, name), data, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
	}
	switch session {
	case "missing":
		fmt.Fprintln(os.Stderr, "warning: session missing: export session missing: session not found")
		os.Exit(1)
	case "incomplete":
		fmt.Fprintln(os.Stderr, "warning: session incomplete: store: session incomplete holds an incomplete latest attempt beside its last-good generation")
		os.Exit(1)
	case "schema-newer":
		fmt.Fprintln(os.Stderr, "Error: open analytics store: store: open db: database schema is version 68, but this build understands only up to version 67 (the database was upgraded by a newer Peasant)")
		os.Exit(1)
	case "invalid-id":
		fmt.Fprintln(os.Stderr, "Error: export session odd: invalid session ID \"odd\": contains path separator or traversal")
		os.Exit(1)
	case "unknown-failure":
		fmt.Fprintln(os.Stderr, "boom: internal failure")
		os.Exit(2)
	case "no-file":
		os.Exit(0)
	case "malformed":
		_ = os.WriteFile(filepath.Join(outDir, "malformed.json"), []byte("{not json"), 0o600)
	case "no-identity":
		_ = os.WriteFile(filepath.Join(outDir, "no-identity.json"), []byte("{\"turns\":[]}"), 0o600)
	case "empty":
		_ = os.WriteFile(filepath.Join(outDir, "empty.json"), nil, 0o600)
	case "two-files":
		write("two-files.json", payload)
		write("other.json", payload)
	case "mismatch-id":
		payload["id"] = "someone-else"
		write("mismatch-id.json", payload)
	case "mismatch-harness":
		payload["harness"] = "codex"
		write("mismatch-harness.json", payload)
	case "slow":
		time.Sleep(10 * time.Second)
	case "ok":
		write("ok.json", payload)
	default:
		write(session+".json", payload)
	}
}
`

func TestPeasantTranscriptSourceExportsTheDurablePayload(t *testing.T) {
	t.Parallel()
	source := &PeasantTranscriptSource{Binary: fakePeasantBinary(t), Timeout: 5 * time.Second}

	data, err := source.ExportTranscript(context.Background(), "claude-code", "ok")
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))
	require.Equal(t, "ok", payload["id"])
	require.Equal(t, "claude-code", payload["harness"])
	require.Equal(t, "700", payload["dirMode"], "the export directory must be private")
	require.NoDirExists(t, payload["outputDir"].(string), "the private export directory must be removed")
}

func TestPeasantTranscriptSourceClassifiesFailures(t *testing.T) {
	t.Parallel()
	binary := fakePeasantBinary(t)
	cases := []struct {
		name    string
		session string
		harness string
		want    error
		message string
	}{
		{
			name:    "missing session",
			session: "missing",
			want:    ErrTranscriptNotFound,
			message: "session not found",
		},
		{
			name:    "incomplete latest attempt",
			session: "incomplete",
			want:    ErrTranscriptIncomplete,
			message: "incomplete latest attempt",
		},
		{
			name:    "store written by a newer peasant",
			session: "schema-newer",
			want:    ErrTranscriptUnsupported,
			message: "install the Peasant version",
		},
		{
			name:    "session id the shared contract rejects",
			session: "invalid-id",
			want:    ErrTranscriptUnsupported,
			message: "Peasant supports UUID",
		},
		{
			name:    "unclassified failure",
			session: "unknown-failure",
			want:    ErrTranscriptExportFailed,
			message: "boom: internal failure",
		},
		{
			name:    "no document written",
			session: "no-file",
			want:    ErrTranscriptMalformed,
			message: "wrote no JSON transcript",
		},
		{
			name:    "two documents for one session",
			session: "two-files",
			want:    ErrTranscriptMalformed,
			message: "cannot tell which document",
		},
		{
			name:    "invalid JSON",
			session: "malformed",
			want:    ErrTranscriptMalformed,
			message: "not one complete JSON document",
		},
		{
			// An unexpected export envelope (a payload nested under another
			// object) must fail closed instead of being forwarded as a payload.
			name:    "unexpected export envelope",
			session: "no-identity",
			want:    ErrTranscriptMalformed,
			message: "carries no id and harness",
		},
		{
			name:    "empty document",
			session: "empty",
			want:    ErrTranscriptMalformed,
			message: "is empty",
		},
		{
			name:    "payload names another session",
			session: "mismatch-id",
			harness: "claude-code",
			want:    ErrTranscriptIdentityMismatch,
			message: "someone-else",
		},
		{
			name:    "payload names another harness",
			session: "mismatch-harness",
			harness: "claude-code",
			want:    ErrTranscriptIdentityMismatch,
			message: "codex",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			harness := tc.harness
			if harness == "" {
				harness = "claude-code"
			}
			source := &PeasantTranscriptSource{Binary: binary, Timeout: 5 * time.Second}
			_, err := source.ExportTranscript(context.Background(), harness, tc.session)
			require.ErrorIs(t, err, tc.want)
			require.Contains(t, err.Error(), tc.message)
		})
	}
}

func TestPeasantTranscriptSourceReportsAMissingBinary(t *testing.T) {
	t.Parallel()
	source := &PeasantTranscriptSource{Binary: filepath.Join(t.TempDir(), "absent-peasant")}
	_, err := source.ExportTranscript(context.Background(), "claude-code", "ok")
	require.ErrorIs(t, err, ErrTranscriptSourceUnavailable)
	require.Contains(t, err.Error(), "install Peasant")
}

func TestPeasantTranscriptSourceStopsAtTheBoundedTimeout(t *testing.T) {
	t.Parallel()
	source := &PeasantTranscriptSource{Binary: fakePeasantBinary(t), Timeout: 150 * time.Millisecond}

	started := time.Now()
	_, err := source.ExportTranscript(context.Background(), "claude-code", "slow")
	require.ErrorIs(t, err, ErrTranscriptTimedOut)
	require.Contains(t, err.Error(), "Peasant was stopped")
	require.Less(t, time.Since(started), 5*time.Second, "the bounded context must kill the export, not wait for it")
}

// stubTranscriptSource lets handler tests assert each error mapping without
// starting a process.
type stubTranscriptSource struct {
	payload  []byte
	err      error
	block    bool
	deadline chan time.Time
}

func (s *stubTranscriptSource) ExportTranscript(ctx context.Context, _, _ string) ([]byte, error) {
	if s.deadline != nil {
		deadline, _ := ctx.Deadline()
		select {
		case s.deadline <- deadline:
		default:
		}
	}
	if s.block {
		<-ctx.Done()
		return nil, fmt.Errorf("%w: stub observed %v", ErrTranscriptTimedOut, ctx.Err())
	}
	return s.payload, s.err
}

func TestSessionTranscriptHandler(t *testing.T) {
	t.Parallel()
	validPayload := []byte(`{"id":"session-1","harness":"claude-code","turns":[]}`)

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
			name:       "unsupported store",
			source:     &stubTranscriptSource{err: fmt.Errorf("%w: newer schema", ErrTranscriptUnsupported)},
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
			server := httptest.NewServer(newHandler(nil, nil, tc.source, 5*time.Second))
			defer server.Close()

			response, err := http.Get(server.URL + "/api/v1/sessions/claude-code/session-1/transcript")
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, tc.wantStatus, response.StatusCode)

			if tc.wantStatus == http.StatusOK {
				require.Equal(t, "application/json", response.Header.Get("Content-Type"))
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

func TestSessionTranscriptHandlerRejectsAnInvalidSessionKey(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(newHandler(nil, nil, &stubTranscriptSource{}, time.Second))
	defer server.Close()

	response, err := http.Get(server.URL + "/api/v1/sessions/claude-code/a%2Fb/transcript")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusBadRequest, response.StatusCode)

	var envelope errorEnvelope
	require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
	require.Equal(t, "invalid_session_key", envelope.Error.Code)
}

func TestSessionTranscriptHandlerBoundsTheWait(t *testing.T) {
	t.Parallel()
	source := &stubTranscriptSource{block: true, deadline: make(chan time.Time, 1)}
	server := httptest.NewServer(newHandler(nil, nil, source, 60*time.Millisecond))
	defer server.Close()

	started := time.Now()
	response, err := http.Get(server.URL + "/api/v1/sessions/claude-code/session-1/transcript")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusGatewayTimeout, response.StatusCode)
	require.Less(t, time.Since(started), 5*time.Second)

	select {
	case deadline := <-source.deadline:
		require.False(t, deadline.IsZero(), "the handler must derive a request deadline")
		require.WithinDuration(t, time.Now(), deadline, 5*time.Second)
	default:
		t.Fatal("the transcript source never observed a request deadline")
	}
}

func TestNewPeasantTranscriptSourceUsesTheOutermostTimeoutTier(t *testing.T) {
	t.Parallel()
	require.Equal(t, timeouts.ProductionProfile().WorkflowResult(), NewPeasantTranscriptSource().Timeout)
	require.Positive(t, transcriptExportTimeout())
}

func TestSummarizeDiagnosticKeepsTheRefusalLine(t *testing.T) {
	t.Parallel()
	require.Equal(t,
		"store: session x holds an incomplete latest attempt",
		summarizeDiagnostic("\n  store: session x holds an incomplete latest attempt  \nUsage:\n  peasant export sessions\n"))
	require.Equal(t, "no diagnostic was printed", summarizeDiagnostic(" \n\t\n"))
	require.True(t, strings.HasSuffix(summarizeDiagnostic(strings.Repeat("x", 600)), "..."))
}
