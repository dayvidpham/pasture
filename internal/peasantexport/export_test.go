package peasantexport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/timeouts"
)

// fakePeasant is the helper program the source tests run instead of the real
// Peasant CLI. It is compiled once per test process from the Go source below and
// selected by session id, so no test mutates PATH or the environment.
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
	writeRaw := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(outDir, name), data, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
	}
	write := func(name string, value any) {
		data, err := json.Marshal(value)
		if err != nil {
			fmt.Fprintln(os.Stderr, "marshal:", err)
			os.Exit(1)
		}
		writeRaw(name, data)
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
		writeRaw("malformed.json", []byte("{not json"))
	case "trailing":
		writeRaw("trailing.json", []byte("{\"id\":\""+session+"\",\"harness\":\"claude-code\",\"turns\":[]} {\"extra\":true}"))
	case "no-identity":
		writeRaw("no-identity.json", []byte("{\"turns\":[]}"))
	case "no-id":
		writeRaw("no-id.json", []byte("{\"harness\":\"claude-code\",\"turns\":[]}"))
	case "empty-harness":
		writeRaw("empty-harness.json", []byte("{\"id\":\""+session+"\",\"turns\":[]}"))
	case "empty":
		writeRaw("empty.json", nil)
	case "two-files":
		write("two-files.json", payload)
		write("other.json", payload)
	case "legacy-claude":
		payload["harness"] = "claude"
		write("legacy-claude.json", payload)
	case "legacy-gemini":
		payload["harness"] = "gemini"
		write("legacy-gemini.json", payload)
	case "mismatch-id":
		payload["id"] = "someone-else"
		write("mismatch-id.json", payload)
	case "mismatch-harness":
		payload["harness"] = "codex"
		write("mismatch-harness.json", payload)
	case "too-large":
		writeRaw("too-large.json", []byte("{\"id\":\""+session+"\",\"harness\":\"claude-code\",\"turns\":[]}"))
		if err := os.Truncate(filepath.Join(outDir, "too-large.json"), 136 << 20); err != nil {
			fmt.Fprintln(os.Stderr, "truncate:", err)
			os.Exit(1)
		}
	case "slow":
		time.Sleep(10 * time.Second)
	case "ok":
		write("ok.json", payload)
	default:
		write(session+".json", payload)
	}
}
`

func TestSourceExportsTheDurablePayload(t *testing.T) {
	t.Parallel()
	source := &Source{Binary: fakePeasantBinary(t), Timeout: 5 * time.Second}

	export, err := source.ExportTranscript(context.Background(), "claude-code", "ok")
	require.NoError(t, err)
	require.NotNil(t, export)
	t.Cleanup(func() { _ = export.Close() })

	data, err := io.ReadAll(export)
	require.NoError(t, err)
	require.Equal(t, export.Size, int64(len(data)))
	require.False(t, export.ModTime.IsZero())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))
	require.Equal(t, "ok", payload["id"])
	require.Equal(t, "claude-code", payload["harness"])
	require.Equal(t, "700", payload["dirMode"], "the export directory must be private")

	outputDir := payload["outputDir"].(string)
	require.DirExists(t, outputDir, "the directory must stay alive while the caller streams the file")
	require.NoError(t, export.Close())
	require.NoDirExists(t, outputDir, "closing the export must remove the private directory")
	require.NoError(t, export.Close(), "Close must be safe to call twice")
}

func TestSourceClassifiesFailures(t *testing.T) {
	t.Parallel()
	binary := fakePeasantBinary(t)

	cases := []struct {
		name    string
		session string
		harness string
		want    Kind
		message string
	}{
		{
			name:    "missing session",
			session: "missing",
			want:    KindNotFound,
			message: "session not found",
		},
		{
			name:    "incomplete latest attempt",
			session: "incomplete",
			want:    KindIncomplete,
			message: "incomplete latest attempt",
		},
		{
			name:    "store written by a newer peasant",
			session: "schema-newer",
			want:    KindUnsupported,
			message: "install the Peasant version",
		},
		{
			name:    "session id the shared contract rejects",
			session: "invalid-id",
			want:    KindUnsupported,
			message: "Peasant supports UUID",
		},
		{
			name:    "unclassified failure",
			session: "unknown-failure",
			want:    KindExportFailed,
			message: "boom: internal failure",
		},
		{
			name:    "no document written",
			session: "no-file",
			want:    KindMalformed,
			message: "wrote no JSON transcript",
		},
		{
			name:    "two documents for one session",
			session: "two-files",
			want:    KindMalformed,
			message: "cannot tell which document",
		},
		{
			name:    "invalid JSON",
			session: "malformed",
			want:    KindMalformed,
			message: "not one complete JSON document",
		},
		{
			name:    "trailing content after the payload",
			session: "trailing",
			want:    KindMalformed,
			message: "carries content after the durable document",
		},
		{
			// An unexpected export envelope (a payload nested under another
			// object) must fail closed instead of being forwarded as a payload.
			name:    "unexpected export envelope",
			session: "no-identity",
			want:    KindMalformed,
			message: "carries no id and harness",
		},
		{
			name:    "document without a session id",
			session: "no-id",
			want:    KindMalformed,
			message: "carries no session id",
		},
		{
			name:    "empty document",
			session: "empty",
			want:    KindMalformed,
			message: "is empty",
		},
		{
			name:    "payload names another session",
			session: "mismatch-id",
			harness: "claude-code",
			want:    KindIdentityMismatch,
			message: "someone-else",
		},
		{
			name:    "payload names another harness",
			session: "mismatch-harness",
			harness: "claude-code",
			want:    KindIdentityMismatch,
			message: "codex",
		},
		{
			name:    "payload records no harness",
			session: "empty-harness",
			harness: "claude-code",
			want:    KindUnsupported,
			message: "did not record a harness",
		},
		{
			name:    "export above the size bound",
			session: "too-large",
			want:    KindTooLarge,
			message: "above the",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			harness := tc.harness
			if harness == "" {
				harness = "claude-code"
			}
			source := &Source{Binary: binary, Timeout: 5 * time.Second}
			export, err := source.ExportTranscript(context.Background(), harness, tc.session)
			if err == nil {
				_ = export.Close()
			}
			require.Error(t, err)
			require.Equal(t, tc.want, KindOf(err), "error: %v", err)
			require.Contains(t, err.Error(), tc.message)
		})
	}
}

func TestVerifyIdentityNamesTrailingContent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		payload    string
		wantDetail string
	}{
		{name: "second object", payload: `{"id":"x","harness":"claude-code"}{"id":"y"}`, wantDetail: "token {"},
		{name: "trailing scalar", payload: `{"id":"x","harness":"claude-code"} true`, wantDetail: "token true"},
		{name: "malformed tail", payload: `{"id":"x","harness":"claude-code"} garbage`, wantDetail: "invalid character"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := verifyIdentity(strings.NewReader(tc.payload), "claude-code", "x")
			require.Equal(t, KindMalformed, KindOf(err))
			require.Contains(t, err.Error(), "carries content after the durable document")
			require.Contains(t, err.Error(), tc.wantDetail)
			require.NotContains(t, err.Error(), "<nil>", "a valid trailing token has a nil error and must not be printed")
		})
	}
}

func TestSourceNormalizesLegacyHarnesses(t *testing.T) {
	t.Parallel()
	binary := fakePeasantBinary(t)
	cases := []struct {
		name    string
		session string
		harness string
	}{
		{name: "claude to claude-code", session: "legacy-claude", harness: "claude-code"},
		{name: "gemini to gemini-cli", session: "legacy-gemini", harness: "gemini-cli"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := &Source{Binary: binary, Timeout: 5 * time.Second}
			export, err := source.ExportTranscript(context.Background(), tc.harness, tc.session)
			require.NoError(t, err)
			require.NoError(t, export.Close())
		})
	}

	t.Run("legacy alias does not accept a different canonical harness", func(t *testing.T) {
		t.Parallel()
		source := &Source{Binary: binary, Timeout: 5 * time.Second}
		export, err := source.ExportTranscript(context.Background(), "opencode", "legacy-claude")
		if err == nil {
			_ = export.Close()
		}
		require.Equal(t, KindIdentityMismatch, KindOf(err))
	})
}

func TestSourceReportsAMissingBinary(t *testing.T) {
	t.Parallel()
	source := &Source{Binary: filepath.Join(t.TempDir(), "absent-peasant")}
	export, err := source.ExportTranscript(context.Background(), "claude-code", "ok")
	if err == nil {
		_ = export.Close()
	}
	require.Equal(t, KindUnavailable, KindOf(err))
	require.Contains(t, err.Error(), "install Peasant")
}

func TestSourceStopsAtTheBoundedTimeout(t *testing.T) {
	t.Parallel()
	source := &Source{Binary: fakePeasantBinary(t), Timeout: 150 * time.Millisecond}

	started := time.Now()
	export, err := source.ExportTranscript(context.Background(), "claude-code", "slow")
	if err == nil {
		_ = export.Close()
	}
	require.Equal(t, KindTimedOut, KindOf(err))
	require.Contains(t, err.Error(), "Peasant was stopped")
	require.Less(t, time.Since(started), 5*time.Second, "the bounded context must kill the export, not wait for it")
}

func TestSourceUsesTheOutermostTimeoutTier(t *testing.T) {
	t.Parallel()
	require.Equal(t, timeouts.ProductionProfile().WorkflowResult(), NewSource().Timeout)
	require.Positive(t, NewSource().Timeout)
}

func TestSummarizeDiagnosticKeepsTheRefusalLine(t *testing.T) {
	t.Parallel()
	require.Equal(t,
		"store: session x holds an incomplete latest attempt",
		summarizeDiagnostic("\n  store: session x holds an incomplete latest attempt  \nUsage:\n  peasant export sessions\n"))
	require.Equal(t, "no diagnostic was printed", summarizeDiagnostic(" \n\t\n"))
	require.True(t, strings.HasSuffix(summarizeDiagnostic(strings.Repeat("x", 600)), "..."))
}

func TestSummarizeDiagnosticTruncatesOnARuneBoundary(t *testing.T) {
	t.Parallel()
	// Every "é" is two bytes, so a byte-index cut would split one.
	line := "/home/user/éxport/" + strings.Repeat("é", 400)
	got := summarizeDiagnostic(line)
	require.True(t, utf8.ValidString(got), "truncated diagnostic must stay valid UTF-8")
	require.True(t, strings.HasSuffix(got, "..."))
	require.LessOrEqual(t, len(got), 480)
	require.NotContains(t, got, "\uFFFD")
}

func TestTruncateUTF8CommonCases(t *testing.T) {
	t.Parallel()
	require.Equal(t, "short", truncateUTF8("short", 480))
	require.Equal(t, "ab...", truncateUTF8("abcdef", 5))
	// "aé" is three bytes; cutting at byte 3 would land between the two bytes
	// of "é" if the bound were 4, so the helper must back up to a rune start.
	got := truncateUTF8("aéééééééééé", 6)
	require.True(t, utf8.ValidString(got))
	require.True(t, strings.HasSuffix(got, "..."))
}
