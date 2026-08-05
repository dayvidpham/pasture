package main_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_EpochInventoryAndHiddenAdapter(t *testing.T) {
	t.Parallel()

	root := runCLI(t, "--help")
	if root.exitCode != 0 {
		t.Fatalf("root help exit %d: %s", root.exitCode, root.stderr)
	}
	if got, want := commandNames(root.stdout), []string{"epoch", "hook", "init", "install", "migrate", "status", "task"}; !sameStrings(got, want) {
		t.Fatalf("root commands = %v, want %v\n%s", got, want, root.stdout)
	}
	if strings.Contains(root.stdout, "__adapter") {
		t.Fatalf("hidden adapter appeared in ordinary root help:\n%s", root.stdout)
	}

	epoch := runCLI(t, "epoch", "--help")
	if epoch.exitCode != 0 {
		t.Fatalf("epoch help exit %d: %s", epoch.exitCode, epoch.stderr)
	}
	if got, want := commandNames(epoch.stdout), []string{"implementation", "integration", "interaction-mode", "land", "plan", "review", "slice"}; !sameStrings(got, want) {
		t.Fatalf("epoch commands = %v, want %v\n%s", got, want, epoch.stdout)
	}

	task := runCLI(t, "task", "--help")
	if task.exitCode != 0 {
		t.Fatalf("task help exit %d: %s", task.exitCode, task.stderr)
	}
	if got, want := commandNames(task.stdout), []string{"assignment", "close", "comment", "create", "relation", "show", "timeline", "update"}; !sameStrings(got, want) {
		t.Fatalf("task commands = %v, want %v\n%s", got, want, task.stdout)
	}

	adapter := runCLI(t, "__adapter", "--help")
	if adapter.exitCode != 0 {
		t.Fatalf("hidden adapter was not registered by the production binary: exit=%d stderr=%s", adapter.exitCode, adapter.stderr)
	}
	if strings.Contains(adapter.stdout, "invoke") {
		t.Fatalf("hidden adapter child appeared in adapter help:\n%s", adapter.stdout)
	}
}

func TestCLI_TaskAssignmentTransferRejectsInvalidInputBeforeStoreOpen(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "not-opened.db")
	out := runCLI(t,
		"--db", db,
		"task", "assignment", "transfer", "not-a-task-id",
		"--slot", "owner-responsibility",
		"--assignment", "assignment/cli/next",
		"--actor", "cli--01960000-0000-7000-8000-000000000001",
		"--occupant", "cli--01960000-0000-7000-8000-000000000002",
	)
	if out.exitCode != 1 {
		t.Fatalf("exit=%d, want validation failure; stdout=%s stderr=%s", out.exitCode, out.stdout, out.stderr)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("invalid input opened or created %q: stat error=%v", db, err)
	}
	if !strings.Contains(out.stderr, "Pasture store was not opened") {
		t.Fatalf("error did not explain zero-write preflight behavior:\n%s", out.stderr)
	}
}

func TestCLI_EpochStructuredInputRejectsBeforeStoreOpen(t *testing.T) {
	t.Parallel()

	const (
		epoch = "cli--01960000-0000-7000-8000-000000000001"
		round = "cli--01960000-0000-7000-8000-000000000002"
	)
	cases := []struct {
		name  string
		input string
	}{
		{"unknown field", `{"verdict":"accept","feedback":[],"unexpected":true}`},
		{"trailing JSON", `{"verdict":"accept","feedback":[]}{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := filepath.Join(t.TempDir(), "not-opened.db")
			out := runCLIInput(t, tc.input,
				"--db", db,
				"epoch", "review", "submit",
				"--epoch", epoch,
				"--round", round,
				"--axis", "correctness",
				"--assignment", "assignment/cli/reviewer",
				"--input", "-",
			)
			if out.exitCode != 1 {
				t.Fatalf("exit=%d, want validation failure; stdout=%s stderr=%s", out.exitCode, out.stdout, out.stderr)
			}
			if _, err := os.Stat(db); !os.IsNotExist(err) {
				t.Fatalf("invalid input opened or created %q: stat error=%v", db, err)
			}
			if !strings.Contains(out.stderr, "Pasture store was not opened") {
				t.Fatalf("error did not explain zero-write preflight behavior:\n%s", out.stderr)
			}
		})
	}
}

func runCLIInput(t *testing.T, input string, args ...string) runOutcome {
	t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return runOutcome{stdout: stdout.String(), stderr: stderr.String()}
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return runOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: exit.ExitCode()}
	}
	t.Fatalf("unexpected command execution error: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	return runOutcome{}
}

func commandNames(help string) []string {
	start := strings.Index(help, "Available Commands:\n")
	if start < 0 {
		return nil
	}
	section := help[start+len("Available Commands:\n"):]
	if end := strings.Index(section, "\nFlags:"); end >= 0 {
		section = section[:end]
	}
	var names []string
	for _, line := range strings.Split(section, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "completion" || fields[0] == "help" {
			continue
		}
		names = append(names, fields[0])
	}
	return names
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
