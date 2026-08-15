package service

import (
	"context"
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/selection"
)

type planned struct {
	key         registry.Key
	cell        cell.Cell
	op          apply.Operation
	act         activation.ComponentActivation
	prior       *registry.Record
	declarative bool
}

// GroupSelection supplies one reconciler with exhaustive sibling intent and
// all scoped prior facts. It is the sole selection-wide mutation seam.
type GroupSelection struct {
	Selection  selection.Selection
	Scope      apply.Scope
	Source     apply.Source
	Prior      map[cell.Cell]registry.Record
	Activation map[cell.Cell]activation.ComponentActivation
}

// GroupCell supplies the same reconciler with a context-free cell request. A
// reconciler rejects when live group state requires exhaustive sibling intent.
type GroupCell struct {
	Cell       cell.Cell
	Enabled    bool
	Scope      apply.Scope
	Source     apply.Source
	Prior      map[cell.Cell]registry.Record
	Activation map[cell.Cell]activation.ComponentActivation
}

// GroupStep is one immutable action in a read-only, canonical group plan. The
// key is part of the plan so project requests cannot be executed or persisted
// under a different project root.
type GroupStep struct {
	cell cell.Cell
	key  registry.Key
	op   apply.Operation
}

func NewGroupStep(c cell.Cell, key registry.Key, op apply.Operation) GroupStep {
	return GroupStep{cell: c, key: key, op: op}
}
func (s GroupStep) Cell() cell.Cell            { return s.cell }
func (s GroupStep) Key() registry.Key          { return s.key }
func (s GroupStep) Operation() apply.Operation { return s.op }

// GroupPlan is produced without mutation and must contain exactly one step for
// every extension of the reconciler's harness in canonical order.
type GroupPlan struct {
	Handled bool
	Steps   []GroupStep
}

// GroupAction is one inspected group row and its strongest confirmed fact.
type GroupAction struct {
	Row    apply.ActionRow
	Record *registry.Record
}

// GroupReconciler is the one typed selection-wide native protocol. PlanSelection
// is read-only. The engine then executes and inspects exactly one validated step
// at a time and persists the inspected fact before advancing.
type GroupReconciler interface {
	Harness() ir.HarnessID
	PlanSelection(context.Context, GroupSelection) (GroupPlan, error)
	Execute(context.Context, GroupStep) error
	Inspect(context.Context, GroupStep) (GroupAction, error)
	PreflightCell(context.Context, GroupCell) error
}

type engine struct {
	activators map[activation.StrategyKind]apply.Activator
	group      GroupReconciler
}

func newEngine(activators []apply.Activator, group GroupReconciler) (*engine, error) {
	reg := make(map[activation.StrategyKind]apply.Activator, len(activators))
	for _, a := range activators {
		if a == nil || !a.StrategyKind().IsValid() {
			return nil, cell.NewFault("service construction", "valid strategy activator", "an activator is nil or reports an invalid strategy", "internal/install/service.newEngine", "assembling the application service", "strategy dispatch cannot be exhaustive", "register one non-nil activator for each used strategy", nil)
		}
		kind := a.StrategyKind()
		if _, dup := reg[kind]; dup {
			return nil, cell.NewFault("service construction", "one activator per strategy kind", fmt.Sprintf("two activators were registered for %q", kind), "internal/install/service.newEngine", "assembling the application service", "strategy dispatch would be ambiguous", "register exactly one activator per strategy kind", nil)
		}
		reg[kind] = a
	}
	if group != nil && !group.Harness().IsValid() {
		return nil, cell.NewFault("service construction", "valid group reconciler", "the group reconciler reports an invalid harness", "internal/install/service.newEngine", "assembling selection-wide reconciliation", "group dispatch cannot be deterministic", "register one reconciler with a known harness", nil)
	}
	return &engine{activators: reg, group: group}, nil
}

