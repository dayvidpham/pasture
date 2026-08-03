package directfile_test

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/directfile"
)

// hookBundle builds a one-leaf bundle at plugin/pasture-hooks.ts.
func hookBundle(t *testing.T, content string) artifact.Bundle {
	t.Helper()
	rel := "plugin/pasture-hooks.ts"
	p, _ := artifact.NewPath(rel)
	mode, _ := artifact.NewMode(0o644)
	entry, err := artifact.NewFileEntry(p, mode, artifact.DigestBytes([]byte(content)))
	if err != nil {
		t.Fatalf("NewFileEntry: %v", err)
	}
	manifest, err := artifact.NewManifest(entry)
	if err != nil {
		t.Fatalf("NewManifest: %v", err)
	}
	src := fstest.MapFS{rel: &fstest.MapFile{Data: []byte(content), Mode: 0o644}}
	bundle, err := artifact.NewBundle(src, manifest)
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}
	return bundle
}

func TestEnsureCreatesAbsentLeaf(t *testing.T) {
	root := t.TempDir()
	bundle := hookBundle(t, "export default {}\n")
	out, err := directfile.Ensure(root, bundle, nil)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !out.Managed || out.External {
		t.Errorf("expected managed create, got managed=%v external=%v", out.Managed, out.External)
	}
	dest := filepath.Join(root, "plugin", "pasture-hooks.ts")
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "export default {}\n" {
		t.Fatalf("leaf not written: err=%v content=%q", err, got)
	}
	info, _ := os.Stat(dest)
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o", info.Mode().Perm())
	}
}

func TestEnsureIdempotentReRun(t *testing.T) {
	root := t.TempDir()
	bundle := hookBundle(t, "export default {}\n")
	first, err := directfile.Ensure(root, bundle, nil)
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	// Re-run with the prior managed leaves recorded; should be a no-op.
	second, err := directfile.Ensure(root, bundle, first.Leaves)
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if second.Managed {
		t.Errorf("re-run should not report a mutation")
	}
}

func TestEnsureExactExternalMatchSatisfiesWithoutAdoption(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "plugin", "pasture-hooks.ts")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("export default {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle := hookBundle(t, "export default {}\n")
	out, err := directfile.Ensure(root, bundle, nil) // no prior record
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if out.Managed {
		t.Errorf("external match must not be reported as managed")
	}
	if !out.External {
		t.Errorf("expected external match report")
	}
}

func TestEnsureRejectsForeignDifferingLeaf(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "plugin", "pasture-hooks.ts")
	_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	_ = os.WriteFile(dest, []byte("// hand-written\n"), 0o644)
	bundle := hookBundle(t, "export default {}\n")
	if _, err := directfile.Ensure(root, bundle, nil); err == nil {
		t.Fatal("foreign differing leaf = nil error, want rejection")
	}
	// The foreign file must be preserved.
	got, _ := os.ReadFile(dest)
	if string(got) != "// hand-written\n" {
		t.Errorf("foreign file was overwritten: %q", got)
	}
}

func TestEnsureRejectsModifiedManagedLeaf(t *testing.T) {
	root := t.TempDir()
	bundle := hookBundle(t, "export default {}\n")
	first, _ := directfile.Ensure(root, bundle, nil)
	dest := filepath.Join(root, "plugin", "pasture-hooks.ts")
	// User edits the managed leaf.
	_ = os.WriteFile(dest, []byte("edited\n"), 0o644)
	// Upgrade attempt with a new bundle; live drifted from prior record.
	newBundle := hookBundle(t, "export default { v: 2 }\n")
	if _, err := directfile.Ensure(root, newBundle, first.Leaves); err == nil {
		t.Fatal("modified managed leaf = nil error, want rejection")
	}
}

func TestEnsureRejectsSymlinkLeaf(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "plugin", "pasture-hooks.ts")
	_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	target := filepath.Join(root, "elsewhere")
	_ = os.WriteFile(target, []byte("x"), 0o644)
	if err := os.Symlink(target, dest); err != nil {
		t.Fatal(err)
	}
	bundle := hookBundle(t, "export default {}\n")
	if _, err := directfile.Ensure(root, bundle, nil); err == nil {
		t.Fatal("symlink leaf = nil error, want rejection")
	}
}

func TestRemoveOnlyMatchingAndPreservesSiblings(t *testing.T) {
	root := t.TempDir()
	bundle := hookBundle(t, "export default {}\n")
	out, _ := directfile.Ensure(root, bundle, nil)
	// A sibling the installer did not create.
	sibling := filepath.Join(root, "plugin", "other.ts")
	_ = os.WriteFile(sibling, []byte("keep me"), 0o644)

	if _, err := directfile.Remove(root, out.Leaves, out.CreatedDirs); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "plugin", "pasture-hooks.ts")); !os.IsNotExist(err) {
		t.Errorf("managed leaf not removed")
	}
	if got, err := os.ReadFile(sibling); err != nil || string(got) != "keep me" {
		t.Errorf("sibling not preserved: err=%v content=%q", err, got)
	}
	// The directory had a sibling, so it must remain.
	if _, err := os.Stat(filepath.Join(root, "plugin")); err != nil {
		t.Errorf("non-empty created dir was removed")
	}
}

