package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// Environment variables the lifecycle hook reads. They are named here once so
// the parser, the diagnostics and the tests cannot drift apart.
const (
	// hookFailClosedEnv opts an evaluation FAULT into blocking the host. It
	// accepts exactly "1" (fail closed) and "0" (fail open).
	//
	// It refuses through the process EXIT CODE, so it reaches only the hosts
	// that read one as a refusal. Fail-closed has no channel on OpenCode named
	// callbacks until the typed refusal object exists; such an invocation
	// continues, and the fault diagnostic says so.
	hookFailClosedEnv = "PASTURE_HOOK_FAIL_CLOSED"
	// hookCaptureDirEnv names a directory OUTSIDE this repository where the
	// hook writes captured host payloads.
	hookCaptureDirEnv = "PASTURE_CAPTURE_DIR"
	// hookActorIDEnv carries the actor a host session claims at session start.
	hookActorIDEnv = "PASTURE_ACTOR_ID"
	// hookInvocationTimeoutEnv tunes the hook-invocation deadline: how long ONE
	// whole lifecycle hook invocation may run before it reports a fault. It is
	// the budget the HOST pays for, so lowering it makes a slow store fail fast
	// (the gate is left unevaluated and the host is answered fail-open) instead
	// of stalling for the whole window; raising it only helps on a fast store,
	// because a hook that outruns the host's own budget freezes the session.
	hookInvocationTimeoutEnv = "PASTURE_HOOK_INVOCATION_TIMEOUT"
	// hookIngressTimeoutEnv tunes one lifecycle receipt append, including its
	// lock retries. It must stay strictly above the SQLite busy timeout and
	// strictly below the hook-invocation deadline.
	hookIngressTimeoutEnv = "PASTURE_INGRESS_TIMEOUT"
	// hookStartSliceTimeoutEnv tunes how long a slice sub-workflow waits for its
	// start_slice signal. It must stay strictly above the SQLite busy timeout and
	// strictly below the hook-invocation deadline. The lifecycle hook command
	// never reaches this wait: it is read and validated with its siblings so the
	// ordering invariant covers it, and its consumer is the slice workflow.
	hookStartSliceTimeoutEnv = "PASTURE_START_SLICE_TIMEOUT"
	// hookWriteLaneEnv selects whether the hook asks the daemon's write lane to
	// record its event. It accepts exactly "auto" (the default: ask the daemon,
	// and write directly when it is unreachable) and "direct" (never ask: write
	// in-process exactly as a build without a lane does).
	hookWriteLaneEnv = "PASTURE_WRITE_LANE"
	// hookWriteLaneSocketEnv overrides the socket the hook asks. It exists for
	// an operator who runs the daemon against a store the hook cannot derive
	// from its own flags; the derived path is the default and needs no setting.
	hookWriteLaneSocketEnv = "PASTURE_WRITE_LANE_SOCKET"
)

// WriteLaneMode is the operator's choice of how the hook records its event.
type WriteLaneMode string

const (
	// WriteLaneAuto asks the daemon's lane and falls back to the direct write
	// path when it cannot be reached. It is the default.
	WriteLaneAuto WriteLaneMode = "auto"
	// WriteLaneDirect never asks, and writes in-process exactly as a build
	// without a lane does.
	WriteLaneDirect WriteLaneMode = "direct"
)

// HookEnvironment is the parsed process environment of one lifecycle hook
// invocation. It is a value, so it is passed as a parameter and never read
// again deeper in the call stack.
//
// The zero value is deliberately unusable: FaultPolicy is unset, and the fault
// mapper refuses an unset policy. A caller therefore cannot skip the parser and
// silently inherit fail-open behaviour it never chose.
type HookEnvironment struct {
	// FaultPolicy says what an evaluation fault does to the host. It defaults
	// to fail-open, so a broken hook does not stop the user working.
	FaultPolicy hostexit.FaultPolicy
	// CaptureDir is the directory host payloads are captured into, or "" when
	// capture is off. This parser only reads the value; the capture sink owns
	// the rules about which directories are acceptable.
	CaptureDir string
	// ActorClaim is the actor identifier a host session claims, or "" when the
	// session makes no claim.
	ActorClaim string
	// BudgetOverride carries the operator's requested substitutions for the
	// hook-invocation deadline and the caller windows beneath it. The zero
	// value asks for no change, so an unset environment keeps the production
	// profile exactly. The values are validated against the ordered hierarchy
	// when they are applied to a base profile (see timeouts.Profile.WithOverride),
	// so an inverted combination is refused rather than clamped.
	BudgetOverride timeouts.Override
	// WriteLane is whether the hook asks the daemon's write lane. It defaults
	// to WriteLaneAuto, so a hook with no environment set asks the daemon and
	// writes directly when it cannot be reached.
	WriteLane WriteLaneMode
	// WriteLaneSocket overrides the socket path the hook asks, or "" to derive
	// it from the store path. A value here is an exact path, like every other
	// path this parser reads.
	WriteLaneSocket string
}

