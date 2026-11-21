package antigravity

import (
	"fmt"
	"path/filepath"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
	targetantigravity "github.com/dayvidpham/pasture/internal/target/antigravity"
)

// NewDirectFilePolicy constructs the direct-file policy for the Antigravity CLI
// skills cell. Agents and hooks are bound as Unsupported and do not use
// DirectFile activation.
func NewDirectFilePolicy(target targetantigravity.TargetDescriptor, home string) (apply.DirectFilePolicy, error) {
	if !target.IsValid() || target.Harness() != ir.HarnessAntigravityCLI {
		return apply.DirectFilePolicy{}, fmt.Errorf("Antigravity CLI direct-file policy construction rejected an invalid target before filesystem access; rebuild the embedded Antigravity CLI descriptor and retry")
	}
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return apply.DirectFilePolicy{}, fmt.Errorf("Antigravity CLI direct-file policy construction requires one canonical absolute global home, got %q", home)
	}
	skills := target.Skills()
	if err := targetantigravity.ValidateComponentLayout(skills); err != nil {
		return apply.DirectFilePolicy{}, fmt.Errorf("validate immutable Antigravity CLI skills policy layout: %w", err)
	}
	coordinate, err := cell.New(artifact.HarnessAntigravityCLI, artifact.ExtensionSkills)
	if err != nil {
		return apply.DirectFilePolicy{}, err
	}
	validate := exactGlobalValidator(coordinate, skills.Bundle().ID(), home)
	policy, err := apply.NewDirectFilePolicy(coordinate, validate, apply.PassThroughDecoration())
	if err != nil {
		return apply.DirectFilePolicy{}, fmt.Errorf("construct Antigravity CLI skills direct-file policy: %w", err)
	}
	return policy, nil
}

func exactGlobalValidator(expected cell.Cell, bundle artifact.BundleID, destination string) apply.DirectFileValidator {
	return func(request apply.DirectFileRequest) error {
		if request.Cell() != expected || request.Key().Cell() != expected || request.Key().Scope() != registry.ScopeGlobal {
			return fmt.Errorf("Antigravity CLI global policy rejected key %s for activation cell %s before mutation; use the exact global key for %s", request.Key().Cell(), request.Cell(), expected)
		}
		if request.StrategyKind() != activation.DirectFileKindValue() || request.ArtifactID() != bundle || request.DestinationRoot() != destination {
			return fmt.Errorf("Antigravity CLI global policy rejected %s before mutation because its strategy, immutable bundle, or destination differs from the reviewed contract", expected)
		}
		return nil
	}
}
