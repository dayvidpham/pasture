// Package enginetest builds durable-engine fixtures for tests in other
// packages.
//
// It lives beside the engine rather than in internal/testutil because building
// one of these fixtures needs the engine itself. internal/engine has white-box
// test files in package engine, so if any of them ever imported
// internal/testutil and internal/testutil imported the engine, that would be an
// import cycle. A separate package cannot be walked into that way, and it also
// keeps the durable runtime out of the compile of every test binary that uses
// internal/testutil.
//
// Nothing here is used by production code.
package enginetest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/testutil"
)

// registeredQueuesShutdownTimeout is what the fixture builder gives the engine
// to stop. The engine has no work of its own to drain, so reaching this means
// something is wrong rather than busy.
const registeredQueuesShutdownTimeout = 10 * time.Second

// fixtureIdentity is the executor and application identity the fixture engine
// runs under. It is fixed and distinct from any production value, so a fixture
// can never be mistaken for a real deployment in a database someone inspects.
const fixtureIdentity = "enginetest-registered-queues"

var registeredQueuesDB struct {
	once sync.Once
	path string
	err  error
}

// RegisteredQueuesDBPath returns a per-test copy of a database in the state one
// daemon start leaves behind: the work queues registered, each with the limit
// the engine is configured with.
//
// The source is built ONCE per test binary and copied for each test, in the same
// shape as testutil.GoldenUnifiedDBPath. Building it means starting and stopping
// a whole durable engine, which is expensive enough that doing it in several
// parallel tests loads the machine measurably; copying a file does not. Each
// caller gets its own copy, so a test that changes a queue setting cannot
// disturb another.
func RegisteredQueuesDBPath(t *testing.T) string {
	t.Helper()
	src := registeredQueuesSource(t)
	dst := filepath.Join(t.TempDir(), "pasture.db")
	if err := testutil.CopyFile(dst, src); err != nil {
		t.Fatalf("copy the registered-queues database: %v", err)
	}
	return dst
}

func registeredQueuesSource(t *testing.T) string {
	t.Helper()
	registeredQueuesDB.once.Do(func() {
		dir := filepath.Join(os.TempDir(), fmt.Sprintf("pasture-registered-queues-%d", os.Getpid()))
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			registeredQueuesDB.err = fmt.Errorf("create the fixture directory: %w", err)
			return
		}
		// Registered as soon as it exists, so it is deleted at the end of the
		// run even if the build below fails half way and leaves a partial file.
		testutil.RegisterFixtureDir(dir)
		dbPath := filepath.Join(dir, "pasture.db")

		// Registering the queues is a side effect of building the engine, which
		// is exactly what one daemon start does. The engine is not launched: it
		// must write the queue rows, not serve them.
		e, err := engine.New(context.Background(), engine.Config{
			DBPath:             dbPath,
			ApplicationVersion: fixtureIdentity,
			ExecutorID:         fixtureIdentity,
		})
		if err != nil {
			registeredQueuesDB.err = fmt.Errorf("build the engine that registers the queues: %w", err)
			return
		}
		// The stop has to be CLEAN before the file is touched. An incomplete
		// stop means some part of the runtime is still writing, and the
		// checkpoint below would then truncate the log under it and leave a
		// SHORT database — which would be copied into every test that asks for
		// this fixture, and would fail them far away from this cause.
		if err := e.Shutdown(registeredQueuesShutdownTimeout); err != nil {
			registeredQueuesDB.err = fmt.Errorf("stop the engine that registered the queues: %w", err)
			return
		}

		// Fold the write-ahead log back in, so the single file is a complete
		// database and the copies below are not missing the queue rows.
		if err := testutil.CheckpointWAL(dbPath); err != nil {
			registeredQueuesDB.err = err
			return
		}
		registeredQueuesDB.path = dbPath
	})
	if registeredQueuesDB.err != nil {
		t.Fatalf("build the registered-queues database: %v", registeredQueuesDB.err)
	}
	return registeredQueuesDB.path
}
