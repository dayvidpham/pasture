package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// marketplaceJSON is a minimal aura marketplace matching the projected split
// plugins at 0.0.4 (the embedded plugin.json versions).
const marketplaceJSON = `{
  "name": "aura-plugins",
  "metadata": {"version": "0.10.2"},
  "plugins": [
    {"name": "pasture-skills", "source": {"source": "github", "repo": "dayvidpham/pasture"}, "version": "0.0.4"},
    {"name": "pasture-agents", "source": {"source": "github", "repo": "dayvidpham/pasture"}, "version": "0.0.4"},
    {"name": "pasture-hooks",  "source": {"source": "github", "repo": "dayvidpham/pasture"}, "version": "0.0.4"}
  ]
}`

func TestPromoteStableCLIEndToEnd(t *testing.T) {
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "--initial-branch=main", ".")

	work := t.TempDir()
	runGit(t, work, "init", "--initial-branch=main", ".")
	runGit(t, work, "config", "commit.gpgsign", "false")
	runGit(t, work, "remote", "add", "origin", bare)

	// A real marketplace.json matching the projection.
	if err := os.MkdirAll(filepath.Join(work, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".claude-plugin", "marketplace.json"), []byte(marketplaceJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "seed")
	head := runGit(t, work, "rev-parse", "HEAD")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"promote-pasture-stable",
		"--pasture-revision", head,
		"--aura-revision", head,
		"--expected-old", "absent",
		"--remote", "origin",
		"--pasture-repo", work,
		"--aura-repo", work,
		"--skip-command-gates",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("promote-pasture-stable: %v\noutput:\n%s", err, out.String())
	}

	// Channel created at the exact commit.
	got := runGit(t, bare, "rev-parse", "refs/heads/pasture-stable")
	if got != head {
		t.Fatalf("pasture-stable = %q, want %q", got, head)
	}
	text := out.String()
	for _, want := range []string{"promoted refs/heads/pasture-stable", "outcome: pushed", "pasture-skills 0.0.4", "claude-code/skills"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q\n%s", want, text)
		}
	}
}

func TestPromoteStableCLIFailsOnMarketplaceMismatch(t *testing.T) {
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "--initial-branch=main", ".")

	work := t.TempDir()
	runGit(t, work, "init", "--initial-branch=main", ".")
	runGit(t, work, "config", "commit.gpgsign", "false")
	runGit(t, work, "remote", "add", "origin", bare)

	// Marketplace advertises a wrong version for pasture-skills.
	bad := strings.Replace(marketplaceJSON, `"name": "pasture-skills", "source": {"source": "github", "repo": "dayvidpham/pasture"}, "version": "0.0.4"`,
		`"name": "pasture-skills", "source": {"source": "github", "repo": "dayvidpham/pasture"}, "version": "9.9.9"`, 1)
	if err := os.MkdirAll(filepath.Join(work, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".claude-plugin", "marketplace.json"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "seed")
	head := runGit(t, work, "rev-parse", "HEAD")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"promote-pasture-stable",
		"--pasture-revision", head,
		"--aura-revision", head,
		"--expected-old", "absent",
		"--remote", "origin",
		"--pasture-repo", work,
		"--aura-repo", work,
		"--skip-command-gates",
	})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected marketplace mismatch to fail the promotion\n%s", out.String())
	}
	// The ref must NOT have been created — the gate aborts before any push.
	if v := runGitAllow(t, bare, "rev-parse", "refs/heads/pasture-stable"); v != "" {
		t.Fatalf("pasture-stable was created despite a failing gate: %q", v)
	}
}

// runGitAllow runs git and returns trimmed output, or "" on error (for probing a
// possibly-absent ref).
func runGitAllow(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
