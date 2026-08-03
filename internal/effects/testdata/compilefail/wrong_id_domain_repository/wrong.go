// Package compilefail is an isolated compile-fail fixture: a CommitOID must not
// satisfy the RepositoryID operand of NewGuardedPushInput. Distinct typed
// operands cannot be transposed even though both wrap a string.
package compilefail

import "github.com/dayvidpham/pasture/internal/effects"

func transposed(commit effects.CommitOID, tree effects.TreeDigest, ref effects.RemoteRef) {
	// commit (CommitOID) is passed where a RepositoryID is required.
	_, _ = effects.NewGuardedPushInput(commit, commit, tree, ref, effects.ExpectAbsentRemote())
}
