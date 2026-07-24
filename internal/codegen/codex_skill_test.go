package codegen

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCodexSkillsEmitRegisteredInventory(t *testing.T) {
	t.Parallel()

	moduleRoot := testModuleRoot(t)
	out := t.TempDir()
	files, err := EmitHarness(moduleRoot, out, CodexTarget, filepath.Join(moduleRoot, "skills", "protocol", "figures"), GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("EmitHarness(%s): %v", HarnessCodex, err)
	}

	skillRoot := filepath.Join(out, ".agents", "skills")
	byPath := make(map[string]GeneratedFile)
	for _, file := range files {
		rel, err := filepath.Rel(skillRoot, file.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 2 || parts[1] != "SKILL.md" {
			continue
		}
		rel = filepath.ToSlash(rel)
		if _, exists := byPath[rel]; exists {
			t.Fatalf("EmitHarness(%s) emitted duplicate skill path %q", HarnessCodex, rel)
		}
		byPath[rel] = file
	}

	wantPaths := make(map[string]struct{}, len(roleSkillDirs)+len(commandSkillDirs)+len(portableVerbatimDirs))
	for _, item := range roleSkillItems() {
		wantPaths[filepath.ToSlash(filepath.Join(item.dir, "SKILL.md"))] = struct{}{}
	}
	for _, item := range commandSkillItems() {
		wantPaths[filepath.ToSlash(filepath.Join(item.dir, "SKILL.md"))] = struct{}{}
	}
	for _, dir := range portableVerbatimDirs {
		wantPaths[filepath.ToSlash(filepath.Join(dir, "SKILL.md"))] = struct{}{}
	}
	missing := make([]string, 0)
	for path := range wantPaths {
		if _, ok := byPath[path]; !ok {
			missing = append(missing, path)
		}
	}
	orphaned := make([]string, 0)
	for path := range byPath {
		if _, ok := wantPaths[path]; !ok {
			orphaned = append(orphaned, path)
		}
	}
	sort.Strings(missing)
	sort.Strings(orphaned)
	if len(missing) > 0 || len(orphaned) > 0 {
		t.Fatalf("EmitHarness(%s) Codex SKILL.md inventory mismatch: missing %v, orphaned %v", HarnessCodex, missing, orphaned)
	}

	for rel, file := range byPath {
		dir := strings.Split(rel, "/")[0]
		frontmatter, _ := splitFrontmatter(t, file.Path, file.Content)
		var fm skillFrontmatter
		decoder := yaml.NewDecoder(strings.NewReader(frontmatter))
		decoder.KnownFields(true)
		if err := decoder.Decode(&fm); err != nil {
			t.Fatalf("%s: decode Codex skill frontmatter: %v", file.Path, err)
		}
		if strings.TrimSpace(fm.Name) == "" {
			t.Errorf("%s: Codex skill frontmatter has an empty required name", file.Path)
		}
		if strings.TrimSpace(fm.Description) == "" {
			t.Errorf("%s: Codex skill frontmatter has an empty required description", file.Path)
		}
		if fm.Name != dir {
			t.Errorf("%s: frontmatter name = %q, want deterministic directory name %q", file.Path, fm.Name, dir)
		}
		if fm.Skills != "" || fm.Tools != "" || fm.Model != "" {
			t.Errorf("%s: Codex skill frontmatter contains unsupported harness fields", file.Path)
		}
	}
}

func TestCodexVerbatimSupportTreesAreByteIdentical(t *testing.T) {
	t.Parallel()

	root := testModuleRoot(t)
	out := t.TempDir()
	files, err := EmitHarness(root, out, CodexTarget, filepath.Join(root, "skills", "protocol", "figures"), GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("EmitHarness(%s): %v", HarnessCodex, err)
	}
	for _, file := range files {
		rel, err := filepath.Rel(filepath.Join(out, ".agents", "skills"), file.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) == 0 || (parts[0] != "protocol" && parts[0] != "install-cli") {
			continue
		}
		source, err := os.ReadFile(filepath.Join(root, "skills", rel))
		if err != nil {
			t.Fatalf("read verbatim source for %s: %v", file.Path, err)
		}
		if file.Content != string(source) {
			t.Errorf("%s differs from verbatim source skills/%s", file.Path, filepath.ToSlash(rel))
		}
	}
}

func TestCodexEmissionIsDeterministic(t *testing.T) {
	t.Parallel()

	root := testModuleRoot(t)
	figuresDir := filepath.Join(root, "skills", "protocol", "figures")
	emit := func() map[string]string {
		t.Helper()
		out := t.TempDir()
		files, err := EmitHarness(root, out, CodexTarget, figuresDir, GenerateOptions{Diff: false, Write: false})
		if err != nil {
			t.Fatalf("EmitHarness(%s): %v", HarnessCodex, err)
		}
		byPath := make(map[string]string, len(files))
		for _, file := range files {
			rel, err := filepath.Rel(out, file.Path)
			if err != nil {
				t.Fatalf("relative Codex output path for %q: %v", file.Path, err)
			}
			if _, exists := byPath[rel]; exists {
				t.Fatalf("Codex emitted duplicate relative path %q", rel)
			}
			byPath[rel] = file.Content
		}
		return byPath
	}

	first := emit()
	second := emit()
	if len(first) != len(second) {
		t.Fatalf("Codex emission count changed between runs: %d then %d", len(first), len(second))
	}
	for path, want := range first {
		if got, ok := second[path]; !ok {
			t.Errorf("second Codex emission is missing %q", path)
		} else if got != want {
			t.Errorf("Codex output %q changed between identical emissions", path)
		}
	}
}

func TestCodexTargetDoesNotEmitPluginManifest(t *testing.T) {
	t.Parallel()

	if CodexTarget.Manifest != nil {
		t.Fatal("Codex target must not emit a plugin manifest for the direct Home Manager path")
	}
}
