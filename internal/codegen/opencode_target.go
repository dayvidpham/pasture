package codegen

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dayvidpham/pasture/internal/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// ComponentID is a stable, harness-scoped identity for one generated OpenCode
// component. It is published in the target descriptor and the target manifest so
// downstream installation (issue #39) can attribute a materialized file to the
// component that produced it without inspecting the file's bytes.
type ComponentID string

const (
	// ComponentOpenCodeHooks is the embedded server-hook module.
	ComponentOpenCodeHooks ComponentID = "opencode.pasture-hooks"
	// ComponentOpenCodeManifest is the target manifest that serializes this
	// descriptor (runtime contract, native tool allow-list, component index).
	ComponentOpenCodeManifest ComponentID = "opencode.target-manifest"
)

// OpenCodeTargetManifestPath is the canonical bundle-relative path of the
// serialized target descriptor.
const OpenCodeTargetManifestPath = "pasture-opencode.json"

// OpenCodeComponent pairs a stable component identity with the canonical
// bundle-relative path it materializes to and the artifact entry type it
// contributes.
type OpenCodeComponent struct {
	ID   ComponentID        `json:"id"`
	Path string             `json:"path"`
	Type artifact.EntryType `json:"type"`
}

// OpenCodeTargetDescriptor is the OpenCode projection's stable, opaque target
// descriptor. It exposes the pinned RuntimeContractID, the native tool allow-list
// derived from that contract, the stable component identities the target owns,
// and a constructor for the packaged artifact.Bundle a source-free CLI
// materializes. It carries no downstream ActivationContractID and holds no second
// catalog: installation semantics live in issue #39.
//
// # Delivered-surface divergence
//
// Issue #27's output contract also lists whole skill directories and standalone
// agent files as bundle components. Those are emitted by the existing OpenCode
// harness path (EmitHarness / OpenCodeTarget) from strict-gate-clean canonical
// source (issue #42). To keep this descriptor deterministic and independent of
// source-tree state, it owns and always includes the two target-native new
// components (the embedded hook module and this serialized manifest) and accepts
// the emitted skill/agent components through Bundle's extra argument, so an
// integration layer folds the full tree into one opaque bundle without this
// package re-walking or re-templating the source checkout.
type OpenCodeTargetDescriptor struct {
	contract   ir.RuntimeContractID
	toolNames  []string
	hooks      string
	components []OpenCodeComponent
}

// NewOpenCodeTargetDescriptor builds the descriptor from the delivered pinned
// OpenCode 1.17.18 runtime contract. It derives the native tool allow-list and
// generates the embedded hook module up front so the descriptor is a complete,
// immutable value.
func NewOpenCodeTargetDescriptor() (OpenCodeTargetDescriptor, error) {
	const where = "codegen.NewOpenCodeTargetDescriptor"
	toolNames, err := deriveOpenCodeNativeToolNames()
	if err != nil {
		return OpenCodeTargetDescriptor{}, fmt.Errorf("%s: %w", where, err)
	}
	hooks, err := GenerateOpenCodeHooksModule()
	if err != nil {
		return OpenCodeTargetDescriptor{}, fmt.Errorf("%s: %w", where, err)
	}
	components := []OpenCodeComponent{
		{ID: ComponentOpenCodeHooks, Path: OpenCodeHooksModulePath, Type: artifact.EntryTypeHook},
		{ID: ComponentOpenCodeManifest, Path: OpenCodeTargetManifestPath, Type: artifact.EntryTypeManifest},
	}
	sort.Slice(components, func(i, j int) bool { return components[i].ID < components[j].ID })
	return OpenCodeTargetDescriptor{
		contract:   runtime.OpenCode1_17_18().ID(),
		toolNames:  toolNames,
		hooks:      hooks,
		components: components,
	}, nil
}

// RuntimeContract returns the pinned OpenCode runtime contract identity.
func (d OpenCodeTargetDescriptor) RuntimeContract() ir.RuntimeContractID { return d.contract }

// NativeToolNames returns a copy of the sorted native tool allow-list derived
// from the pinned contract. No generated OpenCode artifact may reference a tool
// name outside this set.
func (d OpenCodeTargetDescriptor) NativeToolNames() []string {
	return append([]string(nil), d.toolNames...)
}

// Components returns a copy of the target's stable component identities, sorted
// by ID.
func (d OpenCodeTargetDescriptor) Components() []OpenCodeComponent {
	return append([]OpenCodeComponent(nil), d.components...)
}

// HooksModule returns the generated embedded server-hook module source.
func (d OpenCodeTargetDescriptor) HooksModule() string { return d.hooks }

// openCodeTargetManifest is the typed on-disk shape of the serialized descriptor.
// A named struct gives MarshalIndent a deterministic field order.
type openCodeTargetManifest struct {
	Schema          string              `json:"$schema"`
	Target          string              `json:"target"`
	RuntimeContract string              `json:"runtime_contract"`
	HostVersion     string              `json:"host_version"`
	NativeTools     []string            `json:"native_tools"`
	Components      []OpenCodeComponent `json:"components"`
}

// Manifest returns the deterministic JSON serialization of this descriptor. It
// is the manifest component's content and a stable, human-auditable oracle of
// the target's contract and component index.
func (d OpenCodeTargetDescriptor) Manifest() (string, error) {
	manifest := openCodeTargetManifest{
		Schema:          "https://pasture.dev/opencode-target.json",
		Target:          string(ir.HarnessOpenCode),
		RuntimeContract: d.contract.String(),
		HostVersion:     openCodeHostVersion,
		NativeTools:     d.NativeToolNames(),
		Components:      d.Components(),
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf(
			"codegen.OpenCodeTargetDescriptor.Manifest: marshal target manifest failed — this is a bug in the manifest struct definition: %w", err)
	}
	return string(data) + "\n", nil
}

// Bundle assembles the opaque artifact.Bundle a packaged CLI materializes. It
// always includes the two target-owned components (the embedded hook module and
// the serialized manifest) and appends extra components — typically the emitted
// whole skill directories and standalone agent files — supplied by an
// integration layer. The bundle's id is the pinned RuntimeContractID, so a
// consumer can attribute the whole bundle to its producing contract.
func (d OpenCodeTargetDescriptor) Bundle(extra []artifact.Source) (artifact.Bundle, error) {
	const where = "codegen.OpenCodeTargetDescriptor.Bundle"
	manifest, err := d.Manifest()
	if err != nil {
		return artifact.Bundle{}, fmt.Errorf("%s: %w", where, err)
	}
	sources := []artifact.Source{
		{Path: OpenCodeHooksModulePath, Type: artifact.EntryTypeHook, Mode: 0o644, Content: []byte(d.hooks)},
		{Path: OpenCodeTargetManifestPath, Type: artifact.EntryTypeManifest, Mode: 0o644, Content: []byte(manifest)},
	}
	sources = append(sources, extra...)
	bundle, err := artifact.NewBundle(d.contract.String(), sources)
	if err != nil {
		return artifact.Bundle{}, fmt.Errorf(
			"%s: assemble bundle for %d component(s) failed — a supplied component path collided with a target-owned path (%q, %q) or was not a clean relative path: %w",
			where, len(sources), OpenCodeHooksModulePath, OpenCodeTargetManifestPath, err)
	}
	return bundle, nil
}
