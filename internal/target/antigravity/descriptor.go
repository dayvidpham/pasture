// Package antigravity publishes the immutable Antigravity CLI target descriptor
// consumed by installation. It adapts the canonical codegen target without
// creating a second component identity or activation-state authority.
package antigravity

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

const maxSnapshotBytes = 32 << 20

//go:embed assets/antigravity-cli-generated.json.gz
var generatedSnapshot []byte

type snapshotFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Component is one canonical installer coordinate and its immutable target
// bundle. Only skills is populated; agents and hooks are explicitly unsupported
// and carry empty bundles with defaultEnabled false.
type Component struct {
	id             artifact.ComponentID
	packageID      PackageID
	bundle         artifact.Bundle
	layout         ComponentLayout
	defaultEnabled bool
	valid          bool
}

// PackageID is the closed Antigravity CLI package identity paired with its
// installer extension.
type PackageID struct {
	extension artifact.Extension
	value     codegen.AntigravityCLIComponentID
}

func canonicalPackageName(extension artifact.Extension) (string, bool) {
	switch extension {
	case artifact.ExtensionSkills:
		return "pasture-antigravity-cli-skills", true
	case artifact.ExtensionAgents:
		return "pasture-antigravity-cli-agents", true
	case artifact.ExtensionHooks:
		return "pasture-antigravity-cli-hooks", true
	default:
		return "", false
	}
}

func NewPackageID(extension artifact.Extension, value codegen.AntigravityCLIComponentID) (PackageID, error) {
	want, ok := canonicalPackageName(extension)
	if !ok || value.String() == "" || value.String() != want {
		return PackageID{}, fmt.Errorf("Antigravity CLI package identity %q does not match extension %q; use the canonical generated package identity for skills, agents, or hooks", value.String(), extension)
	}
	return PackageID{extension: extension, value: value}, nil
}

func (p PackageID) String() string                { return p.value.String() }
func (p PackageID) Extension() artifact.Extension { return p.extension }
func (p PackageID) IsValid() bool {
	want, ok := canonicalPackageName(p.extension)
	return ok && p.value.String() != "" && p.value.String() == want
}

// ComponentLayout is the closed installation layout for one typed Antigravity
// CLI extension.
type ComponentLayout struct {
	extension artifact.Extension
	prefix    string
	supported bool
}

func (l ComponentLayout) Extension() artifact.Extension { return l.extension }
func (l ComponentLayout) Prefix() string                { return l.prefix }
func (l ComponentLayout) Supported() bool               { return l.supported }

var componentLayouts = map[artifact.Extension]ComponentLayout{
	artifact.ExtensionSkills: {extension: artifact.ExtensionSkills, prefix: ".gemini/config/skills/", supported: true},
	artifact.ExtensionAgents: {extension: artifact.ExtensionAgents, prefix: "", supported: false},
	artifact.ExtensionHooks:  {extension: artifact.ExtensionHooks, prefix: "", supported: false},
}

// NewComponent constructs one canonical Antigravity CLI component. Unsupported
// slots (agents, hooks) cannot be default-enabled.
func NewComponent(extension artifact.Extension, packageID PackageID, bundle artifact.Bundle, defaultEnabled bool) (Component, error) {
	id, err := artifact.NewComponentID(artifact.HarnessAntigravityCLI, extension)
	if err != nil {
		return Component{}, fmt.Errorf("construct canonical Antigravity CLI %s identity: %w", extension, err)
	}
	if !id.IsValid() || id.Harness() != artifact.HarnessAntigravityCLI {
		return Component{}, fmt.Errorf("Antigravity CLI target component construction failed: identity %q is not a canonical Antigravity CLI component", id)
	}
	if !packageID.IsValid() || packageID.Extension() != extension {
		return Component{}, fmt.Errorf("Antigravity CLI %s component requires its nonzero extension-matching package identity", extension)
	}
	layout, ok := componentLayouts[extension]
	if !ok {
		return Component{}, fmt.Errorf("Antigravity CLI target component %q has no approved immutable layout", id)
	}
	if !layout.supported && defaultEnabled {
		return Component{}, fmt.Errorf("Antigravity CLI %s cannot be default-enabled: this extension is unsupported on Antigravity CLI", extension)
	}
	component := Component{id: id, packageID: packageID, bundle: bundle, layout: layout, defaultEnabled: defaultEnabled, valid: true}
	if err := ValidateComponentLayout(component); err != nil {
		return Component{}, err
	}
	return component, nil
}

