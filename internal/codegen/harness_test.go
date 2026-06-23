package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveHarnessUnknownTargetIsActionable(t *testing.T) {
	t.Parallel()

	_, err := ResolveHarness([]string{"claude"})
	if err == nil {
		t.Fatal("ResolveHarness(\"claude\") returned nil error, want unknown-target error")
	}
	got := err.Error()
	for _, want := range []string{"claude", "claude-code", "opencode"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ResolveHarness error %q does not contain %q", got, want)
		}
	}
}

func TestEmitHarnessClaudeCodeIsByteIdentical(t *testing.T) {
	t.Parallel()

	root := testModuleRoot(t)
	targets, err := ResolveHarness([]string{string(HarnessClaudeCode)})
	if err != nil {
		t.Fatalf("ResolveHarness: %v", err)
	}
	files, err := EmitHarness(root, targets[0], filepath.Join(root, "skills", "protocol", "figures"), GenerateOptions{
		Diff:  false,
		Write: false,
	})
	if err != nil {
		t.Fatalf("EmitHarness(%s): %v", HarnessClaudeCode, err)
	}
	if len(files) == 0 {
		t.Fatalf("EmitHarness(%s) returned no files", HarnessClaudeCode)
	}
	for _, file := range files {
		onDisk, err := os.ReadFile(file.Path)
		if err != nil {
			t.Fatalf("read generated baseline %q: %v", file.Path, err)
		}
		if string(onDisk) != file.Content {
			t.Fatalf("%s output changed; capture the byte-neutral baseline by running codegen on a clean tree, reviewing the diff for skills/ + agents/ + schema.xml, then committing the intended generated output", file.Path)
		}
	}
}

func TestEmitHarnessCombinedTargetsDoNotPerturbClaudeCode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeHarnessSeedFiles(t, root)
	targets, err := ResolveHarness([]string{string(HarnessClaudeCode), string(HarnessOpenCode)})
	if err != nil {
		t.Fatalf("ResolveHarness: %v", err)
	}

	claudeFiles, err := EmitHarness(root, targets[0], "", GenerateOptions{Diff: false, Write: true})
	if err != nil {
		t.Fatalf("EmitHarness(%s): %v", targets[0].Name, err)
	}
	before := readGeneratedFiles(t, claudeFiles)

	if _, err := EmitHarness(root, targets[1], "", GenerateOptions{Diff: false, Write: true}); err != nil {
		t.Fatalf("EmitHarness(%s): %v", targets[1].Name, err)
	}
	after := readGeneratedFiles(t, claudeFiles)
	for path, want := range before {
		if got := after[path]; got != want {
			t.Fatalf("OpenCode target changed claude-code output %q", path)
		}
	}

	// Dir-coverage: every role and command skill dir the emitter iterates must
	// have produced a SKILL.md under .opencode/skill/<dir>/. Enumerating the
	// same sources EmitHarness iterates (roleSkillItems/commandSkillItems)
	// guarantees the assertion fails if any single dir were dropped from the
	// OpenCode emission, not just the one previously spot-checked.
	var expectedSkillDirs []string
	for _, item := range roleSkillItems() {
		expectedSkillDirs = append(expectedSkillDirs, item.dir)
	}
	for _, item := range commandSkillItems() {
		expectedSkillDirs = append(expectedSkillDirs, item.dir)
	}
	if len(expectedSkillDirs) != len(roleSkillDirs)+len(commandSkillDirs) {
		t.Fatalf("expected %d OpenCode skill dirs, enumerated %d", len(roleSkillDirs)+len(commandSkillDirs), len(expectedSkillDirs))
	}
	for _, dir := range expectedSkillDirs {
		skillPath := filepath.Join(root, ".opencode", "skill", dir, "SKILL.md")
		if _, err := os.Stat(skillPath); err != nil {
			t.Fatalf("combined target did not emit OpenCode skill %q: %v", skillPath, err)
		}
	}
}

func writeHarnessSeedFiles(t *testing.T, root string) {
	t.Helper()
	for _, item := range roleSkillItems() {
		writeSeedSkill(t, filepath.Join(root, "skills", item.dir, "SKILL.md"))
	}
	for _, item := range commandSkillItems() {
		writeSeedSkill(t, filepath.Join(root, "skills", item.dir, "SKILL.md"))
	}
}

func writeSeedSkill(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create seed dir for %q: %v", path, err)
	}
	content := GeneratedBegin + "\n" + GeneratedEnd + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write seed skill %q: %v", path, err)
	}
}

func readGeneratedFiles(t *testing.T, files []GeneratedFile) map[string]string {
	t.Helper()
	out := make(map[string]string, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file.Path)
		if err != nil {
			t.Fatalf("read generated file %q: %v", file.Path, err)
		}
		out[file.Path] = string(data)
	}
	return out
}

func testModuleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from %q", wd)
		}
		dir = parent
	}
}
