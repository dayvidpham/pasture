package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/service"
	"github.com/dayvidpham/pasture/internal/runtime"
	target "github.com/dayvidpham/pasture/internal/target/claudecode"
)

type nativeSnapshot struct {
	marketplace bool
	plugins     map[cell.Cell]pluginRow
	legacy      *pluginRow
}

type probeUnavailableError struct{ cause error }

func (e *probeUnavailableError) Error() string { return e.cause.Error() }
func (e *probeUnavailableError) Unwrap() error { return e.cause }

// Controller is stateless. Every plan and action is derived from immutable
// bindings, the supplied registry facts, and a fresh complete native probe.
type Controller struct {
	runner    Runner
	manifests ManifestReader
	artifacts map[cell.Cell]artifact.BundleID
	versions  map[cell.Cell]registry.Version
}

func NewController(runner Runner, manifests ManifestReader) (*Controller, error) {
	if runner == nil {
		return nil, fault("Claude controller construction", "non-nil native runner", "the runner is nil", "NewController", "wiring Claude activation", "no reviewed command can execute", "provide OSRunner in production", nil)
	}
	if manifests == nil {
		return nil, fault("Claude controller construction", "non-nil manifest reader", "the manifest reader is nil", "NewController", "wiring versionless-row verification", "selected release identity could be guessed", "provide OSManifestReader in production", nil)
	}
	descriptor, err := target.Descriptor()
	if err != nil {
		return nil, fault("Claude controller construction", "immutable embedded target descriptor", err.Error(), "NewController", "wiring immutable component bindings", "native actions cannot be tied to shipped bytes", "regenerate and rebuild the embedded Claude target", err)
	}
	artifacts := make(map[cell.Cell]artifact.BundleID, 3)
	versions := make(map[cell.Cell]registry.Version, 3)
	for _, component := range descriptor.Components() {
		cc, cellErr := cell.New(ir.HarnessClaudeCode, component.Extension())
		if cellErr != nil {
			return nil, cellErr
		}
		_, versionText, manifestErr := componentManifest(component)
		if manifestErr != nil {
			return nil, manifestErr
		}
		version, versionErr := registry.NewVersion(versionText)
		if versionErr != nil {
			return nil, versionErr
		}
		artifacts[cc] = component.Bundle().ID()
		versions[cc] = version
	}
	return &Controller{runner: runner, manifests: manifests, artifacts: artifacts, versions: versions}, nil
}

func (c *Controller) Harness() ir.HarnessID { return ir.HarnessClaudeCode }
func (c *Controller) StrategyKind() activation.StrategyKind {
	return activation.NativePluginKindValue()
}

func (c *Controller) PlanSelection(ctx context.Context, request service.GroupSelection) (service.GroupPlan, error) {
	if request.Scope.Kind() != registry.ScopeGlobal {
		return service.GroupPlan{}, nil
	}
	if !request.Source.IsValid() {
		return service.GroupPlan{}, fault("Claude group planning", "valid controller source", "the source is invalid", "Controller.PlanSelection", "planning exhaustive sibling intent", "ownership cannot be selected", "use installer or home-manager source", nil)
	}
	if err := c.validateRequest(request); err != nil {
		return service.GroupPlan{}, err
	}
	snapshot, probeErr := c.probe(ctx)
	if probeErr != nil {
		if !optionalSelection(request) {
			return service.GroupPlan{}, probeErr
		}
		return c.newPlan(request, nativeSnapshot{}, true)
	}
	if err := c.validatePrior(request, snapshot); err != nil {
		if !optionalSelection(request) {
			return service.GroupPlan{}, err
		}
		return c.newPlan(request, snapshot, true)
	}
	return c.newPlan(request, snapshot, false)
}

