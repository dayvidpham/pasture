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
			started := time.Now()
			_, err := registry.Load(path)
			if err == nil || !strings.Contains(err.Error(), "regular") {
				t.Fatalf("Load error=%v", err)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("Load blocked for %s", elapsed)
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
