package effects

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// RepositoryID is the opaque identity of one git repository an effect acts on.
type RepositoryID struct {
	value       string
	constructed bool
}

// NewRepositoryID validates a repository identity.
func NewRepositoryID(value string) (RepositoryID, error) {
	if !utf8.ValidString(value) || value == "" || strings.TrimSpace(value) != value {
		return RepositoryID{}, effectError(
			"repository identity is empty, padded, or not valid UTF-8",
			"a repository identity requires one exact non-empty UTF-8 spelling",
			"NewRepositoryID", "git operand validation",
			"the git effect cannot name its repository",
			"supply a non-empty UTF-8 repository identity without surrounding whitespace", nil,
		)
	}
	if r, ok := containsControl(value); ok {
		return RepositoryID{}, effectError(
			fmt.Sprintf("repository identity contains control character U+%04X", r),
			"control characters are unsafe in a portable repository identity",
			"NewRepositoryID", "git operand validation",
			"the repository identity cannot be represented safely",
			"remove control characters from the repository identity", nil,
		)
	}
	return RepositoryID{value: value, constructed: true}, nil
}

func (r RepositoryID) String() string { return r.value }
func (r RepositoryID) IsValid() bool  { return r.constructed && r.value != "" }
func (r RepositoryID) Equal(other RepositoryID) bool {
	return r.IsValid() && other.IsValid() && r.value == other.value
}

// gitObjectID validates a lowercase hex git object identity (SHA-1 or SHA-256).
func gitObjectID(domain, value string) (string, error) {
	if len(value) != 40 && len(value) != 64 {
		return "", effectError(
			fmt.Sprintf("%s %q is not a 40- or 64-character hex object id", domain, value),
			"a git object id is a full SHA-1 (40) or SHA-256 (64) hex digest; abbreviations are ambiguous",
			"git object validation", "git operand validation",
			"the exact object cannot be verified",
			"supply the full lowercase hex object id", nil,
		)
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return "", effectError(
				fmt.Sprintf("%s %q contains non-hex or uppercase character %q", domain, value, string(r)),
				"a git object id is compared byte-for-byte, so only lowercase hex is canonical",
				"git object validation", "git operand validation",
				"two spellings of one object could compare unequal",
				"supply the object id in lowercase hexadecimal", nil,
			)
		}
	}
	return value, nil
}

// CommitOID is a full, lowercase-hex git commit object id.
type CommitOID struct {
	value       string
	constructed bool
}

// NewCommitOID validates a full commit object id.
func NewCommitOID(value string) (CommitOID, error) {
	validated, err := gitObjectID("commit id", value)
	if err != nil {
		return CommitOID{}, err
	}
	return CommitOID{value: validated, constructed: true}, nil
}

func (c CommitOID) String() string { return c.value }
func (c CommitOID) IsValid() bool  { return c.constructed && c.value != "" }
func (c CommitOID) Equal(other CommitOID) bool {
	return c.IsValid() && other.IsValid() && c.value == other.value
}

// TreeDigest is a full, lowercase-hex git tree object id naming the exact tree a
// commit must carry.
type TreeDigest struct {
	value       string
	constructed bool
}

// NewTreeDigest validates a full tree object id.
func NewTreeDigest(value string) (TreeDigest, error) {
	validated, err := gitObjectID("tree digest", value)
	if err != nil {
		return TreeDigest{}, err
	}
	return TreeDigest{value: validated, constructed: true}, nil
}

func (t TreeDigest) String() string { return t.value }
func (t TreeDigest) IsValid() bool  { return t.constructed && t.value != "" }
func (t TreeDigest) Equal(other TreeDigest) bool {
	return t.IsValid() && other.IsValid() && t.value == other.value
}

// RemoteRef is the exact destination ref a push updates (for example
// refs/heads/main). It carries no whitespace, control, or glob characters.
type RemoteRef struct {
	value       string
	constructed bool
}

