// Package compilefail is an isolated compile-fail fixture: a raw string literal
// must not satisfy the opaque CommitOID operand of NewGuardedPushInput.
package compilefail

import "github.com/dayvidpham/pasture/internal/effects"

func rawCommit(repository effects.RepositoryID, tree effects.TreeDigest, ref effects.RemoteRef) {
	_, _ = effects.NewGuardedPushInput(repository, "1111111111111111111111111111111111111111", tree, ref, effects.ExpectAbsentRemote())
}
