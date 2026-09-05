package tasks

// gate_reader_feasibility_test.go is the regression guard that the PINNED
// provenance module still supports the read model the gate decision needs.
//
// Every probe below runs against a REAL store handle, opened the way
// production opens it (OpenTaskTracker / OpenTaskTrackerWithOptions), and
// reaches provenance only through its exported API. No mock, no copied type,
// no internal package.
//
// Each probe names, in its doc comment, what turns it RED. Where no mutation
// is performable the doc comment says "feasibility only" and the probe makes
// no guard claim.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// ─── shared probe fixtures ───────────────────────────────────────────────────

// feasibilityEpisode is one seeded assignment episode plus the two journal ids
// the read model needs: the authority row and the material start event.
type feasibilityEpisode struct {
	task      provenance.TaskID
	occupant  provenance.ActorID
	authority provenance.JournalID
	event     provenance.JournalID
}

// seedFeasibilityEpisode commits one assignment episode through a single
// journal Apply and returns the two ids from the Apply RESULT. The effect shape
// mirrors the seed the assignment service's own tests use: an
// EffectAssignmentStart carrying a caller-named result slot, plus the material
// FamilyAssignmentStarted task event carrying its own slot.
func seedFeasibilityEpisode(t *testing.T, tracker *trackerImpl, task provenance.TaskID, assignment provenance.AssignmentID, occupant provenance.ActorID, operation provenance.OperationID) feasibilityEpisode {
	t.Helper()
	_, systemAuthority, found, err := readSystemIdentity(tracker.auditDB)
	if err != nil || !found {
		t.Fatalf("read system identity: found=%t err=%v", found, err)
	}
	event, err := MapMaterialEvent(AssignmentStartedEvent{Task: task, Assignment: assignment, Role: RoleOwnerResponsibility, Occupant: occupant})
	if err != nil {
		t.Fatalf("map assignment-start event: %v", err)
	}
	event.ResultSlot = feasibilityEventSlot
	result, err := tracker.Journal().Apply(provenance.OperationInput{
		OperationID:        operation,
		ActorID:            occupant,
		AuthorityJournalID: &systemAuthority,
		CommandDigest:      []byte(operation),
		Effects: []provenance.Effect{
			{Sort: provenance.EffectAssignmentStart, ResultSlot: feasibilityAuthoritySlot, TaskID: task, AssignmentID: assignment, SlotID: provenance.SlotOwnerResponsibility, Occupant: occupant},
			event,
		},
	})
	if err != nil {
		t.Fatalf("seed assignment episode %q: %v", assignment, err)
	}
	episode := feasibilityEpisode{task: task, occupant: occupant}
	for _, slot := range result.ResultSlots {
		switch slot.Slot {
		case feasibilityAuthoritySlot:
			episode.authority = slot.ProducedJournalID
			if slot.Kind != provenance.JournalKindAuthority {
				t.Fatalf("authority slot %q has journal kind %v, want JournalKindAuthority", slot.Slot, slot.Kind)
			}
		case feasibilityEventSlot:
			episode.event = slot.ProducedJournalID
		}
	}
	if episode.authority == 0 || episode.event == 0 {
		t.Fatalf("Apply result did not bind both slots: authority=%d event=%d slots=%+v", episode.authority, episode.event, result.ResultSlots)
	}
	return episode
}

const (
	feasibilityAuthoritySlot provenance.ResultSlotID = "authority"
	feasibilityEventSlot     provenance.ResultSlotID = "event"
)

// feasibilityActor registers one human agent for a probe.
func feasibilityActor(t *testing.T, tracker *trackerImpl, handle string) provenance.ActorID {
	t.Helper()
	actor, err := tracker.RegisterHumanAgent(handle, "Feasibility Probe", handle+"@example.test")
	if err != nil {
		t.Fatalf("register actor %q: %v", handle, err)
	}
	return actor.ID
}

// journalMaximum is the probe-2 query, written once and reused: an unfiltered
// task-event page under the canonical order with a caller-set page size of one.
// The page returns the maximum journal_id of the WHOLE journal, all kinds, not
// only of task events (provenance internal/sqlite/journal.go QueryTaskEvents:
// SELECT COALESCE(MAX(journal_id), 0) FROM journal).
func journalMaximum(t *testing.T, tracker *trackerImpl) provenance.JournalID {
	t.Helper()
	page, err := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, Limit: 1})
	if err != nil {
		t.Fatalf("read the journal maximum: %v", err)
	}
	return page.SnapshotMaxJournalID
}

// appendFillerEvents commits count task events on task, in batches, so a probe
// can build a journal of a stated size without one Apply per event.
func appendFillerEvents(t *testing.T, tracker *trackerImpl, task provenance.TaskID, actor provenance.ActorID, prefix string, count int) {
	t.Helper()
	_, systemAuthority, found, err := readSystemIdentity(tracker.auditDB)
	if err != nil || !found {
		t.Fatalf("read system identity for filler: found=%t err=%v", found, err)
	}
	const batch = 200
	for start := 0; start < count; start += batch {
		size := batch
		if start+size > count {
			size = count - start
		}
		effects := make([]provenance.Effect, 0, size)
		for i := 0; i < size; i++ {
			payload, err := json.Marshal(map[string]any{"n": start + i})
			if err != nil {
				t.Fatalf("encode filler payload: %v", err)
			}
			effect, err := epochTaskEvent(task, FamilySkillRun.EventKind(), payload)
			if err != nil {
				t.Fatalf("build filler effect: %v", err)
			}
			effect.ResultSlot = provenance.ResultSlotID(fmt.Sprintf("filler-%d", start+i))
			effects = append(effects, effect)
		}
		operation := provenance.OperationID(fmt.Sprintf("%s-filler-%d", prefix, start))
		if _, err := tracker.Journal().Apply(provenance.OperationInput{
			OperationID:        operation,
			ActorID:            actor,
			AuthorityJournalID: &systemAuthority,
			CommandDigest:      []byte(operation),
			Effects:            effects,
		}); err != nil {
			t.Fatalf("append filler batch at %d: %v", start, err)
		}
	}
}

