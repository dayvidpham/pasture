package engine

// schema_gate_test.go — the durable schema gate's bounded, progress-based wait.
//
// Subject: what RequireSupportedDurableSchema does with a layout below the
// floor. Three endings, each with its own sentence:
//
//   - the layout reaches the floor while the gate watches (another process was
//     writing it): the gate accepts the file and says nothing;
//   - the layout stands still for one stability window: the gate refuses it with
//     the older-build sentence, unchanged from before the wait existed;
//   - the layout keeps moving until the bound runs out: the gate says another
//     process was migrating the file and did not finish, and never blames an
//     older build.
//
// The wait is driven through its seams — a scripted look at the layout and a
// clock that advances on every pause — so every ending is reached in
// microseconds and the deadline path is asserted without a real clock. One
// test then runs the REAL look against a real file, so the numbers the scripted
// tests hand in are the numbers production reads.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// scriptedLayout is a look at the layout that returns a scripted sequence and
// then repeats its last entry. next, when set, generates every look past the
// script instead.
//
// maxLooks bounds how many looks one wait may take. A wait that exceeds it has
// lost its deadline check, and the test fails BY NAME at that look instead of
// looping until the package times out. looksWithinBound derives it from the
// profile the test hands the gate.
type scriptedLayout struct {
	t        *testing.T
	script   []durableLayoutObservation
	next     func(look int) durableLayoutObservation
	looks    int
	maxLooks int
}

// looksWithinBound is the most looks a wait on profile may take: the first
// look, one per interval up to the bound, and a margin of two.
func looksWithinBound(profile timeouts.Profile) int {
	return int(profile.WorkflowResult()/profile.SQLiteBusy()) + 3
}

func (s *scriptedLayout) probe(context.Context) (durableLayoutObservation, error) {
	if s.maxLooks > 0 && s.looks >= s.maxLooks {
		s.t.Fatalf("the gate took %d looks, more than the bound allows (%d); the deadline check is gone",
			s.looks+1, s.maxLooks)
	}
	look := s.looks
	s.looks++
	if look < len(s.script) {
		return s.script[look], nil
	}
	if s.next != nil {
		return s.next(look), nil
	}
	return s.script[len(s.script)-1], nil
}

// belowFloor is one look at a layout the library refuses at the given version.
func belowFloor(version int64) durableLayoutObservation {
	return durableLayoutObservation{
		state:   durableLayoutSuperseded,
		version: version,
		refusal: fmt.Errorf("scripted refusal: records DBOS system schema version %d, below the supported floor: %w",
			version, provenance.ErrSupersededDBOSSystemSchema),
	}
}

var (
	writerHeld = durableLayoutObservation{state: durableLayoutWriterHeld}
	usable     = durableLayoutObservation{state: durableLayoutUsable}
)

// fakeSchemaClock advances by the requested pause instead of waiting, and
// records every pause it was asked for.
type fakeSchemaClock struct {
	at     time.Time
	pauses []time.Duration
	// onPause, when set, runs before each pause is recorded; a test uses it to
	// cancel the wait at a chosen point.
	onPause func(pause int)
}

