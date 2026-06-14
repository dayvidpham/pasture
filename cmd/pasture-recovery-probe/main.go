//go:build recovery

// Command pasture-recovery-probe is a build-tagged helper for the permanent
// kill-9 recovery test (internal/engine/recovery_test.go). It is NOT part of
// any production build: it compiles only under `-tags recovery`.
//
// It drives a short epoch through the durable engine. With PROBE_STALL > 0 it
// is the "victim": it writes the forensic row for a mid-epoch transition,
// signals readiness to its parent process with SIGUSR1, then sleeps inside the
// durable step so the test can SIGKILL it after the side-effect write but before
// the step returns.
// With PROBE_STALL == 0 it is the "resumer": Launch runs the DBOS recovery sweep,
// which resumes the victim's in-flight workflow; RunWorkflow with the same id
// returns a handle to it and GetResult waits for completion.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// buildStamp is injected via -ldflags so a rebuild changes the binary hash
// (exercising the recompiled-binary recovery tier) while ApplicationVersion
// below stays pinned.
var buildStamp string

// pinnedAppVersion is the DBOS application version. It is pinned identically
// across every process and every rebuild so recovery is never filtered out by a
// changed binary hash.
const pinnedAppVersion = "recovery-probe-v1"

func main() {
	_ = buildStamp // referenced so the linker keeps the -X target

	dbPath := os.Getenv("PROBE_DB")
	wfID := os.Getenv("PROBE_WFID")
	stall, _ := strconv.Atoi(os.Getenv("PROBE_STALL"))
	if dbPath == "" || wfID == "" {
		fmt.Fprintln(os.Stderr, "PROBE_DB and PROBE_WFID are required")
		os.Exit(2)
	}
	if os.Getenv("PROBE_MODE") == "queue" {
		if err := runQueueProbe(dbPath, wfID, stall); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	// The stall phase is the mid-epoch transition the test crashes in.
	const stallPhase = protocol.PhasePropose

	// Open the unified tracker so the engine records BOTH forensic tiers — the
	// audit_events row (via the audit methods) and the PROV-O activity (via the
	// provenance methods). The crash window (the stall below) lands after BOTH
	// writes, so resume exercises exactly-once on the activities tier too, not
	// just audit_events.
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "OpenTaskTracker:", err)
		os.Exit(1)
	}
	defer tracker.Close()

	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: pinnedAppVersion,
		Trail:              tracker,
		Tracker:            tracker,
		OnTransition: func(_ context.Context, _ string, rec *protocol.TransitionRecord, _ string) error {
			// Fires AFTER the forensic row is written, BEFORE the step returns.
			// The stall lives here (process-local), NOT in the persisted workflow
			// input, so a recovering process with PROBE_STALL=0 re-runs this step
			// without stalling and completes the epoch.
			if rec.ToPhase == stallPhase {
				if stall > 0 {
					if err := signalReady(); err != nil {
						return err
					}
					time.Sleep(time.Duration(stall) * time.Second)
				}
			}
			return nil
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "engine.New:", err)
		os.Exit(1)
	}
	defer e.Shutdown(5 * time.Second)

	if err := e.Launch(); err != nil {
		fmt.Fprintln(os.Stderr, "engine.Launch:", err)
		os.Exit(1)
	}

	plan := []engine.AdvanceStep{
		{ToPhase: protocol.PhaseElicit, TriggeredBy: "epoch", ConditionMet: "classified"},
		{ToPhase: stallPhase, TriggeredBy: "architect", ConditionMet: "elicited"},
	}

	h, err := dbos.RunWorkflow(e.DBOS(), e.EpochWorkflow,
		engine.EpochInput{EpochId: wfID, Advances: plan},
		dbos.WithWorkflowID(wfID))
	if err != nil {
		fmt.Fprintln(os.Stderr, "RunWorkflow:", err)
		os.Exit(1)
	}

	final, err := h.GetResult(dbos.WithHandleTimeout(120 * time.Second))
	if err != nil {
		fmt.Fprintln(os.Stderr, "GetResult:", err)
		os.Exit(1)
	}

	// Reached only by the resumer (the victim is killed during the stall).
	fmt.Printf("COMPLETE %s\n", final.CurrentPhase)
}

type queueStallHook struct {
	targetSliceID string
	started       chan<- struct{}
	stall         time.Duration
}

func (h queueStallHook) Events() []hooks.HookEvent {
	return []hooks.HookEvent{hooks.HookSliceStarted}
}

func (h queueStallHook) Handle(_ context.Context, p hooks.HookPayload) (hooks.HandleOutcome, error) {
	if p.Event != hooks.HookSliceStarted {
		return hooks.HandleOutcome{}, nil
	}
	if got, _ := p.Data["sliceId"].(string); got != h.targetSliceID {
		return hooks.HandleOutcome{}, nil
	}
	if h.started != nil {
		select {
		case h.started <- struct{}{}:
		default:
		}
	}
	if h.stall > 0 {
		time.Sleep(h.stall)
	}
	return hooks.HandleOutcome{}, nil
}

