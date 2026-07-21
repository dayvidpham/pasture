package promotion

import (
	"errors"
	"path/filepath"

	"github.com/dayvidpham/pasture/internal/effects"
)

// PromotionResult is the verified outcome of a completed pasture-stable
// promotion, including the single candidate-owned marketplace projection that
// passed validation.
type PromotionResult struct {
	Ref         string
	Commit      string
	Tree        string
	Outcome     effects.GuardedPushOutcome
	Marketplace Projection
	proof       effects.VerifiedGuardedPush
}

// Proof returns the process-local verified guarded-push proof.
func (r PromotionResult) Proof() effects.VerifiedGuardedPush { return r.proof }

// Coordinator is the sole promotion entry point. Its production dependencies
// are injectable process effects; release policy, candidate states, and the
// mandatory gate inventory are not caller-selectable.
type Coordinator struct {
	resolve effects.ExecutableResolver
	run     effects.CommandRunner
	valid   bool
}

// NewCoordinator wires the process effects used for git and gate commands.
// Tests inject command outcomes through run while exercising the same mandatory
// gate inventory and state transitions as production.
func NewCoordinator(resolve effects.ExecutableResolver, run effects.CommandRunner) (Coordinator, error) {
	if resolve == nil || run == nil {
		return Coordinator{}, fault("promotion coordinator is missing an executable resolver or command runner", "candidate preparation, mandatory gates, cleanup, and publication share one injected process boundary", "promotion.NewCoordinator", "coordinator wiring", "no promotion can start", "pass exec.LookPath and effects.DefaultCommandRunner in production", nil)
	}
	return Coordinator{resolve: resolve, run: run, valid: true}, nil
}

type preparedCandidate struct {
	pasture    repositorySnapshot
	aura       repositorySnapshot
	projection Projection
	stableRef  effects.RemoteRef
	expected   effects.ExpectedOldOID
}

type gatedCandidate struct{ candidate preparedCandidate }

type publishableCandidate struct {
	repository effects.RepositoryID
	commit     effects.CommitOID
	tree       effects.TreeDigest
	pushURL    string
	stableRef  effects.RemoteRef
	expected   effects.ExpectedOldOID
	projection Projection
}

// Promote executes the unforgeable transition chain: exact request, immutable
// prepared candidate, mandatory-gated candidate, verified cleanup, then guarded
// publication. No intermediate state is exported or caller-constructible.
func (c Coordinator) Promote(request PromotionRequest) (PromotionResult, error) {
	if !c.valid || !request.IsValid() {
		return PromotionResult{}, fault("promotion coordinator or request is zero or invalid", "promotion requires constructor-validated runtime dependencies and exact request operands", "promotion.Coordinator.Promote", "promotion startup", "no candidate is prepared and no ref is touched", "construct both values with NewCoordinator and NewPromotionRequest", nil)
	}
	prepared, err := c.prepare(request)
	if err != nil {
		return PromotionResult{}, err
	}
	gated, err := c.gate(prepared)
	if err != nil {
		return PromotionResult{}, errors.Join(err, c.cleanup(prepared))
	}
	publishable, err := c.cleanupForPublication(gated)
	if err != nil {
		return PromotionResult{}, err
	}
	return c.publish(publishable)
}

func (c Coordinator) prepare(request PromotionRequest) (preparedCandidate, error) {
	pasture, err := prepareRepositorySnapshot(request.pastureRepo, request.pastureCommit.String(), request.remote, PastureRepository, c.resolve, c.run)
	if err != nil {
		return preparedCandidate{}, err
	}
	aura, err := prepareRepositorySnapshot(request.auraRepo, request.auraCommit.String(), "origin", AuraRepository, c.resolve, c.run)
	if err != nil {
		return preparedCandidate{}, errors.Join(err, pasture.close())
	}
	projection, err := ProjectClaudeCodeTree(pasture.repository.String(), "aura-plugins", pasture.commit.String())
	if err != nil {
		return preparedCandidate{}, errors.Join(err, aura.close(), pasture.close())
	}
	return preparedCandidate{pasture: pasture, aura: aura, projection: projection, stableRef: request.stableRef, expected: request.expectedOld}, nil
}

