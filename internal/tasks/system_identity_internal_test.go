package tasks

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
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

type coordinatedCheckpoint struct {
	tracker   string
	authority provenance.JournalID
	proceed   chan struct{}
}

type coordinatedOutcome struct {
	tracker string
	err     error
}

func TestSystemIdentityConcurrentFirstOpenConverges(t *testing.T) {
	t.Parallel()
	testDir, err := os.MkdirTemp("", "pasture-system-identity-concurrency-")
	if err != nil {
		t.Fatalf("create coordinated tracker directory: %v", err)
	}
	cleanupSafe := true
	t.Cleanup(func() {
		if cleanupSafe {
			_ = os.RemoveAll(testDir)
		}
	})
	dbPath := filepath.Join(testDir, "pasture.db")
	ready := make(chan coordinatedCheckpoint, 2)
	abort := make(chan struct{})
	var abortOnce sync.Once
	abortAll := func() { abortOnce.Do(func() { close(abort) }) }
	t.Cleanup(abortAll)
	afterGenesis := func(tracker string) func(provenance.JournalID) error {
		return func(authority provenance.JournalID) error {
			point := coordinatedCheckpoint{tracker: tracker, authority: authority, proceed: make(chan struct{})}
			select {
			case ready <- point:
			case <-abort:
				return fmt.Errorf("%s tracker aborted while reporting the post-genesis checkpoint", tracker)
			}
			select {
			case <-point.proceed:
				return nil
			case <-abort:
				return fmt.Errorf("%s tracker aborted while waiting at the post-genesis checkpoint", tracker)
			}
		}
	}

	first, err := openTaskTrackerWithOptions(dbPath, openTaskTrackerOptions{afterGenesisCommit: afterGenesis("first")})
	if err != nil {
		t.Fatalf("open first coordinated tracker: %v", err)
	}
	second, err := openTaskTrackerWithOptions(dbPath, openTaskTrackerOptions{afterGenesisCommit: afterGenesis("second")})
	if err != nil {
		_ = first.Close()
		t.Fatalf("open second coordinated tracker: %v", err)
	}
	// Fixed-agent activation has its own multi-writer coverage. Pre-activate here
	// so this proof coordinates only the deterministic genesis replay boundary.
	if _, err := provadapter.ActivatePastureSystem(first); err != nil {
		_ = second.Close()
		_ = first.Close()
		t.Fatalf("pre-activate pasture-system actor: %v", err)
	}

	results := make(chan coordinatedOutcome, 2)
	startTracker := func(name string, tracker interface {
		Create(string, string, string, provenance.TaskType, provenance.Priority, provenance.Phase) (provenance.Task, error)
	}) {
		go func() {
			results <- coordinatedOutcome{tracker: name, err: createSystemIdentityTask(tracker, "concurrent-"+name)}
		}()
	}
	startTracker("first", first)
	cleanupSafe = false
	launched := 1

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	checkpoints := make(map[string]coordinatedCheckpoint, 2)
	outcomes := make(map[string]error, 2)
	released := make(map[string]bool, 2)
	launchedNames := []string{"first"}
	failure := ""
	var liveTrackers []string
	release := func(tracker string) {
		if point, ok := checkpoints[tracker]; ok && !released[tracker] {
			close(point.proceed)
			released[tracker] = true
		}
	}
coordination:
	for len(outcomes) != 2 {
		if failure != "" {
			liveTrackers = joinCoordinatedOutcomes(results, outcomes, launchedNames, 6*time.Second)
			if len(liveTrackers) != 0 {
				failure += fmt.Sprintf("; timed out after abort waiting for tracker operations to stop: %v", liveTrackers)
			}
			break coordination
		}
		if failure == "" && len(checkpoints) == 2 && !released["first"] {
			firstAuthority := checkpoints["first"].authority
			secondAuthority := checkpoints["second"].authority
			if firstAuthority == 0 || firstAuthority != secondAuthority {
				failure = fmt.Sprintf("post-genesis authorities differ: first=%d second=%d", firstAuthority, secondAuthority)
				abortAll()
			}
			if failure == "" {
				release("first")
			}
		}
		if failure == "" && released["first"] && !released["second"] {
			if firstErr, done := outcomes["first"]; done {
				if firstErr != nil {
					failure = fmt.Sprintf("first tracker failed after its post-genesis checkpoint: %v", firstErr)
					abortAll()
				} else {
					release("second")
				}
			}
		}
		select {
		case point := <-ready:
			if _, duplicate := checkpoints[point.tracker]; duplicate {
				failure = fmt.Sprintf("%s tracker reported the post-genesis checkpoint twice", point.tracker)
				abortAll()
				continue
			}
			checkpoints[point.tracker] = point
			if point.tracker == "first" && launched == 1 {
				// Keep the first tracker paused after its committed genesis while the
				// independent second tracker performs the exact replay. This exercises
				// overlapping first opens without racing unrelated SQLite connection locks.
				startTracker("second", second)
				launched = 2
				launchedNames = append(launchedNames, "second")
			}
		case result := <-results:
			outcomes[result.tracker] = result.err
			if failure == "" && len(checkpoints) < 2 {
				failure = fmt.Sprintf("%s tracker returned before both post-genesis checkpoints: %v (checkpoints=%v)",
					result.tracker, result.err, checkpointNames(checkpoints))
				abortAll()
			} else if failure == "" && result.err != nil {
				failure = fmt.Sprintf("%s tracker failed after its post-genesis checkpoint: %v", result.tracker, result.err)
				abortAll()
			}
		case <-timer.C:
			failure = fmt.Sprintf("timed out coordinating first-open trackers (checkpoints=%v outcomes=%v released=%v)",
				checkpointNames(checkpoints), outcomeNames(outcomes), released)
			abortAll()
		}
	}
	if failure != "" {
		if len(liveTrackers) == 0 {
			_ = second.Close()
			_ = first.Close()
			cleanupSafe = true
		}
		t.Fatal(failure)
	}
	for tracker, err := range outcomes {
		if err != nil {
			t.Fatalf("%s tracker Create failed: %v", tracker, err)
		}
	}

	authority := checkpoints["first"].authority
	assertCommittedGenesisAuthority(t, first.Journal(), authority)
	db := openIdentityAssertionDB(t, dbPath)
	assertPersistedIdentity(t, db, authority)
	assertGenesisOperationCount(t, db, 1)
	if err := db.Close(); err != nil {
		t.Fatalf("close coordinated assertion database: %v", err)
	}
	if firstCloseErr, secondCloseErr := first.Close(), second.Close(); firstCloseErr != nil || secondCloseErr != nil {
		t.Fatalf("close coordinated trackers: first=%v second=%v", firstCloseErr, secondCloseErr)
	}
	cleanupSafe = true
}

