package handlers_test

// queue_test.go covers the operator surface for work-queue settings: which
// queue names are accepted, what is refused, and that a change is written to
// the shared database and read back from it.
//
// Rendering is covered by internal/formatters/queue_test.go, and the whole
// command path (arguments, output, exit codes) by cmd/pasture/queue_test.go.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

// Exit codes an operator sees, from the mapping in internal/errors/errors.go
// (errors.ExitCode).
const (
	exitValidation = 1
	exitStorage    = 5
)

// queueFixture builds ONE database with the queues registered — the state a
// single daemon start leaves behind — and every test here takes a private copy
// of it.
//
// It is built once per test binary on purpose. Registering the queues means
// building and stopping a whole durable engine, which is by far the most
// expensive thing these tests do, and doing it in each of several parallel
// tests loaded the machine enough to starve a slice test elsewhere in the tree
// that waits a fixed two seconds for a signal. One build, then a file copy per
// test, removes that load and still gives each test a database of its own to
// write to.
var queueFixture = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "pasture-queue-fixture-*")
	if err != nil {
		return "", fmt.Errorf("create the queue fixture directory: %w", err)
	}
	queueFixtureDir = dir
	dbPath := filepath.Join(dir, "pasture.db")
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: "test-queue-handler",
		ExecutorID:         "test-queue-handler",
	})
	if err != nil {
		return "", fmt.Errorf("build the engine that registers the queues: %w", err)
	}
	// Shutdown closes the handle, so the file on disk is complete and can be
	// copied.
	e.Shutdown(10 * time.Second)
	return dbPath, nil
})

// queueFixtureDir is the directory queueFixture built, kept so the tests can
// delete it when they are done. It is written inside queueFixture and read only
// after every test has finished.
var queueFixtureDir string

// removeQueueFixture deletes the shared fixture. TestMain calls it after the
// tests finish; a t.Cleanup could not, because the fixture outlives the test
// that happened to build it.
func removeQueueFixture() {
	if queueFixtureDir != "" {
		_ = os.RemoveAll(queueFixtureDir)
	}
}

// restartDaemonAt does what a daemon start does to the queue rows: it builds an
// engine on this database, which registers the queues with the engine's own
// configured limits, and stops it again. Only the test that is ABOUT that
// behaviour calls it; the others copy the fixture instead.
func restartDaemonAt(t *testing.T, dbPath string) {
	t.Helper()
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: "test-queue-handler",
		ExecutorID:         "test-queue-handler-restart",
	})
	if err != nil {
		t.Fatalf("engine.New (restart): %v", err)
	}
	e.Shutdown(10 * time.Second)
}

// registeredQueueDB returns a private copy of the fixture: a database with the
// queues already registered, which this test alone may change.
func registeredQueueDB(t *testing.T) string {
	t.Helper()
	src, err := queueFixture()
	if err != nil {
		t.Fatalf("queue fixture: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "pasture.db")
	copyQueueFixture(t, dst, src)
	return dst
}

// copyQueueFixture copies the fixture database, and any write-ahead files beside
// it, so the copy is a complete database and not a truncated one.
func copyQueueFixture(t *testing.T, dst, src string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(src + suffix)
		if err != nil {
			if os.IsNotExist(err) {
				continue // a cleanly closed database has no write-ahead files
			}
			t.Fatalf("read the queue fixture %q: %v", src+suffix, err)
		}
		if err := os.WriteFile(dst+suffix, data, 0o600); err != nil {
			t.Fatalf("write the queue fixture copy %q: %v", dst+suffix, err)
		}
	}
}

// storedConcurrency reads one queue's stored limit through a plain database
// client, which is what an operator's second terminal is.
//
// It must NOT read through an engine: building an engine registers the queues
// and so rewrites the very row under test back to that engine's start-up value.
// That overwrite is real behaviour, and it is pinned separately by
// TestSetQueueConcurrency_IsReplacedWhenTheDaemonStartsAgain.
func storedConcurrency(t *testing.T, dbPath, queueName string) *int {
	t.Helper()
	db, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		t.Fatalf("OpenSharedDB(%q): %v", dbPath, err)
	}
	client, err := dbos.NewClient(context.Background(), dbos.ClientConfig{SQLiteSystemDB: db})
	if err != nil {
		_ = db.Close()
		t.Fatalf("dbos.NewClient: %v", err)
	}
	defer func() {
		// The client owns the handle it was given and closes it on shutdown.
		if err := dbos.Shutdown(client, 10*time.Second); err != nil {
			t.Errorf("client shutdown: %v", err)
		}
	}()

	q, err := dbos.RetrieveQueue(client, queueName)
	if err != nil {
		t.Fatalf("RetrieveQueue(%q): %v", queueName, err)
	}
	return q.GetWorkerConcurrency()
}

