// Package codex publishes the immutable Codex target descriptor consumed by
// installation. It adapts the canonical codegen target without creating a
// second component identity or activation-state authority.
package codex

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

const maxSnapshotBytes = 32 << 20

//go:embed assets/codex-generated.json.gz
var generatedSnapshot []byte

type snapshotFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Component is one canonical installer coordinate and its immutable target
// bundle. Default enablement is descriptive; hooks are always default-off.
type Component struct {
	id             artifact.ComponentID
	packageID      codegen.CodexComponentID
	bundle         artifact.Bundle
	layout         ComponentLayout
	defaultEnabled bool
	valid          bool
}

// ComponentLayout is the closed installation layout for one typed Codex
// extension. It is defined once and shared by descriptor construction,
// activation construction, and independent fixture verification.
type ComponentLayout struct {
	extension   artifact.Extension
	prefix      string
	publicFiles []string
}

func (l ComponentLayout) Extension() artifact.Extension { return l.extension }
func (l ComponentLayout) Prefix() string                { return l.prefix }
func (l ComponentLayout) PublicFiles() []string         { return append([]string(nil), l.publicFiles...) }

var componentLayouts = map[artifact.Extension]ComponentLayout{
	artifact.ExtensionSkills: {extension: artifact.ExtensionSkills, prefix: ".agents/skills/"},
	artifact.ExtensionAgents: {extension: artifact.ExtensionAgents, prefix: ".codex/agents/"},
	artifact.ExtensionHooks: {
		extension: artifact.ExtensionHooks,
		prefix:    ".codex/hooks/",
		publicFiles: []string{
			".codex/hooks.json",
			".codex/pasture-codex-activation.json",
		},
	},
}

// NewComponent constructs one canonical Codex component. Hooks cannot be
// default-enabled; that policy is statically enforced rather than left to a UI.
func NewComponent(extension artifact.Extension, bundle artifact.Bundle, defaultEnabled bool) (Component, error) {
	id, err := artifact.NewComponentID(artifact.HarnessCodex, extension)
	if err != nil {
		return Component{}, fmt.Errorf("construct canonical Codex %s identity: %w", extension, err)
	}
	if !id.IsValid() || id.Harness() != artifact.HarnessCodex {
		return Component{}, fmt.Errorf("codex target component construction failed: identity %q is not a canonical Codex component; use artifact.NewComponentID with artifact.HarnessCodex and one supported extension", id)
	}
	layout, ok := componentLayouts[extension]
	if !ok {
		return Component{}, fmt.Errorf("codex target component %q has no approved immutable layout; use skills, agents, or hooks and regenerate the target", id)
	}
	component := Component{id: id, bundle: bundle, layout: layout, defaultEnabled: defaultEnabled, valid: true}
	if err := ValidateComponentLayout(component); err != nil {
		return Component{}, err
	}
	if extension == artifact.ExtensionHooks && defaultEnabled {
		return Component{}, fmt.Errorf("Codex hooks cannot be default-enabled: hooks execute lifecycle commands and require explicit selection plus native trust review; construct the hooks component with defaultEnabled false")
	}
	return component, nil
}

func (c Component) ID() artifact.ComponentID            { return c.id }
func (c Component) PackageID() codegen.CodexComponentID { return c.packageID }
func (c Component) Extension() artifact.Extension       { return c.id.Extension() }
func (c Component) Bundle() artifact.Bundle             { return c.bundle }
func (c Component) Layout() ComponentLayout             { return c.layout }
func (c Component) DefaultEnabled() bool                { return c.defaultEnabled }
func (c Component) IsValid() bool                       { return c.valid }

// TargetDescriptor exposes exactly skills, agents, and hooks in canonical
// order. It contains no destination, mutable install state, or trust approval.
type TargetDescriptor struct {
	contract ir.RuntimeContractID
	skills   Component
	agents   Component
	hooks    Component
	valid    bool
}

// NewTargetDescriptor assembles the closed three-component target using the
// pinned Codex runtime contract.
func NewTargetDescriptor(skills, agents, hooks Component) (TargetDescriptor, error) {
	want := []artifact.Extension{artifact.ExtensionSkills, artifact.ExtensionAgents, artifact.ExtensionHooks}
	provided := []Component{skills, agents, hooks}
	for index, component := range provided {
		if !component.IsValid() || component.Extension() != want[index] {
			return TargetDescriptor{}, fmt.Errorf("Codex target descriptor slot %s contains an invalid or mismatched component; pass exactly skills, agents, and hooks in canonical order", want[index])
		}
	}
	if hooks.DefaultEnabled() {
		return TargetDescriptor{}, fmt.Errorf("Codex target descriptor rejected default-enabled hooks; require explicit hook selection and native trust review")
	}
	return TargetDescriptor{contract: codegen.CodexRuntimeContractID(), skills: skills, agents: agents, hooks: hooks, valid: true}, nil
}

