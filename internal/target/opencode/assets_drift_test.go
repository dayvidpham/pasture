package opencode

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
)

func TestEmbeddedAssetsMatchOpenCodeHarness(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := findModuleRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	files, err := codegen.EmitHarness(codegen.RepoRoots(root), codegen.OpenCodeTarget, codegen.GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("render OpenCode harness: %v", err)
	}
	want := map[string][]byte{}
	for _, file := range files {
		rel, relErr := filepath.Rel(root, file.Path)
		if relErr != nil {
			t.Fatal(relErr)
		}
		rel = filepath.ToSlash(rel)
		var embedded string
		switch {
		case strings.HasPrefix(rel, ".opencode/skill/"):
			embedded = "assets/skills/" + strings.TrimPrefix(rel, ".opencode/skill/")
		case strings.HasPrefix(rel, ".opencode/agent/"):
			embedded = "assets/agents/" + strings.TrimPrefix(rel, ".opencode/agent/")
		case rel == codegen.OpenCodeHooksModulePath:
			embedded = "assets/hooks/pasture-hooks.ts"
		default:
			continue
		}
		want[embedded] = []byte(file.Content)
	}
	for name, expected := range want {
		got, readErr := assetsFS.ReadFile(name)
		if readErr != nil {
			t.Errorf("embedded generated asset %q is missing; run `go generate ./internal/target/opencode`: %v", name, readErr)
			continue
		}
		if !bytes.Equal(got, expected) {
			t.Errorf("embedded generated asset %q drifted; run `go generate ./internal/target/opencode`", name)
		}
	}
	err = fs.WalkDir(assetsFS, "assets", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if _, ok := want[name]; !ok {
			t.Errorf("embedded generated asset %q is stale; run `go generate ./internal/target/opencode`", name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded OpenCode assets: %v", err)
	}
}

func findModuleRoot(start string) (string, error) {
	for current := start; ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current, nil
		}
		if filepath.Dir(current) == current {
			return "", os.ErrNotExist
		}
	}
}
