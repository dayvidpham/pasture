// Package codegen — Antigravity CLI native target (harness registration, target
// descriptor, packaged artifact bundles, and target validation).
//
// The Antigravity CLI target projects Pasture's canonical protocol source into
// the skills package rooted at `.gemini/config/skills/<dir>/SKILL.md` (matching
// Antigravity's global discovery path `~/.gemini/config/skills/<name>/SKILL.md`),
// plus the hand-authored `protocol` and `install-cli` support trees copied
// verbatim.
//
// Agents and lifecycle hooks are explicitly unsupported for Antigravity CLI:
// their descriptor slots carry empty immutable bundles so the canonical
// 4x3 component matrix stays exhaustive while runtime.AntigravityLifecycleContract
// continues to refuse lifecycle hook activation.
package codegen

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// HarnessAntigravityCLI is the registry name of the Antigravity CLI native
// target. It matches the canonical ir.HarnessAntigravityCLI harness identity
// the pinned runtime contract binds.
const HarnessAntigravityCLI HarnessName = "antigravity-cli"

// antigravityCLISkillRoot is the package-relative root for the Antigravity CLI
// skills package. The harness emits role and command skills to
// <root>/<antigravityCLISkillRoot>/<dir>/SKILL.md and copies the verbatim
// support trees into the same package.
const antigravityCLISkillRoot = ".gemini/config/skills"

// antigravityCLIVerbatimDirs names the hand-authored skill directories copied
// verbatim (recursively) into the Antigravity CLI skills package so intra-skill
// links resolve — the same two shared trees the OpenCode and Codex targets copy.
var antigravityCLIVerbatimDirs = []string{
	"protocol",
	"install-cli",
}

// AntigravityCLITarget is the Antigravity CLI native TargetHarness. It is
// registered in harnessRegistry (see harness.go) so
// codegen.ResolveHarness("antigravity-cli") and codegen.Generate route
// Antigravity CLI skill emission through the shared pipeline.
var AntigravityCLITarget = TargetHarness{
	Name:             HarnessAntigravityCLI,
	SkillRoot:        filepath.FromSlash(antigravityCLISkillRoot),
	SkillTemplate:    "templates/antigravity_skill.go.tmpl",
	SubSkillTemplate: "templates/antigravity_skill_sub.go.tmpl",
	SkillWrite:       WriteFullFile,
	Agents:           nil,
	Manifest:         nil,
	Verbatim:         antigravityCLIVerbatimDirs,
}

// ─── Pinned runtime contract accessors ──────────────────────────────────────

// AntigravityCLIRuntimeContract returns the Antigravity CLI runtime contract
// the target lowers against.
func AntigravityCLIRuntimeContract() runtime.RuntimeContract { return runtime.AntigravityCLI1_5_9() }

// AntigravityCLIRuntimeContractID returns the pinned Antigravity CLI contract
// identity the target publishes in its descriptor.
func AntigravityCLIRuntimeContractID() ir.RuntimeContractID {
	return AntigravityCLIRuntimeContract().ID()
}

// antigravityCLINativeFunctions returns the sorted, de-duplicated native
// Antigravity CLI tool/call names the pinned contract classifies across the
// closed core operation vocabulary.
func antigravityCLINativeFunctions() ([]string, error) {
	return runtime.NativeOperationNames(AntigravityCLIRuntimeContract())
}

// ─── Component identities ────────────────────────────────────────────────────

// AntigravityCLIComponentID is the stable package identity of one Antigravity
// CLI package within the canonical installer coordinate matrix.
type AntigravityCLIComponentID struct{ value string }

// String returns the component identity spelling.
func (c AntigravityCLIComponentID) String() string { return c.value }

var (
	antigravityCLISkillsComponent = AntigravityCLIComponentID{value: "pasture-antigravity-cli-skills"}
	antigravityCLIAgentsComponent = AntigravityCLIComponentID{value: "pasture-antigravity-cli-agents"}
	antigravityCLIHooksComponent  = AntigravityCLIComponentID{value: "pasture-antigravity-cli-hooks"}
)

type antigravityCLIComponent struct {
	id   AntigravityCLIComponentID
	root string
}

var antigravityCLIComponents = []antigravityCLIComponent{
	{id: antigravityCLISkillsComponent, root: antigravityCLISkillRoot},
	{id: antigravityCLIAgentsComponent, root: ""},
	{id: antigravityCLIHooksComponent, root: ""},
}

// ─── Target descriptor ───────────────────────────────────────────────────────

