package release_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/release"
	"github.com/dayvidpham/pasture/internal/types"
)

// ─── SemVer parsing ──────────────────────────────────────────────────────────

func TestParseSemVer(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
		want    release.SemVer
	}{
		{"1.2.3", false, release.SemVer{Major: 1, Minor: 2, Patch: 3}},
		{"0.0.0", false, release.SemVer{Major: 0, Minor: 0, Patch: 0}},
		{"10.20.30", false, release.SemVer{Major: 10, Minor: 20, Patch: 30}},
		{"  1.2.3  ", false, release.SemVer{Major: 1, Minor: 2, Patch: 3}}, // whitespace stripped
		{"v1.2.3", true, release.SemVer{}},
		{"1.2", true, release.SemVer{}},
		{"1.2.3.4", true, release.SemVer{}},
		{"abc", true, release.SemVer{}},
		{"", true, release.SemVer{}},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := release.ParseSemVer(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ParseSemVer(%q) expected error, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSemVer(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("ParseSemVer(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// ─── SemVer bumping ──────────────────────────────────────────────────────────

func TestSemVerBump(t *testing.T) {
	base := release.SemVer{Major: 1, Minor: 2, Patch: 3}
	tests := []struct {
		kind types.BumpKind
		want release.SemVer
	}{
		{types.BumpPatch, release.SemVer{Major: 1, Minor: 2, Patch: 4}},
		{types.BumpMinor, release.SemVer{Major: 1, Minor: 3, Patch: 0}},
		{types.BumpMajor, release.SemVer{Major: 2, Minor: 0, Patch: 0}},
	}
	for _, tc := range tests {
		t.Run(string(tc.kind), func(t *testing.T) {
			got := base.Bump(tc.kind)
			if got != tc.want {
				t.Errorf("Bump(%s) = %v, want %v", tc.kind, got, tc.want)
			}
		})
	}
}

func TestSemVerString(t *testing.T) {
	v := release.SemVer{Major: 1, Minor: 2, Patch: 3}
	if got := v.String(); got != "1.2.3" {
		t.Errorf("SemVer.String() = %q, want %q", got, "1.2.3")
	}
}

// ─── PyprojectVersionFile ────────────────────────────────────────────────────

func TestPyprojectVersionFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pyproject.toml")
	content := "[project]\nname = \"foo\"\nversion = \"1.2.3\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	vf := release.NewPyprojectVersionFile("pyproject.toml", path)

	got, err := vf.Read()
	if err != nil {
		t.Fatalf("Read() error: %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("Read() = %q, want %q", got, "1.2.3")
	}

	// Write a new version.
	if err := vf.Write("2.0.0", false); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	got, err = vf.Read()
	if err != nil {
		t.Fatal(err)
	}
	if got != "2.0.0" {
		t.Errorf("after Write, Read() = %q, want %q", got, "2.0.0")
	}
}

func TestPyprojectVersionFileDryRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pyproject.toml")
	content := "[project]\nversion = \"1.0.0\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	vf := release.NewPyprojectVersionFile("pyproject.toml", path)
	if err := vf.Write("9.9.9", true); err != nil {
		t.Fatal(err)
	}
	// File should be unchanged.
	got, _ := vf.Read()
	if got != "1.0.0" {
		t.Errorf("dry-run should not modify file; got %q", got)
	}
}

// ─── JsonVersionFile ─────────────────────────────────────────────────────────

func TestJsonVersionFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	content := `{"name":"foo","version":"0.1.0"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	vf := release.NewJsonVersionFile("package.json", path)
	got, err := vf.Read()
	if err != nil {
		t.Fatalf("Read() error: %v", err)
	}
	if got != "0.1.0" {
		t.Errorf("Read() = %q, want %q", got, "0.1.0")
	}

	if err := vf.Write("1.0.0", false); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	got, err = vf.Read()
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.0.0" {
		t.Errorf("after Write, Read() = %q, want %q", got, "1.0.0")
	}
}

// ─── MarketplaceVersionFile ───────────────────────────────────────────────────

func TestMarketplaceVersionFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "marketplace.json")
	content := `{"metadata":{"version":"3.2.1"},"plugins":[]}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	vf := release.NewMarketplaceVersionFile("marketplace.json", path)
	got, err := vf.Read()
	if err != nil {
		t.Fatalf("Read() error: %v", err)
	}
	if got != "3.2.1" {
		t.Errorf("Read() = %q, want %q", got, "3.2.1")
	}

	if err := vf.Write("4.0.0", false); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	got, _ = vf.Read()
	if got != "4.0.0" {
		t.Errorf("after Write, Read() = %q, want %q", got, "4.0.0")
	}
}

// ─── DiscoverVersionFiles ─────────────────────────────────────────────────────

func TestDiscoverVersionFiles_Pyproject(t *testing.T) {
	root := t.TempDir()
	pyproject := "[project]\nversion = \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(pyproject), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := release.DiscoverVersionFiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Name() != "pyproject.toml" {
		t.Errorf("Name() = %q, want %q", files[0].Name(), "pyproject.toml")
	}
}

func TestDiscoverVersionFiles_MultipleTypes(t *testing.T) {
	root := t.TempDir()

	// pyproject.toml
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"),
		[]byte("[project]\nversion = \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// package.json
	if err := os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"version":"1.0.0"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// .claude-plugin/plugin.json
	pluginDir := filepath.Join(root, ".claude-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"),
		[]byte(`{"version":"1.0.0"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// .claude-plugin/marketplace.json
	if err := os.WriteFile(filepath.Join(pluginDir, "marketplace.json"),
		[]byte(`{"metadata":{"version":"1.0.0"},"plugins":[]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := release.DiscoverVersionFiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 4 {
		t.Errorf("expected 4 files, got %d", len(files))
	}
}

func TestDiscoverVersionFiles_Subdirs(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "frontend")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "package.json"),
		[]byte(`{"version":"2.0.0"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := release.DiscoverVersionFiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file in subdir, got %d", len(files))
	}
	wantName := "frontend/package.json"
	if files[0].Name() != wantName {
		t.Errorf("Name() = %q, want %q", files[0].Name(), wantName)
	}
}

func TestDiscoverVersionFiles_SkipNodeModules(t *testing.T) {
	root := t.TempDir()
	nm := filepath.Join(root, "node_modules", "some-pkg")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "package.json"),
		[]byte(`{"version":"0.0.1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := release.DiscoverVersionFiles(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files (node_modules skipped), got %d", len(files))
	}
}
