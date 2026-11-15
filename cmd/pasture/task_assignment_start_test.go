package main_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/provenance"
)

func TestCLI_TaskAssignmentParentLineage(t *testing.T) {
	t.Parallel()
	db := filepath.Join(t.TempDir(), "pasture.db")
	tr, err := tasks.OpenTaskTracker(db)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := tr.RegisterSoftwareAgent("cli-test", "parent-owner", "1", "test")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := tr.Create("cli-test", "parent", "", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseWorkerSlices)
	if err != nil {
		t.Fatal(err)
	}
	child, err := tr.Create("cli-test", "child", "", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseWorkerSlices)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	parentStart := runCLI(t, "--db", db, "task", "assignment", "start", parent.ID.String(), "--assignment", "parent-owner", "--actor", actor.ID.String(), "--occupant", actor.ID.String())
	if parentStart.exitCode != 0 {
		t.Fatalf("parent start exit %d: %s", parentStart.exitCode, parentStart.stderr)
	}
	childStart := runCLI(t, "--db", db, "task", "assignment", "start", child.ID.String(), "--assignment", "child-owner", "--actor", actor.ID.String(), "--occupant", actor.ID.String(), "--parent-task", parent.ID.String(), "--parent-assignment", "parent-owner")
	if childStart.exitCode != 0 {
		t.Fatalf("child start exit %d: %s", childStart.exitCode, childStart.stderr)
	}
	childShow := runCLI(t, "--db", db, "task", "assignment", "show", child.ID.String())
	if childShow.exitCode != 0 || !strings.Contains(childShow.stdout, "child-owner") {
		t.Fatalf("child show exit %d: %s", childShow.exitCode, childShow.stderr)
	}
	if end := runCLI(t, "--db", db, "task", "assignment", "end", child.ID.String(), "--assignment", "child-owner", "--actor", actor.ID.String()); end.exitCode != 0 {
		t.Fatalf("child end exit %d: %s", end.exitCode, end.stderr)
	}
	if end := runCLI(t, "--db", db, "task", "assignment", "end", parent.ID.String(), "--assignment", "parent-owner", "--actor", actor.ID.String()); end.exitCode != 0 {
		t.Fatalf("parent end exit %d: %s", end.exitCode, end.stderr)
	}
}

