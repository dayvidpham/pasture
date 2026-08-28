package handlers

// White-box coverage for the Close contract in controller.go.
//
// It lives in package handlers because the subject — dbosController and the
// incomplete-shutdown contract it reports — is unexported. The controller here
// is the production struct running its production Close; nothing is stubbed and
// no seam exists for the test's benefit. The clean half of the contract is
// covered black-box in controller_test.go.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// TestDbosControllerClose_ReportsIncompleteShutdown pins the failing half of the
// Close contract with a real durable client that cannot finish shutting down.
//
// The client holds one workflow that is parked on a channel the test controls,
// so the runtime's wait for in-flight work runs out its budget for certain —
// there is no timing assumption and no sleep. Close must convert that into an
// actionable error rather than the runtime's bare message, because the caller
// has to know the shutdown cut running work off mid-flight.
func TestDbosControllerClose_ReportsIncompleteShutdown(t *testing.T) {
	t.Parallel()

	// parked is closed by the test to release the workflow; entered tells the
	// test the workflow is really running and counted by the runtime.
	parked := make(chan struct{})
	entered := make(chan struct{})
	finished := make(chan struct{})

	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	db, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		t.Fatalf("OpenSharedDB: %v", err)
	}
	dbosCtx, err := dbos.NewContext(context.Background(), dbos.Config{
		AppName:            engine.DefaultAppName,
		SQLiteSystemDB:     db,
		ExecutorID:         engine.DefaultExecutorID,
		ApplicationVersion: "controller-close-test-v1",
	})
	if err != nil {
		_ = db.Close()
		t.Fatalf("dbos.NewContext: %v", err)
	}

	// The workflow deliberately ignores its context: cancellation is exactly
	// what shutdown uses to unwind in-flight work, so a workflow that honoured
	// it would let the shutdown finish and there would be no timeout to observe.
	parkedWorkflow := func(_ dbos.Context, _ string) (string, error) {
		close(entered)
		<-parked
		close(finished)
		return "released", nil
	}
	dbos.RegisterWorkflow(dbosCtx, parkedWorkflow)

	if err := dbos.Launch(dbosCtx); err != nil {
		_ = db.Close()
		t.Fatalf("dbos.Launch: %v", err)
	}
	// Release the parked workflow whatever happens, and wait for it to leave the
	// runtime, so a failing assertion cannot leave a goroutine behind.
	t.Cleanup(func() {
		select {
		case <-parked:
		default:
			close(parked)
		}
		<-finished
	})

	if _, err := dbos.RunWorkflow(dbosCtx, parkedWorkflow, "park"); err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
	<-entered

	// The production struct, running production Close. trail/trailCloser are nil
	// here: this test is about the client half of the contract, and Close skips
	// a nil closer.
	c := &dbosController{client: dbosCtx, db: db}

	start := time.Now()
	closeErr := c.Close()
	elapsed := time.Since(start)
	if closeErr == nil {
		t.Fatal("Close returned nil while a workflow was still running; an incomplete shutdown must be reported")
	}
	if elapsed < controllerShutdownTimeout {
		t.Fatalf("Close returned after %s, before the %s shutdown budget expired: the wait is not being honoured",
			elapsed, controllerShutdownTimeout)
	}

	var structured *pasterrors.StructuredError
	if !errors.As(closeErr, &structured) {
		t.Fatalf("Close error = %v (%T), want a structured, actionable error", closeErr, closeErr)
	}
	if structured.Category != pasterrors.CategoryWorkflow {
		t.Errorf("Category = %v, want %v (exit code 3)", structured.Category, pasterrors.CategoryWorkflow)
	}
	if structured.Cause == nil {
		t.Error("Cause is nil: the runtime's own shutdown error must stay reachable for diagnosis")
	}
	for name, field := range map[string]string{
		"What":   structured.What,
		"Why":    structured.Why,
		"Where":  structured.Where,
		"Impact": structured.Impact,
		"Fix":    structured.Fix,
	} {
		if field == "" {
			t.Errorf("%s is empty: an actionable error must say what, why, where, what it means and how to fix it", name)
		}
	}
}