// NewRemoteRef validates an exact remote ref name.
func NewRemoteRef(value string) (RemoteRef, error) {
	if !utf8.ValidString(value) || value == "" {
		return RemoteRef{}, effectError(
			"remote ref is empty or not valid UTF-8",
			"a push must name one exact destination ref",
			"NewRemoteRef", "git operand validation",
			"the push has no deterministic destination",
			"supply a non-empty UTF-8 ref such as refs/heads/main", nil,
		)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return RemoteRef{}, effectError(
				fmt.Sprintf("remote ref %q contains whitespace or control character", value),
				"whitespace and control characters are unsafe in a portable ref name",
				"NewRemoteRef", "git operand validation",
				"the ref cannot be represented safely",
				"remove whitespace and control characters from the ref", nil,
			)
		}
	}
	if i := strings.IndexAny(value, pathGlobMetacharacters); i >= 0 {
		return RemoteRef{}, effectError(
			fmt.Sprintf("remote ref %q contains glob metacharacter %q", value, string(value[i])),
			"a push updates exactly one ref; a wildcard could update refs the effect does not intend",
			"NewRemoteRef", "git operand validation",
			"the push could update unintended refs",
			"name one exact ref instead of a glob pattern", nil,
		)
	}
	return RemoteRef{value: value, constructed: true}, nil
}

func (r RemoteRef) String() string { return r.value }
func (r RemoteRef) IsValid() bool  { return r.constructed && r.value != "" }
func (r RemoteRef) Equal(other RemoteRef) bool {
	return r.IsValid() && other.IsValid() && r.value == other.value
}

// ExpectedOldOID states what the remote ref must currently be for a guarded push
// to proceed: either explicitly absent (the ref must not exist) or an exact
// prior commit. Its zero value is invalid — a guarded push must state its
// expectation explicitly, never leave it unspecified.
type ExpectedOldOID struct {
	present     bool
	oid         CommitOID
	constructed bool
}

// ExpectAbsentRemote states the remote ref must not currently exist.
func ExpectAbsentRemote() ExpectedOldOID {
	return ExpectedOldOID{present: false, constructed: true}
}

// ExpectRemoteAt states the remote ref must currently be exactly oid.
func ExpectRemoteAt(oid CommitOID) (ExpectedOldOID, error) {
	if !oid.IsValid() {
		return ExpectedOldOID{}, effectError(
			"expected-old commit id is zero or invalid",
			"a non-absent expectation must name the exact prior commit the remote must hold",
			"ExpectRemoteAt", "git operand validation",
			"the guarded push cannot verify the remote's prior state",
			"construct the commit id with NewCommitOID, or use ExpectAbsentRemote", nil,
		)
	}
	return ExpectedOldOID{present: true, oid: oid, constructed: true}, nil
}

func (e ExpectedOldOID) IsValid() bool { return e.constructed }

// Absent reports whether the expectation is that the remote ref does not exist.
func (e ExpectedOldOID) Absent() bool { return e.constructed && !e.present }

// Commit returns the expected prior commit and true when the expectation is a
// specific commit rather than absence.
func (e ExpectedOldOID) Commit() (CommitOID, bool) {
	if !e.present {
		return CommitOID{}, false
	}
	return e.oid, true
}

// CommitPolicy is the closed set of repository commit policies. A policy is an
// explicit operand and contract, not a best-effort renderer substitution: a
// repository that requires `git agent-commit` names it here, and a lowerer may
// not silently substitute a plain `git commit`.
type CommitPolicy string

const (
	// CommitPolicyAgentCommit requires the repository's `git agent-commit`.
	CommitPolicyAgentCommit CommitPolicy = "git-agent-commit"
	// CommitPolicyPlainCommit uses a plain `git commit` where the repository
	// permits it.
	CommitPolicyPlainCommit CommitPolicy = "git-commit"
)

