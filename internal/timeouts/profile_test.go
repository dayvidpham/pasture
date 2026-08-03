package timeouts

import (
	"testing"
	"time"
)

func TestEveryKnownProfilePreservesStrictOrdering(t *testing.T) {
	t.Parallel()
	for _, profile := range KnownProfiles() {
		if err := profile.Validate(); err != nil {
			t.Fatalf("profile %d: %v", profile.Kind(), err)
		}
		if profile.SQLiteBusy() >= profile.Ingress() || profile.SQLiteBusy() >= profile.StartSlice() {
			t.Fatalf("profile %d inverted", profile.Kind())
		}
	}
}

func TestGeneralTestProfileUsesTwoSecondIngress(t *testing.T) {
	t.Parallel()
	if got := TestProfile().Ingress(); got != 2*time.Second {
		t.Fatalf("test ingress=%s, want 2s", got)
	}
}

func TestDeadlineTestProfileRetainsTightFailurePath(t *testing.T) {
	t.Parallel()
	if got := DeadlineTestProfile().Ingress(); got != 250*time.Millisecond {
		t.Fatalf("deadline-test ingress=%s, want 250ms", got)
	}
}

func TestNewRejectsInvertedTestProfile(t *testing.T) {
	t.Parallel()
	if _, err := New(Test, 5*time.Second, 250*time.Millisecond, 500*time.Millisecond); err == nil {
		t.Fatal("inverted test profile passed")
	}
}
