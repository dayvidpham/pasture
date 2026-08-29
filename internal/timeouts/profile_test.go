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
		if profile.HookInvocation() <= profile.Ingress() || profile.HookInvocation() <= profile.StartSlice() {
			t.Fatalf("profile %d: hook-invocation deadline %s is not above ingress %s and start_slice %s",
				profile.Kind(), profile.HookInvocation(), profile.Ingress(), profile.StartSlice())
		}
		if profile.WorkflowResult() <= profile.HookInvocation() {
			t.Fatalf("profile %d: workflow-result wait %s is not above the hook-invocation deadline %s",
				profile.Kind(), profile.WorkflowResult(), profile.HookInvocation())
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
	if _, err := New(Test, 5*time.Second, 250*time.Millisecond, 500*time.Millisecond, time.Second, 30*time.Second); err == nil {
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
			_, err := New(Test, 500*time.Millisecond, 2*time.Second, 3*time.Second, 4*time.Second, tc.workflowResult)
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

// TestNewRejectsAHookInvocationDeadlineOutsideItsOrder pins the new tier's
// place in the hierarchy. One hook invocation CONTAINS the lifecycle receipt
// append it performs, so it must outlive the ingress window; and the caller
// that waits for a whole workflow must outlive the hook. A hook deadline that
// escapes either bound reports a timeout for work still inside its own budget,
// or lets the hook outrun the host budget and freeze the session.
func TestNewRejectsAHookInvocationDeadlineOutsideItsOrder(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		hookInvocation time.Duration
		workflowResult time.Duration
	}{
		{name: "below ingress", hookInvocation: time.Second, workflowResult: 30 * time.Second},
		{name: "equal to ingress", hookInvocation: 2 * time.Second, workflowResult: 30 * time.Second},
		{name: "below start_slice", hookInvocation: 2500 * time.Millisecond, workflowResult: 30 * time.Second},
		{name: "equal to start_slice", hookInvocation: 3 * time.Second, workflowResult: 30 * time.Second},
		{name: "above the workflow-result wait", hookInvocation: 40 * time.Second, workflowResult: 30 * time.Second},
		{name: "equal to the workflow-result wait", hookInvocation: 30 * time.Second, workflowResult: 30 * time.Second},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(Test, 500*time.Millisecond, 2*time.Second, 3*time.Second, tc.hookInvocation, tc.workflowResult)
			if err == nil {
				t.Fatalf("New accepted a hook-invocation deadline of %s beside ingress 2s, start_slice 3s and workflow-result %s",
					tc.hookInvocation, tc.workflowResult)
			}
		})
	}
}

// TestProductionHookInvocationSitsBelowTheSmallestHostBudget pins the value
// against the budget that pays for it. Claude Code gives a hook 10s by default;
// a hook that outruns it freezes the session, so pasture must stop first, with
// headroom for process start.
func TestProductionHookInvocationSitsBelowTheSmallestHostBudget(t *testing.T) {
	t.Parallel()

	const smallestHostBudget = 10 * time.Second

	got := ProductionProfile().HookInvocation()
	if got != 5*time.Second {
		t.Fatalf("production hook-invocation deadline = %s, want 5s", got)
	}
	if got >= smallestHostBudget {
		t.Fatalf("production hook-invocation deadline %s does not sit below the smallest host budget %s",
			got, smallestHostBudget)
	}
	if smallestHostBudget-got < 2*time.Second {
		t.Fatalf("production hook-invocation deadline %s leaves only %s under the smallest host budget %s, which is not enough headroom for process start",
			got, smallestHostBudget-got, smallestHostBudget)
	}
}
