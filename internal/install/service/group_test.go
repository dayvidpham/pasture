package service_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
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
		{service.EnsureCellGroupAction(), cells[0]},
		{service.RemoveSharedGroupAction(), cells[0]},
		{service.RemoveCellGroupAction(), cells[1]},
		{service.RemoveCellGroupAction(), cells[2]},
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
	if _, err := service.NewGroupPlan(results, []service.GroupStep{actions[4], actions[3]}); err == nil {
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

func TestGroupConstructorsRejectResultCountsAndEveryDescendingPhaseBoundary(t *testing.T) {
	t.Parallel()
	results, actions := groupParts(t)
	for _, count := range []int{0, 1, 2, 4} {
		candidate := append([]service.GroupResultCell(nil), results...)
		for len(candidate) < count {
			candidate = append(candidate, results[0])
		}
		if len(candidate) > count {
			candidate = candidate[:count]
		}
		if _, err := service.NewGroupPlan(candidate, actions[:1]); err == nil || !strings.Contains(err.Error(), "where:") || !strings.Contains(err.Error(), "fix:") {
			t.Fatalf("result count %d error=%v", count, err)
		}
	}
	boundaries := [][2]int{{6, 4}, {6, 3}, {6, 0}, {5, 4}, {5, 3}, {5, 0}, {4, 3}, {4, 0}, {3, 0}}
	for _, pair := range boundaries {
		if _, err := service.NewGroupPlan(results, []service.GroupStep{actions[pair[0]], actions[pair[1]]}); err == nil {
			t.Fatalf("descending phase boundary %v accepted", pair)
		}
	}
	if _, err := service.NewGroupFacts(); err == nil {
		t.Fatal("zero facts accepted")
	}
}

type eventRegistry struct {
	mu             sync.Mutex
	store          registry.Store
	events         *[]string
	saveErr        error
	rejectCanceled bool
}

func (r *eventRegistry) Load(context.Context) (registry.Store, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.store, nil
}
func (r *eventRegistry) Save(ctx context.Context, store registry.Store) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.events = append(*r.events, "save")
	if r.rejectCanceled && ctx.Err() != nil {
		return fmt.Errorf("registry rejected canceled context: %w", ctx.Err())
	}
	if r.saveErr != nil {
		return r.saveErr
	}
	r.store = store
	return nil
}

type recordingGroup struct {
	mu               sync.Mutex
	events           *[]string
	planErr          error
	plan             func(service.GroupSelection) (service.GroupPlan, error)
	handledOnError   bool
	executeErr       error
	inspectErr       error
	inspectErrByCell map[cell.Cell]error
	closeErr         error
	cancel           context.CancelFunc
	inspectLive      bool
	malform          func(service.GroupSelection, service.GroupPlan, service.GroupStep, []service.GroupAction) []service.GroupAction
	closeCount       int
	planCount        int
	stage            service.GroupTerminalStage
	priorAt          map[string][][]string
	executed         []executedGroupStep
}

type executedGroupStep struct {
	kind      service.GroupActionKind
	operation apply.Operation
	control   cell.Cell
}

type nilMapGroup map[string]struct{}

func (nilMapGroup) Harness() ir.HarnessID { return artifact.HarnessClaudeCode }
func (nilMapGroup) PlanSelection(context.Context, service.GroupSelection) (service.GroupPlan, error) {
	return service.GroupPlan{}, nil
}
func (nilMapGroup) ExecuteAction(context.Context, service.GroupSelection, service.GroupPlan, service.GroupStep) error {
	return nil
}
func (nilMapGroup) InspectAction(context.Context, service.GroupSelection, service.GroupPlan, service.GroupStep, error) (service.GroupFacts, error) {
	return service.GroupFacts{}, nil
}
func (nilMapGroup) ClosePlan(context.Context, service.GroupSelection, service.GroupPlan, service.GroupTerminalStage) error {
	return nil
}
func (nilMapGroup) PreflightCell(context.Context, service.GroupCell) error { return nil }

func priorSignature(request service.GroupSelection) []string {
	result := make([]string, 0, len(cell.CanonicalExtensions()))
	for _, extension := range cell.CanonicalExtensions() {
		c, _ := cell.New(artifact.HarnessClaudeCode, extension)
		record, ok := request.Prior[c]
		if !ok {
			result = append(result, c.String()+":<absent>")
			continue
		}
		result = append(result, fmt.Sprintf("%s:%s:%s:%s:%s:%s", c, record.Observation(), record.Trust(), record.LastOperation(), record.LastOutcome(), record.Diagnostic()))
	}
	return result
}

func groupRegistryOperation(operation apply.Operation) registry.Operation {
	switch operation {
	case apply.Ensure():
		return registry.OperationEnsure
	case apply.RemoveOp():
		return registry.OperationRemove
	default:
		return registry.OperationInspect
	}
}

func (g *recordingGroup) capturePrior(stage string, request service.GroupSelection) {
	if g.priorAt == nil {
		g.priorAt = make(map[string][][]string)
	}
	g.priorAt[stage] = append(g.priorAt[stage], priorSignature(request))
}

