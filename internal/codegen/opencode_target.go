package codegen

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// ComponentID is a stable, harness-scoped identity for one generated OpenCode
// component. It is published in the target descriptor and the target manifest so
// downstream installation (issue #39) can attribute a materialized file to the
// component that produced it without inspecting the file's bytes.
//
// artifact.ComponentID and artifact.Extension are the canonical install
// coordinate across harnesses. This target-local identity describes generated
// OpenCode file roles within that coordinate; it is not a second install
// identity authority.
type ComponentID string

const (
	// ComponentOpenCodeHooks is the embedded server-hook module.
	ComponentOpenCodeHooks ComponentID = "opencode.pasture-hooks"
	// ComponentOpenCodeManifest is the target manifest that serializes this
	// descriptor (runtime contract, native tool allow-list, component index).
	ComponentOpenCodeManifest ComponentID = "opencode.target-manifest"
)

// ComponentKind classifies an OpenCode target component by the role its file
// plays in the projected plugin. It is target-descriptor metadata, deliberately
// distinct from artifact.EntryType: the shared artifact bundle classifies every
// entry only as a regular file or a directory (its ownership inventory concern),
// while this kind records what a materialized file means to the target so the
// serialized manifest stays a human-auditable component index.
type ComponentKind string

const (
	// ComponentKindSkill is a whole-skill-directory file preserved for the target.
	ComponentKindSkill ComponentKind = "skill"
	// ComponentKindAgent is a standalone agent role file.
	ComponentKindAgent ComponentKind = "agent"
	// ComponentKindHook is an embedded server-hook module file.
	ComponentKindHook ComponentKind = "hook"
	// ComponentKindManifest is the target manifest file.
	ComponentKindManifest ComponentKind = "manifest"
)

// IsValid reports whether k is one of the closed ComponentKind members.
func (k ComponentKind) IsValid() bool {
	switch k {
	case ComponentKindSkill, ComponentKindAgent, ComponentKindHook, ComponentKindManifest:
		return true
	default:
		return false
	}
}

// OpenCodeTargetManifestPath is the canonical bundle-relative path of the
// serialized target descriptor.
const OpenCodeTargetManifestPath = ".opencode/pasture-opencode.json"

// OpenCodeComponent pairs a stable component identity with the canonical
// bundle-relative path it materializes to and the descriptor-level kind it
// contributes.
type OpenCodeComponent struct {
	ID   ComponentID   `json:"id"`
	Path string        `json:"path"`
	Type ComponentKind `json:"type"`
}

// OpenCodeComponentFile is one already-emitted file an integration layer folds
// into the target bundle: its canonical bundle-relative path and its exact
// bytes. The shared artifact bundle freezes each file's path, mode, and content
// digest — it records no target-role kind, so this input carries none either.
type OpenCodeComponentFile struct {
	Path    string
	Content []byte
}

// OpenCodeTargetDescriptor is the OpenCode projection's stable, opaque target
// descriptor. It exposes the pinned RuntimeContractID, the native tool allow-list
// derived from that contract, the stable component identities the target owns,
// and a constructor for the packaged artifact.Bundle a source-free CLI
// materializes. It carries no downstream ActivationContractID and holds no second
// catalog: installation semantics live in issue #39.
//
// The packaged bundle is the shared, content-addressed
// github.com/dayvidpham/pasture/artifact.Bundle (issue #48) that the Claude Code
// (#24) and Codex (#45) targets also publish — one neutral bundle model across
// all three targets, never a per-target copy — with its strongly-typed
// Path/Mode/Digest value objects and a lexicographically sorted manifest.
//
// # Delivered-surface divergence
//
// Issue #27's output contract also lists whole skill directories and standalone
// agent files as bundle components. Those are emitted by the existing OpenCode
// harness path (EmitHarness / OpenCodeTarget) from strict-gate-clean canonical
// source (issue #42). To keep this descriptor deterministic and independent of
// source-tree state, it owns and always includes the two target-native new
// components (the embedded hook module and this serialized manifest) and accepts
// the emitted skill/agent files through Bundle's extra argument, so an
// integration layer folds the full tree into one opaque bundle without this
// package re-walking or re-templating the source checkout.
type OpenCodeTargetDescriptor struct {
	contract   ir.RuntimeContractID
	install    []artifact.ComponentID
	toolNames  []string
	hooks      string
	components []OpenCodeComponent
}

