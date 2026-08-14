package apply

import (
	"context"
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/selection"
)

// Source identifies the controller applying desired state.
type Source uint8

const (
	SourceInvalid Source = iota
	SourceInstaller
	SourceHomeManager
)

func InstallerSource() Source   { return SourceInstaller }
func HomeManagerSource() Source { return SourceHomeManager }
func (s Source) String() string {
	switch s {
	case SourceInstaller:
		return "installer"
	case SourceHomeManager:
		return "home-manager"
	default:
		return "invalid"
	}
}
func (s Source) IsValid() bool { return s == SourceInstaller || s == SourceHomeManager }

// Scope selects one logical registry table. Project scope carries one canonical
// root, allowing the same service to be reused by project reconciliation.
type Scope struct {
	kind registry.Scope
	root registry.ProjectRoot
}

func GlobalScope() Scope { return Scope{kind: registry.ScopeGlobal} }
func ProjectScope(root registry.ProjectRoot) (Scope, error) {
	if !root.IsValid() {
		return Scope{}, cell.NewFault("apply scope construction", "canonical project root", "the project root is invalid", "internal/install/apply.ProjectScope", "constructing an apply scope", "project records could collide or enter the global table", "construct the root with registry.CanonicalProjectRoot", nil)
	}
	return Scope{kind: registry.ScopeProject, root: root}, nil
}
func (s Scope) Kind() registry.Scope              { return s.kind }
func (s Scope) ProjectRoot() registry.ProjectRoot { return s.root }
func (s Scope) IsValid() bool {
	return s.kind == registry.ScopeGlobal && !s.root.IsValid() || s.kind == registry.ScopeProject && s.root.IsValid()
}
func (s Scope) key(c cell.Cell) (registry.Key, error) {
	if s.kind == registry.ScopeProject {
		return registry.ProjectKey(s.root, c)
	}
	if s.kind == registry.ScopeGlobal {
		return registry.GlobalKey(c)
	}
	return registry.Key{}, cell.NewFault("apply scope resolution", "global or project scope", "the scope is invalid", "internal/install/apply.Scope.key", "planning a scoped cell", "the confirmed fact has no logical registry table", "use GlobalScope or ProjectScope", nil)
}

// Outcome is one live-inspected activator result. Record is the strongest
// confirmed fact and is persisted before execution advances to another cell.
type Outcome struct {
	Status      Status
	Observation registry.Observation
	Record      *registry.Record
	Diagnostic  string
}

// Activator is the production boundary implemented by each closed activation
// strategy. Inspect must be read-only. Ensure and Remove must return facts based
// on a live postcondition check, including when they return an action error.
type Activator interface {
	StrategyKind() activation.StrategyKind
	Inspect(context.Context, Source, registry.Key, activation.ComponentActivation, *registry.Record) (Outcome, error)
	Ensure(context.Context, Source, registry.Key, activation.ComponentActivation, *registry.Record) (Outcome, error)
	Remove(context.Context, Source, registry.Key, activation.ComponentActivation, registry.Record) (Outcome, error)
}

// Committer persists a fully updated registry atomically. It is called after
// every confirmed fact and before the next external action.
type Committer func(context.Context, registry.Store) error

type Engine struct{ activators map[string]Activator }

func NewEngine(activators ...Activator) (*Engine, error) {
	reg := make(map[string]Activator, len(activators))
	for _, a := range activators {
		if a == nil || !a.StrategyKind().IsValid() {
			return nil, cell.NewFault("engine construction", "valid strategy activator", "an activator is nil or reports an invalid strategy", "internal/install/apply.NewEngine", "assembling the apply engine", "strategy dispatch cannot be exhaustive", "register one non-nil activator for each used strategy", nil)
		}
		kind := a.StrategyKind().String()
		if _, dup := reg[kind]; dup {
			return nil, cell.NewFault("engine construction", "one activator per strategy kind", fmt.Sprintf("two activators were registered for %q", kind), "internal/install/apply.NewEngine", "assembling the apply engine", "strategy dispatch would be ambiguous", "register exactly one activator per strategy kind", nil)
		}
		reg[kind] = a
	}
	return &Engine{activators: reg}, nil
}

