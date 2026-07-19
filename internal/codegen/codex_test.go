package codegen

import (
	"io"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// emitCodexIntoSeededRoot seeds a temp module root with the verbatim source
// trees the Codex skills package copies, then emits the full Codex target into
// it (Write=false, so no disk mutation) and returns the root and files.
func emitCodexIntoSeededRoot(t *testing.T) (string, []GeneratedFile) {
	t.Helper()
	root := t.TempDir()
	seedVerbatimSourceDirs(t, root)
	figuresDir := filepath.Join(testModuleRoot(t), "skills", "protocol", "figures")
	files, err := EmitHarness(root, CodexTarget, figuresDir, GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("EmitHarness(codex): %v", err)
	}
	if len(files) == 0 {
		t.Fatal("EmitHarness(codex) returned no files")
	}
	return root, files
}

// TestCodexTargetIsResolvable proves the Codex target is registered in the
// harness registry and selectable by name through the production ResolveHarness
// entry point.
func TestCodexTargetIsResolvable(t *testing.T) {
	t.Parallel()

	targets, err := ResolveHarness([]string{string(HarnessCodex)})
	if err != nil {
		t.Fatalf("ResolveHarness(codex): %v", err)
	}
	if len(targets) != 1 || targets[0].Name != HarnessCodex {
		t.Fatalf("ResolveHarness(codex) = %+v, want the codex target", targets)
	}
	if targets[0].Agents == nil || targets[0].Manifest == nil {
		t.Fatal("codex target is missing its agent or manifest emitter")
	}
	// The unknown-target error must now enumerate codex among registered targets.
	_, err = ResolveHarness([]string{"not-a-harness"})
	if err == nil || !strings.Contains(err.Error(), string(HarnessCodex)) {
		t.Fatalf("unknown-target error must list codex; got %v", err)
	}
}

// TestEmitHarnessCodexIsDeterministic is the double-generate diff proof: two
// emissions from identical input are byte-identical, file-for-file.
func TestEmitHarnessCodexIsDeterministic(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	seedVerbatimSourceDirs(t, root)
	figuresDir := filepath.Join(testModuleRoot(t), "skills", "protocol", "figures")
	opts := GenerateOptions{Diff: false, Write: false}

	first, err := EmitHarness(root, CodexTarget, figuresDir, opts)
	if err != nil {
		t.Fatalf("first EmitHarness(codex): %v", err)
	}
	second, err := EmitHarness(root, CodexTarget, figuresDir, opts)
	if err != nil {
		t.Fatalf("second EmitHarness(codex): %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("emission count changed between runs: %d then %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Path != second[i].Path {
			t.Fatalf("emission %d path changed: %q then %q", i, first[i].Path, second[i].Path)
		}
		if first[i].Content != second[i].Content {
			t.Fatalf("emission %q content is not byte-stable across runs", first[i].Path)
		}
	}
}

// TestCodexTargetDescriptorPartitionsPackages proves the descriptor splits a
// full emission into the three closed packages plus the target manifest, each a
// valid content-addressed bundle, and exposes the pinned contract identity.
func TestCodexTargetDescriptorPartitionsPackages(t *testing.T) {
	t.Parallel()

	root, files := emitCodexIntoSeededRoot(t)
	desc, err := NewCodexTargetDescriptor(root, files)
	if err != nil {
		t.Fatalf("NewCodexTargetDescriptor: %v", err)
	}

	if desc.Harness() != ir.HarnessCodex {
		t.Fatalf("descriptor harness = %q, want %q", desc.Harness(), ir.HarnessCodex)
	}
	if desc.RuntimeContractID().String() != CodexRuntimeContractID().String() {
		t.Fatalf("descriptor contract = %q, want %q", desc.RuntimeContractID(), CodexRuntimeContractID())
	}
	if len(desc.Packages()) != 3 {
		t.Fatalf("descriptor has %d packages, want 3", len(desc.Packages()))
	}

	skills, ok := desc.Package(codexSkillsComponent)
	if !ok || skills.Bundle().Manifest().Len() == 0 {
		t.Fatal("skills package is missing or empty")
	}
	agents, ok := desc.Package(codexAgentsComponent)
	if !ok {
		t.Fatal("agents package is missing")
	}
	if got := codexRegularFileCount(agents); got != countToolRoles() {
		t.Fatalf("agents package has %d profile files, want one per tool role (%d)", got, countToolRoles())
	}
	hooks, ok := desc.Package(codexHooksComponent)
	if !ok {
		t.Fatal("hooks package is missing")
	}
	hookFiles := codexRegularFilePaths(hooks)
	if len(hookFiles) != 1 || hookFiles[0] != ".codex/hooks/README.md" {
		t.Fatalf("hooks package files = %v, want only the default-off README", hookFiles)
	}

	// The target manifest bundle carries exactly the plugin manifest.
	mf := desc.ManifestBundle()
	var manifestPaths []string
	for _, e := range mf.Manifest().Entries() {
		if e.IsRegular() {
			manifestPaths = append(manifestPaths, e.Path().String())
		}
	}
	if len(manifestPaths) != 1 || manifestPaths[0] != codexTargetManifestPath {
		t.Fatalf("manifest bundle files = %v, want only %q", manifestPaths, codexTargetManifestPath)
	}
}

// TestCodexBundleManifestsAreCanonical proves every bundle manifest sorts paths
// lexicographically and freezes each entry's type, octal mode, and (for files)
// a sha256:<64 hex> digest.
func TestCodexBundleManifestsAreCanonical(t *testing.T) {
	t.Parallel()

	root, files := emitCodexIntoSeededRoot(t)
	desc, err := NewCodexTargetDescriptor(root, files)
	if err != nil {
		t.Fatalf("NewCodexTargetDescriptor: %v", err)
	}

	for _, pkg := range desc.Packages() {
		entries := pkg.Bundle().Manifest().Entries()
		prev := ""
		for _, e := range entries {
			p := e.Path().String()
			if prev != "" && p < prev {
				t.Fatalf("package %q manifest is not lexicographically sorted: %q after %q", pkg.ID(), p, prev)
			}
			prev = p
			if e.IsRegular() {
				if e.Mode().String() != "0644" {
					t.Fatalf("file %q mode = %q, want 0644", p, e.Mode().String())
				}
				d := e.Digest().String()
				if !strings.HasPrefix(d, "sha256:") || len(strings.TrimPrefix(d, "sha256:")) != 64 {
					t.Fatalf("file %q digest %q is not sha256:<64 hex>", p, d)
				}
			} else if e.Mode().String() != "0755" {
				t.Fatalf("dir %q mode = %q, want 0755", p, e.Mode().String())
			}
		}
	}
}

// TestCodexDescriptorIsContentAddressedDeterministic proves the descriptor is a
// pure content-addressed projection: two independent emissions yield identical
// bundle content addresses per package.
func TestCodexDescriptorIsContentAddressedDeterministic(t *testing.T) {
	t.Parallel()

	rootA, filesA := emitCodexIntoSeededRoot(t)
	descA, err := NewCodexTargetDescriptor(rootA, filesA)
	if err != nil {
		t.Fatalf("descriptor A: %v", err)
	}
	rootB, filesB := emitCodexIntoSeededRoot(t)
	descB, err := NewCodexTargetDescriptor(rootB, filesB)
	if err != nil {
		t.Fatalf("descriptor B: %v", err)
	}
	for _, component := range []CodexComponentID{codexSkillsComponent, codexAgentsComponent, codexHooksComponent} {
		a, _ := descA.Package(component)
		b, _ := descB.Package(component)
		if a.Bundle().ID() != b.Bundle().ID() {
			t.Fatalf("package %q bundle id differs across emissions: %s vs %s", component, a.Bundle().ID(), b.Bundle().ID())
		}
	}
	if descA.ManifestBundle().ID() != descB.ManifestBundle().ID() {
		t.Fatal("manifest bundle id differs across emissions")
	}
}

// TestValidateCodexTargetAcceptsGeneratedTarget proves the real generated
// target passes every validation gate.
func TestValidateCodexTargetAcceptsGeneratedTarget(t *testing.T) {
	t.Parallel()

	root, files := emitCodexIntoSeededRoot(t)
	desc, err := NewCodexTargetDescriptor(root, files)
	if err != nil {
		t.Fatalf("NewCodexTargetDescriptor: %v", err)
	}
	if err := ValidateCodexTarget(desc); err != nil {
		t.Fatalf("ValidateCodexTarget rejected the real generated target: %v", err)
	}
}

// TestValidateCodexTargetRejectsFabricatedNativeCall proves validation fails
// when an agent profile names a native function the pinned contract does not
// classify as native (the core contract-fidelity guard).
func TestValidateCodexTargetRejectsFabricatedNativeCall(t *testing.T) {
	t.Parallel()

	root, files := emitCodexIntoSeededRoot(t)
	// Corrupt one agent profile's functions line to name a fabricated call.
	for i, f := range files {
		if strings.HasSuffix(f.Path, ".toml") && strings.Contains(f.Content, "functions =") {
			files[i].Content = strings.Replace(
				f.Content,
				`functions = ["request-input"]`,
				`functions = ["request-input", "task"]`,
				1,
			)
			break
		}
	}
	desc, err := NewCodexTargetDescriptor(root, files)
	if err != nil {
		t.Fatalf("NewCodexTargetDescriptor: %v", err)
	}
	err = ValidateCodexTarget(desc)
	if err == nil {
		t.Fatal("ValidateCodexTarget accepted a fabricated native call")
	}
	for _, want := range []string{"task", "native"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection error %q does not mention %q", err, want)
		}
	}
}

// TestValidateCodexTargetRejectsContractDrift proves validation fails when the
// descriptor's packaged contract identity does not match the pinned lowering
// contract.
func TestValidateCodexTargetRejectsContractDrift(t *testing.T) {
	t.Parallel()

	root, files := emitCodexIntoSeededRoot(t)
	desc, err := NewCodexTargetDescriptor(root, files)
	if err != nil {
		t.Fatalf("NewCodexTargetDescriptor: %v", err)
	}
	drifted, err := ir.NewRuntimeContractID(ir.HarnessCodex, "codex@9.9.9")
	if err != nil {
		t.Fatalf("build drift contract id: %v", err)
	}
	desc.contract = drifted
	if err := ValidateCodexTarget(desc); err == nil {
		t.Fatal("ValidateCodexTarget accepted a drifted contract identity")
	}
}

// TestNewCodexTargetDescriptorRejectsForeignPath proves a file under the root
// but outside every package root (and not the target manifest) is rejected, so
// stray output can never silently escape package partitioning.
func TestNewCodexTargetDescriptorRejectsForeignPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	files := []GeneratedFile{{
		Path:    filepath.Join(root, ".codex", "stray", "x.md"),
		Content: "stray",
	}}
	_, err := NewCodexTargetDescriptor(root, files)
	if err == nil {
		t.Fatal("NewCodexTargetDescriptor accepted a file outside every package root")
	}
	if !strings.Contains(err.Error(), "no Codex package root") {
		t.Fatalf("error %q does not explain the package-root violation", err)
	}
}

