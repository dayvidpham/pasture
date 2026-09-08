package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// These executables are boundary doubles, not authentic host payload evidence.
func versionExecutable(t *testing.T, body string) string {
	t.Helper()
	shell, err := exec.LookPath("sh")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "host space ; $(not-a-command)")
	script := "#!" + shell + "\n[ \"$#\" = 1 ] && [ \"$1\" = --version ] || exit 91\n" + body + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	return path
}

func generatedClaudeLifecycleCommand(t *testing.T, event string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "hooks", "hooks.json"))
	require.NoError(t, err)
	var config struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(raw, &config))
	var commands []string
	for _, group := range config.Hooks[event] {
		for _, hook := range group.Hooks {
			if strings.Contains(hook.Command, " hook lifecycle ") {
				commands = append(commands, hook.Command)
			}
		}
	}
	require.Len(t, commands, 1, "exactly one lifecycle command must bind this generated event")
	return commands[0]
}

// Child environments keep parallel proofs independent of the test process.
// A nil value removes a key; an empty string deliberately retains an empty key.
func discoveryChildEnv(values map[string]*string) []string {
	env := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := values[key]; !replaced {
			env = append(env, entry)
		}
	}
	for key, value := range values {
		if value != nil {
			env = append(env, key+"="+*value)
		}
	}
	return env
}

func discoveryValue(value string) *string { return &value }

func discoveryPathExecutable(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "path space ; $(not-a-command)")
	require.NoError(t, os.Mkdir(dir, 0o700))
	executable := filepath.Join(dir, "claude")
	require.NoError(t, os.Symlink(versionExecutable(t, body), executable))
	return dir, executable
}

// Direct CLI proofs intentionally do not claim generated consumer readiness.
func TestNativeDefaultVersionQuery(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	for _, tc := range []struct{ harness, event, fixture, banner, version, response string }{
		{"codex", "SessionStart", "session_start_0_153_0.json", "codex-cli 0.154.1-beta.2+build.7", "0.154.1-beta.2+build.7", `{}`},
		{"opencode", "session.created", "session_created_1_18_29.json", "1.19.1-beta.2+build.7", "1.19.1-beta.2+build.7", ""},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", tc.harness, "testdata", "fixtures", tc.fixture))
			require.NoError(t, err)
			path := t.TempDir()
			marker := filepath.Join(t.TempDir(), "queries")
			executable := versionExecutable(t, "printf x >> '"+marker+"'; printf '%s\\n' '"+tc.banner+"'")
			require.NoError(t, os.Symlink(executable, filepath.Join(path, tc.harness)))
			dbPath := filepath.Join(t.TempDir(), "pasture.db")
			initializeLifecycleTestDatabase(t, dbPath)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "hook", "lifecycle", "--harness", tc.harness, "--event", tc.event)
			command.Env = discoveryChildEnv(map[string]*string{"PATH": &path, "CLAUDE_CODE_EXECPATH": nil,
				"PASTURE_DB_PATH": &dbPath, "PASTURE_CAPTURE_DIR": nil, "PASTURE_ACTOR_ID": nil, "PASTURE_HOOK_FAIL_CLOSED": nil})
			command.Stdin = bytes.NewReader(raw)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			require.NoError(t, command.Run(), stderr.String())
			queried, err := os.ReadFile(marker)
			require.NoError(t, err, "default must query the selected native executable")
			require.Equal(t, "x", string(queried))
			require.Empty(t, stderr.String())
			require.Equal(t, tc.response, stdout.String())
			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			defer tracker.Close()
			rows := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
			require.Len(t, rows, 1)
			payload := decodeOccurrencePayload(t, rows[0].Payload)
			require.Equal(t, tc.version, payload.Envelope.HostVersion)
			require.Equal(t, model.HostVersionExecutableQuery, payload.Envelope.HostVersionSource)
			require.Len(t, queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind), 1)
		})
	}
}

