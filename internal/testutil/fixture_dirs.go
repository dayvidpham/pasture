package testutil

// fixture_dirs.go keeps one record of the directories fixture builders create,
// so a test binary can delete them all when it finishes.
//
// A fixture that is built once per binary cannot be cleaned up by t.Cleanup: it
// outlives the test that happened to build it. Removing it at the START of the
// next run does not help either, because the directory name carries the process
// id and the next run is a different process. Without a remover, every run of
// every test binary leaves a database behind for good — which is what happened:
// hundreds of directories and over a hundred megabytes accumulated on one
// machine over two months.
//
// So each builder registers the directory it created, and each test binary
// calls RemoveFixtureDirs once when its tests are done. A new fixture builder
// gets cleanup by registering, not by another sweep through every TestMain.

import (
	"os"
	"sync"
)

var fixtureDirs struct {
	mu   sync.Mutex
	dirs []string
}

// RegisterFixtureDir records a directory that a fixture builder created and that
// must outlive the test which built it. RemoveFixtureDirs deletes it later.
//
// Registering the same directory twice is harmless.
func RegisterFixtureDir(dir string) {
	if dir == "" {
		return
	}
	fixtureDirs.mu.Lock()
	defer fixtureDirs.mu.Unlock()
	fixtureDirs.dirs = append(fixtureDirs.dirs, dir)
}

// RemoveFixtureDirs deletes every directory registered so far and forgets them.
//
// Call it from TestMain, AFTER m.Run: a fixture is shared by the whole binary,
// so it is only safe to remove once every test has finished. It is deliberately
// silent about failures — a test run must not be reported as failed because a
// temporary directory could not be deleted — and it is safe to call in a binary
// that built no fixture at all.
func RemoveFixtureDirs() {
	fixtureDirs.mu.Lock()
	dirs := fixtureDirs.dirs
	fixtureDirs.dirs = nil
	fixtureDirs.mu.Unlock()

	for _, dir := range dirs {
		_ = os.RemoveAll(dir)
	}
}
