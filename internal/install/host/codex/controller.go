package codex

import (
	"context"
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

const pendingTrustDiagnostic = "Codex hook artifacts are installed but execution is not claimed: review and approve them through Codex's native hooks interface; Pasture does not read or modify private trust state"

// Controller is the sole direct-file activator. It delegates conservative file
// ownership to the accepted controller and adds only Codex-specific layout and
// pending-trust facts. Non-Codex direct-file cells pass through unchanged.
type Controller struct{ direct apply.DirectFileActivator }

func NewController() Controller                          { return Controller{direct: apply.NewDirectFileActivator()} }
func (Controller) StrategyKind() activation.StrategyKind { return activation.DirectFileKindValue() }

func (c Controller) Inspect(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	if err := validateRequest(key, act); err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	out, err := c.direct.Inspect(ctx, source, key, act, prior)
	return c.decorate(key, out, err)
}

func (c Controller) Ensure(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	if err := validateRequest(key, act); err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	out, err := c.direct.Ensure(ctx, source, key, act, prior)
	return c.decorate(key, out, err)
}

func (c Controller) Remove(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior registry.Record) (apply.Outcome, error) {
	if err := validateRequest(key, act); err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	out, err := c.direct.Remove(ctx, source, key, act, prior)
	return c.decorate(key, out, err)
}

func validateRequest(key registry.Key, act activation.ComponentActivation) error {
	if key.Cell() != act.Cell() {
		return fmt.Errorf("direct-file request rejected before mutation: registry key cell %s does not match activation cell %s; rebuild the scoped request from the same typed cell", key.Cell(), act.Cell())
	}
	if act.Cell().Harness() == artifact.HarnessCodex && key.Scope() != registry.ScopeGlobal {
		return fmt.Errorf("Codex global controller rejected project-scoped key for %s before mutation: project installation has a separate controller; use registry.GlobalKey for this global activation", act.Cell())
	}
	return validateCodexLayout(act)
}

func validateCodexLayout(act activation.ComponentActivation) error {
	if act.Cell().Harness() != artifact.HarnessCodex {
		return nil
	}
	direct, ok := act.Strategy().(activation.DirectFile)
	if !ok || !direct.IsValid() {
		return fmt.Errorf("Codex direct-file dispatch failed for %s: activation does not contain a valid immutable direct-file strategy; bind the target with codex.NewActivationContract", act.Cell())
	}
	prefix := map[artifact.Extension]string{
		artifact.ExtensionSkills: ".agents/skills/",
		artifact.ExtensionAgents: ".codex/agents/",
		artifact.ExtensionHooks:  ".codex/hooks/",
	}[act.Cell().Extension()]
	hookPublicFile := map[string]bool{
		".codex/hooks.json":                    true,
		".codex/pasture-codex-activation.json": true,
	}
	regular := 0
	for _, entry := range direct.Bundle().Manifest().Entries() {
		if !entry.IsRegular() {
			continue
		}
		regular++
		if !strings.HasPrefix(entry.Path().String(), prefix) && !(act.Cell().Extension() == artifact.ExtensionHooks && hookPublicFile[entry.Path().String()]) {
			return fmt.Errorf("Codex %s bundle rejected before mutation: leaf %q is outside public layout %q and could enter a sibling component; regenerate the immutable target and retry", act.Cell().Extension(), entry.Path(), prefix)
		}
	}
	if regular == 0 {
		return fmt.Errorf("Codex %s bundle rejected before mutation: no regular artifact exists below %q; regenerate the complete immutable target", act.Cell().Extension(), prefix)
	}
	return nil
}

func (c Controller) decorate(key registry.Key, out apply.Outcome, actionErr error) (apply.Outcome, error) {
	if key.Cell().Harness() != artifact.HarnessCodex || key.Cell().Extension() != artifact.ExtensionHooks || out.Record == nil {
		return out, actionErr
	}
	record := *out.Record
	trust := registry.TrustNotApplicable
	diagnostic := record.Diagnostic()
	if record.Observation() == registry.ObservationInstalled {
		trust = registry.TrustPending
		out.Status = apply.InstalledPendingTrust()
		if diagnostic == "" {
			diagnostic = pendingTrustDiagnostic
		} else {
			diagnostic += "; " + pendingTrustDiagnostic
		}
		out.Diagnostic = diagnostic
	}
	updated, err := registry.NewRecord(registry.RecordInput{
		Key: key, Source: record.Source(), Strategy: record.Strategy(), Managed: record.Managed(), ArtifactID: record.ArtifactID(),
		Version: record.Version(), Selector: record.Selector(), Leaves: record.Leaves(), CreatedDirs: record.CreatedDirs(), SharedConfig: record.SharedConfig(),
		Observation: record.Observation(), Trust: trust, LastOperation: record.LastOperation(), LastOutcome: record.LastOutcome(), Diagnostic: diagnostic,
	})
	if err != nil {
		return apply.Outcome{Status: apply.Failed(), Observation: out.Observation}, fmt.Errorf("Codex hook trust fact could not be constructed after filesystem inspection: %w; hook activation is not claimed and the caller must inspect status before retrying", err)
	}
	out.Record = &updated
	return out, actionErr
}