func (c *Controller) newPlan(request service.GroupSelection, snapshot nativeSnapshot, preserveOnly bool) (service.GroupPlan, error) {
	results := make([]service.GroupResultCell, 0, 3)
	actions := make([]service.GroupStep, 0, 7)
	if preserveOnly {
		skills, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
		step, err := service.NewGroupStep(service.InspectGroupAction(), skills)
		if err != nil {
			return service.GroupPlan{}, err
		}
		actions = append(actions, step)
	} else {
		for _, extension := range cell.CanonicalExtensions() {
			cc, _ := cell.New(ir.HarnessClaudeCode, extension)
			if !request.Selection.Enabled(cc) {
				continue
			}
			prior, hasPrior := request.Prior[cc]
			_, installed := snapshot.plugins[cc]
			external := installed && (!hasPrior || !prior.Managed() || prior.Observation() == registry.ObservationAbsent)
			if !external {
				step, err := service.NewGroupStep(service.EnsureCellGroupAction(), cc)
				if err != nil {
					return service.GroupPlan{}, err
				}
				actions = append(actions, step)
			}
		}
		if snapshot.legacy != nil {
			skills, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
			step, err := service.NewGroupStep(service.RemoveSharedGroupAction(), skills)
			if err != nil {
				return service.GroupPlan{}, err
			}
			actions = append(actions, step)
		}
		for _, extension := range cell.CanonicalExtensions() {
			cc, _ := cell.New(ir.HarnessClaudeCode, extension)
			if request.Selection.Enabled(cc) {
				continue
			}
			prior, hasPrior := request.Prior[cc]
			_, installed := snapshot.plugins[cc]
			// A legacy monolith authorizes removal of the monolith itself, not
			// arbitrary split plugins that may have been installed externally.
			// Only an exact, non-absent managed record can authorize removing a
			// split plugin.  This keeps migration ownership-safe when native state
			// contains both the old monolith and an external split install.
			authorized := hasPrior && prior.Managed() && prior.Observation() != registry.ObservationAbsent
			if installed && authorized {
				step, err := service.NewGroupStep(service.RemoveCellGroupAction(), cc)
				if err != nil {
					return service.GroupPlan{}, err
				}
				actions = append(actions, step)
			}
		}
		if len(actions) == 0 {
			skills, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
			step, err := service.NewGroupStep(service.InspectGroupAction(), skills)
			if err != nil {
				return service.GroupPlan{}, err
			}
			actions = append(actions, step)
		}
	}
	resultOps := map[cell.Cell]apply.Operation{}
	for _, step := range actions {
		if step.Kind() == service.EnsureCellGroupAction() || step.Kind() == service.RemoveCellGroupAction() {
			resultOps[step.ControlCell()] = step.Operation()
		}
	}
	for _, extension := range cell.CanonicalExtensions() {
		cc, _ := cell.New(ir.HarnessClaudeCode, extension)
		key, err := request.Scope.Key(cc)
		if err != nil {
			return service.GroupPlan{}, err
		}
		op := resultOps[cc]
		if !op.IsValid() {
			op = apply.Inspect()
		}
		result, err := service.NewGroupResultCell(cc, key, op)
		if err != nil {
			return service.GroupPlan{}, err
		}
		results = append(results, result)
	}
	return service.NewGroupPlan(results, actions)
}

func (c *Controller) ExecuteAction(ctx context.Context, request service.GroupSelection, plan service.GroupPlan, step service.GroupStep) error {
	if err := c.validateActionRequest(request, plan, step); err != nil {
		return err
	}
	if step.Kind() == service.InspectGroupAction() {
		return nil
	}
	snapshot, err := c.probe(ctx)
	if err != nil {
		return err
	}
	if err := c.validatePrior(request, snapshot); err != nil {
		return err
	}
	cc := step.ControlCell()
	switch step.Kind() {
	case service.EnsureCellGroupAction():
		prior, hasPrior := request.Prior[cc]
		_, installed := snapshot.plugins[cc]
		if installed && (!hasPrior || !prior.Managed() || prior.Observation() == registry.ObservationAbsent) {
			return nil
		}
		if err := c.ensureMarketplace(ctx, snapshot.marketplace); err != nil {
			return err
		}
		verb := "install"
		if installed {
			verb = "update"
		}
		_, err = c.run(ctx, command("claude", "plugin", verb, selector(packageFor(cc.Extension())), "--scope", "user"))
		return err
	case service.RemoveSharedGroupAction():
		if snapshot.legacy == nil {
			return nil
		}
		_, err = c.run(ctx, command("claude", "plugin", "uninstall", selector(LegacyPackage), "--scope", "user"))
		return err
	case service.RemoveCellGroupAction():
		if _, installed := snapshot.plugins[cc]; !installed {
			return nil
		}
		prior, hasPrior := request.Prior[cc]
		// Shared legacy removal does not confer ownership of an independently
		// installed split plugin.  Require the exact managed prior record for
		// every split uninstall, including during monolith migration.
		authorized := hasPrior && prior.Managed() && prior.Observation() != registry.ObservationAbsent
		if !authorized {
			return fault("Claude split removal", "current managed authority or exact legacy migration authority", fmt.Sprintf("cell %s is installed without removable authority", cc), "Controller.ExecuteAction", "authorizing native uninstall", "the external plugin was preserved", "remove it manually or restore an exact managed record", nil)
		}
		_, err = c.run(ctx, command("claude", "plugin", "uninstall", selector(packageFor(cc.Extension())), "--scope", "user"))
		return err
	default:
		return fault("Claude group action", "known typed action", "the action discriminator is invalid", "Controller.ExecuteAction", "executing a bounded native action", "no command was run", "rebuild the plan with typed group actions", nil)
	}
}