func (e *engine) applySelection(ctx context.Context, sel selection.Selection, scope apply.Scope, source apply.Source, contracts map[ir.HarnessID]activation.ActivationContract, store *registry.Store, commit func(context.Context, registry.Store) error) (apply.Result, error) {
	if !sel.IsValid() {
		return apply.Result{}, preplan(source, "selection validation", "the effective selection is invalid", "internal/install/service.engine.applySelection", "no cell can be applied", "provide an exhaustive selection from selection.Parse or preferences.EffectiveSelection", apply.RemediationRerunInstaller)
	}
	bindings, err := e.bindAll(source, contracts)
	if err != nil {
		return apply.Result{}, err
	}
	plan, err := e.planSelection(sel, scope, source, bindings, *store)
	if err != nil {
		return apply.Result{}, err
	}
	if err := preflightNativeOwnership(source, plan); err != nil {
		return apply.Result{}, err
	}
	preRows, preFailed := e.preinspectDeclarative(ctx, source, plan)
	if preFailed {
		return assembleResult(source, scope.Kind(), plan, preRows, nil, true), nil
	}

	groupRows := map[cell.Cell]GroupAction{}
	var handledHarness ir.HarnessID
	if e.group != nil {
		harness := e.group.Harness()
		reconciler := e.group
		prior := scopedPrior(scope, harness, *store)
		acts := harnessActivations(harness, bindings)
		group, groupErr := reconciler.PlanSelection(ctx, GroupSelection{Selection: sel, Scope: scope, Source: source, Prior: prior, Activation: acts})
		if groupErr != nil {
			return apply.Result{}, normalizePreplan(source, groupErr, apply.RemediationApplySelection)
		}
		if group.Handled {
			if err := validateGroupPlan(scope, harness, group); err != nil {
				return apply.Result{}, normalizePreplan(source, err, apply.RemediationManualRepair)
			}
			handledHarness = harness
			groupRows = e.executeGroup(ctx, source, group, store, commit)
		}
	}

	filtered := plan[:0]
	for _, p := range plan {
		if p.cell.Harness() != handledHarness {
			filtered = append(filtered, p)
		}
	}
	plan = filtered
	if groupFailed(groupRows) {
		return assembleResult(source, scope.Kind(), plan, preRows, groupRows, true), nil
	}
	ordinary := e.executePlan(ctx, source, scope.Kind(), plan, preRows, store, commit)
	return mergeGroupResult(source, scope.Kind(), ordinary, groupRows), nil
}

func (e *engine) applyCell(ctx context.Context, c cell.Cell, enabled bool, scope apply.Scope, source apply.Source, contracts map[ir.HarnessID]activation.ActivationContract, store *registry.Store, commit func(context.Context, registry.Store) error) (apply.Result, error) {
	bindings, err := e.bindAll(source, contracts)
	if err != nil {
		return apply.Result{}, err
	}
	if !c.IsValid() {
		return apply.Result{}, preplan(source, "cell validation", "the requested cell is invalid", "internal/install/service.engine.applyCell", "no cell can be applied", "construct the cell with cell.New", apply.RemediationApplyCell)
	}
	act := bindings[c]
	if source == apply.HomeManagerSource() && act.Strategy().Kind() == activation.DirectFileKindValue() {
		return apply.Result{}, preplan(source, "declarative ownership validation", fmt.Sprintf("Home Manager owns DirectFile cell %s declaratively", c), "internal/install/service.engine.applyCell", "Pasture did not inspect, write, or remove the declarative destination", "rerun Home Manager activation so Nix realizes the link and invokes apply-selection for native cells", apply.RemediationRerunHomeManager)
	}
	if e.group != nil && e.group.Harness() == c.Harness() {
		if err := e.group.PreflightCell(ctx, GroupCell{Cell: c, Enabled: enabled, Scope: scope, Source: source, Prior: scopedPrior(scope, c.Harness(), *store), Activation: harnessActivations(c.Harness(), bindings)}); err != nil {
			return apply.Result{}, normalizePreplan(source, err, apply.RemediationApplySelection)
		}
	}
	key, keyErr := scope.Key(c)
	if keyErr != nil {
		return apply.Result{}, normalizePreplan(source, keyErr, apply.RemediationManualRepair)
	}
	prior, hasPrior := store.Lookup(key)
	var ptr *registry.Record
	if hasPrior {
		copy := prior
		ptr = &copy
	}
	op := apply.Inspect()
	switch {
	case enabled:
		op = apply.Ensure()
	case hasPrior && removable(prior):
		op = apply.RemoveOp()
	default:
		return apply.NewResult(source, scope.Kind(), true, []apply.ActionRow{apply.NewActionRow(c, apply.Inspect(), apply.NoOp(), management(ptr), registry.ObservationInvalid, "desired state is false and no Pasture-managed installed or unknown fact authorizes removal")}), nil
	}
	p := planned{key: key, cell: c, op: op, act: act, prior: ptr}
	if err := preflightNativeOwnership(source, []planned{p}); err != nil {
		return apply.Result{}, err
	}
	return e.executePlan(ctx, source, scope.Kind(), []planned{p}, nil, store, commit), nil
}

