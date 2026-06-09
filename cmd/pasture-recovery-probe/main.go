//go:build recovery

// Command pasture-recovery-probe is a build-tagged helper for the permanent
// kill-9 recovery test (internal/engine/recovery_test.go). It is NOT part of
// any production build: it compiles only under `-tags recovery`.
//
// It drives a short epoch through the durable engine. With PROBE_STALL > 0 it
// is the "victim": it writes the forensic row for a mid-epoch transition, signals
// readiness by creating PROBE_READY, then sleeps inside the durable step so the
// test can SIGKILL it after the side-effect write but before the step returns.
// With PROBE_STALL == 0 it is the "resumer": Launch runs the DBOS recovery sweep,
// which resumes the victim's in-flight workflow; RunWorkflow with the same id
// returns a handle to it and GetResult waits for completion.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
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
	readyFile := os.Getenv("PROBE_READY")
	stall, _ := strconv.Atoi(os.Getenv("PROBE_STALL"))
	if dbPath == "" || wfID == "" {
		fmt.Fprintln(os.Stderr, "PROBE_DB and PROBE_WFID are required")
		os.Exit(2)
	}

	// The stall phase is the mid-epoch transition the test crashes in.
	const stallPhase = protocol.PhasePropose

	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: pinnedAppVersion,
		OnTransition: func(_ context.Context, _ string, rec *protocol.TransitionRecord) error {
			// Fires AFTER the forensic row is written, BEFORE the step returns.
			// The stall lives here (process-local), NOT in the persisted workflow
			// input, so a recovering process with PROBE_STALL=0 re-runs this step
			// without stalling and completes the epoch.
			if rec.ToPhase == stallPhase {
				if readyFile != "" {
					_ = os.WriteFile(readyFile, []byte("ready"), 0o644)
				}
				if stall > 0 {
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