// NewOpenCodeTargetDescriptor builds the descriptor from the delivered pinned
// OpenCode runtime contract. It derives the native tool allow-list and
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
		{ID: ComponentOpenCodeHooks, Path: OpenCodeHooksModulePath, Type: ComponentKindHook},
		{ID: ComponentOpenCodeManifest, Path: OpenCodeTargetManifestPath, Type: ComponentKindManifest},
	}
	sort.Slice(components, func(i, j int) bool { return components[i].ID < components[j].ID })
	return OpenCodeTargetDescriptor{
		contract:   runtime.OpenCode1_18_29().ID(),
		install:    installationCoordinates(artifact.HarnessOpenCode),
		toolNames:  toolNames,
		hooks:      hooks,
		components: components,
	}, nil
}

// RuntimeContract returns the pinned OpenCode runtime contract identity.
func (d OpenCodeTargetDescriptor) RuntimeContract() ir.RuntimeContractID { return d.contract }

// InstallationComponents returns the shared installer coordinates. Target-file
// component IDs remain projection metadata and are not installation identities.
func (d OpenCodeTargetDescriptor) InstallationComponents() []artifact.ComponentID {
	return append([]artifact.ComponentID(nil), d.install...)
}

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
	Schema          string                            `json:"$schema"`
	Target          string                            `json:"target"`
	RuntimeContract string                            `json:"runtime_contract"`
	HostVersion     string                            `json:"host_version"`
	NativeTools     []string                          `json:"native_tools"`
	Components      []OpenCodeComponent               `json:"components"`
	Activation      []openCodeActivationManifestEntry `json:"activation"`
}

type openCodeActivationManifestEntry struct {
	Event           string `json:"event"`
	State           string `json:"state"`
	Reason          string `json:"reason,omitempty"`
	CaptureProof    string `json:"capture_proof,omitempty"`
	ProductionProof string `json:"production_proof,omitempty"`
}

// Manifest returns the deterministic JSON serialization of this descriptor. It
// is the manifest component's content and a stable, human-auditable oracle of
// the target's contract and component index.
func (d OpenCodeTargetDescriptor) Manifest() (string, error) {
	activationManifest, err := openCodeActivationManifest()
	if err != nil {
		return "", fmt.Errorf("codegen.OpenCodeTargetDescriptor.Manifest: activation metadata: %w", err)
	}
	manifest := openCodeTargetManifest{
		Schema:          "https://pasture.dev/opencode-target.json",
		Target:          string(ir.HarnessOpenCode),
		RuntimeContract: d.contract.String(),
		HostVersion:     openCodeHostVersion(),
		NativeTools:     d.NativeToolNames(),
		Components:      d.Components(),
		Activation:      activationManifest,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf(
			"codegen.OpenCodeTargetDescriptor.Manifest: marshal target manifest failed — this is a bug in the manifest struct definition: %w", err)
	}
	return string(data) + "\n", nil
}

// renderOpenCodeActivationReport builds the committed OpenCode activation
// audit report written to OpenCodeActivationReportPath, in the shape shared
// with the Claude Code and Codex reports. The target manifest keeps its own
// activation array as target data; the two are derived from the same
// activation entries in one generation, and a test holds them equal.
func renderOpenCodeActivationReport() (string, error) {
	manifest := registration.OpenCode1_18_29()
	states, err := openCodeActivationEntries()
	if err != nil {
		return "", fmt.Errorf("codegen.renderOpenCodeActivationReport: %w", err)
	}
	report, err := buildActivationSupportReport("codegen.renderOpenCodeActivationReport", manifest, states)
	if err != nil {
		return "", err
	}
	wire, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("codegen.renderOpenCodeActivationReport: marshal activation report: %w", err)
	}
	return string(wire) + "\n", nil
}