func (e *engine) bindAll(source apply.Source, contracts map[ir.HarnessID]activation.ActivationContract) (map[cell.Cell]activation.ComponentActivation, error) {
	if !source.IsValid() {
		return nil, preplan(source, "source validation", "the apply source is neither installer nor home-manager", "internal/install/service.engine.bindAll", "ownership rules cannot be selected", "use InstallerSource or HomeManagerSource", apply.RemediationManualRepair)
	}
	bindings := make(map[cell.Cell]activation.ComponentActivation, 9)
	for _, c := range cell.CanonicalCells() {
		contract, ok := contracts[c.Harness()]
		if !ok || !contract.IsValid() || contract.Harness() != c.Harness() {
			return nil, preplan(source, "activation contract validation", fmt.Sprintf("harness %s has no matching valid activation contract", c.Harness()), "internal/install/service.engine.bindAll", "the nine-cell plan is incomplete and no mutation was started", fmt.Sprintf("wire one exhaustive activation contract for %s", c.Harness()), apply.RemediationRerunInstaller)
		}
		desc, descriptorErr := activation.NewComponentDescriptor(c)
		if descriptorErr != nil {
			return nil, normalizePreplan(source, descriptorErr, apply.RemediationManualRepair)
		}
		act, lookupErr := activation.LookupComponentActivation(contract, desc)
		if lookupErr != nil {
			return nil, normalizePreplan(source, lookupErr, apply.RemediationManualRepair)
		}
		if _, ok := e.activators[act.Strategy().Kind()]; !ok {
			return nil, preplan(source, "activator validation", fmt.Sprintf("cell %s uses %s but no production activator is registered", c, act.Strategy().Kind()), "internal/install/service.engine.bindAll", "execution would stop only after earlier cells had mutated", "register the strategy activator before applying", apply.RemediationManualRepair)
		}
		bindings[c] = act
	}
	return bindings, nil
}

func (e *engine) planSelection(sel selection.Selection, scope apply.Scope, source apply.Source, bindings map[cell.Cell]activation.ComponentActivation, store registry.Store) ([]planned, error) {
	plan := make([]planned, 0, 9)
	for _, state := range sel.Ordered() {
		key, err := scope.Key(state.Cell)
		if err != nil {
			return nil, normalizePreplan(source, err, apply.RemediationManualRepair)
		}
		prior, hasPrior := store.Lookup(key)
		act := bindings[state.Cell]
		declarative := source == apply.HomeManagerSource() && act.Strategy().Kind() == activation.DirectFileKindValue()
		var op apply.Operation
		switch {
		case declarative:
			op = apply.Inspect()
		case state.Enabled:
			op = apply.Ensure()
		case hasPrior && removable(prior):
			op = apply.RemoveOp()
		default:
			continue
		}
		var ptr *registry.Record
		if hasPrior {
			copy := prior
			ptr = &copy
		}
		plan = append(plan, planned{key: key, cell: state.Cell, op: op, act: act, prior: ptr, declarative: declarative})
	}
	return plan, nil
}

func preflightNativeOwnership(source apply.Source, plan []planned) error {
	wanted := sourceRegistry(source)
	for _, p := range plan {
		if p.declarative || p.act.Strategy().Kind() == activation.DirectFileKindValue() || p.prior == nil {
			continue
		}
		if p.prior.Source() != wanted {
			return preplan(source, "native controller validation", fmt.Sprintf("native cell %s is recorded under %s but the request uses %s", p.cell, p.prior.Source(), wanted), "internal/install/service.preflightNativeOwnership", "no live inspection or native mutation was attempted for any cell", fmt.Sprintf("disable %s control of %s, keep exactly one controller, then rerun %s", p.prior.Source(), p.cell, wanted), remediationFor(source))
		}
	}
	return nil
}

