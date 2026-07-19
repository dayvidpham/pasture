// Package codegen — Codex native target (harness registration, target
// descriptor, packaged artifact bundles, and target validation).
//
// The Codex target projects Pasture's canonical protocol source into three
// independently selectable Codex plugin packages under `.codex/`:
//
//   - skills  (`.codex/skills/<dir>/SKILL.md`): protocol skills rendered as
//     semantic-instruction markdown. Codex 0.144.1 has no native skill
//     invocation, so these carry the reviewed protocol steps the agent performs
//     directly (matching the pinned contract's semantic lowering of
//     InvokeSkill), plus the hand-authored `protocol`/`install-cli` support
//     trees copied verbatim.
//   - agents  (`.codex/agents/<role>.toml`): one standalone Codex agent profile
//     per tool-bearing role (see codex_agent.go).
//   - hooks   (`.codex/hooks/`): a default-off, intentionally inert package —
//     Codex 0.144.1 exposes no hook runtime (see codex_manifest.go).
//
// The target flows through the existing codegen.Generate pipeline (it is a
// TargetHarness registered in harnessRegistry), so its output is produced
// behind the same strict source-migration gate as every other target and its
// skills participate in the same registry/global-ID checks.
//
// On top of file emission this file publishes a CodexTargetDescriptor: the
// stable per-package component identities, the pinned RuntimeContractID, and an
// immutable, content-addressed artifact.Bundle per package (built from #48's
// neutral bundle model) whose manifest freezes each entry's canonical relative
// path, type, octal mode, and sha256 content digest. A packaged CLI can carry
// these bundles outside the source checkout; the descriptor deliberately does
// not carry any downstream activation/install-state contract (owned elsewhere).
package codegen

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// HarnessCodex is the registry name of the Codex native target. It matches the
// canonical ir.HarnessCodex harness identity the pinned runtime contract binds.
const HarnessCodex HarnessName = "codex"

// codexSkillRoot is the package-relative root for the Codex skills package. The
// harness emits role and command skills to <root>/<codexSkillRoot>/<dir>/SKILL.md
// and copies the verbatim support trees into the same package.
const codexSkillRoot = ".codex/skills"

// codexAgentsRoot and codexHooksRoot are the package roots for the standalone
// agent TOML package and the (default-off) hooks package.
const (
	codexAgentsRoot = ".codex/agents"
	codexHooksRoot  = ".codex/hooks"
)

// codexVerbatimDirs names the hand-authored skill directories copied verbatim
// (recursively) into the Codex skills package so intra-skill links resolve —
// the same two shared trees the OpenCode target copies. "protocol" carries the
// shared documentation and figures the generated per-role skills link to;
// "install-cli" is the hand-authored Pasture installer skill.
var codexVerbatimDirs = []string{
	"protocol",
	"install-cli",
}

// CodexTarget is the Codex native TargetHarness. It is registered in
// harnessRegistry (see harness.go) so codegen.ResolveHarness("codex") and
// codegen.Generate route Codex emission through the shared pipeline.
var CodexTarget = TargetHarness{
	Name:             HarnessCodex,
	SkillRoot:        filepath.FromSlash(codexSkillRoot),
	SkillTemplate:    "templates/codex_skill.go.tmpl",
	SubSkillTemplate: "templates/codex_skill_sub.go.tmpl",
	SkillWrite:       WriteFullFile,
	Agents:           codexAgentEmitter{},
	Manifest:         codexManifestEmitter{},
	Verbatim:         codexVerbatimDirs,
}

// ─── Pinned runtime contract accessors ──────────────────────────────────────

// CodexRuntimeContract returns the pinned Codex 0.144.1 runtime contract the
// target lowers against. It is the single source of the target's native
// vocabulary and RuntimeContractID.
func CodexRuntimeContract() runtime.RuntimeContract { return runtime.Codex0_144_1() }

// CodexRuntimeContractID returns the pinned Codex contract identity the target
// publishes in its descriptor, manifest, and agent profiles.
func CodexRuntimeContractID() ir.RuntimeContractID { return CodexRuntimeContract().ID() }