func (p CommitPolicy) IsValid() bool {
	switch p {
	case CommitPolicyAgentCommit, CommitPolicyPlainCommit:
		return true
	default:
		return false
	}
}

// GitEffectKind is the closed set of modeled non-landing git effects. The only
// landing push effect is the guarded push (see GuardedPushInput), which is a
// distinct parent-mediated effect.
type GitEffectKind string

const (
	// GitStatusEvidence reads repository status as read-only evidence.
	GitStatusEvidence GitEffectKind = "status-evidence"
	// GitCommitEvidence attaches an exact commit as read-only evidence.
	GitCommitEvidence GitEffectKind = "commit-evidence"
	// GitDiffEvidence attaches an exact diff as read-only evidence.
	GitDiffEvidence GitEffectKind = "diff-evidence"
	// GitStage stages exact owned paths.
	GitStage GitEffectKind = "stage"
	// GitCommit creates a commit under an explicit repository commit policy.
	GitCommit GitEffectKind = "commit"
	// GitFetch fetches from a remote where the protocol has authority.
	GitFetch GitEffectKind = "fetch"
	// GitRebase rebases where the protocol has authority.
	GitRebase GitEffectKind = "rebase"
)

// GitEffect is the closed sum of modeled non-landing git effects. It is opaque
// and constructor-owned. Read evidence effects report StateChanging() == false;
// stage/commit/fetch/rebase report true. It is an Effect variant.
type GitEffect struct {
	kind        GitEffectKind
	repository  RepositoryID
	paths       []OwnedPath
	commit      CommitOID
	policy      CommitPolicy
	remote      RemoteRef
	constructed bool
}

// NewGitReadEvidence builds a read-only git evidence effect (status, commit, or
// diff evidence).
func NewGitReadEvidence(repository RepositoryID, kind GitEffectKind) (GitEffect, error) {
	switch kind {
	case GitStatusEvidence, GitCommitEvidence, GitDiffEvidence:
	default:
		return GitEffect{}, effectError(
			fmt.Sprintf("git evidence kind %q is not a read-evidence kind", kind),
			"read evidence must be one of status, commit, or diff evidence",
			"NewGitReadEvidence", "git effect validation",
			"the effect is not a valid read-evidence effect",
			"use GitStatusEvidence, GitCommitEvidence, or GitDiffEvidence", nil,
		)
	}
	if !repository.IsValid() {
		return GitEffect{}, invalidGitRepository("NewGitReadEvidence")
	}
	return GitEffect{kind: kind, repository: repository, constructed: true}, nil
}

// NewGitStage stages exact owned paths in a repository.
func NewGitStage(repository RepositoryID, paths ...OwnedPath) (GitEffect, error) {
	if !repository.IsValid() {
		return GitEffect{}, invalidGitRepository("NewGitStage")
	}
	if len(paths) == 0 {
		return GitEffect{}, effectError(
			"git stage names no paths",
			"staging must name at least one exact owned path so it never stages an unintended set",
			"NewGitStage", "git effect validation",
			"the stage effect has no exact target",
			"supply at least one owned path to stage", nil,
		)
	}
	for index, path := range paths {
		if !path.IsValid() {
			return GitEffect{}, effectError(
				fmt.Sprintf("git stage path %d is zero or invalid", index),
				"every staged path must be a constructor-validated owned path",
				"NewGitStage", "git effect validation",
				"the stage effect could act on the wrong path",
				"construct every path with NewOwnedPath", nil,
			)
		}
	}
	return GitEffect{kind: GitStage, repository: repository, paths: append([]OwnedPath(nil), paths...), constructed: true}, nil
}