func (*recordingGroup) Harness() ir.HarnessID { return artifact.HarnessClaudeCode }
func (g *recordingGroup) PlanSelection(_ context.Context, request service.GroupSelection) (service.GroupPlan, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.capturePrior("plan", request)
	*g.events = append(*g.events, "plan")
	g.planCount++
	if g.plan != nil {
		return g.plan(request)
	}
	if g.planErr != nil && !g.handledOnError {
		return service.GroupPlan{}, g.planErr
	}
	results := make([]service.GroupResultCell, 0, 3)
	for _, extension := range cell.CanonicalExtensions() {
		c, _ := cell.New(artifact.HarnessClaudeCode, extension)
		key, _ := request.Scope.Key(c)
		result, err := service.NewGroupResultCell(c, key, apply.Inspect())
		if err != nil {
			return service.GroupPlan{}, err
		}
		results = append(results, result)
	}
	steps := make([]service.GroupStep, 0, 2)
	for _, result := range results[:2] {
		step, _ := service.NewGroupStep(service.InspectGroupAction(), result.Cell())
		steps = append(steps, step)
	}
	plan, err := service.NewGroupPlan(results, steps)
	if err != nil {
		return service.GroupPlan{}, err
	}
	return plan, g.planErr
}

func selectionGroupPlan(t *testing.T, request service.GroupSelection, invalidKind service.GroupActionKind, invalidCell cell.Cell) service.GroupPlan {
	t.Helper()
	results := make([]service.GroupResultCell, 0, 3)
	actions := make([]service.GroupStep, 0, 7)
	claudeCells := make([]cell.Cell, 0, 3)
	for _, extension := range cell.CanonicalExtensions() {
		c, _ := cell.New(artifact.HarnessClaudeCode, extension)
		claudeCells = append(claudeCells, c)
		key, _ := request.Scope.Key(c)
		operation := apply.RemoveOp()
		if request.Selection.Enabled(c) {
			operation = apply.Ensure()
		}
		result, err := service.NewGroupResultCell(c, key, operation)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, result)
		inspect, _ := service.NewGroupStep(service.InspectGroupAction(), c)
		actions = append(actions, inspect)
	}
	for _, c := range claudeCells {
		if request.Selection.Enabled(c) {
			step, _ := service.NewGroupStep(service.EnsureCellGroupAction(), c)
			actions = append(actions, step)
		}
	}
	shared, _ := service.NewGroupStep(service.RemoveSharedGroupAction(), claudeCells[0])
	actions = append(actions, shared)
	for _, c := range claudeCells {
		if !request.Selection.Enabled(c) {
			step, _ := service.NewGroupStep(service.RemoveCellGroupAction(), c)
			actions = append(actions, step)
		}
	}
	if invalidKind.IsValid() {
		step, _ := service.NewGroupStep(invalidKind, invalidCell)
		replace := 3
		if invalidKind == service.RemoveCellGroupAction() {
			replace = len(actions) - 1
		}
		actions[replace] = step
	}
	plan, err := service.NewGroupPlan(results, actions)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestServiceGroupPlanSelectionConsistencyForEveryCombination(t *testing.T) {
	t.Parallel()
	for mask := 0; mask < 8; mask++ {
		mask := mask
		t.Run(fmt.Sprintf("selection-%03b", mask), func(t *testing.T) {
			request := groupRequest(t)
			request.Selection = all(t, func(c cell.Cell) bool {
				if c.Harness() != artifact.HarnessClaudeCode {
					return false
				}
				return mask&(1<<(c.Extension()-1)) != 0
			})
			events := []string{}
			group := &recordingGroup{events: &events}
			group.plan = func(selection service.GroupSelection) (service.GroupPlan, error) {
				return selectionGroupPlan(t, selection, 0, cell.Cell{}), nil
			}
			result, err := groupService(t, group, nil).ApplySelection(context.Background(), request)
			if err != nil || !result.OK() || len(result.Rows()) != 3 || group.closeCount != 1 {
				t.Fatalf("valid selection plan rejected: result=%+v err=%v events=%v closes=%d", result.Rows(), err, events, group.closeCount)
			}
			for i, extension := range cell.CanonicalExtensions() {
				want, _ := cell.New(artifact.HarnessClaudeCode, extension)
				row := result.Rows()[i]
				if row.Cell() != want || row.Status() != apply.Completed() {
					t.Fatalf("row %d=%+v, want canonical completed row for %s", i, row, want)
				}
				wantOp := apply.RemoveOp()
				if request.Selection.Enabled(want) {
					wantOp = apply.Ensure()
				}
				if row.Operation() != wantOp {
					t.Fatalf("row %d operation=%s, want %s", i, row.Operation(), wantOp)
				}
			}
			wantSteps := make([]executedGroupStep, 0, 7)
			for _, c := range []cell.Cell{
				mustCell(t, artifact.HarnessClaudeCode, cell.SkillsAxis()),
				mustCell(t, artifact.HarnessClaudeCode, cell.AgentsAxis()),
				mustCell(t, artifact.HarnessClaudeCode, cell.HooksAxis()),
			} {
				wantSteps = append(wantSteps, executedGroupStep{kind: service.InspectGroupAction(), operation: apply.Inspect(), control: c})
			}
			for _, c := range []cell.Cell{
				mustCell(t, artifact.HarnessClaudeCode, cell.SkillsAxis()),
				mustCell(t, artifact.HarnessClaudeCode, cell.AgentsAxis()),
				mustCell(t, artifact.HarnessClaudeCode, cell.HooksAxis()),
			} {
				if request.Selection.Enabled(c) {
					wantSteps = append(wantSteps, executedGroupStep{kind: service.EnsureCellGroupAction(), operation: apply.Ensure(), control: c})
				}
			}
			wantSteps = append(wantSteps, executedGroupStep{kind: service.RemoveSharedGroupAction(), operation: apply.RemoveOp(), control: mustCell(t, artifact.HarnessClaudeCode, cell.SkillsAxis())})
			for _, c := range []cell.Cell{
				mustCell(t, artifact.HarnessClaudeCode, cell.SkillsAxis()),
				mustCell(t, artifact.HarnessClaudeCode, cell.AgentsAxis()),
				mustCell(t, artifact.HarnessClaudeCode, cell.HooksAxis()),
			} {
				if !request.Selection.Enabled(c) {
					wantSteps = append(wantSteps, executedGroupStep{kind: service.RemoveCellGroupAction(), operation: apply.RemoveOp(), control: c})
				}
			}
			if !reflect.DeepEqual(group.executed, wantSteps) {
				t.Fatalf("executed group steps=%v, want exact sequence=%v", group.executed, wantSteps)
			}
			if saves := strings.Count(fmt.Sprint(events), "save"); saves != len(wantSteps) {
				t.Fatalf("events=%v, want %d per-action saves", events, len(wantSteps))
			}
		})
	}
}

