package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/nativeresponse"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
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

// lifecycleFaultOutcomeClass is the value of the outcomeClass member of every
// fault record line. The file records FAULTS only: an invocation that emitted
// the host's continue bytes still failed to evaluate the event, and the class
// is what keeps that distinction readable after the fact.
const lifecycleFaultOutcomeClass = "fault"

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
		emitLifecycleOutcome(cmd, lifecycleOutcome(cmd, args,
			handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile()))
		return nil
	},
}

// lifecycleOutcome runs one hook invocation and returns the ONE outcome the
// host receives. Every path out of the command, including a recovered panic,
// goes through here, so there is no path that can leave silently.
//
// The barrier is the commit-to-emit boundary the handler calls after the
// durable receipt is committed and before the host is answered. Production
// passes the pass-through barrier; it is a parameter so the boundary can be
// held open and observed without a second code path.
//
// The budget carries the hook-invocation deadline. Production passes the
// production profile, and the zero profile reads as the production one, so a
// caller that supplies nothing still gets the tier the host budget requires.
//
// IT IS AN IN-PROCESS PARAMETER AND NOTHING MORE. There is NO environment
// variable and NO flag for it, and that absence is a refusal rather than a
// gap, because HANDING THIS DIAL TO A USER HANDS THE USER THE DEFECT BACK.
// Both directions of change harm the person who turns it:
//
//   - A user who SHORTENS the tier stops proving the budget the host actually
//     enforces. The hook would keep satisfying its own proof while it says
//     nothing at all about the window the host gives it.
//   - A user who LENGTHENS the tier can FREEZE THEIR OWN SESSION. That is the
//     exact failure this tier exists to prevent, and it is measured: with the
//     store held under a write lock the work below took about 31 seconds,
//     three times the smallest host budget of 10 seconds.
//
// A test may supply a shorter tier because a test is not a user: it observes
// the deadline PATH in this process, and the value it supplies never reaches
// a host. That distinction is what makes the seam safe, and it is pinned by
// an assertion that the command path passes the production tier.
//
// A supplied profile is NOT re-validated here, and it does not need to be:
// timeouts.New is the only constructor, its fields are unexported, and it
// refuses an out-of-order hook-invocation tier. So the only profile a caller
// can build without New is the zero one, which is normalized to production
// below. There is no third state to check for.
//
// The internal/errors category table is deliberately NOT consulted. That table
// maps a pasture error class to an operator exit code for a person at a
// terminal. This command answers to a HOST, whose exit codes mean "proceed",
// "report" and "block", and whose meaning comes from the event's declared
// failure mode, not from the class of the error.
func lifecycleOutcome(
	cmd *cobra.Command,
	args []string,
	barrier handlers.CommitBarrier,
	budget timeouts.Profile,
) (outcome hostexit.Outcome) {
	// The recover is installed FIRST, before the coordinates and the
	// environment are read, so a panic in either is a fault and not a process
	// crash. Until those reads finish the fault is described by the safe
	// defaults below: no harness, the observe-only policy and fail-open, which
	// is the weakest claim the command can make.
	var (
		coords       lifecycleCoordinates
		failure      = pastureruntime.LifecycleFailurePolicy{Mode: pastureruntime.FailureObserveOnly}
		policy       = hostexit.FaultFailOpen
		continuation = hostexit.EmptyContinuation()
	)
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = lifecycleFault(cmd, coords, failure, policy, continuation,
				hostexit.FaultStageNotRecorded, lifecyclePanicCause(coords, recovered))
		}
	}()

	coords = lifecycleCoordinatesFrom(cmd)
	failure = lifecycleFailurePolicy(coords)
	continuation = lifecycleContinuation(coords, failure)

	// The fault policy is read before the work starts, so a fault that happens
	// during the work is judged by the policy the user chose. A malformed
	// environment is itself a fault, and it is reported under the safe default.
	env, envErr := hookEnvironment()
	if envErr == nil {
		policy = env.FaultPolicy
	}

	if envErr != nil {
		return lifecycleFault(cmd, coords, failure, policy, continuation,
			hostexit.FaultStageNotRecorded, envErr)
	}

	if len(args) != 0 {
		return lifecycleFault(cmd, coords, failure, policy, continuation,
			hostexit.FaultStageNotRecorded, fmt.Errorf(
				"the hook received the unexpected positional arguments %q while handling event %q of harness %q; "+
					"hook coordinates are passed through flags only",
				args, coords.Event, coords.Harness))
	}

	// The WORK runs under the hook-invocation deadline. The HOST pays for a
	// hook that does not return: its session freezes while it waits. pasture
	// stops first, well inside the smallest host budget, and reports the expiry
	// as a fault, which fails open by default.
	//
	// The deadline is enforced HERE, around the work, and not only by handing a
	// context down. Layers below retry a locked SQLite database on their own
	// ceilings, which are longer than any host budget, and the deadline context
	// does not reach all of them: the store opener takes no context, so the
	// migrator below it runs to its own ceiling whatever this deadline says.
	// Measured on a database held under a write lock, the hook returned after
	// about 31 seconds, three times the Claude budget. Selecting on the deadline
	// bounds the invocation whatever the layers below do, and it is the ONLY
	// thing that bounds it today.
	//
	// The reporting path AFTER the select is outside the bound: mapping the
	// fault, appending one line to the fault record, and writing the streams.
	// That is a fixed number of local syscalls with no retry and no lock. If the
	// fault record ever grows a retry or a lock, it moves inside the bound.
	//
	// On expiry the work is ABANDONED. That is safe only because the process
	// reports the fault and exits immediately: the abandoned goroutine still
	// holds a store handle, so a future caller that runs this function
	// in-process and keeps running would leak it. An abandoned SQLite
	// transaction is rolled back when the process ends. What the abandonment
	// CANNOT know is whether the receipt committed first, so the fault is
	// reported with an unknown durable state rather than a false claim.
	//
	// The deadline arrives as a parameter, so an in-process proof can observe
	// this PATH in about a second instead of five. The reason there is no
	// environment variable and no flag for it is recorded where the parameter
	// is declared, above; it is a refusal, not a gap.
	//
	// The zero profile reads as the production one, so a caller that supplies
	// nothing still runs under the tier the host budget requires. It is
	// normalized HERE, below the recover, because nothing may run before the
	// recover is installed.
	if budget.IsZero() {
		budget = timeouts.ProductionProfile()
	}
	deadline := budget.HookInvocation()
	ctx, cancel := context.WithTimeout(cmd.Context(), deadline)
	defer cancel()

	// HookLifecycleNative is the single dispatch surface: it commits the
	// durable receipt and, only on the nil-error path, returns the exact
	// native continuation bytes this harness reads on stdout — so nothing is
	// written to stdout before the commit completes.
	completed := make(chan lifecycleWork, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				completed <- lifecycleWork{err: lifecyclePanicCause(coords, recovered)}
			}
		}()
		native, err := handlers.HookLifecycleNative(ctx, handlers.HookLifecycleInput{
			DBPath: flagDBPath, Harness: coords.Harness, Event: coords.Event,
			HostVersion: coords.HostVersion, Input: cmd.InOrStdin(),
			Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{},
			Barrier: barrier,
		})
		completed <- lifecycleWork{native: native, err: err}
	}()

	var work lifecycleWork
	select {
	case work = <-completed:
	case <-ctx.Done():
		return lifecycleFault(cmd, coords, failure, policy, continuation,
			hostexit.FaultStageRecordUnknown, fmt.Errorf(
				"the hook stopped waiting at its %s hook-invocation deadline and abandoned the work for event %q of harness %q "+
					"at host version %q, so the host is not left waiting; the usual reason is another writer holding the pasture "+
					"store, so find that writer or retry once it releases the store: %w",
				deadline,
				coords.Event, coords.Harness, coords.HostVersion, ctx.Err()))
	}

	if work.err != nil {
		// A fault raised AFTER the durable commit leaves an occurrence behind.
		// Saying "not recorded" there would send a maintainer to look in the
		// wrong place, so the stage follows the error and not the position.
		stage := hostexit.FaultStageNotRecorded
		if errors.Is(work.err, handlers.ErrLifecycleCommittedWithoutContinuation) {
			stage = hostexit.FaultStageRecordUnknown
		}
		return lifecycleFault(cmd, coords, failure, policy, continuation, stage, fmt.Errorf(
			"the hook could not evaluate event %q of harness %q at host version %q: %w",
			coords.Event, coords.Harness, coords.HostVersion, work.err))
	}
	native := work.native

	return hostexit.ForDecision(native, hostexit.ExitContinue, "")
}

