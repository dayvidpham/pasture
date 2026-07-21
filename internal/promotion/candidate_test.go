package promotion_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/promotion"
)

func candidateGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPrepareRepositorySnapshotUsesNamedCommitNotLiveFiles(t *testing.T) {
	repoPath := t.TempDir()
	candidateGit(t, repoPath, "init", "--initial-branch=main", ".")
	candidateGit(t, repoPath, "config", "commit.gpgsign", "false")
	candidateGit(t, repoPath, "remote", "add", "origin", "https://github.com/dayvidpham/pasture.git")
	file := filepath.Join(repoPath, "evidence.txt")
	if err := os.WriteFile(file, []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidateGit(t, repoPath, "add", "evidence.txt")
	candidateGit(t, repoPath, "commit", "-m", "candidate")
	commit := candidateGit(t, repoPath, "rev-parse", "HEAD")
	if err := os.WriteFile(file, []byte("dirty live checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, _ := effects.NewRepositoryID(repoPath)
	snapshot, err := promotion.PrepareRepositorySnapshot(repo, commit, "origin", promotion.PastureRepository)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	t.Cleanup(func() {
		if err := snapshot.Close(); err != nil {
			t.Errorf("close snapshot: %v", err)
		}
	})
	data, err := os.ReadFile(filepath.Join(snapshot.Repository.String(), "evidence.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "committed\n" {
		t.Fatalf("snapshot read live content %q", data)
	}
}

func TestPrepareRepositorySnapshotRejectsWrongProvenanceBeforeMaterialization(t *testing.T) {
	repoPath := t.TempDir()
	candidateGit(t, repoPath, "init", "--initial-branch=main", ".")
	candidateGit(t, repoPath, "remote", "add", "origin", "https://github.com/example/unrelated.git")
	repo, _ := effects.NewRepositoryID(repoPath)
	_, err := promotion.PrepareRepositorySnapshot(repo, testPastureCommit, "origin", promotion.PastureRepository)
	if err == nil || !strings.Contains(err.Error(), "not the canonical") {
		t.Fatalf("wrong provenance error = %v", err)
	}
}
