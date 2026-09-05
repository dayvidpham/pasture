package testutil

import (
	"fmt"
	"testing"
)

// EnumMirror describes one enum that mirrors another. Source is the enum whose
// every arm must be represented; Mirror looks up one source arm on the mirror
// side and reports whether a mapping exists and how the mirror spells it.
//
// The population is the caller's SOURCE ARM LIST, so the caller must derive
// that list from the enum itself (a sentinel-bounded range, a generated arm
// table, a declaration table), never hand-list it: a hand list cannot see the
// arm that was added after it was written, and that arm is the one this check
// exists to catch.
type EnumMirror[S comparable] struct {
	// Subject names the pair for the failure message, for example
	// "WithheldReason -> String()".
	Subject string
	// Arms is every arm of the source enum, in ordinal order.
	Arms []S
	// Mirror maps one source arm to its representation on the mirror side.
	// ok is false when the mirror has no mapping for the arm.
	Mirror func(arm S) (representation string, ok bool)
	// Describe spells one source arm for the failure message. When nil, the
	// arm is printed with %v.
	Describe func(arm S) string
}

// RequireEnumMirrorComplete fails the test, naming every unmapped arm, when a
// source arm has no mapping on the mirror side or when two source arms share
// one mirror representation. It also fails when the source list is empty,
// because a check over nothing proves nothing.
func RequireEnumMirrorComplete[S comparable](t testing.TB, mirror EnumMirror[S]) {
	t.Helper()
	if len(mirror.Arms) == 0 {
		t.Fatalf("enum mirror %s: the source arm list is empty, so no mapping was checked; derive the arms from the enum's own sentinel, table or generated list", mirror.Subject)
		return
	}
	describe := mirror.Describe
	if describe == nil {
		describe = func(arm S) string { return fmt.Sprintf("%v", arm) }
	}
	seen := make(map[string]S, len(mirror.Arms))
	var unmapped, collided []string
	for _, arm := range mirror.Arms {
		representation, ok := mirror.Mirror(arm)
		if !ok {
			unmapped = append(unmapped, describe(arm))
			continue
		}
		if first, taken := seen[representation]; taken {
			collided = append(collided, fmt.Sprintf("%s and %s both map to %q", describe(first), describe(arm), representation))
			continue
		}
		seen[representation] = arm
	}
	if len(unmapped) > 0 {
		t.Errorf("enum mirror %s: %d source arm(s) have no mapping on the mirror side: %v; add the mapping for each named arm", mirror.Subject, len(unmapped), unmapped)
	}
	if len(collided) > 0 {
		t.Errorf("enum mirror %s: two source arms share one mirror representation: %v; give each arm its own representation", mirror.Subject, collided)
	}
}
