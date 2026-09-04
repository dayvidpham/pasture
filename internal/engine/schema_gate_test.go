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
type scriptedLayout struct {
	script []durableLayoutObservation
	next   func(look int) durableLayoutObservation
	looks  int
}

func (s *scriptedLayout) probe(context.Context) (durableLayoutObservation, error) {
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
	look := &scriptedLayout{script: []durableLayoutObservation{belowFloor(1), writerHeld, belowFloor(20), belowFloor(41), usable}}
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
	look := &scriptedLayout{script: []durableLayoutObservation{belowFloor(41)}}
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
func TestAMigrationThatOutlivesTheBoundIsReportedAsUnfinishedAndNeverAsAnOlderBuild(t *testing.T) {
	t.Parallel()
	profile := timeouts.ProductionProfile()
	look := &scriptedLayout{
		script: []durableLayoutObservation{belowFloor(1)},
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
	look := &scriptedLayout{script: []durableLayoutObservation{belowFloor(3), belowFloor(4)}}
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