func (c Component) ID() artifact.ComponentID      { return c.id }
func (c Component) PackageID() PackageID          { return c.packageID }
func (c Component) Extension() artifact.Extension { return c.id.Extension() }
func (c Component) Bundle() artifact.Bundle       { return c.bundle }
func (c Component) Layout() ComponentLayout       { return c.layout }
func (c Component) DefaultEnabled() bool          { return c.defaultEnabled }
func (c Component) IsValid() bool                 { return c.valid }

// TargetDescriptor exposes skills, agents, and hooks in canonical order.
type TargetDescriptor struct {
	contract ir.RuntimeContractID
	skills   Component
	agents   Component
	hooks    Component
	valid    bool
}

// NewTargetDescriptor assembles the closed three-component target using the
// pinned Antigravity CLI runtime contract.
func NewTargetDescriptor(skills, agents, hooks Component) (TargetDescriptor, error) {
	want := []artifact.Extension{artifact.ExtensionSkills, artifact.ExtensionAgents, artifact.ExtensionHooks}
	provided := []Component{skills, agents, hooks}
	for index, component := range provided {
		if !component.IsValid() || component.Extension() != want[index] || !component.PackageID().IsValid() || component.PackageID().Extension() != want[index] {
			return TargetDescriptor{}, fmt.Errorf("Antigravity CLI target descriptor slot %s contains an invalid or mismatched component; pass exactly skills, agents, and hooks in canonical order", want[index])
		}
	}
	if agents.DefaultEnabled() || hooks.DefaultEnabled() {
		return TargetDescriptor{}, fmt.Errorf("Antigravity CLI target descriptor rejected default-enabled agents or hooks; only skills is supported")
	}
	return TargetDescriptor{contract: codegen.AntigravityCLIRuntimeContractID(), skills: skills, agents: agents, hooks: hooks, valid: true}, nil
}

func (d TargetDescriptor) Harness() ir.HarnessID                   { return ir.HarnessAntigravityCLI }
func (d TargetDescriptor) RuntimeContractID() ir.RuntimeContractID { return d.contract }
func (d TargetDescriptor) Skills() Component                       { return d.skills }
func (d TargetDescriptor) Agents() Component                       { return d.agents }
func (d TargetDescriptor) Hooks() Component                        { return d.hooks }
func (d TargetDescriptor) Components() []Component                 { return []Component{d.skills, d.agents, d.hooks} }
func (d TargetDescriptor) IsValid() bool                           { return d.valid }

func (d TargetDescriptor) Component(extension artifact.Extension) (Component, error) {
	switch extension {
	case artifact.ExtensionSkills:
		return d.skills, nil
	case artifact.ExtensionAgents:
		return d.agents, nil
	case artifact.ExtensionHooks:
		return d.hooks, nil
	default:
		return Component{}, fmt.Errorf("Antigravity CLI target lookup failed: extension %q is not skills, agents, or hooks; request one canonical installer extension", extension)
	}
}