func (e *engine) preinspectDeclarative(ctx context.Context, source apply.Source, plan []planned) (map[cell.Cell]apply.ActionRow, bool) {
	rows := map[cell.Cell]apply.ActionRow{}
	failed := false
	for _, p := range plan {
		if !p.declarative {
			continue
		}
		if failed {
			rows[p.cell] = apply.NewActionRow(p.cell, apply.Inspect(), apply.Unattempted(), apply.ManagementDeclarative, registry.ObservationUnknown, "an earlier declarative pre-inspection failed; this path was not inspected")
			continue
		}
		out, err := e.activators[p.act.Strategy().Kind()].Inspect(ctx, source, p.key, p.act, p.prior)
		if err != nil {
			rows[p.cell] = apply.NewActionRow(p.cell, apply.Inspect(), apply.Failed(), apply.ManagementDeclarative, out.Observation, actionable("declarative cell inspection failed", err, p.cell, "pre-plan live inspection", "no native action was attempted", "repair the destination and rerun Home Manager"))
			failed = true
			continue
		}
		rows[p.cell] = apply.NewActionRow(p.cell, apply.Inspect(), apply.ManagedDeclaratively(), apply.ManagementDeclarative, out.Observation, out.Diagnostic)
	}
	return rows, failed
}

func (e *engine) executePlan(ctx context.Context, source apply.Source, scope registry.Scope, plan []planned, preRows map[cell.Cell]apply.ActionRow, store *registry.Store, commit func(context.Context, registry.Store) error) apply.Result {
	rows := make([]apply.ActionRow, 0, len(plan))
	failed := false
	for _, p := range plan {
		if p.declarative {
			rows = append(rows, preRows[p.cell])
			continue
		}
		if failed {
			rows = append(rows, apply.NewActionRow(p.cell, p.op, apply.Unattempted(), management(p.prior), registry.ObservationInvalid, "an earlier canonical cell failed; this cell was not attempted"))
			continue
		}
		row, outcome := e.executeOne(ctx, source, p)
		if outcome.Record != nil {
			row = apply.NewActionRow(row.Cell(), row.Operation(), row.Status(), recordManagement(*outcome.Record), row.Observation(), row.Diagnostic())
			if upsertErr := store.Upsert(*outcome.Record); upsertErr != nil {
				row = apply.NewActionRow(p.cell, p.op, apply.Failed(), row.Management(), row.Observation(), actionable("confirmed registry fact could not be staged", upsertErr, p.cell, "upsert", "the next cell was not attempted", "repair the record construction and retry"))
			} else if saveErr := commit(ctx, *store); saveErr != nil {
				row = apply.NewActionRow(p.cell, p.op, apply.Failed(), row.Management(), row.Observation(), actionable("confirmed registry fact could not be saved atomically", saveErr, p.cell, "registry-save", "the live component may have changed but the previous registry remains authoritative; later cells were not attempted", "inspect status, repair the registry path or permissions, and rerun the same apply operation"))
			}
		}
		rows = append(rows, row)
		if row.Status() == apply.Failed() {
			failed = true
		}
	}
	return apply.NewResult(source, scope, !failed, rows)
}

func (e *engine) executeOne(ctx context.Context, source apply.Source, p planned) (apply.ActionRow, apply.Outcome) {
	activator := e.activators[p.act.Strategy().Kind()]
	if err := ctx.Err(); err != nil {
		return apply.NewActionRow(p.cell, p.op, apply.Failed(), management(p.prior), registry.ObservationUnknown, actionable("cell execution was canceled", err, p.cell, "pre-inspection", "this and later cells were not mutated", "retry with a live context")), apply.Outcome{}
	}
	if _, err := activator.Inspect(ctx, source, p.key, p.act, p.prior); err != nil {
		return apply.NewActionRow(p.cell, p.op, apply.Failed(), management(p.prior), registry.ObservationUnknown, actionable("pre-action live inspection failed", err, p.cell, "live-inspect", "no action was attempted and later cells were stopped", "repair the reported live-state conflict and retry")), apply.Outcome{}
	}
	var out apply.Outcome
	var err error
	if p.op == apply.Ensure() {
		out, err = activator.Ensure(ctx, source, p.key, p.act, p.prior)
	} else {
		out, err = activator.Remove(ctx, source, p.key, p.act, *p.prior)
	}
	if err != nil {
		return apply.NewActionRow(p.cell, p.op, apply.Failed(), management(p.prior), out.Observation, actionable("activation action failed after live inspection", err, p.cell, p.op.String(), "this cell records only confirmed or unknown state and later cells were not attempted", "repair the reported destination or manager state, then rerun the same apply operation")), out
	}
	if out.Record == nil || !out.Record.IsValid() || out.Record.Key() != p.key {
		err := fmt.Errorf("activator returned no valid confirmed record for scoped key %s/%s", p.key.Scope(), p.cell)
		return apply.NewActionRow(p.cell, p.op, apply.Failed(), management(p.prior), out.Observation, actionable("activation postcondition was not representable", err, p.cell, "post-inspection", "the action may have changed live state but no unproved fact was persisted; later cells were stopped", "inspect the cell and rerun after repairing the activator contract")), apply.Outcome{Observation: out.Observation}
	}
	return apply.NewActionRow(p.cell, p.op, out.Status, recordManagement(*out.Record), out.Observation, out.Diagnostic), out
}