func (d TargetDescriptor) Harness() ir.HarnessID                   { return ir.HarnessCodex }
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
		return Component{}, fmt.Errorf("Codex target lookup failed: extension %q is not skills, agents, or hooks; request one canonical installer extension", extension)
	}
}

// Descriptor constructs the production descriptor solely from the embedded
// generated snapshot, making it safe from an unrelated empty working directory.
func Descriptor() (TargetDescriptor, error) {
	files, err := decodeSnapshot(generatedSnapshot)
	if err != nil {
		return TargetDescriptor{}, err
	}
	upstream, err := codegen.NewCodexTargetDescriptor("", files)
	if err != nil {
		return TargetDescriptor{}, fmt.Errorf("construct Codex target from embedded canonical output: %w", err)
	}
	if err := codegen.ValidateCodexTarget(upstream); err != nil {
		return TargetDescriptor{}, fmt.Errorf("validate embedded Codex target before installation: %w", err)
	}

	packages := upstream.Packages()
	coordinates := upstream.InstallationComponents()
	if len(packages) != 3 || len(coordinates) != len(packages) {
		return TargetDescriptor{}, fmt.Errorf("embedded Codex target has %d packages and %d installer coordinates; regenerate exactly three aligned packages", len(packages), len(coordinates))
	}
	byExtension := make(map[artifact.Extension]Component, 3)
	for index, pkg := range packages {
		extension := coordinates[index].Extension()
		bundle := pkg.Bundle()
		if extension == artifact.ExtensionHooks {
			globalConfig, configErr := codegen.EmitCodexGlobalHooksConfig()
			if configErr != nil {
				return TargetDescriptor{}, configErr
			}
			bundle, err = hookBundleWithPublicConfig(bundle, upstream.ManifestBundle(), []byte(globalConfig.Content))
			if err != nil {
				return TargetDescriptor{}, err
			}
		}
		component, err := NewComponent(extension, bundle, extension != artifact.ExtensionHooks)
		if err != nil {
			return TargetDescriptor{}, err
		}
		component.packageID = pkg.ID()
		if _, duplicate := byExtension[extension]; duplicate {
			return TargetDescriptor{}, fmt.Errorf("embedded Codex target contains duplicate %s package; regenerate a target with exactly one package per extension", extension)
		}
		byExtension[extension] = component
	}
	components := make([]Component, 0, 3)
	for _, extension := range []artifact.Extension{artifact.ExtensionSkills, artifact.ExtensionAgents, artifact.ExtensionHooks} {
		component, ok := byExtension[extension]
		if !ok {
			return TargetDescriptor{}, fmt.Errorf("embedded Codex target is missing its %s package; regenerate the complete target snapshot", extension)
		}
		components = append(components, component)
	}
	descriptor, err := NewTargetDescriptor(components[0], components[1], components[2])
	if err != nil {
		return TargetDescriptor{}, err
	}
	if descriptor.contract != upstream.RuntimeContractID() {
		return TargetDescriptor{}, fmt.Errorf("embedded Codex target runtime contract %q differs from pinned descriptor contract %q; regenerate the snapshot and review the contract update", upstream.RuntimeContractID(), descriptor.contract)
	}
	return descriptor, nil
}