func TestRemoveLeavesDriftedLeafAndReports(t *testing.T) {
	root := t.TempDir()
	bundle := hookBundle(t, "export default {}\n")
	out, _ := directfile.Ensure(root, bundle, nil)
	dest := filepath.Join(root, "plugin", "pasture-hooks.ts")
	_ = os.WriteFile(dest, []byte("drifted\n"), 0o644)
	if _, err := directfile.Remove(root, out.Leaves, out.CreatedDirs); err == nil {
		t.Fatal("removing a drifted managed leaf = nil error, want rejection")
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("drifted leaf was removed despite mismatch")
	}
}

func TestEnsureRemovesEmptyCreatedDir(t *testing.T) {
	root := t.TempDir()
	bundle := hookBundle(t, "export default {}\n")
	out, _ := directfile.Ensure(root, bundle, nil)
	if _, err := directfile.Remove(root, out.Leaves, out.CreatedDirs); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "plugin")); !os.IsNotExist(err) {
		t.Errorf("empty created dir should be removed")
	}
}

// nestedBundle builds a one-leaf bundle under a multi-level created directory
// (skills/pasture/SKILL.md), exercising intermediate-directory ownership.
func nestedBundle(t *testing.T, content string) artifact.Bundle {
	t.Helper()
	rel := "skills/pasture/SKILL.md"
	p, _ := artifact.NewPath(rel)
	mode, _ := artifact.NewMode(0o644)
	entry, err := artifact.NewFileEntry(p, mode, artifact.DigestBytes([]byte(content)))
	if err != nil {
		t.Fatalf("NewFileEntry: %v", err)
	}
	manifest, err := artifact.NewManifest(entry)
	if err != nil {
		t.Fatalf("NewManifest: %v", err)
	}
	src := fstest.MapFS{rel: &fstest.MapFile{Data: []byte(content), Mode: 0o644}}
	bundle, err := artifact.NewBundle(src, manifest)
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}
	return bundle
}

// TestEnsureRecordsAndRemovesEveryCreatedDirLevel pins that Ensure records every
// intermediate directory it creates (not only the deepest), so Remove reclaims
// the whole tree and leaves zero orphaned directories under root.
func TestEnsureRecordsAndRemovesEveryCreatedDirLevel(t *testing.T) {
	root := t.TempDir()
	bundle := nestedBundle(t, "# skill\n")
	out, err := directfile.Ensure(root, bundle, nil)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// Both levels must be recorded, shallowest-first, as relative paths.
	want := []string{"skills", "skills/pasture"}
	if len(out.CreatedDirs) != len(want) {
		t.Fatalf("created dirs = %v, want %v", out.CreatedDirs, want)
	}
	for i, w := range want {
		if out.CreatedDirs[i] != w {
			t.Errorf("created dir[%d] = %q, want %q", i, out.CreatedDirs[i], w)
		}
	}
	res, err := directfile.Remove(root, out.Leaves, out.CreatedDirs)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(res.PreservedDirs) != 0 {
		t.Errorf("preserved dirs = %v, want none", res.PreservedDirs)
	}
	// Zero orphaned directories: root must be empty.
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Errorf("orphaned entries under root: %v", entries)
	}
}

// TestRemovePreservesCreatedDirWithForeignFile pins that a created directory a
// foreign file moved into survives removal and is reported, never force-removed.
func TestRemovePreservesCreatedDirWithForeignFile(t *testing.T) {
	root := t.TempDir()
	bundle := nestedBundle(t, "# skill\n")
	out, err := directfile.Ensure(root, bundle, nil)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// A foreign file dropped into the deepest created directory.
	foreign := filepath.Join(root, "skills", "pasture", "notes.txt")
	if err := os.WriteFile(foreign, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := directfile.Remove(root, out.Leaves, out.CreatedDirs)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// The foreign file and both non-empty directories must survive.
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("foreign file was removed: %v", err)
	}
	if len(res.PreservedDirs) == 0 {
		t.Fatal("expected preserved-dir report for a non-empty created dir")
	}
	// The deepest non-empty dir must be reported.
	found := false
	for _, d := range res.PreservedDirs {
		if d == "skills/pasture" {
			found = true
		}
	}
	if !found {
		t.Errorf("preserved dirs = %v, want to include skills/pasture", res.PreservedDirs)
	}
}
