package fsatomic_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/install/fsatomic"
)

func TestWriteFileAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	if err := fsatomic.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := fsatomic.WriteFile(path, []byte("second"), 0o600); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "second" {
		t.Errorf("content = %q, want second", got)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
	// No temp files remain after a successful commit.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if len(e.Name()) >= 12 && e.Name()[:12] == ".pasture-tmp" {
			t.Errorf("stray temp left behind: %s", e.Name())
		}
	}
}

func TestWriteFileRejectsSymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	realTarget := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(realTarget, []byte("sensitive"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "state.yaml")
	if err := os.Symlink(realTarget, link); err != nil {
		t.Fatal(err)
	}
	if err := fsatomic.WriteFile(link, []byte("attacker"), 0o600); err == nil {
		t.Fatal("write through symlink = nil error, want rejection")
	}
	// The symlink target must be untouched.
	got, _ := os.ReadFile(realTarget)
	if string(got) != "sensitive" {
		t.Errorf("symlink target was modified: %q", got)
	}
}

func TestWriteFileLeavesCrashOrphanUntouched(t *testing.T) {
	dir := t.TempDir()
	// Simulate a crash orphan from a previous process: a stale temp file.
	orphan := filepath.Join(dir, ".pasture-tmp-deadbeef0000")
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.yaml")
	if err := fsatomic.WriteFile(path, []byte("committed"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The orphan is neither scanned, deleted, nor reused.
	got, err := os.ReadFile(orphan)
	if err != nil || string(got) != "orphan" {
		t.Errorf("crash orphan was touched: err=%v content=%q", err, got)
	}
	committed, _ := os.ReadFile(path)
	if string(committed) != "committed" {
		t.Errorf("committed content = %q", committed)
	}
}