func mustCell(t *testing.T, harness ir.HarnessID, extension cell.Extension) cell.Cell {
	t.Helper()
	c, err := cell.New(harness, extension)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestServiceRejectsNewExternalInstalledGroupFactWithoutRecord(t *testing.T) {
	t.Parallel()
	request := groupRequest(t)
	request.Selection = all(t, func(c cell.Cell) bool {
		return c == mustCell(t, artifact.HarnessClaudeCode, cell.SkillsAxis())
	})
	events := []string{}
	group := &recordingGroup{events: &events}
	group.plan = func(selection service.GroupSelection) (service.GroupPlan, error) {
		results := make([]service.GroupResultCell, 0, 3)
		for _, extension := range cell.CanonicalExtensions() {
			c := mustCell(t, artifact.HarnessClaudeCode, extension)
			operation := apply.Inspect()
			if selection.Selection.Enabled(c) {
				operation = apply.Ensure()
			}
			key, err := selection.Scope.Key(c)
			if err != nil {
				return service.GroupPlan{}, err
			}
			result, err := service.NewGroupResultCell(c, key, operation)
			if err != nil {
				return service.GroupPlan{}, err
			}
			results = append(results, result)
		}
		step, err := service.NewGroupStep(service.EnsureCellGroupAction(), results[0].Cell())
		if err != nil {
			return service.GroupPlan{}, err
		}
		return service.NewGroupPlan(results, []service.GroupStep{step})
	}
	group.malform = func(_ service.GroupSelection, _ service.GroupPlan, step service.GroupStep, actions []service.GroupAction) []service.GroupAction {
		if step.Operation() != apply.Ensure() {
			return actions
		}
		row := actions[0].Row()
		row = apply.NewActionRow(row.Cell(), row.Operation(), apply.Completed(), apply.ManagementExternal, registry.ObservationInstalled, "external installed fact")
		return replaceGroupAction(t, 0, row, nil, actions)
	}
	result, err := groupService(t, group, nil).ApplySelection(context.Background(), request)
	if err != nil || result.OK() || group.stage != service.GroupTerminalFactInvalid || group.closeCount != 1 || strings.Contains(strings.Join(events, ","), "save") {
		t.Fatalf("result=%+v err=%v events=%v stage=%v closes=%d", result.Rows(), err, events, group.stage, group.closeCount)
	}
}

func replaceGroupAction(t *testing.T, index int, row apply.ActionRow, record *registry.Record, actions []service.GroupAction) []service.GroupAction {
	t.Helper()
	action, err := service.NewGroupAction(row, record)
	if err != nil {
		t.Fatal(err)
	}
	actions[index] = action
	return actions
}

func TestServiceGroupPlanRejectsSelectionContradictions(t *testing.T) {
	t.Parallel()
	selected, _ := cell.New(artifact.HarnessClaudeCode, cell.SkillsAxis())
	disabled, _ := cell.New(artifact.HarnessClaudeCode, cell.AgentsAxis())
	for _, tc := range []struct {
		name string
		kind service.GroupActionKind
		cell cell.Cell
	}{
		{name: "ensure disabled", kind: service.EnsureCellGroupAction(), cell: disabled},
		{name: "remove selected", kind: service.RemoveCellGroupAction(), cell: selected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := groupRequest(t)
			request.Selection = all(t, func(c cell.Cell) bool { return c == selected })
			events := []string{}
			group := &recordingGroup{events: &events}
			group.plan = func(selection service.GroupSelection) (service.GroupPlan, error) {
				return selectionGroupPlan(t, selection, tc.kind, tc.cell), nil
			}
			_, err := groupService(t, group, nil).ApplySelection(context.Background(), request)
			var applyErr *apply.ApplyError
			if !errors.As(err, &applyErr) || applyErr.Stage() != "pre-plan validation" || group.closeCount != 1 || strings.Contains(fmt.Sprint(events), "execute") {
				t.Fatalf("err=%v events=%v closes=%d", err, events, group.closeCount)
			}
		})
	}
}

func TestGroupRejectsTypedNilAndZeroFacts(t *testing.T) {
	t.Parallel()
	var typedNil *recordingGroup
	root := t.TempDir()
	contractSet := contracts(t, root)
	_, err := service.New(service.Config{Registry: &eventRegistry{store: registry.New(), events: &[]string{}}, Contracts: contractSet, Activators: []apply.Activator{directFileActivator(t, contractSet)}, Group: typedNil})
	if err == nil || !strings.Contains(err.Error(), "typed nil") {
		t.Fatalf("typed nil error=%v", err)
	}
	var typedNilMap nilMapGroup
	_, err = service.New(service.Config{Registry: &eventRegistry{store: registry.New(), events: &[]string{}}, Contracts: contractSet, Activators: []apply.Activator{directFileActivator(t, contractSet)}, Group: typedNilMap})
	if err == nil || !strings.Contains(err.Error(), "typed nil") {
		t.Fatalf("named non-pointer typed nil error=%v", err)
	}
	if _, err := service.NewGroupAction(apply.ActionRow{}, nil); err == nil {
		t.Fatal("zero GroupAction accepted")
	}
	if _, err := service.NewGroupFacts(service.GroupAction{}, service.GroupAction{}, service.GroupAction{}); err == nil {
		t.Fatal("forged zero GroupActions accepted")
	}
}