func TestNativeVersionSelectionAndRefusal(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	for _, harness := range []string{"codex", "opencode"} {
		event, fixture, prefix, native := "SessionStart", "session_start_0_153_0.json", "codex-cli ", `{}`
		if harness == "opencode" {
			event, fixture, prefix, native = "session.created", "session_created_1_18_29.json", "", ""
		}
		raw, err := os.ReadFile(filepath.Join("..", "..", "internal/lifecycle/ingress", harness, "testdata/fixtures", fixture))
		require.NoError(t, err)
		for _, mode := range []string{"first", "reverse", "explicit executable", "explicit version", "missing", "malformed", "nonzero", "overflow", "explicit failure", "conflict", "empty conflict", "empty executable", "relative executable", "refused"} {
			t.Run(harness+"/"+mode, func(t *testing.T) {
				markers := t.TempDir()
				firstDir, secondDir := t.TempDir(), t.TempDir()
				write := func(dir, marker, body string) string {
					p := filepath.Join(dir, harness)
					require.NoError(t, os.Symlink(versionExecutable(t, "printf x >> '"+filepath.Join(markers, marker)+"'; "+body), p))
					return p
				}
				firstBody := "printf '%s\\n' '" + prefix + "3.4.5'"
				wantFault := ""
				switch mode {
				case "malformed":
					firstBody, wantFault = "printf 'junk banner\\n'", "host version resolution failed"
				case "nonzero", "explicit failure":
					firstBody, wantFault = "exit 17", "exit status 17"
				case "overflow":
					firstBody, wantFault = "printf '%4097s' x", "4096-byte"
				}
				first := write(firstDir, "first", firstBody)
				write(secondDir, "second", "printf '%s\\n' '"+prefix+"3.4.6'")
				path := firstDir + string(os.PathListSeparator) + secondDir
				args := []string{"hook", "lifecycle", "--harness", harness, "--event", event}
				wantVersion, wantMarker, source := "3.4.5", "first", model.HostVersionExecutableQuery
				switch mode {
				case "reverse":
					path, wantVersion, wantMarker = secondDir+string(os.PathListSeparator)+firstDir, "3.4.6", "second"
				case "explicit executable", "explicit failure":
					args = append(args, "--host-executable", first)
					path = secondDir
				case "explicit version":
					args = append(args, "--host-version", "host-local")
					wantVersion, wantMarker, source = "host-local", "", model.HostVersionCallerSupplied
				case "missing":
					path, wantMarker, wantFault = t.TempDir(), "", "no usable "+harness+" executable"
				case "conflict":
					args = append(args, "--host-executable", first, "--host-version", "3.4.5")
					wantMarker, wantFault = "", "mutually exclusive"
				case "empty conflict":
					args = append(args, "--host-executable", "", "--host-version", "")
					wantMarker, wantFault = "", "mutually exclusive"
				case "empty executable":
					args = append(args, "--host-executable", "")
					wantMarker, wantFault = "", "supplied empty"
				case "relative executable":
					args = append(args, "--host-executable", harness)
					wantMarker, wantFault = "", "complete absolute path"
				}
				input := raw
				if mode == "refused" {
					input = []byte(`{}`)
				}
				dbPath := filepath.Join(t.TempDir(), "pasture.db")
				if wantFault == "" {
					initializeLifecycleTestDatabase(t, dbPath)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, binary, args...)
				command.Env = discoveryChildEnv(map[string]*string{"PATH": &path, "CLAUDE_CODE_EXECPATH": discoveryValue("/unused-claude-hint"), "PASTURE_DB_PATH": &dbPath,
					"PASTURE_CAPTURE_DIR": nil, "PASTURE_ACTOR_ID": nil, "PASTURE_HOOK_FAIL_CLOSED": nil})
				command.Stdin = bytes.NewReader(input)
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				require.NoError(t, command.Run(), stderr.String())
				require.Equal(t, native, stdout.String())
				for _, marker := range []string{"first", "second"} {
					body, err := os.ReadFile(filepath.Join(markers, marker))
					if marker == wantMarker {
						require.NoError(t, err)
						require.Equal(t, "x", string(body))
					} else {
						require.ErrorIs(t, err, os.ErrNotExist, "must not hunt another candidate")
					}
				}
				if wantFault != "" {
					require.Contains(t, stderr.String(), wantFault)
					_, err := os.Stat(dbPath)
					require.ErrorIs(t, err, os.ErrNotExist)
					fault, err := os.ReadFile(filepath.Join(filepath.Dir(dbPath), lifecycleFaultRecordFile))
					require.NoError(t, err)
					record := decodeJSONObject(t, fault)
					require.JSONEq(t, `""`, string(record["hostVersion"]))
					require.NotContains(t, record, "hostVersionSource")
					return
				}
				tracker, err := tasks.OpenTaskTracker(dbPath)
				require.NoError(t, err)
				defer tracker.Close()
				rows := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
				require.Len(t, rows, 1)
				occurrence := decodeOccurrencePayload(t, rows[0].Payload)
				require.Equal(t, wantVersion, occurrence.Envelope.HostVersion)
				require.Equal(t, source, occurrence.Envelope.HostVersionSource)
				require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
				reader, err := tasks.NewLifecycleReader(tracker)
				require.NoError(t, err)
				size, err := model.NewPageSize(1)
				require.NoError(t, err)
				page, err := reader.Records(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: size}})
				require.NoError(t, err)
				require.Len(t, page.Records(), 1)
				projected := page.Records()[0].Occurrence
				require.Equal(t, occurrence.Envelope, projected.Envelope)
				body, err := reader.Payload(context.Background(), projected.Payload.Digest)
				require.NoError(t, err)
				require.Equal(t, input, body)
				if mode == "refused" {
					require.Equal(t, model.CaptureUnsupportedSchema, projected.Capture)
					require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind))
					fault, err := os.ReadFile(filepath.Join(filepath.Dir(dbPath), lifecycleFaultRecordFile))
					require.NoError(t, err)
					var record struct {
						HostVersion string                  `json:"hostVersion"`
						Source      model.HostVersionSource `json:"hostVersionSource"`
					}
					require.NoError(t, json.Unmarshal(fault, &record))
					require.Equal(t, wantVersion, record.HostVersion)
					require.Equal(t, source, record.Source)
				} else {
					require.Empty(t, stderr.String())
					require.Equal(t, model.CaptureValid, projected.Capture)
					require.Len(t, queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind), 1)
				}
			})
		}
	}
}