func openCodeActivationManifest() ([]openCodeActivationManifestEntry, error) {
	entries, err := openCodeActivationEntries()
	if err != nil {
		return nil, err
	}
	events := registration.OpenCode1_18_29().Entries()
	out := make([]openCodeActivationManifestEntry, len(entries))
	for index, entry := range entries {
		out[index] = openCodeActivationManifestEntry{
			Event:           events[index].NativeName,
			State:           entry.State.String(),
			Reason:          entry.Reason.String(),
			CaptureProof:    entry.CaptureProof.Name(),
			ProductionProof: entry.ProductionProof.Name(),
		}
	}
	return out, nil
}

// Bundle assembles the shared, content-addressed artifact.Bundle a packaged CLI
// materializes. It always includes the two target-owned components (the embedded
// hook module and the serialized manifest) and folds in extra emitted files —
// typically the whole skill directories and standalone agent files — supplied by
// an integration layer. Every entry is frozen at mode 0644 with its exact-byte
// sha256 digest, and each file's parent directories are declared, so the bundle
// manifest is a complete, deterministic ownership inventory.
func (d OpenCodeTargetDescriptor) Bundle(extra []OpenCodeComponentFile) (artifact.Bundle, error) {
	const where = "codegen.OpenCodeTargetDescriptor.Bundle"
	manifest, err := d.Manifest()
	if err != nil {
		return artifact.Bundle{}, fmt.Errorf("%s: %w", where, err)
	}
	files := map[string][]byte{
		OpenCodeHooksModulePath:    []byte(d.hooks),
		OpenCodeTargetManifestPath: []byte(manifest),
	}
	for _, component := range extra {
		rel := filepath.ToSlash(component.Path)
		if _, clash := files[rel]; clash {
			return artifact.Bundle{}, fmt.Errorf(
				"%s: extra component path %q collides with a target-owned component (%q or %q) — "+
					"where: folding integration-layer files into the OpenCode bundle — "+
					"why: each bundle path is a unique ownership key so a materialized tree is unambiguous — "+
					"fix: emit the skill/agent file under a path distinct from the target-owned hook module and manifest",
				where, rel, OpenCodeHooksModulePath, OpenCodeTargetManifestPath)
		}
		files[rel] = append([]byte(nil), component.Content...)
	}
	bundle, err := buildOpenCodeBundle(files)
	if err != nil {
		return artifact.Bundle{}, fmt.Errorf(
			"%s: assemble bundle for %d component(s) failed — a supplied component path was not a clean relative path: %w",
			where, len(files), err)
	}
	return bundle, nil
}

// buildOpenCodeBundle freezes a set of package-relative files into an immutable,
// content-addressed artifact.Bundle. Every regular file entry carries mode 0644
// and its exact-byte sha256 digest; the parent directory chain of each file is
// declared (mode 0755) so the manifest is a complete ownership inventory. The
// manifest is lexicographically sorted, so identical files always yield a
// byte-identical manifest and content address.
func buildOpenCodeBundle(files map[string][]byte) (artifact.Bundle, error) {
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
			return artifact.Bundle{}, fmt.Errorf("opencode bundle entry path %q: %w", rel, err)
		}
		fileEntry, err := artifact.NewFileEntry(entryPath, fileMode, artifact.DigestBytes(content))
		if err != nil {
			return artifact.Bundle{}, fmt.Errorf("opencode bundle file entry %q: %w", rel, err)
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
				return artifact.Bundle{}, fmt.Errorf("opencode bundle dir path %q: %w", dir, err)
			}
			dirEntry, err := artifact.NewDirectoryEntry(dirPath, dirMode)
			if err != nil {
				return artifact.Bundle{}, fmt.Errorf("opencode bundle dir entry %q: %w", dir, err)
			}
			entries = append(entries, dirEntry)
		}
	}

	manifest, err := artifact.NewManifest(entries...)
	if err != nil {
		return artifact.Bundle{}, fmt.Errorf("opencode bundle manifest: %w", err)
	}
	return artifact.NewBundle(source, manifest)
}
