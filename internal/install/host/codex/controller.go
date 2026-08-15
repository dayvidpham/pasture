package codex

import (
	"fmt"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

const pendingTrustDiagnostic = "Codex hook artifacts are installed but execution is not claimed: review and approve them through Codex's native hooks interface; Pasture does not read or modify private trust state"

// Controller exposes Codex-specific validation and factual trust policy only.
// It intentionally does not implement apply.Activator or claim the strategy-wide
// DirectFile slot. Frontend composition registers one generic
// apply.DirectFileActivator centrally and applies this policy around Codex cells.
type Controller struct{}

func NewController() Controller { return Controller{} }

// Validate checks the Codex scope contract before the generic activator runs.
func (Controller) Validate(key registry.Key, act activation.ComponentActivation) error {
	return validateRequest(key, act)
}

// Decorate adds Codex's observable pending-trust fact after generic filesystem
// execution. It never reads or writes private host trust state.
func (c Controller) Decorate(key registry.Key, out apply.Outcome, actionErr error) (apply.Outcome, error) {
	return c.decorate(key, out, actionErr)
}

func validateRequest(key registry.Key, act activation.ComponentActivation) error {
	if key.Cell() != act.Cell() {
		return fmt.Errorf("direct-file request rejected before mutation: registry key cell %s does not match activation cell %s; rebuild the scoped request from the same typed cell", key.Cell(), act.Cell())
	}
	if act.Cell().Harness() != artifact.HarnessCodex {
		return fmt.Errorf("Codex controller rejected non-Codex cell %s before mutation; dispatch this cell through its owning harness controller", act.Cell())
	}
	if act.Cell().Harness() == artifact.HarnessCodex && key.Scope() != registry.ScopeGlobal {
		return fmt.Errorf("Codex global controller rejected project-scoped key for %s before mutation: project installation has a separate controller; use registry.GlobalKey for this global activation", act.Cell())
	}
	direct, ok := act.Strategy().(activation.DirectFile)
	if !ok || !direct.IsValid() {
		return fmt.Errorf("Codex policy rejected %s before mutation: activation does not contain a valid immutable direct-file strategy; bind the target with codex.NewActivationContract", act.Cell())
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
