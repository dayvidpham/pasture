package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// These probes are local to one test invocation, never package globals. They
// acknowledge the actual completion, join and held-outcome boundaries. A manual
// failure channel exercises timeout cleanup without waiting for a clock to win.
type lifecycleTestHooks struct {
	finished func()
	joining  func()
	observed func(hostexit.Outcome)
	timeout  func() <-chan time.Time
}

func (h lifecycleTestHooks) failureCeiling() <-chan time.Time {
	if h.timeout != nil {
		return h.timeout()
	}
	return time.After(preCommitStallCeiling)
}

type lifecycleTestInvocation struct {
	t              *testing.T
	outcomes       chan hostexit.Outcome
	foregroundDone chan struct{}
	backgroundDone chan struct{}
	cancel         context.CancelFunc
	release        func()
	hooks          lifecycleTestHooks
	joinOnce       sync.Once
}

func startLifecycleTestInvocation(t *testing.T, cmd *cobra.Command, args []string,
	barrier handlers.CommitBarrier, budget timeouts.Profile, deadline lifecycleDeadline,
	release func(), hooks lifecycleTestHooks,
) *lifecycleTestInvocation {
	t.Helper()
	cancelSignal, cancel := context.WithCancel(context.Background())
	invocation := &lifecycleTestInvocation{
		t:              t,
		outcomes:       make(chan hostexit.Outcome, 1),
		foregroundDone: make(chan struct{}),
		backgroundDone: make(chan struct{}),
		cancel:         cancel,
		release:        release,
		hooks:          hooks,
	}
	// Registration precedes launch. Join is also deferred by each caller so
	// even its post-invocation assertions cannot race unfinished store work.
	t.Cleanup(invocation.Join)
	derive := func(parent context.Context, tier time.Duration) (context.Context, context.CancelFunc) {
		ctx, stopDeadline := deadline(parent, tier)
		stopCancellation := context.AfterFunc(cancelSignal, stopDeadline)
		return ctx, func() {
			stopCancellation()
			stopDeadline()
		}
	}
	go func() {
		defer close(invocation.foregroundDone)
		invocation.outcomes <- lifecycleOutcomeWithCompletion(cmd, args, barrier, budget, derive, func() {
			if hooks.finished != nil {
				hooks.finished()
			}
			close(invocation.backgroundDone)
		})
	}()
	return invocation
}

func (i *lifecycleTestInvocation) Join() {
	i.t.Helper()
	i.joinOnce.Do(func() {
		i.cancel()
		if i.release != nil {
			i.release()
		}
		if i.hooks.joining != nil {
			i.hooks.joining()
		}
		select {
		case <-i.backgroundDone:
		case <-time.After(preCommitStallCeiling):
			i.t.Errorf("lifecycle test worker did not finish after release/cancel within %s; still joining before resource cleanup", preCommitStallCeiling)
			<-i.backgroundDone
		}
		<-i.foregroundDone
	})
}

func lifecycleTestOutcome(t *testing.T, cmd *cobra.Command, args []string,
	barrier handlers.CommitBarrier, budget timeouts.Profile, deadline lifecycleDeadline,
) hostexit.Outcome {
	t.Helper()
	i := startLifecycleTestInvocation(t, cmd, args, barrier, budget, deadline, nil, lifecycleTestHooks{})
	defer i.Join()
	select {
	case outcome := <-i.outcomes:
		return outcome
	case <-time.After(preCommitStallCeiling):
		t.Fatal("lifecycle test foreground did not return; canceling and joining its worker before cleanup")
		return hostexit.Outcome{}
	}
}

func waitLifecycleHold(reached <-chan struct{}, i *lifecycleTestInvocation, hooks lifecycleTestHooks, label string) error {
	select {
	case <-reached:
		return nil
	case outcome := <-i.outcomes:
		return fmt.Errorf("invocation ended before %s hold: %+v", label, outcome)
	case <-hooks.failureCeiling():
		return fmt.Errorf("invocation did not reach %s hold within its test failure ceiling", label)
	}
}

