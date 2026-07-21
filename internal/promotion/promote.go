package promotion

import (
	"github.com/dayvidpham/pasture/internal/effects"
)

// PromotionResult is the verified outcome of a completed pasture-stable
// promotion. It carries the exact ref and commit the channel now resolves to and
// the guarded-push outcome (a fresh advance or a verified idempotent replay), so
// a release operator and downstream consumers observe exactly what was published.
type PromotionResult struct {
	Ref     string
	Commit  string
	Tree    string
	Outcome effects.GuardedPushOutcome
	proof   effects.VerifiedGuardedPush
}

// Proof returns the process-local verified guarded-push proof. It is never
// serialized (its codec deliberately fails); a caller uses it in-process to gate
// a protected follow-on operation.
func (r PromotionResult) Proof() effects.VerifiedGuardedPush { return r.proof }

// Promoter performs the gated, guarded promotion of the pasture-stable ref. It
// composes an injected RevisionResolver (to resolve the exact commit/tree to
// publish) and an injected effects.RepositoryPusher (to perform the guarded
// update). It re-implements no guarded-push logic: the verify/push/re-read/prove
// algorithm lives entirely in effects.GuardedPushExactCommit.
type Promoter struct {
	pusher   effects.RepositoryPusher
	resolver RevisionResolver
	valid    bool
}

// NewPromoter wires a promoter with a revision resolver and a repository pusher.
// Production passes a GitRevisionResolver and an effects.GitRepositoryPusher;
// tests pass fakes.
func NewPromoter(resolver RevisionResolver, pusher effects.RepositoryPusher) (Promoter, error) {
	if resolver == nil {
		return Promoter{}, fault(
			"promoter has no revision resolver",
			"the promoter must resolve the exact commit and tree to publish",
			"promotion.NewPromoter", "promoter wiring",
			"the promotion cannot identify the object to land",
			"pass a RevisionResolver (GitRevisionResolver in production)", nil,
		)
	}
	if pusher == nil {
		return Promoter{}, fault(
			"promoter has no repository pusher",
			"the guarded update is performed through an injected repository pusher",
			"promotion.NewPromoter", "promoter wiring",
			"the promotion cannot verify or publish the channel update",
			"pass an effects.RepositoryPusher (effects.GitRepositoryPusher in production)", nil,
		)
	}
	return Promoter{resolver: resolver, pusher: pusher, valid: true}, nil
}

// Promote resolves the immutable candidate, runs the ordered gate set, then
// advances the pasture-stable ref with exactly one guarded update.
//
// Ordering guarantees:
//  1. The full requested revision is resolved before expensive gates. Invalid or
//     unavailable candidates fail without running the suite or touching a ref.
//  2. Gates run against immutable materializations prepared from that commit.
//  3. The guarded push re-reads the remote immediately before publication and
//     performs a single --force-with-lease update. A stale expected-old, a racing
//     advance, or a different ref yields no proof and never overwrites the remote;
//     a remote already at the exact target is a verified idempotent replay.
func (p Promoter) Promote(request PromotionRequest, gates []Gate) (PromotionResult, error) {
	if !p.valid {
		return PromotionResult{}, fault(
			"promoter is zero or invalid",
			"a promotion requires a validly wired promoter",
			"promotion.Promoter.Promote", "promotion",
			"no promotion can be performed",
			"construct the promoter with NewPromoter", nil,
		)
	}
	if !request.IsValid() {
		return PromotionResult{}, fault(
			"promotion request is zero or invalid",
			"a promotion requires a constructor-validated request",
			"promotion.Promoter.Promote", "promotion",
			"no promotion is attempted",
			"construct the request with NewPromotionRequest", nil,
		)
	}

	// (1) Resolve the exact commit and tree before expensive gates.
	commit, err := p.resolver.ResolveCommit(request.PastureRepo(), request.PastureRevision())
	if err != nil {
		return PromotionResult{}, err
	}
	tree, err := p.resolver.ResolveTree(request.PastureRepo(), commit)
	if err != nil {
		return PromotionResult{}, err
	}
	if commit.String() != request.PastureRevision() {
		return PromotionResult{}, fault(
			"resolved pasture commit does not equal the requested commit",
			"the promotion request must name the exact object used by gates and publication",
			"promotion.Promoter.Promote", "candidate resolution",
			"the candidate is not published and the pasture-stable ref is unchanged",
			"fetch the exact commit and pass its full lowercase object id", nil,
		)
	}

	// (2) A failure aborts before the remote is touched.
	if err := RunGates(gates); err != nil {
		return PromotionResult{}, err
	}

	// (3) Build the guarded update input and perform exactly one guarded push.
	input, err := effects.NewGuardedPushInput(
		request.PastureRepo(),
		commit,
		tree,
		request.StableRef(),
		request.ExpectedOld(),
	)
	if err != nil {
		return PromotionResult{}, fault(
			"guarded push input could not be constructed for the promotion",
			"the resolved operands did not form a valid guarded-push input",
			"promotion.Promoter.Promote", "guarded update construction",
			"the promotion cannot publish the channel safely",
			"check the resolved commit, tree, ref, and expected-old operands", err,
		)
	}

	proof, err := effects.GuardedPushExactCommit(input, p.pusher)
	if err != nil {
		return PromotionResult{}, fault(
			"guarded promotion of "+request.StableRef().String()+" did not land",
			"the remote was absent, stale, racing, or at a different commit, so the update was not verified",
			"promotion.Promoter.Promote", "guarded update",
			"the pasture-stable ref is unchanged and the promotion did not publish a racing publisher's work",
			"re-read --expected-old from the current remote channel state and re-run the promotion", err,
		)
	}

	return PromotionResult{
		Ref:     request.StableRef().String(),
		Commit:  commit.String(),
		Tree:    tree.String(),
		Outcome: proof.Outcome(),
		proof:   proof,
	}, nil
}
