package tasks

import (
	"bytes"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/provadapter"
)

func createSystemIdentityTask(tr interface {
	Create(string, string, string, provenance.TaskType, provenance.Priority, provenance.Phase) (provenance.Task, error)
}, title string) error {
	_, err := tr.Create("pasture-system-identity", title, "bootstrap integration test",
		provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseRequest)
	return err
}

func TestPastureSystemGenesisInputIsDeterministic(t *testing.T) {
	t.Parallel()
	actor := provadapter.PastureSystemDefaultActorID()
	first := pastureSystemGenesisInput(actor, 1)
	second := pastureSystemGenesisInput(actor, 2)

	if first.OperationID != second.OperationID || first.ActorID != second.ActorID ||
		first.AuthorityJournalID != nil || second.AuthorityJournalID != nil ||
		!bytes.Equal(first.CommandDigest, second.CommandDigest) || !reflect.DeepEqual(first.Effects, second.Effects) {
		t.Fatalf("genesis retry identity differs:\n first=%+v\nsecond=%+v", first, second)
	}
	firstMutation, err := provenance.PrepareMutationV1(first.Effects)
	if err != nil {
		t.Fatalf("PrepareMutationV1(first): %v", err)
	}
	secondMutation, err := provenance.PrepareMutationV1(second.Effects)
	if err != nil {
		t.Fatalf("PrepareMutationV1(second): %v", err)
	}
	if !bytes.Equal(firstMutation.CanonicalBytes(), secondMutation.CanonicalBytes()) ||
		!bytes.Equal(firstMutation.DerivedDigest(), secondMutation.DerivedDigest()) {
		t.Fatal("genesis canonical mutation differs across retry timestamps")
	}
}

func TestSystemIdentityCrashAfterGenesisCommitConvergesOnReopen(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	injected := errors.New("injected crash after genesis commit")
	var committedAuthority provenance.JournalID

	first, err := openTaskTrackerWithOptions(dbPath, openTaskTrackerOptions{
		afterGenesisCommit: func(authority provenance.JournalID) error {
			committedAuthority = authority
			return injected
		},
	})
	if err != nil {
		t.Fatalf("open crash-injected tracker: %v", err)
	}
	if err := createSystemIdentityTask(first, "crash-gap-first-attempt"); !errors.Is(err, injected) {
		t.Fatalf("first Create error = %v, want injected crash", err)
	}
	if committedAuthority == 0 {
		t.Fatal("crash hook observed no committed authority")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close crash-injected tracker: %v", err)
	}

	db := openIdentityAssertionDB(t, dbPath)
	assertIdentityRowCount(t, db, 0)
	assertGenesisOperationCount(t, db, 1)
	if err := db.Close(); err != nil {
		t.Fatalf("close crash-gap assertion database: %v", err)
	}

	reopened, err := openTaskTrackerWithOptions(dbPath, openTaskTrackerOptions{})
	if err != nil {
		t.Fatalf("reopen after crash gap: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if err := createSystemIdentityTask(reopened, "crash-gap-retry"); err != nil {
		t.Fatalf("retry Create after committed genesis: %v", err)
	}
	assertCommittedGenesisAuthority(t, reopened.Journal(), committedAuthority)

	db = openIdentityAssertionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	assertPersistedIdentity(t, db, committedAuthority)
	assertGenesisOperationCount(t, db, 1)
}

func TestSystemIdentityConcurrentFirstOpenConverges(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	type checkpoint struct {
		authority provenance.JournalID
		proceed   chan struct{}
	}
	ready := make(chan checkpoint, 2)
	abort := make(chan struct{})
	t.Cleanup(func() { close(abort) })
	afterGenesis := func(authority provenance.JournalID) error {
		point := checkpoint{authority: authority, proceed: make(chan struct{})}
		ready <- point
		select {
		case <-point.proceed:
			return nil
		case <-abort:
			return errors.New("coordinated genesis test aborted")
		}
	}

	first, err := openTaskTrackerWithOptions(dbPath, openTaskTrackerOptions{afterGenesisCommit: afterGenesis})
	if err != nil {
		t.Fatalf("open first coordinated tracker: %v", err)
	}
	defer func() { _ = first.Close() }()
	second, err := openTaskTrackerWithOptions(dbPath, openTaskTrackerOptions{afterGenesisCommit: afterGenesis})
	if err != nil {
		t.Fatalf("open second coordinated tracker: %v", err)
	}
	defer func() { _ = second.Close() }()

	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() { <-start; errs <- createSystemIdentityTask(first, "concurrent-first") }()
	go func() { <-start; errs <- createSystemIdentityTask(second, "concurrent-second") }()
	close(start)

	checkpoints := make([]checkpoint, 0, 2)
	for len(checkpoints) < 2 {
		select {
		case point := <-ready:
			checkpoints = append(checkpoints, point)
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for both trackers to replay genesis")
		}
	}
	if checkpoints[0].authority == 0 || checkpoints[0].authority != checkpoints[1].authority {
		t.Fatalf("coordinated authorities = [%d %d], want one non-zero authority",
			checkpoints[0].authority, checkpoints[1].authority)
	}
	// Both trackers reached the post-genesis boundary concurrently. Serialize only
	// their later singleton/task writes so this test isolates genesis convergence.
	for _, point := range checkpoints {
		close(point.proceed)
		if err := <-errs; err != nil {
			t.Errorf("concurrent first-open Create: %v", err)
		}
	}

	assertCommittedGenesisAuthority(t, first.Journal(), checkpoints[0].authority)
	db := openIdentityAssertionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	assertPersistedIdentity(t, db, checkpoints[0].authority)
	assertGenesisOperationCount(t, db, 1)
}

func openIdentityAssertionDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open identity assertion database: %v", err)
	}
	return db
}

func assertIdentityRowCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pasture_system_identity`).Scan(&got); err != nil {
		t.Fatalf("count pasture_system_identity: %v", err)
	}
	if got != want {
		t.Fatalf("pasture_system_identity rows = %d, want %d", got, want)
	}
}

func assertGenesisOperationCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM journal_operations WHERE operation_id = ?`,
		string(pastureSystemGenesisOperationID)).Scan(&got); err != nil {
		t.Fatalf("count genesis operations: %v", err)
	}
	if got != want {
		t.Fatalf("genesis operation rows = %d, want %d", got, want)
	}
}

func assertPersistedIdentity(t *testing.T, db *sql.DB, wantAuthority provenance.JournalID) {
	t.Helper()
	var actor string
	var authority int64
	if err := db.QueryRow(`SELECT committer_actor_id, genesis_authority_journal_id FROM pasture_system_identity WHERE singleton_id = 0`).
		Scan(&actor, &authority); err != nil {
		t.Fatalf("read persisted system identity: %v", err)
	}
	if actor != provadapter.PastureSystemDefaultActorID().String() || provenance.JournalID(authority) != wantAuthority {
		t.Fatalf("persisted identity = (%q, %d), want (%q, %d)",
			actor, authority, provadapter.PastureSystemDefaultActorID(), wantAuthority)
	}
}

func assertCommittedGenesisAuthority(t *testing.T, journal provenance.JournalAPI, want provenance.JournalID) {
	t.Helper()
	result, err := journal.LookupCommitted(pastureSystemGenesisOperationID)
	if err != nil {
		t.Fatalf("LookupCommitted(genesis): %v", err)
	}
	if result.Kind != provenance.CommittedExact {
		t.Fatalf("genesis result kind = %v, want CommittedExact", result.Kind)
	}
	for _, slot := range result.ResultSlots {
		if slot.Slot == pastureSystemGenesisResultSlot {
			if slot.ProducedJournalID != want {
				t.Fatalf("genesis authority = %d, want %d", slot.ProducedJournalID, want)
			}
			return
		}
	}
	t.Fatal("committed genesis has no authority result slot")
}
