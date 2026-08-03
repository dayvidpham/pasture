package apply

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/inventory"
	"github.com/dayvidpham/pasture/internal/install/selection"
)

// Source identifies the caller applying an effective selection.
type Source struct{ name string }

var (
	installerSource   = Source{name: "installer"}
	homeManagerSource = Source{name: "home-manager"}
)

// InstallerSource applies native and direct-file cells.
func InstallerSource() Source { return installerSource }

// HomeManagerSource applies native cells only; direct-file cells are inspected
// read-only as managed_declaratively.
func HomeManagerSource() Source { return homeManagerSource }

func (s Source) String() string { return s.name }
func (s Source) IsValid() bool {
	return s.name == installerSource.name || s.name == homeManagerSource.name
}

// Outcome is one activator result.
type Outcome struct {
	Status      Status
	Observation inventory.Observation
	// Record, when non-nil, is written to the confirmed inventory.
	Record     *inventory.Record
	Diagnostic string
}

// Activator activates or removes one strategy kind's cells.
type Activator interface {
	StrategyKind() activation.StrategyKind
	Ensure(c cell.Cell, act activation.ComponentActivation, prior *inventory.Record) (Outcome, error)
	Remove(c cell.Cell, act activation.ComponentActivation, prior inventory.Record) (Outcome, error)
}

// Engine executes an effective selection over a set of activation contracts.
type Engine struct {
	activators map[string]Activator
}

// NewEngine builds an engine from strategy activators. Registering more than one
// activator for the same strategy kind is rejected.
func NewEngine(activators ...Activator) (*Engine, error) {
	reg := make(map[string]Activator, len(activators))
	for _, a := range activators {
		kind := a.StrategyKind().String()
		if _, dup := reg[kind]; dup {
			return nil, cell.NewFault(
				"engine construction", "one activator per strategy kind",
				fmt.Sprintf("two activators were registered for %q", kind),
				"internal/install/apply.NewEngine", "assembling the apply engine",
				"strategy dispatch would be ambiguous",
				"register exactly one activator per strategy kind", nil,
			)
		}
		reg[kind] = a
	}
	return &Engine{activators: reg}, nil
}

// ApplySelection plans and executes sel under source. contracts binds each
// harness to its activation contract. inv is the prior confirmed inventory and
// is updated in place for native and installer direct-file mutations. A pre-plan
// problem returns a typed *ApplyError; per-row failures are embedded in the
// returned Result and set OK=false.
func (e *Engine) ApplySelection(
	sel selection.Selection,
	source Source,
	contracts map[ir.HarnessID]activation.ActivationContract,
	inv *inventory.Inventory,
) (Result, error) {
	if !sel.IsValid() {
		return Result{}, e.preplanError(source, "selection validation",
			"the effective selection is invalid",
			"internal/install/apply.ApplySelection",
			"no cell can be applied",
			"provide a validated selection from selection.Parse or preferences.EffectiveSelection", "")
	}
	if !source.IsValid() {
		return Result{}, e.preplanError(source, "source validation",
			"the apply source is neither installer nor home-manager",
			"internal/install/apply.ApplySelection",
			"the engine cannot decide which cells to mutate",
			"pass InstallerSource or HomeManagerSource", "")
	}

	// Build the plan first so a missing contract fails before any mutation.
	type planned struct {
		row      cell.Cell
		op       Operation
		act      activation.ComponentActivation
		priorPtr *inventory.Record
	}
	var plan []planned
	for _, cs := range sel.Ordered() {
		prior, hasPrior := inv.Lookup(cs.Cell)
		var op Operation
		switch {
		case cs.Enabled:
			op = Ensure()
		case hasPrior && removable(prior):
			op = RemoveOp()
		default:
			continue // never-recorded false, or absent tombstone: no row.
		}
		contract, ok := contracts[cs.Cell.Harness()]
		if !ok {
			return Result{}, e.preplanError(source, "activation contract lookup",
				fmt.Sprintf("no activation contract is wired for harness %s", cs.Cell.Harness()),
				"internal/install/apply.ApplySelection",
				fmt.Sprintf("cell %s cannot be activated by this build", cs.Cell),
				fmt.Sprintf("provide an activation contract for %s, or deselect that harness", cs.Cell.Harness()),
				"rerun_installer")
		}
		desc, err := activation.NewComponentDescriptor(cs.Cell)
		if err != nil {
			return Result{}, e.wrapPreplan(source, err)
		}
		act, err := activation.LookupComponentActivation(contract, desc)
		if err != nil {
			return Result{}, e.wrapPreplan(source, err)
		}
		var priorPtr *inventory.Record
		if hasPrior {
			p := prior
			priorPtr = &p
		}
		plan = append(plan, planned{row: cs.Cell, op: op, act: act, priorPtr: priorPtr})
	}

	// Execute in canonical order, stopping at the first failure.
	result := Result{source: source.String(), ok: true}
	failed := false
	for _, p := range plan {
		if failed {
			result.rows = append(result.rows, ActionRow{
				cell: p.row, operation: p.op, status: Unattempted(),
				diagnostic: "an earlier cell failed; this cell was not attempted",
			})
			continue
		}
		row, outcome := e.execute(p.row, p.op, p.act, source, p.priorPtr)
		result.rows = append(result.rows, row)
		if outcome.Record != nil {
			_ = inv.Upsert(*outcome.Record)
		}
		if row.status == Failed() {
			failed = true
			result.ok = false
		}
	}
	return result, nil
}

