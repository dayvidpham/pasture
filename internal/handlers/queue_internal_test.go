package handlers

// White-box coverage for the two decisions the work-queue commands make that
// their black-box tests cannot reach: what a command reports when releasing the
// durable client fails, and that releasing a client which cannot stop really
// does produce that failure.
//
// It lives in package handlers because both subjects are unexported. Nothing is
// stubbed: queueCommandResult and releaseClient are the production functions the
// commands call.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// TestQueueCommandResult covers which of two possible failures a queue command
// reports.
//
// The case that matters is the last one: the command did its work and printed
// the answer, and only then did the client fail to stop. That failure must still
// reach the operator, because it means the runtime closed its database handle
// while part of it was still running.
func TestQueueCommandResult(t *testing.T) {
	t.Parallel()

	opErr := &pasterrors.StructuredError{Category: pasterrors.CategoryStorage, What: "the read failed"}
	releaseErr := &pasterrors.StructuredError{Category: pasterrors.CategoryWorkflow, What: "the client did not stop"}

	tests := []struct {
		name       string
		code       int
		opErr      error
		releaseErr error
		wantCode   int
		wantErr    error
	}{
		{
			name:     "everything worked",
			code:     0,
			wantCode: 0,
		},
		{
			name:     "the operation failed",
			code:     5,
			opErr:    opErr,
			wantCode: 5,
			wantErr:  opErr,
		},
		{
			name:       "the operation failed and the client did not stop either",
			code:       5,
			opErr:      opErr,
			releaseErr: releaseErr,
			wantCode:   5,
			wantErr:    opErr, // the operator asked about the operation
		},
		{
			name:       "the operation worked but the client did not stop",
			code:       0,
			releaseErr: releaseErr,
			wantCode:   3, // the release error's own category decides the exit code
			wantErr:    releaseErr,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, err := queueCommandResult(tc.code, tc.opErr, tc.releaseErr)
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", code, tc.wantCode)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestReleaseClient_ReportsAnIncompleteShutdown proves the other half: a client
// that cannot finish stopping produces the actionable error that
// queueCommandResult then passes on.
//
// The client holds one workflow parked on a channel the test controls, so the
// runtime's wait for in-flight work runs out its budget for certain. There is no
// timing assumption and no sleep.
func TestReleaseClient_ReportsAnIncompleteShutdown(t *testing.T) {
	t.Parallel()

	parked := make(chan struct{})
	entered := make(chan struct{})
	finished := make(chan struct{})
	var enteredOnce, parkedOnce, finishedOnce sync.Once
	releaseParked := func() { parkedOnce.Do(func() { close(parked) }) }

	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	db, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		t.Fatalf("OpenSharedDB: %v", err)
	}
	dbosCtx, err := dbos.NewContext(context.Background(), dbos.Config{
		AppName:            engine.DefaultAppName,
		SQLiteSystemDB:     db,
		ExecutorID:         engine.DefaultExecutorID,
		ApplicationVersion: "queue-release-test-v1",
	})
	if err != nil {
		_ = db.Close()
		t.Fatalf("dbos.NewContext: %v", err)
	}

	// The workflow ignores its context on purpose: cancellation is how a
	// shutdown unwinds in-flight work, so a workflow that honoured it would let
	// the shutdown finish and there would be no failure to observe.
	parkedWorkflow := func(_ dbos.Context, _ string) (string, error) {
		enteredOnce.Do(func() { close(entered) })
		<-parked
		finishedOnce.Do(func() { close(finished) })
		return "released", nil
	}
	dbos.RegisterWorkflow(dbosCtx, parkedWorkflow)

	if err := dbos.Launch(dbosCtx); err != nil {
		_ = db.Close()
		t.Fatalf("dbos.Launch: %v", err)
	}
	t.Cleanup(func() {
		releaseParked()
		<-finished
	})

	if _, err := dbos.RunWorkflow(dbosCtx, parkedWorkflow, "park"); err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
	<-entered

	start := time.Now()
	err = releaseClient(dbosCtx, releaseSiteQueueCommand)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("releaseClient returned nil while a workflow was still running; an incomplete shutdown must be reported")
	}
	if elapsed < controllerShutdownTimeout {
		t.Fatalf("releaseClient returned after %s, before the %s budget expired: the wait is not being honoured",
			elapsed, controllerShutdownTimeout)
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("error is %T, want a structured error", err)
	}
	if got := pasterrors.ExitCode(err); got != 3 {
		t.Errorf("ExitCode = %d, want 3", got)
	}
	// The report must describe what the OPERATOR did, not what some other
	// caller of the same runtime does. A queue command has read its answer back
	// from the database before the stop begins, and its operator has no epoch id
	// to give, so the epoch controller's wording would be wrong twice over.
	for _, want := range []string{"work-queue command", "read back from the database before the stop began"} {
		if !strings.Contains(se.What+" "+se.Impact, want) {
			t.Errorf("the report does not contain %q, so it does not describe what this caller did:\nwhat:   %s\nimpact: %s", want, se.What, se.Impact)
		}
	}
	if !strings.Contains(se.Where, "queue.go") {
		t.Errorf("the location does not name the queue command's own release:\n%s", se.Where)
	}
	if !strings.Contains(se.Fix, "pasture queue concurrency get") {
		t.Errorf("the fix does not point at a queue command:\n%s", se.Fix)
	}
	for _, unwanted := range []string{"epoch-id", "epoch controller", "the controller"} {
		if strings.Contains(se.What+se.Where+se.Impact+se.Fix, unwanted) {
			t.Errorf("the report mentions %q, which this operator never used:\n%s", unwanted, se.Fix)
		}
	}

	// The epoch controller keeps its own wording, which is the point of the
	// distinction.
	controllerErr := incompleteShutdownError(releaseSiteEpochController, errors.New("stop timed out"))
	var controllerSE *pasterrors.StructuredError
	if !errors.As(controllerErr, &controllerSE) {
		t.Fatalf("controller error is %T, want a structured error", controllerErr)
	}
	if !strings.Contains(controllerSE.Fix, "pasture status --epoch-id") {
		t.Errorf("the epoch controller's fix no longer points at the epoch it is about:\n%s", controllerSE.Fix)
	}

	// A nil client is not a failure: there is nothing to release.
	if err := releaseClient(nil, releaseSiteQueueCommand); err != nil {
		t.Errorf("releaseClient(nil) = %v, want nil", err)
	}
}
