package service_test

import (
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/service"
)

func groupParts(t *testing.T) ([]service.GroupResultCell, []service.GroupStep) {
	t.Helper()
	cells := make([]cell.Cell, 0, 3)
	results := make([]service.GroupResultCell, 0, 3)
	for _, extension := range cell.CanonicalExtensions() {
		c, _ := cell.New(artifact.HarnessClaudeCode, extension)
		key, _ := registry.GlobalKey(c)
		result, err := service.NewGroupResultCell(c, key, apply.Inspect())
		if err != nil {
			t.Fatal(err)
		}
		cells = append(cells, c)
		results = append(results, result)
	}
	specs := []struct {
		kind service.GroupActionKind
		cell cell.Cell
	}{
		{service.InspectGroupAction(), cells[0]},
		{service.InspectGroupAction(), cells[1]},
		{service.InspectGroupAction(), cells[2]},
		{service.RemoveSharedGroupAction(), cells[0]},
		{service.RemoveCellGroupAction(), cells[1]},
		{service.RemoveCellGroupAction(), cells[2]},
		{service.EnsureCellGroupAction(), cells[0]},
	}
	actions := make([]service.GroupStep, 0, len(specs))
	for _, spec := range specs {
		action, err := service.NewGroupStep(spec.kind, spec.cell)
		if err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	return results, actions
}

func TestGroupPlanAcceptsEveryBoundedActionCount(t *testing.T) {
	t.Parallel()
	results, actions := groupParts(t)
	for count := 1; count <= 7; count++ {
		if _, err := service.NewGroupPlan(results, actions[:count]); err != nil {
			t.Fatalf("count %d: %v", count, err)
		}
	}
}

func TestGroupPlanRejectsBoundsOrderDuplicatesAndForeignCells(t *testing.T) {
	t.Parallel()
	results, actions := groupParts(t)
	if _, err := service.NewGroupPlan(results, nil); err == nil {
		t.Fatal("zero actions accepted")
	}
	eight := append(append([]service.GroupStep(nil), actions...), actions[0])
	if _, err := service.NewGroupPlan(results, eight); err == nil {
		t.Fatal("eight actions accepted")
	}
	if _, err := service.NewGroupPlan(results, []service.GroupStep{actions[6], actions[0]}); err == nil {
		t.Fatal("out-of-order actions accepted")
	}
	if _, err := service.NewGroupPlan(results, []service.GroupStep{actions[0], actions[0]}); err == nil {
		t.Fatal("duplicate actions accepted")
	}
	foreign, _ := cell.New(artifact.HarnessCodex, cell.SkillsAxis())
	foreignAction, _ := service.NewGroupStep(service.InspectGroupAction(), foreign)
	if _, err := service.NewGroupPlan(results, []service.GroupStep{foreignAction}); err == nil {
		t.Fatal("foreign control cell accepted")
	}
	if _, err := service.NewGroupStep(service.RemoveSharedGroupAction(), results[1].Cell()); err == nil {
		t.Fatal("non-skills shared-removal control accepted")
	}
	swapped := append([]service.GroupResultCell(nil), results...)
	swapped[0], swapped[1] = swapped[1], swapped[0]
	if _, err := service.NewGroupPlan(swapped, actions[:1]); err == nil {
		t.Fatal("noncanonical result cells accepted")
	}
}