func TestLifecycleTestInvocationNotifiesWithoutLaunchingWorker(t *testing.T) {
	// Serial: this uses the production command and its DB flag.
	database := filepath.Join(t.TempDir(), "pasture.db")
	cmd := lifecycleTestCommand(t, "claude-code", "SessionStart", "2.1.261", database)
	notifications := 0
	outcome := lifecycleOutcomeWithCompletion(cmd, []string{"unexpected-argument"},
		handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile(), context.WithTimeout,
		func() { notifications++ })
	require.Equal(t, hostexit.ExitContinue, outcome.Exit)
	require.Contains(t, outcome.Stderr, "unexpected positional arguments")
	require.Equal(t, 1, notifications, "an in-process owner must not wait forever for a worker that was never launched")
}

func TestLifecycleTestInvocationJoinIsIdempotent(t *testing.T) {
	cmd := lifecycleTestCommand(t, "claude-code", "SessionStart", "2.1.261", filepath.Join(t.TempDir(), "pasture.db"))
	var releases, joins, finishes atomic.Int32
	i := startLifecycleTestInvocation(t, cmd, []string{"unexpected-argument"},
		handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile(), context.WithTimeout,
		func() { releases.Add(1) }, lifecycleTestHooks{
			joining:  func() { joins.Add(1) },
			finished: func() { finishes.Add(1) },
		})
	select {
	case <-i.outcomes:
	case <-time.After(preCommitStallCeiling):
		t.Fatal("early-refusal invocation did not finish")
	}
	i.Join()
	i.Join()
	require.EqualValues(t, 1, releases.Load())
	require.EqualValues(t, 1, joins.Load())
	require.EqualValues(t, 1, finishes.Load())
}

// TestLifecycleTestInvocationJoinsHeldWorkerBeforeHelperReturn observes the
// actual hold helpers, not a substitute waiter.
// WHAT IT VISITS: pre-fence input and post-commit barrier holds with a real
// native handler and store.
// WHAT IT DOES NOT READ: which file caused any historical TempDir cleanup failure.
func TestLifecycleTestInvocationJoinsHeldWorkerBeforeHelperReturn(t *testing.T) {
	for _, postCommit := range []bool{false, true} {
		name := "pre-fence"
		if postCommit {
			name = "post-commit"
		}
		t.Run(name, func(t *testing.T) {
			database := filepath.Join(t.TempDir(), "pasture.db")
			initializeLifecycleTestDatabase(t, database)
			raw := claudeFixture(t, "pre_tool_use_2_1_261.json")
			// The post-commit cell needs a COMMITTED refusal to survive the
			// expiry, and the gate reads that refusal from the store: a bound
			// session whose registered actor owns no assignment.
			seedBoundActorWithoutAssignment(t, database, ir.HarnessClaudeCode, raw)
			cmd := lifecycleTestCommand(t, "claude-code", "PreToolUse", "2.1.261", database)
			cmd.SetIn(bytes.NewReader(raw))

			var observed atomic.Bool
			tailReached := make(chan bool, 1)
			releaseTail := make(chan struct{})
			joinEntered := make(chan struct{})
			helperReturned := make(chan struct{})
			controller := make(chan error, 1)
			hooks := lifecycleTestHooks{
				observed: func(hostexit.Outcome) { observed.Store(true) },
				finished: func() {
					tailReached <- observed.Load()
					<-releaseTail
				},
				joining: func() { close(joinEntered) },
			}
			// This controller always releases its hold, independently of the
			// helper stack and its FailNow/deferred cleanup. No timer can PASS
			// the proof: only entering the real synchronous Join can do that.
			go func() {
				defer close(releaseTail)
				select {
				case hadOutcome := <-tailReached:
					if !hadOutcome {
						controller <- errors.New("worker completion ran before the held host Outcome was observed")
						return
					}
				case <-helperReturned:
					controller <- errors.New("helper returned before joining its worker tail")
					return
				case <-time.After(preCommitStallCeiling):
					controller <- errors.New("worker did not reach its completion tail; releasing controller hold for cleanup")
					return
				}
				select {
				case <-joinEntered:
					controller <- nil
				case <-helperReturned:
					controller <- errors.New("helper released input but returned without entering Join")
				case <-time.After(preCommitStallCeiling):
					controller <- errors.New("helper never entered Join; releasing controller hold for cleanup")
				}
			}()

			var outcome hostexit.Outcome
			var invocationErr error
			func() {
				defer close(helperReturned)
				if postCommit {
					outcome, invocationErr = abandonAfterTheCommitWithHooks(t, cmd, hooks)
				} else {
					outcome, invocationErr = expireBeforeCommitWithHooks(t, cmd, raw, hooks)
				}
			}()
			require.NoError(t, <-controller)
			require.NoError(t, invocationErr)
			if postCommit {
				require.Equal(t, hostexit.ExitBlock, outcome.Exit)
				require.Equal(t, backend.ReasonNoActiveAssignment.Message(), outcome.Stderr)
			} else {
				require.Equal(t, hostexit.ExitContinue, outcome.Exit)
				require.Contains(t, outcome.Stderr, "hook-invocation deadline")
			}
			require.Empty(t, outcome.Stdout)
		})
	}
}