// Descriptor constructs the production descriptor solely from the embedded
// generated snapshot, making it safe from an unrelated empty working directory.
func Descriptor() (TargetDescriptor, error) {
	files, err := decodeSnapshot(generatedSnapshot)
	if err != nil {
		return TargetDescriptor{}, err
	}
	upstream, err := codegen.NewAntigravityCLITargetDescriptor("", files)
	if err != nil {
		return TargetDescriptor{}, fmt.Errorf("construct Antigravity CLI target from embedded canonical output: %w", err)
	}
	if err := codegen.ValidateAntigravityCLITarget(upstream); err != nil {
		return TargetDescriptor{}, fmt.Errorf("validate embedded Antigravity CLI target before installation: %w", err)
	}

	packages := upstream.Packages()
	coordinates := upstream.InstallationComponents()
	if len(packages) != 3 || len(coordinates) != len(packages) {
		return TargetDescriptor{}, fmt.Errorf("embedded Antigravity CLI target has %d packages and %d installer coordinates; regenerate exactly three aligned slots", len(packages), len(coordinates))
	}
	byExtension := make(map[artifact.Extension]Component, 3)
	for index, pkg := range packages {
		extension := coordinates[index].Extension()
		packageID, err := NewPackageID(extension, pkg.ID())
		if err != nil {
			return TargetDescriptor{}, err
		}
		component, err := NewComponent(extension, packageID, pkg.Bundle(), extension == artifact.ExtensionSkills)
		if err != nil {
			return TargetDescriptor{}, err
		}
		if _, duplicate := byExtension[extension]; duplicate {
			return TargetDescriptor{}, fmt.Errorf("embedded Antigravity CLI target contains duplicate %s package", extension)
		}
		byExtension[extension] = component
	}
	components := make([]Component, 0, 3)
	for _, extension := range []artifact.Extension{artifact.ExtensionSkills, artifact.ExtensionAgents, artifact.ExtensionHooks} {
		component, ok := byExtension[extension]
		if !ok {
			return TargetDescriptor{}, fmt.Errorf("embedded Antigravity CLI target is missing its %s package; regenerate the complete target snapshot", extension)
		}
		components = append(components, component)
	}
	descriptor, err := NewTargetDescriptor(components[0], components[1], components[2])
	if err != nil {
		return TargetDescriptor{}, err
	}
	if descriptor.contract != upstream.RuntimeContractID() {
		return TargetDescriptor{}, fmt.Errorf("embedded Antigravity CLI target runtime contract %q differs from pinned descriptor contract %q; regenerate the snapshot", upstream.RuntimeContractID(), descriptor.contract)
	}
	return descriptor, nil
}

// ValidateComponentLayout proves one immutable component owns only its closed
// public global paths (or is empty for unsupported extensions).
func ValidateComponentLayout(component Component) error {
	if !component.IsValid() {
		return fmt.Errorf("Antigravity CLI component layout validation failed: component is invalid; construct it with NewComponent or Descriptor")
	}
	layout, ok := componentLayouts[component.Extension()]
	if !ok || layout.extension != component.Extension() {
		return fmt.Errorf("Antigravity CLI %s component layout metadata is missing or mismatched", component.Extension())
	}
	entries := component.Bundle().Manifest().Entries()
	if !layout.supported {
		if len(entries) != 0 {
			return fmt.Errorf("Antigravity CLI unsupported %s component must carry an empty bundle, got %d entries", component.Extension(), len(entries))
		}
		return nil
	}
	regular := 0
	prefixRoot := strings.TrimSuffix(layout.prefix, "/")
	for _, entry := range entries {
		p := entry.Path().String()
		underPrefix := strings.HasPrefix(p, layout.prefix)
		prefixAncestor := !entry.IsRegular() && (p == prefixRoot || strings.HasPrefix(prefixRoot, p+"/"))
		if !underPrefix && !prefixAncestor {
			return fmt.Errorf("Antigravity CLI %s component contains out-of-layout path %q outside approved prefix %q; regenerate the immutable target before activation", component.Extension(), entry.Path(), layout.prefix)
		}
		if entry.IsRegular() {
			regular++
		}
	}
	if regular == 0 {
		return fmt.Errorf("Antigravity CLI %s component has no regular artifacts below its public layout; regenerate the complete target", component.Extension())
	}
	return nil
}

func decodeSnapshot(compressed []byte) ([]codegen.GeneratedFile, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("open embedded Antigravity CLI target snapshot: %w; rerun go generate ./internal/target/antigravity/...", err)
	}
	defer reader.Close()
	payload, err := io.ReadAll(io.LimitReader(reader, maxSnapshotBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read embedded Antigravity CLI target snapshot: %w; regenerate the snapshot", err)
	}
	if len(payload) > maxSnapshotBytes {
		return nil, fmt.Errorf("embedded Antigravity CLI target snapshot exceeds %d bytes after decompression", maxSnapshotBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var wire []snapshotFile
	if err := decoder.Decode(&wire); err != nil {
		return nil, fmt.Errorf("decode embedded Antigravity CLI target snapshot: %w; rerun go generate ./internal/target/antigravity/...", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("decode embedded Antigravity CLI target snapshot: trailing JSON values are forbidden: %v", err)
	}
	files := make([]codegen.GeneratedFile, 0, len(wire))
	for _, file := range wire {
		files = append(files, codegen.GeneratedFile{Path: file.Path, Content: file.Content})
	}
	return files, nil
}