// hookEnvironment is the production entry point. It reads the real process
// environment and is the only place that names os.LookupEnv, so the parser
// itself stays pure and testable in parallel.
func hookEnvironment() (HookEnvironment, error) {
	return HookEnvironmentFromOS(os.LookupEnv)
}

// HookEnvironmentFromOS parses one lifecycle hook environment through the
// injected lookup.
//
// SET-BUT-EMPTY IS NOT AN OPT-IN. An empty value is exactly as good as an unset
// one: an empty PASTURE_HOOK_FAIL_CLOSED does not fail closed, and an empty
// PASTURE_CAPTURE_DIR is not a capture directory. A shell that exports an unset
// variable produces an empty string, and reading that as an opt-in would turn a
// harmless shell habit into a blocked session.
//
// A value that is present but not understood is refused with an actionable
// error instead of being guessed, because guessing here decides whether the
// user's tool call is refused.
func HookEnvironmentFromOS(lookup func(string) (string, bool)) (HookEnvironment, error) {
	if lookup == nil {
		return HookEnvironment{}, fmt.Errorf(
			"cannot parse the lifecycle hook environment because no environment lookup was supplied; " +
				"this happened in HookEnvironmentFromOS (cmd/pasture/hook_environment.go) while the hook " +
				"was starting, before any host payload was read; the hook cannot decide its fault policy " +
				"without one; pass os.LookupEnv, or use hookEnvironment() which supplies it")
	}

	parsed := HookEnvironment{FaultPolicy: hostexit.FaultFailOpen, WriteLane: WriteLaneAuto}

	if raw, present := lookup(hookWriteLaneEnv); present && raw != "" {
		switch WriteLaneMode(raw) {
		case WriteLaneAuto, WriteLaneDirect:
			parsed.WriteLane = WriteLaneMode(raw)
		default:
			return HookEnvironment{}, fmt.Errorf(
				"%s is set to %q, which is not one of the two accepted values %s and %s; "+
					"this happened in HookEnvironmentFromOS (cmd/pasture/hook_environment.go) while the hook "+
					"was starting, before any host payload was read; the hook will not guess whether you asked "+
					"it to write through the daemon's single-writer lane or directly to the store; "+
					"set %s=%s to ask the daemon and write directly when it is unreachable, "+
					"set %s=%s to never ask, or unset it to keep the default",
				hookWriteLaneEnv, raw, WriteLaneAuto, WriteLaneDirect,
				hookWriteLaneEnv, WriteLaneAuto, hookWriteLaneEnv, WriteLaneDirect)
		}
	}

	if raw, present := lookup(hookWriteLaneSocketEnv); present && raw != "" {
		if strings.TrimSpace(raw) != raw || strings.TrimSpace(raw) == "" {
			return HookEnvironment{}, fmt.Errorf(
				"%s is set to %q, which is blank or has leading or trailing space, so it is not an exact "+
					"socket path; this happened in HookEnvironmentFromOS (cmd/pasture/hook_environment.go) while "+
					"the hook was starting, before any host payload was read; a padded path names a different "+
					"socket from the one you meant, so the hook would ask a daemon that is not there and fall "+
					"back without you learning why; set %s to the exact socket path, or unset it to derive the "+
					"path from the store",
				hookWriteLaneSocketEnv, raw, hookWriteLaneSocketEnv)
		}
		parsed.WriteLaneSocket = raw
	}

	if raw, present := lookup(hookFailClosedEnv); present && raw != "" {
		switch raw {
		case "1":
			parsed.FaultPolicy = hostexit.FaultFailClosed
		case "0":
			parsed.FaultPolicy = hostexit.FaultFailOpen
		default:
			return HookEnvironment{}, fmt.Errorf(
				"%s is set to %q, which is not one of the two accepted values 1 and 0; "+
					"this happened in HookEnvironmentFromOS (cmd/pasture/hook_environment.go) while the hook "+
					"was starting, before any host payload was read; the hook will not guess whether you "+
					"asked it to stop your session on an evaluation fault; "+
					"set %s=1 to block the host on a fault where the host refuses by process exit code, "+
					"set %s=0 to let the host continue, or unset it "+
					"to keep the default, which lets the host continue",
				hookFailClosedEnv, raw, hookFailClosedEnv, hookFailClosedEnv)
		}
	}

	// A padded path is refused for the same reason a padded identifier is: both
	// are EXACT strings naming something outside pasture, and a trailing space
	// silently makes a different directory. The two variables are treated the
	// same way on purpose; the capture sink owns which directories are
	// acceptable, but neither value may arrive inexact.
	if raw, present := lookup(hookCaptureDirEnv); present && raw != "" {
		if strings.TrimSpace(raw) != raw || strings.TrimSpace(raw) == "" {
			return HookEnvironment{}, fmt.Errorf(
				"%s is set to %q, which is blank or has leading or trailing space, so it is not an exact "+
					"directory path; this happened in HookEnvironmentFromOS "+
					"(cmd/pasture/hook_environment.go) while the hook was starting, before any host payload "+
					"was read; a padded path names a different directory from the one you meant, so captured "+
					"payloads would be written somewhere you would not look; set %s to the exact directory "+
					"path outside this repository, or unset it to turn capture off",
				hookCaptureDirEnv, raw, hookCaptureDirEnv)
		}
		parsed.CaptureDir = raw
	}

	if raw, present := lookup(hookActorIDEnv); present && raw != "" {
		if strings.TrimSpace(raw) != raw || strings.TrimSpace(raw) == "" {
			return HookEnvironment{}, fmt.Errorf(
				"%s is set to %q, which is blank or has leading or trailing space, so it is not an exact "+
					"actor identifier; this happened in HookEnvironmentFromOS "+
					"(cmd/pasture/hook_environment.go) while the hook was starting, before any host payload "+
					"was read; an inexact identifier would attribute this session to the wrong actor or to "+
					"none; set %s to the exact actor identifier, or unset it to make no claim",
				hookActorIDEnv, raw, hookActorIDEnv)
		}
		parsed.ActorClaim = raw
	}

	// The three time-budget knobs are parsed together and each is refused on its
	// own terms: an unset or empty value keeps the default, a malformed one is
	// refused instead of guessed, and a non-positive one is refused rather than
	// read as "unset" — a zero is a value the operator typed, not an absence.
	// The ORDERING between the three is not checked here, because the base
	// profile is a parameter the parser does not see; it is checked where they
	// are applied, by timeouts.New through Profile.WithOverride.
	for _, knob := range []struct {
		env    string
		target *time.Duration
	}{
		{hookInvocationTimeoutEnv, &parsed.BudgetOverride.HookInvocation},
		{hookIngressTimeoutEnv, &parsed.BudgetOverride.Ingress},
		{hookStartSliceTimeoutEnv, &parsed.BudgetOverride.StartSlice},
	} {
		raw, present := lookup(knob.env)
		if !present || raw == "" {
			continue
		}
		d, err := parseHookDuration(knob.env, raw)
		if err != nil {
			return HookEnvironment{}, err
		}
		*knob.target = d
	}

	return parsed, nil
}