func (c *fakeSchemaClock) clock() schemaGateClock {
	return schemaGateClock{
		now: func() time.Time { return c.at },
		pause: func(ctx context.Context, d time.Duration) error {
			if c.onPause != nil {
				c.onPause(len(c.pauses))
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			c.pauses = append(c.pauses, d)
			c.at = c.at.Add(d)
			return nil
		},
	}
}

func (c *fakeSchemaClock) elapsedSince(start time.Time) time.Duration { return c.at.Sub(start) }

const gateTestPath = "/var/lib/pasture/pasture.db"

// A loser that opens the file while the winner is still writing the layout
// sees a below-floor version, then a held lock, then a rising version, and
// finally a usable layout. It must wait through all of that and be accepted;
// before the wait existed it was refused on the first look.
//
// MUTATION: make the gate refuse on its first below-floor look (return the
// older-build error before the loop). This test fails with "the gate refused a
// layout another process was still writing".
func TestALoserThatOpensMidBootstrapWaitsForTheWinnerAndIsAccepted(t *testing.T) {
	t.Parallel()
	profile := timeouts.ProductionProfile()
	look := &scriptedLayout{t: t, maxLooks: looksWithinBound(profile),
		script: []durableLayoutObservation{belowFloor(1), writerHeld, belowFloor(20), belowFloor(41), usable}}
	clock := &fakeSchemaClock{at: time.Unix(1_700_000_000, 0)}
	start := clock.at

	err := awaitSupportedDurableSchema(t.Context(), engineConstructionSite, gateTestPath, look.probe, profile, clock.clock())
	if err != nil {
		t.Fatalf("the gate refused a layout another process was still writing: %v", err)
	}
	if look.looks != 5 {
		t.Errorf("looks = %d, want 5: one per scripted observation, and no look after the usable one", look.looks)
	}
	// Every pause between looks is one SQLiteBusy window of the profile the
	// caller handed in, and nothing else.
	if len(clock.pauses) != 4 {
		t.Fatalf("pauses = %v, want 4 (one before each look after the first)", clock.pauses)
	}
	for i, pause := range clock.pauses {
		if pause != profile.SQLiteBusy() {
			t.Errorf("pause %d = %s, want the profile's SQLiteBusy window %s", i, pause, profile.SQLiteBusy())
		}
	}
	if elapsed := clock.elapsedSince(start); elapsed >= profile.WorkflowResult() {
		t.Errorf("the wait consumed %s, which is the whole WorkflowResult bound %s; a winner that finishes must be accepted before the bound", elapsed, profile.WorkflowResult())
	}
}

// A layout that stands still below the floor for one stability window is an
// older build's database, or an interrupted first start. The refusal must keep
// the sentence an operator reads today, must still match the library's
// sentinel, and must arrive after ONE window — not after the whole bound.
//
// MUTATION: report a stable layout with the unfinished-migration error. This
// test fails on the older-build sentence, and again on the sentinel.
func TestAStableOldLayoutIsRefusedWithTheOlderBuildSentenceAfterOneWindow(t *testing.T) {
	t.Parallel()
	profile := timeouts.ProductionProfile()
	look := &scriptedLayout{t: t, maxLooks: looksWithinBound(profile), script: []durableLayoutObservation{belowFloor(41)}}
	clock := &fakeSchemaClock{at: time.Unix(1_700_000_000, 0)}
	start := clock.at

	err := awaitSupportedDurableSchema(t.Context(), engineConstructionSite, gateTestPath, look.probe, profile, clock.clock())
	if err == nil {
		t.Fatal("the gate accepted a layout that stood still below the floor")
	}
	structured := requireStructuredStorageError(t, err)
	if !errors.Is(err, provenance.ErrSupersededDBOSSystemSchema) {
		t.Errorf("a stable below-floor layout no longer matches the library's sentinel: %v", err)
	}
	if want := "an older build, or an interrupted first\nstart, left it in a layout this build doesn't read"; !strings.Contains(structured.What, want) {
		t.Errorf("the refusal lost the older-build sentence %q:\n%s", want, renderReport(structured))
	}
	if forbidden := "another process was migrating"; strings.Contains(renderReport(structured), forbidden) {
		t.Errorf("a layout that never moved was reported as %q:\n%s", forbidden, renderReport(structured))
	}
	if look.looks != 2 {
		t.Errorf("looks = %d, want 2: one look, one stability window, one confirming look", look.looks)
	}
	if elapsed := clock.elapsedSince(start); elapsed != profile.SQLiteBusy() {
		t.Errorf("the refusal took %s, want exactly one SQLiteBusy window %s; a stable layout must not wait out the whole bound", elapsed, profile.SQLiteBusy())
	}
}

// A layout that keeps moving until the bound runs out was being written by
// another process the whole time. The refusal must say so, name the bound, and
// never say "older build": the gate watched the file being written, and that
// sentence would send the operator to delete it.
//
// MUTATION: report the expired bound with the older-build error. This test
// fails on the unfinished sentence, and again on the forbidden one.
//
// MUTATION: remove the deadline check ("if false && ..."). The look cap fails
// this test by name — "the gate took N looks, more than the bound allows; the
// deadline check is gone" — at the look after the bound, instead of the
// package hanging until go test's timeout.
func TestAMigrationThatOutlivesTheBoundIsReportedAsUnfinishedAndNeverAsAnOlderBuild(t *testing.T) {
	t.Parallel()
	profile := timeouts.ProductionProfile()
	look := &scriptedLayout{
		t:        t,
		maxLooks: looksWithinBound(profile),
		script:   []durableLayoutObservation{belowFloor(1)},
		// Every later look sees the version one higher, and never the floor.
		next: func(look int) durableLayoutObservation { return belowFloor(int64(look + 1)) },
	}
	clock := &fakeSchemaClock{at: time.Unix(1_700_000_000, 0)}
	start := clock.at

	err := awaitSupportedDurableSchema(t.Context(), engineConstructionSite, gateTestPath, look.probe, profile, clock.clock())
	if err == nil {
		t.Fatal("the gate accepted a layout that never reached the floor")
	}
	structured := requireStructuredStorageError(t, err)
	if errors.Is(err, provenance.ErrSupersededDBOSSystemSchema) {
		t.Errorf("a migration that outlived the bound was reported as the library's refusal of the file: %v", err)
	}
	want := fmt.Sprintf("another process was migrating it and did not finish\nwithin %s", profile.WorkflowResult())
	if !strings.Contains(structured.What, want) {
		t.Errorf("the refusal does not say %q:\n%s", want, renderReport(structured))
	}
	for _, forbidden := range []string{"older build", "interrupted first"} {
		if strings.Contains(renderReport(structured), forbidden) {
			t.Errorf("a file the gate watched being written was blamed on an %q:\n%s", forbidden, renderReport(structured))
		}
	}
	if !strings.Contains(structured.Why, "advanced from 1 to") {
		t.Errorf("the refusal does not report the versions it watched:\n%s", renderReport(structured))
	}
	if elapsed := clock.elapsedSince(start); elapsed < profile.WorkflowResult() {
		t.Errorf("the gate gave up after %s, before the WorkflowResult bound %s", elapsed, profile.WorkflowResult())
	}
	if look.looks < 3 {
		t.Errorf("looks = %d; a wait that runs to the bound must keep looking", look.looks)
	}
}

// A caller that cancels start-up while the gate is still deciding gets a
// sentence that says what was being waited for, and the cancellation cause.
func TestACancelledWaitSaysWhatItWasWaitingFor(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	look := &scriptedLayout{t: t, maxLooks: looksWithinBound(timeouts.ProductionProfile()),
		script: []durableLayoutObservation{belowFloor(3), belowFloor(4)}}
	clock := &fakeSchemaClock{at: time.Unix(1_700_000_000, 0)}
	clock.onPause = func(pause int) {
		if pause == 1 {
			cancel()
		}
	}

	err := awaitSupportedDurableSchema(ctx, engineConstructionSite, gateTestPath, look.probe, timeouts.ProductionProfile(), clock.clock())
	if err == nil {
		t.Fatal("a cancelled wait returned nil")
	}
	structured := requireStructuredStorageError(t, err)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the cancellation cause is not wrapped: %v", err)
	}
	if !strings.Contains(structured.What, "was cancelled") {
		t.Errorf("the refusal does not say the wait was cancelled:\n%s", renderReport(structured))
	}
	if !strings.Contains(structured.Why, "recorded layout version 4") {
		t.Errorf("the refusal does not report the last observation:\n%s", renderReport(structured))
	}
}