func removable(prior inventory.Record) bool {
	obs := prior.Observation()
	// managed installed/unknown cells are removed; absent tombstones are not.
	return prior.Managed() && (obs == inventory.Installed() || obs == inventory.Unknown())
}

func (e *Engine) execute(
	c cell.Cell, op Operation, act activation.ComponentActivation, source Source, prior *inventory.Record,
) (ActionRow, Outcome) {
	kind := act.Strategy().Kind()

	// Home Manager inspects direct-file cells read-only; it never mutates them.
	if source == HomeManagerSource() && kind == activation.DirectFileKindValue() {
		return ActionRow{
			cell: c, operation: Inspect(), status: ManagedDeclaratively(),
			diagnostic: "direct-file cells are realized by Home Manager's declarative links; the installer only inspects them",
		}, Outcome{Status: ManagedDeclaratively()}
	}

	activator, ok := e.activators[kind.String()]
	if !ok {
		return ActionRow{
			cell: c, operation: op, status: Failed(),
			diagnostic: fmt.Sprintf("this build registered no activator for the %s strategy; %s cannot be activated", kind, c),
		}, Outcome{Status: Failed()}
	}

	var outcome Outcome
	var err error
	switch op {
	case Ensure():
		outcome, err = activator.Ensure(c, act, prior)
	case RemoveOp():
		if prior == nil {
			return ActionRow{cell: c, operation: op, status: Failed(),
				diagnostic: "a remove was planned without a prior record; this is an internal inconsistency"}, Outcome{Status: Failed()}
		}
		outcome, err = activator.Remove(c, act, *prior)
	default:
		return ActionRow{cell: c, operation: op, status: Failed(),
			diagnostic: fmt.Sprintf("unsupported operation %s", op)}, Outcome{Status: Failed()}
	}
	if err != nil {
		return ActionRow{cell: c, operation: op, status: Failed(),
			observation: outcome.Observation, diagnostic: err.Error()}, Outcome{Status: Failed(), Record: outcome.Record}
	}
	return ActionRow{
		cell: c, operation: op, status: outcome.Status,
		observation: outcome.Observation, diagnostic: outcome.Diagnostic,
	}, outcome
}

func (e *Engine) preplanError(source Source, stage, reason, where, impact, fix, remediation string) *ApplyError {
	return &ApplyError{
		source: source.String(), stage: stage, reason: reason,
		where: where, impact: impact, fix: fix, remediation: remediation,
	}
}

func (e *Engine) wrapPreplan(source Source, err error) *ApplyError {
	return &ApplyError{
		source: source.String(), stage: "activation binding",
		reason: err.Error(), where: "internal/install/apply.ApplySelection",
		impact: "the effective selection could not be bound to activation strategies before mutation",
		fix:    "ensure the wired activation contracts cover every desired cell",
	}
}
