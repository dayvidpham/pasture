package codegen

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
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
	files, err := EmitHarness(RepoRoots(root), targets[0], GenerateOptions{
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
	// Every hand-authored input stays in the real module tree; only the output
	// goes to the temporary root, so no input tree is copied or seeded.
	roots := HarnessRoots{Source: testModuleRoot(t), Output: root}
	targets, err := ResolveHarness([]string{string(HarnessClaudeCode), string(HarnessOpenCode)})
	if err != nil {
		t.Fatalf("ResolveHarness: %v", err)
	}

	claudeFiles, err := EmitHarness(roots, targets[0], GenerateOptions{Diff: false, Write: true})
	if err != nil {
		t.Fatalf("EmitHarness(%s): %v", targets[0].Name, err)
	}
	before := readGeneratedFiles(t, claudeFiles)

	if _, err := EmitHarness(roots, targets[1], GenerateOptions{Diff: false, Write: true}); err != nil {
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

// sourceGuardDirs are the subdirectories of the module root that a harness
// emission reads as input, plus every subdirectory a harness writes output to.
// A split-root run must leave all of them byte-identical and untouched.
var sourceGuardDirs = []string{"skills", "agents", "hooks", ".opencode", ".agents", ".codex"}

// TestEmitHarnessCopiesVerbatimFromSourceRootIntoSeparateOutputRoot proves the
// verbatim SOURCE root and the OUTPUT root are independent: the harness reads
// the hand-authored trees from the real module and writes every file into a
// temporary directory, with no copy of the source seeded there first. It also
// proves the source tree is never written.
func TestEmitHarnessCopiesVerbatimFromSourceRootIntoSeparateOutputRoot(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		target       TargetHarness
		verbatimDirs []string
	}{
		{name: string(HarnessOpenCode), target: OpenCodeTarget, verbatimDirs: openCodeVerbatimDirs},
		{name: string(HarnessCodex), target: CodexTarget, verbatimDirs: codexVerbatimDirs},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			sourceRoot := testModuleRoot(t)
			outputRoot := t.TempDir()
			beforeSource := snapshotTrees(t, sourceRoot, sourceGuardDirs)

			files, err := EmitHarness(
				HarnessRoots{Source: sourceRoot, Output: outputRoot},
				testCase.target,
				GenerateOptions{Diff: false, Write: true},
			)
			if err != nil {
				t.Fatalf("EmitHarness(%s) with a separate output root: %v", testCase.target.Name, err)
			}
			if len(files) == 0 {
				t.Fatalf("EmitHarness(%s) returned no files", testCase.target.Name)
			}

			// Nothing may be written below the source tree.
			assertTreesUnchanged(t, sourceRoot, sourceGuardDirs, beforeSource)

			if len(testCase.verbatimDirs) == 0 {
				t.Fatalf("target %s has no verbatim dirs to exercise", testCase.target.Name)
			}
			// Copy fidelity covers the WHOLE subtree: same file count, same
			// relative paths, and byte-identical content for every file.
			for _, dir := range testCase.verbatimDirs {
				srcDir := filepath.Join(sourceRoot, "skills", dir)
				dstDir := filepath.Join(outputRoot, testCase.target.SkillRoot, dir)
				want := hashTree(t, srcDir)
				got := hashTree(t, dstDir)
				if len(got) != len(want) {
					t.Fatalf("verbatim dir %q copied %d files to %q, want %d", srcDir, len(got), dstDir, len(want))
				}
				for rel, wantHash := range want {
					gotHash, ok := got[rel]
					if !ok {
						t.Fatalf("verbatim file %q was not copied into %q", rel, dstDir)
					}
					if gotHash != wantHash {
						t.Fatalf("verbatim file %q in %q is not byte-identical to the source", rel, dstDir)
					}
				}
			}

			// Nothing may be emitted into the source tree.
			for _, file := range files {
				rel, err := filepath.Rel(outputRoot, file.Path)
				if err != nil || strings.HasPrefix(rel, "..") {
					t.Fatalf("EmitHarness(%s) emitted %q outside the output root %q", testCase.target.Name, file.Path, outputRoot)
				}
			}
		})
	}
}

// TestEmitHarnessRejectsIncompleteRoots proves an unset root stops generation
// before any read or write, with a message that names the missing field.
func TestEmitHarnessRejectsIncompleteRoots(t *testing.T) {
	t.Parallel()

	missingSource := t.TempDir()
	sourceWithoutSkills := t.TempDir()
	sourceIsAFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(sourceIsAFile, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatalf("write %q: %v", sourceIsAFile, err)
	}

	cases := []struct {
		name   string
		target TargetHarness
		roots  HarnessRoots
		want   string
	}{
		{name: "both empty", target: OpenCodeTarget, roots: HarnessRoots{}, want: "Source and Output are empty"},
		{name: "source empty", target: OpenCodeTarget, roots: HarnessRoots{Output: "out"}, want: "Source is empty"},
		{name: "output empty", target: OpenCodeTarget, roots: HarnessRoots{Source: "src"}, want: "Output is empty"},
		{
			name:   "source does not exist",
			target: OpenCodeTarget,
			roots:  HarnessRoots{Source: filepath.Join(missingSource, "absent"), Output: t.TempDir()},
			want:   "cannot read the source root",
		},
		{
			name:   "source is a file",
			target: OpenCodeTarget,
			roots:  HarnessRoots{Source: sourceIsAFile, Output: t.TempDir()},
			want:   "is a file, not a directory",
		},
		{
			// The claude-code target has no verbatim dirs, so only this check
			// can detect a wrong Source; without it the run emits agents and
			// no skill, with no error.
			name:   "claude-code source has no skills directory",
			target: ClaudeCodeTarget,
			roots:  HarnessRoots{Source: sourceWithoutSkills, Output: t.TempDir()},
			want:   "cannot read the hand-authored skill directory",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			files, err := EmitHarness(testCase.roots, testCase.target, GenerateOptions{Diff: false, Write: true})
			if err == nil {
				t.Fatalf("EmitHarness with roots %+v returned nil error, want a rejected-roots error", testCase.roots)
			}
			if files != nil {
				t.Fatalf("EmitHarness with roots %+v returned %d files, want none", testCase.roots, len(files))
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("EmitHarness error %q does not contain %q", err.Error(), testCase.want)
			}
		})
	}
}