func TestClaudeDefaultDiscoverySelectsOneExecutable(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	shell, err := exec.LookPath("sh")
	require.NoError(t, err)
	raw := readProductionClaudeFixture(t, "session_start_2_1_261.json", "SessionStart")
	for _, mode := range []string{"absent", "empty", "reverse", "hint", "relative hint", "missing hint", "directory hint", "nonexecutable hint", "dangling hint", "explicit executable", "explicit version", "failed PATH query", "failed hint query"} {
		t.Run(mode, func(t *testing.T) {
			markers := t.TempDir()
			firstDir, first := discoveryPathExecutable(t, "printf x >> '"+filepath.Join(markers, "first")+"'; printf '2.1.299 (Claude Code)\\n'")
			secondDir, _ := discoveryPathExecutable(t, "printf x >> '"+filepath.Join(markers, "second")+"'; printf '2.1.300 (Claude Code)\\n'")
			hint := versionExecutable(t, "printf x >> '"+filepath.Join(markers, "hint")+"'; printf '2.1.301 (Claude Code)\\n'")
			path := firstDir + string(os.PathListSeparator) + secondDir
			var hintValue *string
			wantVersion, wantMarker := "2.1.299", "first"
			args := generatedClaudeLifecycleCommand(t, "SessionStart")
			failed := false
			switch mode {
			case "empty":
				hintValue = discoveryValue("")
			case "reverse":
				path = secondDir + string(os.PathListSeparator) + firstDir
				wantVersion, wantMarker = "2.1.300", "second"
			case "hint":
				hintValue = &hint
				wantVersion, wantMarker = "2.1.301", "hint"
			case "relative hint":
				hintValue = discoveryValue("claude")
			case "missing hint":
				hintValue = discoveryValue(filepath.Join(markers, "missing"))
			case "directory hint":
				hintValue = &markers
			case "nonexecutable hint":
				p := filepath.Join(markers, "not-executable")
				require.NoError(t, os.WriteFile(p, []byte("no"), 0o600))
				hintValue = &p
			case "dangling hint":
				p := filepath.Join(markers, "dangling")
				require.NoError(t, os.Symlink(filepath.Join(markers, "missing"), p))
				hintValue = &p
			case "explicit executable":
				hintValue = &hint
				args += " --host-executable '" + first + "'"
			case "explicit version":
				hintValue = &hint
				args += " --host-version 2.1.302"
				wantVersion, wantMarker = "2.1.302", ""
			case "failed PATH query":
				bad := versionExecutable(t, "printf x >> '"+filepath.Join(markers, "first")+"'; exit 17")
				require.NoError(t, os.Remove(first))
				require.NoError(t, os.Symlink(bad, first))
				failed = true
			case "failed hint query":
				hint = versionExecutable(t, "printf x >> '"+filepath.Join(markers, "hint")+"'; exit 17")
				hintValue = &hint
				wantMarker = "hint"
				failed = true
			}
			dbPath := filepath.Join(t.TempDir(), "pasture.db")
			if !failed {
				initializeLifecycleTestDatabase(t, dbPath)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, shell, "-c", args)
			command.Env = discoveryChildEnv(map[string]*string{"PATH": &path, "CLAUDE_CODE_EXECPATH": hintValue,
				"PASTURE_BIN": &binary, "PASTURE_DB_PATH": &dbPath, "PASTURE_CAPTURE_DIR": nil, "PASTURE_ACTOR_ID": nil, "PASTURE_HOOK_FAIL_CLOSED": nil})
			command.Stdin = bytes.NewReader(raw)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			require.NoError(t, command.Run(), stderr.String())
			require.Empty(t, stdout.String())
			for _, marker := range []string{"first", "second", "hint"} {
				body, err := os.ReadFile(filepath.Join(markers, marker))
				if marker == wantMarker {
					require.NoError(t, err)
					require.Equal(t, "x", string(body))
				} else {
					require.ErrorIs(t, err, os.ErrNotExist, "must not query another candidate")
				}
			}
			if failed {
				require.Contains(t, stderr.String(), "exit status 17")
				_, err := os.Stat(dbPath)
				require.ErrorIs(t, err, os.ErrNotExist)
				fault, err := os.ReadFile(filepath.Join(filepath.Dir(dbPath), lifecycleFaultRecordFile))
				require.NoError(t, err)
				record := decodeJSONObject(t, fault)
				require.NotContains(t, record, "hostVersionSource")
				require.JSONEq(t, `""`, string(record["hostVersion"]))
				return
			}
			require.Empty(t, stderr.String())
			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			defer tracker.Close()
			rows := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
			require.Len(t, rows, 1)
			payload := decodeOccurrencePayload(t, rows[0].Payload)
			require.Equal(t, wantVersion, payload.Envelope.HostVersion)
			wantSource := model.HostVersionExecutableQuery
			if mode == "explicit version" {
				wantSource = model.HostVersionCallerSupplied
			}
			require.Equal(t, wantSource, payload.Envelope.HostVersionSource)
			require.Len(t, queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind), 1)
		})
	}
}

