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

func TestGeneratedClaudeCommandRetainsObservedVersionAndCaptureName(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	executable := versionExecutable(t, "printf '2.1.299 (Claude Code)\\n'")
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	initializeLifecycleTestDatabase(t, dbPath)
	captureDir := t.TempDir()
	raw := readProductionClaudeFixture(t, "session_start_2_1_261.json", "SessionStart")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", "-c", generatedClaudeLifecycleCommand(t, "SessionStart"))
	command.Env = append(os.Environ(), "PASTURE_BIN="+binary, "PASTURE_DB_PATH="+dbPath,
		"CLAUDE_CODE_EXECPATH="+executable, "CLAUDE_CODE_VERSION=not-a-version",
		"PASTURE_CAPTURE_DIR="+captureDir, "PASTURE_ACTOR_ID=")
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
		{name: "missing executable", args: []string{"--host-executable", filepath.Join(t.TempDir(), "missing")}, want: "start explicit Claude executable"},
		{name: "relative", args: []string{"--host-executable", "claude"}, want: "complete absolute path"},
		{name: "not executable", args: []string{"--host-executable", nonExecutable}, want: "start explicit Claude executable"},
		{name: "wrong harness", harness: "codex", args: []string{"--host-executable", good}, want: "only for --harness claude-code"},
		{name: "conflicting sources", args: []string{"--host-executable", good, "--host-version", "2.1.261"}, want: "mutually exclusive"},
		{name: "empty explicit conflict", args: []string{"--host-executable", good, "--host-version", ""}, want: "mutually exclusive"},
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
	command.Env = append(os.Environ(), "PASTURE_BIN="+lifecycleBinary(t), "PASTURE_DB_PATH="+dbPath,
		"CLAUDE_CODE_EXECPATH=", "CLAUDE_CODE_VERSION=2.1.299", "PASTURE_CAPTURE_DIR=")
	command.Stdin = strings.NewReader("{}")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()

	require.NoError(t, err, stderr.String())
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "supplied empty")
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
