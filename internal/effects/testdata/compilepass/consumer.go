// Package compilepass is an external consumer fixture proving the intended
// one-way usage: a downstream package (like the authoritative task package)
// imports internal/effects, accepts the opaque VerifiedGuardedPush proof, and
// hands it to its own protected commit after re-validating it. It also
// demonstrates that every effect operand is built through its typed constructor.
package compilepass

import "github.com/dayvidpham/pasture/internal/effects"

// protectedCommit is the shape of a task-side protected commit: it accepts the
// opaque proof and re-checks it. The proof cannot be forged, only obtained from
// effects.GuardedPushExactCommit.
func protectedCommit(proof effects.VerifiedGuardedPush) error {
	if err := proof.Validate(); err != nil {
		return err
	}
	_ = proof.Repository()
	_ = proof.Commit()
	_ = proof.Tree()
	_ = proof.RemoteRef()
	_ = proof.Outcome()
	return nil
}

// buildGuardedPush shows the typed operand construction path a consumer uses to
// prepare a landing input.
func buildGuardedPush() (effects.GuardedPushInput, error) {
	repository, err := effects.NewRepositoryID("/repo")
	if err != nil {
		return effects.GuardedPushInput{}, err
	}
	commit, err := effects.NewCommitOID("1111111111111111111111111111111111111111")
	if err != nil {
		return effects.GuardedPushInput{}, err
	}
	tree, err := effects.NewTreeDigest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		return effects.GuardedPushInput{}, err
	}
	ref, err := effects.NewRemoteRef("refs/heads/main")
	if err != nil {
		return effects.GuardedPushInput{}, err
	}
	return effects.NewGuardedPushInput(repository, commit, tree, ref, effects.ExpectAbsentRemote())
}

// land wires the input through the algorithm and the protected commit.
func land(input effects.GuardedPushInput, pusher effects.RepositoryPusher) error {
	proof, err := effects.GuardedPushExactCommit(input, pusher)
	if err != nil {
		return err
	}
	return protectedCommit(proof)
}

var _ = buildGuardedPush
var _ = land
