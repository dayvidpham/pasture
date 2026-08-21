//go:build linux || darwin

package registry_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/install/registry"
	"golang.org/x/sys/unix"
)

func TestLoadRejectsFIFOAndSpecialFilesWithoutBlocking(t *testing.T) {
	for _, kind := range []string{"fifo", "directory"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "installations.yaml")
			if kind == "fifo" {
				if err := unix.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { _, err := registry.Load(path); result <- err }()
			var err error
			select {
			case err = <-result:
			case <-time.After(500 * time.Millisecond):
				if kind == "fifo" {
					if fd, openErr := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0); openErr == nil {
						_ = unix.Close(fd)
					}
				}
				select {
				case <-result:
				case <-time.After(time.Second):
				}
				t.Fatalf("Load blocked past its 500ms bound")
			}
			if err == nil || !strings.Contains(err.Error(), "regular") {
				t.Fatalf("Load error=%v", err)
			}
		})
	}
}

func TestSaveRejectsFIFOAndDirectoryWithoutBlocking(t *testing.T) {
	for _, kind := range []string{"fifo", "directory"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "installations.yaml")
			if kind == "fifo" {
				if err := unix.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { result <- registry.Save(path, registry.New()) }()
			select {
			case err := <-result:
				if err == nil || !strings.Contains(err.Error(), "regular") {
					t.Fatalf("Save error=%v", err)
				}
			case <-time.After(500 * time.Millisecond):
				if kind == "fifo" {
					if fd, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0); err == nil {
						_ = unix.Close(fd)
					}
				}
				select {
				case <-result:
				case <-time.After(time.Second):
				}
				t.Fatal("Save blocked past its 500ms bound")
			}
		})
	}
}

func TestLoadRejects0644BeforeDecoding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installations.yaml")
	if err := os.WriteFile(path, []byte("not: [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := registry.Load(path)
	if err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("Load error=%v, want descriptor mode rejection", err)
	}
}

func TestCanonicalProjectRootRejectsInaccessiblePath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory execute permissions")
	}
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	if _, err := registry.CanonicalProjectRoot(project); err == nil {
		t.Fatal("inaccessible project root accepted")
	}
}