func (c *Controller) InspectAction(ctx context.Context, request service.GroupSelection, plan service.GroupPlan, step service.GroupStep, executeErr error) (service.GroupFacts, error) {
	if err := c.validateActionRequest(request, plan, step); err != nil {
		return service.GroupFacts{}, err
	}
	snapshot, probeErr := c.probe(ctx)
	if probeErr != nil {
		if step.Kind() == service.InspectGroupAction() && optionalSelection(request) {
			return optionalPreservationFacts(plan, probeErr)
		}
		return service.GroupFacts{}, fault("Claude post-action inspection", "complete live three-cell state", probeErr.Error(), "Controller.InspectAction", "probing after a typed group action", "no stale registry facts can be emitted", "repair the Claude binary or response and retry the full selection", errors.Join(executeErr, probeErr))
	}
	if err := c.validatePriorForInspection(request, snapshot, step); err != nil {
		return service.GroupFacts{}, err
	}
	results := plan.ResultCells()
	facts := make([]service.GroupAction, 0, 3)
	for _, result := range results {
		cc := result.Cell()
		key := result.Key()
		plugin, installed := snapshot.plugins[cc]
		prior, hasPrior := request.Prior[cc]
		managed := hasPrior && prior.Managed() && prior.Observation() != registry.ObservationAbsent
		if step.Kind() == service.EnsureCellGroupAction() && step.ControlCell() == cc && installed {
			managed = true
		}
		observation := registry.ObservationAbsent
		management := apply.ManagementExternal
		diagnostic := "exact split plugin is absent"
		if installed {
			observation = registry.ObservationInstalled
			management = apply.ManagementExternal
			diagnostic = fmt.Sprintf("exact plugin %s is installed and remains externally owned", plugin.ID)
			if managed {
				management = apply.ManagementPasture
				diagnostic = fmt.Sprintf("exact managed plugin %s is installed", plugin.ID)
			}
		} else if managed || hasPrior {
			management = apply.ManagementPasture
			if hasPrior && !prior.Managed() {
				management = apply.ManagementExternal
			}
		}
		isControl := cc == step.ControlCell()
		status := apply.Completed()
		outcome := registry.OutcomeCompleted
		lastOperation := registry.OperationInspect
		if isControl {
			lastOperation = registryOperation(step.Operation())
			if executeErr != nil {
				status = apply.Failed()
				outcome = registry.OutcomeFailed
				diagnostic = fmt.Sprintf("typed Claude action failed; strongest live state was retained: %v", executeErr)
			}
		}
		var record *registry.Record
		if installed || hasPrior || managed {
			record = c.observedRecord(key, request.Activation[cc], request.Source, management == apply.ManagementPasture, lastOperation, outcome, observation, diagnostic)
			if record == nil {
				return service.GroupFacts{}, fault("Claude fact construction", "valid immutable registry record", fmt.Sprintf("record construction failed for %s", cc), "Controller.InspectAction", "building complete live group facts", "the service cannot persist the post-action state", "repair immutable binding or registry identity construction", nil)
			}
		}
		action, err := service.NewGroupAction(apply.NewActionRow(cc, result.Operation(), status, management, observation, diagnostic), record)
		if err != nil {
			return service.GroupFacts{}, err
		}
		facts = append(facts, action)
	}
	return service.NewGroupFacts(facts...)
}

func optionalPreservationFacts(plan service.GroupPlan, cause error) (service.GroupFacts, error) {
	actions := make([]service.GroupAction, 0, 3)
	for _, result := range plan.ResultCells() {
		diagnostic := fmt.Sprintf("optional all-false Claude probe was unavailable or ambiguous; no state was claimed or mutated: %v", cause)
		action, err := service.NewGroupAction(apply.NewActionRow(result.Cell(), result.Operation(), apply.Completed(), apply.ManagementExternal, registry.ObservationUnknown, diagnostic), nil)
		if err != nil {
			return service.GroupFacts{}, err
		}
		actions = append(actions, action)
	}
	return service.NewGroupFacts(actions...)
}

func (c *Controller) ClosePlan(ctx context.Context, request service.GroupSelection, plan service.GroupPlan, stage service.GroupTerminalStage) error {
	if err := ctx.Err(); err != nil {
		return fault("Claude plan closure", "live bounded cleanup context", err.Error(), "Controller.ClosePlan", "closing a terminal stateless plan", "closure was not acknowledged", "retry the full selection with a live context", err)
	}
	if !stage.IsValid() || !plan.Handled() || request.Scope.Kind() != registry.ScopeGlobal {
		return fault("Claude plan closure", "valid terminal stage and handled global plan", "the closure request is malformed", "Controller.ClosePlan", "closing a terminal stateless plan", "the terminal path cannot be confirmed", "pass the original handled plan and typed terminal stage", nil)
	}
	return nil
}

