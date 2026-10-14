package timeouts

import (
	"strings"
	"testing"
	"time"
)

// TestZeroOverrideLeavesTheProductionProfileUntouched pins the default: an
// operator who sets no knob gets exactly the profile production has always
// used, tier for tier. This is the "unchanged when unset" half of the knob's
// contract, and it is checked against the values rather than against identity,
// because Profile is a value.
func TestZeroOverrideLeavesTheProductionProfileUntouched(t *testing.T) {
	t.Parallel()

	base := ProductionProfile()
	got, err := base.WithOverride(Override{})
	if err != nil {
		t.Fatalf("WithOverride(zero) returned an error: %v", err)
	}
	if got.SQLiteBusy() != base.SQLiteBusy() ||
		got.Ingress() != base.Ingress() ||
		got.StartSlice() != base.StartSlice() ||
		got.HookInvocation() != base.HookInvocation() ||
		got.WorkflowResult() != base.WorkflowResult() {
		t.Fatalf("WithOverride(zero) changed the profile: got %+v, want the production tiers unchanged", got)
	}
	if got.HookInvocation() != 5*time.Second {
		t.Fatalf("default hook-invocation deadline = %s, want the production 5s", got.HookInvocation())
	}
	if !(Override{}).IsZero() {
		t.Fatal("the zero Override must report IsZero")
	}
}

// TestOverrideAppliesEachExposedTier is the "a valid override takes effect"
// half of the contract. Each knob is checked on its own so that one tier's
// substitution cannot be satisfied by another's.
func TestOverrideAppliesEachExposedTier(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		o    Override
		want func(Profile) time.Duration
	}{
		{
			name: "hook-invocation deadline lowered",
			o:    Override{HookInvocation: 3 * time.Second},
			want: func(p Profile) time.Duration { return p.HookInvocation() },
		},
		{
			name: "hook-invocation deadline lowered to just above the siblings",
			o:    Override{HookInvocation: 2500 * time.Millisecond},
			want: func(p Profile) time.Duration { return p.HookInvocation() },
		},
		{
			name: "ingress window lowered",
			o:    Override{Ingress: 800 * time.Millisecond},
			want: func(p Profile) time.Duration { return p.Ingress() },
		},
		{
			name: "start-slice window lowered",
			o:    Override{StartSlice: 1500 * time.Millisecond},
			want: func(p Profile) time.Duration { return p.StartSlice() },
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ProductionProfile().WithOverride(tc.o)
			if err != nil {
				t.Fatalf("a valid override was refused: %v", err)
			}
			var want time.Duration
			switch {
			case tc.o.HookInvocation > 0:
				want = tc.o.HookInvocation
			case tc.o.Ingress > 0:
				want = tc.o.Ingress
			default:
				want = tc.o.StartSlice
			}
			if gotTier := tc.want(got); gotTier != want {
				t.Fatalf("overridden tier = %s, want %s", gotTier, want)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("the overridden profile is not self-consistent: %v", err)
			}
		})
	}
}

// TestOverrideRefusesAnUnusableCombination is the invariant half of the
// contract. A hook-invocation deadline at or below either caller window, or
// above the workflow-result wait, is refused with the existing inversion error;
// a negative field is refused as not positive. Neither refusal is a silent
// clamp: the caller receives an error and the zero Profile, not a quietly
// adjusted one.
func TestOverrideRefusesAnUnusableCombination(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		o       Override
		wantErr string
	}{
		{
			name:    "hook-invocation below the production ingress",
			o:       Override{HookInvocation: 500 * time.Millisecond},
			wantErr: "timeout profile is inverted",
		},
		{
			name:    "hook-invocation equal to production's start-slice sibling",
			o:       Override{HookInvocation: 2 * time.Second},
			wantErr: "timeout profile is inverted",
		},
		{
			name:    "hook-invocation above the workflow-result wait",
			o:       Override{HookInvocation: 45 * time.Second},
			wantErr: "timeout profile is inverted",
		},
		{
			name:    "an ingress raise that leaves the hook-invocation deadline too low",
			o:       Override{Ingress: 6 * time.Second},
			wantErr: "timeout profile is inverted",
		},
		{
			name:    "an ingress below the SQLite lock wait that sits inside it",
			o:       Override{Ingress: 400 * time.Millisecond},
			wantErr: "timeout profile is inverted",
		},
		{
			name:    "a negative hook-invocation deadline",
			o:       Override{HookInvocation: -time.Second},
			wantErr: "override durations must be positive",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ProductionProfile().WithOverride(tc.o)
			if err == nil {
				t.Fatalf("WithOverride accepted an unusable override %+v as %s", tc.o, got.HookInvocation())
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("refusal error = %q, want it to carry %q", err.Error(), tc.wantErr)
			}
			if !got.IsZero() {
				t.Fatalf("a refused override must not hand back a usable profile, got %+v", got)
			}
		})
	}
}
