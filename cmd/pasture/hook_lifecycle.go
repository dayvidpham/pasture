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
	"strings"
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
	return pastureruntime.LifecycleFailurePolicy{
		Mode:         pastureruntime.FailureObserveOnly,
		DeclaredMode: pastureruntime.FailureObserveOnly,
	}
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
		coords  lifecycleCoordinates
		failure = pastureruntime.LifecycleFailurePolicy{
			Mode:         pastureruntime.FailureObserveOnly,
			DeclaredMode: pastureruntime.FailureObserveOnly,
		}
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
// empty stdout is not an answer the OpenCode plugin can read.
//
// The plugin this build generates reports "did not evaluate" and continues. The
// bytes are still emitted, for a reason that does not expire: PASTURE CANNOT
// KNOW WHICH PLUGIN IS INSTALLED, and an ALREADY-INSTALLED OLDER ONE STILL
// THROWS inside the callback chain and stops the user's tool call — the
// opposite of failing open.
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
	rawFault := hostexit.Fault{
		Mode:         failure.Mode,
		DeclaredMode: failure.DeclaredMode,
		Evidence:     failure.Evidence,
		Policy:       policy,
		Stage:        stage,
		Continuation: continuation,
		Cause:        cause,
	}
	unusableFaultInputs := rawFault.UnusableInputs()
	outcome, mapped := hostexit.ForFault(rawFault)
	if !mapped {
		// Unreachable with a real cause, a declared mode and a parsed policy.
		// It is handled anyway, because the one thing this command may never do
		// is exit 0 in silence: the host would read that as a proceed.
		//
		// The message NAMES THE INPUTS THAT WERE NOT USABLE rather than
		// guessing at one. Six inputs can produce this refusal, and the list
		// comes from the same function the refusal itself uses, so the sentence
		// cannot describe a different condition from the one that happened.
		outcome = hostexit.Outcome{
			Exit:   hostexit.ExitNonBlockingError,
			Stderr: unclassifiableFaultDiagnostic(coords, unusableFaultInputs, cause),
		}
	}

	recordLifecycleFault(cmd, coords, failure, policy, stage, outcome, unusableFaultInputs, cause)
	return outcome
}

// unclassifiableFaultDiagnostic composes the message for a fault the exit
// authority could not map.
//
// It NAMES THE INPUTS THAT WERE NOT USABLE. The list is produced by
// hostexit.Fault.UnusableInputs, the same function the refusal itself uses, so
// the sentence cannot describe a condition other than the one that happened.
// The message used to say "the declared failure mode or the fault policy was
// not usable", which names one field of six and reads as an accusation against
// the declared mode in particular.
//
// It also says what THIS exit means on a throwing host. This arm is the one
// fault path that leaves with exit 1, and the pasture-generated OpenCode plugin
// reads any non-zero exit as a broken installation and throws. Nothing catches
// that throw on a GATE callback, so the user's tool call is stopped there. An
// OBSERVATION callback CATCHES it and only logs, so nothing is stopped on those
// rows. A message that said only "the host is not blocked" was true of
// pasture's intent and false of what a user on a gate row would see; a message
// that said every row is stopped would be false of the observation rows, so the
// sentence names the gate callbacks.
func unclassifiableFaultDiagnostic(coords lifecycleCoordinates, unusable []string, cause error) string {
	return fmt.Sprintf(
		"pasture hook lifecycle could not classify a fault on event %q of harness %q, "+
			"because %s. "+
			"This happened in lifecycleFault (cmd/pasture/hook_lifecycle.go) after the event "+
			"coordinates were read; pasture did not ask the host to block, and this event was "+
			"not evaluated; the hook still leaves with exit 1, and a host that reads any "+
			"non-zero exit as a broken pasture installation stops the tool call on its GATE "+
			"rows — the generated OpenCode plugin does, while its observation rows catch the "+
			"same failure and only log it; "+
			"report this with the cause below, which is: %v",
		coords.Event, coords.Harness, unusableInputSentence(unusable), cause)
}

