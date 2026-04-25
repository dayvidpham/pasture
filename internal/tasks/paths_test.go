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
	t.Setenv(tasks.DBPathEnv, "/custom/path/provenance.db")
	got := tasks.DefaultDBPath()
	if got != "/custom/path/provenance.db" {
		t.Fatalf("expected env override to win, got %q", got)
	}
}

func TestDefaultDBPath_XDG(t *testing.T) {
	t.Setenv(tasks.DBPathEnv, "")
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	got := tasks.DefaultDBPath()
	want := filepath.Join("/xdg/data", "pasture", "provenance.db")
	if got != want {
		t.Fatalf("expected XDG path %q, got %q", want, got)
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
	want := filepath.Join("/home/test", ".local", "share", "pasture", "provenance.db")
	if got != want {
		t.Fatalf("expected HOME fallback %q, got %q", want, got)
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
