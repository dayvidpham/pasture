package promotion

import (
	"strings"

	"github.com/dayvidpham/pasture/internal/effects"
)

// DefaultStableRef is the moving release channel ref this package promotes.
const DefaultStableRef = "refs/heads/pasture-stable"

// PromotionRequest is the exact, constructor-validated input to Coordinator.
// It identifies both immutable commits and the local repositories used to
// materialize them. It contains no gate evidence or projected marketplace data;
// those are derived exactly once by the coordinator.
type PromotionRequest struct {
	pastureRepo   effects.RepositoryID
	pastureCommit effects.CommitOID
	auraRepo      effects.RepositoryID
	auraCommit    effects.CommitOID
	remote        string
	stableRef     effects.RemoteRef
	expectedOld   effects.ExpectedOldOID
	valid         bool
}

// NewPromotionRequest validates every operand of a pasture-stable promotion. The
// pasture and aura repositories are working-directory identities; both revision
// operands must already be full immutable commit IDs;
// remote is the configured git remote; stableRef is the destination ref; and
// expectedOld states the remote's required prior state.
func NewPromotionRequest(
	pastureRepo effects.RepositoryID,
	pastureRevision string,
	auraRepo effects.RepositoryID,
	auraRevision string,
	remote string,
	stableRef effects.RemoteRef,
	expectedOld effects.ExpectedOldOID,
) (PromotionRequest, error) {
	if !pastureRepo.IsValid() {
		return PromotionRequest{}, fault(
			"pasture repository is zero or invalid",
			"a promotion publishes a commit from a concrete pasture working repository",
			"promotion.NewPromotionRequest", "promotion request validation",
			"the promotion has no repository to read the revision or push from",
			"construct the repository with effects.NewRepositoryID from the pasture checkout path", nil,
		)
	}
	if strings.TrimSpace(pastureRevision) == "" || strings.TrimSpace(pastureRevision) != pastureRevision {
		return PromotionRequest{}, fault(
			"pasture revision is empty or padded",
			"a promotion advances the channel to one exact named Pasture revision",
			"promotion.NewPromotionRequest", "promotion request validation",
			"the promotion has no source revision to publish",
			"pass --pasture-revision <sha> naming the reviewed commit to promote", nil,
		)
	}
	pastureCommit, err := effects.NewCommitOID(pastureRevision)
	if err != nil {
		return PromotionRequest{}, fault(
			"pasture revision is not a full immutable commit id",
			"promotion gates and publication must address the same exact commit object",
			"promotion.NewPromotionRequest", "promotion request validation",
			"a symbolic, abbreviated, or malformed revision could move or identify the wrong candidate",
			"pass --pasture-revision as the full lowercase commit sha reported by git rev-parse HEAD", err,
		)
	}
	if !auraRepo.IsValid() {
		return PromotionRequest{}, fault(
			"aura repository is zero or invalid",
			"the marketplace/repository gate runs against a concrete Aura working repository",
			"promotion.NewPromotionRequest", "promotion request validation",
			"the promotion cannot validate the Aura marketplace before publishing",
			"construct the repository with effects.NewRepositoryID from the aura-plugins checkout path", nil,
		)
	}
	if strings.TrimSpace(auraRevision) == "" || strings.TrimSpace(auraRevision) != auraRevision {
		return PromotionRequest{}, fault(
			"aura revision is empty or padded",
			"the Aura gate validates one exact named Aura revision",
			"promotion.NewPromotionRequest", "promotion request validation",
			"the promotion cannot pin the Aura tree it validated against",
			"pass --aura-revision <sha> naming the Aura commit to validate", nil,
		)
	}
	auraCommit, err := effects.NewCommitOID(auraRevision)
	if err != nil {
		return PromotionRequest{}, fault(
			"aura revision is not a full immutable commit id",
			"marketplace validation must address one exact Aura commit object",
			"promotion.NewPromotionRequest", "promotion request validation",
			"a symbolic, abbreviated, or malformed revision could validate the wrong marketplace",
			"pass --aura-revision as the full lowercase commit sha reported by git rev-parse HEAD", err,
		)
	}
	if strings.TrimSpace(remote) == "" || strings.TrimSpace(remote) != remote {
		return PromotionRequest{}, fault(
			"git remote name is empty or padded",
			"a guarded promotion targets one configured git remote",
			"promotion.NewPromotionRequest", "promotion request validation",
			"the promotion has no remote to publish the channel to",
			"pass --remote <git-remote>, such as origin", nil,
		)
	}
	if !stableRef.IsValid() {
		return PromotionRequest{}, fault(
			"pasture-stable ref is zero or invalid",
			"a promotion updates exactly one destination ref",
			"promotion.NewPromotionRequest", "promotion request validation",
			"the promotion has no destination ref",
			"construct the ref with effects.NewRemoteRef; the default is "+DefaultStableRef, nil,
		)
	}
	if !expectedOld.IsValid() {
		return PromotionRequest{}, fault(
			"expected-old state is unspecified",
			"a guarded promotion must state the channel's expected prior state explicitly, including explicit absence",
			"promotion.NewPromotionRequest", "promotion request validation",
			"the promotion could clobber an unexpected or racing channel state",
			"pass --expected-old <sha> for an existing channel or --expected-old absent for a first publication", nil,
		)
	}
	return PromotionRequest{
		pastureRepo:   pastureRepo,
		pastureCommit: pastureCommit,
		auraRepo:      auraRepo,
		auraCommit:    auraCommit,
		remote:        remote,
		stableRef:     stableRef,
		expectedOld:   expectedOld,
		valid:         true,
	}, nil
}

// IsValid reports whether the request was validly constructed.
func (r PromotionRequest) IsValid() bool { return r.valid }

// ParseExpectedOld decodes the --expected-old flag value: the literal "absent"
// requires the ref not to exist; any other value is parsed as an exact prior
// commit id the remote must currently hold.
func ParseExpectedOld(value string) (effects.ExpectedOldOID, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return effects.ExpectedOldOID{}, fault(
			"expected-old value is empty",
			"a guarded promotion requires an explicit prior-state expectation",
			"promotion.ParseExpectedOld", "expected-old parsing",
			"the promotion cannot decide whether it is a first publication or an advance",
			"pass a full commit sha, or the literal 'absent' for a first publication", nil,
		)
	}
	if strings.EqualFold(trimmed, "absent") {
		return effects.ExpectAbsentRemote(), nil
	}
	commit, err := effects.NewCommitOID(strings.ToLower(trimmed))
	if err != nil {
		return effects.ExpectedOldOID{}, fault(
			"expected-old value is not 'absent' or a full commit id",
			"the prior-state expectation must be an exact 40- or 64-hex commit or explicit absence",
			"promotion.ParseExpectedOld", "expected-old parsing",
			"the guarded push cannot form a --force-with-lease expectation",
			"pass a full lowercase commit sha, or the literal 'absent'", err,
		)
	}
	return effects.ExpectRemoteAt(commit)
}
