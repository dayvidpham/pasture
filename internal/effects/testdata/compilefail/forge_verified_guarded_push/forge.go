// Package compilefail is an isolated compile-fail fixture: VerifiedGuardedPush
// has only unexported fields, so no external caller can forge one by setting
// its verified flag or any other field in a struct literal.
package compilefail

import "github.com/dayvidpham/pasture/internal/effects"

func forge() effects.VerifiedGuardedPush {
	return effects.VerifiedGuardedPush{verified: true}
}