func TestLifecycleTestInvocationJoinsDuringPanicAndGoexit(t *testing.T) {
	for _, goexit := range []bool{false, true} {
		name := "panic"
		if goexit {
			name = "Goexit"
		}
		t.Run(name, func(t *testing.T) {
			database := filepath.Join(t.TempDir(), "pasture.db")
			initializeLifecycleTestDatabase(t, database)
			cmd := lifecycleTestCommand(t, "claude-code", "PreToolUse", "2.1.261", database)
			raw := claudeFixture(t, "pre_tool_use_2_1_261.json")
			joined := make(chan struct{})
			finished := make(chan struct{})
			returned := make(chan struct{})
			caught := make(chan any, 1)
			var normalReturn atomic.Bool
			hooks := lifecycleTestHooks{
				finished: func() { close(finished) },
				joining:  func() { close(joined) },
				observed: func(hostexit.Outcome) {
					if goexit {
						// FailNow uses Goexit to unwind the calling goroutine. This
						// exercises those defers without marking the parent test failed.
						runtime.Goexit()
					}
					panic("controlled assertion unwind")
				},
			}
			go func() {
				defer close(returned)
				defer func() { caught <- recover() }()
				_, _ = expireBeforeCommitWithHooks(t, cmd, raw, hooks)
				normalReturn.Store(true)
			}()
			select {
			case <-returned:
			case <-time.After(preCommitStallCeiling):
				t.Fatal("controlled unwind did not drain its invocation")
			}
			require.False(t, normalReturn.Load())
			if goexit {
				require.Nil(t, <-caught)
			} else {
				require.Equal(t, "controlled assertion unwind", <-caught)
			}
			select {
			case <-joined:
			default:
				t.Fatal("unwind returned without entering actual helper Join")
			}
			select {
			case <-finished:
			default:
				t.Fatal("unwind returned before the actual worker finished")
			}
		})
	}
}

func TestLifecycleTestInvocationJoinsEarlyHoldFailure(t *testing.T) {
	database := filepath.Join(t.TempDir(), "pasture.db")
	// WorktreeCreate remains deferred as a provider hook. Its activation
	// refusal precedes the input read, so these bytes are never a fixture.
	cmd := lifecycleTestCommand(t, "claude-code", "WorktreeCreate", "2.1.261", database)
	var joins, finishes atomic.Int32
	_, err := expireBeforeCommitWithHooks(t, cmd, []byte(`{}`), lifecycleTestHooks{
		joining:  func() { joins.Add(1) },
		finished: func() { finishes.Add(1) },
	})
	require.ErrorContains(t, err, "ended before pre-commit input hold")
	require.ErrorContains(t, err, `event "WorktreeCreate" is withheld`, "the actual activation refusal must cause the early return")
	require.EqualValues(t, 1, joins.Load(), "failure must drain before the wrapper can call require.NoError")
	require.EqualValues(t, 1, finishes.Load())
	_, statErr := os.Stat(database)
	require.ErrorIs(t, statErr, os.ErrNotExist, "early activation refusal must not open the store")
}

