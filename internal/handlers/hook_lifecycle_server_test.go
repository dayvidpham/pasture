package handlers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/internal/writelane"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

// fakeLane is the served path's write lane under a test's control: it counts
// admissions and can hold them open before delegating to a REAL lane, so the
// served tests exercise the production two-phase commit while still being able
// to assert on what has NOT happened yet.
type fakeLane struct {
	inner      *writelane.Lane
	admissions int64
	hold       <-chan struct{}
}

func (f *fakeLane) Submit(ctx context.Context, bytes int64, record writelane.Record) error {
	atomic.AddInt64(&f.admissions, 1)
	if f.hold != nil {
		<-f.hold
	}
	return f.inner.Submit(ctx, bytes, record)
}

// servedFixture opens the ONE unified store the served path and its lane share,
// plus a REAL lane committing through the pinned batch journal, so every served
// test runs the production commit path rather than a seam.
func servedFixture(t *testing.T, dbPath string) (protocol.TaskTracker, *writelane.Lane) {
	t.Helper()
	tracker := mustOpenTracker(t, dbPath)
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, gateClock{}, &gateOperations{}, timeouts.TestProfile())
	require.NoError(t, err)
	budget, err := writelane.BudgetFromProfile(timeouts.TestProfile())
	require.NoError(t, err)
	lane, err := writelane.New(budget, service.Appender.AppendBatch)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lane.Close(context.Background())) })
	return tracker, lane
}

func servedServer(t *testing.T, tracker protocol.TaskTracker, lane LifecycleWriteLane, readers lifecycleReaderFactory) *HookLifecycleServer {
	t.Helper()
	server, err := NewHookLifecycleServer(HookLifecycleServerConfig{
		Tracker:    tracker,
		Lane:       lane,
		Clock:      gateClock{},
		Operations: &gateOperations{},
		Profile:    timeouts.TestProfile(),
		Readers:    readers,
	})
	require.NoError(t, err)
	return server
}

// mustOpenTracker opens the same unified store the direct path opens, so the
// served path is exercised against real storage and not a seam.
func mustOpenTracker(t *testing.T, dbPath string) protocol.TaskTracker {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	return tracker
}

func gateServeInput(t *testing.T, raw []byte, event string) HookLifecycleServeInput {
	t.Helper()
	return HookLifecycleServeInput{
		Harness:     ir.HarnessClaudeCode,
		Event:       event,
		HostVersion: registration.ClaudeCode__2_1_261().Version,
		Payload:     raw,
	}
}

// TestTheServedAnswerIsNotPublishedBeforeTheCommit is the served path's copy of
// the command's commit-before-emit invariant, and the reason the lane exists in
// front of the store: a hook must never be told "recorded" for a record that has
// not committed.
func TestTheServedAnswerIsNotPublishedBeforeTheCommit(t *testing.T) {
	raw := gateFixture(t, gateClaudeGateFixture)
	dbPath := gateStore(t)
	const actor = "lifecycle-lane-allow/0193f1c0-0000-7000-8000-0000000000c1"
	reader := &gateFakeReader{snap: gateBoundSnapshot(t, actor, gateOwnedEpisode(t, actor, gateauthority.PhaseImplPlan))}

	tracker, realLane := servedFixture(t, dbPath)
	t.Cleanup(func() { require.NoError(t, tracker.Close()) })
	hold := make(chan struct{})
	lane := &fakeLane{inner: realLane, hold: hold}
	server := servedServer(t, tracker, lane, reader.gateFactory)

	input := gateServeInput(t, raw, "PreToolUse")
	input.ActorClaim = tasks.ActorClaim(actor)

	done := make(chan struct {
		served HookLifecycleServed
		err    error
	}, 1)
	go func() {
		served, err := server.Serve(context.Background(), input)
		done <- struct {
			served HookLifecycleServed
			err    error
		}{served, err}
	}()

	// Wait until the record is admitted, which is the moment the commit is
	// entering the lane and no answer may exist yet.
	require.Eventually(t, func() bool { return atomic.LoadInt64(&lane.admissions) == 1 }, 2*time.Second, time.Millisecond,
		"the record must be admitted to the lane")
	select {
	case got := <-done:
		t.Fatalf("Serve returned (%+v, %v) while the record's commit was still held: the hook would have been told recorded for an uncommitted event", got.served, got.err)
	default:
	}

	close(hold)
	got := <-done
	require.NoError(t, got.err)
	require.True(t, got.served.Outcome.Exit.IsValid(), "a committed answer must carry a declared exit status")
	require.NotEmpty(t, got.served.OccurrenceID, "a committed answer must name the occurrence it committed")
}