func TestClaudeDefaultDiscoveryResolutionFaultsPrecedeCaptureAndStorage(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	shell, err := exec.LookPath("sh")
	require.NoError(t, err)
	for _, mode := range []string{"absent PATH", "empty PATH", "no match", "ErrDot", "missing interpreter", "malformed", "stdout cap", "stderr cap"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			path := discoveryValue(dir)
			want := "no usable claude executable"
			switch mode {
			case "absent PATH":
				path = nil
			case "empty PATH":
				path = discoveryValue("")
			case "ErrDot":
				require.NoError(t, os.Symlink(versionExecutable(t, "exit 91"), filepath.Join(dir, "claude")))
				path = discoveryValue(".")
				want = "relative to current directory"
			case "missing interpreter":
				require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/nonexistent/claude-test-interpreter\n"), 0o700))
				want = "start selected host executable"
			case "malformed", "stdout cap", "stderr cap":
				body := "printf 'not a version\\n'"
				want = "does not match"
				if mode != "malformed" {
					body = "printf '%4097s' x"
					want = "4096-byte"
					if mode == "stderr cap" {
						body += " >&2; printf '2.1.299 (Claude Code)\\n'"
					}
				}
				require.NoError(t, os.Symlink(versionExecutable(t, body), filepath.Join(dir, "claude")))
			}
			dbPath := filepath.Join(t.TempDir(), "pasture.db")
			captureDir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, shell, "-c", generatedClaudeLifecycleCommand(t, "SessionStart"))
			command.Dir = dir
			command.Env = discoveryChildEnv(map[string]*string{"PATH": path, "CLAUDE_CODE_EXECPATH": nil, "GODEBUG": discoveryValue("execerrdot=1"),
				"PASTURE_BIN": &binary, "PASTURE_DB_PATH": &dbPath, "PASTURE_CAPTURE_DIR": &captureDir, "PASTURE_ACTOR_ID": nil, "PASTURE_HOOK_FAIL_CLOSED": nil})
			command.Stdin = strings.NewReader("{}")
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			require.NoError(t, command.Run(), stderr.String())
			require.Empty(t, stdout.String())
			require.Contains(t, stderr.String(), want)
			require.Contains(t, stderr.String(), "no occurrence was recorded")
			require.NotContains(t, stderr.String(), "variable is required")
			_, err := os.Stat(dbPath)
			require.ErrorIs(t, err, os.ErrNotExist)
			files, err := os.ReadDir(captureDir)
			require.NoError(t, err)
			require.Empty(t, files)
			fault, err := os.ReadFile(filepath.Join(filepath.Dir(dbPath), lifecycleFaultRecordFile))
			require.NoError(t, err)
			record := decodeJSONObject(t, fault)
			require.NotContains(t, record, "hostVersionSource")
			require.JSONEq(t, `""`, string(record["hostVersion"]))
		})
	}
}