func TestServiceRejectsTypedNilRegistryAndNilReceivers(t *testing.T) {
	t.Parallel()
	var typedRegistry *eventRegistry
	if _, err := service.New(service.Config{Registry: typedRegistry}); err == nil || !strings.Contains(err.Error(), "registry dependency is nil") {
		t.Fatalf("typed nil registry error=%v", err)
	}
	var svc *service.Service
	selectionRequest := groupRequest(t)
	if _, err := svc.ApplySelection(context.Background(), selectionRequest); err == nil || !strings.Contains(err.Error(), "service receiver is nil") {
		t.Fatalf("nil ApplySelection error=%v", err)
	}
	c, _ := cell.New(artifact.HarnessClaudeCode, cell.SkillsAxis())
	if _, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Scope: apply.GlobalScope(), Source: apply.InstallerSource()}); err == nil || !strings.Contains(err.Error(), "service receiver is nil") {
		t.Fatalf("nil ApplyCell error=%v", err)
	}
}
func (g *recordingGroup) ExecuteAction(_ context.Context, request service.GroupSelection, _ service.GroupPlan, step service.GroupStep) error {
	g.mu.Lock()
	g.capturePrior("execute", request)
	*g.events = append(*g.events, "execute:"+step.ControlCell().String())
	g.executed = append(g.executed, executedGroupStep{kind: step.Kind(), operation: step.Operation(), control: step.ControlCell()})
	g.mu.Unlock()
	if g.cancel != nil {
		g.cancel()
	}
	return g.executeErr
}
func (g *recordingGroup) InspectAction(ctx context.Context, request service.GroupSelection, plan service.GroupPlan, step service.GroupStep, executeErr error) (service.GroupFacts, error) {
	g.mu.Lock()
	g.capturePrior("inspect", request)
	*g.events = append(*g.events, "inspect:"+step.ControlCell().String())
	g.inspectLive = ctx.Err() == nil
	g.mu.Unlock()
	if err := g.inspectErrByCell[step.ControlCell()]; err != nil {
		return service.GroupFacts{}, err
	}
	if g.inspectErr != nil {
		return service.GroupFacts{}, g.inspectErr
	}
	actions := make([]service.GroupAction, 0, 3)
	for _, result := range plan.ResultCells() {
		control := result.Cell() == step.ControlCell()
		status := apply.Completed()
		outcome := registry.OutcomeCompleted
		if control && executeErr != nil {
			status = apply.Failed()
			outcome = registry.OutcomeFailed
		}
		lastOperation := registry.OperationInspect
		if control {
			lastOperation = groupRegistryOperation(step.Operation())
		}
		record, err := registry.NewRecord(registry.RecordInput{Key: result.Key(), Source: registry.SourceInstaller, Strategy: request.Activation[result.Cell()].Strategy().Kind(), Managed: true, Observation: registry.ObservationAbsent, Trust: registry.TrustNotApplicable, LastOperation: lastOperation, LastOutcome: outcome, Diagnostic: "live fact"})
		if err != nil {
			return service.GroupFacts{}, err
		}
		action, err := service.NewGroupAction(apply.NewActionRow(result.Cell(), result.Operation(), status, apply.ManagementPasture, registry.ObservationAbsent, "live fact"), &record)
		if err != nil {
			return service.GroupFacts{}, err
		}
		actions = append(actions, action)
	}
	if g.malform != nil {
		actions = g.malform(request, plan, step, actions)
	}
	if len(actions) != 3 {
		return service.GroupFacts{}, nil
	}
	return service.NewGroupFacts(actions...)
}

func groupStoreRecords(t *testing.T, repo *eventRegistry) map[cell.Cell]registry.Record {
	t.Helper()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	result := make(map[cell.Cell]registry.Record)
	for _, record := range repo.store.Ordered() {
		result[record.Cell()] = record
	}
	return result
}

func assertGroupRow(t *testing.T, row apply.ActionRow, wantStatus apply.Status, wantOperation apply.Operation, wantManagement apply.Management, wantObservation registry.Observation, wantRecord bool) {
	t.Helper()
	if row.Status() != wantStatus || row.Operation() != wantOperation || row.Management() != wantManagement || row.Observation() != wantObservation {
		t.Fatalf("row %s = status=%s operation=%s management=%s observation=%s, want %s/%s/%s/%s", row.Cell(), row.Status(), row.Operation(), row.Management(), row.Observation(), wantStatus, wantOperation, wantManagement, wantObservation)
	}
	if wantRecord && row.Diagnostic() == "" {
		t.Fatalf("row %s has no diagnostic despite confirmed record", row.Cell())
	}
}

