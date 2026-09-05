// Package codex binds the immutable Codex target to its reviewed global
// filesystem layout and factual pending-trust reporting.
package codex

import (
	"fmt"
	"path/filepath"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/runtime"
	targetcodex "github.com/dayvidpham/pasture/internal/target/codex"
)

// ActivationContractID names the Codex activation contract. Its version is
// read from the Codex runtime contract, the one root, so the id follows the
// recorded host version instead of restating it.
func ActivationContractID() string {
	return "codex/global@" + runtime.Codex0_153_0().Versions().Min().String()
}

// installerVersions is the Codex runtime contract's own admission: a floor at
// the recorded host version. The generated runner evidence was captured at that
// version; a host at or above it is admitted and a host below it is refused.
var installerVersions = runtime.Codex0_153_0().Versions()

// NewActivationContract binds all three independent Codex packages beneath one
// absolute home root. Their immutable bundle paths retain the public native
// `.agents/skills`, `.codex/agents`, and `.codex/hooks` layouts.
func NewActivationContract(target targetcodex.TargetDescriptor, home string) (activation.ActivationContract, error) {
	if !target.IsValid() || target.Harness() != ir.HarnessCodex {
		return activation.ActivationContract{}, fmt.Errorf("Codex activation contract rejected an invalid target descriptor before host validation: the descriptor is zero, malformed, or belongs to another harness; rebuild it with target/codex.Descriptor and retry")
	}
	if target.RuntimeContractID() != runtime.Codex0_153_0().ID() {
		return activation.ActivationContract{}, fmt.Errorf("Codex activation contract rejected target runtime %q before host validation: generated artifacts must remain exactly compiled for %s; regenerate target/codex and review the runtime contract change instead of widening installer compatibility", target.RuntimeContractID(), runtime.Codex0_153_0().ID())
	}
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return activation.ActivationContract{}, fmt.Errorf("Codex activation contract construction failed: home root %q is empty, relative, or unclean; pass one canonical absolute user home directory", home)
	}
	activations := make([]activation.ComponentActivation, 0, 3)
	for _, component := range target.Components() {
		if err := targetcodex.ValidateComponentLayout(component); err != nil {
			return activation.ActivationContract{}, fmt.Errorf("validate immutable Codex %s layout before activation: %w", component.Extension(), err)
		}
		strategy, err := activation.NewDirectFile(component.Bundle(), home)
		if err != nil {
			return activation.ActivationContract{}, fmt.Errorf("bind Codex %s direct-file strategy at %q: %w", component.Extension(), home, err)
		}
		coordinate, err := cell.New(artifact.HarnessCodex, component.Extension())
		if err != nil {
			return activation.ActivationContract{}, err
		}
		bound, err := activation.NewComponentActivation(coordinate, strategy)
		if err != nil {
			return activation.ActivationContract{}, fmt.Errorf("bind Codex %s component activation: %w", component.Extension(), err)
		}
		activations = append(activations, bound)
	}
	exhaustive, err := activation.NewExhaustiveComponentActivations(activations[0], activations[1], activations[2])
	if err != nil {
		return activation.ActivationContract{}, fmt.Errorf("assemble exhaustive Codex activations: %w", err)
	}
	id, err := activation.NewActivationContractID(ActivationContractID())
	if err != nil {
		return activation.ActivationContract{}, err
	}
	probe, err := activation.NewCommandSchema("codex", "--version")
	if err != nil {
		return activation.ActivationContract{}, err
	}
	return activation.NewActivationContract(id, ir.HarnessCodex, installerVersions, probe, exhaustive)
}
