package tasks_test

// system_identity_test.go covers the journaled task-backend system identity:
// the mutation verbs commit through a Session bound to the pasture-system
// committing actor and genesis authority (Tracker.As), the reserved namespace is
// activated with the deterministic ordinal-zero software agent, and the whole
// thing is journaled, reproducible, and stable across reopen.

import (
	"database/sql"
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
	RegisterSoftwareAgent(namespace, name, version, source string) (provenance.SoftwareAgent, error)
	SoftwareAgent(id provenance.AgentID) (provenance.SoftwareAgent, error)
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

// TestSystemIdentity_ActivatesExactDefaultActor proves the first production task
// mutation atomically installs the exact claim and ordinal-zero software actor,
// persists that actor as the committer, and materializes no reserved ordinal above
// zero.
func TestSystemIdentity_ActivatesExactDefaultActor(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	tr := openTempTracker(t, dbPath)

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

	agent, err := tr.SoftwareAgent(provadapter.PastureSystemDefaultActorID())
	if err != nil {
		t.Fatalf("SoftwareAgent(default): %v", err)
	}
	if agent.ID != provadapter.PastureSystemDefaultActorID() || agent.Name != provadapter.PastureSystemDefaultName ||
		agent.Version != "1" || agent.Source != "pasture" {
		t.Errorf("default software agent = %+v", agent)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open persisted store for assertions: %v", err)
	}
	defer func() { _ = db.Close() }()
	var committer string
	if err := db.QueryRow(`SELECT committer_actor_id FROM pasture_system_identity WHERE singleton_id = 0`).Scan(&committer); err != nil {
		t.Fatalf("read persisted committer: %v", err)
	}
	if committer != provadapter.PastureSystemDefaultActorID().String() {
		t.Errorf("persisted committer = %q, want %q", committer, provadapter.PastureSystemDefaultActorID())
	}
	var manifestCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM fixed_actor_manifest_entries`).Scan(&manifestCount); err != nil {
		t.Fatalf("count fixed manifest entries: %v", err)
	}
	if manifestCount != 1 {
		t.Fatalf("fixed manifest entries = %d, want only ordinal zero", manifestCount)
	}
	var actorID, namespace, name, metadata string
	var kind int
	if err := db.QueryRow(`SELECT actor_id, namespace, kind_id, name, metadata FROM fixed_actor_manifest_entries`).
		Scan(&actorID, &namespace, &kind, &name, &metadata); err != nil {
		t.Fatalf("read fixed manifest entry: %v", err)
	}
	if actorID != provadapter.PastureSystemDefaultActorID().String() ||
		namespace != provadapter.PastureSystemNamespace || kind != int(provenance.AgentKindSoftware) ||
		name != provadapter.PastureSystemDefaultName || metadata != "{}" {
		t.Errorf("fixed manifest entry = actor=%q namespace=%q kind=%d name=%q metadata=%q",
			actorID, namespace, kind, name, metadata)
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

// TestSystemIdentity_PersistedFallbackUpgradesToDefaultActor proves a store
// initialized with the former random committer preserves its genesis authority
// while converging the persisted actor to manifest-v1 ordinal zero on reopen.
func TestSystemIdentity_PersistedFallbackUpgradesToDefaultActor(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	tr1 := openTempTracker(t, dbPath)
	createSmoke(t, tr1, "before-identity-upgrade")
	random, err := tr1.RegisterSoftwareAgent("pasture-system", "old-fallback", "1", "pasture")
	if err != nil {
		t.Fatalf("register old fallback fixture: %v", err)
	}
	if err := tr1.Close(); err != nil {
		t.Fatalf("close first tracker: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open persisted store fixture: %v", err)
	}
	var authorityBefore int64
	if err := db.QueryRow(`SELECT genesis_authority_journal_id FROM pasture_system_identity WHERE singleton_id = 0`).Scan(&authorityBefore); err != nil {
		t.Fatalf("read authority before upgrade: %v", err)
	}
	if _, err := db.Exec(`UPDATE pasture_system_identity SET committer_actor_id = ? WHERE singleton_id = 0`, random.ID.String()); err != nil {
		t.Fatalf("seed old fallback identity: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close persisted store fixture: %v", err)
	}

	tr2 := openTempTracker(t, dbPath)
	createSmoke(t, tr2, "after-identity-upgrade")

	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen persisted store fixture: %v", err)
	}
	defer func() { _ = db.Close() }()
	var actorAfter string
	var authorityAfter int64
	if err := db.QueryRow(`SELECT committer_actor_id, genesis_authority_journal_id FROM pasture_system_identity WHERE singleton_id = 0`).
		Scan(&actorAfter, &authorityAfter); err != nil {
		t.Fatalf("read upgraded identity: %v", err)
	}
	if actorAfter != provadapter.PastureSystemDefaultActorID().String() {
		t.Errorf("upgraded actor = %q, want %q", actorAfter, provadapter.PastureSystemDefaultActorID())
	}
	if authorityAfter != authorityBefore {
		t.Errorf("genesis authority changed from %d to %d during actor upgrade", authorityBefore, authorityAfter)
	}
}