// lifecycleContinuation resolves the bytes THIS host reads as "you may
// continue" for THIS event class. A fail-open fault emits them, because on two
// of the three harnesses a proceed is a byte shape and not an exit code: an
// empty stdout makes the generated OpenCode plugin throw inside the callback
// chain, which stops the user's tool call — the opposite of failing open.
//
// A harness this build has no response contract for has no bytes to write, so
// the empty continuation is used rather than a guessed shape. Such an
// invocation is refused by the handler anyway.
func lifecycleContinuation(
	coords lifecycleCoordinates,
	failure pastureruntime.LifecycleFailurePolicy,
) hostexit.Continuation {
	continuation, err := nativeresponse.FaultContinuation(coords.Harness, failure.Semantic)
	if err != nil {
		return hostexit.EmptyContinuation()
	}
	return continuation
}

// lifecycleWork is what one hook evaluation produced: the native continuation
// bytes, or the error that stopped it.
type lifecycleWork struct {
	native []byte
	err    error
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
	continuation hostexit.Continuation,
	stage hostexit.FaultStage,
	cause error,
) hostexit.Outcome {
	outcome, mapped := hostexit.ForFault(hostexit.Fault{
		Mode:         failure.Mode,
		Evidence:     failure.Evidence,
		Policy:       policy,
		Stage:        stage,
		Continuation: continuation,
		Cause:        cause,
	})
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

	recordLifecycleFault(cmd, coords, failure, policy, stage, outcome, cause)
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
	stage hostexit.FaultStage,
	outcome hostexit.Outcome,
	cause error,
) {
	path := lifecycleFaultRecordPath()
	if path == "" {
		return
	}

	line, err := json.Marshal(map[string]any{
		"recordedAt": time.Now().UTC().Format(time.RFC3339Nano),
		// outcomeClass is always "fault". It is written on every line so the
		// file cannot be mistaken for a record of decisions: emitting the
		// host's continue bytes under fail-open is NOT an evaluated proceed,
		// and this member is what says so where a later reader meets it.
		"outcomeClass":   lifecycleFaultOutcomeClass,
		"harness":        string(coords.Harness),
		"event":          coords.Event,
		"hostVersion":    coords.HostVersion,
		"failureMode":    failure.Mode.String(),
		"failureCitedBy": failure.Evidence.Source,
		"faultPolicy":    policy.String(),
		"faultStage":     stage.String(),
		"hostExit":       outcome.Exit.String(),
		// hostContinuation is the exact byte body the host was given so that
		// it could carry on. It is recorded because on OpenCode and Codex the
		// proceed is a byte shape, and a reader must be able to see that these
		// bytes were emitted WITHOUT an evaluation.
		"hostContinuation": string(outcome.Stdout),
		"cause":            cause.Error(),
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

// lifecycleFaultRecordPath puts the record beside the database THIS invocation
// would have used, so a maintainer finds it where the rest of the pasture state
// lives. It resolves the store path exactly as the store itself does — the
// --db flag first, then the same environment and layout rules — because a
// record written somewhere else is evidence nobody finds. A generated host hook
// passes no --db flag, so this is the path a real hook takes.
//
// THE FILE GROWS WITHOUT A BOUND. One line is appended per faulting invocation,
// about 500 bytes, and nothing rotates, trims or removes a line. A hook runs on
// every wired event, so a database that stays broken accumulates one line per
// event, silently, because a fail-open fault exits 0 and most hosts do not show
// the standard error of an exit-0 hook. Retention and reclaim are deliberately
// out of scope here; the file is named in AGENTS.md with the command to clear
// it so that somebody who never sees the diagnostic can still find it.
func lifecycleFaultRecordPath() string {
	path := flagDBPath
	if path == "" {
		path = tasks.DefaultDBPath()
	}
	directory := filepath.Dir(path)
	if directory == "" || directory == "." {
		return ""
	}
	return filepath.Join(directory, lifecycleFaultRecordFile)
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
		emitLifecycleOutcome(cmd, lifecycleFault(cmd, coords, failure, policy,
			lifecycleContinuation(coords, failure), hostexit.FaultStageNotRecorded, fmt.Errorf(
				"the hook could not parse its flags while handling event %q of harness %q; flag error: %w; "+
					"inspect the generated hook command and retry",
				coords.Event, coords.Harness, err)))
		return nil
	})
	hookCmd.AddCommand(hookLifecycleCmd)
}