func (c *Controller) PreflightCell(ctx context.Context, request service.GroupCell) error {
	if request.Scope.Kind() != registry.ScopeGlobal {
		return nil
	}
	if err := validateActivations(request.Activation); err != nil {
		return err
	}
	snapshot, err := c.probe(ctx)
	if err != nil {
		return err
	}
	if snapshot.legacy != nil {
		return fault("Claude cell preflight", "split-only native state", "the exact v0.0.4 monolith requires all three sibling choices", "Controller.PreflightCell", "validating a context-free cell request", "no mutation was attempted because sibling intent is unavailable", "rerun `pasture install` or apply-selection with an exhaustive desired document", nil)
	}
	selection := service.GroupSelection{Scope: request.Scope, Source: request.Source, Prior: request.Prior, Activation: request.Activation}
	return c.validatePrior(selection, snapshot)
}

func (c *Controller) inspectCell(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	if err := c.validateOrdinary(source, key, act, prior); err != nil {
		return apply.Outcome{}, err
	}
	snapshot, err := c.probe(ctx)
	if err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	if snapshot.legacy != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, fault("Claude cell inspection", "split-only state for apply-cell", "the exact legacy monolith requires exhaustive sibling intent", "Controller.inspectCell", "inspecting an ordinary cell", "no mutation was attempted", "rerun the full installer or apply-selection", nil)
	}
	if prior != nil {
		request := service.GroupSelection{Scope: apply.GlobalScope(), Source: source, Prior: map[cell.Cell]registry.Record{key.Cell(): *prior}, Activation: map[cell.Cell]activation.ComponentActivation{key.Cell(): act}}
		if err := c.validatePriorRecord(request, key.Cell(), *prior); err != nil {
			return apply.Outcome{}, err
		}
	}
	row, present := snapshot.plugins[key.Cell()]
	if !present {
		return apply.Outcome{Status: apply.Completed(), Observation: registry.ObservationAbsent, Diagnostic: "the exact split plugin is absent"}, nil
	}
	managed := prior != nil && prior.Managed() && prior.Observation() != registry.ObservationAbsent
	record := c.observedRecord(key, act, source, managed, registry.OperationInspect, registry.OutcomeCompleted, registry.ObservationInstalled, "exact native state observed")
	return apply.Outcome{Status: apply.Completed(), Observation: registry.ObservationInstalled, Diagnostic: fmt.Sprintf("exact user-scoped plugin %s is installed", row.ID), Record: record}, nil
}

func (c *Controller) ensureCell(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	if err := c.validateOrdinary(source, key, act, prior); err != nil {
		return apply.Outcome{}, err
	}
	before, err := c.probe(ctx)
	if err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	if before.legacy != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, fault("Claude cell ensure", "split-only state", "legacy migration requires exhaustive sibling intent", "Controller.ensureCell", "ensuring one cell", "no mutation was attempted", "rerun the full installer or apply-selection", nil)
	}
	if prior != nil {
		request := service.GroupSelection{Scope: apply.GlobalScope(), Source: source, Prior: map[cell.Cell]registry.Record{key.Cell(): *prior}, Activation: map[cell.Cell]activation.ComponentActivation{key.Cell(): act}}
		if err := c.validatePriorRecord(request, key.Cell(), *prior); err != nil {
			return apply.Outcome{}, err
		}
	}
	if _, installed := before.plugins[key.Cell()]; installed && (prior == nil || !prior.Managed() || prior.Observation() == registry.ObservationAbsent) {
		record := c.observedRecord(key, act, source, false, registry.OperationEnsure, registry.OutcomeCompleted, registry.ObservationInstalled, "exact matching plugin remains externally owned")
		return apply.Outcome{Status: apply.Completed(), Observation: registry.ObservationInstalled, Record: record, Diagnostic: "exact matching plugin remains externally owned"}, nil
	}
	if err := c.ensureMarketplace(ctx, before.marketplace); err != nil {
		return c.ordinaryFailureProbe(ctx, source, key, act, prior, registry.OperationEnsure, err)
	}
	verb := "install"
	if _, installed := before.plugins[key.Cell()]; installed {
		verb = "update"
	}
	if _, err := c.run(ctx, command("claude", "plugin", verb, selector(nativePackage(act)), "--scope", "user")); err != nil {
		return c.ordinaryFailureProbe(ctx, source, key, act, prior, registry.OperationEnsure, err)
	}
	after, err := c.probe(ctx)
	if err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	if _, ok := after.plugins[key.Cell()]; !ok {
		return apply.Outcome{Observation: registry.ObservationAbsent}, fmt.Errorf("Claude ensure postcondition for %s is absent", key.Cell())
	}
	record := c.observedRecord(key, act, source, true, registry.OperationEnsure, registry.OutcomeCompleted, registry.ObservationInstalled, "exact native ensure postcondition confirmed")
	return apply.Outcome{Status: apply.Completed(), Observation: registry.ObservationInstalled, Record: record, Diagnostic: "exact native ensure postcondition confirmed"}, nil
}