func (c Coordinator) gate(candidate preparedCandidate) (gatedCandidate, error) {
	marketplace := filepath.Join(candidate.aura.repository.String(), ".claude-plugin", "marketplace.json")
	if err := ValidateMarketplaceFile(marketplace, candidate.projection); err != nil {
		return gatedCandidate{}, mandatoryGateFailure("aura-marketplace-validation", err)
	}
	if err := c.runGoGate(candidate.pasture.repository, "pasture-package-race", "./..."); err != nil {
		return gatedCandidate{}, mandatoryGateFailure("pasture-package-race", err)
	}
	if err := c.runGoGate(candidate.pasture.repository, "activation-race", "./internal/install/..."); err != nil {
		return gatedCandidate{}, mandatoryGateFailure("activation-race", err)
	}
	return gatedCandidate{candidate: candidate}, nil
}

func mandatoryGateFailure(name string, cause error) error {
	return fault("mandatory promotion gate "+name+" failed", "every static release gate must pass against the immutable candidate", "promotion.Coordinator.gate", name+" evaluation", "the pasture-stable ref remains unchanged", "fix the failing candidate check and retry", cause)
}

func (c Coordinator) runGoGate(repository effects.RepositoryID, name, pattern string) error {
	goBinary, err := c.resolve("go")
	if err != nil {
		return fault("mandatory gate "+name+" could not resolve go", "the gate runs the repository test suite with the race detector", "promotion.Coordinator.runGoGate", name+" dispatch", "the candidate is not published", "install Go on PATH and retry", err)
	}
	if _, err := c.run(repository.String(), goBinary, "test", "-race", pattern); err != nil {
		return fault("mandatory gate "+name+" failed", "the immutable Pasture candidate did not pass its required race-enabled suite", "promotion.Coordinator.runGoGate", name+" execution", "the candidate is not published", "fix the failing tests at the requested Pasture commit and retry", err)
	}
	return nil
}

func (c Coordinator) cleanup(candidate preparedCandidate) error {
	return errors.Join(candidate.aura.close(), candidate.pasture.close())
}

func (c Coordinator) cleanupForPublication(candidate gatedCandidate) (publishableCandidate, error) {
	prepared := candidate.candidate
	if err := c.cleanup(prepared); err != nil {
		return publishableCandidate{}, fault("immutable promotion candidate cleanup failed", "both detached candidate worktrees must be removed successfully before publication", "promotion.Coordinator.cleanupForPublication", "pre-publication cleanup", "the pasture-stable ref remains unchanged and guarded publication does not begin", "follow the nested cleanup error, prune stale worktrees, and retry", err)
	}
	return publishableCandidate{
		repository: prepared.pasture.owner,
		commit:     prepared.pasture.commit,
		tree:       prepared.pasture.tree,
		pushURL:    prepared.pasture.pushURL,
		stableRef:  prepared.stableRef,
		expected:   prepared.expected,
		projection: prepared.projection,
	}, nil
}

func (c Coordinator) publish(candidate publishableCandidate) (PromotionResult, error) {
	pusher, err := effects.NewGitRepositoryPusher(c.resolve, c.run, candidate.pushURL)
	if err != nil {
		return PromotionResult{}, err
	}
	input, err := effects.NewGuardedPushInput(candidate.repository, candidate.commit, candidate.tree, candidate.stableRef, candidate.expected)
	if err != nil {
		return PromotionResult{}, fault("guarded publication input could not be constructed", "the prepared candidate must carry exact verified publication operands", "promotion.Coordinator.publish", "guarded publication construction", "the ref is unchanged", "inspect candidate preparation and retry", err)
	}
	proof, err := effects.GuardedPushExactCommit(input, pusher)
	if err != nil {
		return PromotionResult{}, fault("guarded promotion of "+candidate.stableRef.String()+" did not land", "the exact URL was absent, stale, racing, or at a different commit", "promotion.Coordinator.publish", "guarded publication", "the promotion did not overwrite a racing publisher", "re-read --expected-old from the canonical channel and retry", err)
	}
	return PromotionResult{Ref: candidate.stableRef.String(), Commit: candidate.commit.String(), Tree: candidate.tree.String(), Outcome: proof.Outcome(), Marketplace: candidate.projection, proof: proof}, nil
}
