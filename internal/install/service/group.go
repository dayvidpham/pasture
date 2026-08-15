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

type GroupSelection struct {
	Selection  selection.Selection
	Scope      apply.Scope
	Source     apply.Source
	Prior      map[cell.Cell]registry.Record
	Activation map[cell.Cell]activation.ComponentActivation
}

type GroupCell struct {
	Cell       cell.Cell
	Enabled    bool
	Scope      apply.Scope
	Source     apply.Source
	Prior      map[cell.Cell]registry.Record
	Activation map[cell.Cell]activation.ComponentActivation
}

// GroupResultCell is one of the exactly three canonical output cells. Result
// cells are intentionally independent from native action count.
type GroupResultCell struct {
	cell cell.Cell
	key  registry.Key
	op   apply.Operation
}

func NewGroupResultCell(c cell.Cell, key registry.Key, operation apply.Operation) (GroupResultCell, error) {
	if !c.IsValid() || key.Cell() != c || !operation.IsValid() {
		return GroupResultCell{}, fmt.Errorf("group result construction failed: cell %s, scoped key, and operation must match; where: internal/install/service.NewGroupResultCell; fix: derive the key from the request scope and use a typed operation", c)
	}
	return GroupResultCell{cell: c, key: key, op: operation}, nil
}
func (r GroupResultCell) Cell() cell.Cell            { return r.cell }
func (r GroupResultCell) Key() registry.Key          { return r.key }
func (r GroupResultCell) Operation() apply.Operation { return r.op }

type GroupActionKind uint8

const (
	groupActionInvalid GroupActionKind = iota
	groupActionInspect
	groupActionRemoveShared
	groupActionRemoveCell
	groupActionEnsureCell
)

func InspectGroupAction() GroupActionKind      { return groupActionInspect }
func EnsureCellGroupAction() GroupActionKind   { return groupActionEnsureCell }
func RemoveSharedGroupAction() GroupActionKind { return groupActionRemoveShared }
func RemoveCellGroupAction() GroupActionKind   { return groupActionRemoveCell }
func (k GroupActionKind) IsValid() bool        { return k >= groupActionInspect && k <= groupActionEnsureCell }
func (k GroupActionKind) operation() apply.Operation {
	if k == groupActionInspect {
		return apply.Inspect()
	}
	if k == groupActionEnsureCell {
		return apply.Ensure()
	}
	return apply.RemoveOp()
}

// GroupStep is one typed native action. It contains no command, callback,
// argument vector, or session identity.
type GroupStep struct {
	kind    GroupActionKind
	control cell.Cell
}

func NewGroupStep(kind GroupActionKind, control cell.Cell) (GroupStep, error) {
	if !kind.IsValid() || !control.IsValid() {
		return GroupStep{}, fmt.Errorf("group action construction failed: action kind and control cell must be valid; where: internal/install/service.NewGroupStep; fix: use a typed group action accessor and canonical cell")
	}
	if kind == groupActionRemoveShared && control.Extension() != cell.SkillsAxis() {
		return GroupStep{}, fmt.Errorf("group action construction failed: shared removal control must be the canonical skills cell; where: internal/install/service.NewGroupStep; fix: use the group's skills cell")
	}
	return GroupStep{kind: kind, control: control}, nil
}
func (s GroupStep) Kind() GroupActionKind      { return s.kind }
func (s GroupStep) ControlCell() cell.Cell     { return s.control }
func (s GroupStep) Operation() apply.Operation { return s.kind.operation() }

type GroupPlan struct {
	results []GroupResultCell
	actions []GroupStep
}

func NewGroupPlan(results []GroupResultCell, actions []GroupStep) (GroupPlan, error) {
	plan := GroupPlan{results: append([]GroupResultCell(nil), results...), actions: append([]GroupStep(nil), actions...)}
	if err := validateGroupShape(plan); err != nil {
		return GroupPlan{}, err
	}
	return plan, nil
}
func (p GroupPlan) Handled() bool { return len(p.results) != 0 || len(p.actions) != 0 }
func (p GroupPlan) ResultCells() []GroupResultCell {
	return append([]GroupResultCell(nil), p.results...)
}
func (p GroupPlan) Actions() []GroupStep { return append([]GroupStep(nil), p.actions...) }