func TestGeneratedClaudeCommandRetainsObservedVersionAndCaptureName(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	path, _ := discoveryPathExecutable(t, "printf '2.1.299 (Claude Code)\\n'")
	for _, state := range []string{"absent", "empty"} {
		t.Run(state, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "pasture.db")
			initializeLifecycleTestDatabase(t, dbPath)
			captureDir := t.TempDir()
			raw := readProductionClaudeFixture(t, "session_start_2_1_261.json", "SessionStart")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "sh", "-c", generatedClaudeLifecycleCommand(t, "SessionStart"))
			var hint *string
			if state == "empty" {
				hint = discoveryValue("")
			}
			command.Env = discoveryChildEnv(map[string]*string{"PASTURE_BIN": &binary, "PASTURE_DB_PATH": &dbPath,
				"PATH": &path, "CLAUDE_CODE_EXECPATH": hint, "CLAUDE_CODE_VERSION": discoveryValue("not-a-version"),
				"PASTURE_CAPTURE_DIR": &captureDir, "PASTURE_ACTOR_ID": nil, "PASTURE_HOOK_FAIL_CLOSED": nil})
			command.Stdin = bytes.NewReader(raw)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr

			err := command.Run()

			require.NoError(t, err, stderr.String())
			require.Empty(t, stdout.String())
			require.Contains(t, stderr.String(), "capture mode is recording")
			require.NotContains(t, stderr.String(), "could not")
			files, err := os.ReadDir(captureDir)
			require.NoError(t, err)
			require.Len(t, files, 1)
			require.Contains(t, files[0].Name(), "2_1_299")
			captured, err := os.ReadFile(filepath.Join(captureDir, files[0].Name()))
			require.NoError(t, err)
			require.Equal(t, raw, captured, "this is a scratch replay of existing cleared bytes, not a new authentic capture")
			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			defer tracker.Close()
			rows := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
			require.Len(t, rows, 1)
			require.Equal(t, "2.1.299", decodeOccurrencePayload(t, rows[0].Payload).Envelope.HostVersion)
			require.Equal(t, model.HostVersionExecutableQuery, decodeOccurrencePayload(t, rows[0].Payload).Envelope.HostVersionSource)
		})
	}
}

func TestLifecycleHostVersionFailuresNeverReachStorage(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	good := versionExecutable(t, "printf '2.1.299 (Claude Code)\\n'")
	malformed := versionExecutable(t, "printf 'junk 2.1.299 (Claude Code)\\n'")
	oversized := versionExecutable(t, "head -c 4097 /dev/zero")
	oversizedStderr := versionExecutable(t, "printf '2.1.299 (Claude Code)\\n'; head -c 4097 /dev/zero >&2")
	nonzero := versionExecutable(t, "printf '2.1.299 (Claude Code)\\n'; printf 'private diagnostic' >&2; exit 17")
	nonExecutable := filepath.Join(t.TempDir(), "not-executable")
	require.NoError(t, os.WriteFile(nonExecutable, []byte("no"), 0o600))
	cases := []struct {
		name    string
		harness string
		args    []string
		want    string
	}{
		{name: "empty", args: []string{"--host-executable", ""}, want: "supplied empty"},
		{name: "missing executable", args: []string{"--host-executable", filepath.Join(t.TempDir(), "missing")}, want: "start selected host executable"},
		{name: "relative", args: []string{"--host-executable", "claude"}, want: "complete absolute path"},
		{name: "not executable", args: []string{"--host-executable", nonExecutable}, want: "start selected host executable"},
		{name: "wrong product banner", harness: "codex", args: []string{"--host-executable", good}, want: "does not match"},
		{name: "conflicting sources", args: []string{"--host-executable", good, "--host-version", "2.1.261"}, want: "mutually exclusive"},
		{name: "empty explicit conflict", args: []string{"--host-executable", good, "--host-version", ""}, want: "mutually exclusive"},
		{name: "both empty conflict", args: []string{"--host-executable", "", "--host-version", ""}, want: "mutually exclusive"},
		{name: "malformed output", args: []string{"--host-executable", malformed}, want: "does not match"},
		{name: "oversized output", args: []string{"--host-executable", oversized}, want: "4096-byte"},
		{name: "oversized diagnostic", args: []string{"--host-executable", oversizedStderr}, want: "4096-byte"},
		{name: "nonzero exit", args: []string{"--host-executable", nonzero}, want: "exit status 17"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "pasture.db")
			captureDir := t.TempDir()
			harness := tc.harness
			if harness == "" {
				harness = "claude-code"
			}
			args := append([]string{"hook", "lifecycle", "--harness", harness, "--event", "SessionStart"}, tc.args...)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, args...)
			command.Env = append(os.Environ(), "PASTURE_DB_PATH="+dbPath, "PASTURE_CAPTURE_DIR="+captureDir)
			command.Stdin = strings.NewReader("{}")
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr

			err := command.Run()

			require.NoError(t, err, stderr.String())
			if harness == "claude-code" {
				require.Empty(t, stdout.String())
			} else {
				require.JSONEq(t, `{}`, stdout.String(), "Codex SessionStart is an observation")
			}
			require.Contains(t, stderr.String(), tc.want)
			require.Contains(t, stderr.String(), "no occurrence was recorded")
			require.NotContains(t, stderr.String(), "private diagnostic")
			require.NotContains(t, stderr.String(), "usual reason is another writer")
			_, err = os.Stat(dbPath)
			require.ErrorIs(t, err, os.ErrNotExist)
			files, err := os.ReadDir(captureDir)
			require.NoError(t, err)
			require.Empty(t, files)
			fault, err := os.ReadFile(filepath.Join(filepath.Dir(dbPath), lifecycleFaultRecordFile))
			require.NoError(t, err)
			var record map[string]any
			require.NoError(t, json.Unmarshal(fault, &record))
			require.Equal(t, "", record["hostVersion"])
			require.Equal(t, "not-recorded", record["faultStage"])
		})
	}
}