// unusableInputSentence renders the unusable-input list WITH A VISIBLE END.
//
// The clauses after the list are separated by "; ", so a list joined with "; "
// runs straight into them and the following clause reads as one more item.
// Each item carries its ordinal and the sentence closes with a full stop, so
// the reader can see how many items there are and where the last one ends. The
// lead-in agrees with the count, because "these inputs" in front of a one-item
// list is wrong on the commonest case, which is a single unusable member.
func unusableInputSentence(unusable []string) string {
	if len(unusable) == 0 {
		return "the exit authority refused the fault although it named no unusable input, " +
			"which is a defect in the refusal itself rather than in the fault"
	}

	numbered := make([]string, 0, len(unusable))
	for index, input := range unusable {
		numbered = append(numbered, fmt.Sprintf("(%d) %s", index+1, input))
	}
	if len(numbered) == 1 {
		return "one input of the fault was not usable: " + numbered[0]
	}
	return fmt.Sprintf("%d inputs of the fault were not usable: %s",
		len(numbered), strings.Join(numbered, "; "))
}

// recordedFailureMode renders a failure mode for the durable record. An
// invalid or unset mode has an empty String(), and an empty string in the
// record cannot be told apart from a member the writer forgot. This says which
// of the two happened, so the record stays readable on the one arm that can
// hold an unusable mode.
func recordedFailureMode(mode pastureruntime.FailureMode) string {
	if !mode.IsValid() {
		return "unset-or-unknown"
	}
	return mode.String()
}

// recordedCause renders the fault cause for the durable record.
//
// A NIL CAUSE IS ONE OF THE SIX INPUTS THAT MAKE A FAULT UNMAPPABLE, so it
// reaches this writer on the one arm that exists to report exactly that. Calling
// Error() on it panics the writer BEFORE the composed diagnostic is emitted, and
// the two arms that follow from that panic are DIFFERENT. They were once stated
// as one sentence, which was false of each; they are stated apart here because
// two readers of the same claim reported two different exit codes.
//
// The lifecycle path installs EXACTLY TWO recovers — the deferred one at the
// head of lifecycleOutcome, and one inside the work goroutine — and main()
// installs NONE, so a panic that passes them both is the Go runtime's exit 2.
//
// FIRST ARM, a panic from a NORMAL-PATH call: the deferred recover catches it
// and re-enters lifecycleFault with the panic as the cause. That cause is not
// nil, so the record IS written and the hook DOES fail open — exit 0, the
// host's continue bytes, and one durable line. What the operator loses is the
// ORIGINAL fault: the line and the diagnostic both describe the PANIC, and the
// cause the first call was reporting is never emitted at all. Measured on the
// built binary: exit 0, one line, its cause the panic, its hostExit continue.
//
// SECOND ARM, a nil cause that reaches BOTH calls: the recover's own call
// panics too, inside the deferred function, where nothing is left to catch it.
// The panic escapes to the runtime — exit 2, NO record, and a stack trace on
// standard error that Claude Code reads as a policy refusal. Measured on the
// built binary with the sentinel removed and a nil cause forced on both calls.
//
// THE FIRST RECOVER THEREFORE CATCHES A RECORD-WRITER PANIC ONCE ONLY. The
// sentinel below is LOAD-BEARING and not belt-and-braces: it is what keeps the
// nil cause off both arms. The sentinel cannot be mistaken for the text of a
// real cause, and the unusableFaultInputs member of the same line names the nil
// cause in words.
func recordedCause(cause error) string {
	if cause == nil {
		return "unset-or-missing"
	}
	return cause.Error()
}

// faultRecordLossSuffix is the clause EVERY failing arm of recordLifecycleFault
// ends with. It is one constant and not five literals so that the arms cannot
// drift apart again: three of them once carried it, two returned in silence,
// and each round that fixed one left the others as they were.
//
// IT SAYS "BELOW", AND IT USED TO SAY "ABOVE". recordLifecycleFault runs INSIDE
// lifecycleFault, which RETURNS the outcome; emitLifecycleOutcome writes the
// fault afterwards. Measured on the built binary: the record message is line 1
// of standard error and the fault is line 2. "above" sent the operator to a
// line that had not been written yet, and the previous round added a third
// instance of the wrong word and a test that pinned it.
const faultRecordLossSuffix = "the fault below is reported on this stream only"

