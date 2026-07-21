package promotion

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/internal/effects"
)

const (
	PastureRepository = "dayvidpham/pasture"
	AuraRepository    = "dayvidpham/aura-plugins"
)

// repositorySnapshot is an immutable detached worktree at one exact commit.
// It is an internal transition state and cannot be supplied to publication.
type repositorySnapshot struct {
	repository effects.RepositoryID
	commit     effects.CommitOID
	tree       effects.TreeDigest
	owner      effects.RepositoryID
	path       string
	resolve    effects.ExecutableResolver
	run        effects.CommandRunner
	fetchURL   string
	pushURL    string
}

// close removes the detached worktree and its temporary parent.
func (s repositorySnapshot) close() error {
	if s.path == "" || !s.owner.IsValid() {
		return nil
	}
	if _, err := runGit(s.resolve, s.run, s.owner.String(), "worktree", "remove", "--force", s.path); err != nil {
		return fault("immutable candidate checkout could not be removed at "+s.path, "candidate cleanup must complete before publication so registered worktrees are never leaked", "promotion.repositorySnapshot.close", "candidate cleanup", "the pasture-stable ref remains unchanged and publication does not begin", "repair the worktree registration in "+s.owner.String()+", run git worktree prune, and retry", err)
	}
	if err := os.RemoveAll(filepath.Dir(s.path)); err != nil {
		return fault("immutable candidate temporary directory could not be removed at "+filepath.Dir(s.path), "candidate cleanup must remove both the registered worktree and its temporary parent", "promotion.repositorySnapshot.close", "candidate cleanup", "the pasture-stable ref remains unchanged and publication does not begin", "remove the temporary directory and retry the promotion", err)
	}
	return nil
}

func prepareRepositorySnapshot(repository effects.RepositoryID, revision, remote, expectedRepository string, resolve effects.ExecutableResolver, run effects.CommandRunner) (repositorySnapshot, error) {
	if !repository.IsValid() {
		return repositorySnapshot{}, fault("candidate repository is invalid", "candidate preparation requires a concrete git repository", "promotion.prepareRepositorySnapshot", "repository validation", "no immutable candidate can be prepared", "construct the repository from an absolute checkout path", nil)
	}
	commit, err := effects.NewCommitOID(revision)
	if err != nil {
		return repositorySnapshot{}, fault("candidate revision is not a full immutable commit id", "candidate preparation accepts only exact commit object ids", "promotion.prepareRepositorySnapshot", "revision validation", "no worktree or expensive gate is started", "pass the full lowercase commit sha", err)
	}
	if resolve == nil || run == nil {
		return repositorySnapshot{}, fault("candidate runtime is missing an executable resolver or command runner", "candidate preparation and cleanup require one injected process boundary", "promotion.prepareRepositorySnapshot", "candidate wiring", "no candidate is materialized", "construct the coordinator with non-nil production dependencies", nil)
	}
	fetchURL, pushURL, err := verifyRemoteRepository(repository, remote, expectedRepository, resolve, run)
	if err != nil {
		return repositorySnapshot{}, err
	}
	resolved, err := runGit(resolve, run, repository.String(), "rev-parse", "--verify", "--quiet", revision+"^{commit}")
	if err != nil || strings.TrimSpace(resolved) != revision {
		return repositorySnapshot{}, fault("candidate commit "+revision+" is unavailable in "+repository.String(), "the named revision must resolve locally to the same exact commit before gates run", "promotion.prepareRepositorySnapshot", "candidate resolution", "no worktree or expensive gate is started", "fetch the exact commit from the verified repository and retry", err)
	}
	treeValue, err := runGit(resolve, run, repository.String(), "rev-parse", "--verify", "--quiet", revision+"^{tree}")
	if err != nil {
		return repositorySnapshot{}, fault("candidate tree for "+revision+" is unavailable in "+repository.String(), "guarded publication binds the exact commit and tree before gates run", "promotion.prepareRepositorySnapshot", "candidate resolution", "no worktree or expensive gate is started", "fetch the complete commit and tree, then retry", err)
	}
	tree, err := effects.NewTreeDigest(strings.TrimSpace(treeValue))
	if err != nil {
		return repositorySnapshot{}, fault("candidate tree for "+revision+" is not a full object id", "guarded publication requires an exact tree digest", "promotion.prepareRepositorySnapshot", "candidate resolution", "no worktree or expensive gate is started", "repair the repository object database and retry", err)
	}
	parent, err := os.MkdirTemp("", "pasture-promotion-candidate-*")
	if err != nil {
		return repositorySnapshot{}, fault("temporary candidate directory could not be created", "gates run in isolated detached worktrees", "promotion.prepareRepositorySnapshot", "candidate materialization", "the promotion is aborted before gates or publication", "ensure the system temporary directory is writable", err)
	}
	path := filepath.Join(parent, "checkout")
	if _, err := runGit(resolve, run, repository.String(), "worktree", "add", "--detach", path, revision); err != nil {
		_ = os.RemoveAll(parent)
		return repositorySnapshot{}, fault("immutable candidate checkout could not be created for "+revision, "the exact commit must be materialized independently of live checkout state", "promotion.prepareRepositorySnapshot", "candidate materialization", "the promotion is aborted before gates or publication", "repair the git repository and run git worktree prune before retrying", err)
	}
	snapshotRepo, err := effects.NewRepositoryID(path)
	if err != nil {
		_, _ = runGit(resolve, run, repository.String(), "worktree", "remove", "--force", path)
		_ = os.RemoveAll(parent)
		return repositorySnapshot{}, err
	}
	return repositorySnapshot{repository: snapshotRepo, commit: commit, tree: tree, owner: repository, path: path, resolve: resolve, run: run, fetchURL: fetchURL, pushURL: pushURL}, nil
}

