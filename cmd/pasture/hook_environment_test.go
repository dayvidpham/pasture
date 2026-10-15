package main

import (
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envMap builds an injected lookup from a fixture map. A key that is absent
// from the map is UNSET; a key present with "" is SET BUT EMPTY. The two are
// different inputs and the parser must tell them apart.
func envMap(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, present := values[name]
		return value, present
	}
}

// TestHookEnvironmentTreatsSetButEmptyAsUnset is the parser table. The
// load-bearing case is set-but-empty: a shell that exports an unset variable
// produces an empty string, so reading "" as an opt-in would make an ordinary
// shell habit block a user's session on any evaluation fault.
// WHAT IT VISITS: the values listed below, chosen because each is a shell
// habit that produces a non-obvious string — empty, whitespace, and mixed
// case around the two accepted words.
// WHAT IT DOES NOT READ: any other value. That the ACCEPTED set is exactly
// two words is held where that set is declared, not here.
func TestHookEnvironmentTreatsSetButEmptyAsUnset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		values     map[string]string
		want       HookEnvironment
		wantErrHas string
	}{
		{
			name:   "every variable unset",
			values: map[string]string{},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen, WriteLane: WriteLaneAuto},
		},
		{
			name:   "every variable set but empty",
			values: map[string]string{hookFailClosedEnv: "", hookCaptureDirEnv: "", hookActorIDEnv: "", hookInvocationTimeoutEnv: "", hookIngressTimeoutEnv: "", hookStartSliceTimeoutEnv: ""},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen, WriteLane: WriteLaneAuto},
		},
		{
			name:   "fail closed opted in",
			values: map[string]string{hookFailClosedEnv: "1"},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailClosed, WriteLane: WriteLaneAuto},
		},
		{
			name:   "fail closed opted out",
			values: map[string]string{hookFailClosedEnv: "0"},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen, WriteLane: WriteLaneAuto},
		},
		{
			name:       "fail closed with a word instead of a digit",
			values:     map[string]string{hookFailClosedEnv: "true"},
			wantErrHas: `PASTURE_HOOK_FAIL_CLOSED is set to "true"`,
		},
		{
			name:       "fail closed with a space",
			values:     map[string]string{hookFailClosedEnv: " 1"},
			wantErrHas: `PASTURE_HOOK_FAIL_CLOSED is set to " 1"`,
		},
		{
			name:       "a padded capture directory is refused, exactly as a padded actor identifier is",
			values:     map[string]string{hookCaptureDirEnv: "/var/tmp/pasture-captures "},
			wantErrHas: "blank or has leading or trailing space, so it is not an exact directory path",
		},
		{
			name:   "capture directory set",
			values: map[string]string{hookCaptureDirEnv: "/var/tmp/pasture-captures"},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen, WriteLane: WriteLaneAuto, CaptureDir: "/var/tmp/pasture-captures"},
		},
		{
			name:       "capture directory is only space",
			values:     map[string]string{hookCaptureDirEnv: "  "},
			wantErrHas: "blank or has leading or trailing space, so it is not an exact directory path",
		},
		{
			name:   "actor claim set",
			values: map[string]string{hookActorIDEnv: "worker-1"},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen, WriteLane: WriteLaneAuto, ActorClaim: "worker-1"},
		},
		{
			name:       "actor claim padded",
			values:     map[string]string{hookActorIDEnv: "worker-1 "},
			wantErrHas: "leading or trailing space",
		},
		{
			name:       "actor claim is only space",
			values:     map[string]string{hookActorIDEnv: " "},
			wantErrHas: "blank or has leading or trailing space",
		},
		{
			name: "all three set together",
			values: map[string]string{
				hookFailClosedEnv: "1",
				hookCaptureDirEnv: "/var/tmp/pasture-captures",
				hookActorIDEnv:    "worker-1",
			},
			want: HookEnvironment{
				FaultPolicy: hostexit.FaultFailClosed,
				WriteLane:   WriteLaneAuto,
				CaptureDir:  "/var/tmp/pasture-captures",
				ActorClaim:  "worker-1",
			},
		},
		{
			name:   "hook-invocation deadline lowered",
			values: map[string]string{hookInvocationTimeoutEnv: "3s"},
			want: HookEnvironment{
				FaultPolicy:    hostexit.FaultFailOpen,
				WriteLane:      WriteLaneAuto,
				BudgetOverride: timeouts.Override{HookInvocation: 3 * time.Second},
			},
		},
		{
			name: "all three time budgets set together",
			values: map[string]string{
				hookInvocationTimeoutEnv: "4s",
				hookIngressTimeoutEnv:    "900ms",
				hookStartSliceTimeoutEnv: "1500ms",
			},
			want: HookEnvironment{
				FaultPolicy: hostexit.FaultFailOpen,
				WriteLane:   WriteLaneAuto,
				BudgetOverride: timeouts.Override{
					HookInvocation: 4 * time.Second,
					Ingress:        900 * time.Millisecond,
					StartSlice:     1500 * time.Millisecond,
				},
			},
		},
		{
			name:       "hook-invocation deadline is not a duration",
			values:     map[string]string{hookInvocationTimeoutEnv: "soon"},
			wantErrHas: `PASTURE_HOOK_INVOCATION_TIMEOUT is set to "soon"`,
		},
		{
			name:       "ingress window is padded",
			values:     map[string]string{hookIngressTimeoutEnv: " 1s"},
			wantErrHas: `PASTURE_INGRESS_TIMEOUT is set to " 1s"`,
		},
		{
			name:       "start-slice window is zero",
			values:     map[string]string{hookStartSliceTimeoutEnv: "0s"},
			wantErrHas: `PASTURE_START_SLICE_TIMEOUT is set to "0s", which is not a positive duration`,
		},
		{
			name:       "hook-invocation deadline is negative",
			values:     map[string]string{hookInvocationTimeoutEnv: "-1s"},
			wantErrHas: "which is not a positive duration",
		},
		{
			name:   "write lane direct",
			values: map[string]string{hookWriteLaneEnv: "direct"},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen, WriteLane: WriteLaneDirect},
		},
		{
			name:   "write lane auto",
			values: map[string]string{hookWriteLaneEnv: "auto"},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen, WriteLane: WriteLaneAuto},
		},
		{
			name:       "write lane set to a word that is neither accepted value",
			values:     map[string]string{hookWriteLaneEnv: "always"},
			wantErrHas: `PASTURE_WRITE_LANE is set to "always"`,
		},
		{
			name:   "write lane socket override",
			values: map[string]string{hookWriteLaneSocketEnv: "/run/pasture/other.sock"},
			want: HookEnvironment{
				FaultPolicy:     hostexit.FaultFailOpen,
				WriteLane:       WriteLaneAuto,
				WriteLaneSocket: "/run/pasture/other.sock",
			},
		},
		{
			name:       "write lane socket is padded",
			values:     map[string]string{hookWriteLaneSocketEnv: "/run/pasture/other.sock "},
			wantErrHas: "blank or has leading or trailing space, so it is not an exact socket path",
		},
		{
			name:   "set-but-empty write lane socket is not an opt-in",
			values: map[string]string{hookWriteLaneSocketEnv: ""},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen, WriteLane: WriteLaneAuto},
		},
		{
			name:   "set-but-empty write lane keeps the default auto mode",
			values: map[string]string{hookWriteLaneEnv: ""},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen, WriteLane: WriteLaneAuto},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := HookEnvironmentFromOS(envMap(test.values))

			if test.wantErrHas != "" {
				require.Error(t, err, "a value that is present but not understood must be refused")
				assert.Contains(t, err.Error(), test.wantErrHas)
				assertActionableEnvError(t, err)
				assert.Equal(t, HookEnvironment{}, got,
					"a refused environment must not hand back a usable fault policy")
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

// TestHookEnvironmentRefusesAMissingLookup pins the injected seam. A nil lookup
// is a wiring mistake, and guessing an environment here would decide whether a
// user's tool call is refused.
func TestHookEnvironmentRefusesAMissingLookup(t *testing.T) {
	t.Parallel()

	got, err := HookEnvironmentFromOS(nil)
	require.Error(t, err)
	assert.Equal(t, HookEnvironment{}, got)
	assertActionableEnvError(t, err)
}

// TestHookEnvironmentZeroValueIsUnusable pins that the parser is the only way
// in. The zero value carries an unset fault policy, and the fault mapper
// refuses an unset policy, so code that skips the parser fails loudly instead
// of inheriting fail-open behaviour it never chose.
func TestHookEnvironmentZeroValueIsUnusable(t *testing.T) {
	t.Parallel()

	var zero HookEnvironment
	assert.False(t, zero.FaultPolicy.IsValid(),
		"the zero hook environment must not carry a usable fault policy")
}

// TestHookEnvironmentReadsTheRealProcessEnvironment is the ONE serial test for
// this seam. It proves that the production entry point, which the hook command
// calls, reads the real process environment rather than a fixture. t.Setenv
// mutates process-global state, so this test cannot run in parallel; it is the
// only env-reading test here, so it never slows the suite.
func TestHookEnvironmentReadsTheRealProcessEnvironment(t *testing.T) {
	t.Setenv(hookFailClosedEnv, "1")
	t.Setenv(hookCaptureDirEnv, "/var/tmp/pasture-captures")
	t.Setenv(hookActorIDEnv, "worker-1")
	t.Setenv(hookInvocationTimeoutEnv, "3s")
	t.Setenv(hookIngressTimeoutEnv, "800ms")
	t.Setenv(hookStartSliceTimeoutEnv, "1500ms")
	t.Setenv(hookWriteLaneEnv, "direct")
	t.Setenv(hookWriteLaneSocketEnv, "/run/pasture/lane.sock")

	got, err := hookEnvironment()
	require.NoError(t, err)
	assert.Equal(t, HookEnvironment{
		FaultPolicy: hostexit.FaultFailClosed,
		WriteLane:   WriteLaneDirect,
		CaptureDir:  "/var/tmp/pasture-captures",
		ActorClaim:  "worker-1",
		BudgetOverride: timeouts.Override{
			HookInvocation: 3 * time.Second,
			Ingress:        800 * time.Millisecond,
			StartSlice:     1500 * time.Millisecond,
		},
		WriteLaneSocket: "/run/pasture/lane.sock",
	}, got, "the production entry point must read the real process environment")

	t.Setenv(hookFailClosedEnv, "")
	t.Setenv(hookInvocationTimeoutEnv, "")
	t.Setenv(hookIngressTimeoutEnv, "")
	t.Setenv(hookStartSliceTimeoutEnv, "")
	t.Setenv(hookWriteLaneEnv, "")
	t.Setenv(hookWriteLaneSocketEnv, "")
	empty, err := hookEnvironment()
	require.NoError(t, err)
	assert.Equal(t, hostexit.FaultFailOpen, empty.FaultPolicy,
		"an exported but empty variable must read as unset in the real environment too")
	assert.True(t, empty.BudgetOverride.IsZero(),
		"an exported but empty time-budget knob must read as unset, keeping the default tiers")
	assert.Equal(t, WriteLaneAuto, empty.WriteLane,
		"an exported but empty write-lane mode must read as unset, keeping the ask-the-daemon default")
	assert.Empty(t, empty.WriteLaneSocket,
		"an exported but empty write-lane socket must read as unset, keeping the derived path")
}

func assertActionableEnvError(t *testing.T, err error) {
	t.Helper()

	message := err.Error()
	assert.Contains(t, message, "HookEnvironmentFromOS", "the reader must be told WHERE it failed")
	assert.Contains(t, message, "cmd/pasture/hook_environment.go", "the reader must be told which file")
	assert.Contains(t, message, "while the hook was starting", "the reader must be told WHEN it failed")
	// THE SAME THREE LITERALS UNDER THE SAME CLASS SENTENCE, in the second of
	// two identical guards. The archetype fix reached one and not this one, so
	// the forms it could not see were still green here: the population is
	// derived from the AGENTS.md rule now, exactly as its sibling's is.
	assertNoInternalReferenceInPackage(t, "the hook environment refusal", message)
}