func (c *Controller) removeCell(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior registry.Record) (apply.Outcome, error) {
	if err := c.validateOrdinary(source, key, act, &prior); err != nil {
		return apply.Outcome{}, err
	}
	if !prior.Managed() || prior.Observation() == registry.ObservationAbsent {
		return apply.Outcome{Observation: registry.ObservationUnknown}, fault("Claude plugin removal", "Pasture-managed non-absent prior fact", "the prior record is external or an absent tombstone", "Controller.removeCell", "authorizing native uninstall", "an external reinstall was preserved", "remove it manually or restore exact managed installed evidence", nil)
	}
	before, err := c.probe(ctx)
	if err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	request := service.GroupSelection{Scope: apply.GlobalScope(), Source: source, Prior: map[cell.Cell]registry.Record{key.Cell(): prior}, Activation: map[cell.Cell]activation.ComponentActivation{key.Cell(): act}}
	if err := c.validatePriorRecord(request, key.Cell(), prior); err != nil {
		return apply.Outcome{}, err
	}
	if before.legacy != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, fault("Claude plugin removal", "split-only state", "legacy migration requires exhaustive sibling intent", "Controller.removeCell", "removing one cell", "no mutation was attempted", "rerun the full installer or apply-selection", nil)
	}
	if _, installed := before.plugins[key.Cell()]; installed {
		if _, err := c.run(ctx, command("claude", "plugin", "uninstall", selector(nativePackage(act)), "--scope", "user")); err != nil {
			return c.ordinaryFailureProbe(ctx, source, key, act, &prior, registry.OperationRemove, err)
		}
	}
	after, err := c.probe(ctx)
	if err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	if _, remains := after.plugins[key.Cell()]; remains {
		return apply.Outcome{Observation: registry.ObservationInstalled}, fmt.Errorf("Claude remove postcondition for %s remains installed", key.Cell())
	}
	record := c.observedRecord(key, act, source, true, registry.OperationRemove, registry.OutcomeCompleted, registry.ObservationAbsent, "exact native remove postcondition confirmed")
	return apply.Outcome{Status: apply.Completed(), Observation: registry.ObservationAbsent, Record: record, Diagnostic: "exact native remove postcondition confirmed"}, nil
}

func (c *Controller) ordinaryFailureProbe(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record, operation registry.Operation, actionErr error) (apply.Outcome, error) {
	after, probeErr := c.probe(context.WithoutCancel(ctx))
	if probeErr != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, errors.Join(actionErr, probeErr)
	}
	observation := registry.ObservationAbsent
	if _, installed := after.plugins[key.Cell()]; installed {
		observation = registry.ObservationInstalled
	}
	managed := prior != nil && prior.Managed() && prior.Observation() != registry.ObservationAbsent
	record := c.observedRecord(key, act, source, managed, operation, registry.OutcomeFailed, observation, actionErr.Error())
	return apply.Outcome{Observation: observation, Record: record, Diagnostic: actionErr.Error()}, actionErr
}

func (c *Controller) validateRequest(request service.GroupSelection) error {
	if !request.Selection.IsValid() || request.Scope.Kind() != registry.ScopeGlobal {
		return fault("Claude group request validation", "valid exhaustive global selection", "the selection or scope is invalid", "Controller.validateRequest", "planning a group reconciliation", "no probe or mutation can safely start", "use a parsed exhaustive selection and global scope", nil)
	}
	return validateActivations(request.Activation)
}

func (c *Controller) validateActionRequest(request service.GroupSelection, plan service.GroupPlan, step service.GroupStep) error {
	if err := c.validateRequest(request); err != nil {
		return err
	}
	found := false
	for _, planned := range plan.Actions() {
		if planned == step {
			found = true
			break
		}
	}
	if !found {
		return fault("Claude group action validation", "action from the supplied immutable plan", "the action is stale or foreign", "Controller.validateActionRequest", "executing or inspecting a group action", "no native command may run", "rerun apply-selection to construct a fresh plan", nil)
	}
	return nil
}

func (c *Controller) validatePrior(request service.GroupSelection, live nativeSnapshot) error {
	if err := validatePriorCells(request.Prior); err != nil {
		return err
	}
	for cc, record := range request.Prior {
		if err := c.validatePriorRecord(request, cc, record); err != nil {
			return err
		}
		_, installed := live.plugins[cc]
		if record.Managed() && record.Observation() == registry.ObservationInstalled && !installed {
			return fault("Claude managed-state validation", "live identity agrees with managed inventory", fmt.Sprintf("managed record for %s says installed but exact live plugin is absent", cc), "Controller.validatePrior", "preflighting ownership before mutation", "no native mutation was attempted", "inspect and manually repair the missing managed component or registry ambiguity", nil)
		}
	}
	return nil
}

