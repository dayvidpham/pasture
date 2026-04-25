package tasks_test

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
)

func TestDefaultDBPath_EnvOverride(t *testing.T) {
	// $PASTURE_DB_PATH wins over both the unified default and any XDG path.
	// The exact filename is whatever the caller passed — DefaultDBPath does
	// not enforce DefaultDBFilename when the env var is set, because users
	// pointing at a custom path know what file they want.
	t.Setenv(tasks.DBPathEnv, "/custom/path/custom.db")
	got := tasks.DefaultDBPath()
	if got != "/custom/path/custom.db" {
		t.Fatalf("expected env override to win, got %q", got)
	}
}

func TestDefaultDBPath_XDG(t *testing.T) {
	// PROPOSAL-2 §7.1: when XDG_DATA_HOME is set the unified database lives
	// at $XDG_DATA_HOME/pasture/pasture.db (NOT provenance.db or audit.db,
	// which were the pre-PROPOSAL-2 defaults for the two subsystems).
	t.Setenv(tasks.DBPathEnv, "")
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	got := tasks.DefaultDBPath()
	want := filepath.Join("/xdg/data", "pasture", tasks.DefaultDBFilename)
	if got != want {
		t.Fatalf("expected XDG path %q, got %q", want, got)
	}
	// Belt-and-braces: the filename must literally be "pasture.db" so the
	// hjsdt CLI tests (Scenario 9) and the unified pastured daemon both
	// open the same on-disk file.
	if filepath.Base(got) != "pasture.db" {
		t.Fatalf("expected unified filename pasture.db, got %q", filepath.Base(got))
	}
}

func TestDefaultDBPath_HomeFallback(t *testing.T) {
	// os.UserHomeDir honors HOME on linux/darwin and USERPROFILE on windows.
	// Gating on linux keeps the assertion deterministic across CI platforms.
	if runtime.GOOS != "linux" {
		t.Skipf("HOME-based fallback test runs only on linux (got %s)", runtime.GOOS)
	}
	t.Setenv(tasks.DBPathEnv, "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/test")
	got := tasks.DefaultDBPath()
	want := filepath.Join("/home/test", ".local", "share", "pasture", tasks.DefaultDBFilename)
	if got != want {
		t.Fatalf("expected HOME fallback %q, got %q", want, got)
	}
	if filepath.Base(got) != "pasture.db" {
		t.Fatalf("expected unified filename pasture.db, got %q", filepath.Base(got))
	}
}

// TestDefaultDBFilename_IsUnified asserts the filename constant matches the
// PROPOSAL-2 §7.1 binding. If this changes the hjsdt CLI tests (Scenario 9)
// and the pastured daemon will silently diverge to different files, breaking
// the single-file invariant.
func TestDefaultDBFilename_IsUnified(t *testing.T) {
	if tasks.DefaultDBFilename != "pasture.db" {
		t.Fatalf("PROPOSAL-2 §7.1 binds DefaultDBFilename to %q; got %q",
			"pasture.db", tasks.DefaultDBFilename)
	}
}

func TestOpenTracker_CreatesDirectoryAndOpens(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "subdir", "test.db")

	tr, err := tasks.OpenTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTracker failed: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })

	if tr == nil {
		t.Fatal("expected non-nil tracker")
	}
}

func TestOpenTracker_FailsForUnopenablePath(t *testing.T) {
	// Pointing at a path under an existing file — MkdirAll and SQLite both fail.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	dbPath := filepath.Join(blocker, "nested", "test.db")

	_, err := tasks.OpenTracker(dbPath)
	if err == nil {
		t.Fatal("expected error when database parent path is not a directory")
	}

	var se *pasterrors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("expected *pasterrors.StructuredError, got %T: %v", err, err)
	}
	if se.Category != pasterrors.CategoryConnection {
		t.Fatalf("expected CategoryConnection, got %q", se.Category)
	}
}