func joinCoordinatedOutcomes(
	results <-chan coordinatedOutcome,
	outcomes map[string]error,
	launched []string,
	timeout time.Duration,
) []string {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for len(outcomes) < len(launched) {
		select {
		case result := <-results:
			outcomes[result.tracker] = result.err
		case <-timer.C:
			missing := make([]string, 0, len(launched)-len(outcomes))
			for _, tracker := range launched {
				if _, joined := outcomes[tracker]; !joined {
					missing = append(missing, tracker)
				}
			}
			return missing
		}
	}
	return nil
}

func TestJoinCoordinatedOutcomesBoundsStuckOperation(t *testing.T) {
	t.Parallel()
	results := make(chan coordinatedOutcome, 1)
	outcomes := make(map[string]error, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-release
		results <- coordinatedOutcome{tracker: "stuck"}
	}()

	started := time.Now()
	missing := joinCoordinatedOutcomes(results, outcomes, []string{"stuck"}, 25*time.Millisecond)
	if !reflect.DeepEqual(missing, []string{"stuck"}) {
		t.Fatalf("bounded join missing trackers = %v, want [stuck]", missing)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded join took %s for stuck tracker, want under 1s", elapsed)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("controlled stuck tracker did not stop after test release")
	}
}

func checkpointNames(checkpoints map[string]coordinatedCheckpoint) []string {
	names := make([]string, 0, len(checkpoints))
	for _, name := range []string{"first", "second"} {
		if _, ok := checkpoints[name]; ok {
			names = append(names, name)
		}
	}
	return names
}

func outcomeNames(outcomes map[string]error) []string {
	names := make([]string, 0, len(outcomes))
	for _, name := range []string{"first", "second"} {
		if _, ok := outcomes[name]; ok {
			names = append(names, name)
		}
	}
	return names
}

