package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

type lifecycleCLIClock struct{}

func (lifecycleCLIClock) Now() time.Time { return time.Now() }

type lifecycleCLIOperations struct{}

func (lifecycleCLIOperations) NewOperationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create lifecycle operation identity from the operating system random source: %w", err)
	}
	return "pasture.lifecycle." + hex.EncodeToString(value[:]), nil
}

// lifecycleFaultRecordFile is the append-only record of hook evaluation faults.
// It sits BESIDE the database and not inside it on purpose: the most common
// fault is that the database could not be opened or written, and a record that
// needs the failing store would be lost exactly when it is needed.
const lifecycleFaultRecordFile = "lifecycle-faults.jsonl"

// lifecycleCoordinates are the three host-supplied coordinates of one hook
// invocation. They are read before anything else, because the declared failure
// mode of the event decides what every later failure tells the host.
type lifecycleCoordinates struct {
	Harness     ir.HarnessID
	Event       string
	HostVersion string
}

// lifecycleFailurePolicy resolves the declared failure behaviour of the named
// event.
//
// An event this build does not declare is treated as OBSERVE-ONLY. A build that
// cannot name the event cannot know that the host blocks on it, and guessing
// "blocking" would let a stale generated hook stop a user's session.
func lifecycleFailurePolicy(coords lifecycleCoordinates) pastureruntime.LifecycleFailurePolicy {
	if policy, found := pastureruntime.LookupLifecycleFailure(coords.Harness, coords.Event); found {
		return policy
	}
	return pastureruntime.LifecycleFailurePolicy{Mode: pastureruntime.FailureObserveOnly}
}

var hookLifecycleCmd = &cobra.Command{
	Use:   "lifecycle",
	Short: "Record a native harness lifecycle event",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		emitLifecycleOutcome(cmd, lifecycleOutcome(cmd, args))
		return nil
	},
}

// lifecycleOutcome runs one hook invocation and returns the ONE outcome the
// host receives. Every path out of the command, including a recovered panic,
// goes through here, so there is no path that can leave silently.
//
// The internal/errors category table is deliberately NOT consulted. That table
// maps a pasture error class to an operator exit code for a person at a
// terminal. This command answers to a HOST, whose exit codes mean "proceed",
// "report" and "block", and whose meaning comes from the event's declared
// failure mode, not from the class of the error.
func lifecycleOutcome(cmd *cobra.Command, args []string) (outcome hostexit.Outcome) {
	coords := lifecycleCoordinatesFrom(cmd)
	failure := lifecycleFailurePolicy(coords)

	// The fault policy is read before the work starts, so a fault that happens
	// during the work is judged by the policy the user chose. A malformed
	// environment is itself a fault, and it is reported under the safe default.
	policy := hostexit.FaultFailOpen
	env, envErr := hookEnvironment()
	if envErr == nil {
		policy = env.FaultPolicy
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = lifecycleFault(cmd, coords, failure, policy, lifecyclePanicCause(coords, recovered))
		}
	}()

	if envErr != nil {
		return lifecycleFault(cmd, coords, failure, policy, envErr)
	}

	if len(args) != 0 {
		return lifecycleFault(cmd, coords, failure, policy, fmt.Errorf(
			"the hook received the unexpected positional arguments %q while handling event %q of harness %q; "+
				"hook coordinates are passed through flags only",
			args, coords.Event, coords.Harness))
	}

	// HookLifecycleNative is the single dispatch surface: it commits the
	// durable receipt and, only on the nil-error path, returns the exact
	// native continuation bytes this harness reads on stdout — so nothing is
	// written to stdout before the commit completes.
	native, err := handlers.HookLifecycleNative(cmd.Context(), handlers.HookLifecycleInput{
		DBPath: flagDBPath, Harness: coords.Harness, Event: coords.Event,
		HostVersion: coords.HostVersion, Input: cmd.InOrStdin(),
		Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{},
	})
	if err != nil {
		return lifecycleFault(cmd, coords, failure, policy, fmt.Errorf(
			"the hook could not evaluate event %q of harness %q at host version %q: %w",
			coords.Event, coords.Harness, coords.HostVersion, err))
	}

	return hostexit.ForDecision(native, hostexit.ExitContinue, "")
}

// lifecyclePanicCause describes a recovered panic as the fault it is. A panic
// means the event was never evaluated, so the host must be told, and the fault
// policy decides whether the operation is refused.
func lifecyclePanicCause(coords lifecycleCoordinates, recovered any) error {
	return fmt.Errorf("the hook panicked while handling event %q of harness %q: %v",
		coords.Event, coords.Harness, recovered)
}

// lifecycleCoordinatesFrom reads the three coordinate flags. A flag that did
// not parse reads as empty, which resolves to the observe-only policy, so a
// broken invocation reports and lets the host continue.
func lifecycleCoordinatesFrom(cmd *cobra.Command) lifecycleCoordinates {
	harness, _ := cmd.Flags().GetString("harness")
	event, _ := cmd.Flags().GetString("event")
	hostVersion, _ := cmd.Flags().GetString("host-version")
	return lifecycleCoordinates{Harness: ir.HarnessID(harness), Event: event, HostVersion: hostVersion}
}

