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

const ActivationContractID = "codex/global@0.146.0"

// NewActivationContract binds all three independent Codex packages beneath one
// absolute home root. Their immutable bundle paths retain the public native
// `.agents/skills`, `.codex/agents`, and `.codex/hooks` layouts.
func NewActivationContract(target targetcodex.TargetDescriptor, home string) (activation.ActivationContract, error) {
	if !target.IsValid() || target.Harness() != ir.HarnessCodex || target.RuntimeContractID() != runtime.Codex0_146_0().ID() {
		return activation.ActivationContract{}, fmt.Errorf("Codex activation contract construction failed: target descriptor is invalid or was not compiled for %s; rebuild it with target/codex.Descriptor", runtime.Codex0_146_0().ID())
	}
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return activation.ActivationContract{}, fmt.Errorf("Codex activation contract construction failed: home root %q is empty, relative, or unclean; pass one canonical absolute user home directory", home)
	}
	activations := make([]activation.ComponentActivation, 0, 3)
	for _, component := range target.Components() {
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
	id, err := activation.NewActivationContractID(ActivationContractID)
	if err != nil {
		return activation.ActivationContract{}, err
	}
	probe, err := activation.NewCommandSchema("codex", "--version")
	if err != nil {
		return activation.ActivationContract{}, err
	}
	return activation.NewActivationContract(id, ir.HarnessCodex, runtime.Codex0_146_0().Versions(), probe, exhaustive)
}