func TestCLI_TaskAssignmentLifecycle(t *testing.T) {
	t.Parallel()
	help := runCLI(t, "task", "assignment", "--help")
	if help.exitCode != 0 {
		t.Fatalf("assignment help exit %d: %s", help.exitCode, help.stderr)
	}
	for _, command := range []string{"start", "show", "end", "transfer"} {
		if !strings.Contains(help.stdout, command) {
			t.Fatalf("assignment help is missing %q:\n%s", command, help.stdout)
		}
	}

	db := filepath.Join(t.TempDir(), "pasture.db")
	tr, err := tasks.OpenTaskTracker(db)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := tr.RegisterSoftwareAgent("cli-test", "assignment-owner", "1", "test")
	if err != nil {
		t.Fatal(err)
	}
	task, err := tr.Create("cli-test", "assignment target", "", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseWorkerSlices)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}

	startArgs := []string{"--db", db, "task", "assignment", "start", task.ID.String(), "--assignment", "cli-owner", "--actor", actor.ID.String(), "--occupant", actor.ID.String()}
	beforeStart := runCLI(t, "--db", db, "task", "assignment", "show", task.ID.String())
	if beforeStart.exitCode == 0 {
		t.Fatal("show on an unassigned task was accepted")
	}
	started := runCLI(t, startArgs...)
	if started.exitCode != 0 {
		t.Fatalf("start exit %d: %s", started.exitCode, started.stderr)
	}
	var startResult struct {
		Assignment struct {
			AssignmentID string `json:"assignmentId"`
			Occupant     string `json:"occupant"`
		} `json:"assignment"`
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal([]byte(started.stdout), &startResult); err != nil {
		t.Fatal(err)
	}
	if startResult.Assignment.AssignmentID != "cli-owner" || startResult.Assignment.Occupant != actor.ID.String() || startResult.Replayed {
		t.Fatalf("start result = %+v", startResult)
	}

	replayed := runCLI(t, startArgs...)
	if replayed.exitCode != 0 {
		t.Fatalf("start replay exit %d: %s", replayed.exitCode, replayed.stderr)
	}
	if err := json.Unmarshal([]byte(replayed.stdout), &startResult); err != nil {
		t.Fatal(err)
	}
	if !startResult.Replayed {
		t.Fatal("exact start replay was not reported")
	}

	shown := runCLI(t, "--db", db, "task", "assignment", "show", task.ID.String())
	if shown.exitCode != 0 {
		t.Fatalf("show exit %d: %s", shown.exitCode, shown.stderr)
	}
	var shownState struct {
		TaskID       string `json:"taskId"`
		Slot         string `json:"slot"`
		AssignmentID string `json:"assignmentId"`
		Occupant     string `json:"occupant"`
	}
	if err := json.Unmarshal([]byte(shown.stdout), &shownState); err != nil {
		t.Fatal(err)
	}
	if shownState.TaskID != task.ID.String() || shownState.Slot != "owner-responsibility" || shownState.AssignmentID != "cli-owner" || shownState.Occupant != actor.ID.String() {
		t.Fatalf("show state = %+v", shownState)
	}

	second := runCLI(t, "--db", db, "task", "assignment", "start", task.ID.String(), "--assignment", "cli-second", "--actor", actor.ID.String(), "--occupant", actor.ID.String())
	if second.exitCode == 0 {
		t.Fatal("second active owner was accepted")
	}
	premature := runCLI(t, "--db", db, "task", "close", task.ID.String(), "--reason", "premature")
	if premature.exitCode == 0 {
		t.Fatal("close with an active assignment was accepted")
	}
	invalidParent := runCLI(t, "--db", db, "task", "assignment", "start", task.ID.String(), "--assignment", "cli-parent", "--actor", actor.ID.String(), "--occupant", actor.ID.String(), "--parent-task", task.ID.String())
	if invalidParent.exitCode == 0 {
		t.Fatal("incomplete parent citation was accepted")
	}

	transfer := runCLI(t, "--db", db, "task", "assignment", "transfer", task.ID.String(), "--slot", "owner-responsibility", "--assignment", "cli-next", "--actor", actor.ID.String(), "--occupant", actor.ID.String())
	if transfer.exitCode != 0 {
		t.Fatalf("transfer exit %d: %s", transfer.exitCode, transfer.stderr)
	}
	staleEnd := runCLI(t, "--db", db, "task", "assignment", "end", task.ID.String(), "--assignment", "cli-owner", "--actor", actor.ID.String())
	if staleEnd.exitCode == 0 {
		t.Fatal("stale assignment end was accepted")
	}
	for _, replay := range []bool{false, true} {
		ended := runCLI(t, "--db", db, "task", "assignment", "end", task.ID.String(), "--assignment", "cli-next", "--actor", actor.ID.String())
		if ended.exitCode != 0 {
			t.Fatalf("end exit %d: %s", ended.exitCode, ended.stderr)
		}
		var result struct {
			Replayed bool `json:"replayed"`
		}
		if err := json.Unmarshal([]byte(ended.stdout), &result); err != nil {
			t.Fatal(err)
		}
		if result.Replayed != replay {
			t.Fatalf("end replay = %v, want %v", result.Replayed, replay)
		}
	}
	closed := runCLI(t, "--db", db, "task", "close", task.ID.String(), "--reason", "done")
	if closed.exitCode != 0 {
		t.Fatalf("close after end exit %d: %s", closed.exitCode, closed.stderr)
	}
	closedStart := runCLI(t, "--db", db, "task", "assignment", "start", task.ID.String(), "--assignment", "after-close", "--actor", actor.ID.String(), "--occupant", actor.ID.String())
	if closedStart.exitCode == 0 {
		t.Fatal("start on a closed task was accepted")
	}
}