// ─── probe 1 ─────────────────────────────────────────────────────────────────

// TestGateFeasibilityProbe1AuthorityPredicateIsCallable proves that
// AuthorityGovernsTaskAt is reachable through the EXPORTED provenance.Journal
// interface, obtained the way internal/tasks already obtains it
// (trackerImpl.Journal, which forwards to provenance.Tracker.Journal).
//
// RED when: the predicate answers true for a task the authority does not
// govern, or the exported interface stops carrying the method (a compile
// error on the interface assertion below).
func TestGateFeasibilityProbe1AuthorityPredicateIsCallable(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()

	// The predicate must be on the EXPORTED interface, not on a concrete type.
	var journal provenance.Journal = tracker.Journal()

	actor := feasibilityActor(t, tracker, "probe1")
	governed := createHumanTestTask(t, tracker, "probe1-governed")
	stranger := createHumanTestTask(t, tracker, "probe1-stranger")
	episode := seedFeasibilityEpisode(t, tracker, governed, "probe1-owner", actor, "probe1-start")

	governs, err := journal.AuthorityGovernsTaskAt(episode.authority, governed, provenance.JournalID(math.MaxInt64))
	if err != nil {
		t.Fatalf("AuthorityGovernsTaskAt on the governed task: %v", err)
	}
	if !governs {
		t.Fatalf("AuthorityGovernsTaskAt(%d, governed) = false; want true, because the episode was just committed and never ended", episode.authority)
	}

	// MUTATION: the same authority, a task it does not govern.
	governsStranger, err := journal.AuthorityGovernsTaskAt(episode.authority, stranger, provenance.JournalID(math.MaxInt64))
	if err != nil {
		t.Fatalf("AuthorityGovernsTaskAt on the stranger task: %v", err)
	}
	if governsStranger {
		t.Fatalf("AuthorityGovernsTaskAt(%d, stranger) = true; want false, because no episode of that authority covers task %q", episode.authority, stranger)
	}

	// The predicate is bounded by beforeJID: at the authority's own row the
	// episode has not opened yet.
	governsBefore, err := journal.AuthorityGovernsTaskAt(episode.authority, governed, episode.authority)
	if err != nil {
		t.Fatalf("AuthorityGovernsTaskAt bounded at the authority row: %v", err)
	}
	if governsBefore {
		t.Fatalf("AuthorityGovernsTaskAt(%d, governed, before=%d) = true; want false, because the bound excludes the authority's own row", episode.authority, episode.authority)
	}
}

// ─── probe 2 ─────────────────────────────────────────────────────────────────

// TestGateFeasibilityProbe2CurrentJournalMaximumIsObtainable proves that the
// CURRENT MAXIMUM JournalID is obtainable in one query, and states what it
// yields on an EMPTY journal.
//
// The query is QueryTaskEvents(JournalQueryV1{OrderBy: OrderByJournalID,
// Limit: 1}); the answer is the returned page's SnapshotMaxJournalID. It is
// the maximum over the WHOLE journal (every kind), not over task events only,
// so an authority row or an operation anchor moves it.
//
// RED when: the maximum does not move after one committed event.
func TestGateFeasibilityProbe2CurrentJournalMaximumIsObtainable(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()

	// An EMPTY journal: the store is open, nothing has been committed. The
	// COALESCE default is the answer.
	if empty := journalMaximum(t, tracker); empty != 0 {
		t.Fatalf("journal maximum on an empty journal = %d; want 0, because a freshly opened store has committed no journal row", empty)
	}

	actor := feasibilityActor(t, tracker, "probe2")
	task := createHumanTestTask(t, tracker, "probe2")
	afterTask := journalMaximum(t, tracker)
	if afterTask == 0 {
		t.Fatalf("journal maximum after one task creation = 0; want a committed journal id")
	}

	// MUTATION: append exactly one event and require the maximum to move.
	appendFillerEvents(t, tracker, task, actor, "probe2", 1)
	afterEvent := journalMaximum(t, tracker)
	if afterEvent <= afterTask {
		t.Fatalf("journal maximum after one appended event = %d; want strictly above %d, because the append committed at least one journal row", afterEvent, afterTask)
	}
}

// ─── probe 3 ─────────────────────────────────────────────────────────────────