// TestAServedPreAdmissionRefusalStagedNothing proves the refusal the hook reads
// as not-recorded is raised BEFORE the lane is asked anything, so the claim is
// evidence rather than a default.
func TestAServedPreAdmissionRefusalStagedNothing(t *testing.T) {
	dbPath := gateStore(t)
	tracker, realLane := servedFixture(t, dbPath)
	t.Cleanup(func() { require.NoError(t, tracker.Close()) })
	lane := &fakeLane{inner: realLane}
	server := servedServer(t, tracker, lane, nil)

	// An empty payload is refused by the shared preparation, before any store
	// handle is touched and before any write class exists.
	_, err := server.Serve(context.Background(), gateServeInput(t, nil, "PreToolUse"))
	require.Error(t, err)
	require.ErrorIs(t, err, ErrLifecycleBeforeDurableWrite,
		"a refusal raised before the first write attempt must carry the not-recorded sentinel")
	require.Zero(t, atomic.LoadInt64(&lane.admissions),
		"the lane must not have been asked to commit anything for a pre-admission refusal")
	require.Equal(t, hostexit.FaultStageNotRecorded, LifecycleFaultStageForError(err),
		"the daemon must answer not-recorded for a refusal that staged nothing")
}

// TestAServedWithheldEventStagedNothing drives the other pre-admission refusal
// family: an event this build does not admit is refused before the payload is
// bound, and the lane is untouched.
func TestAServedWithheldEventStagedNothing(t *testing.T) {
	dbPath := gateStore(t)
	tracker, realLane := servedFixture(t, dbPath)
	t.Cleanup(func() { require.NoError(t, tracker.Close()) })
	lane := &fakeLane{inner: realLane}
	server := servedServer(t, tracker, lane, nil)

	_, err := server.Serve(context.Background(), HookLifecycleServeInput{
		Harness:     ir.HarnessClaudeCode,
		Event:       "anEventNoRegistrationDeclares",
		HostVersion: registration.ClaudeCode__2_1_261().Version,
		Payload:     []byte(`{}`),
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrLifecycleBeforeDurableWrite)
	require.Zero(t, atomic.LoadInt64(&lane.admissions))
}

// TestTheServedPathCommitsThroughARealLane proves the served path works end to
// end with the production lane: the answer arrives only after the lane's commit
// goroutine ran the closure, and the occurrence is durable.
func TestTheServedPathCommitsThroughARealLane(t *testing.T) {
	raw := gateFixture(t, gateClaudeGateFixture)
	dbPath := gateStore(t)
	const actor = "lifecycle-lane-real/0193f1c0-0000-7000-8000-0000000000c2"
	reader := &gateFakeReader{snap: gateBoundSnapshot(t, actor, gateOwnedEpisode(t, actor, gateauthority.PhaseImplPlan))}

	tracker, lane := servedFixture(t, dbPath)
	t.Cleanup(func() { require.NoError(t, tracker.Close()) })

	server := servedServer(t, tracker, lane, reader.gateFactory)
	input := gateServeInput(t, raw, "PreToolUse")
	input.ActorClaim = tasks.ActorClaim(actor)

	served, err := server.Serve(context.Background(), input)
	require.NoError(t, err)
	require.NotEmpty(t, served.OccurrenceID)
	require.GreaterOrEqual(t, lane.Stats().Records, int64(1),
		"the answer must be published only after the lane committed the record")
	require.Equal(t, 1, reader.reads, "a gate event consults the store exactly once")
}

// TestServedObservationCommitsWithoutConsultingTheGate keeps the served path's
// observation arm honest: an observation is answered by the middle end, so the
// reader is never opened, and the record still commits.
func TestServedObservationCommitsWithoutConsultingTheGate(t *testing.T) {
	raw := gateFixture(t, gateClaudeSessionStartFixture)
	dbPath := gateStore(t)
	reader := &gateFakeReader{fault: errors.New("the reader must not be reached for an observation")}

	tracker, lane := servedFixture(t, dbPath)
	t.Cleanup(func() { require.NoError(t, tracker.Close()) })

	server := servedServer(t, tracker, lane, reader.gateFactory)
	served, err := server.Serve(context.Background(), gateServeInput(t, raw, "SessionStart"))
	require.NoError(t, err)
	require.NotEmpty(t, served.OccurrenceID)
	require.Zero(t, reader.reads, "no snapshot may be taken for an observation")
}

// TestTheServedPathReportsAWholeBatchFailureAsRecordUnknown pins the served
// half of the batch mapping: a record the lane admitted and whose batch then
// rolled back is NOT a pre-admission refusal and must never be answered as a
// commit — it is record-unknown.
func TestTheServedPathReportsAWholeBatchFailureAsRecordUnknown(t *testing.T) {
	raw := gateFixture(t, gateClaudeGateFixture)
	dbPath := gateStore(t)
	const actor = "lifecycle-lane-batch/0193f1c0-0000-7000-8000-0000000000d1"
	reader := &gateFakeReader{snap: gateBoundSnapshot(t, actor, gateOwnedEpisode(t, actor, gateauthority.PhaseImplPlan))}

	tracker := mustOpenTracker(t, dbPath)
	t.Cleanup(func() { require.NoError(t, tracker.Close()) })

	rollback := errors.New("the batch transaction rolled back")
	apply := func(_ context.Context, inputs []provenance.OperationInput) (provenance.BatchResult, error) {
		records := make([]provenance.BatchRecordResult, len(inputs))
		for i := range records {
			records[i] = provenance.BatchRecordResult{Outcome: provenance.BatchRecordNotRecorded}
		}
		return provenance.BatchResult{Committed: false, Records: records},
			&provenance.BatchApplyError{Index: 0, Cause: rollback}
	}
	budget, err := writelane.BudgetFromProfile(timeouts.TestProfile())
	require.NoError(t, err)
	lane, err := writelane.New(budget, apply)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lane.Close(context.Background())) })

	server := servedServer(t, tracker, lane, reader.gateFactory)
	input := gateServeInput(t, raw, "PreToolUse")
	input.ActorClaim = tasks.ActorClaim(actor)

	_, err = server.Serve(context.Background(), input)
	require.Error(t, err)
	require.ErrorIs(t, err, rollback, "the served path must report the batch's own failure")
	require.NotErrorIs(t, err, ErrLifecycleBeforeDurableWrite,
		"a record that was admitted and then rolled back is not a pre-admission refusal")
	require.Equal(t, hostexit.FaultStageRecordUnknown, LifecycleFaultStageForError(err),
		"a whole-batch failure must be answered record-unknown, never as a commit")
	require.Equal(t, int64(1), lane.Stats().Records, "the admitted record must have reached exactly one commit attempt")
}