// A look that fails BECAUSE the caller's context ended is a cancelled wait,
// and must be reported as one — never as an unreadable, damaged file. The
// cancel can land at the statement's own context check inside a look, on the
// first look when the context was already cancelled before the gate ran, or
// between a pause and the next look. Both look sites are pinned here with a
// look that returns the context's error, so no clock is involved.
//
// MUTATION: drop the ctx.Err() checks at the two look sites. Both cases then
// carry the unreadable-file sentence and fail here.
func TestALookThatFailsOnTheCancelledContextIsReportedAsCancelledNotDamaged(t *testing.T) {
	t.Parallel()
	profile := timeouts.ProductionProfile()
	failsOnContext := func(ctx context.Context) (durableLayoutObservation, error) {
		if err := ctx.Err(); err != nil {
			return durableLayoutObservation{}, fmt.Errorf("probe sqlite_master: %w", err)
		}
		return belowFloor(7), nil
	}

	t.Run("cancelled before the first look", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		clock := &fakeSchemaClock{at: time.Unix(1_700_000_000, 0)}
		err := awaitSupportedDurableSchema(ctx, engineConstructionSite, gateTestPath, failsOnContext, profile, clock.clock())
		assertCancelledNotDamaged(t, err)
	})

	t.Run("cancelled between a pause and the next look", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		// A pause that does not see the cancel itself, so the NEXT look is
		// the first thing that meets the cancelled context.
		clock := &fakeSchemaClock{at: time.Unix(1_700_000_000, 0)}
		blind := clock.clock()
		blind.pause = func(_ context.Context, d time.Duration) error {
			cancel()
			clock.at = clock.at.Add(d)
			return nil
		}
		err := awaitSupportedDurableSchema(ctx, engineConstructionSite, gateTestPath, failsOnContext, profile, blind)
		assertCancelledNotDamaged(t, err)
	})
}