func TestLifecycleTestInvocationManualPhaseTimeoutDrainsWorker(t *testing.T) {
	database := filepath.Join(t.TempDir(), "pasture.db")
	initializeLifecycleTestDatabase(t, database)
	cmd := lifecycleTestCommand(t, "claude-code", "SessionStart", "2.1.261", database)
	input := &preCommitReader{
		Reader:  bytes.NewReader(claudeFixture(t, "session_start_2_1_261.json")),
		reached: make(chan struct{}), release: make(chan struct{}),
	}
	cmd.SetIn(input)
	deadline := newTrippedDeadline(t)
	i := startLifecycleTestInvocation(t, cmd, nil, handlers.PassThroughCommitBarrier{},
		timeouts.ProductionProfile(), deadline.derive, func() { close(input.release) }, lifecycleTestHooks{})
	defer i.Join()
	select {
	case <-input.reached:
	case <-time.After(preCommitStallCeiling):
		t.Fatal("worker did not reach the controlled input hold")
	}
	manualFailure := make(chan time.Time)
	close(manualFailure)
	// The shared phase waiter is given a deliberately unacknowledged phase.
	// Its timeout branch is selected by state, not elapsed time. Both real hold
	// helpers defer Join before calling this same waiter (pinned below).
	err := func() error {
		defer i.Join()
		return waitLifecycleHold(make(chan struct{}), i, lifecycleTestHooks{
			timeout: func() <-chan time.Time { return manualFailure },
		}, "unacknowledged input")
	}()
	require.ErrorContains(t, err, "test failure ceiling")
	select {
	case <-i.backgroundDone:
	default:
		t.Fatal("phase failure returned without joining worker completion")
	}
}

func TestLifecycleTestInvocationNotifiesAfterEarlyPanicRecovery(t *testing.T) {
	dir := t.TempDir()
	cmd := lifecycleTestCommand(t, "claude-code", "SessionStart", "2.1.261", filepath.Join(dir, "pasture.db"))
	cmd.SetContext(nil) // The actual production recovery owns this invalid input.
	notifications := 0
	var observedFault []byte
	var readErr error
	outcome := lifecycleOutcomeWithCompletion(cmd, nil, handlers.PassThroughCommitBarrier{},
		timeouts.ProductionProfile(), context.WithTimeout, func() {
			notifications++
			observedFault, readErr = os.ReadFile(filepath.Join(dir, lifecycleFaultRecordFile))
		})
	require.Equal(t, hostexit.ExitContinue, outcome.Exit)
	require.Contains(t, outcome.Stderr, "panicked")
	require.Equal(t, 1, notifications)
	require.NoError(t, readErr, "zero-worker completion must run after recovery writes its fault record")
	require.Contains(t, string(observedFault), "panicked")
}

func TestLifecycleTestInvocationWorkerPanicNotifiesAfterRecovery(t *testing.T) {
	cmd := lifecycleTestCommand(t, "claude-code", "SessionStart", "2.1.261", filepath.Join(t.TempDir(), "pasture.db"))
	cmd.SetIn(panickingReader{message: "controlled worker panic"})
	finished := make(chan struct{})
	var notifications atomic.Int32
	outcome := lifecycleOutcomeWithCompletion(cmd, nil, handlers.PassThroughCommitBarrier{},
		timeouts.ProductionProfile(), context.WithTimeout, func() {
			notifications.Add(1)
			close(finished)
		})
	require.Contains(t, outcome.Stderr, "controlled worker panic")
	select {
	case <-finished:
	case <-time.After(preCommitStallCeiling):
		t.Fatal("worker panic returned its result without a completion notification")
	}
	require.EqualValues(t, 1, notifications.Load())
}