func TestSystemIdentityPersistedNoncanonicalGenesisFailsClosed(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	opened, err := openTaskTrackerWithOptions(dbPath, openTaskTrackerOptions{})
	if err != nil {
		t.Fatalf("open fixture tracker: %v", err)
	}
	impl := opened.(*trackerImpl)
	if _, err := provadapter.ActivatePastureSystem(impl.prov); err != nil {
		t.Fatalf("activate pasture-system fixture: %v", err)
	}
	noncanonicalActor, err := impl.prov.RegisterSoftwareAgent("genesis-conflict", "noncanonical", "1", "test")
	if err != nil {
		t.Fatalf("register noncanonical genesis actor: %v", err)
	}
	noncanonical := pastureSystemGenesisInput(noncanonicalActor.ID, time.Now().UTC().UnixNano())
	noncanonical.CommandDigest = []byte("noncanonical-genesis-command")
	noncanonical.Effects[0].BootstrapLabel = "noncanonical-genesis"
	noncanonical.Effects[0].OperationAuthorityID = "noncanonical.genesis.authority"
	committed, err := impl.prov.Journal().Apply(noncanonical)
	if err != nil {
		t.Fatalf("commit noncanonical genesis fixture: %v", err)
	}
	authority := resultSlotJournalID(t, committed, pastureSystemGenesisResultSlot)
	if err := impl.ensurePastureTablesOnce(); err != nil {
		t.Fatalf("create pasture identity table: %v", err)
	}
	if err := writeSystemIdentity(impl.auditDB, provadapter.PastureSystemDefaultActorID(), authority); err != nil {
		t.Fatalf("write matching singleton fixture: %v", err)
	}
	before := systemIdentityStoreSnapshot(t, impl.auditDB)

	err = createSystemIdentityTask(opened, "must-fail-noncanonical-genesis")
	if err == nil {
		t.Fatal("Create succeeded with a noncanonical operation under the deterministic genesis ID")
	}
	if !errors.Is(err, provenance.ErrOperationConflict) {
		t.Fatalf("Create error does not preserve provenance.ErrOperationConflict: %v", err)
	}
	var conflict *provenance.OperationConflict
	if !errors.As(err, &conflict) || conflict.Field == "" {
		t.Fatalf("Create error does not expose an actionable typed OperationConflict: %T %v", err, err)
	}
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) || structured.Category != pasterrors.CategoryStorage ||
		structured.What == "" || structured.Why == "" || structured.Where == "" || structured.Impact == "" || structured.Fix == "" {
		t.Fatalf("Create error is not an actionable CategoryStorage StructuredError: %T %v", err, err)
	}
	after := systemIdentityStoreSnapshot(t, impl.auditDB)
	if after != before {
		t.Fatalf("store mutated while rejecting noncanonical genesis:\n before=%+v\n  after=%+v", before, after)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("close fixture tracker: %v", err)
	}
}

func TestSystemIdentityPersistedMissingGenesisFailsClosed(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	opened, err := openTaskTrackerWithOptions(dbPath, openTaskTrackerOptions{})
	if err != nil {
		t.Fatalf("open fixture tracker: %v", err)
	}
	impl := opened.(*trackerImpl)
	if _, err := provadapter.ActivatePastureSystem(impl.prov); err != nil {
		t.Fatalf("activate pasture-system fixture: %v", err)
	}
	if err := impl.ensurePastureTablesOnce(); err != nil {
		t.Fatalf("create pasture identity table: %v", err)
	}
	const arbitraryAuthority provenance.JournalID = 4242
	if err := writeSystemIdentity(impl.auditDB, provadapter.PastureSystemDefaultActorID(), arbitraryAuthority); err != nil {
		t.Fatalf("write missing-genesis singleton fixture: %v", err)
	}
	before := systemIdentityStoreSnapshot(t, impl.auditDB)
	if before.operations != 0 {
		t.Fatalf("fixture journal operations = %d, want empty journal", before.operations)
	}

	err = createSystemIdentityTask(opened, "must-fail-missing-genesis")
	if err == nil {
		t.Fatal("Create succeeded with a persisted singleton and no deterministic genesis operation")
	}
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) || structured.Category != pasterrors.CategoryStorage ||
		structured.What == "" || structured.Why == "" || structured.Where == "" || structured.Impact == "" || structured.Fix == "" {
		t.Fatalf("Create error is not an actionable CategoryStorage StructuredError: %T %v", err, err)
	}
	after := systemIdentityStoreSnapshot(t, impl.auditDB)
	if after != before {
		t.Fatalf("store mutated while rejecting missing genesis:\n before=%+v\n  after=%+v", before, after)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("close fixture tracker: %v", err)
	}
}

func resultSlotJournalID(t *testing.T, result provenance.CommittedResult, slot provenance.ResultSlotID) provenance.JournalID {
	t.Helper()
	for _, candidate := range result.ResultSlots {
		if candidate.Slot == slot {
			return candidate.ProducedJournalID
		}
	}
	t.Fatalf("committed result has no %q slot", slot)
	return 0
}

type systemIdentitySnapshot struct {
	actor      string
	authority  int64
	operations int
	tasks      int
	claims     int
	agents     int
	manifests  int
}

func systemIdentityStoreSnapshot(t *testing.T, db *sql.DB) systemIdentitySnapshot {
	t.Helper()
	var snapshot systemIdentitySnapshot
	if err := db.QueryRow(`SELECT committer_actor_id, genesis_authority_journal_id FROM pasture_system_identity WHERE singleton_id = 0`).
		Scan(&snapshot.actor, &snapshot.authority); err != nil {
		t.Fatalf("read system identity snapshot: %v", err)
	}
	counts := []struct {
		query string
		out   *int
	}{
		{`SELECT COUNT(*) FROM journal_operations`, &snapshot.operations},
		{`SELECT COUNT(*) FROM tasks`, &snapshot.tasks},
		{`SELECT COUNT(*) FROM actor_namespace_claims`, &snapshot.claims},
		{`SELECT COUNT(*) FROM agents`, &snapshot.agents},
		{`SELECT COUNT(*) FROM fixed_actor_manifest_entries`, &snapshot.manifests},
	}
	for _, count := range counts {
		if err := db.QueryRow(count.query).Scan(count.out); err != nil {
			t.Fatalf("query snapshot count %q: %v", count.query, err)
		}
	}
	return snapshot
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