func TestGeneratedClaudeMissingExecutableDoesNotInventVersion(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", "-c", generatedClaudeLifecycleCommand(t, "SessionStart"))
	command.Env = discoveryChildEnv(map[string]*string{"PASTURE_BIN": discoveryValue(lifecycleBinary(t)), "PASTURE_DB_PATH": &dbPath,
		"PATH": nil, "CLAUDE_CODE_EXECPATH": nil, "CLAUDE_CODE_VERSION": discoveryValue("2.1.299"), "PASTURE_CAPTURE_DIR": nil, "PASTURE_HOOK_FAIL_CLOSED": nil})
	command.Stdin = strings.NewReader("{}")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()

	require.NoError(t, err, stderr.String())
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "no usable claude executable")
	require.Contains(t, stderr.String(), "install Claude and expose it on PATH")
	require.Contains(t, stderr.String(), "CLAUDE_CODE_EXECPATH")
	require.Contains(t, stderr.String(), "no occurrence was recorded")
	_, err = os.Stat(dbPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestGeneratedClaudeCommandSurvivesExecutableUpdate(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	transport := generatedClaudeLifecycleCommand(t, "SessionStart")
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	initializeLifecycleTestDatabase(t, dbPath)
	raw := readProductionClaudeFixture(t, "session_start_2_1_261.json", "SessionStart")
	first := versionExecutable(t, "printf '2.1.299 (Claude Code)\\n'")
	second := versionExecutable(t, "printf '2.1.300 (Claude Code)\\n'")
	current := filepath.Join(t.TempDir(), "current host")
	require.NoError(t, os.Symlink(first, current))
	run := func(payload []byte) string {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "sh", "-c", transport)
		command.Env = append(os.Environ(), "PASTURE_BIN="+binary, "PASTURE_DB_PATH="+dbPath,
			"CLAUDE_CODE_EXECPATH="+current, "PASTURE_CAPTURE_DIR=", "PASTURE_ACTOR_ID=")
		command.Stdin = bytes.NewReader(payload)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr

		err := command.Run()

		require.NoError(t, err, stderr.String())
		require.Empty(t, stdout.String())
		return stderr.String()
	}
	require.Empty(t, run(raw))
	// Simulate an atomic updater changing the executable link between hooks.
	// The observation describes that queried executable, not an older process.
	require.NoError(t, os.Symlink(second, current+".next"))
	require.NoError(t, os.Rename(current+".next", current))
	require.Empty(t, run(raw))
	var incompatible map[string]any
	require.NoError(t, json.Unmarshal(raw, &incompatible))
	incompatible["session_id"] = true
	invalid, err := json.Marshal(incompatible)
	require.NoError(t, err)
	require.NotEmpty(t, run(invalid), "a newer number does not prove required payload compatibility")
	require.Equal(t, transport, generatedClaudeLifecycleCommand(t, "SessionStart"), "no regeneration between invocations")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	rows := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	require.Len(t, rows, 3)
	var observed []string
	var valid, refused int
	for _, row := range rows {
		payload := decodeOccurrencePayload(t, row.Payload)
		observed = append(observed, payload.Envelope.HostVersion)
		switch payload.Capture {
		case model.CaptureValid:
			valid++
		case model.CaptureUnsupportedSchema:
			refused++
		default:
			t.Fatalf("unexpected capture disposition %v", payload.Capture)
		}
	}
	require.ElementsMatch(t, []string{"2.1.299", "2.1.300", "2.1.300"}, observed)
	require.Equal(t, 2, valid)
	require.Equal(t, 1, refused)
	require.Len(t, queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind), 2,
		"the incompatible required shape is retained as refused evidence, not interpreted because its version is newer")
}

func TestLifecycleVersionQueryBoundsExitAndInheritedPipes(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"exec sleep 30",
		"sleep 30 &\nprintf '2.1.299 (Claude Code)\\n'\nexit 0",
		"sleep 30 >&2 &\nprintf '2.1.299 (Claude Code)\\n'\nexit 0",
	} {
		t.Run(body, func(t *testing.T) {
			executable := versionExecutable(t, body)
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			started := time.Now()

			output, err := queryLifecycleHostVersion(ctx, executable)

			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Nil(t, output)
			require.Less(t, time.Since(started), 3*time.Second, "exit and inherited-pipe reads share the invocation budget")
		})
	}
}

func TestLifecycleVersionQueryPreservesCancellationCause(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("caller withdrew the invocation")
	cancel(cause)

	output, err := queryLifecycleHostVersion(ctx, versionExecutable(t, "exit 91"))

	require.Nil(t, output)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, cause)
}