func TestLifecycleCompletionFollowsRecoveryAndWorkerCleanup(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "hook_lifecycle.go", nil, 0)
	require.NoError(t, err)
	var core *ast.FuncDecl
	for _, node := range file.Decls {
		if fn, ok := node.(*ast.FuncDecl); ok && fn.Name.Name == "lifecycleOutcomeWithCompletion" {
			core = fn
		}
	}
	require.NotNil(t, core)
	var worker *ast.FuncLit
	var recovery *ast.FuncLit
	starts := 0
	for _, statement := range core.Body.List {
		if deferred, ok := statement.(*ast.DeferStmt); ok && recovery == nil {
			recovery, _ = deferred.Call.Fun.(*ast.FuncLit)
		}
		if launched, ok := statement.(*ast.GoStmt); ok {
			starts++
			worker, _ = launched.Call.Fun.(*ast.FuncLit)
		}
	}
	require.Equal(t, 1, starts, "one invocation launches at most one owned worker")
	require.NotNil(t, recovery)
	require.NotNil(t, worker)
	require.Len(t, recovery.Body.List, 2)
	zeroWorker, ok := recovery.Body.List[0].(*ast.DeferStmt)
	require.True(t, ok, "zero-worker notification must be deferred until outer recovery finishes")
	require.Contains(t, lifecycleTestNodeSource(t, zeroWorker), "!workerStarted && finished != nil")
	require.Contains(t, lifecycleTestNodeSource(t, recovery.Body.List[1]), "recover()")
	require.GreaterOrEqual(t, len(worker.Body.List), 4)
	completion, ok := worker.Body.List[0].(*ast.DeferStmt)
	require.True(t, ok, "worker completion must be registered first so it runs last")
	require.Contains(t, lifecycleTestNodeSource(t, completion), "finished()")
	workerRecovery, ok := worker.Body.List[1].(*ast.DeferStmt)
	require.True(t, ok)
	require.Contains(t, lifecycleTestNodeSource(t, workerRecovery), "recover()")
	require.Contains(t, lifecycleTestNodeSource(t, workerRecovery), "completed <-")
	publication, ok := worker.Body.List[len(worker.Body.List)-1].(*ast.SendStmt)
	require.True(t, ok)
	require.Equal(t, "completed", sourceOf(publication.Chan))
	require.Contains(t, lifecycleTestNodeSource(t, worker.Body.List[len(worker.Body.List)-2]), "handlers.HookLifecycleNative")
	observerCalls := 0
	ast.Inspect(core, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && sourceOf(call.Fun) == "finished" {
			observerCalls++
		}
		return true
	})
	require.Equal(t, 2, observerCalls, "only the mutually exclusive zero-worker and final worker defers notify")
}

func TestLifecycleHoldHelpersInstallJoinBeforeWaiting(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "hook_lifecycle_failuremode_test.go", nil, 0)
	require.NoError(t, err)
	found := 0
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || (fn.Name.Name != "expireBeforeCommitWithHooks" && fn.Name.Name != "abandonAfterTheCommitWithHooks") {
			continue
		}
		found++
		startAt, joinAt, waitAt := -1, -1, -1
		for index, statement := range fn.Body.List {
			text := lifecycleTestNodeSource(t, statement)
			if strings.Contains(text, "startLifecycleTestInvocation(") {
				startAt = index
			}
			if text == "defer i.Join()" {
				joinAt = index
			}
			if strings.Contains(text, "waitLifecycleHold(") {
				waitAt = index
			}
		}
		require.NotEqual(t, -1, startAt)
		require.Equal(t, startAt+1, joinAt, "all returns and FailNow/Goexit paths must unwind through Join")
		require.Greater(t, waitAt, joinAt)
	}
	require.Equal(t, 2, found, "both actual hold helpers must be inspected")

	file, err = parser.ParseFile(token.NewFileSet(), "hook_lifecycle_worker_lifetime_test.go", nil, 0)
	require.NoError(t, err)
	var launch *ast.FuncDecl
	for _, declaration := range file.Decls {
		if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name.Name == "startLifecycleTestInvocation" {
			launch = fn
		}
	}
	require.NotNil(t, launch)
	cleanupAt, goAt := -1, -1
	for index, statement := range launch.Body.List {
		if lifecycleTestNodeSource(t, statement) == "t.Cleanup(invocation.Join)" {
			cleanupAt = index
		}
		if _, ok := statement.(*ast.GoStmt); ok {
			require.Equal(t, -1, goAt, "the test owner launches exactly one foreground runner")
			goAt = index
		}
	}
	require.NotEqual(t, -1, cleanupAt, "fallback cleanup must be registered before any launch")
	require.Greater(t, goAt, cleanupAt)
}

func lifecycleTestNodeSource(t *testing.T, node ast.Node) string {
	t.Helper()
	var text bytes.Buffer
	require.NoError(t, printer.Fprint(&text, token.NewFileSet(), node))
	return text.String()
}