// AntigravityCLIPackage pairs one Antigravity CLI component identity with its
// immutable artifact bundle.
type AntigravityCLIPackage struct {
	id     AntigravityCLIComponentID
	bundle artifact.Bundle
}

// ID returns the package's stable component identity.
func (p AntigravityCLIPackage) ID() AntigravityCLIComponentID { return p.id }

// Bundle returns the package's immutable artifact bundle.
func (p AntigravityCLIPackage) Bundle() artifact.Bundle { return p.bundle }

// AntigravityCLITargetDescriptor is the packaged, source-checkout-independent
// projection of the Antigravity CLI target: the pinned RuntimeContractID plus
// the three canonical slots (skills populated; agents and hooks empty).
type AntigravityCLITargetDescriptor struct {
	contract ir.RuntimeContractID
	install  []artifact.ComponentID
	packages []AntigravityCLIPackage
}

// Harness returns the canonical harness identity this descriptor targets.
func (d AntigravityCLITargetDescriptor) Harness() ir.HarnessID { return ir.HarnessAntigravityCLI }

// RuntimeContractID returns the pinned Antigravity CLI runtime contract identity.
func (d AntigravityCLITargetDescriptor) RuntimeContractID() ir.RuntimeContractID { return d.contract }

// InstallationComponents returns the shared installer coordinates.
func (d AntigravityCLITargetDescriptor) InstallationComponents() []artifact.ComponentID {
	return append([]artifact.ComponentID(nil), d.install...)
}

// Packages returns the descriptor's packages in canonical order (skills, agents, hooks).
func (d AntigravityCLITargetDescriptor) Packages() []AntigravityCLIPackage {
	return append([]AntigravityCLIPackage(nil), d.packages...)
}

// Package returns the package with the given component identity.
func (d AntigravityCLITargetDescriptor) Package(id AntigravityCLIComponentID) (AntigravityCLIPackage, bool) {
	for _, pkg := range d.packages {
		if pkg.id == id {
			return pkg, true
		}
	}
	return AntigravityCLIPackage{}, false
}

// NewAntigravityCLITargetDescriptor freezes a completed set of Antigravity CLI
// GeneratedFiles (as produced by EmitHarness with AntigravityCLITarget) into an
// immutable skills bundle alongside empty immutable bundles for unsupported
// agents and hooks slots.
func NewAntigravityCLITargetDescriptor(root string, files []GeneratedFile) (AntigravityCLITargetDescriptor, error) {
	rootPrefix := ""
	if cleaned := filepath.Clean(root); cleaned != "." && cleaned != "" {
		rootPrefix = filepath.ToSlash(cleaned) + "/"
	}

	skillFiles := make(map[string][]byte, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		rel := filepath.ToSlash(file.Path)
		if rootPrefix != "" {
			if !strings.HasPrefix(rel, rootPrefix) {
				return AntigravityCLITargetDescriptor{}, fmt.Errorf(
					"codegen.NewAntigravityCLITargetDescriptor: generated file %q is not under the module root %q — "+
						"where: partitioning Antigravity CLI output into packages — "+
						"why: every Antigravity CLI file must be a package-relative path under the emit root — "+
						"fix: emit the Antigravity CLI target with EmitHarness(HarnessRoots{Source: ..., Output: root}, AntigravityCLITarget, ...) using the same output root passed here",
					file.Path, root,
				)
			}
			rel = strings.TrimPrefix(rel, rootPrefix)
		}
		if _, duplicate := seen[rel]; duplicate {
			return AntigravityCLITargetDescriptor{}, fmt.Errorf(
				"codegen.NewAntigravityCLITargetDescriptor: generated file %q was produced twice — "+
					"where: partitioning Antigravity CLI output into packages — "+
					"fix: emit each Antigravity CLI file exactly once",
				rel,
			)
		}
		seen[rel] = struct{}{}

		if rel != antigravityCLISkillRoot && !strings.HasPrefix(rel, antigravityCLISkillRoot+"/") {
			return AntigravityCLITargetDescriptor{}, fmt.Errorf(
				"codegen.NewAntigravityCLITargetDescriptor: generated file %q is outside the Antigravity CLI skills root %q — "+
					"where: partitioning Antigravity CLI output into packages — "+
					"why: Antigravity CLI only emits skills under %s (agents and hooks are unsupported) — "+
					"fix: emit only files under %s",
				rel, antigravityCLISkillRoot, antigravityCLISkillRoot, antigravityCLISkillRoot,
			)
		}
		skillFiles[rel] = []byte(file.Content)
	}

	skillsBundle, err := buildCodexBundle(skillFiles)
	if err != nil {
		return AntigravityCLITargetDescriptor{}, fmt.Errorf(
			"codegen.NewAntigravityCLITargetDescriptor: build bundle for Antigravity CLI package %q failed: %w",
			antigravityCLISkillsComponent, err,
		)
	}
	emptyAgentsBundle, err := buildCodexBundle(nil)
	if err != nil {
		return AntigravityCLITargetDescriptor{}, fmt.Errorf(
			"codegen.NewAntigravityCLITargetDescriptor: build empty bundle for %q failed: %w",
			antigravityCLIAgentsComponent, err,
		)
	}
	emptyHooksBundle, err := buildCodexBundle(nil)
	if err != nil {
		return AntigravityCLITargetDescriptor{}, fmt.Errorf(
			"codegen.NewAntigravityCLITargetDescriptor: build empty bundle for %q failed: %w",
			antigravityCLIHooksComponent, err,
		)
	}

	return AntigravityCLITargetDescriptor{
		contract: AntigravityCLIRuntimeContractID(),
		install:  installationCoordinates(artifact.HarnessAntigravityCLI),
		packages: []AntigravityCLIPackage{
			{id: antigravityCLISkillsComponent, bundle: skillsBundle},
			{id: antigravityCLIAgentsComponent, bundle: emptyAgentsBundle},
			{id: antigravityCLIHooksComponent, bundle: emptyHooksBundle},
		},
	}, nil
}

