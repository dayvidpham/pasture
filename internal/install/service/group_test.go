package service_test

import (
	"context"
	"errors"
	"fmt"
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
	boundaries := [][2]int{{6, 5}, {6, 3}, {6, 0}, {5, 3}, {5, 0}, {3, 0}}
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
	mu      sync.Mutex
	store   registry.Store
	events  *[]string
	saveErr error
}

func (r *eventRegistry) Load(context.Context) (registry.Store, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.store, nil
}
func (r *eventRegistry) Save(_ context.Context, store registry.Store) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.events = append(*r.events, "save")
	if r.saveErr != nil {
		return r.saveErr
	}
	r.store = store
	return nil
}

type recordingGroup struct {
	mu             sync.Mutex
	events         *[]string
	planErr        error
	handledOnError bool
	executeErr     error
	inspectErr     error
	closeErr       error
	cancel         context.CancelFunc
	inspectLive    bool
	malform        func(service.GroupSelection, service.GroupPlan, service.GroupStep, []service.GroupAction) []service.GroupAction
	closeCount     int
	planCount      int
	stage          service.GroupTerminalStage
}

func (*recordingGroup) Harness() ir.HarnessID { return artifact.HarnessClaudeCode }
func (g *recordingGroup) PlanSelection(_ context.Context, request service.GroupSelection) (service.GroupPlan, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	*g.events = append(*g.events, "plan")
	g.planCount++
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
func (g *recordingGroup) ExecuteAction(_ context.Context, _ service.GroupSelection, _ service.GroupPlan, step service.GroupStep) error {
	g.mu.Lock()
	*g.events = append(*g.events, "execute:"+step.ControlCell().String())
	g.mu.Unlock()
	if g.cancel != nil {
		g.cancel()
	}
	return g.executeErr
}
func (g *recordingGroup) InspectAction(ctx context.Context, request service.GroupSelection, plan service.GroupPlan, step service.GroupStep, executeErr error) (service.GroupFacts, error) {
	g.mu.Lock()
	*g.events = append(*g.events, "inspect:"+step.ControlCell().String())
	g.inspectLive = ctx.Err() == nil
	g.mu.Unlock()
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
		record, err := registry.NewRecord(registry.RecordInput{Key: result.Key(), Source: registry.SourceInstaller, Strategy: request.Activation[result.Cell()].Strategy().Kind(), Managed: true, Observation: registry.ObservationAbsent, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationInspect, LastOutcome: outcome, Diagnostic: "live fact"})
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
	return service.NewGroupFacts(actions...)
}
func (g *recordingGroup) ClosePlan(_ context.Context, _ service.GroupSelection, _ service.GroupPlan, stage service.GroupTerminalStage) error {
	g.mu.Lock()
	defer g.mu.Unlock()
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
		result, err := groupService(t, group, nil).ApplySelection(ctx, groupRequest(t))
		if err != nil || result.OK() || !group.inspectLive || group.closeCount != 1 {
			t.Fatalf("result=%+v err=%v inspectLive=%t closes=%d", result.Rows(), err, group.inspectLive, group.closeCount)
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
