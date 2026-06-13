package engine_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// newEngineWithTracker opens a unified tracker (audit + provenance on one file)
// and an engine that records activities through it. Both target the same
// pasture.db. Returns the engine and the db path for direct verification reads.
func newEngineWithTracker(t *testing.T) (*engine.Engine, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })

	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: "activities-v1",
		// Route BOTH forensic tiers through the one tracker: audit_events via
		// the audit methods, activities via the provenance methods.
		Trail:   tracker,
		Tracker: tracker,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}
	t.Cleanup(func() { e.Shutdown(5 * time.Second) })
	return e, dbPath
}

func countRows(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", q, err)
	}
	return n
}

// TestEngine_RecordsActivityPerTransition: with a Tracker configured, the engine
// records exactly one activity per successful transition, attributed to the
// stable engine agent, in lock-step with the audit rows.
func TestEngine_RecordsActivityPerTransition(t *testing.T) {
	e, dbPath := newEngineWithTracker(t)
	const epochId = "epoch-acts"

	final := runEpoch(t, e, epochId, fullEpochPlan())
	if final.CurrentPhase != protocol.PhaseComplete {
		t.Fatalf("final phase = %q, want complete", final.CurrentPhase)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// 12 transitions → 12 activities AND 12 keyed audit rows.
	if n := countRows(t, db, `SELECT COUNT(*) FROM activities`); n != 12 {
		t.Errorf("activities = %d, want 12 (one per transition)", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM audit_events WHERE dedup_key IS NOT NULL`); n != 12 {
		t.Errorf("keyed audit_events = %d, want 12", n)
	}

	// Every activity is namespaced to the engine ("pasture--<uuid>") and
	// attributed to exactly one agent (the stable engine agent).
	if n := countRows(t, db, `SELECT COUNT(DISTINCT agent_id) FROM activities`); n != 1 {
		t.Errorf("distinct activity agents = %d, want 1 (stable engine agent)", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM activities WHERE id LIKE 'pasture--%'`); n != 12 {
		t.Errorf("namespaced activity ids = %d, want 12", n)
	}
}

// TestEngine_ActivityAndAuditDistinctKeys: for the same transition, the activity
// id and the audit dedup_key are DIFFERENT (distinct id-spaces) because the two
// tiers pass distinct kinds to the one DedupKey encoder — an audit system event
// and a PROV-O activity are different entities and must not be implicitly joined
// by a shared id. Both remain deterministic (the activity id matches the encoder
// recomputed with the activity kind).
func TestEngine_ActivityAndAuditDistinctKeys(t *testing.T) {
	e, dbPath := newEngineWithTracker(t)
	const epochId = "epoch-distinct"

	runEpoch(t, e, epochId, []engine.AdvanceStep{
		{ToPhase: protocol.PhaseElicit, TriggeredBy: "epoch"},
	})

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var auditKey string
	if err := db.QueryRow(
		`SELECT dedup_key FROM audit_events WHERE dedup_key IS NOT NULL AND phase = 'elicit'`).Scan(&auditKey); err != nil {
		t.Fatalf("read audit dedup_key: %v", err)
	}
	var activityID string
	if err := db.QueryRow(`SELECT id FROM activities`).Scan(&activityID); err != nil {
		t.Fatalf("read activity id: %v", err)
	}

	// Distinct id-spaces: the activity id must NOT be the audit dedup_key's uuid.
	if strings.HasSuffix(activityID, auditKey) {
		t.Errorf("activity id %q carries the audit dedup_key uuid %q — the tiers must use distinct kinds", activityID, auditKey)
	}

	// Determinism + same-step / different-kind: find the step the audit key was
	// derived from, then prove the activity id is the encoder recomputed with the
	// engine's DISTINCT activity kind at that SAME step.
	var stepSeq string
	for _, s := range []string{"-1", "0", "1", "2"} {
		if protocol.DedupKey(epochId, string(protocol.PhaseElicit), string(protocol.EventPhaseTransition), s) == auditKey {
			stepSeq = s
			break
		}
	}
	if stepSeq == "" {
		t.Fatalf("could not recover the step for audit key %q", auditKey)
	}
	wantUUID := protocol.DedupKey(epochId, string(protocol.PhaseElicit), engine.ActivityKindPhaseTransition, stepSeq)
	if activityID != "pasture--"+wantUUID {
		t.Errorf("activity id = %q, want %q (deterministic from the distinct activity kind at the same step)", activityID, "pasture--"+wantUUID)
	}
}

// TestEngine_ActivitiesCrossEpochDistinct: two epochs at the same step produce
// distinct activity ids (epoch hashed into the key) — no cross-epoch false dedup
// on the activities tier.
func TestEngine_ActivitiesCrossEpochDistinct(t *testing.T) {
	e, dbPath := newEngineWithTracker(t)

	short := []engine.AdvanceStep{
		{ToPhase: protocol.PhaseElicit, TriggeredBy: "epoch"},
		{ToPhase: protocol.PhasePropose, TriggeredBy: "architect"},
	}
	runEpoch(t, e, "epoch-a", short)
	runEpoch(t, e, "epoch-b", short)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// 2 epochs × 2 transitions = 4 activities, all distinct ids.
	if n := countRows(t, db, `SELECT COUNT(*) FROM activities`); n != 4 {
		t.Errorf("activities = %d, want 4", n)
	}
	if n := countRows(t, db, `SELECT COUNT(DISTINCT id) FROM activities`); n != 4 {
		t.Errorf("distinct activity ids = %d, want 4 (no cross-epoch false dedup)", n)
	}
}

// TestEngine_NoTracker_NoActivities: without a Tracker, the engine records no
// activities (backward-compatible) while still recording audit rows.
func TestEngine_NoTracker_NoActivities(t *testing.T) {
	e := newEngine(t) // no Tracker configured
	const epochId = "epoch-no-acts"
	runEpoch(t, e, epochId, []engine.AdvanceStep{
		{ToPhase: protocol.PhaseElicit, TriggeredBy: "epoch"},
	})

	// audit_events recorded as before.
	rows, err := e.Trail().QueryEvents(context.Background(), epochId, nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("audit rows = %d, want 1", len(rows))
	}
	// No activities table writes (the engine without a Tracker doesn't create
	// or populate provenance tables).
	var hasActivities int
	_ = e.DB().QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='activities'`).Scan(&hasActivities)
	if hasActivities == 1 {
		if n := countRows(t, e.DB(), `SELECT COUNT(*) FROM activities`); n != 0 {
			t.Errorf("activities = %d, want 0 without a Tracker", n)
		}
	}
}