// TestGateFeasibilityProbe3BoundedCatchUpPage proves that a bounded page of
// FamilyAssignmentStarted facts with JournalID above a watermark is obtainable
// in ONE query with a caller-set page size, under the canonical order.
//
// RED when: the page is read from the wrong watermark and the fact above the
// true watermark is missed.
func TestGateFeasibilityProbe3BoundedCatchUpPage(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()

	actor := feasibilityActor(t, tracker, "probe3")
	first := createHumanTestTask(t, tracker, "probe3-first")
	second := createHumanTestTask(t, tracker, "probe3-second")
	third := createHumanTestTask(t, tracker, "probe3-third")

	seedFeasibilityEpisode(t, tracker, first, "probe3-owner-1", actor, "probe3-start-1")
	seedFeasibilityEpisode(t, tracker, second, "probe3-owner-2", actor, "probe3-start-2")

	watermark := journalMaximum(t, tracker)
	late := seedFeasibilityEpisode(t, tracker, third, "probe3-owner-3", actor, "probe3-start-3")

	catchUp := provenance.JournalQueryV1{
		OrderBy:        provenance.OrderByJournalID,
		EventKinds:     []provenance.EventKind{FamilyAssignmentStarted.EventKind()},
		AfterJournalID: watermark,
		Limit:          8,
	}
	page, err := tracker.Journal().QueryTaskEvents(catchUp)
	if err != nil {
		t.Fatalf("read the catch-up page: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].JournalID != late.event || page.Events[0].TaskID != third {
		t.Fatalf("catch-up page above watermark %d = %+v; want exactly the one assignment-start fact %d on task %q", watermark, page.Events, late.event, third)
	}

	// The page size is the caller's: two seeded facts, a limit of one, and a
	// next cursor for the rest.
	fromZero := provenance.JournalQueryV1{
		OrderBy:    provenance.OrderByJournalID,
		EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()},
		Limit:      1,
	}
	firstPage, err := tracker.Journal().QueryTaskEvents(fromZero)
	if err != nil {
		t.Fatalf("read the first bounded page: %v", err)
	}
	if len(firstPage.Events) != 1 || firstPage.Next == nil {
		t.Fatalf("bounded page with Limit 1 = %d rows, next=%v; want one row and a next cursor, because three assignment-start facts exist", len(firstPage.Events), firstPage.Next)
	}

	// MUTATION: page from the WRONG watermark (the journal maximum taken AFTER
	// the late fact committed). The late fact is missed.
	wrongWatermark := journalMaximum(t, tracker)
	missed := catchUp
	missed.AfterJournalID = wrongWatermark
	missedPage, err := tracker.Journal().QueryTaskEvents(missed)
	if err != nil {
		t.Fatalf("read the catch-up page from the wrong watermark: %v", err)
	}
	if len(missedPage.Events) != 0 {
		t.Fatalf("catch-up page above the wrong watermark %d = %+v; want no rows, which is exactly the missed fact this mutation demonstrates", wrongWatermark, missedPage.Events)
	}
}

// ─── probe 4 ─────────────────────────────────────────────────────────────────

// TestGateFeasibilityProbe4TornSnapshotProbe proves that a torn snapshot is
// detectable by one query: JournalQueryV1{TaskIDs: <episode tasks>,
// AfterJournalID: snapshotJID, Limit: 1}. Any row means the snapshot is torn.
//
// RED when: the probe is pointed at the wrong task set; the torn write is then
// invisible and the probe reports "not torn" for a snapshot that is torn.
func TestGateFeasibilityProbe4TornSnapshotProbe(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()

	actor := feasibilityActor(t, tracker, "probe4")
	episodeTask := createHumanTestTask(t, tracker, "probe4-episode")
	otherTask := createHumanTestTask(t, tracker, "probe4-other")
	seedFeasibilityEpisode(t, tracker, episodeTask, "probe4-owner", actor, "probe4-start")

	snapshot := journalMaximum(t, tracker)
	tornProbe := provenance.JournalQueryV1{
		OrderBy:        provenance.OrderByJournalID,
		TaskIDs:        []provenance.TaskID{episodeTask},
		AfterJournalID: snapshot,
		Limit:          1,
	}
	quiet, err := tracker.Journal().QueryTaskEvents(tornProbe)
	if err != nil {
		t.Fatalf("run the torn probe on a quiet journal: %v", err)
	}
	if len(quiet.Events) != 0 {
		t.Fatalf("torn probe above snapshot %d on a quiet journal = %+v; want no rows, because nothing was written after the snapshot", snapshot, quiet.Events)
	}

	// A write lands on an episode task after the snapshot: the snapshot is torn.
	appendFillerEvents(t, tracker, episodeTask, actor, "probe4-torn", 1)
	torn, err := tracker.Journal().QueryTaskEvents(tornProbe)
	if err != nil {
		t.Fatalf("run the torn probe after a write: %v", err)
	}
	if len(torn.Events) != 1 {
		t.Fatalf("torn probe above snapshot %d after one write on task %q = %d rows; want exactly one, because any row means the snapshot is torn", snapshot, episodeTask, len(torn.Events))
	}

	// MUTATION: point the probe at the WRONG task set. The torn case turns RED:
	// the same torn journal now reports "not torn".
	blind := tornProbe
	blind.TaskIDs = []provenance.TaskID{otherTask}
	blindPage, err := tracker.Journal().QueryTaskEvents(blind)
	if err != nil {
		t.Fatalf("run the torn probe on the wrong task set: %v", err)
	}
	if len(blindPage.Events) != 0 {
		t.Fatalf("torn probe on the wrong task set = %+v; want no rows, which is the blindness this mutation demonstrates", blindPage.Events)
	}
}

// ─── probe 5 ─────────────────────────────────────────────────────────────────