// ValidateAntigravityCLITarget verifies that an Antigravity CLI target
// descriptor matches the pinned runtime contract, contains the three closed
// slots, has regular skill files exclusively under `.gemini/config/skills/`,
// and keeps the unsupported agents and hooks bundles empty.
func ValidateAntigravityCLITarget(descriptor AntigravityCLITargetDescriptor) error {
	if descriptor.contract.String() != AntigravityCLIRuntimeContractID().String() {
		return fmt.Errorf(
			"codegen.ValidateAntigravityCLITarget: descriptor runtime contract %q does not match the pinned Antigravity CLI contract %q — "+
				"where: validating an Antigravity CLI target descriptor — "+
				"why: the packaged identity and the lowering contract must agree exactly — "+
				"fix: rebuild the descriptor from an Antigravity CLI target generated against the pinned contract",
			descriptor.contract, AntigravityCLIRuntimeContractID(),
		)
	}

	if len(descriptor.packages) != len(antigravityCLIComponents) {
		return fmt.Errorf(
			"codegen.ValidateAntigravityCLITarget: descriptor has %d packages, want the %d closed Antigravity CLI slots — "+
				"fix: build the descriptor with NewAntigravityCLITargetDescriptor over a complete Antigravity CLI emission",
			len(descriptor.packages), len(antigravityCLIComponents),
		)
	}
	for _, component := range antigravityCLIComponents {
		if _, ok := descriptor.Package(component.id); !ok {
			return fmt.Errorf(
				"codegen.ValidateAntigravityCLITarget: descriptor is missing the %q package — "+
					"fix: build the descriptor over a complete Antigravity CLI emission",
				component.id,
			)
		}
	}

	skills, _ := descriptor.Package(antigravityCLISkillsComponent)
	regularSkills := 0
	for _, entry := range skills.bundle.Manifest().Entries() {
		if !entry.IsRegular() {
			continue
		}
		rel := entry.Path().String()
		if !strings.HasPrefix(rel, antigravityCLISkillRoot+"/") {
			return fmt.Errorf(
				"codegen.ValidateAntigravityCLITarget: skills bundle contains path %q outside %q — "+
					"fix: emit Antigravity CLI skills exclusively under %s",
				rel, antigravityCLISkillRoot, antigravityCLISkillRoot,
			)
		}
		regularSkills++
	}
	if regularSkills == 0 {
		return fmt.Errorf(
			"codegen.ValidateAntigravityCLITarget: skills bundle contains no regular files — "+
				"fix: emit Antigravity CLI skills before building the descriptor",
		)
	}

	for _, emptyID := range []AntigravityCLIComponentID{antigravityCLIAgentsComponent, antigravityCLIHooksComponent} {
		pkg, _ := descriptor.Package(emptyID)
		if len(pkg.bundle.Manifest().Entries()) != 0 {
			return fmt.Errorf(
				"codegen.ValidateAntigravityCLITarget: unsupported slot %q must have an empty bundle, got %d entries — "+
					"why: Antigravity CLI does not ship generated agents or lifecycle hooks — "+
					"fix: leave %q empty",
				emptyID, len(pkg.bundle.Manifest().Entries()), emptyID,
			)
		}
	}

	return nil
}