func verifyRemoteRepository(repository effects.RepositoryID, remote, expectedRepository string, resolve effects.ExecutableResolver, run effects.CommandRunner) (string, string, error) {
	if strings.TrimSpace(remote) == "" || strings.TrimSpace(remote) != remote {
		return "", "", fault("repository remote name is empty or padded", "provenance verification requires one configured remote", "promotion.verifyRemoteRepository", "repository provenance", "the candidate cannot be attributed to its canonical repository", "pass the configured remote name", nil)
	}
	urls := make([]string, 0, 2)
	for _, args := range [][]string{{"remote", "get-url", remote}, {"remote", "get-url", "--push", remote}} {
		url, err := runGit(resolve, run, repository.String(), args...)
		if err != nil || repositorySlug(url) != expectedRepository {
			return "", "", fault("git remote "+remote+" in "+repository.String()+" is not the canonical "+expectedRepository+" repository", "both fetch and push provenance must identify the repository whose release policy is being enforced", "promotion.verifyRemoteRepository", "repository provenance", "the promotion is aborted before candidate materialization, gates, or publication", "configure "+remote+" to https://github.com/"+expectedRepository+".git or git@github.com:"+expectedRepository+".git", err)
		}
		urls = append(urls, strings.TrimSpace(url))
	}
	return urls[0], urls[1], nil
}

func repositorySlug(value string) string {
	value = strings.TrimSpace(strings.TrimSuffix(value, ".git"))
	value = strings.TrimPrefix(value, "ssh://git@github.com/")
	value = strings.TrimPrefix(value, "git@github.com:")
	value = strings.TrimPrefix(value, "https://github.com/")
	return value
}

func runGit(resolve effects.ExecutableResolver, run effects.CommandRunner, dir string, args ...string) (string, error) {
	git, err := resolve("git")
	if err != nil {
		return "", fmt.Errorf("resolve git for git %s in %s: %w", strings.Join(args, " "), dir, err)
	}
	out, err := run(dir, git, args...)
	if err != nil {
		return "", fmt.Errorf("git %s in %s failed: %w", strings.Join(args, " "), dir, err)
	}
	return strings.TrimSpace(out), nil
}