// TestNewCodexTargetDescriptorRejectsFileOutsideRoot proves a file whose path is
// not under the emit root is rejected with an actionable error.
func TestNewCodexTargetDescriptorRejectsFileOutsideRoot(t *testing.T) {
	t.Parallel()

	_, err := NewCodexTargetDescriptor("/emit/root", []GeneratedFile{{
		Path:    "/somewhere/else/.codex/skills/x/SKILL.md",
		Content: "x",
	}})
	if err == nil {
		t.Fatal("NewCodexTargetDescriptor accepted a file outside the module root")
	}
	if !strings.Contains(err.Error(), "not under the module root") {
		t.Fatalf("error %q does not explain the root violation", err)
	}
}

// TestCodexPackagesLoadInIsolation is the isolated-load oracle: for each package
// independently (siblings absent — only that package's bundle in hand), every
// declared file opens and reads, the package's regular-file tree matches a
// stable, reproducible oracle, and the package contains no file belonging to a
// sibling package root.
func TestCodexPackagesLoadInIsolation(t *testing.T) {
	t.Parallel()

	rootA, filesA := emitCodexIntoSeededRoot(t)
	descA, err := NewCodexTargetDescriptor(rootA, filesA)
	if err != nil {
		t.Fatalf("descriptor A: %v", err)
	}
	rootB, filesB := emitCodexIntoSeededRoot(t)
	descB, err := NewCodexTargetDescriptor(rootB, filesB)
	if err != nil {
		t.Fatalf("descriptor B: %v", err)
	}

	for _, component := range codexComponents {
		pkgA, _ := descA.Package(component.id)
		pkgB, _ := descB.Package(component.id)

		treeA := codexRegularFilePaths(pkgA)
		treeB := codexRegularFilePaths(pkgB)
		if strings.Join(treeA, "\n") != strings.Join(treeB, "\n") {
			t.Fatalf("package %q tree oracle is not reproducible:\nA=%v\nB=%v", component.id, treeA, treeB)
		}

		// Every declared file must open and read from the package bundle alone
		// (siblings absent), proving the package is self-contained/loadable.
		for _, rel := range treeA {
			f, err := pkgA.Bundle().Open(rel)
			if err != nil {
				t.Fatalf("package %q cannot open its own file %q in isolation: %v", component.id, rel, err)
			}
			if _, err := io.ReadAll(f); err != nil {
				t.Fatalf("package %q cannot read its own file %q: %v", component.id, rel, err)
			}
			_ = f.Close()

			// No file may belong to a sibling package root.
			owner, ok := codexComponentForPath(rel)
			if !ok || owner != component.id {
				t.Fatalf("package %q contains file %q owned by a sibling root", component.id, rel)
			}
		}
	}
}

// codexRegularFilePaths returns the sorted package-relative paths of a package's
// regular-file entries (its stable tree oracle).
func codexRegularFilePaths(pkg CodexPackage) []string {
	var out []string
	for _, e := range pkg.Bundle().Manifest().Entries() {
		if e.IsRegular() {
			out = append(out, e.Path().String())
		}
	}
	sort.Strings(out)
	return out
}

func codexRegularFileCount(pkg CodexPackage) int {
	return len(codexRegularFilePaths(pkg))
}
