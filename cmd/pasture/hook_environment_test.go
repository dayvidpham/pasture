package main

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
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
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen},
		},
		{
			name:   "every variable set but empty",
			values: map[string]string{hookFailClosedEnv: "", hookCaptureDirEnv: "", hookActorIDEnv: ""},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen},
		},
		{
			name:   "fail closed opted in",
			values: map[string]string{hookFailClosedEnv: "1"},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailClosed},
		},
		{
			name:   "fail closed opted out",
			values: map[string]string{hookFailClosedEnv: "0"},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen},
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
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen, CaptureDir: "/var/tmp/pasture-captures"},
		},
		{
			name:       "capture directory is only space",
			values:     map[string]string{hookCaptureDirEnv: "  "},
			wantErrHas: "blank or has leading or trailing space, so it is not an exact directory path",
		},
		{
			name:   "actor claim set",
			values: map[string]string{hookActorIDEnv: "worker-1"},
			want:   HookEnvironment{FaultPolicy: hostexit.FaultFailOpen, ActorClaim: "worker-1"},
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
				CaptureDir:  "/var/tmp/pasture-captures",
				ActorClaim:  "worker-1",
			},
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

	got, err := hookEnvironment()
	require.NoError(t, err)
	assert.Equal(t, HookEnvironment{
		FaultPolicy: hostexit.FaultFailClosed,
		CaptureDir:  "/var/tmp/pasture-captures",
		ActorClaim:  "worker-1",
	}, got, "the production entry point must read the real process environment")

	t.Setenv(hookFailClosedEnv, "")
	empty, err := hookEnvironment()
	require.NoError(t, err)
	assert.Equal(t, hostexit.FaultFailOpen, empty.FaultPolicy,
		"an exported but empty variable must read as unset in the real environment too")
}

func assertActionableEnvError(t *testing.T, err error) {
	t.Helper()

	message := err.Error()
	assert.Contains(t, message, "HookEnvironmentFromOS", "the reader must be told WHERE it failed")
	assert.Contains(t, message, "cmd/pasture/hook_environment.go", "the reader must be told which file")
	assert.Contains(t, message, "while the hook was starting", "the reader must be told WHEN it failed")
	for _, internal := range []string{"SLICE-", "PROPOSAL-", "aura-plugins-"} {
		assert.NotContains(t, message, internal,
			"user-visible text must not carry an internal process reference")
	}
}