func (c *Controller) validatePriorForInspection(request service.GroupSelection, live nativeSnapshot, step service.GroupStep) error {
	if err := validatePriorCells(request.Prior); err != nil {
		return err
	}
	for cc, record := range request.Prior {
		if err := c.validatePriorRecord(request, cc, record); err != nil {
			return err
		}
		_, installed := live.plugins[cc]
		if record.Managed() && record.Observation() == registry.ObservationInstalled && !installed && !(step.ControlCell() == cc && (step.Kind() == service.RemoveCellGroupAction() || step.Kind() == service.EnsureCellGroupAction())) {
			return fault("Claude inspected-state validation", "live identity compatible with prior or current action", fmt.Sprintf("managed record for %s became absent outside its typed action", cc), "Controller.validatePriorForInspection", "building complete live facts", "facts may reflect an external concurrent change", "repair native state and retry with one installer", nil)
		}
	}
	return nil
}

func validatePriorCells(prior map[cell.Cell]registry.Record) error {
	for cc := range prior {
		if cc.Harness() != ir.HarnessClaudeCode || !cc.IsValid() {
			return fault("Claude prior-record validation", "only valid Claude global cells", fmt.Sprintf("prior inventory contains foreign or invalid cell %s", cc), "Controller.validatePriorCells", "validating native ownership before mutation", "no native mutation was attempted", "reload the scoped Claude registry and remove the contradictory entry", nil)
		}
	}
	return nil
}

func (c *Controller) validatePriorRecord(request service.GroupSelection, cc cell.Cell, record registry.Record) error {
	key, keyErr := request.Scope.Key(cc)
	if keyErr != nil {
		return keyErr
	}
	act, ok := request.Activation[cc]
	wantSource := registry.SourceInstaller
	if request.Source == apply.HomeManagerSource() {
		wantSource = registry.SourceHomeManager
	}
	wantSelector, _ := registry.NewSelector(selector(packageFor(cc.Extension())))
	if !ok || !record.IsValid() || record.Key() != key || record.Cell() != cc || record.Source() != wantSource || record.Strategy() != activation.NativePluginKindValue() || record.ArtifactID() != c.artifacts[cc] || record.Version() != c.versions[cc] || record.Selector() != wantSelector || record.Trust() != registry.TrustNotApplicable || !act.IsValid() {
		return fault("Claude prior-record validation", "exact source, key, strategy, bundle, current version, selector, and trust", fmt.Sprintf("prior record for %s does not match immutable activation authority", cc), "Controller.validatePriorRecord", "authorizing native reconciliation", "no mutation was attempted", "repair or remove the contradictory registry record, then rerun status and apply-selection", nil)
	}
	return nil
}

