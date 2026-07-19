package tasks_test

// system_identity_test.go covers the journaled task-backend system identity:
// the mutation verbs commit through a Session bound to the pasture-system
// committing actor and genesis authority (Tracker.As), the reserved namespace is
// activated as a claim + [0, 1023] range (the seam the ordinal-zero seed flips in
// through — asserted here via the claim/range registry, NOT a seeded row), and the
// whole thing is journaled and reproducible and stable across reopen.

import (
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/provadapter"
	"github.com/dayvidpham/pasture/internal/tasks"
)

func openTempTracker(t *testing.T, dbPath string) provenanceTracker {
	t.Helper()
	tr, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

// provenanceTracker is the subset of protocol.TaskTracker these tests exercise.
type provenanceTracker interface {
	Create(namespace, title, description string, taskType provenance.TaskType, priority provenance.Priority, phase provenance.Phase) (provenance.Task, error)
	Update(id provenance.TaskID, fields provenance.UpdateFields) (provenance.Task, error)
	CloseTask(id provenance.TaskID, reason string) (provenance.Task, error)
	Start(id provenance.TaskID) (provenance.Task, error)
	Stop(id provenance.TaskID) (provenance.Task, error)
	Reopen(id provenance.TaskID) (provenance.Task, error)
	Show(id provenance.TaskID) (provenance.Task, error)
	Journal() provenance.JournalAPI
	Close() error
}

func createSmoke(t *testing.T, tr provenanceTracker, title string) provenance.Task {
	t.Helper()
	task, err := tr.Create("pasture-sysid-test", title, "system identity smoke",
		provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseRequest)
	if err != nil {
		t.Fatalf("Create(%q): %v", title, err)
	}
	return task
}

// TestSystemIdentity_ActivatesClaimAndRange proves the first mutation reserves the
// pasture-system namespace as the exact manifest claim over the [0, 1023] range.
// The assertion is the claim/range registry entry, not a seeded ordinal-zero row,
// so it holds in the reservation-only era and continues to hold once the seed lands.
func TestSystemIdentity_ActivatesClaimAndRange(t *testing.T) {
	t.Parallel()
	tr := openTempTracker(t, filepath.Join(t.TempDir(), "pasture.db"))

	// A mutation triggers identity bootstrap (namespace activation + genesis).
	createSmoke(t, tr, "activate")

	claims, err := tr.Journal().NamespaceClaims()
	if err != nil {
		t.Fatalf("NamespaceClaims: %v", err)
	}
	var found *provenance.ActorNamespaceClaim
	for i := range claims {
		if claims[i].Namespace == provadapter.PastureSystemNamespace {
			found = &claims[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("pasture-system namespace claim not registered; claims=%+v", claims)
	}
	if !found.Equal(provadapter.PastureSystemClaim()) {
		t.Errorf("registered claim %+v does not equal the manifest claim %+v",
			*found, provadapter.PastureSystemClaim())
	}
	if found.Range != provadapter.PastureSystemRange {
		t.Errorf("claim range = %+v, want the reserved [0, 1023] range %+v",
			found.Range, provadapter.PastureSystemRange)
	}
}

// TestSystemIdentity_JournaledAndReproducible proves every mutation the façade
// exposes is committed through the ordered journal: after a create/update/close the
// journal replays and the incremental projection converges with the recomputed one.
func TestSystemIdentity_JournaledAndReproducible(t *testing.T) {
	t.Parallel()
	tr := openTempTracker(t, filepath.Join(t.TempDir(), "pasture.db"))

	task := createSmoke(t, tr, "journaled")
	newTitle := "journaled-updated"
	if _, err := tr.Update(task.ID, provenance.UpdateFields{Title: &newTitle}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := tr.CloseTask(task.ID, "done"); err != nil {
		t.Fatalf("CloseTask: %v", err)
	}

	if _, err := tr.Journal().ReplayProjections(); err != nil {
		t.Fatalf("ReplayProjections did not converge after journaled mutations: %v", err)
	}
}

// TestSystemIdentity_LifecycleFSM proves the dedicated lifecycle verbs drive the
// journaled status FSM: open → in_progress → open → closed → open.
func TestSystemIdentity_LifecycleFSM(t *testing.T) {
	t.Parallel()
	tr := openTempTracker(t, filepath.Join(t.TempDir(), "pasture.db"))

	task := createSmoke(t, tr, "lifecycle")
	if task.Status != provenance.StatusOpen {
		t.Fatalf("fresh task status = %v, want open", task.Status)
	}

	steps := []struct {
		name string
		do   func(provenance.TaskID) (provenance.Task, error)
		want provenance.Status
	}{
		{"start", tr.Start, provenance.StatusInProgress},
		{"stop", tr.Stop, provenance.StatusOpen},
		{"close", func(id provenance.TaskID) (provenance.Task, error) { return tr.CloseTask(id, "closing") }, provenance.StatusClosed},
		{"reopen", tr.Reopen, provenance.StatusOpen},
	}
	for _, s := range steps {
		got, err := s.do(task.ID)
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if got.Status != s.want {
			t.Errorf("after %s: status = %v, want %v", s.name, got.Status, s.want)
		}
	}
}

// TestSystemIdentity_StableAcrossReopen proves the resolved identity is persisted
// and reused: a second process-style open commits under the same reservation
// (exactly one pasture-system claim, no drift) and both tasks survive.
func TestSystemIdentity_StableAcrossReopen(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	tr1 := openTempTracker(t, dbPath)
	first := createSmoke(t, tr1, "before-reopen")
	if err := tr1.Close(); err != nil {
		t.Fatalf("close tr1: %v", err)
	}

	tr2 := openTempTracker(t, dbPath)
	second := createSmoke(t, tr2, "after-reopen")

	// Both tasks are visible on the reopened tracker.
	if _, err := tr2.Show(first.ID); err != nil {
		t.Errorf("first task not visible after reopen: %v", err)
	}
	if _, err := tr2.Show(second.ID); err != nil {
		t.Errorf("second task not visible: %v", err)
	}

	// Activation stayed idempotent: exactly one pasture-system claim exists.
	claims, err := tr2.Journal().NamespaceClaims()
	if err != nil {
		t.Fatalf("NamespaceClaims: %v", err)
	}
	count := 0
	for i := range claims {
		if claims[i].Namespace == provadapter.PastureSystemNamespace {
			count++
		}
	}
	if count != 1 {
		t.Errorf("pasture-system claim count = %d after reopen, want exactly 1", count)
	}

	// The journal remains reproducible after the cross-open mutations.
	if _, err := tr2.Journal().ReplayProjections(); err != nil {
		t.Errorf("ReplayProjections after reopen: %v", err)
	}
}