// lifecycleFault maps one internal fault to the host outcome and records it
// durably. The record is BEST EFFORT: if it cannot be written, the fault is
// still reported on stderr and the outcome does not change, because a host must
// not be blocked by pasture's own bookkeeping.
func lifecycleFault(
	cmd *cobra.Command,
	coords lifecycleCoordinates,
	failure pastureruntime.LifecycleFailurePolicy,
	policy hostexit.FaultPolicy,
	cause error,
) hostexit.Outcome {
	outcome, mapped := hostexit.ForFault(failure.Mode, failure.Evidence, policy, cause)
	if !mapped {
		// Unreachable with a real cause, a declared mode and a parsed policy.
		// It is handled anyway, because the one thing this command may never do
		// is exit 0 in silence: the host would read that as a proceed.
		outcome = hostexit.Outcome{
			Exit: hostexit.ExitNonBlockingError,
			Stderr: fmt.Sprintf(
				"pasture hook lifecycle could not classify a fault on event %q of harness %q, "+
					"because the declared failure mode or the fault policy was not usable; "+
					"this happened in lifecycleFault (cmd/pasture/hook_lifecycle.go) after the event "+
					"coordinates were read; the host is not blocked and this event was not evaluated; "+
					"report this with the cause below, which is: %v",
				coords.Event, coords.Harness, cause),
		}
	}

	recordLifecycleFault(cmd, coords, failure, policy, outcome, cause)
	return outcome
}

// recordLifecycleFault appends one JSON line describing the fault. Every error
// here is swallowed on purpose: the record is evidence for a maintainer, never
// a condition of the host outcome.
func recordLifecycleFault(
	cmd *cobra.Command,
	coords lifecycleCoordinates,
	failure pastureruntime.LifecycleFailurePolicy,
	policy hostexit.FaultPolicy,
	outcome hostexit.Outcome,
	cause error,
) {
	path := lifecycleFaultRecordPath()
	if path == "" {
		return
	}

	line, err := json.Marshal(map[string]any{
		"recordedAt":     time.Now().UTC().Format(time.RFC3339Nano),
		"harness":        string(coords.Harness),
		"event":          coords.Event,
		"hostVersion":    coords.HostVersion,
		"failureMode":    failure.Mode.String(),
		"failureCitedBy": failure.Evidence.Source,
		"faultPolicy":    policy.String(),
		"hostExit":       outcome.Exit.String(),
		"cause":          cause.Error(),
	})
	if err != nil {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"pasture hook lifecycle could not open its fault record at %s: %v; "+
				"the fault above is reported on this stream only\n", path, err)
		return
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"pasture hook lifecycle could not append to its fault record at %s: %v; "+
				"the fault above is reported on this stream only\n", path, err)
	}
}

// lifecycleFaultRecordPath puts the record beside the database the invocation
// used, so a maintainer finds it where the rest of the pasture state lives.
func lifecycleFaultRecordPath() string {
	if flagDBPath != "" {
		return filepath.Join(filepath.Dir(flagDBPath), lifecycleFaultRecordFile)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "pasture", lifecycleFaultRecordFile)
}

// emitLifecycleOutcome is the ONLY writer of this command's host-facing bytes
// and the only place it leaves the process. stdout carries the native
// continuation, stderr carries the diagnostic, and the process exit code is the
// one the outcome names.
func emitLifecycleOutcome(cmd *cobra.Command, outcome hostexit.Outcome) {
	if len(outcome.Stdout) > 0 {
		if _, err := cmd.OutOrStdout().Write(outcome.Stdout); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"pasture hook lifecycle could not write its committed host continuation: %v; "+
					"this happened in emitLifecycleOutcome (cmd/pasture/hook_lifecycle.go) after the "+
					"durable commit; the event was recorded but the host received no continuation; "+
					"inspect the database and retry the hook input\n", err)
		}
	}
	if outcome.Stderr != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), outcome.Stderr)
	}

	code, known := outcome.Exit.Code()
	if !known {
		// An unset exit status means a path returned no decision at all. It
		// must never become a silent exit 0.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"pasture hook lifecycle produced no exit decision for this invocation; "+
				"this happened in emitLifecycleOutcome (cmd/pasture/hook_lifecycle.go) after the hook ran; "+
				"the host is not blocked, and the event may not have been recorded; "+
				"report this, and retry the hook input\n")
		exitWithCode(1)
		return
	}
	if code != 0 {
		exitWithCode(code)
	}
}

func init() {
	flags := hookLifecycleCmd.Flags()
	flags.String("harness", "", "Native harness whose payload is on standard input (required)")
	flags.String("event", "", "Native event this generated hook is registered for (required)")
	flags.String("host-version", "", "Observed native host version to retain with this occurrence (required)")
	hookLifecycleCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		if cmd != hookLifecycleCmd {
			return err
		}
		// A flag error happens before RunE, so it goes through the same fault
		// path here. The coordinates are whatever parsed before the bad flag;
		// when the event did not parse, the observe-only policy applies and the
		// host continues.
		coords := lifecycleCoordinatesFrom(cmd)
		failure := lifecycleFailurePolicy(coords)
		policy := hostexit.FaultFailOpen
		if env, envErr := hookEnvironment(); envErr == nil {
			policy = env.FaultPolicy
		}
		emitLifecycleOutcome(cmd, lifecycleFault(cmd, coords, failure, policy, fmt.Errorf(
			"the hook could not parse its flags while handling event %q of harness %q; flag error: %w; "+
				"inspect the generated hook command and retry",
			coords.Event, coords.Harness, err)))
		return nil
	})
	hookCmd.AddCommand(hookLifecycleCmd)
}