func hookBundleWithPublicConfig(hooks, targetManifest artifact.Bundle, globalConfig []byte) (artifact.Bundle, error) {
	const hooksConfig = ".codex/hooks.json"
	const activationReport = ".codex/pasture-codex-activation.json"
	selected := map[string]bool{hooksConfig: true, activationReport: true}
	entries := hooks.Manifest().Entries()
	source := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsRegular() {
			content, err := readBundleFile(hooks, entry.Path().String())
			if err != nil {
				return artifact.Bundle{}, fmt.Errorf("read embedded Codex hook artifact %q: %w", entry.Path(), err)
			}
			source[entry.Path().String()] = &fstest.MapFile{Data: content, Mode: fs.FileMode(entry.Mode().Bits())}
		}
	}
	for _, entry := range targetManifest.Manifest().Entries() {
		if !entry.IsRegular() || !selected[entry.Path().String()] {
			continue
		}
		content, err := readBundleFile(targetManifest, entry.Path().String())
		if err != nil {
			return artifact.Bundle{}, fmt.Errorf("read embedded Codex public hook configuration %q: %w", entry.Path(), err)
		}
		if entry.Path().String() == hooksConfig {
			content = append([]byte(nil), globalConfig...)
			entry, err = artifact.NewFileEntry(entry.Path(), entry.Mode(), artifact.DigestBytes(content))
			if err != nil {
				return artifact.Bundle{}, fmt.Errorf("construct global Codex hooks configuration identity: %w", err)
			}
		}
		entries = append(entries, entry)
		source[entry.Path().String()] = &fstest.MapFile{Data: content, Mode: fs.FileMode(entry.Mode().Bits())}
		delete(selected, entry.Path().String())
	}
	if len(selected) != 0 {
		return artifact.Bundle{}, fmt.Errorf("embedded Codex target omits public hook configuration %v; regenerate the complete target snapshot", selected)
	}
	manifest, err := artifact.NewManifest(entries...)
	if err != nil {
		return artifact.Bundle{}, fmt.Errorf("assemble immutable Codex hook artifact manifest: %w", err)
	}
	bundle, err := artifact.NewBundle(source, manifest)
	if err != nil {
		return artifact.Bundle{}, fmt.Errorf("snapshot immutable Codex hook artifacts: %w", err)
	}
	return bundle, nil
}

// ValidateComponentLayout proves one immutable component owns only its closed
// public global paths. Host activation calls it before filesystem access.
func ValidateComponentLayout(component Component) error {
	if !component.IsValid() {
		return fmt.Errorf("Codex component layout validation failed: component is invalid; construct it with NewComponent or Descriptor")
	}
	layout, ok := componentLayouts[component.Extension()]
	if !ok || layout.extension != component.Extension() {
		return fmt.Errorf("Codex %s component layout metadata is missing or mismatched; regenerate the closed target descriptor", component.Extension())
	}
	public := make(map[string]bool, len(layout.publicFiles))
	for _, path := range layout.publicFiles {
		public[path] = true
	}
	regular := 0
	for _, entry := range component.Bundle().Manifest().Entries() {
		path := entry.Path().String()
		underPrefix := strings.HasPrefix(path, layout.prefix)
		prefixRoot := strings.TrimSuffix(layout.prefix, "/")
		prefixAncestor := !entry.IsRegular() && (path == prefixRoot || strings.HasPrefix(prefixRoot, path+"/"))
		publicAncestor := false
		if !entry.IsRegular() {
			for approved := range public {
				publicAncestor = publicAncestor || strings.HasPrefix(approved, path+"/")
			}
		}
		if !underPrefix && !prefixAncestor && !public[path] && !publicAncestor {
			return fmt.Errorf("Codex %s component contains out-of-layout path %q outside approved prefix %q; regenerate the immutable target before activation", component.Extension(), entry.Path(), layout.prefix)
		}
		if entry.IsRegular() {
			regular++
		}
	}
	if regular == 0 {
		return fmt.Errorf("Codex %s component has no regular artifacts below its public layout; regenerate the complete target", component.Extension())
	}
	return nil
}

func readBundleFile(bundle artifact.Bundle, name string) ([]byte, error) {
	handle, err := bundle.Open(name)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	return io.ReadAll(handle)
}

func decodeSnapshot(compressed []byte) ([]codegen.GeneratedFile, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("open embedded Codex target snapshot: %w; rerun go generate ./internal/target/codex/...", err)
	}
	defer reader.Close()
	payload, err := io.ReadAll(io.LimitReader(reader, maxSnapshotBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read embedded Codex target snapshot: %w; regenerate the snapshot", err)
	}
	if len(payload) > maxSnapshotBytes {
		return nil, fmt.Errorf("embedded Codex target snapshot exceeds %d bytes after decompression; inspect unexpected generated output before increasing the reviewed limit", maxSnapshotBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var wire []snapshotFile
	if err := decoder.Decode(&wire); err != nil {
		return nil, fmt.Errorf("decode embedded Codex target snapshot: %w; rerun go generate ./internal/target/codex/...", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("decode embedded Codex target snapshot: trailing JSON values are forbidden; regenerate the snapshot: %v", err)
	}
	files := make([]codegen.GeneratedFile, 0, len(wire))
	for _, file := range wire {
		files = append(files, codegen.GeneratedFile{Path: file.Path, Content: file.Content})
	}
	return files, nil
}