func assertCancelledNotDamaged(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("a look that failed on the cancelled context returned nil")
	}
	structured := requireStructuredStorageError(t, err)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the cancellation is not wrapped as the cause: %v", err)
	}
	if !strings.Contains(structured.What, "was cancelled") {
		t.Errorf("the refusal does not say the wait was cancelled:\n%s", renderReport(structured))
	}
	for _, forbidden := range []string{"unreadable, damaged", "Couldn't check the durable-execution layout"} {
		if strings.Contains(renderReport(structured), forbidden) {
			t.Errorf("a cancelled wait was reported with %q:\n%s", forbidden, renderReport(structured))
		}
	}
}

// The real clock's pause must return at the cancellation, not at the end of
// the interval. This is the only thing that turns a shutdown signal during
// the wait into a prompt return, and no seam test can see it: the seam tests
// inject their own clock.
//
// MUTATION: make realSchemaGateClock.pause ignore its context ("<-timer.C;
// return nil"). The pause then runs to its full length and returns nil, and
// this test fails on both.
func TestTheRealClockPauseReturnsAtCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	const pause = 2 * time.Second
	start := time.Now()
	err := realSchemaGateClock().pause(ctx, pause)
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("pause returned %v after its context was cancelled, want the cancellation", err)
	}
	if elapsed >= pause {
		t.Errorf("pause ran for %s, its full length, after its context was cancelled; it must return at the cancellation", elapsed)
	}
}

// A cancel that lands during the REAL wait — while the real look is blocked
// on the driver's busy window, or in a real pause — comes back promptly as the
// cancelled sentence, never as an unreadable, damaged file. The gate is held
// waiting by a second handle that holds the file's write lock, so every look
// reports a writer and the wait would otherwise run to the bound.
//
// MUTATION: report a look that failed on the cancelled context as unreadable
// (drop the ctx.Err() checks at the two look sites). The sentence then says the
// file is unreadable or damaged, and this test fails on it.
func TestACancelDuringTheRealWaitIsReportedAsCancelledNotDamaged(t *testing.T) {
	t.Parallel()
	profile := timeouts.DeadlineTestProfile()
	path, _ := testutil.WriteSupersededDurableDatabase(t)
	db, err := dbconn.OpenSharedDBWithProfile(path, profile)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	holder, err := dbconn.OpenSharedDBWithProfile(path, profile)
	if err != nil {
		t.Fatalf("open the lock-holding handle on %s: %v", path, err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	held, err := holder.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("hold the write lock on %s: %v", path, err)
	}
	t.Cleanup(func() { _ = held.Rollback() })

	// The caller's own deadline is the cancel; it lands well inside the bound.
	const callerDeadline = 150 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), callerDeadline)
	defer cancel()
	start := time.Now()
	err = RequireSupportedDurableSchema(ctx, engineConstructionSite, db, path, profile)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a cancelled wait on a held, below-floor file returned nil")
	}
	structured := requireStructuredStorageError(t, err)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the caller's deadline is not wrapped as the cause: %v", err)
	}
	if !strings.Contains(structured.What, "was cancelled") {
		t.Errorf("the refusal does not say the wait was cancelled:\n%s", renderReport(structured))
	}
	for _, forbidden := range []string{"unreadable, damaged", "Couldn't check the durable-execution layout"} {
		if strings.Contains(renderReport(structured), forbidden) {
			t.Errorf("a cancelled wait was reported with %q:\n%s", forbidden, renderReport(structured))
		}
	}
	// Bounded: the return is the deadline plus at most one busy window and one
	// interval of the profile, with room for scheduling — never the bound.
	if limit := callerDeadline + profile.SQLiteBusy()*2 + 500*time.Millisecond; elapsed > limit {
		t.Errorf("the cancelled wait returned after %s, want within %s", elapsed, limit)
	}
	if elapsed >= profile.WorkflowResult() {
		t.Errorf("the cancelled wait ran to the bound %s; the cancel did not end it", profile.WorkflowResult())
	}
}