// TestGateFeasibilityProbe5OneReadTransactionIsNotObtainable measures whether
// ONE read transaction on the shared store handle can hold the pasture-side
// reads (claim, index) and the provenance-side reads (governance predicate,
// task record) together.
//
// HOW A PASTURE PACKAGE OBTAINS A TRANSACTION ON THE SHARED HANDLE TODAY: the
// only site is bindWellKnownAgent in internal/tasks/well_known.go, which calls
// auditDB.BeginTx. The shared DSN sets _txlock=immediate (internal/dbconn), so
// every such transaction is an immediate write transaction.
//
// MEASURED RESULT, recorded by this probe: provenance reads through the
// borrowed handle LEASE THEIR OWN CONNECTION from the same pool. The Journal
// interface exposes no transaction-scoped variant, so a provenance read cannot
// join a pasture-held *sql.Tx. Two consequences, both asserted below:
//  1. at the production pool size of one connection, a provenance read issued
//     while a pasture transaction is open cannot obtain a connection at all;
//  2. above pool size one, the provenance read succeeds but on a DIFFERENT
//     connection, therefore under a DIFFERENT read snapshot.
//
// RED when: either measured behaviour changes — the single-connection case
// stops blocking, or the multi-connection case starts sharing the snapshot.
// A change in either direction means the read model must be re-planned, which
// is why this probe stays as a guard.
func TestGateFeasibilityProbe5OneReadTransactionIsNotObtainable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pasture.db")
	tracker := openHumanTestTracker(t, path)
	defer tracker.Close()

	actor := feasibilityActor(t, tracker, "probe5")
	task := createHumanTestTask(t, tracker, "probe5")
	episode := seedFeasibilityEpisode(t, tracker, task, "probe5-owner", actor, "probe5-start")

	// (1) The production pool is one connection. A pasture-held transaction
	// takes it; a provenance read then has none to lease.
	tx, err := tracker.auditDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin a read transaction on the shared handle: %v", err)
	}
	starved := make(chan error, 1)
	go func() {
		_, predicateErr := tracker.Journal().AuthorityGovernsTaskAt(episode.authority, task, provenance.JournalID(math.MaxInt64))
		starved <- predicateErr
	}()
	// The negative half of the claim ("the predicate does not return") cannot be
	// observed by a condition, so a clock serves here as a FAILURE CEILING only:
	// a return inside the ceiling falsifies the claim. The positive half below
	// is a condition wait and carries the proof.
	const starvationCeiling = 2 * time.Second
	ceiling := time.NewTimer(starvationCeiling)
	defer ceiling.Stop()
	select {
	case predicateErr := <-starved:
		_ = tx.Rollback()
		t.Fatalf("the governance predicate returned (err=%v) while a pasture transaction held the only pooled connection; want it to wait, because provenance leases its own connection from the same pool and the pool size is one", predicateErr)
	case <-ceiling.C:
		// Expected: the predicate is still waiting for a connection.
	}
	// Releasing the transaction releases the only connection. The predicate then
	// completes: that is the condition proving it was waiting for the lease.
	if err := tx.Rollback(); err != nil {
		t.Fatalf("roll back the read transaction: %v", err)
	}
	if predicateErr := <-starved; predicateErr != nil {
		t.Fatalf("the governance predicate failed after the transaction released the connection: %v", predicateErr)
	}

	// (2) MUTATION: give the pool a second connection and run the same reads on
	// two transactions with a write between them. The snapshots differ, which
	// is the fact that makes the torn check of probe 4 load-bearing.
	widePath := filepath.Join(t.TempDir(), "wide.db")
	openedWide, err := OpenTaskTrackerWithOptions(widePath, WithMaxOpenConns(4))
	if err != nil {
		t.Fatalf("open a store with a wider pool: %v", err)
	}
	wide, ok := openedWide.(*trackerImpl)
	if !ok {
		t.Fatalf("OpenTaskTrackerWithOptions returned %T, want *trackerImpl", openedWide)
	}
	defer wide.Close()

	wideActor := feasibilityActor(t, wide, "probe5-wide")
	wideTask := createHumanTestTask(t, wide, "probe5-wide")
	seedFeasibilityEpisode(t, wide, wideTask, "probe5-wide-owner", wideActor, "probe5-wide-start")

	firstTx, err := wide.auditDB.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin the first read transaction: %v", err)
	}
	before := countJournalRows(t, firstTx)
	// The provenance read succeeds while the transaction is open, which proves
	// it did NOT join that transaction.
	if _, err := wide.Journal().AuthorityGovernsTaskAt(1, wideTask, provenance.JournalID(math.MaxInt64)); err != nil {
		t.Fatalf("the governance predicate failed on the wider pool: %v", err)
	}
	appendFillerEvents(t, wide, wideTask, wideActor, "probe5-wide", 1)
	duringFirst := countJournalRows(t, firstTx)
	if err := firstTx.Rollback(); err != nil {
		t.Fatalf("roll back the first read transaction: %v", err)
	}
	secondTx, err := wide.auditDB.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin the second read transaction: %v", err)
	}
	after := countJournalRows(t, secondTx)
	if err := secondTx.Rollback(); err != nil {
		t.Fatalf("roll back the second read transaction: %v", err)
	}
	if duringFirst != before {
		t.Fatalf("the first read transaction saw %d journal rows after the write and %d before it; want the same count, because one transaction is one snapshot", duringFirst, before)
	}
	if after <= before {
		t.Fatalf("the second read transaction saw %d journal rows, the first saw %d; want strictly more, which is the snapshot difference this mutation demonstrates", after, before)
	}
}

