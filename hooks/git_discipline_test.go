package hooks

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	exitAllow = 0
	exitBlock = 2
)

// gitDisciplineScript returns the path to the hook under test, resolved
// relative to this test file (robust to the caller's working directory).
func gitDisciplineScript(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file via runtime.Caller")
	}
	return filepath.Join(filepath.Dir(thisFile), "scripts", "git-discipline.sh")
}

// runHook pipes a PreToolUse event to the hook and returns its exit code.
func runHook(t *testing.T, role, bypass, stdin string) int {
	t.Helper()
	cmd := exec.Command("bash", gitDisciplineScript(t))
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = os.Environ()
	if role != "" {
		cmd.Env = append(cmd.Env, "PASTURE_ROLE="+role)
	}
	if bypass != "" {
		cmd.Env = append(cmd.Env, "BYPASS_GIT_DISCIPLINE="+bypass)
	}
	err := cmd.Run()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	t.Fatalf("hook exec failed (is bash/jq on PATH?): %v", err)
	return -1
}

// bashEvent builds a PreToolUse JSON event for a Bash tool invocation.
func bashEvent(t *testing.T, command string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": command},
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return string(b)
}

// TestGitDiscipline_BlocksDestructiveForWorker covers the destructive commands
// the hook must block for a worker — including the accidental-variant forms
// (global-option prefix, unbundled/any-order flags) that a naive adjacency
// regex would miss.
func TestGitDiscipline_BlocksDestructiveForWorker(t *testing.T) {
	t.Parallel()
	cmds := []string{
		"git reset --hard HEAD~1",
		"git -C . reset --hard",           // global -C option prefix
		"git --git-dir=.git reset --hard", // global --git-dir
		"git -c user.name=x reset --hard", // global -c
		"git clean -f -d",                 // unbundled flags
		"git clean -d -f",                 // any order
		"git clean -fdx",                  // bundled
		"git branch -d -D feature",        // force-delete anywhere in flags
		"git restore --worktree --source=HEAD path",
		"git checkout HEAD -- file.txt",
		"git stash pop",
		"git stash apply",
		"git rebase --abort",
		"true && git reset --hard", // pipeline
	}
	for _, c := range cmds {
		c := c
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			if rc := runHook(t, "worker", "", bashEvent(t, c)); rc != exitBlock {
				t.Errorf("command %q: got exit %d, want %d (block)", c, rc, exitBlock)
			}
		})
	}
}

// TestGitDiscipline_AllowsSafeForWorker covers benign commands that must NOT be
// blocked, including near-misses of the destructive patterns.
func TestGitDiscipline_AllowsSafeForWorker(t *testing.T) {
	t.Parallel()
	cmds := []string{
		"git status",
		"git restore --staged file",
		"git clean -n",         // dry-run, no force
		"git branch -d merged", // lowercase = safe delete of a merged branch
		"git add -A",
		"git commit -m msg",
		"echo 'git reset --hard'", // quoted substring, not an invocation
	}
	for _, c := range cmds {
		c := c
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			if rc := runHook(t, "worker", "", bashEvent(t, c)); rc != exitAllow {
				t.Errorf("command %q: got exit %d, want %d (allow)", c, rc, exitAllow)
			}
		})
	}
}

// TestGitDiscipline_KnownLimitations pins the threat-model boundary: the hook is
// a backstop against ACCIDENTAL destructive commands, not a sandbox. These
// deliberate-evasion forms are NOT detected by design. If a future change starts
// catching one, update the hook header and move the case into the block test.
func TestGitDiscipline_KnownLimitations(t *testing.T) {
	t.Parallel()
	cmds := []string{
		`eval "git reset --hard"`,
		`g\it reset --hard`,
		`git${IFS}reset${IFS}--hard`,
		`$(git reset --hard)`,
	}
	for _, c := range cmds {
		c := c
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			if rc := runHook(t, "worker", "", bashEvent(t, c)); rc != exitAllow {
				t.Errorf("documented limitation %q: got exit %d, want %d (not detected by design)", c, rc, exitAllow)
			}
		})
	}
}

// TestGitDiscipline_Gating covers the no-op paths: only a worker running a Bash
// command is subject to enforcement; everything else (and transient failures)
// must fail open.
func TestGitDiscipline_Gating(t *testing.T) {
	t.Parallel()
	destructive := bashEvent(t, "git reset --hard")
	cases := []struct {
		name         string
		role, bypass string
		stdin        string
		want         int
	}{
		{"non-worker role", "supervisor", "", destructive, exitAllow},
		{"unset role", "", "", destructive, exitAllow},
		{"bypass flag set", "worker", "1", destructive, exitAllow},
		{"non-Bash tool", "worker", "", `{"tool_name":"Edit","tool_input":{}}`, exitAllow},
		{"malformed json (fail open)", "worker", "", "not json at all", exitAllow},
		{"empty command", "worker", "", `{"tool_name":"Bash","tool_input":{"command":""}}`, exitAllow},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if rc := runHook(t, tc.role, tc.bypass, tc.stdin); rc != tc.want {
				t.Errorf("%s: got exit %d, want %d", tc.name, rc, tc.want)
			}
		})
	}
}