func TestClaudeDefaultQueryUsesInvocationProcessAndPipeBounds(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	shell, err := exec.LookPath("sh")
	require.NoError(t, err)
	// This child is the stalled-query stimulus, not a test synchronization wait.
	// Resolve it before replacing PATH so the failure cannot be a missing utility.
	sleeper, err := exec.LookPath("sleep")
	require.NoError(t, err)
	for _, mode := range []string{"process", "inherited stdout", "inherited stderr"} {
		t.Run(mode, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "selected")
			body := "printf x > '" + marker + "'\nexec '" + sleeper + "' 30"
			if mode != "process" {
				stream := ""
				if mode == "inherited stderr" {
					stream = " >&2"
				}
				body = "printf x > '" + marker + "'\n'" + sleeper + "' 30" + stream + " &\nprintf '2.1.299 (Claude Code)\\n'\nexit 0"
			}
			path, _ := discoveryPathExecutable(t, body)
			dbPath := filepath.Join(t.TempDir(), "pasture.db")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, shell, "-c", generatedClaudeLifecycleCommand(t, "SessionStart"))
			command.Env = discoveryChildEnv(map[string]*string{"PATH": &path, "CLAUDE_CODE_EXECPATH": nil,
				"PASTURE_BIN": &binary, "PASTURE_DB_PATH": &dbPath, "PASTURE_CAPTURE_DIR": nil, "PASTURE_ACTOR_ID": nil, "PASTURE_HOOK_FAIL_CLOSED": nil})
			command.Stdin = strings.NewReader("{}")
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			require.NoError(t, command.Run(), stderr.String())
			require.NoError(t, ctx.Err(), "the query must finish under its invocation budget, not the test watchdog")
			selected, err := os.ReadFile(marker)
			require.NoError(t, err)
			require.Equal(t, "x", string(selected))
			require.Empty(t, stdout.String())
			require.Contains(t, stderr.String(), "context deadline exceeded")
			_, err = os.Stat(dbPath)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestLifecycleExplicitHostVersionDoesNotRequestProbing(t *testing.T) {
	t.Parallel()
	for _, harness := range []ir.HarnessID{ir.HarnessClaudeCode, ir.HarnessCodex, ir.HarnessOpenCode} {
		t.Run(string(harness), func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().String("host-version", "", "")
			cmd.Flags().String("host-executable", "", "")
			require.NoError(t, cmd.Flags().Set("host-version", "3.4.5"))

			version, err := resolveLifecycleHostVersion(context.Background(), cmd,
				lifecycleCoordinates{Harness: harness, HostVersion: "3.4.5"})

			require.NoError(t, err)
			require.Equal(t, "3.4.5", version)
		})
	}
}

func TestLifecycleQueriesExplicitExecutableBeforeDurableReceipt(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	executable := versionExecutable(t, "printf '2.1.299 (Claude Code)\\n'")
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	initializeLifecycleTestDatabase(t, dbPath)
	raw := readProductionClaudeFixture(t, "session_start_2_1_261.json", "SessionStart")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "hook", "lifecycle", "--harness", "claude-code",
		"--event", "SessionStart", "--host-executable", executable)
	command.Env = append(os.Environ(), "PASTURE_DB_PATH="+dbPath, "PASTURE_CAPTURE_DIR=", "PASTURE_ACTOR_ID=")
	command.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()

	require.NoError(t, err, stderr.String())
	require.Empty(t, stderr.String(), "version query must succeed before admission")
	require.Empty(t, stdout.String(), "version stdout is not a Claude directive")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	rows := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	require.Len(t, rows, 1)
	payload := decodeOccurrencePayload(t, rows[0].Payload)
	require.Equal(t, "2.1.299", payload.Envelope.HostVersion)
}