func TestServiceGroupFailureRowsAndAuthorityAreComplete(t *testing.T) {
	t.Parallel()
	events := []string{}
	group := &recordingGroup{events: &events, inspectErrByCell: map[cell.Cell]error{mustCell(t, artifact.HarnessClaudeCode, cell.AgentsAxis()): errors.New("second probe failed")}}
	repo := &eventRegistry{store: registry.New(), events: &events}
	request := groupRequest(t)
	request.Selection = all(t, func(c cell.Cell) bool { return c == mustCell(t, artifact.HarnessClaudeCode, cell.SkillsAxis()) })
	group.plan = func(selection service.GroupSelection) (service.GroupPlan, error) {
		return selectionGroupPlan(t, selection, 0, cell.Cell{}), nil
	}
	result, err := groupService(t, group, repo).ApplySelection(context.Background(), request)
	if err != nil || result.OK() || group.closeCount != 1 {
		t.Fatalf("result=%+v err=%v events=%v closes=%d", result.Rows(), err, events, group.closeCount)
	}
	rows := result.Rows()
	if len(rows) != 3 {
		t.Fatalf("rows=%d, want three canonical group rows", len(rows))
	}
	assertGroupRow(t, rows[0], apply.Completed(), apply.Ensure(), apply.ManagementPasture, registry.ObservationAbsent, true)
	assertGroupRow(t, rows[1], apply.Failed(), apply.RemoveOp(), apply.ManagementPasture, registry.ObservationAbsent, true)
	assertGroupRow(t, rows[2], apply.Completed(), apply.RemoveOp(), apply.ManagementPasture, registry.ObservationAbsent, true)
	if rows[0].Diagnostic() != "live fact" || rows[2].Diagnostic() != "live fact" {
		t.Fatalf("successful live diagnostics changed: skills=%q hooks=%q", rows[0].Diagnostic(), rows[2].Diagnostic())
	}
	if !strings.Contains(rows[1].Diagnostic(), "second probe failed") || strings.Contains(fmt.Sprint(events), "execute:claude-code.hooks") {
		t.Fatalf("failure row/events lost probe or attempted later action: row=%+v events=%v", rows[1], events)
	}
	records := groupStoreRecords(t, repo)
	if len(records) != 3 {
		t.Fatalf("persisted records=%d, want complete live authority after first successful action", len(records))
	}
	for _, c := range []cell.Cell{mustCell(t, artifact.HarnessClaudeCode, cell.SkillsAxis()), mustCell(t, artifact.HarnessClaudeCode, cell.AgentsAxis()), mustCell(t, artifact.HarnessClaudeCode, cell.HooksAxis())} {
		if record, ok := records[c]; !ok || record.Observation() != registry.ObservationAbsent {
			t.Fatalf("persisted record for %s=%+v, want confirmed absent observation", c, record)
		}
	}
}

func TestServiceGroupMutationThenErrorUsesSuccessfulLiveProbe(t *testing.T) {
	t.Parallel()
	events := []string{}
	group := &recordingGroup{events: &events, executeErr: errors.New("mutation returned an error after changing native state")}
	repo := &eventRegistry{store: registry.New(), events: &events}
	result, err := groupService(t, group, repo).ApplySelection(context.Background(), groupRequest(t))
	if err != nil || result.OK() || len(result.Rows()) != 3 {
		t.Fatalf("result=%+v err=%v", result.Rows(), err)
	}
	row := result.Rows()[0]
	assertGroupRow(t, row, apply.Failed(), apply.Inspect(), apply.ManagementPasture, registry.ObservationAbsent, true)
	if !strings.Contains(row.Diagnostic(), "mutation returned an error") {
		t.Fatalf("row lost action diagnostic: %s", row.Diagnostic())
	}
	records := groupStoreRecords(t, repo)
	control := mustCell(t, artifact.HarnessClaudeCode, cell.SkillsAxis())
	record, ok := records[control]
	if !ok || record.LastOperation() != registry.OperationInspect || record.LastOutcome() != registry.OutcomeFailed || record.Observation() != registry.ObservationAbsent {
		t.Fatalf("persisted control fact=%+v, want failed live-probe authority", record)
	}
}

func TestServiceGroupRetryReturnsRowsFromCommittedRegistryOnly(t *testing.T) {
	t.Parallel()
	events := []string{}
	group := &recordingGroup{events: &events}
	repo := &eventRegistry{store: registry.New(), events: &events, saveErr: errors.New("first save failed")}
	svc := groupService(t, group, repo)
	first, err := svc.ApplySelection(context.Background(), groupRequest(t))
	if err != nil || first.OK() || len(groupStoreRecords(t, repo)) != 0 {
		t.Fatalf("first failed apply=%+v err=%v store=%v", first.Rows(), err, groupStoreRecords(t, repo))
	}
	if got := group.priorAt["plan"]; len(got) != 1 {
		t.Fatalf("plan prior count=%d, want one failed attempt", len(got))
	}
	repo.saveErr = nil
	second, err := svc.ApplySelection(context.Background(), groupRequest(t))
	if err != nil || !second.OK() || len(second.Rows()) != 3 {
		t.Fatalf("retry=%+v err=%v", second.Rows(), err)
	}
	for _, row := range second.Rows() {
		if row.Status() != apply.Completed() || row.Management() != apply.ManagementPasture || row.Observation() != registry.ObservationAbsent {
			t.Fatalf("retry returned non-authoritative row=%+v", row)
		}
	}
	records := groupStoreRecords(t, repo)
	if len(records) != 3 {
		t.Fatalf("retry persisted %d records, want complete three-cell authority", len(records))
	}
	if got := group.priorAt["plan"][1]; len(got) != 3 || got[0] != "claude-code.skills:<absent>" || got[1] != "claude-code.agents:<absent>" || got[2] != "claude-code.hooks:<absent>" {
		t.Fatalf("retry reused uncommitted authority: %v", got)
	}
	if strings.Contains(fmt.Sprint(events), "execute:claude-code.hooks") && len(group.executed) < 4 {
		t.Fatalf("remaining work was attempted before retry committed prior facts: %v", events)
	}
}