// codexNativeFunctions returns the sorted, de-duplicated native Codex function
// (call) names the pinned contract classifies across the closed core operation
// vocabulary. For Codex 0.144.1 this is exactly ["request-input"]. Deriving the
// list from the contract — never a literal — guarantees the target can never
// emit or accept a fabricated native call: if the pinned contract stops
// classifying an operation as native, this set changes with it.
func codexNativeFunctions() []string {
	contract := CodexRuntimeContract()
	seen := make(map[string]struct{})
	for _, kind := range ir.AllOperationKinds() {
		descriptor, ok := runtime.CoreOperationDescriptorFor(kind)
		if !ok {
			continue
		}
		binding, err := runtime.LookupOperationBinding(contract, descriptor)
		if err != nil {
			// Unsupported operations return an actionable lookup error and are,
			// by definition, not native; mediated/semantic bindings resolve but
			// report Native()==false below. Either way they contribute no native
			// call name.
			continue
		}
		if call, isNative := binding.Native(); isNative {
			seen[call.CallName()] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ─── Component identities ────────────────────────────────────────────────────

// CodexComponentID is the stable marketplace identity of one Codex plugin
// package. It is a typed value, not a bare string, so a package identity can
// never be confused with an arbitrary label.
type CodexComponentID struct{ value string }

// String returns the component identity spelling.
func (c CodexComponentID) String() string { return c.value }

// The three Codex package component identities. They are stable, sibling-free
// identities: a consumer may select any one with the other two absent.
var (
	codexSkillsComponent = CodexComponentID{value: "pasture-codex-skills"}
	codexAgentsComponent = CodexComponentID{value: "pasture-codex-agents"}
	codexHooksComponent  = CodexComponentID{value: "pasture-codex-hooks"}
)

// codexComponent binds one package's component identity to the package-relative
// root every one of its files must live under.
type codexComponent struct {
	id   CodexComponentID
	root string
}

// codexComponents is the ordered, closed set of Codex packages. Order is the
// stable descriptor iteration order (skills, agents, hooks).
var codexComponents = []codexComponent{
	{id: codexSkillsComponent, root: codexSkillRoot},
	{id: codexAgentsComponent, root: codexAgentsRoot},
	{id: codexHooksComponent, root: codexHooksRoot},
}

// ─── Target descriptor ───────────────────────────────────────────────────────

// CodexPackage is one independently selectable Codex plugin package: its stable
// component identity and its immutable, content-addressed artifact bundle. The
// bundle is the only file-bearing surface a packaged CLI needs; it owns no
// handle into the source checkout.
type CodexPackage struct {
	id     CodexComponentID
	bundle artifact.Bundle
}

// ID returns the package's stable component identity.
func (p CodexPackage) ID() CodexComponentID { return p.id }

// Bundle returns the package's immutable artifact bundle.
func (p CodexPackage) Bundle() artifact.Bundle { return p.bundle }

// CodexTargetDescriptor is the packaged, source-checkout-independent projection
// of the Codex target: the pinned RuntimeContractID plus one CodexPackage per
// plugin package. It deliberately carries no downstream activation or
// install-state contract.
type CodexTargetDescriptor struct {
	contract ir.RuntimeContractID
	packages []CodexPackage
	manifest artifact.Bundle
}

// Harness returns the canonical harness identity this descriptor targets.
func (d CodexTargetDescriptor) Harness() ir.HarnessID { return ir.HarnessCodex }

// RuntimeContractID returns the pinned Codex runtime contract identity the
// packages were generated against.
func (d CodexTargetDescriptor) RuntimeContractID() ir.RuntimeContractID { return d.contract }

// ManifestBundle returns the immutable target-level manifest bundle (the
// `.codex/codex.toml` plugin manifest that declares the three packages). It is
// distinct from the per-package bundles because it describes the packages
// rather than belonging to any one of them.
func (d CodexTargetDescriptor) ManifestBundle() artifact.Bundle { return d.manifest }

// Packages returns the descriptor's packages in stable order (skills, agents,
// hooks).
func (d CodexTargetDescriptor) Packages() []CodexPackage {
	return append([]CodexPackage(nil), d.packages...)
}

// Package returns the package with the given component identity.
func (d CodexTargetDescriptor) Package(id CodexComponentID) (CodexPackage, bool) {
	for _, pkg := range d.packages {
		if pkg.id == id {
			return pkg, true
		}
	}
	return CodexPackage{}, false
}

// NewCodexTargetDescriptor partitions a completed set of Codex GeneratedFiles
// (as produced by EmitHarness(root, CodexTarget, ...)) into the three plugin
// packages and freezes each into an immutable artifact.Bundle keyed by a
// canonical manifest.
//
// root is the module root the files were emitted under; it is stripped so every
// bundle entry is a clean package-relative path. Partitioning is strict: every
// file must live under exactly one package root, and no file may leak into a
// sibling package (that would break independent selectability). The resulting
// descriptor is deterministic — the same files always yield byte-identical
// bundle manifests and content addresses — because artifact.NewManifest sorts
// entries lexicographically and the bundle identity is content-derived.
func NewCodexTargetDescriptor(root string, files []GeneratedFile) (CodexTargetDescriptor, error) {
	rootPrefix := ""
	if cleaned := filepath.Clean(root); cleaned != "." && cleaned != "" {
		rootPrefix = filepath.ToSlash(cleaned) + "/"
	}

	// Bucket relative paths + content per component, plus the target-level
	// manifest files (directly under .codex/, owned by no package).
	byComponent := make(map[CodexComponentID]map[string][]byte, len(codexComponents))
	for _, component := range codexComponents {
		byComponent[component.id] = make(map[string][]byte)
	}
	manifestFiles := make(map[string][]byte)

	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		rel := filepath.ToSlash(file.Path)
		if rootPrefix != "" {
			if !strings.HasPrefix(rel, rootPrefix) {
				return CodexTargetDescriptor{}, fmt.Errorf(
					"codegen.NewCodexTargetDescriptor: generated file %q is not under the module root %q — "+
						"where: partitioning Codex output into packages — "+
						"why: every Codex file must be a package-relative path under the emit root — "+
						"fix: emit the Codex target with EmitHarness(root, CodexTarget, ...) using the same root passed here",
					file.Path, root,
				)
			}
			rel = strings.TrimPrefix(rel, rootPrefix)
		}
		if _, duplicate := seen[rel]; duplicate {
			return CodexTargetDescriptor{}, fmt.Errorf(
				"codegen.NewCodexTargetDescriptor: generated file %q was produced twice — "+
					"where: partitioning Codex output into packages — "+
					"fix: emit each Codex file exactly once",
				rel,
			)
		}
		seen[rel] = struct{}{}

		if component, ok := codexComponentForPath(rel); ok {
			byComponent[component][rel] = []byte(file.Content)
			continue
		}
		if isCodexTargetManifestPath(rel) {
			manifestFiles[rel] = []byte(file.Content)
			continue
		}
		return CodexTargetDescriptor{}, fmt.Errorf(
			"codegen.NewCodexTargetDescriptor: generated file %q belongs to no Codex package root (%s) and is not a target manifest file — "+
				"where: partitioning Codex output into packages — "+
				"why: every Codex file must live under exactly one package root, or be the target manifest, so packages stay independently selectable — "+
				"fix: emit the file under %s, %s, or %s, or as %s",
			rel, strings.Join(codexComponentRoots(), ", "),
			codexSkillRoot, codexAgentsRoot, codexHooksRoot, codexTargetManifestPath,
		)
	}

	packages := make([]CodexPackage, 0, len(codexComponents))
	for _, component := range codexComponents {
		bundle, err := buildCodexBundle(byComponent[component.id])
		if err != nil {
			return CodexTargetDescriptor{}, fmt.Errorf(
				"codegen.NewCodexTargetDescriptor: build bundle for Codex package %q failed: %w",
				component.id, err,
			)
		}
		packages = append(packages, CodexPackage{id: component.id, bundle: bundle})
	}

	manifestBundle, err := buildCodexBundle(manifestFiles)
	if err != nil {
		return CodexTargetDescriptor{}, fmt.Errorf(
			"codegen.NewCodexTargetDescriptor: build target manifest bundle failed: %w", err)
	}

	return CodexTargetDescriptor{
		contract: CodexRuntimeContractID(),
		packages: packages,
		manifest: manifestBundle,
	}, nil
}

// codexTargetManifestPath is the single target-level manifest file the Codex
// target emits directly under .codex/ (owned by no package).
const codexTargetManifestPath = ".codex/codex.toml"

// isCodexTargetManifestPath reports whether rel is a recognized target-level
// manifest file: a file directly under .codex/ that is not under a package root.
func isCodexTargetManifestPath(rel string) bool {
	return rel == codexTargetManifestPath
}

// codexComponentForPath returns the component whose root owns rel, matching the
// longest (most specific) package root first so `.codex/skills` is never
// mis-assigned to a broader sibling.
func codexComponentForPath(rel string) (CodexComponentID, bool) {
	best := CodexComponentID{}
	bestLen := -1
	for _, component := range codexComponents {
		root := component.root
		if rel == root || strings.HasPrefix(rel, root+"/") {
			if len(root) > bestLen {
				best = component.id
				bestLen = len(root)
			}
		}
	}
	if bestLen < 0 {
		return CodexComponentID{}, false
	}
	return best, true
}

func codexComponentRoots() []string {
	roots := make([]string, 0, len(codexComponents))
	for _, component := range codexComponents {
		roots = append(roots, component.root)
	}
	return roots
}

// buildCodexBundle freezes one package's package-relative files into an
// immutable artifact.Bundle. Every regular file entry carries mode 0644 and its
// exact-byte sha256 digest; the parent directory chain of each file is declared
// (mode 0755) so the bundle manifest is a complete ownership inventory. An
// empty package (for example the default-off hooks package when it ships no
// files) yields a valid empty bundle.
func buildCodexBundle(files map[string][]byte) (artifact.Bundle, error) {
	fileMode, err := artifact.NewMode(0o644)
	if err != nil {
		return artifact.Bundle{}, err
	}
	dirMode, err := artifact.NewMode(0o755)
	if err != nil {
		return artifact.Bundle{}, err
	}

	source := make(fstest.MapFS, len(files))
	entries := make([]artifact.Entry, 0, len(files))
	declaredDirs := make(map[string]struct{})

	relPaths := make([]string, 0, len(files))
	for rel := range files {
		relPaths = append(relPaths, rel)
	}
	sort.Strings(relPaths)

	for _, rel := range relPaths {
		content := files[rel]
		entryPath, err := artifact.NewPath(rel)
		if err != nil {
			return artifact.Bundle{}, fmt.Errorf("codex bundle entry path %q: %w", rel, err)
		}
		fileEntry, err := artifact.NewFileEntry(entryPath, fileMode, artifact.DigestBytes(content))
		if err != nil {
			return artifact.Bundle{}, fmt.Errorf("codex bundle file entry %q: %w", rel, err)
		}
		entries = append(entries, fileEntry)
		// fstest.MapFS synthesizes the parent directories of this file, so only
		// the regular file itself is inserted as source; the directory entries
		// below are declared in the manifest for a complete ownership inventory.
		source[rel] = &fstest.MapFile{Data: content, Mode: 0o644}

		// Declare each ancestor directory exactly once.
		for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if _, done := declaredDirs[dir]; done {
				continue
			}
			declaredDirs[dir] = struct{}{}
			dirPath, err := artifact.NewPath(dir)
			if err != nil {
				return artifact.Bundle{}, fmt.Errorf("codex bundle dir path %q: %w", dir, err)
			}
			dirEntry, err := artifact.NewDirectoryEntry(dirPath, dirMode)
			if err != nil {
				return artifact.Bundle{}, fmt.Errorf("codex bundle dir entry %q: %w", dir, err)
			}
			entries = append(entries, dirEntry)
		}
	}

	manifest, err := artifact.NewManifest(entries...)
	if err != nil {
		return artifact.Bundle{}, fmt.Errorf("codex bundle manifest: %w", err)
	}
	return artifact.NewBundle(source, manifest)
}

// ─── Target validation ───────────────────────────────────────────────────────

// ValidateCodexTarget verifies that a Codex target descriptor is faithful to
// the pinned runtime contract and honors package independence. It is the
// production check that stands between "the files were emitted" and "the target
// is releasable":
//
//  1. Contract identity: the descriptor's RuntimeContractID must equal the
//     pinned Codex contract identity (no drift between packaging and lowering).
//  2. Package completeness: exactly the three closed packages must be present.
//  3. Native-call fidelity: every native Codex function named by any emitted
//     agent profile must be a call the pinned contract classifies as native.
//     Codex 0.144.1 classifies only `request-input`, so an emitted profile that
//     named a fabricated `task`/`Skill`/`Agent` function would be rejected here.
//  4. Package independence: no package's bundle may contain a path owned by a
//     sibling package root (sibling files would break isolated selection).
func ValidateCodexTarget(descriptor CodexTargetDescriptor) error {
	if descriptor.contract.String() != CodexRuntimeContractID().String() {
		return fmt.Errorf(
			"codegen.ValidateCodexTarget: descriptor runtime contract %q does not match the pinned Codex contract %q — "+
				"where: validating a Codex target descriptor — "+
				"why: the packaged identity and the lowering contract must agree exactly — "+
				"fix: rebuild the descriptor from a Codex target generated against the pinned contract",
			descriptor.contract, CodexRuntimeContractID(),
		)
	}

	if len(descriptor.packages) != len(codexComponents) {
		return fmt.Errorf(
			"codegen.ValidateCodexTarget: descriptor has %d packages, want the %d closed Codex packages — "+
				"fix: build the descriptor with NewCodexTargetDescriptor over a complete Codex emission",
			len(descriptor.packages), len(codexComponents),
		)
	}
	for _, component := range codexComponents {
		if _, ok := descriptor.Package(component.id); !ok {
			return fmt.Errorf(
				"codegen.ValidateCodexTarget: descriptor is missing the %q package — "+
					"fix: build the descriptor over a complete Codex emission",
				component.id,
			)
		}
	}

	allowed := make(map[string]struct{})
	for _, call := range codexNativeFunctions() {
		allowed[call] = struct{}{}
	}

	agents, ok := descriptor.Package(codexAgentsComponent)
	if !ok {
		return fmt.Errorf("codegen.ValidateCodexTarget: descriptor is missing the %q package", codexAgentsComponent)
	}
	for _, entry := range agents.bundle.Manifest().Entries() {
		if !entry.IsRegular() {
			continue
		}
		rel := entry.Path().String()
		if !strings.HasSuffix(rel, ".toml") {
			continue
		}
		content, err := readCodexBundleFile(agents.bundle, rel)
		if err != nil {
			return fmt.Errorf("codegen.ValidateCodexTarget: read agent profile %q: %w", rel, err)
		}
		for _, call := range parseCodexAgentFunctions(content) {
			if _, ok := allowed[call]; !ok {
				return fmt.Errorf(
					"codegen.ValidateCodexTarget: agent profile %q declares native function %q, which the pinned Codex contract %q does not classify as native — "+
						"where: validating Codex native-call fidelity — "+
						"why: a generated agent may name only functions the pinned contract lowers natively — "+
						"fix: remove %q, or classify the underlying operation as native in the pinned contract",
					rel, call, CodexRuntimeContractID(), call,
				)
			}
		}
	}

	// Package independence: no package may carry a sibling package's FILE.
	// Shared ancestor directory entries (for example the common ".codex" root)
	// are structural, not owned content, so only regular files are checked.
	for _, pkg := range descriptor.packages {
		ownRoot := codexRootForComponent(pkg.id)
		for _, entry := range pkg.bundle.Manifest().Entries() {
			if !entry.IsRegular() {
				continue
			}
			rel := entry.Path().String()
			owner, ok := codexComponentForPath(rel)
			if !ok || owner != pkg.id {
				return fmt.Errorf(
					"codegen.ValidateCodexTarget: package %q bundle contains path %q that is not owned by its root %q — "+
						"where: validating Codex package independence — "+
						"why: a package must not carry a sibling package's files or it cannot be selected in isolation — "+
						"fix: emit %q under its owning package root only",
					pkg.id, rel, ownRoot, rel,
				)
			}
		}
	}
	return nil
}

// readCodexBundleFile reads a declared regular-file leaf from a Codex package
// bundle in full. The bundle owns its own byte snapshot, so this reads the
// packaged content, not the source checkout.
func readCodexBundleFile(bundle artifact.Bundle, name string) (string, error) {
	file, err := bundle.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func codexRootForComponent(id CodexComponentID) string {
	for _, component := range codexComponents {
		if component.id == id {
			return component.root
		}
	}
	return ""
}

// parseCodexAgentFunctions extracts the TOML `functions = [...]` array values
// from an emitted agent profile. It reads only the single-line array the agent
// emitter writes (see renderCodexAgent); a profile without a functions line has
// no native declarations and returns nil.
func parseCodexAgentFunctions(content string) []string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "functions") {
			continue
		}
		eq := strings.IndexByte(trimmed, '=')
		if eq < 0 {
			continue
		}
		rhs := strings.TrimSpace(trimmed[eq+1:])
		rhs = strings.TrimPrefix(rhs, "[")
		rhs = strings.TrimSuffix(rhs, "]")
		var calls []string
		for _, part := range strings.Split(rhs, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			calls = append(calls, strings.Trim(part, `"`))
		}
		return calls
	}
	return nil
}