// TestEmitHarnessClaudeCodeSplitRootMatchesInPlace proves the marker-merge
// target reads its hand-authored base files from roots.Source. A split-root
// emission must produce the same file set and the same bytes as an in-place
// emission, and must leave the source tree untouched.
func TestEmitHarnessClaudeCodeSplitRootMatchesInPlace(t *testing.T) {
	t.Parallel()

	sourceRoot := testModuleRoot(t)
	outputRoot := t.TempDir()

	beforeSource := snapshotTrees(t, sourceRoot, sourceGuardDirs)

	inPlace, err := EmitHarness(RepoRoots(sourceRoot), ClaudeCodeTarget, GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("EmitHarness(%s) in place: %v", HarnessClaudeCode, err)
	}
	split, err := EmitHarness(HarnessRoots{Source: sourceRoot, Output: outputRoot}, ClaudeCodeTarget, GenerateOptions{Diff: false, Write: true})
	if err != nil {
		t.Fatalf("EmitHarness(%s) with a separate output root: %v", HarnessClaudeCode, err)
	}

	// The source tree is an input only. A write below it is a defect.
	assertTreesUnchanged(t, sourceRoot, sourceGuardDirs, beforeSource)

	wantFiles := relativeContents(t, sourceRoot, inPlace)
	gotFiles := relativeContents(t, outputRoot, split)
	if len(gotFiles) != len(wantFiles) {
		t.Fatalf("split-root emission produced %d files, want %d (the in-place count)", len(gotFiles), len(wantFiles))
	}
	if countSkillFiles(wantFiles) == 0 {
		t.Fatal("in-place emission produced no SKILL.md; the test cannot prove the marker-merge path")
	}
	for rel, want := range wantFiles {
		got, ok := gotFiles[rel]
		if !ok {
			t.Fatalf("split-root emission did not produce %q", rel)
		}
		if got != want {
			t.Fatalf("split-root emission of %q differs from the in-place bytes", rel)
		}
		onDisk, err := os.ReadFile(filepath.Join(outputRoot, rel))
		if err != nil {
			t.Fatalf("read split-root output %q: %v", rel, err)
		}
		if string(onDisk) != want {
			t.Fatalf("split-root file %q on disk differs from the in-place bytes", rel)
		}
	}
}

// hashTree hashes every file below dir, keyed by the path relative to dir.
func hashTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	}); err != nil {
		t.Fatalf("hash tree %q: %v", dir, err)
	}
	if len(out) == 0 {
		t.Fatalf("tree %q holds no file; a comparison against it would pass for any behaviour", dir)
	}
	return out
}

func countSkillFiles(files map[string]string) int {
	count := 0
	for rel := range files {
		if filepath.Base(rel) == "SKILL.md" {
			count++
		}
	}
	return count
}

// relativeContents keys the emitted content by the path relative to root, and
// fails when a file escapes root.
func relativeContents(t *testing.T, root string, files []GeneratedFile) map[string]string {
	t.Helper()
	out := make(map[string]string, len(files))
	for _, file := range files {
		rel, err := filepath.Rel(root, file.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("emitted %q is outside the root %q", file.Path, root)
		}
		rel = filepath.ToSlash(rel)
		if _, duplicate := out[rel]; duplicate {
			t.Fatalf("emitted %q twice", rel)
		}
		out[rel] = file.Content
	}
	return out
}

// snapshotTrees records the content hash AND the modification time of every
// file below each named subdirectory of root. The modification time matters:
// generation is idempotent, so a stray write into the source tree can leave the
// bytes equal, and only the timestamp then shows the file was rewritten.
func snapshotTrees(t *testing.T, root string, subDirs []string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, subDir := range subDirs {
		base := filepath.Join(root, subDir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		for rel, hash := range hashTree(t, base) {
			info, err := os.Stat(filepath.Join(base, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatalf("stat source file %q: %v", rel, err)
			}
			out[filepath.ToSlash(filepath.Join(subDir, rel))] = fmt.Sprintf("%s@%d", hash, info.ModTime().UnixNano())
		}
	}
	if len(out) == 0 {
		t.Fatalf("snapshot of %q under %q is empty; the guard would pass for any behaviour", subDirs, root)
	}
	return out
}

func assertTreesUnchanged(t *testing.T, root string, subDirs []string, before map[string]string) {
	t.Helper()
	after := snapshotTrees(t, root, subDirs)
	for rel, want := range before {
		got, ok := after[rel]
		if !ok {
			t.Fatalf("source file %q was deleted; nothing may be written below roots.Source", rel)
		}
		if got != want {
			t.Fatalf("source file %q changed; nothing may be written below roots.Source", rel)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			t.Fatalf("source file %q was created; nothing may be written below roots.Source", rel)
		}
	}
}