func validateGroupPlan(scope apply.Scope, harness ir.HarnessID, plan GroupPlan) error {
	if len(plan.Steps) != len(cell.CanonicalExtensions()) {
		return fmt.Errorf("group plan for %s contains %d rows instead of three", harness, len(plan.Steps))
	}
	for i, extension := range cell.CanonicalExtensions() {
		wantCell, _ := cell.New(harness, extension)
		wantKey, err := scope.Key(wantCell)
		if err != nil {
			return err
		}
		step := plan.Steps[i]
		if step.Cell() != wantCell || step.Key() != wantKey || !step.Operation().IsValid() {
			return fmt.Errorf("group plan row %d must be canonical cell %s with exact scoped key and valid operation", i, wantCell)
		}
	}
	return nil
}

func (e *engine) executeGroup(ctx context.Context, source apply.Source, plan GroupPlan, store *registry.Store, commit func(context.Context, registry.Store) error) map[cell.Cell]GroupAction {
	groups := make(map[cell.Cell]GroupAction, len(plan.Steps))
	failed := false
	for _, step := range plan.Steps {
		c := step.Cell()
		if failed {
			groups[c] = GroupAction{Row: apply.NewActionRow(c, step.Operation(), apply.Unattempted(), apply.ManagementUnknown, registry.ObservationUnknown, "an earlier group action failed; this action was not attempted")}
			continue
		}
		executeErr := e.group.Execute(ctx, step)
		action, inspectErr := e.group.Inspect(ctx, step)
		if validationErr := validateGroupAction(step, action); validationErr != nil {
			inspectErr = validationErr
		}
		row := action.Row
		if inspectErr != nil {
			row = apply.NewActionRow(c, step.Operation(), apply.Failed(), row.Management(), registry.ObservationUnknown, actionable("group live inspection failed", inspectErr, c, "post-action inspection", "the strongest group fact could not be confirmed and later actions were not attempted", "repair the native probe and retry the full selection"))
		} else if executeErr != nil {
			row = apply.NewActionRow(c, step.Operation(), apply.Failed(), row.Management(), row.Observation(), actionable("group action failed", executeErr, c, step.Operation().String(), "the inspected live fact is retained and later actions were not attempted", "repair the native action and retry the full selection"))
		}
		if action.Record != nil {
			if err := store.Upsert(*action.Record); err != nil {
				row = apply.NewActionRow(c, row.Operation(), apply.Failed(), row.Management(), row.Observation(), actionable("group fact could not be staged", err, c, "upsert", "later facts and cells were not attempted", "repair the group record and retry the full selection"))
			} else if err := commit(ctx, *store); err != nil {
				row = apply.NewActionRow(c, row.Operation(), apply.Failed(), row.Management(), row.Observation(), actionable("group fact could not be saved atomically", err, c, "registry-save", "the previous registry remains authoritative and later cells were not attempted", "repair registry persistence and retry the full selection"))
			}
		}
		groups[c] = GroupAction{Row: row, Record: action.Record}
		if row.Status() == apply.Failed() {
			failed = true
		}
	}
	return groups
}

func validateGroupAction(step GroupStep, action GroupAction) error {
	row := action.Row
	if row.Cell() != step.Cell() || row.Operation() != step.Operation() || !row.Status().IsValid() || !row.Management().IsValid() || !row.Observation().IsValid() {
		return fmt.Errorf("group inspection returned invalid typed fields for %s", step.Cell())
	}
	if action.Record != nil && (!action.Record.IsValid() || action.Record.Key() != step.Key() || action.Record.Cell() != step.Cell()) {
		return fmt.Errorf("group inspection returned a record for a different scoped key than %s", step.Cell())
	}
	return nil
}

func groupFailed(groups map[cell.Cell]GroupAction) bool {
	for _, c := range cell.CanonicalCells() {
		if action, ok := groups[c]; ok && action.Row.Status() == apply.Failed() {
			return true
		}
	}
	return false
}

