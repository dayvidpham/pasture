package tasks

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
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

const (
	systemIdentityHelperModeEnv = "PASTURE_SYSTEM_IDENTITY_HELPER_MODE"
	systemIdentityHelperDBEnv   = "PASTURE_SYSTEM_IDENTITY_HELPER_DB"
	systemIdentityHelperReady   = "PASTURE_SYSTEM_IDENTITY_HELPER_READY"
)

func TestSystemIdentityConcurrentFirstOpenConverges(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run", "^TestSystemIdentityConcurrentFirstOpenHelperProcess$")
	cmd.Env = append(os.Environ(),
		systemIdentityHelperModeEnv+"=converge",
		systemIdentityHelperDBEnv+"="+dbPath,
	)
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("coordinated first-open child timed out after 15s and was killed/reaped; output:\n%s", output)
	}
	if err != nil {
		t.Fatalf("coordinated first-open child failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "SYSTEM_IDENTITY_CONVERGED") {
		t.Fatalf("coordinated first-open child returned no success evidence; output:\n%s", output)
	}

	db := openIdentityAssertionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	assertIdentityRowCount(t, db, 1)
	assertGenesisOperationCount(t, db, 1)
}

func TestSystemIdentityConcurrentFirstOpenHelperProcess(t *testing.T) {
	mode := os.Getenv(systemIdentityHelperModeEnv)
	if mode == "" {
		return
	}
	if mode == "stuck" {
		readyPath := os.Getenv(systemIdentityHelperReady)
		if readyPath == "" {
			t.Fatal("stuck helper requires PASTURE_SYSTEM_IDENTITY_HELPER_READY")
		}
		if err := os.WriteFile(readyPath, []byte("ready\n"), 0o600); err != nil {
			t.Fatalf("write stuck-helper readiness evidence: %v", err)
		}
		fmt.Println("SYSTEM_IDENTITY_STUCK_READY")
		for {
			time.Sleep(time.Hour)
		}
	}
	if mode != "converge" {
		t.Fatalf("unknown system-identity helper mode %q", mode)
	}
	dbPath := os.Getenv(systemIdentityHelperDBEnv)
	if dbPath == "" {
		t.Fatal("converge helper requires PASTURE_SYSTEM_IDENTITY_HELPER_DB")
	}
	runCoordinatedFirstOpenChild(t, dbPath)
}

func runCoordinatedFirstOpenChild(t *testing.T, dbPath string) {
	t.Helper()
	ready := make(chan coordinatedCheckpoint, 2)
	afterGenesis := func(tracker string) func(provenance.JournalID) error {
		return func(authority provenance.JournalID) error {
			point := coordinatedCheckpoint{tracker: tracker, authority: authority, proceed: make(chan struct{})}
			fmt.Printf("SYSTEM_IDENTITY_CHECKPOINT tracker=%s authority=%d\n", tracker, authority)
			ready <- point
			<-point.proceed
			return nil
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
	firstPoint := awaitCoordinatedCheckpoint(t, "first", ready, results)
	// Keep the first tracker paused after committed genesis while the independent
	// second tracker performs the exact replay.
	startTracker("second", second)
	secondPoint := awaitCoordinatedCheckpoint(t, "second", ready, results)
	if firstPoint.authority == 0 || firstPoint.authority != secondPoint.authority {
		t.Fatalf("post-genesis authorities differ: first=%d second=%d", firstPoint.authority, secondPoint.authority)
	}

	close(firstPoint.proceed)
	awaitCoordinatedOutcome(t, "first", results)
	close(secondPoint.proceed)
	awaitCoordinatedOutcome(t, "second", results)

	authority := firstPoint.authority
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
	fmt.Printf("SYSTEM_IDENTITY_CONVERGED authority=%d\n", authority)
}

func awaitCoordinatedCheckpoint(
	t *testing.T,
	want string,
	ready <-chan coordinatedCheckpoint,
	results <-chan coordinatedOutcome,
) coordinatedCheckpoint {
	t.Helper()
	select {
	case point := <-ready:
		if point.tracker != want {
			t.Fatalf("received %s checkpoint while waiting for %s", point.tracker, want)
		}
		return point
	case result := <-results:
		t.Fatalf("%s tracker returned before the %s post-genesis checkpoint: %v", result.tracker, want, result.err)
	}
	return coordinatedCheckpoint{}
}

func awaitCoordinatedOutcome(t *testing.T, want string, results <-chan coordinatedOutcome) {
	t.Helper()
	result := <-results
	if result.tracker != want {
		t.Fatalf("%s tracker returned while waiting for %s", result.tracker, want)
	}
	if result.err != nil {
		t.Fatalf("%s tracker failed after its post-genesis checkpoint: %v", result.tracker, result.err)
	}
}

func TestSystemIdentityConcurrentFirstOpenTimeoutKillsAndReaps(t *testing.T) {
	t.Parallel()
	testDir := t.TempDir()
	readyPath := filepath.Join(testDir, "stuck.ready")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run", "^TestSystemIdentityConcurrentFirstOpenHelperProcess$")
	cmd.Env = append(os.Environ(),
		systemIdentityHelperModeEnv+"=stuck",
		systemIdentityHelperReady+"="+readyPath,
	)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("controlled stuck child was not stopped by its command deadline: err=%v output=\n%s", err, output)
	}
	if err == nil {
		t.Fatalf("controlled stuck child exited successfully instead of being killed; output:\n%s", output)
	}
	if !strings.Contains(string(output), "SYSTEM_IDENTITY_STUCK_READY") {
		t.Fatalf("controlled stuck child reached no readiness point before timeout; output:\n%s", output)
	}
	if _, statErr := os.Stat(readyPath); statErr != nil {
		t.Fatalf("read stuck-child readiness evidence after reap: %v", statErr)
	}
	if cmd.ProcessState == nil {
		t.Fatal("controlled stuck child has no ProcessState after Command.Wait")
	}
	if signalErr := cmd.Process.Signal(os.Kill); !errors.Is(signalErr, os.ErrProcessDone) {
		t.Fatalf("controlled stuck child still accepts signals after Command.Wait: %v", signalErr)
	}
	if removeErr := os.RemoveAll(testDir); removeErr != nil {
		t.Fatalf("remove parent temp directory after child reap: %v", removeErr)
	}
	if _, statErr := os.Stat(testDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("parent temp directory still exists after reaped-child cleanup: %v", statErr)
	}
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
