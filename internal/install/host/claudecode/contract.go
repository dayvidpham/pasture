// Package claudecode binds the immutable Claude Code target to the reviewed
// global native-manager contract and implements its ownership-safe controller.
package claudecode

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/runtime"
	target "github.com/dayvidpham/pasture/internal/target/claudecode"
)

const (
	MarketplaceName = "aura-plugins"
	MarketplaceRepo = "dayvidpham/aura-plugins"
	LegacyPackage   = "pasture"
	LegacyVersion   = "0.0.4"
)

// Contract is the complete static Claude activation contract. Its admission is
// the target runtime contract's own: a floor at the recorded Claude Code
// version, read from that contract and never restated here.
func Contract(descriptor target.TargetDescriptor) (activation.ActivationContract, error) {
	if !descriptor.IsValid() || descriptor.Harness() != ir.HarnessClaudeCode {
		return activation.ActivationContract{}, fault("Claude activation contract construction", "valid Claude target descriptor", "the target descriptor is zero, invalid, or belongs to another harness", "Contract", "binding immutable Claude components", "the installer cannot prove which artifacts it would activate", "pass claudecode.Descriptor() from the target package", nil)
	}
	runtimeContract := runtime.ClaudeCode2_1_261()
	if descriptor.RuntimeContractID() != runtimeContract.ID() {
		return activation.ActivationContract{}, fault("Claude activation contract construction", "matching reviewed runtime identity", fmt.Sprintf("target runtime %s differs from reviewed runtime %s", descriptor.RuntimeContractID(), runtimeContract.ID()), "Contract", "binding host compatibility", "manager behavior could be applied outside the artifact's reviewed contract", "rebuild the target and activation contract from the same release", nil)
	}
	components := descriptor.Components()
	acts := make([]activation.ComponentActivation, 0, len(components))
	for _, component := range components {
		act, err := componentActivation(component)
		if err != nil {
			return activation.ActivationContract{}, err
		}
		acts = append(acts, act)
	}
	exhaustive, err := activation.NewExhaustiveComponentActivations(acts[0], acts[1], acts[2])
	if err != nil {
		return activation.ActivationContract{}, err
	}
	// The native-plugin activation id names the MAJOR.MINOR family of the
	// recorded Claude Code version, read from the runtime contract.
	major, minor, _ := runtimeContract.Versions().Min().Release()
	id, err := activation.NewActivationContractID(fmt.Sprintf("claude-code/native-plugins@%d.%d", major, minor))
	if err != nil {
		return activation.ActivationContract{}, err
	}
	probe := command("claude", "--version")
	return activation.NewActivationContract(id, ir.HarnessClaudeCode, runtimeContract.Versions(), probe, exhaustive)
}

func componentActivation(component target.Component) (activation.ComponentActivation, error) {
	if !component.IsValid() {
		return activation.ComponentActivation{}, fault("Claude component binding", "valid immutable target component", "a target component is invalid", "componentActivation", "assembling static activation", "one Claude cell would be unbound", "use the three components returned by the target descriptor", nil)
	}
	name, version, err := componentManifest(component)
	if err != nil {
		return activation.ComponentActivation{}, err
	}
	wantName := packageFor(component.Extension())
	if name != wantName {
		return activation.ComponentActivation{}, fault("Claude component binding", "manifest name matches typed component", fmt.Sprintf("component %s contains plugin name %q instead of %q", component.ID(), name, wantName), "componentActivation", "validating immutable package identity", "the native selector and artifact bytes disagree", "regenerate the target with the matching plugin manifest", nil)
	}
	if version == "" {
		return activation.ComponentActivation{}, fault("Claude component binding", "versioned plugin manifest", fmt.Sprintf("component %s has no version", component.ID()), "componentActivation", "validating immutable package identity", "post-install release identity could not be proved", "publish a non-empty semantic plugin version", nil)
	}
	pkg, err := activation.NewNativePlugin(name,
		command("claude", "plugin", "marketplace", "list", "--json"),
		command("claude", "plugin", "marketplace", "add", MarketplaceRepo, "--scope", "user"),
		command("claude", "plugin", "marketplace", "update", MarketplaceName),
		command("claude", "plugin", "list", "--available", "--json"),
		command("claude", "plugin", "install", selector(name), "--scope", "user"),
		command("claude", "plugin", "update", selector(name), "--scope", "user"),
		command("claude", "plugin", "uninstall", selector(name), "--scope", "user"),
	)
	if err != nil {
		return activation.ComponentActivation{}, err
	}
	c, err := cell.New(artifact.HarnessClaudeCode, component.Extension())
	if err != nil {
		return activation.ComponentActivation{}, err
	}
	return activation.NewComponentActivation(c, pkg)
}

func componentManifest(component target.Component) (string, string, error) {
	f, err := component.Bundle().Open(".claude-plugin/plugin.json")
	if err != nil {
		return "", "", fault("Claude component manifest read", "embedded .claude-plugin/plugin.json", err.Error(), component.ID().String(), "binding immutable package identity", "the package cannot be selected or verified", "regenerate the target bundle with its plugin manifest", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return "", "", fault("Claude component manifest read", "readable bounded manifest", err.Error(), component.ID().String(), "binding immutable package identity", "the package cannot be selected or verified", "regenerate the target bundle with a readable manifest", err)
	}
	var wire struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return "", "", fault("Claude component manifest decode", "valid plugin JSON", err.Error(), component.ID().String(), "binding immutable package identity", "the package cannot be selected or verified", "regenerate the target bundle with valid plugin JSON", err)
	}
	return wire.Name, wire.Version, nil
}

func packageFor(extension artifact.Extension) string {
	switch extension {
	case artifact.ExtensionSkills:
		return "pasture-skills"
	case artifact.ExtensionAgents:
		return "pasture-agents"
	case artifact.ExtensionHooks:
		return "pasture-hooks"
	default:
		return ""
	}
}

func selector(pkg string) string { return pkg + "@" + MarketplaceName }

func command(program string, args ...string) activation.CommandSchema {
	cmd, err := activation.NewCommandSchema(program, args...)
	if err != nil {
		panic(err)
	}
	return cmd
}