// countJournalRows reads the journal row count inside the caller's transaction,
// so two calls on one transaction report one snapshot.
func countJournalRows(t *testing.T, tx *sql.Tx) int64 {
	t.Helper()
	var count int64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM journal`).Scan(&count); err != nil {
		t.Fatalf("count journal rows inside the transaction: %v", err)
	}
	return count
}

// ─── probe 6a ────────────────────────────────────────────────────────────────

// TestGateFeasibilityProbe6aAuthorityIdFromTheApplyResult proves WHERE the
// assignment authority JournalID comes from at write time.
//
// THE EXACT FIELD: provenance.CommittedResult.ResultSlots, the binding whose
// Slot is the ResultSlotID the caller set on its EffectAssignmentStart effect;
// the value is that binding's ProducedJournalID, and its Kind is
// JournalKindAuthority. EmittedEvents is NOT the source: it is the task_event
// closure only, and the authority row is not a task event.
//
// THE ID IS AVAILABLE ONLY AFTER THE JOURNAL COMMIT. Apply returns it; nothing
// before Apply carries it. An index write that needs the id therefore happens
// AFTER Apply, never inside the journal Apply transaction.
//
// RED when: the slot binding stops carrying the authority row, or the id it
// carries stops matching what the existing backwards scan recovers.
func TestGateFeasibilityProbe6aAuthorityIdFromTheApplyResult(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()

	actor := feasibilityActor(t, tracker, "probe6a")
	task := createHumanTestTask(t, tracker, "probe6a")
	episode := seedFeasibilityEpisode(t, tracker, task, "probe6a-owner", actor, "probe6a-start")

	if episode.authority >= episode.event {
		t.Fatalf("authority row %d is not below its material event %d; the authority is expected between the operation anchor and the material event", episode.authority, episode.event)
	}

	// The same id, recovered by the EXISTING backwards scan.
	scanned := scanRecoveredAuthority(t, tracker, task, episode.event)
	if scanned != episode.authority {
		t.Fatalf("the backwards scan recovered authority %d; the Apply result slot yielded %d; want the same id", scanned, episode.authority)
	}

	// MUTATION: the emitted-event closure is NOT a source of the authority id.
	page, err := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{task}, Limit: 16})
	if err != nil {
		t.Fatalf("read the task events: %v", err)
	}
	for _, row := range page.Events {
		if row.JournalID == episode.authority {
			t.Fatalf("journal row %d is both the assignment authority and a task event; the authority row is expected to be a separate journal kind, so the emitted-event closure cannot be read as the authority source", row.JournalID)
		}
	}
}

// TestGateFeasibilityProbe6aAuthorityIdFromTheComposedAllocationResult proves
// the SAME question for the shape the four production assignment writers use:
// a composed governed allocation, where provenance (not pasture) creates the
// child assignment authority.
//
// THE EXACT FIELD on that path:
// GovernedAllocationComposedResult.Closure().Children()[i].AssignmentRow.JournalID
// (provenance GovernedChildBinding / GovernedProducedRow). The sibling field
// TaskRow.JournalID carries the child's task row. Neither is available before
// the allocation commits.
//
// RED when: the closure stops carrying the assignment row, or the id it carries
// stops matching what the existing backwards recovery finds.
func TestGateFeasibilityProbe6aAuthorityIdFromTheComposedAllocationResult(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	bindTestGovernedAllocation(t, tracker)

	actor := feasibilityActor(t, tracker, "probe6a-composed")
	parentTask := createHumanTestTask(t, tracker, "probe6a-composed-parent")
	const parentAssignment provenance.AssignmentID = "probe6a-composed-parent-owner"
	parent := seedFeasibilityEpisode(t, tracker, parentTask, parentAssignment, actor, "probe6a-composed-parent-start")

	child := deterministicTask("probe6a-composed", "child")
	const childAssignment provenance.AssignmentID = "probe6a-composed-child-owner"
	childEvent, err := MapMaterialEvent(AssignmentStartedEvent{Task: child, Assignment: childAssignment, Role: RoleOwnerResponsibility, Occupant: actor})
	if err != nil {
		t.Fatalf("map the composed assignment-start event: %v", err)
	}
	childEvent.ResultSlot = feasibilityEventSlot

	allocator := tracker.allocationRunner
	if allocator == nil {
		t.Fatalf("the test tracker has no composed-allocation runner after binding one")
	}
	result, err := allocator.RunAllocateComposed(context.Background(), "probe6a-composed", parent.authority, provenance.GovernedAllocationComposedRequest{
		Version: provenance.GovernedAllocationCompositionV1,
		Allocation: provenance.GovernedAllocationRequest{
			OperationID: "probe6a-composed", ActorID: actor, Command: "pasture.gate.feasibility.v1", ParentAssignmentID: parentAssignment,
			Children: []provenance.GovernedChildSpec{{TaskID: child, AssignmentID: childAssignment, Occupant: actor, Title: "feasibility child", Description: "feasibility child", Type: provenance.TaskTypeTask, Priority: provenance.PriorityMedium, Phase: provenance.PhaseWorkerSlices}},
		},
		SupplementalEffects: []provenance.Effect{childEvent},
	})
	if err != nil {
		t.Fatalf("run the composed allocation: %v", err)
	}
	children := result.Closure().Children()
	if len(children) != 1 || children[0].TaskID != child || children[0].AssignmentID != childAssignment {
		t.Fatalf("the composed closure carried %+v; want exactly the requested child %q and assignment %q", children, child, childAssignment)
	}
	authority := children[0].AssignmentRow.JournalID
	if authority == 0 {
		t.Fatalf("the composed closure bound assignment row %+v; want a committed journal id", children[0].AssignmentRow)
	}

	// The material event id comes from the supplemental result slots.
	var event provenance.JournalID
	for _, slot := range result.SupplementalResultSlots() {
		if slot.Slot == feasibilityEventSlot {
			event = slot.ProducedJournalID
		}
	}
	if event == 0 {
		t.Fatalf("the composed result omitted the material-event slot %q: %+v", feasibilityEventSlot, result.SupplementalResultSlots())
	}

	// The same id, recovered by the EXISTING backwards recovery.
	scanned := scanRecoveredAuthority(t, tracker, child, event)
	if scanned != authority {
		t.Fatalf("the backwards recovery found authority %d; the composed closure yielded %d; want the same id", scanned, authority)
	}

	// The recovery cost of THIS shape, which is the shape the four production
	// assignment writers use, measured in operations for probe 6b.
	cost := measureAuthorityScanCost(t, tracker, child, feasibilityEpisode{task: child, occupant: actor, authority: authority, event: event})
	t.Logf("probe 6b, composed-allocation shape: authority row %d, material event %d, distance %d; recovery cost %d predicate calls over %d candidates",
		authority, event, event-authority, cost.predicateCalls, cost.candidates)

	// MUTATION: the child's task row is NOT the authority row; reading the wrong
	// field of the same binding yields a different journal id.
	if children[0].TaskRow.JournalID == authority {
		t.Fatalf("the child's task row and assignment row share journal id %d; want two rows, so the choke point cannot read the wrong one and still pass", authority)
	}
}

// scanRecoveredAuthority recovers the authority id for one material event
// through the production service method assignmentAuthorityForEvent.
func scanRecoveredAuthority(t *testing.T, tracker *trackerImpl, task provenance.TaskID, event provenance.JournalID) provenance.JournalID {
	t.Helper()
	service := feasibilityAssignmentService(t, tracker)
	row := feasibilityEventRow(t, tracker, task, event)
	authority, found, err := service.assignmentAuthorityForEvent(context.Background(), row, task)
	if err != nil || !found {
		t.Fatalf("the backwards scan did not recover an authority for event %d on task %q: found=%t err=%v", event, task, found, err)
	}
	return authority
}

// feasibilityAssignmentService reaches the production assignment service, which
// owns the backwards scan.
func feasibilityAssignmentService(t *testing.T, tracker *trackerImpl) *epochAssignmentService {
	t.Helper()
	service, err := tracker.NewEpochService(EpochServiceOptions{})
	if err != nil {
		t.Fatalf("construct the epoch service: %v", err)
	}
	assignment, ok := service.(*epochService).EpochAssignmentService.(*epochAssignmentService)
	if !ok {
		t.Fatalf("the epoch service does not carry the assignment service")
	}
	return assignment
}

// feasibilityEventRow reads back one committed task-event row by its journal id.
func feasibilityEventRow(t *testing.T, tracker *trackerImpl, task provenance.TaskID, event provenance.JournalID) provenance.TaskEventRow {
	t.Helper()
	page, err := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{task}, AfterJournalID: event - 1, Limit: 1})
	if err != nil {
		t.Fatalf("read task-event row %d: %v", event, err)
	}
	if len(page.Events) != 1 || page.Events[0].JournalID != event {
		t.Fatalf("read task-event row %d returned %+v; want exactly that row", event, page.Events)
	}
	return page.Events[0]
}

// ─── probe 6b ────────────────────────────────────────────────────────────────

// TestGateFeasibilityProbe6bBackfillBound MEASURES the per-fact work of
// recovering the authority id for a HISTORICAL assignment-start fact, in
// OPERATIONS (governance predicate calls and journal ids walked), never in
// wall-clock time.
//
// The recovery walks backwards over journal ids from the material event. Its
// cost is therefore the DISTANCE between the authority row and its material
// event, and, when the first walk finds nothing, the distance from the event's
// producing operation down to the TASK'S BIRTH.
//
// Two shapes are measured, because they cost different amounts:
//   - ADJACENT: the authority and its material event are committed by ONE
//     operation, so the authority sits directly below the event. This is the
//     shape every assignment command writes today.
//   - SEPARATED: the authority was committed by an EARLIER operation than its
//     material event, with a stretch of journal between them. The walk covers
//     that whole stretch.
//
// MEASURED, on every shape below and on the composed-allocation shape of the
// probe above: the material event row carries NO producing-operation id, so the
// recovery takes its single-range walk, from the material event down to the
// task's birth. The per-fact cost is therefore that distance. The two-range
// fallback the recovery also carries was reached by no shape measured here.
//
// THE COMMITTED STRETCH IS A CHEAP REGRESSION GUARD, NOT THE MEASUREMENT. The
// bound was measured once on a journal of 3028 rows: the adjacent shape cost 2
// predicate calls over 1 candidate, the composed-allocation shape 3 calls over
// 2 candidates at a distance of 2, and the separated shape 3018 calls over 3017
// candidates, that is one call per journal id of the stretch. The stretch kept
// below is small enough to leave the suite fast while still proving that the
// separated cost scales with it. A reader who wants the large number again
// raises the constant and reruns.
//
// RED when: the adjacent shape stops being cheap, or the separated shape stops
// scaling with the stretch. Either means the recovery mechanism changed and the
// backfill bound must be measured again.
func TestGateFeasibilityProbe6bBackfillBound(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()

	actor := feasibilityActor(t, tracker, "probe6b")

	// ADJACENT: one operation commits the authority and its material event.
	adjacentTask := createHumanTestTask(t, tracker, "probe6b-adjacent")
	adjacent := seedFeasibilityEpisode(t, tracker, adjacentTask, "probe6b-adjacent-owner", actor, "probe6b-adjacent-start")
	adjacentCost := measureAuthorityScanCost(t, tracker, adjacentTask, adjacent)

	// SEPARATED: the authority is committed first, then a stated stretch of
	// journal, then the material event.
	const stretch = 250
	separatedTask := createHumanTestTask(t, tracker, "probe6b-separated")
	separated := seedSeparatedEpisode(t, tracker, separatedTask, "probe6b-separated-owner", actor, "probe6b-separated", stretch)
	separatedCost := measureAuthorityScanCost(t, tracker, separatedTask, separated)

	total := journalMaximum(t, tracker)
	t.Logf("probe 6b: journal maximum %d over 2 assignment-start facts and %d filler events; adjacent shape %d predicate calls over %d candidates; separated shape %d predicate calls over %d candidates",
		total, stretch, adjacentCost.predicateCalls, adjacentCost.candidates, separatedCost.predicateCalls, separatedCost.candidates)

	if adjacentCost.predicateCalls > 8 {
		t.Fatalf("the adjacent shape cost %d predicate calls; want at most 8, because one operation places the authority directly below its material event", adjacentCost.predicateCalls)
	}
	if separatedCost.candidates < stretch {
		t.Fatalf("the separated shape walked %d candidates over a %d-event stretch; want at least the stretch, because the walk runs from the material event down to the task's birth and the stretch lies between them", separatedCost.candidates, stretch)
	}

	// MUTATION: add one extra predicate call per candidate and the adjacent
	// bound above turns RED. Demonstrated on the adjacent shape, where the
	// bound is asserted.
	inflated := adjacentCost
	inflated.predicateCalls += inflated.candidates * 8
	if inflated.predicateCalls <= 8 {
		t.Fatalf("the inflated adjacent cost is %d predicate calls; want it above the asserted bound of 8, so the bound is shown to be load-bearing", inflated.predicateCalls)
	}

	// The production method is the oracle for both shapes.
	service := feasibilityAssignmentService(t, tracker)
	for _, shape := range []struct {
		name    string
		task    provenance.TaskID
		episode feasibilityEpisode
	}{{"adjacent", adjacentTask, adjacent}, {"separated", separatedTask, separated}} {
		row := feasibilityEventRow(t, tracker, shape.task, shape.episode.event)
		authority, found, err := service.assignmentAuthorityForEvent(context.Background(), row, shape.task)
		if err != nil || !found || authority != shape.episode.authority {
			t.Fatalf("the production recovery returned authority %d (found=%t err=%v) for the %s shape; want %d", authority, found, err, shape.name, shape.episode.authority)
		}
	}
}

// seedSeparatedEpisode commits the assignment authority in ONE operation, then
// a stretch of filler events, then the material FamilyAssignmentStarted event in
// a SECOND operation, so the authority is far below its own material event.
func seedSeparatedEpisode(t *testing.T, tracker *trackerImpl, task provenance.TaskID, assignment provenance.AssignmentID, occupant provenance.ActorID, prefix string, stretch int) feasibilityEpisode {
	t.Helper()
	_, systemAuthority, found, err := readSystemIdentity(tracker.auditDB)
	if err != nil || !found {
		t.Fatalf("read system identity: found=%t err=%v", found, err)
	}
	authorityResult, err := tracker.Journal().Apply(provenance.OperationInput{
		OperationID:        provenance.OperationID(prefix + "-authority"),
		ActorID:            occupant,
		AuthorityJournalID: &systemAuthority,
		CommandDigest:      []byte(prefix + "-authority"),
		Effects: []provenance.Effect{
			{Sort: provenance.EffectAssignmentStart, ResultSlot: feasibilityAuthoritySlot, TaskID: task, AssignmentID: assignment, SlotID: provenance.SlotOwnerResponsibility, Occupant: occupant},
		},
	})
	if err != nil {
		t.Fatalf("commit the separated authority: %v", err)
	}
	episode := feasibilityEpisode{task: task, occupant: occupant}
	for _, slot := range authorityResult.ResultSlots {
		if slot.Slot == feasibilityAuthoritySlot {
			episode.authority = slot.ProducedJournalID
		}
	}
	if episode.authority == 0 {
		t.Fatalf("the authority-only Apply bound no authority slot: %+v", authorityResult.ResultSlots)
	}

	appendFillerEvents(t, tracker, task, occupant, prefix, stretch)

	event, err := MapMaterialEvent(AssignmentStartedEvent{Task: task, Assignment: assignment, Role: RoleOwnerResponsibility, Occupant: occupant})
	if err != nil {
		t.Fatalf("map the separated assignment-start event: %v", err)
	}
	event.ResultSlot = feasibilityEventSlot
	eventResult, err := tracker.Journal().Apply(provenance.OperationInput{
		OperationID:        provenance.OperationID(prefix + "-event"),
		ActorID:            occupant,
		AuthorityJournalID: &systemAuthority,
		CommandDigest:      []byte(prefix + "-event"),
		Effects:            []provenance.Effect{event},
	})
	if err != nil {
		t.Fatalf("commit the separated material event: %v", err)
	}
	for _, slot := range eventResult.ResultSlots {
		if slot.Slot == feasibilityEventSlot {
			episode.event = slot.ProducedJournalID
		}
	}
	if episode.event == 0 {
		t.Fatalf("the event-only Apply bound no event slot: %+v", eventResult.ResultSlots)
	}
	return episode
}

// authorityScanCost is the measured per-fact work of one authority recovery.
type authorityScanCost struct {
	// candidates is the number of journal ids the walk visits.
	candidates int
	// predicateCalls is the number of AuthorityGovernsTaskAt calls the walk makes.
	predicateCalls int
}

// measureAuthorityScanCost walks the SAME candidate ranges the production
// recovery walks (internal/tasks/epoch_assignment_service.go, in
// assignmentAuthorityForEvent), calling the SAME exported predicate, and counts
// the calls. It mirrors both of that function's branches: the two-range walk
// taken when the material event row names a producing operation, and the
// single-range walk from the material event down to the task's birth taken when
// it does not. Every shape measured here takes the single-range walk. The
// production method is the oracle: this walk must recover the id it recovers.
func measureAuthorityScanCost(t *testing.T, tracker *trackerImpl, task provenance.TaskID, episode feasibilityEpisode) authorityScanCost {
	t.Helper()
	service := feasibilityAssignmentService(t, tracker)
	birth, err := service.taskBirthJournalID(context.Background(), task)
	if err != nil {
		t.Fatalf("read the task birth journal id for %q: %v", task, err)
	}
	row := feasibilityEventRow(t, tracker, task, episode.event)

	cost := authorityScanCost{}
	walk := func(high, low provenance.JournalID) (provenance.JournalID, bool) {
		for candidate := high; candidate > low; candidate-- {
			cost.candidates++
			cost.predicateCalls++
			direct, err := tracker.Journal().AuthorityGovernsTaskAt(candidate, task, candidate+1)
			if err != nil {
				t.Fatalf("classify candidate authority %d for task %q: %v", candidate, task, err)
			}
			if !direct {
				continue
			}
			cost.predicateCalls++
			governsAtEvent, err := tracker.Journal().AuthorityGovernsTaskAt(candidate, task, episode.event)
			if err != nil {
				t.Fatalf("check candidate authority %d at event %d for task %q: %v", candidate, episode.event, task, err)
			}
			if governsAtEvent {
				return candidate, true
			}
		}
		return 0, false
	}

	var recovered provenance.JournalID
	var found bool
	if row.ProducedByOperationJournalID != nil && *row.ProducedByOperationJournalID > birth && *row.ProducedByOperationJournalID < row.JournalID {
		recovered, found = walk(row.JournalID-1, *row.ProducedByOperationJournalID)
		if !found {
			recovered, found = walk(*row.ProducedByOperationJournalID-1, birth)
		}
	} else {
		recovered, found = walk(row.JournalID-1, birth)
	}
	if !found || recovered != episode.authority {
		t.Fatalf("the counted walk recovered authority %d (found=%t) for task %q; want %d, the id the Apply result bound", recovered, found, task, episode.authority)
	}
	return cost
}

// ─── probe 7 ─────────────────────────────────────────────────────────────────

// TestGateFeasibilityProbe7PastureWriteUnderAHeldWriteLock proves that a
// pasture-owned write from the gate path surfaces a HELD write lock as the
// SQLite busy timeout of the store's own tier, and that provenance write
// ownership of the shared file is undisturbed afterwards.
//
// The lock is held by a SEPARATE connection for as long as the probe needs it.
// The hold is released by a CONDITION the test signals, never by a sleep, and
// the observation (the busy error) and the action it justifies (releasing the
// holder) are ordered by that same signal.
//
// RED when: the contended write succeeds, or reports something other than a
// busy timeout, or the store cannot commit through provenance afterwards.
func TestGateFeasibilityProbe7PastureWriteUnderAHeldWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pasture.db")
	profile := timeouts.DeadlineTestProfile()
	opened, err := OpenTaskTrackerWithOptions(path, WithTimeoutProfile(profile))
	if err != nil {
		t.Fatalf("open the store with the tight timeout profile: %v", err)
	}
	tracker, ok := opened.(*trackerImpl)
	if !ok {
		t.Fatalf("OpenTaskTrackerWithOptions returned %T, want *trackerImpl", opened)
	}
	defer tracker.Close()

	actor := feasibilityActor(t, tracker, "probe7")
	task := createHumanTestTask(t, tracker, "probe7")

	// A pasture-owned table, created through the same handle the gate path uses.
	if _, err := tracker.auditDB.Exec(`CREATE TABLE IF NOT EXISTS pasture_gate_feasibility_probe (id INTEGER PRIMARY KEY, note TEXT NOT NULL)`); err != nil {
		t.Fatalf("create the throwaway pasture-owned table: %v", err)
	}

	// A second connection on the same file takes the write lock and keeps it.
	holder, err := dbconn.OpenSharedDBWithProfile(path, profile)
	if err != nil {
		t.Fatalf("open the lock-holding connection: %v", err)
	}
	defer holder.Close()

	held := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		tx, beginErr := holder.BeginTx(context.Background(), nil)
		if beginErr != nil {
			holderDone <- fmt.Errorf("begin the holding transaction: %w", beginErr)
			close(held)
			return
		}
		if _, execErr := tx.Exec(`INSERT INTO pasture_gate_feasibility_probe (note) VALUES ('holder')`); execErr != nil {
			_ = tx.Rollback()
			holderDone <- fmt.Errorf("write inside the holding transaction: %w", execErr)
			close(held)
			return
		}
		// The write lock is now held for certain. Signal, then wait for the
		// test to say the contended write has been observed.
		close(held)
		<-release
		holderDone <- tx.Rollback()
	}()

	<-held
	select {
	case holderErr := <-holderDone:
		t.Fatalf("the lock holder ended before the contended write ran: %v", holderErr)
	default:
	}

	// The gate-path write. The store's tier is the profile's SQLiteBusy value,
	// which the shared DSN applied as busy_timeout on this handle.
	_, writeErr := tracker.auditDB.Exec(`INSERT INTO pasture_gate_feasibility_probe (note) VALUES ('gate')`)
	close(release)
	if holderErr := <-holderDone; holderErr != nil {
		t.Fatalf("the lock holder failed to roll back: %v", holderErr)
	}
	if writeErr == nil {
		t.Fatalf("the pasture-owned write succeeded while another connection held the write lock for longer than the %s busy tier; want a busy timeout, because that is the error the gate maps to a stale-index refusal", profile.SQLiteBusy())
	}
	if !isSQLiteBusy(writeErr) {
		t.Fatalf("the contended pasture-owned write failed with %v; want a SQLite busy timeout, because a held write lock must surface as the tier's timeout and not as another fault", writeErr)
	}

	// Provenance write ownership of the shared file is undisturbed: a later
	// Apply still commits, and the pasture table is intact and uncorrupted.
	seedFeasibilityEpisode(t, tracker, task, "probe7-owner", actor, "probe7-start")
	var rows int
	if err := tracker.auditDB.QueryRow(`SELECT COUNT(*) FROM pasture_gate_feasibility_probe`).Scan(&rows); err != nil {
		t.Fatalf("read the throwaway pasture-owned table after the contention: %v", err)
	}
	if rows != 0 {
		t.Fatalf("the throwaway pasture-owned table holds %d rows; want 0, because the holder rolled back and the contended write never committed", rows)
	}
}

// isSQLiteBusy reports whether err is the driver's locked-or-busy outcome. The
// modernc driver reports it in the message text, so the match is on the two
// names SQLite itself uses.
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "database is locked") || strings.Contains(text, "busy")
}
