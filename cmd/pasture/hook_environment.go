package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
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

	parsed := HookEnvironment{FaultPolicy: hostexit.FaultFailOpen}

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

	return parsed, nil
}