// recordLifecycleFault appends one JSON line describing the fault. Every error
// here is swallowed on purpose: the record is evidence for a maintainer, never
// a condition of the host outcome. SWALLOWED IS NOT SILENT: every route that
// loses the record tells the operator on standard error that this fault has no
// durable record.
//
// WHAT HOLDS THAT, AND OVER WHICH POPULATION. Three guards read this function,
// and each names the population it is stated over, because a claim wider than
// the population it is read over is how this writer was twice left an N-1
// sweep:
//
//   - TestEveryFailingArmOfTheFaultWriterTellsTheOperatorOnStandardError reads
//     every GUARDED BRANCH — if arms, else blocks, switch cases, select cases,
//     loop bodies and the bodies of deferred closures — and requires each to
//     report. So a sixth branch cannot be added without a word.
//   - The same test reads every WRITE the function makes, whatever the call
//     shape: fmt.Fprint, Fprintf and Fprintln, and the Write and WriteString
//     methods of any writer expression. It requires the writer of each to be
//     cmd.ErrOrStderr(), and refuses any expression anywhere in this function
//     that names standard output or standard input at all. So no word of this
//     writer can move onto standard output — under a bare method call as much
//     as under fmt, which is the shape that reinstated the founding defect of
//     this command with the whole tree green.
//   - TestTheFaultWriterDiscardsNoResultThatCouldCarryALoss reads DISCARDED
//     RESULTS, which are not branches: a bare `defer file.Close()`, a call
//     statement that is not a report, an assignment that binds the last result
//     to the blank identifier. A loss route need not be a branch, and the
//     unchecked close below was one.
func recordLifecycleFault(
	cmd *cobra.Command,
	coords lifecycleCoordinates,
	failure pastureruntime.LifecycleFailurePolicy,
	policy hostexit.FaultPolicy,
	stage hostexit.FaultStage,
	outcome hostexit.Outcome,
	unusable []string,
	cause error,
) {
	path := lifecycleFaultRecordPath()
	if path == "" {
		// The record has no directory to sit beside. This RETURNED IN SILENCE,
		// while the two sibling failures below — the open and the append — both
		// tell the operator that the fault is on this stream only. The silence
		// made two shipped sentences false: AGENTS.md says the line is appended
		// beside the database, and the record-unknown diagnostic sends the
		// reader to that file.
		//
		// BOTH ROUTES ARE MEASURED, and only one of them was when this arm was
		// written. "--db pasture.db" is driven by the in-process pin, and the
		// documented PASTURE_DB_PATH=pasture.db with no flag at all is driven
		// by TestTheFaultRecordRefusalQuotesThePathTheEnvironmentResolvedTo
		// through the built binary. Both exit 0 with correct host bytes, so
		// nothing else in the run gives the loss away.
		//
		// The quoted path comes from lifecycleStorePath and NEVER from
		// flagDBPath: on the environment route the flag is empty, and quoting
		// it prints `the store path "" names no directory`, which is the exact
		// ambiguity lifecycleStorePath exists to remove.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"pasture hook lifecycle could not place its fault record: the store path %q names no "+
				"directory, so there is no directory to put %s beside; this happened in "+
				"recordLifecycleFault (cmd/pasture/hook_lifecycle.go) while recording this fault; "+
				"%s; give --db a path that names a directory, or set PASTURE_DB_PATH to one, and "+
				"the record returns\n",
			lifecycleStorePath(), lifecycleFaultRecordFile, faultRecordLossSuffix)
		return
	}

	line, err := json.Marshal(map[string]any{
		"recordedAt": time.Now().UTC().Format(time.RFC3339Nano),
		// outcomeClass is always "fault". It is written on every line so the
		// file cannot be mistaken for a record of decisions: emitting the
		// host's continue bytes under fail-open is NOT an evaluated proceed,
		// and this member is what says so where a later reader meets it.
		"outcomeClass": lifecycleFaultOutcomeClass,
		"harness":      string(coords.Harness),
		"event":        coords.Event,
		"hostVersion":  coords.HostVersion,
		// failureMode is the EFFECTIVE mode, the one that decided the host
		// exit. declaredFailureMode is what the host contract DECLARES for the
		// row, before the failure-evidence rule demotes an uncited blocking
		// gate. BOTH are written because the record outlives the process: with
		// the effective mode alone, a demoted gate and a row that was always
		// report-and-continue are byte-identical here, although they need
		// opposite maintainer action — the first becomes able to block once
		// somebody supplies the host citation, the second never blocks.
		"failureMode":         recordedFailureMode(failure.Mode),
		"declaredFailureMode": recordedFailureMode(failure.DeclaredMode),
		// unusableFaultInputs is the EMPTY ARRAY [] on every fault the exit
		// authority could map, and carries the refusal reasons on the one arm
		// it could not. It is never null: UnusableInputs returns a non-nil
		// slice for exactly this member, because null cannot be told apart from
		// a member the writer forgot, which is the ambiguity recordedFailureMode
		// exists to remove one member above. Without this member the record of
		// the refusal arm wrote an empty mode and no reason, so the artefact
		// that outlives the process could not say what stderr had just said.
		"unusableFaultInputs": unusable,
		"failureCitedBy":      failure.Evidence.Source,
		"faultPolicy":         policy.String(),
		"faultStage":          stage.String(),
		"hostExit":            outcome.Exit.String(),
		// hostContinuation is the exact byte body the host was given so that
		// it could carry on. It is recorded because on OpenCode and Codex the
		// proceed is a byte shape, and a reader must be able to see that these
		// bytes were emitted WITHOUT an evaluation.
		"hostContinuation": string(outcome.Stdout),
		"cause":            recordedCause(cause),
	})
	if err != nil {
		// UNREACHABLE BY CONSTRUCTION: every member of the map above is a
		// string, a []string or a value composed of them, and encoding/json
		// cannot refuse those. It reports anyway, because its four siblings
		// report and a silent return here is what made this writer an N-1
		// sweep twice over. No value can drive this arm, so the enumeration
		// test TestEveryFailingArmOfTheFaultWriterTellsTheOperatorOnStandardError
		// reads the function and requires every arm of it to speak, on stderr.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"pasture hook lifecycle could not encode its fault record line: %v; this happened in "+
				"recordLifecycleFault (cmd/pasture/hook_lifecycle.go) before anything was written "+
				"to %s, so the record for this fault is lost; %s; report this, because a line this "+
				"writer composes itself cannot be refused by a correct build\n",
			err, path, faultRecordLossSuffix)
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		// The directory the record would sit in could not be made. This
		// RETURNED IN SILENCE, and the silence falsified the rationale that
		// justifies where this file lives. MEASURED ON THE BUILT BINARY with
		// --db <dir>/afile/sub/pasture.db, where <dir>/afile is a FILE: exit 0,
		// the host's continue bytes on stdout, the fault on standard error, NO
		// record file anywhere, and no word that the record was lost. The
		// placement of the record beside the database is justified by "the
		// commonest fault is that the database could not be opened" — and that
		// measured case IS that fault.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"pasture hook lifecycle could not create the directory for its fault record at %s: %v; "+
				"this happened in recordLifecycleFault (cmd/pasture/hook_lifecycle.go) while "+
				"recording this fault, so the record for this fault is lost; %s; give --db a path "+
				"whose parent directories can all be created, or set PASTURE_DB_PATH to one, and "+
				"the record returns\n",
			filepath.Dir(path), err, faultRecordLossSuffix)
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"pasture hook lifecycle could not open its fault record at %s: %v; "+
				"%s\n", path, err, faultRecordLossSuffix)
		return
	}
	// THE CLOSE IS CHECKED, AND IT WAS A BARE `defer file.Close()`.
	//
	// A close that fails loses the line that was handed to the file. A
	// filesystem that defers the write — a network mount, or delayed
	// allocation — reports the full disk or the I/O error of an earlier write
	// at close(2) and not at write(2), and on that route the append arm below
	// NEVER FIRES. The discarded error made that a record lost with NO WORD ON
	// ANY STREAM, which is the single loss this writer exists to prevent.
	//
	// IT WAS INVISIBLE TO A GUARD THAT READ GUARDED BRANCHES, because a bare
	// defer is not a branch: the arm count stayed at five, every report matched
	// its stream, and the route was outside the population by construction. The
	// guard now reads discarded results as well as branches, so the next one
	// cannot be added in silence either.
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"pasture hook lifecycle could not close its fault record at %s: %v; this happened "+
					"in recordLifecycleFault (cmd/pasture/hook_lifecycle.go) after the line was "+
					"handed to the file, so the line may never have reached the disk and the "+
					"record for this fault is lost; %s; a filesystem that defers the write "+
					"reports a full disk or a device error here rather than at the write, so "+
					"check the free space, the quota and the health of the filesystem holding "+
					"%s, and the record returns\n",
				path, closeErr, faultRecordLossSuffix, filepath.Dir(path))
		}
	}()
	if _, err := file.Write(append(line, '\n')); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"pasture hook lifecycle could not append to its fault record at %s: %v; "+
				"%s\n", path, err, faultRecordLossSuffix)
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
	directory := filepath.Dir(lifecycleStorePath())
	if directory == "" || directory == "." {
		return ""
	}
	return filepath.Join(directory, lifecycleFaultRecordFile)
}