type planned struct {
	key         registry.Key
	cell        cell.Cell
	op          Operation
	act         activation.ComponentActivation
	prior       *registry.Record
	declarative bool
}

// ApplySelection validates every contract and builds the complete canonical
// plan before the first mutation, then executes sequentially and stops after the
// first failed row. Confirmed facts are saved after each attempted action.
func (e *Engine) ApplySelection(ctx context.Context, sel selection.Selection, scope Scope, source Source, contracts map[ir.HarnessID]activation.ActivationContract, store *registry.Store, commit Committer) (Result, error) {
	if !sel.IsValid() {
		return Result{}, e.preplanError(source, "selection validation", "the effective selection is invalid", "internal/install/apply.Engine.ApplySelection", "no cell can be applied", "provide an exhaustive selection from selection.Parse or preferences.EffectiveSelection", RemediationRerunInstaller)
	}
	bindings, err := e.bindAll(source, contracts)
	if err != nil {
		return Result{}, err
	}
	plan := make([]planned, 0, 9)
	for _, state := range sel.Ordered() {
		key, keyErr := scope.key(state.Cell)
		if keyErr != nil {
			return Result{}, e.wrapPreplan(source, keyErr)
		}
		prior, hasPrior := store.Lookup(key)
		act := bindings[state.Cell.String()]
		declarative := source == SourceHomeManager && act.Strategy().Kind() == activation.DirectFileKindValue()
		var op Operation
		switch {
		case declarative:
			op = Inspect()
		case state.Enabled:
			op = Ensure()
		case hasPrior && removable(prior):
			op = RemoveOp()
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
	return e.executePlan(ctx, source, plan, store, commit), nil
}

// ApplyCell uses the identical binding, inspection, execution, and persistence
// path as ApplySelection. It is intentionally only a single-cell remediation;
// selection-wide native migrations may reject it in their activator.
func (e *Engine) ApplyCell(ctx context.Context, c cell.Cell, enabled bool, scope Scope, source Source, contracts map[ir.HarnessID]activation.ActivationContract, store *registry.Store, commit Committer) (Result, error) {
	bindings, err := e.bindAll(source, contracts)
	if err != nil {
		return Result{}, err
	}
	if !c.IsValid() {
		return Result{}, e.preplanError(source, "cell validation", "the requested cell is invalid", "internal/install/apply.Engine.ApplyCell", "no cell can be applied", "construct the cell with cell.New", RemediationApplyCell)
	}
	key, keyErr := scope.key(c)
	if keyErr != nil {
		return Result{}, e.wrapPreplan(source, keyErr)
	}
	prior, hasPrior := store.Lookup(key)
	act := bindings[c.String()]
	declarative := source == SourceHomeManager && act.Strategy().Kind() == activation.DirectFileKindValue()
	op := Inspect()
	switch {
	case declarative:
	case enabled:
		op = Ensure()
	case hasPrior && removable(prior):
		op = RemoveOp()
	default:
		management := ManagementUnknown
		if hasPrior && prior.Managed() {
			management = ManagementPasture
		} else if hasPrior {
			management = ManagementExternal
		}
		return Result{source: source, scope: scope.kind, ok: true, rows: []ActionRow{{cell: c, operation: Inspect(), status: NoOp(), management: management, diagnostic: "desired state is false and no Pasture-managed installed or unknown fact authorizes removal"}}}, nil
	}
	var ptr *registry.Record
	if hasPrior {
		copy := prior
		ptr = &copy
	}
	return e.executePlan(ctx, source, []planned{{key: key, cell: c, op: op, act: act, prior: ptr, declarative: declarative}}, store, commit), nil
}

func (e *Engine) bindAll(source Source, contracts map[ir.HarnessID]activation.ActivationContract) (map[string]activation.ComponentActivation, error) {
	if !source.IsValid() {
		return nil, e.preplanError(source, "source validation", "the apply source is neither installer nor home-manager", "internal/install/apply.Engine.bindAll", "ownership rules cannot be selected", "use InstallerSource or HomeManagerSource", RemediationManualRepair)
	}
	bindings := make(map[string]activation.ComponentActivation, 9)
	for _, c := range cell.CanonicalCells() {
		contract, ok := contracts[c.Harness()]
		if !ok || !contract.IsValid() || contract.Harness() != c.Harness() {
			return nil, e.preplanError(source, "activation contract validation", fmt.Sprintf("harness %s has no matching valid activation contract", c.Harness()), "internal/install/apply.Engine.bindAll", "the nine-cell plan is incomplete and no mutation was started", fmt.Sprintf("wire one exhaustive activation contract for %s", c.Harness()), RemediationRerunInstaller)
		}
		desc, descriptorErr := activation.NewComponentDescriptor(c)
		if descriptorErr != nil {
			return nil, e.wrapPreplan(source, descriptorErr)
		}
		act, lookupErr := activation.LookupComponentActivation(contract, desc)
		if lookupErr != nil {
			return nil, e.wrapPreplan(source, lookupErr)
		}
		if _, ok := e.activators[act.Strategy().Kind().String()]; !ok {
			return nil, e.preplanError(source, "activator validation", fmt.Sprintf("cell %s uses %s but no production activator is registered", c, act.Strategy().Kind()), "internal/install/apply.Engine.bindAll", "execution would stop only after earlier cells had mutated", "register the strategy activator before applying", RemediationManualRepair)
		}
		bindings[c.String()] = act
	}
	return bindings, nil
}

func removable(prior registry.Record) bool {
	return prior.Managed() && (prior.Observation() == registry.ObservationInstalled || prior.Observation() == registry.ObservationUnknown)
}

func (e *Engine) executePlan(ctx context.Context, source Source, plan []planned, store *registry.Store, commit Committer) Result {
	result := Result{source: source, ok: true}
	if len(plan) > 0 {
		result.scope = plan[0].key.Scope()
	}
	failed := false
	for _, p := range plan {
		if failed {
			management := ManagementUnknown
			if p.prior != nil && p.prior.Managed() {
				management = ManagementPasture
			} else if p.prior != nil {
				management = ManagementExternal
			}
			result.rows = append(result.rows, ActionRow{cell: p.cell, operation: p.op, status: Unattempted(), management: management, diagnostic: "an earlier canonical cell failed; this cell was not attempted"})
			continue
		}
		row, outcome := e.executeOne(ctx, source, p)
		if outcome.Record != nil {
			if outcome.Record.Managed() {
				row.management = ManagementPasture
			} else {
				row.management = ManagementExternal
			}
			if upsertErr := store.Upsert(*outcome.Record); upsertErr != nil {
				row.status, row.diagnostic = Failed(), actionable("confirmed registry fact could not be staged", upsertErr, p.cell, "upsert", "the next cell was not attempted", "repair the record construction and retry")
			} else if commit != nil {
				if saveErr := commit(ctx, *store); saveErr != nil {
					row.status, row.diagnostic = Failed(), actionable("confirmed registry fact could not be saved atomically", saveErr, p.cell, "registry-save", "the live component may have changed but the previous registry remains authoritative; later cells were not attempted", "inspect status, repair the registry path or permissions, and rerun the same apply operation")
				}
			}
		}
		result.rows = append(result.rows, row)
		if row.status == Failed() {
			failed, result.ok = true, false
		}
	}
	return result
}

func (e *Engine) executeOne(ctx context.Context, source Source, p planned) (ActionRow, Outcome) {
	activator := e.activators[p.act.Strategy().Kind().String()]
	if err := ctx.Err(); err != nil {
		return ActionRow{cell: p.cell, operation: p.op, status: Failed(), diagnostic: actionable("cell execution was canceled", err, p.cell, "pre-inspection", "this and later cells were not mutated", "retry with a live context")}, Outcome{}
	}
	if p.declarative {
		out, err := activator.Inspect(ctx, source, p.key, p.act, p.prior)
		if err != nil {
			return ActionRow{cell: p.cell, operation: Inspect(), status: Failed(), observation: out.Observation, diagnostic: actionable("declarative cell inspection failed", err, p.cell, "live-inspect", "Home Manager ownership was preserved but live state is unknown", "repair the destination and rerun Home Manager")}, out
		}
		return ActionRow{cell: p.cell, operation: Inspect(), status: ManagedDeclaratively(), management: ManagementDeclarative, observation: out.Observation, diagnostic: out.Diagnostic}, Outcome{Status: ManagedDeclaratively(), Observation: out.Observation}
	}
	// Every mutating action begins with a real read-only inspection. The action
	// implementation performs its own postcondition inspection before returning.
	if _, err := activator.Inspect(ctx, source, p.key, p.act, p.prior); err != nil {
		return ActionRow{cell: p.cell, operation: p.op, status: Failed(), observation: registry.ObservationUnknown, diagnostic: actionable("pre-action live inspection failed", err, p.cell, "live-inspect", "no action was attempted and later cells were stopped", "repair the reported live-state conflict and retry")}, Outcome{}
	}
	var out Outcome
	var err error
	if p.op == Ensure() {
		out, err = activator.Ensure(ctx, source, p.key, p.act, p.prior)
	} else {
		out, err = activator.Remove(ctx, source, p.key, p.act, *p.prior)
	}
	if err != nil {
		return ActionRow{cell: p.cell, operation: p.op, status: Failed(), observation: out.Observation, diagnostic: actionable("activation action failed after live inspection", err, p.cell, p.op.String(), "this cell records only confirmed or unknown state and later cells were not attempted", "repair the reported destination or manager state, then rerun the same apply operation")}, out
	}
	if out.Record == nil || !out.Record.IsValid() || out.Record.Key() != p.key {
		err := fmt.Errorf("activator returned no valid confirmed record for scoped key %s/%s", p.key.Scope(), p.cell)
		return ActionRow{cell: p.cell, operation: p.op, status: Failed(), observation: out.Observation, diagnostic: actionable("activation postcondition was not representable", err, p.cell, "post-inspection", "the action may have changed live state but no unproved fact was persisted; later cells were stopped", "inspect the cell and rerun after repairing the activator contract")}, Outcome{Observation: out.Observation}
	}
	management := ManagementUnknown
	if out.Record.Managed() {
		management = ManagementPasture
	} else {
		management = ManagementExternal
	}
	return ActionRow{cell: p.cell, operation: p.op, status: out.Status, management: management, observation: out.Observation, diagnostic: out.Diagnostic}, out
}

func actionable(what string, cause error, c cell.Cell, operation, impact, fix string) string {
	return fmt.Sprintf("%s: %v; where: %s; when: %s; impact: %s; fix: %s", what, cause, c, operation, impact, fix)
}

func (e *Engine) preplanError(source Source, stage, reason, where, impact, fix string, remediation Remediation) *ApplyError {
	return &ApplyError{source: source, stage: stage, reason: reason, where: where, impact: impact, fix: fix, remediation: remediation}
}
func (e *Engine) wrapPreplan(source Source, err error) *ApplyError {
	return e.preplanError(source, "activation binding", err.Error(), "internal/install/apply.Engine.bindAll", "the complete plan could not be validated before mutation", "repair the activation contracts and retry", RemediationManualRepair)
}
