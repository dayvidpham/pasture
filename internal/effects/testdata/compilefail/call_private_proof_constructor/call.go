// Package compilefail is an isolated compile-fail fixture: the only producer of
// a VerifiedGuardedPush is the package-private newVerifiedGuardedPush, which
// GuardedPushExactCommit calls after exact-target verification. It is
// unreachable from outside the package, so no caller can mint a proof directly.
package compilefail

import "github.com/dayvidpham/pasture/internal/effects"

func mint(input effects.GuardedPushInput) effects.VerifiedGuardedPush {
	return effects.newVerifiedGuardedPush(input, effects.GuardedPushPushed)
}