// The production look reads the same number the library reports. Pasture reads
// the layout version itself, for progress only, and the library does not
// expose the number it judged; this test holds the two together, so a runtime
// or library change that moves the bookkeeping fails here by name.
//
// It also drives the one observation the scripted tests cannot fake honestly:
// a writer holding the file's write lock, with a real lock and the driver's own
// busy wait, on the shortest profile this tree defines.
func TestTheProgressReaderReadsTheVersionTheLibraryReports(t *testing.T) {
	t.Parallel()
	profile := timeouts.DeadlineTestProfile()
	path, _ := testutil.WriteSupersededDurableDatabase(t)
	db, err := dbconn.OpenSharedDBWithProfile(path, profile)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	look := probeDurableLayout(db, path)

	observed, err := look(t.Context())
	if err != nil {
		t.Fatalf("look at a superseded layout: %v", err)
	}
	if observed.state != durableLayoutSuperseded {
		t.Fatalf("state = %v, want superseded for a layout at version %d", observed.state, testutil.SupersededDurableSchemaVersion)
	}
	if observed.version != testutil.SupersededDurableSchemaVersion {
		t.Errorf("version read = %d, want %d", observed.version, testutil.SupersededDurableSchemaVersion)
	}
	if !errors.Is(observed.refusal, provenance.ErrSupersededDBOSSystemSchema) {
		t.Errorf("the refusal is not the library's: %v", observed.refusal)
	}
	if want := fmt.Sprintf("version %d", observed.version); !strings.Contains(observed.refusal.Error(), want) {
		t.Errorf("the number pasture read (%d) is not the number the library reported: %v", observed.version, observed.refusal)
	}

	// A second handle holds the write lock, as a migration transaction does.
	// The look must report a writer, not a version, and must not block past the
	// driver's busy window.
	holder, err := dbconn.OpenSharedDBWithProfile(path, profile)
	if err != nil {
		t.Fatalf("open the lock-holding handle on %s: %v", path, err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	held, err := holder.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("hold the write lock on %s: %v", path, err)
	}
	observed, err = look(t.Context())
	if err != nil {
		t.Fatalf("look at a layout whose write lock another handle holds: %v", err)
	}
	if observed.state != durableLayoutWriterHeld {
		t.Errorf("state = %v, want writerHeld while another handle holds BEGIN IMMEDIATE", observed.state)
	}
	if err := held.Rollback(); err != nil {
		t.Fatalf("release the write lock: %v", err)
	}
	observed, err = look(t.Context())
	if err != nil {
		t.Fatalf("look again after the lock was released: %v", err)
	}
	if observed.state != durableLayoutSuperseded || observed.version != testutil.SupersededDurableSchemaVersion {
		t.Errorf("after the lock was released: state = %v version = %d, want superseded at %d", observed.state, observed.version, testutil.SupersededDurableSchemaVersion)
	}

	// A fresh file is usable at the first look.
	freshDB, err := dbconn.OpenSharedDBWithProfile(t.TempDir()+"/fresh.db", profile)
	if err != nil {
		t.Fatalf("open a fresh database: %v", err)
	}
	t.Cleanup(func() { _ = freshDB.Close() })
	observed, err = probeDurableLayout(freshDB, "fresh.db")(t.Context())
	if err != nil {
		t.Fatalf("look at a fresh database: %v", err)
	}
	if observed.state != durableLayoutUsable {
		t.Errorf("state = %v, want usable for a fresh database", observed.state)
	}
}
