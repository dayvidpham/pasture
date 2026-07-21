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
	cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func candidateRepository(t *testing.T, remoteURL string, files map[string]string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main", ".")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	runGit(t, dir, "remote", "add", "origin", remoteURL)
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "candidate")
	return dir, runGit(t, dir, "rev-parse", "HEAD")
}

func pastureCandidateFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{"go.mod": "module example.invalid/candidate\n\ngo 1.25\n"}
	for _, name := range []string{"pasture-skills", "pasture-agents", "pasture-hooks"} {
		path := filepath.Join("..", "..", "internal", "target", "claudecode", "assets", name, ".claude-plugin", "plugin.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read candidate manifest %s: %v", path, err)
		}
		files[filepath.Join("internal", "target", "claudecode", "assets", name, ".claude-plugin", "plugin.json")] = string(data)
	}
	return files
}

func executePromotion(t *testing.T, pastureRepo, pastureRevision, auraRepo, auraRevision string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"promote-pasture-stable", "--pasture-revision", pastureRevision, "--aura-revision", auraRevision, "--expected-old", "absent", "--remote", "origin", "--pasture-repo", pastureRepo, "--aura-repo", auraRepo})
	err := cmd.Execute()
	return out.String(), err
}

func TestPromoteStableCLIConsumesDistinctRepositoryRevisions(t *testing.T) {
	pastureRepo, pastureCommit := candidateRepository(t, "https://github.com/dayvidpham/pasture.git", pastureCandidateFiles(t))
	auraRepo, auraCommit := candidateRepository(t, "https://github.com/dayvidpham/aura-plugins.git", map[string]string{".claude-plugin/marketplace.json": `{"name":"aura-plugins","plugins":[]}`})
	if pastureRepo == auraRepo || pastureCommit == auraCommit {
		t.Fatal("fixture must use distinct repositories and commits")
	}

	// A valid Pasture commit is not valid Aura evidence merely because it exists
	// in the other repository. The command must consume AuraRevision itself.
	out, err := executePromotion(t, pastureRepo, pastureCommit, auraRepo, pastureCommit)
	if err == nil {
		t.Fatalf("expected foreign Aura revision to fail\n%s", out)
	}
	if !strings.Contains(err.Error(), "candidate commit "+pastureCommit+" is unavailable") {
		t.Fatalf("failure does not identify the unavailable Aura candidate: %v", err)
	}
	if got := runGitAllow(t, auraRepo, "show-ref", "--verify", "refs/heads/pasture-stable"); got != "" {
		t.Fatalf("failure created pasture-stable: %q", got)
	}
	_ = auraCommit
}

func TestPromoteStableCLIPreservesRefOnExactAuraMarketplaceFailure(t *testing.T) {
	pastureRepo, pastureCommit := candidateRepository(t, "https://github.com/dayvidpham/pasture.git", pastureCandidateFiles(t))
	auraRepo, auraCommit := candidateRepository(t, "https://github.com/dayvidpham/aura-plugins.git", map[string]string{".claude-plugin/marketplace.json": `{"name":"aura-plugins","plugins":[]}`})
	runGit(t, pastureRepo, "branch", "pasture-stable", pastureCommit)

	out, err := executePromotion(t, pastureRepo, pastureCommit, auraRepo, auraCommit)
	if err == nil {
		t.Fatalf("expected exact Aura marketplace mismatch to fail\n%s", out)
	}
	if !strings.Contains(err.Error(), "missing projected plugin") {
		t.Fatalf("failure does not come from the immutable Aura marketplace gate: %v", err)
	}
	if got := runGit(t, pastureRepo, "rev-parse", "refs/heads/pasture-stable"); got != pastureCommit {
		t.Fatalf("failing gate changed pasture-stable to %q, want %q", got, pastureCommit)
	}
}

func TestPromoteStableCLIHasNoPublicGateBypass(t *testing.T) {
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"promote-pasture-stable", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	for _, forbidden := range []string{"skip-command-gates", "--marketplace", "--source-repo", "--marketplace-name"} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatalf("public help exposes bypass or provenance override %q\n%s", forbidden, out.String())
		}
	}
}

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