func TestServiceGroupSaveFailurePreservesCompletePriorAuthority(t *testing.T) {
	t.Parallel()
	events := []string{}
	prior := registry.New()
	repo := &eventRegistry{store: prior, events: &events, saveErr: errors.New("save failed")}
	group := &recordingGroup{events: &events}
	request := groupRequest(t)
	result, err := groupService(t, group, repo).ApplySelection(context.Background(), request)
	if err != nil || result.OK() || group.closeCount != 1 {
		t.Fatalf("result=%+v err=%v events=%v closes=%d", result.Rows(), err, events, group.closeCount)
	}
	rows := result.Rows()
	if len(rows) != 3 {
		t.Fatalf("rows=%d, want three canonical rows", len(rows))
	}
	assertGroupRow(t, rows[0], apply.Failed(), apply.Inspect(), apply.ManagementPasture, registry.ObservationAbsent, true)
	for i, row := range rows[1:] {
		assertGroupRow(t, row, apply.Completed(), row.Operation(), apply.ManagementPasture, registry.ObservationAbsent, true)
		if row.Diagnostic() != "live fact" {
			t.Fatalf("saved-failure sibling %d diagnostic=%q, want live fact", i+1, row.Diagnostic())
		}
	}
	for i, row := range rows[1:] {
		if row.Status() == apply.Unattempted() && row.Management() == apply.ManagementUnknown && row.Observation() == registry.ObservationUnknown {
			if _, ok := groupStoreRecords(t, repo)[row.Cell()]; ok {
				t.Fatalf("unresolved sibling %d=%s manufactured persisted authority", i+1, row.Cell())
			}
		}
	}
	if got := groupStoreRecords(t, repo); len(got) != 0 {
		t.Fatalf("save failure changed prior authority: %+v", got)
	}
	if strings.Contains(fmt.Sprint(events), "execute:claude-code.agents") {
		t.Fatalf("later action ran after save failure: %v", events)
	}
}

func TestServiceGroupInitialUnresolvedPlaceholdersAreRecordFree(t *testing.T) {
	t.Parallel()
	events := []string{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	group := &recordingGroup{events: &events}
	result, err := groupService(t, group, nil).ApplySelection(ctx, groupRequest(t))
	if err != nil || result.OK() || len(result.Rows()) != 3 {
		t.Fatalf("result=%+v err=%v", result.Rows(), err)
	}
	for i, row := range result.Rows()[1:] {
		if row.Status() != apply.Unattempted() || row.Management() != apply.ManagementUnknown || row.Observation() != registry.ObservationUnknown || row.Diagnostic() == "" {
			t.Fatalf("unresolved placeholder %d=%+v, want Unattempted/ManagementUnknown/ObservationUnknown with diagnostic", i+1, row)
		}
	}
	if strings.Contains(fmt.Sprint(events), "save") || strings.Contains(fmt.Sprint(events), "execute:") {
		t.Fatalf("canceled group manufactured authority or ran an action: %v", events)
	}
}
func (g *recordingGroup) ClosePlan(_ context.Context, request service.GroupSelection, _ service.GroupPlan, stage service.GroupTerminalStage) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.capturePrior("close", request)
	*g.events = append(*g.events, "close")
	g.closeCount++
	g.stage = stage
	return g.closeErr
}
func (*recordingGroup) PreflightCell(context.Context, service.GroupCell) error { return nil }

func groupService(t *testing.T, group *recordingGroup, repo service.Registry) *service.Service {
	t.Helper()
	root := t.TempDir()
	contracts := contracts(t, root)
	if repo == nil {
		repo = &eventRegistry{store: registry.New(), events: group.events}
	}
	svc, err := service.New(service.Config{Registry: repo, Contracts: contracts, Activators: []apply.Activator{directFileActivator(t, contracts)}, Group: group})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func groupRequest(t *testing.T) service.SelectionRequest {
	t.Helper()
	return service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return false }), Scope: apply.GlobalScope(), Source: apply.InstallerSource()}
}