func signalReady() error {
	parent := os.Getppid()
	if parent <= 1 {
		return fmt.Errorf("cannot signal readiness: invalid parent pid %d", parent)
	}
	if err := syscall.Kill(parent, syscall.SIGUSR1); err != nil {
		return fmt.Errorf("signal readiness to parent pid %d: %w", parent, err)
	}
	return nil
}

func runQueueProbe(dbPath, wfID string, stall int) error {
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return fmt.Errorf("OpenTaskTracker: %w", err)
	}
	defer tracker.Close()

	firstSliceID := wfID + "--queued-first"
	secondSliceID := wfID + "--queued-second"
	started := make(chan struct{}, 1)

	mgr := hooks.NewManager()
	mgr.Register(queueStallHook{
		targetSliceID: firstSliceID,
		started:       started,
		stall:         time.Duration(stall) * time.Second,
	})

	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: pinnedAppVersion,
		Trail:              tracker,
		Tracker:            tracker,
		SliceConcurrency:   1,
		HooksMgr:           mgr,
	})
	if err != nil {
		return fmt.Errorf("engine.New: %w", err)
	}
	defer e.Shutdown(5 * time.Second)

	if err := e.Launch(); err != nil {
		return fmt.Errorf("engine.Launch: %w", err)
	}

	if stall > 0 {
		plan := []engine.AdvanceStep{
			{ToPhase: protocol.PhaseElicit, TriggeredBy: "epoch", ConditionMet: "classified"},
			{ToPhase: protocol.PhasePropose, TriggeredBy: "architect", ConditionMet: "elicited"},
		}
		epoch, err := dbos.RunWorkflow(e.DBOS(), e.EpochWorkflow,
			engine.EpochInput{EpochId: wfID, Advances: plan},
			dbos.WithWorkflowID(wfID))
		if err != nil {
			return fmt.Errorf("RunWorkflow(epoch): %w", err)
		}
		if _, err := epoch.GetResult(dbos.WithHandleTimeout(30 * time.Second)); err != nil {
			return fmt.Errorf("epoch did not reach propose before queue probe: %w", err)
		}

		first, err := e.EnqueueSlice(engine.SliceInput{
			EpochId: wfID,
			SliceId: firstSliceID,
		})
		if err != nil {
			return fmt.Errorf("EnqueueSlice(first): %w", err)
		}
		if _, err := e.EnqueueSlice(engine.SliceInput{
			EpochId: wfID,
			SliceId: secondSliceID,
		}); err != nil {
			return fmt.Errorf("EnqueueSlice(second): %w", err)
		}
		if err := sendMockStartSignal(e, firstSliceID, 30*time.Second); err != nil {
			return err
		}
		if err := sendMockStartSignal(e, secondSliceID, 30*time.Second); err != nil {
			return err
		}
		select {
		case <-started:
		case <-time.After(30 * time.Second):
			return fmt.Errorf("first slice did not reach HookSliceStarted within 30s")
		}
		if err := signalReady(); err != nil {
			return err
		}
		_, err = first.GetResult(dbos.WithHandleTimeout(time.Duration(stall+30) * time.Second))
		if err != nil {
			return fmt.Errorf("victim first slice returned before kill: %w", err)
		}
		return fmt.Errorf("victim first slice completed before kill")
	}

	first, err := dbos.RetrieveWorkflow[engine.SliceResult](e.DBOS(), firstSliceID)
	if err != nil {
		return fmt.Errorf("RetrieveWorkflow(first): %w", err)
	}
	second, err := dbos.RetrieveWorkflow[engine.SliceResult](e.DBOS(), secondSliceID)
	if err != nil {
		return fmt.Errorf("RetrieveWorkflow(second): %w", err)
	}

	firstResult, err := first.GetResult(dbos.WithHandleTimeout(120 * time.Second))
	if err != nil {
		return fmt.Errorf("first slice did not recover: %w", err)
	}
	secondResult, err := second.GetResult(dbos.WithHandleTimeout(120 * time.Second))
	if err != nil {
		return fmt.Errorf("queued second slice did not recover: %w", err)
	}
	if !firstResult.Success || !secondResult.Success {
		return fmt.Errorf("slice recovery returned failures: first=%+v second=%+v", firstResult, secondResult)
	}

	fmt.Printf("QUEUE COMPLETE %s %s\n", firstResult.SliceId, secondResult.SliceId)
	return nil
}

func sendMockStartSignal(e *engine.Engine, sliceID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	sig := protocol.SliceStartSignal{Mode: protocol.SliceMock}
	for time.Now().Before(deadline) {
		if err := dbos.Send(e.DBOS(), sliceID, sig, protocol.SignalStartSlice.String()); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("start_slice(mock) signal for %q was not delivered within %s", sliceID, timeout)
}
