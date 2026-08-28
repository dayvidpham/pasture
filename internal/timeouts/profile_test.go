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
		if profile.WorkflowResult() <= profile.Ingress() || profile.WorkflowResult() <= profile.StartSlice() {
			t.Fatalf("profile %d: workflow-result wait %s is not above ingress %s and start_slice %s",
				profile.Kind(), profile.WorkflowResult(), profile.Ingress(), profile.StartSlice())
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
	if _, err := New(Test, 5*time.Second, 250*time.Millisecond, 500*time.Millisecond, 30*time.Second); err == nil {
		t.Fatal("inverted test profile passed")
	}
}

// The outermost tier must stay outermost. A caller that stops waiting for the
// whole workflow before an inner window has closed reports a timeout for work
// that was still inside its own budget, which hides the real state.
func TestNewRejectsAWorkflowResultWaitBelowTheInnerWindows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		workflowResult time.Duration
	}{
		{name: "below start_slice", workflowResult: 2 * time.Second},
		{name: "equal to start_slice", workflowResult: 3 * time.Second},
		{name: "below ingress", workflowResult: time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(Test, 500*time.Millisecond, 2*time.Second, 3*time.Second, tc.workflowResult)
			if err == nil {
				t.Fatalf("New accepted a workflow-result wait of %s below ingress 2s / start_slice 3s", tc.workflowResult)
			}
		})
	}
}

func TestProductionWorkflowResultWaitIsUnchangedAtThirtySeconds(t *testing.T) {
	t.Parallel()
	if got := ProductionProfile().WorkflowResult(); got != 30*time.Second {
		t.Fatalf("production workflow-result wait = %s, want 30s (the value callers used before it moved into the profile)", got)
	}
}