func (c *Controller) validateOrdinary(source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) error {
	if !source.IsValid() || !key.IsValid() || key.Scope() != registry.ScopeGlobal || key.Cell().Harness() != ir.HarnessClaudeCode {
		return fmt.Errorf("Claude activator requires a valid source and global Claude key")
	}
	if !act.IsValid() || act.Cell() != key.Cell() || act.Strategy().Kind() != activation.NativePluginKindValue() || nativePackage(act) != packageFor(key.Cell().Extension()) {
		return fmt.Errorf("Claude activation contradicts scoped cell %s", key.Cell())
	}
	if prior != nil {
		request := service.GroupSelection{Scope: apply.GlobalScope(), Source: source, Prior: map[cell.Cell]registry.Record{key.Cell(): *prior}, Activation: map[cell.Cell]activation.ComponentActivation{key.Cell(): act}}
		if err := c.validatePriorRecord(request, key.Cell(), *prior); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) probe(ctx context.Context) (nativeSnapshot, error) {
	versionResult, err := c.run(ctx, command("claude", "--version"))
	if err != nil {
		return nativeSnapshot{}, &probeUnavailableError{cause: err}
	}
	versionText := strings.TrimSuffix(strings.TrimSpace(string(versionResult.Stdout)), " (Claude Code)")
	host, err := runtime.ParseHostVersion(versionText)
	// The admitted versions are spelled by the runtime contract's own renderer,
	// so this text follows the contract when the contract moves.
	admitted := runtime.ClaudeCode2_1_261().Versions()
	if err != nil || !admitted.Allows(host) {
		return nativeSnapshot{}, &probeUnavailableError{cause: fault("Claude host probe", fmt.Sprintf("a Claude Code host version %s", admitted.Describe()), fmt.Sprintf("reported host version %q is not admitted by the reviewed floor", strings.TrimSpace(string(versionResult.Stdout))), "Controller.probe", "checking compatibility before native mutation", "no marketplace or plugin action was attempted", fmt.Sprintf("install a Claude Code version %s or update the reviewed activation contract", admitted.Describe()), err)}
	}
	marketResult, err := c.run(ctx, command("claude", "plugin", "marketplace", "list", "--json"))
	if err != nil {
		return nativeSnapshot{}, &probeUnavailableError{cause: err}
	}
	markets, err := decodeMarketplaces(marketResult.Stdout)
	if err != nil {
		return nativeSnapshot{}, &probeUnavailableError{cause: err}
	}
	hasMarket := false
	for _, market := range markets {
		if market.Name != MarketplaceName {
			continue
		}
		if market.Source != marketplaceSourceGitHub || market.Repo != MarketplaceRepo {
			return nativeSnapshot{}, fault("Claude marketplace validation", "exact aura-plugins GitHub source", fmt.Sprintf("marketplace %s points to a different source", MarketplaceName), "Controller.probe", "classifying shared native state", "no plugin mutation was attempted", "remove or repair the conflicting marketplace manually, then rerun", nil)
		}
		hasMarket = true
	}
	pluginResult, err := c.run(ctx, command("claude", "plugin", "list", "--available", "--json"))
	if err != nil {
		return nativeSnapshot{}, &probeUnavailableError{cause: err}
	}
	rows, err := decodePlugins(pluginResult.Stdout)
	if err != nil {
		return nativeSnapshot{}, &probeUnavailableError{cause: err}
	}
	snapshot := nativeSnapshot{marketplace: hasMarket, plugins: map[cell.Cell]pluginRow{}}
	for _, row := range rows {
		cc, exact, legacy := classifyRow(row)
		if !exact && !legacy {
			if isPastureIdentity(row) {
				return nativeSnapshot{}, fault("Claude native-state classification", "exact split packages or exact user-scoped v0.0.4 monolith", fmt.Sprintf("near-match or unknown Pasture plugin row %q was found", row.ID), "Controller.probe", "classifying legacy and split state before mutation", "no plugin or marketplace mutation was attempted", "remove or repair the ambiguous row manually, then rerun the full selection", nil)
			}
			continue
		}
		if err := c.verifyRow(row, cc, legacy); err != nil {
			return nativeSnapshot{}, err
		}
		if legacy {
			if snapshot.legacy != nil {
				return nativeSnapshot{}, fault("Claude native-state reconciliation", "one exact user-scoped v0.0.4 monolith row", fmt.Sprintf("duplicate native legacy row %q affects the Claude skills migration cell", row.ID), "Controller.probe", "reconciling native rows before mutation", "reconciliation stopped before any native mutation and the registry remains unchanged", "remove the duplicate row or repair the native listing, then retry the full selection", nil)
			}
			copy := row
			snapshot.legacy = &copy
			continue
		}
		if _, duplicate := snapshot.plugins[cc]; duplicate {
			return nativeSnapshot{}, fault("Claude native-state reconciliation", "one exact native row per Claude selector", duplicateNativeRowDiagnostic(row, cc), "Controller.probe", "reconciling native rows before mutation", "reconciliation stopped before any native mutation and the registry remains unchanged", "remove the duplicate row or repair the native listing, then retry the full selection", nil)
		}
		snapshot.plugins[cc] = row
	}
	return snapshot, nil
}

func optionalSelection(request service.GroupSelection) bool {
	if len(request.Prior) != 0 {
		return false
	}
	for _, extension := range cell.CanonicalExtensions() {
		cc, _ := cell.New(ir.HarnessClaudeCode, extension)
		if request.Selection.Enabled(cc) {
			return false
		}
	}
	return true
}

func (c *Controller) verifyRow(row pluginRow, cc cell.Cell, legacy bool) error {
	wantPackage := LegacyPackage
	wantVersion := LegacyVersion
	if !legacy {
		wantPackage = packageFor(cc.Extension())
		wantVersion = c.versions[cc].String()
	}
	if row.Scope != "user" || !row.Enabled || row.Marketplace != MarketplaceName || row.ID != selector(wantPackage) || row.Name != wantPackage {
		return fault("Claude plugin-row validation", "exact enabled user-scoped selector", fmt.Sprintf("row %q has wrong name, marketplace, scope, enabled state, or selector", row.ID), "Controller.verifyRow", "classifying native ownership", "no mutation was attempted", "repair the conflicting plugin row manually and rerun", nil)
	}
	if row.Version != nil {
		if *row.Version != wantVersion {
			return fault("Claude plugin release validation", wantVersion, fmt.Sprintf("row %q reports version %q", row.ID, *row.Version), "Controller.verifyRow", "proving selected release identity", "no mutation was attempted", "install the exact reviewed plugin release or update the immutable target", nil)
		}
		return nil
	}
	data, err := c.manifests.ReadPluginManifest(row.InstallPath)
	if err != nil {
		return fault("Claude versionless-row validation", "matching native plugin manifest", err.Error(), row.InstallPath, "proving selected release identity", "the versionless row cannot be accepted", "repair the native installation so its manifest is readable and exact", err)
	}
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Name != wantPackage || manifest.Version != wantVersion {
		return fault("Claude versionless-row validation", "matching name and version in plugin manifest", fmt.Sprintf("manifest reports name %q version %q: %v", manifest.Name, manifest.Version, err), row.InstallPath, "proving selected release identity", "the versionless row cannot be accepted", "repair the native installation so its manifest matches the selected immutable component", err)
	}
	return nil
}

func classifyRow(row pluginRow) (cell.Cell, bool, bool) {
	if row.ID == selector(LegacyPackage) || row.Name == LegacyPackage {
		return cell.Cell{}, row.ID == selector(LegacyPackage) && row.Name == LegacyPackage, true
	}
	for _, extension := range cell.CanonicalExtensions() {
		cc, _ := cell.New(ir.HarnessClaudeCode, extension)
		pkg := packageFor(extension)
		if row.ID == selector(pkg) || row.Name == pkg {
			return cc, row.ID == selector(pkg) && row.Name == pkg, false
		}
	}
	return cell.Cell{}, false, false
}

func duplicateNativeRowDiagnostic(row pluginRow, cc cell.Cell) string {
	return fmt.Sprintf("duplicate native row %q affects selector %q and cell %q", row.ID, selector(packageFor(cc.Extension())), cc.String())
}

func (c *Controller) ensureMarketplace(ctx context.Context, present bool) error {
	if present {
		_, err := c.run(ctx, command("claude", "plugin", "marketplace", "update", MarketplaceName))
		return err
	}
	_, err := c.run(ctx, command("claude", "plugin", "marketplace", "add", MarketplaceRepo, "--scope", "user"))
	return err
}

func (c *Controller) run(ctx context.Context, schema activation.CommandSchema) (CommandResult, error) {
	const managerTimeout = 2 * time.Minute
	runCtx, cancel := context.WithTimeout(ctx, managerTimeout)
	defer cancel()
	result, err := c.runner.Run(runCtx, schema)
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return result, fault("Claude native command timeout", "completion within two minutes", err.Error(), schema.String(), "waiting for a bounded manager command", "the requested state is unconfirmed and later actions stop", "repair the manager hang and retry the full selection", errors.Join(err, runCtx.Err()))
		}
		if errors.Is(runCtx.Err(), context.Canceled) {
			return result, fault("Claude native command cancellation", "live caller context", err.Error(), schema.String(), "running a manager command", "the requested state is unconfirmed and later actions stop", "retry with a live context", errors.Join(err, runCtx.Err()))
		}
	}
	return result, err
}

