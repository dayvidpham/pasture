package testutil

import (
	"fmt"
	"testing"

	"github.com/dayvidpham/pasture/internal/runtime"
)

// BelowFloor returns the release one step below v: the previous patch; when
// the patch is zero, the previous minor with patch zero; when both are zero,
// the previous major. A test that must prove a version floor is exclusive one
// step below it derives that version here from the contract instead of writing
// a number that goes stale when the contract moves.
func BelowFloor(t testing.TB, v runtime.HostVersion) runtime.HostVersion {
	t.Helper()
	major, minor, patch := v.Release()
	switch {
	case patch > 0:
		patch--
	case minor > 0:
		minor--
		patch = 0
	case major > 0:
		major--
		minor, patch = 0, 0
	default:
		t.Fatalf("no release lies below %s", v)
	}
	return mustHostVersion(t, fmt.Sprintf("%d.%d.%d", major, minor, patch))
}

// Bump returns v with its release components raised by the given amounts, so
// a test can name "the next patch release", "a later minor release" or "a
// later major release" relative to a contract without a hand-written number.
func Bump(t testing.TB, v runtime.HostVersion, major, minor, patch uint64) runtime.HostVersion {
	t.Helper()
	gotMajor, gotMinor, gotPatch := v.Release()
	return mustHostVersion(t, fmt.Sprintf("%d.%d.%d", gotMajor+major, gotMinor+minor, gotPatch+patch))
}

func mustHostVersion(t testing.TB, value string) runtime.HostVersion {
	t.Helper()
	parsed, err := runtime.ParseHostVersion(value)
	if err != nil {
		t.Fatalf("parse host version %q: %v", value, err)
	}
	return parsed
}
