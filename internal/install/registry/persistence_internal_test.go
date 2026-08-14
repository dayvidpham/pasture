//go:build linux || darwin

package registry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedRenamePreservesCommittedRegistryAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installations.yaml")
	initial := New()
	if err := Save(path, initial); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	original := registryUnixRenameat
	registryUnixRenameat = func(int, string, int, string) error { return errors.New("injected rename failure") }
	t.Cleanup(func() { registryUnixRenameat = original })
	if err := Save(path, initial); err == nil || !strings.Contains(err.Error(), "rename") {
		t.Fatalf("Save error=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("committed bytes changed\nwant %q\ngot  %q", want, got)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("committed mode=%04o", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pasture-registry-") {
			t.Fatalf("temporary residue %q", entry.Name())
		}
	}
}