func requireStoredConcurrency(t *testing.T, dbPath, queueName string, want int) {
	t.Helper()
	got := storedConcurrency(t, dbPath, queueName)
	if got == nil {
		t.Fatalf("%s stored limit = no limit, want %d", queueName, want)
	}
	if *got != want {
		t.Fatalf("%s stored limit = %d, want %d", queueName, *got, want)
	}
}

// TestResolveQueueSelector pins which arguments an operator may give. Both the
// short name and the stored name are accepted, because the stored name is what
// appears in the database and in log lines and is therefore what an operator
// will paste in.
func TestResolveQueueSelector(t *testing.T) {
	t.Parallel()
	tests := []struct {
		arg        string
		wantStored string
	}{
		{arg: "slice", wantStored: engine.SliceQueueName},
		{arg: "control", wantStored: engine.ControlQueueName},
		{arg: engine.SliceQueueName, wantStored: engine.SliceQueueName},
		{arg: engine.ControlQueueName, wantStored: engine.ControlQueueName},
		{arg: "  slice  ", wantStored: engine.SliceQueueName},
	}
	for _, tc := range tests {
		t.Run(tc.arg, func(t *testing.T) {
			t.Parallel()
			_, stored, err := handlers.ResolveQueueSelector(tc.arg)
			if err != nil {
				t.Fatalf("ResolveQueueSelector(%q): %v", tc.arg, err)
			}
			if stored != tc.wantStored {
				t.Errorf("stored name = %q, want %q", stored, tc.wantStored)
			}
		})
	}
}