// lifecycleStorePath resolves the store path THIS invocation would use, by the
// same two rules the store itself follows: the --db flag first, then the
// default layout. It is named separately from lifecycleFaultRecordPath because
// the refusal above has to QUOTE it — an operator told only that the record was
// not placed cannot tell which of the two rules produced the path.
func lifecycleStorePath() string {
	if flagDBPath != "" {
		return flagDBPath
	}
	return tasks.DefaultDBPath()
}

// noExitDecisionDiagnostic composes the message for the second arm that leaves
// with exit 1: an outcome that named no exit status at all.
//
// IT IS THE SIBLING OF unclassifiableFaultDiagnostic and it says the same thing
// about the exit. It used to end "the host is not blocked", which is the
// sentence that arm's message was corrected away from: exit 1 is not
// non-blocking on a host that reads any non-zero exit as a broken pasture
// installation. Both exit-1 arms of this command now carry the same, narrowed
// claim, so an operator cannot meet the retired sentence on either one.
//
// The message is a function rather than a literal inside the arm so that a test
// can read it. The arm itself calls os.Exit and is unreachable today, so a
// literal there could hold any sentence at all and nothing would notice. The
// arm's EXIT CODE was left as a literal by that same reasoning and had the same
// gap; it now comes from noExitDecisionExit, below.
func noExitDecisionDiagnostic() string {
	return "pasture hook lifecycle produced no exit decision for this invocation; " +
		"this happened in emitLifecycleOutcome (cmd/pasture/hook_lifecycle.go) after the hook ran; " +
		"pasture did not ask the host to block, and the event may not have been recorded; " +
		"the hook still leaves with exit 1, and a host that reads any non-zero exit as a broken " +
		"pasture installation stops the tool call on its GATE rows — the generated OpenCode " +
		"plugin does, while its observation rows catch the same failure and only log it; " +
		"report this, and retry the hook input"
}