// NewGitCommit builds a commit effect bound to an explicit repository commit
// policy. The policy is an operand, never inferred by a renderer.
func NewGitCommit(repository RepositoryID, policy CommitPolicy) (GitEffect, error) {
	if !repository.IsValid() {
		return GitEffect{}, invalidGitRepository("NewGitCommit")
	}
	if !policy.IsValid() {
		return GitEffect{}, effectError(
			fmt.Sprintf("git commit policy %q is invalid", policy),
			"a repository's commit policy is an explicit contract operand, not a renderer default; a lowerer may not substitute `git commit` for a repository that requires `git agent-commit`",
			"NewGitCommit", "git effect validation",
			"the commit could violate repository policy",
			"select CommitPolicyAgentCommit or CommitPolicyPlainCommit explicitly", nil,
		)
	}
	return GitEffect{kind: GitCommit, repository: repository, policy: policy, constructed: true}, nil
}

// NewGitFetch builds a fetch effect from a remote ref.
func NewGitFetch(repository RepositoryID, remote RemoteRef) (GitEffect, error) {
	if !repository.IsValid() {
		return GitEffect{}, invalidGitRepository("NewGitFetch")
	}
	if !remote.IsValid() {
		return GitEffect{}, effectError(
			"git fetch remote ref is zero or invalid",
			"a fetch must name the exact ref it retrieves",
			"NewGitFetch", "git effect validation",
			"the fetch has no deterministic source ref",
			"construct the ref with NewRemoteRef", nil,
		)
	}
	return GitEffect{kind: GitFetch, repository: repository, remote: remote, constructed: true}, nil
}

// NewGitRebase builds a rebase effect onto a remote ref.
func NewGitRebase(repository RepositoryID, onto RemoteRef) (GitEffect, error) {
	if !repository.IsValid() {
		return GitEffect{}, invalidGitRepository("NewGitRebase")
	}
	if !onto.IsValid() {
		return GitEffect{}, effectError(
			"git rebase onto-ref is zero or invalid",
			"a rebase must name the exact ref it replays onto",
			"NewGitRebase", "git effect validation",
			"the rebase has no deterministic base ref",
			"construct the ref with NewRemoteRef", nil,
		)
	}
	return GitEffect{kind: GitRebase, repository: repository, remote: onto, constructed: true}, nil
}

func invalidGitRepository(where string) error {
	return effectError(
		"git effect repository is zero or invalid",
		"a git effect must name a constructor-validated repository",
		where, "git effect validation",
		"the git effect cannot name its repository",
		"construct the repository with NewRepositoryID", nil,
	)
}

func (g GitEffect) Kind() GitEffectKind      { return g.kind }
func (g GitEffect) Repository() RepositoryID { return g.repository }
func (g GitEffect) IsValid() bool            { return g.constructed && g.repository.IsValid() }

// Paths returns the staged paths and true for a stage effect.
func (g GitEffect) Paths() ([]OwnedPath, bool) {
	if g.kind != GitStage {
		return nil, false
	}
	return append([]OwnedPath(nil), g.paths...), true
}

// Policy returns the commit policy and true for a commit effect.
func (g GitEffect) Policy() (CommitPolicy, bool) {
	if g.kind != GitCommit {
		return "", false
	}
	return g.policy, true
}

// Remote returns the remote ref and true for a fetch or rebase effect.
func (g GitEffect) Remote() (RemoteRef, bool) {
	if g.kind != GitFetch && g.kind != GitRebase {
		return RemoteRef{}, false
	}
	return g.remote, true
}

// StateChanging reports whether the effect mutates repository or remote state.
// The three evidence kinds are read-only.
func (g GitEffect) StateChanging() bool {
	switch g.kind {
	case GitStatusEvidence, GitCommitEvidence, GitDiffEvidence:
		return false
	default:
		return true
	}
}

// Classify reports the runtime class. A commit under repository policy is a
// semantic instruction the agent carries out; every other modeled git effect is
// executed natively.
func (g GitEffect) Classify() RuntimeClass {
	if g.kind == GitCommit {
		return RuntimeClassSemanticInstruction
	}
	return RuntimeClassNative
}

func (g GitEffect) isEffect() {}