// TestTheServerRefusesIncompleteWiring pins the construction refusals, so a
// daemon cannot start a served path that would fail on the first user event.
func TestTheServerRefusesIncompleteWiring(t *testing.T) {
	dbPath := gateStore(t)
	tracker := mustOpenTracker(t, dbPath)
	defer tracker.Close()
	_ = tracker

	cases := []struct {
		name string
		cfg  HookLifecycleServerConfig
	}{
		{name: "no store", cfg: HookLifecycleServerConfig{Lane: &fakeLane{}, Clock: gateClock{}, Operations: &gateOperations{}, Profile: timeouts.TestProfile()}},
		{name: "no lane", cfg: HookLifecycleServerConfig{Tracker: tracker, Clock: gateClock{}, Operations: &gateOperations{}, Profile: timeouts.TestProfile()}},
		{name: "no clock", cfg: HookLifecycleServerConfig{Tracker: tracker, Lane: &fakeLane{}, Operations: &gateOperations{}, Profile: timeouts.TestProfile()}},
		{name: "no operations", cfg: HookLifecycleServerConfig{Tracker: tracker, Lane: &fakeLane{}, Clock: gateClock{}, Profile: timeouts.TestProfile()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewHookLifecycleServer(tc.cfg)
			require.Error(t, err, "an incompletely wired served path must be refused at construction")
		})
	}
}

// TestTheServedStageClassifierKeepsTheWeakestDefault pins the daemon's half of
// the fault-stage table: the shared sentinels keep their precise stage and
// everything else is record-unknown.
func TestTheServedStageClassifierKeepsTheWeakestDefault(t *testing.T) {
	require.Equal(t, hostexit.FaultStageNotRecorded, LifecycleFaultStageForError(ErrLifecycleBeforeDurableWrite))
	require.Equal(t, hostexit.FaultStageRecorded, LifecycleFaultStageForError(ErrLifecycleCommittedWithoutContinuation))
	require.Equal(t, hostexit.FaultStageRecorded, LifecycleFaultStageForError(ErrLifecycleDeliveryRefused))
	require.Equal(t, hostexit.FaultStageRecordUnknown, LifecycleFaultStageForError(errors.New("the journal failed after the commit")))
}