func mergeGroupResult(source apply.Source, scope registry.Scope, ordinary apply.Result, groups map[cell.Cell]GroupAction) apply.Result {
	rows := map[cell.Cell]apply.ActionRow{}
	for _, row := range ordinary.Rows() {
		rows[row.Cell()] = row
	}
	failed := !ordinary.OK()
	for _, c := range cell.CanonicalCells() {
		action, ok := groups[c]
		if !ok {
			continue
		}
		row := action.Row
		rows[c] = row
	}
	ordered := make([]apply.ActionRow, 0, len(rows))
	for _, c := range cell.CanonicalCells() {
		if row, ok := rows[c]; ok {
			ordered = append(ordered, row)
		}
	}
	return apply.NewResult(source, scope, !failed, ordered)
}

func assembleResult(source apply.Source, scope registry.Scope, plan []planned, pre map[cell.Cell]apply.ActionRow, groups map[cell.Cell]GroupAction, failed bool) apply.Result {
	rows := map[cell.Cell]apply.ActionRow{}
	for _, p := range plan {
		if row, ok := pre[p.cell]; ok {
			rows[p.cell] = row
		} else {
			rows[p.cell] = apply.NewActionRow(p.cell, p.op, apply.Unattempted(), management(p.prior), registry.ObservationInvalid, "declarative validation failed before native execution")
		}
	}
	for _, c := range cell.CanonicalCells() {
		if action, ok := groups[c]; ok {
			rows[c] = action.Row
		}
	}
	ordered := make([]apply.ActionRow, 0, len(rows))
	for _, c := range cell.CanonicalCells() {
		if row, ok := rows[c]; ok {
			ordered = append(ordered, row)
		}
	}
	return apply.NewResult(source, scope, !failed, ordered)
}

func scopedPrior(scope apply.Scope, harness ir.HarnessID, store registry.Store) map[cell.Cell]registry.Record {
	out := map[cell.Cell]registry.Record{}
	for _, c := range cell.CanonicalCells() {
		if c.Harness() != harness {
			continue
		}
		key, err := scope.Key(c)
		if err != nil {
			continue
		}
		if record, ok := store.Lookup(key); ok {
			out[c] = record
		}
	}
	return out
}

func harnessActivations(harness ir.HarnessID, bindings map[cell.Cell]activation.ComponentActivation) map[cell.Cell]activation.ComponentActivation {
	out := map[cell.Cell]activation.ComponentActivation{}
	for _, extension := range cell.CanonicalExtensions() {
		c, err := cell.New(harness, extension)
		if err == nil {
			out[c] = bindings[c]
		}
	}
	return out
}

func removable(prior registry.Record) bool {
	return prior.Managed() && (prior.Observation() == registry.ObservationInstalled || prior.Observation() == registry.ObservationUnknown)
}
func management(prior *registry.Record) apply.Management {
	if prior == nil {
		return apply.ManagementUnknown
	}
	return recordManagement(*prior)
}
func recordManagement(record registry.Record) apply.Management {
	if record.Managed() {
		return apply.ManagementPasture
	}
	return apply.ManagementExternal
}
func sourceRegistry(source apply.Source) registry.Source {
	if source == apply.HomeManagerSource() {
		return registry.SourceHomeManager
	}
	return registry.SourceInstaller
}
func remediationFor(source apply.Source) apply.Remediation {
	if source == apply.HomeManagerSource() {
		return apply.RemediationRerunHomeManager
	}
	return apply.RemediationRerunInstaller
}
func actionable(what string, cause error, c cell.Cell, operation, impact, fix string) string {
	return fmt.Sprintf("%s: %v; where: %s; when: %s; impact: %s; fix: %s", what, cause, c, operation, impact, fix)
}
func preplan(source apply.Source, stage, reason, where, impact, fix string, remediation apply.Remediation) *apply.ApplyError {
	return apply.NewApplyError(source, stage, reason, where, impact, fix, remediation)
}
func normalizePreplan(source apply.Source, err error, remediation apply.Remediation) *apply.ApplyError {
	if typed, ok := err.(*apply.ApplyError); ok {
		return typed
	}
	return preplan(source, "pre-plan validation", err.Error(), "internal/install/service", "the complete request was rejected before mutation", "repair the reported contract or state and retry", remediation)
}
