// Package antigravity binds the immutable Antigravity CLI target to its
// reviewed global filesystem layout (`~/.gemini/config/skills/`) for skills and
// explicitly marks agents and lifecycle hooks unsupported.
package antigravity

import (
	"fmt"
	"path/filepath"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/runtime"
	targetantigravity "github.com/dayvidpham/pasture/internal/target/antigravity"
)

// ActivationContractID names the Antigravity CLI activation contract. Its
// version is read from the pinned runtime contract so the id follows the
// recorded host version instead of restating it.
func ActivationContractID() string {
	return "antigravity-cli/global@" + runtime.AntigravityCLI1_5_9().Versions().Min().String()
}

var installerVersions = runtime.AntigravityCLI1_5_9().Versions()

const unsupportedAgentsReason = "Antigravity CLI does not ship generated Pasture agents; only `pasture install antigravity-cli skills` (or `pasture install agy skills`) is supported"

// NewActivationContract binds Antigravity CLI skills beneath one absolute home
// root (`.gemini/config/skills/`) and marks agents and hooks unsupported.
func NewActivationContract(target targetantigravity.TargetDescriptor, home string) (activation.ActivationContract, error) {
	if !target.IsValid() || target.Harness() != ir.HarnessAntigravityCLI {
		return activation.ActivationContract{}, fmt.Errorf("Antigravity CLI activation contract rejected an invalid target descriptor before host validation: rebuild it with target/antigravity.Descriptor and retry")
	}
	if target.RuntimeContractID() != runtime.AntigravityCLI1_5_9().ID() {
		return activation.ActivationContract{}, fmt.Errorf("Antigravity CLI activation contract rejected target runtime %q before host validation: generated artifacts must remain compiled for %s", target.RuntimeContractID(), runtime.AntigravityCLI1_5_9().ID())
	}
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return activation.ActivationContract{}, fmt.Errorf("Antigravity CLI activation contract construction failed: home root %q is empty, relative, or unclean; pass one canonical absolute user home directory", home)
	}

	skills := target.Skills()
	if err := targetantigravity.ValidateComponentLayout(skills); err != nil {
		return activation.ActivationContract{}, fmt.Errorf("validate immutable Antigravity CLI skills layout before activation: %w", err)
	}
	skillsStrategy, err := activation.NewDirectFile(skills.Bundle(), home)
	if err != nil {
		return activation.ActivationContract{}, fmt.Errorf("bind Antigravity CLI skills direct-file strategy at %q: %w", home, err)
	}
	skillsCell, err := cell.New(artifact.HarnessAntigravityCLI, artifact.ExtensionSkills)
	if err != nil {
		return activation.ActivationContract{}, err
	}
	skillsAct, err := activation.NewComponentActivation(skillsCell, skillsStrategy)
	if err != nil {
		return activation.ActivationContract{}, fmt.Errorf("bind Antigravity CLI skills component activation: %w", err)
	}

	agentsStrategy, err := activation.NewUnsupported(unsupportedAgentsReason)
	if err != nil {
		return activation.ActivationContract{}, err
	}
	agentsCell, err := cell.New(artifact.HarnessAntigravityCLI, artifact.ExtensionAgents)
	if err != nil {
		return activation.ActivationContract{}, err
	}
	agentsAct, err := activation.NewComponentActivation(agentsCell, agentsStrategy)
	if err != nil {
		return activation.ActivationContract{}, fmt.Errorf("bind Antigravity CLI agents component activation: %w", err)
	}

	hooksStrategy, err := activation.NewUnsupported(runtime.AntigravityLifecycleContract().Error())
	if err != nil {
		return activation.ActivationContract{}, err
	}
	hooksCell, err := cell.New(artifact.HarnessAntigravityCLI, artifact.ExtensionHooks)
	if err != nil {
		return activation.ActivationContract{}, err
	}
	hooksAct, err := activation.NewComponentActivation(hooksCell, hooksStrategy)
	if err != nil {
		return activation.ActivationContract{}, fmt.Errorf("bind Antigravity CLI hooks component activation: %w", err)
	}

	exhaustive, err := activation.NewExhaustiveComponentActivations(skillsAct, agentsAct, hooksAct)
	if err != nil {
		return activation.ActivationContract{}, fmt.Errorf("assemble exhaustive Antigravity CLI activations: %w", err)
	}
	id, err := activation.NewActivationContractID(ActivationContractID())
	if err != nil {
		return activation.ActivationContract{}, err
	}
	probe, err := activation.NewCommandSchema("agy", "--version")
	if err != nil {
		return activation.ActivationContract{}, err
	}
	return activation.NewActivationContract(id, ir.HarnessAntigravityCLI, installerVersions, probe, exhaustive)
}