func TestServiceGroupLifecycleOrderingFailuresCancellationCloseAndRetry(t *testing.T) {
	t.Parallel()
	t.Run("success ordering and fresh retry", func(t *testing.T) {
		events := []string{}
		group := &recordingGroup{events: &events}
		svc := groupService(t, group, nil)
		for attempt := 0; attempt < 2; attempt++ {
			result, err := svc.ApplySelection(context.Background(), groupRequest(t))
			if err != nil || !result.OK() || len(result.Rows()) != 3 {
				t.Fatalf("attempt %d result=%+v err=%v", attempt, result.Rows(), err)
			}
		}
		wantOne := []string{"plan", "execute:claude-code.skills", "inspect:claude-code.skills", "save", "execute:claude-code.agents", "inspect:claude-code.agents", "save", "close"}
		want := append(append([]string{}, wantOne...), wantOne...)
		if fmt.Sprint(events) != fmt.Sprint(want) || group.closeCount != 2 || group.planCount != 2 || group.stage != service.GroupTerminalSucceeded {
			t.Fatalf("events=%v closes=%d plans=%d stage=%v", events, group.closeCount, group.planCount, group.stage)
		}
	})
	t.Run("execute failure persists confirmed facts", func(t *testing.T) {
		events := []string{}
		group := &recordingGroup{events: &events, executeErr: errors.New("execute sentinel")}
		result, err := groupService(t, group, nil).ApplySelection(context.Background(), groupRequest(t))
		if err != nil || result.OK() || len(result.Rows()) != 3 || result.Rows()[0].Status() != apply.Failed() || !strings.Contains(result.Rows()[0].Diagnostic(), "execute sentinel") || group.stage != service.GroupTerminalExecuteFailed || group.closeCount != 1 {
			t.Fatalf("result=%+v err=%v stage=%v closes=%d", result.Rows(), err, group.stage, group.closeCount)
		}
		if fmt.Sprint(events) != fmt.Sprint([]string{"plan", "execute:claude-code.skills", "inspect:claude-code.skills", "save", "close"}) {
			t.Fatalf("events=%v", events)
		}
	})
	t.Run("execute and inspect failures compose without save", func(t *testing.T) {
		events := []string{}
		group := &recordingGroup{events: &events, executeErr: errors.New("execute sentinel"), inspectErr: errors.New("inspect sentinel")}
		result, err := groupService(t, group, nil).ApplySelection(context.Background(), groupRequest(t))
		if err != nil || result.OK() || !strings.Contains(result.Rows()[0].Diagnostic(), "execute sentinel") || !strings.Contains(result.Rows()[0].Diagnostic(), "inspect sentinel") || group.stage != service.GroupTerminalInspectFailed || strings.Contains(fmt.Sprint(events), "save") {
			t.Fatalf("result=%+v events=%v err=%v", result.Rows(), events, err)
		}
	})
	t.Run("canceled execution still inspects with live bounded context", func(t *testing.T) {
		events := []string{}
		ctx, cancel := context.WithCancel(context.Background())
		group := &recordingGroup{events: &events, executeErr: context.Canceled, cancel: cancel}
		repo := &eventRegistry{store: registry.New(), events: &events, rejectCanceled: true}
		result, err := groupService(t, group, repo).ApplySelection(ctx, groupRequest(t))
		if err != nil || result.OK() || !group.inspectLive || group.closeCount != 1 || !strings.Contains(fmt.Sprint(events), "save") || strings.Contains(result.Rows()[0].Diagnostic(), "registry rejected canceled") {
			t.Fatalf("result=%+v err=%v inspectLive=%t closes=%d events=%v", result.Rows(), err, group.inspectLive, group.closeCount, events)
		}
	})
	t.Run("save composes execute error", func(t *testing.T) {
		events := []string{}
		group := &recordingGroup{events: &events, executeErr: errors.New("execute sentinel")}
		repo := &eventRegistry{store: registry.New(), events: &events, saveErr: errors.New("save sentinel")}
		result, err := groupService(t, group, repo).ApplySelection(context.Background(), groupRequest(t))
		if err != nil || result.OK() || !strings.Contains(result.Rows()[0].Diagnostic(), "execute sentinel") || !strings.Contains(result.Rows()[0].Diagnostic(), "save sentinel") || group.stage != service.GroupTerminalSaveFailed {
			t.Fatalf("result=%+v err=%v stage=%v", result.Rows(), err, group.stage)
		}
	})
	t.Run("close failure turns success into failure", func(t *testing.T) {
		events := []string{}
		group := &recordingGroup{events: &events, closeErr: errors.New("close sentinel")}
		result, err := groupService(t, group, nil).ApplySelection(context.Background(), groupRequest(t))
		if err != nil || result.OK() || !strings.Contains(result.Rows()[1].Diagnostic(), "close sentinel") || group.closeCount != 1 {
			t.Fatalf("result=%+v err=%v closes=%d", result.Rows(), err, group.closeCount)
		}
	})
}

func TestGroupLifecyclePriorOnlyAdvancesAfterCommittedSave(t *testing.T) {
	t.Parallel()
	t.Run("successful save is the next plan authority", func(t *testing.T) {
		events := []string{}
		group := &recordingGroup{events: &events}
		repo := &eventRegistry{store: registry.New(), events: &events}
		svc := groupService(t, group, repo)
		if result, err := svc.ApplySelection(context.Background(), groupRequest(t)); err != nil || !result.OK() {
			t.Fatalf("first apply result=%+v err=%v", result.Rows(), err)
		}
		firstClose := group.priorAt["close"][0]
		if len(group.priorAt["execute"]) == 0 || reflect.DeepEqual(group.priorAt["execute"][0], firstClose) {
			t.Fatalf("first action unexpectedly loaded committed authority: execute=%v close=%v", group.priorAt["execute"][0], firstClose)
		}
		if result, err := svc.ApplySelection(context.Background(), groupRequest(t)); err != nil || !result.OK() {
			t.Fatalf("retry result=%+v err=%v", result.Rows(), err)
		}
		if got := group.priorAt["plan"][1]; !reflect.DeepEqual(got, firstClose) {
			t.Fatalf("retry plan prior=%v, want exactly prior committed at close=%v", got, firstClose)
		}
	})
	t.Run("failed save is not reloadable until repair", func(t *testing.T) {
		events := []string{}
		group := &recordingGroup{events: &events}
		repo := &eventRegistry{store: registry.New(), events: &events, saveErr: errors.New("save sentinel")}
		svc := groupService(t, group, repo)
		result, err := svc.ApplySelection(context.Background(), groupRequest(t))
		if err != nil || result.OK() {
			t.Fatalf("failed save result=%+v err=%v", result.Rows(), err)
		}
		failedClose := group.priorAt["close"][0]
		repo.saveErr = nil
		if result, err = svc.ApplySelection(context.Background(), groupRequest(t)); err != nil || !result.OK() {
			t.Fatalf("repaired retry result=%+v err=%v", result.Rows(), err)
		}
		if got := group.priorAt["plan"][1]; !reflect.DeepEqual(got, failedClose) {
			t.Fatalf("retry reloaded uncommitted facts: prior=%v failed-close=%v", got, failedClose)
		}
		if reflect.DeepEqual(group.priorAt["close"][1], failedClose) {
			t.Fatalf("successful repair did not advance committed authority: close=%v", group.priorAt["close"][1])
		}
	})
}

func TestServiceGroupPlanCloseBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		handled        bool
		closeErr       error
		wantClose      int
		wantCloseInErr bool
	}{
		{name: "unhandled error", wantClose: 0},
		{name: "handled invalid plan", handled: true, closeErr: errors.New("close sentinel"), wantClose: 1, wantCloseInErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []string{}
			group := &recordingGroup{events: &events, planErr: errors.New("plan sentinel"), handledOnError: tc.handled, closeErr: tc.closeErr}
			_, err := groupService(t, group, nil).ApplySelection(context.Background(), groupRequest(t))
			if err == nil || group.closeCount != tc.wantClose || strings.Contains(err.Error(), "close sentinel") != tc.wantCloseInErr {
				t.Fatalf("err=%v closes=%d", err, group.closeCount)
			}
		})
	}
}

func TestServiceRejectsContradictoryGroupFactsBeforeSave(t *testing.T) {
	t.Parallel()
	type mutation func(service.GroupSelection, service.GroupPlan, service.GroupStep, []service.GroupAction) []service.GroupAction
	replace := func(index int, row apply.ActionRow, record *registry.Record, actions []service.GroupAction) []service.GroupAction {
		action, err := service.NewGroupAction(row, record)
		if err != nil {
			return []service.GroupAction{}
		}
		actions[index] = action
		return actions
	}
	cases := map[string]mutation{
		"wrong status": func(_ service.GroupSelection, _ service.GroupPlan, _ service.GroupStep, actions []service.GroupAction) []service.GroupAction {
			r := actions[0].Row()
			record, _ := actions[0].Record()
			return replace(0, apply.NewActionRow(r.Cell(), r.Operation(), apply.NoOp(), r.Management(), r.Observation(), r.Diagnostic()), &record, actions)
		},
		"wrong management": func(_ service.GroupSelection, _ service.GroupPlan, _ service.GroupStep, actions []service.GroupAction) []service.GroupAction {
			r := actions[0].Row()
			record, _ := actions[0].Record()
			return replace(0, apply.NewActionRow(r.Cell(), r.Operation(), r.Status(), apply.ManagementExternal, r.Observation(), r.Diagnostic()), &record, actions)
		},
		"wrong observation": func(_ service.GroupSelection, _ service.GroupPlan, _ service.GroupStep, actions []service.GroupAction) []service.GroupAction {
			r := actions[0].Row()
			record, _ := actions[0].Record()
			return replace(0, apply.NewActionRow(r.Cell(), r.Operation(), r.Status(), r.Management(), registry.ObservationInstalled, r.Diagnostic()), &record, actions)
		},
		"wrong row operation": func(_ service.GroupSelection, _ service.GroupPlan, _ service.GroupStep, actions []service.GroupAction) []service.GroupAction {
			r := actions[0].Row()
			record, _ := actions[0].Record()
			return replace(0, apply.NewActionRow(r.Cell(), apply.Ensure(), r.Status(), r.Management(), r.Observation(), r.Diagnostic()), &record, actions)
		},
		"pasture without record": func(_ service.GroupSelection, _ service.GroupPlan, _ service.GroupStep, actions []service.GroupAction) []service.GroupAction {
			r := actions[0].Row()
			return replace(0, r, nil, actions)
		},
	}
	recordMutation := func(change func(registry.RecordInput) registry.RecordInput) mutation {
		return func(_ service.GroupSelection, _ service.GroupPlan, _ service.GroupStep, actions []service.GroupAction) []service.GroupAction {
			r := actions[0].Row()
			old, _ := actions[0].Record()
			input := registry.RecordInput{Key: old.Key(), Source: old.Source(), Strategy: old.Strategy(), Managed: old.Managed(), ArtifactID: old.ArtifactID(), Version: old.Version(), Selector: old.Selector(), Leaves: old.Leaves(), CreatedDirs: old.CreatedDirs(), SharedConfig: old.SharedConfig(), Observation: old.Observation(), Trust: old.Trust(), LastOperation: old.LastOperation(), LastOutcome: old.LastOutcome(), Diagnostic: old.Diagnostic()}
			record, err := registry.NewRecord(change(input))
			if err != nil {
				return []service.GroupAction{}
			}
			return replace(0, r, &record, actions)
		}
	}
	cases["wrong source"] = recordMutation(func(in registry.RecordInput) registry.RecordInput { in.Source = registry.SourceHomeManager; return in })
	target, _ := cell.New(artifact.HarnessClaudeCode, cell.SkillsAxis())
	foreignKey, _ := projectScope(t).Key(target)
	cases["wrong key"] = recordMutation(func(in registry.RecordInput) registry.RecordInput { in.Key = foreignKey; return in })
	cases["wrong strategy"] = recordMutation(func(in registry.RecordInput) registry.RecordInput {
		in.Strategy = activation.NativePluginKindValue()
		return in
	})
	cases["wrong managed"] = recordMutation(func(in registry.RecordInput) registry.RecordInput { in.Managed = false; return in })
	cases["wrong operation"] = recordMutation(func(in registry.RecordInput) registry.RecordInput {
		in.LastOperation = registry.OperationEnsure
		return in
	})
	cases["wrong outcome"] = recordMutation(func(in registry.RecordInput) registry.RecordInput { in.LastOutcome = registry.OutcomeFailed; return in })
	cases["wrong trust"] = recordMutation(func(in registry.RecordInput) registry.RecordInput { in.Trust = registry.TrustTrusted; return in })
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			events := []string{}
			group := &recordingGroup{events: &events, malform: mutate}
			result, err := groupService(t, group, nil).ApplySelection(context.Background(), groupRequest(t))
			if err != nil || result.OK() || group.stage != service.GroupTerminalFactInvalid || strings.Contains(fmt.Sprint(events), "save") || group.closeCount != 1 {
				t.Fatalf("result=%+v err=%v events=%v stage=%v closes=%d", result.Rows(), err, events, group.stage, group.closeCount)
			}
		})
	}
}
