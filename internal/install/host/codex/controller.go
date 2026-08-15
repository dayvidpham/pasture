package codex

import (
	"fmt"
	"path/filepath"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
	targetcodex "github.com/dayvidpham/pasture/internal/target/codex"
)

const pendingTrustDiagnostic = "Codex hook artifacts are installed but execution is not claimed: review and approve them through Codex's native hooks interface; Pasture does not read or modify private trust state"

// NewDirectFilePolicies constructs exactly one policy for each global Codex
// cell. The returned array is intended for the one central DirectFile activator;
// this package never owns a filesystem activator.
func NewDirectFilePolicies(target targetcodex.TargetDescriptor, home string) ([3]apply.DirectFilePolicy, error) {
	var policies [3]apply.DirectFilePolicy
	if !target.IsValid() || target.Harness() != ir.HarnessCodex {
		return policies, fmt.Errorf("Codex direct-file policy construction rejected an invalid target before filesystem access; rebuild the embedded Codex descriptor and retry")
	}
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return policies, fmt.Errorf("Codex direct-file policy construction requires one canonical absolute global home, got %q; resolve and clean the user home before composing the installer", home)
	}
	components := target.Components()
	if len(components) != len(policies) {
		return policies, fmt.Errorf("Codex direct-file policy construction found %d target components, expected exactly three; regenerate the complete descriptor", len(components))
	}
	for index, component := range components {
		if err := targetcodex.ValidateComponentLayout(component); err != nil {
			return policies, fmt.Errorf("validate immutable Codex %s policy layout: %w", component.Extension(), err)
		}
		coordinate, err := cell.New(artifact.HarnessCodex, component.Extension())
		if err != nil {
			return policies, err
		}
		validate := exactGlobalValidator(coordinate, component.Bundle().ID(), home)
		if component.Extension() == artifact.ExtensionHooks {
			policies[index], err = apply.PendingNativeTrustDirectFile(coordinate, validate, pendingTrustDiagnostic)
		} else {
			policies[index], err = apply.NewDirectFilePolicy(coordinate, validate, apply.PassThroughDecoration())
		}
		if err != nil {
			return [3]apply.DirectFilePolicy{}, fmt.Errorf("construct Codex %s direct-file policy: %w", component.Extension(), err)
		}
	}
	return policies, nil
}

func exactGlobalValidator(expected cell.Cell, bundle artifact.BundleID, destination string) apply.DirectFileValidator {
	return func(request apply.DirectFileRequest) error {
		if request.Cell() != expected || request.Key().Cell() != expected || request.Key().Scope() != registry.ScopeGlobal {
			return fmt.Errorf("Codex global policy rejected key %s for activation cell %s before mutation; use the exact global key for %s", request.Key().Cell(), request.Cell(), expected)
		}
		if request.StrategyKind() != activation.DirectFileKindValue() || request.ArtifactID() != bundle || request.DestinationRoot() != destination {
			return fmt.Errorf("Codex global policy rejected %s before mutation because its strategy, immutable bundle, or destination differs from the reviewed contract; rebuild the activation and policy from the same descriptor and home", expected)
		}
		return nil
	}
}