// noExitDecisionExit names the status the no-exit-decision arm leaves with.
//
// THE ARM HELD A BARE LITERAL 1. That literal sat OUTSIDE hostexit, which this
// command made the SOLE exit authority, and nothing pinned it: mutating it to 0
// left the whole cmd/pasture package green, while the comment two lines above
// the arm promised it "must never become a silent exit 0" — the exact defect
// this command exists to remove. The message beside it had already been moved
// into a function so a test could read it; the integer on the next line was
// left behind, where a literal could hold any CODE at all and nothing noticed.
//
// The status is a declared member of the closed set, so Code() always answers
// for it. TestBothExitOneArmsCarryTheSameNarrowedClaim pins both the status and
// that answer.
//
// PINNING THIS FUNCTION IS NOT PINNING THE ARM, and the round that added it
// believed otherwise. Mutating the return here to ExitContinue is red, but
// replacing the ARM's two lines with exitWithCode(0) left the whole cmd/pasture
// package green, so the silent exit 0 was reinstated with nothing noticing.
// TestTheNoExitDecisionArmTakesItsCodeFromTheExitAuthority reads the arm itself
// and refuses any literal there.
func noExitDecisionExit() hostexit.ExitStatus {
	return hostexit.ExitNonBlockingError
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
		// must never become a silent exit 0, so the code comes from the exit
		// authority like every other exit of this command, and never from a
		// literal here. Code() is discarded of its second result on purpose:
		// noExitDecisionExit names a DECLARED status, for which the answer is
		// always known, and the test named above pins that.
		fmt.Fprintln(cmd.ErrOrStderr(), noExitDecisionDiagnostic())
		fallback, _ := noExitDecisionExit().Code()
		exitWithCode(fallback)
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