func validateActivations(acts map[cell.Cell]activation.ComponentActivation) error {
	if len(acts) != 3 {
		return fmt.Errorf("Claude group activation map has %d entries instead of three", len(acts))
	}
	for _, extension := range cell.CanonicalExtensions() {
		cc, _ := cell.New(ir.HarnessClaudeCode, extension)
		act, ok := acts[cc]
		if !ok || !act.IsValid() || act.Cell() != cc || act.Strategy().Kind() != activation.NativePluginKindValue() || nativePackage(act) != packageFor(extension) {
			return fmt.Errorf("Claude activation for %s is missing or contradictory", cc)
		}
	}
	return nil
}

func (c *Controller) observedRecord(key registry.Key, act activation.ComponentActivation, source apply.Source, managed bool, operation registry.Operation, outcome registry.Outcome, observation registry.Observation, diagnostic string) *registry.Record {
	selectorValue, _ := registry.NewSelector(selector(nativePackage(act)))
	registrySource := registry.SourceInstaller
	if source == apply.HomeManagerSource() {
		registrySource = registry.SourceHomeManager
	}
	record, err := registry.NewRecord(registry.RecordInput{Key: key, Source: registrySource, Strategy: activation.NativePluginKindValue(), Managed: managed, ArtifactID: c.artifacts[key.Cell()], Version: c.versions[key.Cell()], Selector: selectorValue, Observation: observation, Trust: registry.TrustNotApplicable, LastOperation: operation, LastOutcome: outcome, Diagnostic: diagnostic})
	if err != nil {
		return nil
	}
	return &record
}

func nativePackage(act activation.ComponentActivation) string {
	plugin, ok := act.Strategy().(activation.NativePlugin)
	if !ok {
		return ""
	}
	return plugin.Package()
}

func registryOperation(operation apply.Operation) registry.Operation {
	switch operation {
	case apply.Ensure():
		return registry.OperationEnsure
	case apply.RemoveOp():
		return registry.OperationRemove
	default:
		return registry.OperationInspect
	}
}

var _ service.GroupReconciler = (*Controller)(nil)

type Activator struct{ controller *Controller }

func (c *Controller) Activator() *Activator { return &Activator{controller: c} }
func (a *Activator) StrategyKind() activation.StrategyKind {
	return activation.NativePluginKindValue()
}
func (a *Activator) Inspect(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	return a.controller.inspectCell(ctx, source, key, act, prior)
}
func (a *Activator) Ensure(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	return a.controller.ensureCell(ctx, source, key, act, prior)
}
func (a *Activator) Remove(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior registry.Record) (apply.Outcome, error) {
	return a.controller.removeCell(ctx, source, key, act, prior)
}

var _ apply.Activator = (*Activator)(nil)