func TestLifecycleVersionSourceReachesDurableAndFaultRecords(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	path, _ := discoveryPathExecutable(t, "printf '2.1.299 (Claude Code)\\n'")
	transport := generatedClaudeLifecycleCommand(t, "SessionStart")
	valid := readProductionClaudeFixture(t, "session_start_2_1_261.json", "SessionStart")
	var incompatible map[string]any
	require.NoError(t, json.Unmarshal(valid, &incompatible))
	incompatible["session_id"] = true
	refused, err := json.Marshal(incompatible)
	require.NoError(t, err)
	cases := []struct {
		name   string
		source model.HostVersionSource
		raw    []byte
		valid  bool
	}{
		{name: "queried valid", source: model.HostVersionExecutableQuery, raw: valid, valid: true},
		{name: "queried refused", source: model.HostVersionExecutableQuery, raw: refused},
		{name: "supplied valid", source: model.HostVersionCallerSupplied, raw: valid, valid: true},
		{name: "supplied refused", source: model.HostVersionCallerSupplied, raw: refused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "pasture.db")
			initializeLifecycleTestDatabase(t, dbPath)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var command *exec.Cmd
			if tc.source == model.HostVersionExecutableQuery {
				command = exec.CommandContext(ctx, "sh", "-c", transport)
			} else {
				command = exec.CommandContext(ctx, binary, "hook", "lifecycle", "--harness", "claude-code",
					"--event", "SessionStart", "--host-version", "2.1.299")
			}
			command.Env = discoveryChildEnv(map[string]*string{"PASTURE_BIN": &binary, "PASTURE_DB_PATH": &dbPath,
				"PATH": &path, "CLAUDE_CODE_EXECPATH": nil, "PASTURE_CAPTURE_DIR": nil, "PASTURE_ACTOR_ID": nil, "PASTURE_HOOK_FAIL_CLOSED": nil})
			command.Stdin = bytes.NewReader(tc.raw)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr

			err := command.Run()

			require.NoError(t, err, stderr.String())
			require.Empty(t, stdout.String())
			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			defer tracker.Close()
			rows := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
			require.Len(t, rows, 1)
			occurrence := decodeOccurrencePayload(t, rows[0].Payload)
			require.Equal(t, "2.1.299", occurrence.Envelope.HostVersion)
			require.Equal(t, tc.source, occurrence.Envelope.HostVersionSource,
				"the receipt must retain the selected source for valid AND refused captures")
			require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
			reader, err := tasks.NewLifecycleReader(tracker)
			require.NoError(t, err)
			size, err := model.NewPageSize(1)
			require.NoError(t, err)
			page, err := reader.Records(context.Background(), model.OccurrenceQuery{
				Page: model.PageRequest{Size: size},
			})
			require.NoError(t, err)
			require.Len(t, page.Records(), 1)
			projected := page.Records()[0].Occurrence.Envelope
			require.Equal(t, "2.1.299", projected.HostVersion)
			require.Equal(t, tc.source, projected.HostVersionSource)
			faultPath := filepath.Join(filepath.Dir(dbPath), lifecycleFaultRecordFile)
			if tc.valid {
				require.Equal(t, model.CaptureValid, occurrence.Capture)
				require.Empty(t, stderr.String())
				_, err := os.Stat(faultPath)
				require.ErrorIs(t, err, os.ErrNotExist)
			} else {
				require.Equal(t, model.CaptureUnsupportedSchema, occurrence.Capture)
				require.NotEmpty(t, stderr.String())
				fault, err := os.ReadFile(faultPath)
				require.NoError(t, err)
				var record struct {
					HostVersion       string                  `json:"hostVersion"`
					HostVersionSource model.HostVersionSource `json:"hostVersionSource"`
					OutcomeClass      string                  `json:"outcomeClass"`
				}
				require.NoError(t, json.Unmarshal(fault, &record))
				require.Equal(t, "2.1.299", record.HostVersion)
				require.Equal(t, tc.source, record.HostVersionSource)
				require.Equal(t, "fault", record.OutcomeClass, "a schema refusal is not a governance Deny")
			}
		})
	}
}

func TestLifecycleFailedVersionQueryDoesNotAssertSource(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", "-c", generatedClaudeLifecycleCommand(t, "SessionStart"))
	path, _ := discoveryPathExecutable(t, "exit 17")
	command.Env = discoveryChildEnv(map[string]*string{"PASTURE_BIN": discoveryValue(lifecycleBinary(t)), "PASTURE_DB_PATH": &dbPath,
		"PATH": &path, "CLAUDE_CODE_EXECPATH": discoveryValue(""), "PASTURE_CAPTURE_DIR": nil, "PASTURE_HOOK_FAIL_CLOSED": nil})
	command.Stdin = strings.NewReader("{}")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()

	require.NoError(t, err, stderr.String())
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "exit status 17")
	fault, err := os.ReadFile(filepath.Join(filepath.Dir(dbPath), lifecycleFaultRecordFile))
	require.NoError(t, err)
	record := decodeJSONObject(t, fault)
	require.JSONEq(t, `""`, string(record["hostVersion"]))
	require.NotContains(t, record, "hostVersionSource", "a failed query does not establish a version source")
	_, err = os.Stat(dbPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestLifecycleFaultVersionSourceIsOptional(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		version string
		source  model.HostVersionSource
	}{
		{name: "legacy source unspecified", version: "2.1.261"},
		{name: "no observed version", source: model.HostVersionExecutableQuery},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fields := map[string]any{"hostVersion": tc.version, "cause": "original fault"}
			before, err := json.Marshal(fields)
			require.NoError(t, err)
			coords := lifecycleCoordinates{HostVersion: tc.version, HostVersionSource: tc.source}

			after, err := json.Marshal(withLifecycleHostVersionSource(coords, fields))

			require.NoError(t, err)
			require.Equal(t, before, after, "unspecified provenance must leave the existing fault fields byte-identical")
		})
	}
}