// parseHookDuration reads one Go duration from a hook time-budget knob. It
// refuses a padded, malformed or non-positive value with an actionable error
// and never guesses: guessing here changes how long the host waits for a hook,
// which is the one decision the operator set the knob to make.
func parseHookDuration(name, raw string) (time.Duration, error) {
	if strings.TrimSpace(raw) != raw || strings.TrimSpace(raw) == "" {
		return 0, fmt.Errorf(
			"%s is set to %q, which is blank or has leading or trailing space, so it is not an exact "+
				"duration; this happened in HookEnvironmentFromOS "+
				"(cmd/pasture/hook_environment.go) while the hook was starting, before any host payload "+
				"was read; a padded value is not the duration you meant; set %s to a Go duration such as "+
				"750ms or 3s, or unset it to keep the default",
			name, raw, name)
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf(
			"%s is set to %q, which is not a Go duration; this happened in HookEnvironmentFromOS "+
				"(cmd/pasture/hook_environment.go) while the hook was starting, before any host payload "+
				"was read; the hook will not guess how long the host should wait for it; set %s to a Go "+
				"duration such as 750ms or 3s, or unset it to keep the default: %w",
			name, raw, name, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf(
			"%s is set to %q, which is not a positive duration; this happened in HookEnvironmentFromOS "+
				"(cmd/pasture/hook_environment.go) while the hook was starting, before any host payload "+
				"was read; a zero or negative budget would expire before the work starts; set %s to a "+
				"positive Go duration such as 750ms or 3s, or unset it to keep the default",
			name, raw, name)
	}
	return d, nil
}
