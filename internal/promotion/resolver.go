package promotion

import (
	"strings"

	"github.com/dayvidpham/pasture/internal/effects"
)

// RevisionResolver resolves a caller-supplied revision (a sha, tag, or ref) to
// the exact commit object and its tree, so the guarded push can verify the exact
// local object before touching the remote. It is an injected seam: production
// wires a git-backed resolver; tests wire a fake.
type RevisionResolver interface {
	// ResolveCommit resolves revision to a full commit object id in repository.
	ResolveCommit(repository effects.RepositoryID, revision string) (effects.CommitOID, error)
	// ResolveTree returns the tree object id the commit carries in repository.
	ResolveTree(repository effects.RepositoryID, commit effects.CommitOID) (effects.TreeDigest, error)
}

// GitRevisionResolver is the production RevisionResolver backed by a git
// executable. It reuses the same injected resolver/runner seams as the effects
// git pusher so no code path assumes a fixed git location or shells out directly.
type GitRevisionResolver struct {
	resolve effects.ExecutableResolver
	run     effects.CommandRunner
}

// NewGitRevisionResolver wires a git-backed revision resolver. Pass exec.LookPath
// as resolve and effects.DefaultCommandRunner as run in production.
func NewGitRevisionResolver(resolve effects.ExecutableResolver, run effects.CommandRunner) (GitRevisionResolver, error) {
	if resolve == nil || run == nil {
		return GitRevisionResolver{}, fault(
			"git revision resolver is missing an executable resolver or command runner",
			"the git binary must be resolved and executed through injected seams",
			"promotion.NewGitRevisionResolver", "revision resolver wiring",
			"the resolver cannot dispatch git to resolve the revision",
			"pass a non-nil resolver (exec.LookPath) and runner (effects.DefaultCommandRunner)", nil,
		)
	}
	return GitRevisionResolver{resolve: resolve, run: run}, nil
}

func (g GitRevisionResolver) git(repository effects.RepositoryID, args ...string) (string, error) {
	path, err := g.resolve("git")
	if err != nil {
		return "", fault(
			"git executable could not be resolved",
			"revision resolution dispatches a resolved git binary, never a shell",
			"promotion.GitRevisionResolver", "git dispatch",
			"the revision cannot be resolved to an exact object",
			"ensure git is installed and on PATH", err,
		)
	}
	return g.run(repository.String(), path, args...)
}

// ResolveCommit resolves revision to a full commit object id via
// `git rev-parse <revision>^{commit}`.
func (g GitRevisionResolver) ResolveCommit(repository effects.RepositoryID, revision string) (effects.CommitOID, error) {
	if !repository.IsValid() {
		return effects.CommitOID{}, fault(
			"repository is zero or invalid",
			"a revision is resolved inside a concrete working repository",
			"promotion.GitRevisionResolver.ResolveCommit", "commit resolution",
			"no repository is available to resolve the revision",
			"construct the repository with effects.NewRepositoryID", nil,
		)
	}
	out, err := g.git(repository, "rev-parse", "--verify", "--quiet", revision+"^{commit}")
	if err != nil {
		return effects.CommitOID{}, fault(
			"pasture revision "+revision+" could not be resolved to a commit",
			"the named revision does not exist in the repository or does not resolve to a commit object",
			"promotion.GitRevisionResolver.ResolveCommit", "commit resolution",
			"the promotion cannot publish a revision that is not present locally",
			"fetch or check out the reviewed revision, then re-run the promotion", err,
		)
	}
	commit, err := effects.NewCommitOID(strings.ToLower(strings.TrimSpace(out)))
	if err != nil {
		return effects.CommitOID{}, fault(
			"resolved commit id for "+revision+" is not a full object id",
			"git returned an unparseable object id for the revision",
			"promotion.GitRevisionResolver.ResolveCommit", "commit resolution",
			"the exact commit to publish cannot be verified",
			"ensure the repository reports full object ids (default git configuration)", err,
		)
	}
	return commit, nil
}

// ResolveTree returns the commit's tree via `git rev-parse <commit>^{tree}`.
func (g GitRevisionResolver) ResolveTree(repository effects.RepositoryID, commit effects.CommitOID) (effects.TreeDigest, error) {
	if !repository.IsValid() {
		return effects.TreeDigest{}, fault(
			"repository is zero or invalid",
			"a commit's tree is resolved inside a concrete working repository",
			"promotion.GitRevisionResolver.ResolveTree", "tree resolution",
			"no repository is available to resolve the tree",
			"construct the repository with effects.NewRepositoryID", nil,
		)
	}
	if !commit.IsValid() {
		return effects.TreeDigest{}, fault(
			"commit is zero or invalid",
			"a tree is resolved for an exact commit object",
			"promotion.GitRevisionResolver.ResolveTree", "tree resolution",
			"the tree to verify cannot be identified",
			"resolve the commit first with ResolveCommit", nil,
		)
	}
	out, err := g.git(repository, "rev-parse", "--verify", "--quiet", commit.String()+"^{tree}")
	if err != nil {
		return effects.TreeDigest{}, fault(
			"tree for commit "+commit.String()+" could not be resolved",
			"the commit object is missing or unreadable in the repository",
			"promotion.GitRevisionResolver.ResolveTree", "tree resolution",
			"the guarded push cannot verify the exact local object before publishing",
			"ensure the commit and its tree exist locally, then re-run the promotion", err,
		)
	}
	tree, err := effects.NewTreeDigest(strings.ToLower(strings.TrimSpace(out)))
	if err != nil {
		return effects.TreeDigest{}, fault(
			"resolved tree id for commit "+commit.String()+" is not a full object id",
			"git returned an unparseable tree object id",
			"promotion.GitRevisionResolver.ResolveTree", "tree resolution",
			"the local object cannot be verified before landing",
			"ensure the repository reports full object ids (default git configuration)", err,
		)
	}
	return tree, nil
}
