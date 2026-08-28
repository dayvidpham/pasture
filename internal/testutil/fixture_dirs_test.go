package testutil_test

// Black-box tests for the fixture-directory registry, exercised through the
// public API that every fixture builder uses.
//
// Nothing pinned this before, and the leak it exists to stop was silent for
// months: a directory abandoned per run, per test binary, noticed only when
// somebody measured the temporary directory. A cleanup nobody checks is a
// cleanup that stops working without anybody noticing, so it is checked here.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/testutil"
)

// makeDirWithFile creates a directory holding one file, and returns the
// directory. The file matters: RemoveAll on a directory with contents is a
// different operation from removing an empty one, and a fixture directory
// always holds a database.
func makeDirWithFile(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create %q: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pasture.db"), []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write into %q: %v", dir, err)
	}
	return dir
}

func requireGone(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("%q still exists after RemoveFixtureDirs (stat error: %v)", dir, err)
	}
}

func requireStillThere(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("%q should have been left alone: %v", dir, err)
	}
}

// TestFixtureDirRegistry covers the whole contract in the order a test binary
// meets it: register what you built, remove it all at the end, and do not touch
// anything you were not given.
//
// The registry is process-global by design — it serves a whole test binary — so
// this test cannot run in parallel with anything else that registers a
// directory. It is the only test in this package that uses the registry, and it
// deliberately does not call t.Parallel.
func TestFixtureDirRegistry(t *testing.T) {
	parent := t.TempDir()

	registered := []string{
		makeDirWithFile(t, parent, "fixture-one"),
		makeDirWithFile(t, parent, "fixture-two"),
	}
	// Never registered, so it must survive: a remover that deleted everything
	// nearby would pass a weaker test and destroy a neighbour's data.
	unregistered := makeDirWithFile(t, parent, "not-registered")

	for _, dir := range registered {
		testutil.RegisterFixtureDir(dir)
	}
	testutil.RemoveFixtureDirs()

	for _, dir := range registered {
		requireGone(t, dir)
	}
	requireStillThere(t, unregistered)

	// A second call must do nothing at all, and the way to see that is to put a
	// directory BACK at a path that was removed. A remover that kept its list
	// would delete it again — which is the real danger, because by then the path
	// may belong to something else entirely. Deleting an already-deleted path is
	// invisible, so a weaker check here would pass against a registry that never
	// forgets.
	reborn := makeDirWithFile(t, parent, "fixture-one")
	testutil.RemoveFixtureDirs()
	requireStillThere(t, reborn)
	requireStillThere(t, unregistered)
	if err := os.RemoveAll(reborn); err != nil {
		t.Fatalf("clean up %q: %v", reborn, err)
	}

	// The registry is empty again, so it can be used a second time in the same
	// process. A builder that rebuilds its fixture after a removal depends on
	// this.
	reused := makeDirWithFile(t, parent, "fixture-three")
	testutil.RegisterFixtureDir(reused)
	testutil.RemoveFixtureDirs()
	requireGone(t, reused)
	requireStillThere(t, unregistered)
}

// TestFixtureDirRegistryIgnoresNothingToDo covers the two registrations that
// have nothing to delete: a directory that is already gone, and an empty path.
// Both happen in practice — a run that removed its own directory early still
// reaches TestMain, and a builder that failed before creating anything has ""
// to offer.
//
// Honest note on what this proves. The already-gone case is a real guarantee:
// removal must not fail the run. The empty path is NOT pinned here, and cannot
// be: os.RemoveAll("") does nothing and reports success, so the guard in the
// registry is cheap defence rather than load-bearing behaviour, and a test that
// claimed otherwise would be theatre. What is asserted is the observable part —
// neither registration disturbs anything and neither ends the run.
func TestFixtureDirRegistryIgnoresNothingToDo(t *testing.T) {
	parent := t.TempDir()
	kept := makeDirWithFile(t, parent, "kept")
	gone := makeDirWithFile(t, parent, "already-removed")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatalf("pre-remove %q: %v", gone, err)
	}

	testutil.RegisterFixtureDir("")
	testutil.RegisterFixtureDir(gone)
	testutil.RemoveFixtureDirs() // must not panic and must not fail the run

	requireStillThere(t, parent)
	requireStillThere(t, kept)
}