func validateGroupShape(plan GroupPlan) error {
	if len(plan.results) != 3 {
		return fmt.Errorf("group plan construction failed: expected exactly three result cells, got %d; where: internal/install/service.NewGroupPlan; fix: provide skills, agents, and hooks result cells", len(plan.results))
	}
	if len(plan.actions) < 1 || len(plan.actions) > 7 {
		return fmt.Errorf("group plan construction failed: expected 1..7 actions, got %d; where: internal/install/service.NewGroupPlan; fix: provide a bounded native action sequence", len(plan.actions))
	}
	harness := plan.results[0].cell.Harness()
	if !harness.IsValid() {
		return fmt.Errorf("group plan construction failed: result harness is invalid; where: internal/install/service.NewGroupPlan; fix: use canonical harness cells")
	}
	for i, extension := range cell.CanonicalExtensions() {
		want, _ := cell.New(harness, extension)
		if plan.results[i].cell != want || plan.results[i].key.Cell() != want || !plan.results[i].op.IsValid() {
			return fmt.Errorf("group plan construction failed: result %d must be canonical cell %s with matching key; where: internal/install/service.NewGroupPlan; fix: provide canonical skills, agents, and hooks results", i, want)
		}
	}
	seen := map[GroupStep]struct{}{}
	lastPhase := GroupActionKind(0)
	for i, action := range plan.actions {
		if !action.kind.IsValid() || !action.control.IsValid() {
			return fmt.Errorf("group plan construction failed: action %d is invalid; where: internal/install/service.NewGroupPlan; fix: construct actions with NewGroupStep", i)
		}
		if action.control.Harness() != harness {
			return fmt.Errorf("group plan construction failed: action %d control cell %s is foreign; where: internal/install/service.NewGroupPlan; fix: use a control cell from %s", i, action.control, harness)
		}
		if action.kind == groupActionRemoveShared && action.control.Extension() != cell.SkillsAxis() {
			return fmt.Errorf("group plan construction failed: action %d shared-removal control is not skills; where: internal/install/service.NewGroupPlan; fix: use the canonical skills control cell", i)
		}
		if _, duplicate := seen[action]; duplicate {
			return fmt.Errorf("group plan construction failed: action %d duplicates kind/control; where: internal/install/service.NewGroupPlan; fix: remove the duplicate action", i)
		}
		seen[action] = struct{}{}
		if action.kind < lastPhase {
			return fmt.Errorf("group plan construction failed: action %d violates inspect, shared-remove, cell-remove, ensure phase order; where: internal/install/service.NewGroupPlan; fix: reorder typed actions by phase", i)
		}
		lastPhase = action.kind
	}
	return nil
}

type GroupAction struct {
	Row    apply.ActionRow
	Record *registry.Record
}

// GroupFacts is one complete, live-inspected three-cell fact set.
type GroupFacts struct{ actions [3]GroupAction }

func NewGroupFacts(actions ...GroupAction) (GroupFacts, error) {
	if len(actions) != 3 {
		return GroupFacts{}, fmt.Errorf("group facts construction failed: expected exactly three facts, got %d; where: internal/install/service.NewGroupFacts; fix: inspect all sibling cells", len(actions))
	}
	var facts GroupFacts
	copy(facts.actions[:], actions)
	harness := actions[0].Row.Cell().Harness()
	for i, extension := range cell.CanonicalExtensions() {
		want, _ := cell.New(harness, extension)
		if actions[i].Row.Cell() != want {
			return GroupFacts{}, fmt.Errorf("group facts construction failed: fact %d must be canonical cell %s; where: internal/install/service.NewGroupFacts; fix: return skills, agents, and hooks facts in canonical order", i, want)
		}
	}
	return facts, nil
}
func (f GroupFacts) Actions() []GroupAction { return append([]GroupAction(nil), f.actions[:]...) }

type GroupTerminalStage uint8

const (
	GroupTerminalInvalid GroupTerminalStage = iota
	GroupTerminalPlanInvalid
	GroupTerminalCanceled
	GroupTerminalExecuteFailed
	GroupTerminalInspectFailed
	GroupTerminalFactInvalid
	GroupTerminalUpsertFailed
	GroupTerminalSaveFailed
	GroupTerminalSucceeded
)

func (s GroupTerminalStage) IsValid() bool {
	return s >= GroupTerminalPlanInvalid && s <= GroupTerminalSucceeded
}

type GroupReconciler interface {
	Harness() ir.HarnessID
	PlanSelection(context.Context, GroupSelection) (GroupPlan, error)
	ExecuteAction(context.Context, GroupSelection, GroupPlan, GroupStep) error
	InspectAction(context.Context, GroupSelection, GroupPlan, GroupStep, error) (GroupFacts, error)
	ClosePlan(context.Context, GroupSelection, GroupPlan, GroupTerminalStage) error
	PreflightCell(context.Context, GroupCell) error
}
