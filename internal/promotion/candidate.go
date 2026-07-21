package promotion

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/internal/effects"
)

const (
	PastureRepository = "dayvidpham/pasture"
	AuraRepository    = "dayvidpham/aura-plugins"
)

// RepositorySnapshot is an immutable detached worktree at one exact commit.
type RepositorySnapshot struct {
	Repository effects.RepositoryID
	Commit     effects.CommitOID
	owner      effects.RepositoryID
	path       string
}

// Close removes the detached worktree and its temporary parent.
func (s RepositorySnapshot) Close() error {
	if s.path == "" || !s.owner.IsValid() {
		return nil
	}
	cmd := exec.Command("git", "worktree", "remove", "--force", s.path)
	cmd.Dir = s.owner.String()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("promotion.RepositorySnapshot.Close: could not remove immutable checkout %q after promotion: %w (%s); run git worktree prune in %q", s.path, err, strings.TrimSpace(string(out)), s.owner.String())
	}
	return os.RemoveAll(filepath.Dir(s.path))
}

// PrepareRepositorySnapshot verifies repository provenance and materializes the
// named full commit in a detached worktree. Live checkout files are never read by
// gates after this boundary.
func PrepareRepositorySnapshot(repository effects.RepositoryID, revision, remote, expectedRepository string) (RepositorySnapshot, error) {
	if !repository.IsValid() {
		return RepositorySnapshot{}, fault("candidate repository is invalid", "candidate preparation requires a concrete git repository", "promotion.PrepareRepositorySnapshot", "repository validation", "no immutable candidate can be prepared", "construct the repository from an absolute checkout path", nil)
	}
	commit, err := effects.NewCommitOID(revision)
	if err != nil {
		return RepositorySnapshot{}, fault("candidate revision is not a full immutable commit id", "candidate preparation accepts only exact commit object ids", "promotion.PrepareRepositorySnapshot", "revision validation", "no worktree or expensive gate is started", "pass the full lowercase commit sha", err)
	}
	if err := verifyRemoteRepository(repository, remote, expectedRepository); err != nil {
		return RepositorySnapshot{}, err
	}
	resolved, err := gitOutput(repository.String(), "rev-parse", "--verify", "--quiet", revision+"^{commit}")
	if err != nil || strings.TrimSpace(resolved) != revision {
		return RepositorySnapshot{}, fault("candidate commit "+revision+" is unavailable in "+repository.String(), "the named revision must resolve locally to the same exact commit before gates run", "promotion.PrepareRepositorySnapshot", "candidate resolution", "no worktree or expensive gate is started", "fetch the exact commit from the verified repository and retry", err)
	}
	parent, err := os.MkdirTemp("", "pasture-promotion-candidate-*")
	if err != nil {
		return RepositorySnapshot{}, fault("temporary candidate directory could not be created", "gates run in isolated detached worktrees", "promotion.PrepareRepositorySnapshot", "candidate materialization", "the promotion is aborted before gates or publication", "ensure the system temporary directory is writable", err)
	}
	path := filepath.Join(parent, "checkout")
	if _, err := gitOutput(repository.String(), "worktree", "add", "--detach", path, revision); err != nil {
		_ = os.RemoveAll(parent)
		return RepositorySnapshot{}, fault("immutable candidate checkout could not be created for "+revision, "the exact commit must be materialized independently of live checkout state", "promotion.PrepareRepositorySnapshot", "candidate materialization", "the promotion is aborted before gates or publication", "repair the git repository and run git worktree prune before retrying", err)
	}
	snapshotRepo, err := effects.NewRepositoryID(path)
	if err != nil {
		_, _ = gitOutput(repository.String(), "worktree", "remove", "--force", path)
		_ = os.RemoveAll(parent)
		return RepositorySnapshot{}, err
	}
	return RepositorySnapshot{Repository: snapshotRepo, Commit: commit, owner: repository, path: path}, nil
}

func verifyRemoteRepository(repository effects.RepositoryID, remote, expectedRepository string) error {
	if strings.TrimSpace(remote) == "" || strings.TrimSpace(remote) != remote {
		return fault("repository remote name is empty or padded", "provenance verification requires one configured remote", "promotion.verifyRemoteRepository", "repository provenance", "the candidate cannot be attributed to its canonical repository", "pass the configured remote name", nil)
	}
	for _, args := range [][]string{{"remote", "get-url", remote}, {"remote", "get-url", "--push", remote}} {
		url, err := gitOutput(repository.String(), args...)
		if err != nil || repositorySlug(url) != expectedRepository {
			return fault("git remote "+remote+" in "+repository.String()+" is not the canonical "+expectedRepository+" repository", "both fetch and push provenance must identify the repository whose release policy is being enforced", "promotion.verifyRemoteRepository", "repository provenance", "the promotion is aborted before candidate materialization, gates, or publication", "configure "+remote+" to https://github.com/"+expectedRepository+".git or git@github.com:"+expectedRepository+".git", err)
		}
	}
	return nil
}

func repositorySlug(value string) string {
	value = strings.TrimSpace(strings.TrimSuffix(value, ".git"))
	value = strings.TrimPrefix(value, "ssh://git@github.com/")
	value = strings.TrimPrefix(value, "git@github.com:")
	value = strings.TrimPrefix(value, "https://github.com/")
	return value
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s in %s failed: %w (%s)", strings.Join(args, " "), dir, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