// TestResolveQueueSelector_RejectsAnUnknownQueue verifies the refusal names
// every queue the operator could have meant, so the next attempt succeeds.
func TestResolveQueueSelector_RejectsAnUnknownQueue(t *testing.T) {
	t.Parallel()
	_, _, err := handlers.ResolveQueueSelector("slices")
	if err == nil {
		t.Fatal("ResolveQueueSelector(\"slices\") returned no error; want a validation error")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("error is %T, want a structured error", err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("category = %v, want %v", se.Category, pasterrors.CategoryValidation)
	}
	for _, want := range []string{"slice", "control", engine.SliceQueueName, engine.ControlQueueName} {
		if !strings.Contains(se.Fix, want) {
			t.Errorf("the fix does not mention %q; an operator cannot correct the name from it:\n%s", want, se.Fix)
		}
	}
}

// TestQueueConcurrency_ReadsTheStoredSetting verifies the read path against a
// database a daemon has run against.
func TestQueueConcurrency_ReadsTheStoredSetting(t *testing.T) {
	t.Parallel()
	dbPath := registeredQueueDB(t)

	for _, queue := range []string{"slice", "control"} {
		code, err := handlers.QueueConcurrency(handlers.QueueConcurrencyInput{
			DBPath: dbPath,
			Queue:  queue,
		}, types.OutputText)
		if err != nil {
			t.Fatalf("QueueConcurrency(%q): %v", queue, err)
		}
		if code != 0 {
			t.Errorf("QueueConcurrency(%q) exit code = %d, want 0", queue, code)
		}
	}
}

// TestSetQueueConcurrency_WritesAndReadsBack verifies the write path: the
// stored row changes, and reading it back through a different process handle
// sees the change.
func TestSetQueueConcurrency_WritesAndReadsBack(t *testing.T) {
	t.Parallel()
	dbPath := registeredQueueDB(t)
	requireStoredConcurrency(t, dbPath, engine.SliceQueueName, engine.DefaultSliceQueueConcurrency)

	const raised = 3
	code, err := handlers.SetQueueConcurrency(handlers.QueueConcurrencyInput{
		DBPath: dbPath,
		Queue:  "slice",
		Limit:  raised,
	}, types.OutputText)
	if err != nil {
		t.Fatalf("SetQueueConcurrency: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	requireStoredConcurrency(t, dbPath, engine.SliceQueueName, raised)

	// The other queue is untouched: the command changes exactly the queue it
	// was given.
	requireStoredConcurrency(t, dbPath, engine.ControlQueueName, 1)
}

// TestSetQueueConcurrency_RejectsAnUnusableNumber verifies that a number that
// would stop the queue is refused before anything is written.
func TestSetQueueConcurrency_RejectsAnUnusableNumber(t *testing.T) {
	t.Parallel()
	dbPath := registeredQueueDB(t)

	for _, limit := range []int{0, -4} {
		code, err := handlers.SetQueueConcurrency(handlers.QueueConcurrencyInput{
			DBPath: dbPath,
			Queue:  "slice",
			Limit:  limit,
		}, types.OutputText)
		if err == nil {
			t.Fatalf("SetQueueConcurrency(%d) returned no error; want a validation error", limit)
		}
		if code != exitValidation {
			t.Errorf("exit code = %d, want %d", code, exitValidation)
		}
	}
	requireStoredConcurrency(t, dbPath, engine.SliceQueueName, engine.DefaultSliceQueueConcurrency)
}

// TestQueueConcurrency_ReportsAQueueThatWasNeverRegistered verifies the error an
// operator meets most often: they run the command against a database no daemon
// has started against yet.
func TestQueueConcurrency_ReportsAQueueThatWasNeverRegistered(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	code, err := handlers.QueueConcurrency(handlers.QueueConcurrencyInput{
		DBPath: dbPath,
		Queue:  "slice",
	}, types.OutputText)
	if err == nil {
		t.Fatal("QueueConcurrency returned no error for a database with no queues; want a storage error")
	}
	if code != exitStorage {
		t.Errorf("exit code = %d, want %d", code, exitStorage)
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("error is %T, want a structured error", err)
	}
	// Assert on wording unique to the never-registered case: the generic
	// read-failure message also mentions the daemon.
	if !strings.Contains(se.What, "is not in this pasture database") {
		t.Errorf("the message does not say the queue was never registered:\n%s", se.What)
	}
	if !strings.Contains(se.Fix, "Start the daemon") {
		t.Errorf("the fix does not tell the operator to start the daemon:\n%s", se.Fix)
	}
}

// TestSetQueueConcurrency_IsReplacedWhenTheDaemonStartsAgain pins how long an
// operator's change lasts: until the daemon starts again.
//
// A daemon writes its configured limit into the shared row every time it
// starts, so a start-up value always replaces a value set at run time. This is
// the deliberate consequence of letting a starting process govern the queue it
// is about to serve, and an operator must be told, because otherwise a restart
// silently undoes their change. The command's help text says so.
func TestSetQueueConcurrency_IsReplacedWhenTheDaemonStartsAgain(t *testing.T) {
	t.Parallel()
	dbPath := registeredQueueDB(t) // the daemon has started once

	if _, err := handlers.SetQueueConcurrency(handlers.QueueConcurrencyInput{
		DBPath: dbPath,
		Queue:  "slice",
		Limit:  3,
	}, types.OutputText); err != nil {
		t.Fatalf("SetQueueConcurrency: %v", err)
	}
	requireStoredConcurrency(t, dbPath, engine.SliceQueueName, 3)

	restartDaemonAt(t, dbPath) // the daemon starts again, with its own configured limit
	requireStoredConcurrency(t, dbPath, engine.SliceQueueName, engine.DefaultSliceQueueConcurrency)
}

// TestSetQueueConcurrency_RefusesTheControlQueue pins the refusal of a change
// that pasture could not honour.
//
// The control queue runs one epoch control workflow at a time in each process
// by design: its limit is fixed where the queue is created and no option
// changes it. Accepting a number for it would write a value that the next
// daemon start replaces with one, so the operator would see their change
// disappear with no explanation. The command refuses instead, and says why.
// Reading the queue stays allowed.
func TestSetQueueConcurrency_RefusesTheControlQueue(t *testing.T) {
	t.Parallel()
	dbPath := registeredQueueDB(t)

	for _, name := range []string{"control", engine.ControlQueueName} {
		code, err := handlers.SetQueueConcurrency(handlers.QueueConcurrencyInput{
			DBPath: dbPath,
			Queue:  name,
			Limit:  4,
		}, types.OutputText)
		if err == nil {
			t.Fatalf("SetQueueConcurrency(%q) returned no error; want a refusal", name)
		}
		if code != exitValidation {
			t.Errorf("exit code = %d, want %d", code, exitValidation)
		}
		var se *pasterrors.StructuredError
		if !errors.As(err, &se) {
			t.Fatalf("error is %T, want a structured error", err)
		}
		if se.Category != pasterrors.CategoryValidation {
			t.Errorf("category = %v, want %v", se.Category, pasterrors.CategoryValidation)
		}
		// The refusal must say WHY, not only no. The reason an operator needs is
		// that the driver of an epoch runs for as long as the epoch does, so one
		// job at a time means one epoch at a time.
		for _, want := range []string{"driver of one epoch", "as long as the epoch does", "one epoch at a time"} {
			if !strings.Contains(se.Why, want) {
				t.Errorf("the reason does not contain %q, so it does not explain the limit:\n%s", want, se.Why)
			}
		}
		if !strings.Contains(se.Fix, string(handlers.QueueSelectorSlice)) {
			t.Errorf("the fix does not point at the queue that can be changed:\n%s", se.Fix)
		}
	}

	// The refusal changed nothing, and reading is still allowed.
	requireStoredConcurrency(t, dbPath, engine.ControlQueueName, 1)
	if _, err := handlers.QueueConcurrency(handlers.QueueConcurrencyInput{
		DBPath: dbPath,
		Queue:  "control",
	}, types.OutputText); err != nil {
		t.Errorf("reading the control queue must stay allowed; got: %v", err)
	}
}
