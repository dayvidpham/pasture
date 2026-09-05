// Package opencode binds the generated OpenCode target to its documented global
// direct-discovery layout. It does not invoke OpenCode's native plugin writer or
// edit any OpenCode configuration file.
package opencode

import (
	"fmt"
	"path/filepath"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/runtime"
	target "github.com/dayvidpham/pasture/internal/target/opencode"
)

const (
	SkillsDirectory = "skills"
	AgentsDirectory = "agent"
	HooksDirectory  = "plugins"
	HookFile        = "pasture-hooks.ts"
)

// ConfigFile is an OpenCode global configuration filename. These facts are
// informational for callers and tests; this controller never mutates them.
type ConfigFile string

const (
	LegacyConfigJSON ConfigFile = "config.json"
	OpenCodeJSON     ConfigFile = "opencode.json"
	OpenCodeJSONC    ConfigFile = "opencode.jsonc"
)

// DiscoveryFacts freezes the direct-discovery and native-writer facts for the
// pinned OpenCode host without granting a configuration mutation API.
type DiscoveryFacts struct {
	readOrder        [3]ConfigFile
	nativeWriteOrder [2]ConfigFile
}

func DirectDiscoveryFacts() DiscoveryFacts {
	return DiscoveryFacts{
		readOrder:        [3]ConfigFile{LegacyConfigJSON, OpenCodeJSON, OpenCodeJSONC},
		nativeWriteOrder: [2]ConfigFile{OpenCodeJSON, OpenCodeJSONC},
	}
}
func (f DiscoveryFacts) ReadOrder() []ConfigFile { return append([]ConfigFile(nil), f.readOrder[:]...) }
func (f DiscoveryFacts) NativeWriterOrder() []ConfigFile {
	return append([]ConfigFile(nil), f.nativeWriteOrder[:]...)
}

// Controller is an immutable OpenCode global activation binding.
type Controller struct {
	root       string
	descriptor target.TargetDescriptor
	contract   activation.ActivationContract
}

// New validates an absolute global config root and binds all three generated
// cells to independent direct-file destinations below it.
func New(configRoot string) (Controller, error) {
	if configRoot == "" || !filepath.IsAbs(configRoot) || filepath.Clean(configRoot) != configRoot {
		return Controller{}, fmt.Errorf("opencode.New: global config root %q is empty, relative, or unclean — resolve the user's absolute OpenCode config directory (for example ~/.config/opencode), clean it once, and retry", configRoot)
	}
	descriptor, err := target.Descriptor()
	if err != nil {
		return Controller{}, fmt.Errorf("opencode.New: construct embedded OpenCode target before binding global paths: %w", err)
	}
	componentActivation := func(component target.Component, directory string) (activation.ComponentActivation, error) {
		coordinate, err := cell.New(artifact.HarnessOpenCode, component.Extension())
		if err != nil {
			return activation.ComponentActivation{}, err
		}
		strategy, err := activation.NewDirectFile(component.Bundle(), filepath.Join(configRoot, directory))
		if err != nil {
			return activation.ComponentActivation{}, err
		}
		return activation.NewComponentActivation(coordinate, strategy)
	}
	skills, err := componentActivation(descriptor.Skills(), SkillsDirectory)
	if err != nil {
		return Controller{}, fmt.Errorf("opencode.New: bind skills destination: %w", err)
	}
	agents, err := componentActivation(descriptor.Agents(), AgentsDirectory)
	if err != nil {
		return Controller{}, fmt.Errorf("opencode.New: bind agents destination: %w", err)
	}
	hooks, err := componentActivation(descriptor.Hooks(), HooksDirectory)
	if err != nil {
		return Controller{}, fmt.Errorf("opencode.New: bind hooks destination: %w", err)
	}
	exhaustive, err := activation.NewExhaustiveComponentActivations(skills, agents, hooks)
	if err != nil {
		return Controller{}, fmt.Errorf("opencode.New: assemble exhaustive direct-file cells: %w", err)
	}
	// The id's version is read from the OpenCode runtime contract, the one
	// root, so it follows the recorded host version instead of restating it.
	id, err := activation.NewActivationContractID("opencode/activation@" + runtime.OpenCode1_18_10().Versions().Min().String())
	if err != nil {
		return Controller{}, err
	}
	probe, err := activation.NewCommandSchema("opencode", "--version")
	if err != nil {
		return Controller{}, err
	}
	contract, err := activation.NewActivationContract(id, descriptor.RuntimeContractID().Harness(), runtime.OpenCode1_18_10().Versions(), probe, exhaustive)
	if err != nil {
		return Controller{}, fmt.Errorf("opencode.New: construct activation contract: %w", err)
	}
	return Controller{root: configRoot, descriptor: descriptor, contract: contract}, nil
}

func (c Controller) ConfigRoot() string                      { return c.root }
func (c Controller) Descriptor() target.TargetDescriptor     { return c.descriptor }
func (c Controller) Contract() activation.ActivationContract { return c.contract }

// Destination returns the exact global discovery root for one extension.
func (c Controller) Destination(extension artifact.Extension) (string, error) {
	switch extension {
	case artifact.ExtensionSkills:
		return filepath.Join(c.root, SkillsDirectory), nil
	case artifact.ExtensionAgents:
		return filepath.Join(c.root, AgentsDirectory), nil
	case artifact.ExtensionHooks:
		return filepath.Join(c.root, HooksDirectory), nil
	default:
		return "", fmt.Errorf("opencode.Controller.Destination: extension %q is unknown — request skills, agents, or hooks", extension)
	}
}
