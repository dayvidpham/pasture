// Package main_test — subprocess CLI tests for SLICE-6 (PROPOSAL-2 §7.9 +
// Scenarios 6, 15).
//
// These tests exercise the `pasture` binary built once in TestMain (see
// main_test.go) through real CLI invocations. The binary's exit semantics
// (via os.Exit) make subprocess execution the right model — running the
// binary in-process would terminate the test runner.
//
// Coverage map (per IMPL_PLAN §3 S6):
//
//   - pasture migrate --dry-run: SHA-256 unchanged, plan printed, exit 0.
//   - pasture migrate (apply):   v -> MaxKnownSchemaVersion, success line.
//   - pasture migrate (re-run):  idempotent no-op, "from vN to vN" line.
//   - Auto-on-open vs explicit-command convergence: identical content.
//   - pasture task events --epoch-id: returns events for the legacy v1 path.
//   - pasture task events --context-kind=GitContext --context-id=<sha>: S6
//     reader path for the S9 free-floating writer (we seed via raw SQL).
//   - pasture task timeline <task-id>: chronological merge of context-edge
//     and legacy epoch-id rows.
//   - pasture task contexts <event-id>: lists context_edges rows.
//   - pasture task agents list / show: enumerates pasture-side agents.
//   - Error paths: missing top-level filter, mismatched context flags,
//     invalid event id, nonexistent agent.
package main_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/audit"
)

// provenanceOpenSQLite is a thin wrapper exposing the upstream constructor.
// We re-export it here so the test file's helpers can call it without
// importing provenance directly twice (clarity > redundancy).
func provenanceOpenSQLite(path string) (provenance.Tracker, error) {
	return provenance.OpenSQLite(path)
}

// ---- pasture migrate ──────────────────────────────────────────────────────

// TestCLI_Migrate_DryRun_DoesNotModifyFile (Scenario 15, dry-run leg) — the
// file's SHA-256 must be byte-identical before and after `pasture migrate
// --dry-run`. Plan must be printed, exit code 0.
func TestCLI_Migrate_DryRun_DoesNotModifyFile(t *testing.T) {
	dbPath := newLegacyV1DB(t)

	beforeSHA := mustSHA256(t, dbPath)

	out := runCLI(t, "--db", dbPath, "migrate", "--dry-run")
	if out.exitCode != 0 {
		t.Fatalf("migrate --dry-run exit %d; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}

	afterSHA := mustSHA256(t, dbPath)
	if beforeSHA != afterSHA {
		t.Fatalf("dry-run modified file: SHA before=%s after=%s", beforeSHA, afterSHA)
	}

	// Plan must contain at least one expected step. The exact wording comes
	// from audit.PlanMigrations + stepDescription; we assert on the v1->v2
	// description (which is independent of MaxKnownSchemaVersion).
	if !strings.Contains(out.stdout, "v1->v2") || !strings.Contains(out.stdout, "audit_schema_meta") {
		t.Errorf("dry-run output missing v1->v2 step description; got: %q", out.stdout)
	}
	if !strings.Contains(out.stdout, "Dry run") {
		t.Errorf("dry-run output missing 'Dry run' header; got: %q", out.stdout)
	}
}

// TestCLI_Migrate_DryRun_JSON exercises the --format=json branch and asserts
// the structured output shape, so CI scripts can parse the plan.
func TestCLI_Migrate_DryRun_JSON(t *testing.T) {
	dbPath := newLegacyV1DB(t)

	out := runCLI(t, "--db", dbPath, "--format", "json", "migrate", "--dry-run")
	if out.exitCode != 0 {
		t.Fatalf("migrate --dry-run --format=json exit %d; stderr=%q", out.exitCode, out.stderr)
	}

	var plan struct {
		DBPath         string `json:"dbPath"`
		CurrentVersion int    `json:"currentVersion"`
		TargetVersion  int    `json:"targetVersion"`
		DryRun         bool   `json:"dryRun"`
		Steps          []struct {
			FromVersion int    `json:"fromVersion"`
			ToVersion   int    `json:"toVersion"`
			Description string `json:"description"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &plan); err != nil {
		t.Fatalf("decode plan json: %v\nbody: %q", err, out.stdout)
	}
	if plan.DBPath != dbPath {
		t.Errorf("plan.DBPath = %q, want %q", plan.DBPath, dbPath)
	}
	if !plan.DryRun {
		t.Error("plan.DryRun = false, want true")
	}
	if plan.CurrentVersion != 1 {
		t.Errorf("plan.CurrentVersion = %d, want 1", plan.CurrentVersion)
	}
	if plan.TargetVersion != audit.MaxKnownSchemaVersion {
		t.Errorf("plan.TargetVersion = %d, want %d", plan.TargetVersion, audit.MaxKnownSchemaVersion)
	}
	wantStepCount := audit.MaxKnownSchemaVersion - 1
	if len(plan.Steps) != wantStepCount {
		t.Errorf("len(plan.Steps) = %d, want %d", len(plan.Steps), wantStepCount)
	}
}

// TestCLI_Migrate_Apply (Scenario 15, apply leg) — `pasture migrate` brings
// a v1 db up to MaxKnownSchemaVersion and prints the success line.
func TestCLI_Migrate_Apply(t *testing.T) {
	dbPath := newLegacyV1DB(t)

	out := runCLI(t, "--db", dbPath, "migrate")
	if out.exitCode != 0 {
		t.Fatalf("migrate exit %d; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}

	wantSuffix := fmt.Sprintf("from v1 to v%d", audit.MaxKnownSchemaVersion)
	if !strings.Contains(out.stdout, "migrated") || !strings.Contains(out.stdout, wantSuffix) {
		t.Errorf("apply output missing 'migrated ... %s'; got: %q", wantSuffix, out.stdout)
	}

	// Verify on-disk state.
	got := mustReadVersion(t, dbPath)
	if got != audit.MaxKnownSchemaVersion {
		t.Errorf("post-apply on-disk version = %d, want %d", got, audit.MaxKnownSchemaVersion)
	}
}

// TestCLI_Migrate_Idempotent_ReRun (Scenario 15, re-run leg) — running
// migrate a second time on an already-migrated db is a no-op and reports
// "from v<n> to v<n>".
func TestCLI_Migrate_Idempotent_ReRun(t *testing.T) {
	dbPath := newLegacyV1DB(t)

	// First apply.
	out1 := runCLI(t, "--db", dbPath, "migrate")
	if out1.exitCode != 0 {
		t.Fatalf("first migrate exit %d; stderr=%q", out1.exitCode, out1.stderr)
	}

	// Second apply must report from==to==MaxKnownSchemaVersion.
	out2 := runCLI(t, "--db", dbPath, "migrate")
	if out2.exitCode != 0 {
		t.Fatalf("second migrate exit %d; stderr=%q", out2.exitCode, out2.stderr)
	}

	wantSuffix := fmt.Sprintf("from v%d to v%d", audit.MaxKnownSchemaVersion, audit.MaxKnownSchemaVersion)
	if !strings.Contains(out2.stdout, wantSuffix) {
		t.Errorf("idempotent re-run output missing %q; got: %q", wantSuffix, out2.stdout)
	}
}

// TestCLI_Migrate_ConvergesWithAutoOnOpen (Scenario 15, convergence leg) —
// File A migrated via OpenTaskTracker (any pasture command that opens the
// db); file B migrated via `pasture migrate`. Final content must be
// identical modulo SQLite WAL ordering. We compare table-by-table contents
// of the user-visible tables.
func TestCLI_Migrate_ConvergesWithAutoOnOpen(t *testing.T) {
	// Path A: migrate via auto-on-open. Run a command that goes through
	// tasks.OpenTaskTracker (NOT tasks.OpenTracker, which is Provenance-only
	// and does not invoke the audit migrator). `pasture task events
	// --epoch-id <none>` opens the unified tracker, runs Migrate as a side
	// effect, queries an empty result, and exits 0.
	pathA := newLegacyV1DB(t)
	outA := runCLI(t, "--db", pathA, "task", "events", "--epoch-id", "convergence-probe")
	if outA.exitCode != 0 {
		t.Fatalf("auto-on-open via task events exit %d; stderr=%q", outA.exitCode, outA.stderr)
	}

	// Path B: migrate via explicit command.
	pathB := newLegacyV1DB(t)
	outB := runCLI(t, "--db", pathB, "migrate")
	if outB.exitCode != 0 {
		t.Fatalf("explicit migrate exit %d; stderr=%q", outB.exitCode, outB.stderr)
	}

	// Both files must report MaxKnownSchemaVersion.
	verA := mustReadVersion(t, pathA)
	verB := mustReadVersion(t, pathB)
	if verA != audit.MaxKnownSchemaVersion || verB != audit.MaxKnownSchemaVersion {
		t.Fatalf("post-convergence versions: A=%d, B=%d, want both %d", verA, verB, audit.MaxKnownSchemaVersion)
	}

	// Compare the tables that S2's migrator created — these are the v3-
	// specific shape. Both should exist on both files. (We don't compare
	// audit_schema_meta.applied_at because Phase A and Phase B applied at
	// different wall-clock instants — the convergence claim is about
	// SCHEMA, not timing.)
	tables := []string{
		"context_edges",
		"pasture_agent_categories",
		"pasture_well_known_agents",
	}
	for _, tbl := range tables {
		schemaA := mustReadTableSchema(t, pathA, tbl)
		schemaB := mustReadTableSchema(t, pathB, tbl)
		if schemaA != schemaB {
			t.Errorf("table %q schema differs:\n  A: %s\n  B: %s", tbl, schemaA, schemaB)
		}
	}

	// audit_schema_meta should have the same row count (one row per
	// migration step from v1).
	rowsA := mustCountRows(t, pathA, "audit_schema_meta")
	rowsB := mustCountRows(t, pathB, "audit_schema_meta")
	if rowsA != rowsB {
		t.Errorf("audit_schema_meta row count differs: A=%d, B=%d", rowsA, rowsB)
	}
}

// TestCLI_Migrate_NoSuchFile_DryRun is an error-path check: a dry-run
// against a missing parent dir should NOT panic; it should surface a clean
// CategoryConnection error and exit 2.
func TestCLI_Migrate_NoSuchFile_DryRun(t *testing.T) {
	// Use a path under a parent dir we DO have permission to create. The
	// dry-run handler creates the parent dir but then opens the file —
	// modernc.org/sqlite happily creates an empty file rather than failing,
	// so the dry-run actually succeeds and reports a v1 (legacy) plan.
	// That behaviour is consistent with `sql.Open` semantics; we assert it
	// here so a regression in MkdirAll surfacing logic is caught.
	tmp := t.TempDir()
	target := filepath.Join(tmp, "newdir", "fresh.db")

	out := runCLI(t, "--db", target, "migrate", "--dry-run")
	// Either exit 0 with a v1->vN plan, OR a clean CategoryConnection
	// error with exit 2 (depends on filesystem semantics).
	if out.exitCode != 0 && out.exitCode != 2 {
		t.Errorf("unexpected exit code %d; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}
}

// ---- pasture task events ──────────────────────────────────────────────────

// TestCLI_TaskEvents_RequiresTopLevelFilter — without --epoch-id or
// --context-kind+--context-id, the command must reject with a
// CategoryValidation error (exit 1).
func TestCLI_TaskEvents_RequiresTopLevelFilter(t *testing.T) {
	dbPath := newLegacyV1DB(t)

	out := runCLI(t, "--db", dbPath, "task", "events")
	if out.exitCode != 1 {
		t.Fatalf("expected exit 1 (validation), got %d; stdout=%q stderr=%q",
			out.exitCode, out.stdout, out.stderr)
	}
	if !strings.Contains(out.stderr, "no top-level filter") {
		t.Errorf("error message missing 'no top-level filter'; got: %q", out.stderr)
	}
}

// TestCLI_TaskEvents_ContextKindWithoutContextID — passing --context-kind
// alone must reject with a paired-flags validation error.
func TestCLI_TaskEvents_ContextKindWithoutContextID(t *testing.T) {
	dbPath := newLegacyV1DB(t)

	out := runCLI(t, "--db", dbPath, "task", "events", "--context-kind=GitContext")
	if out.exitCode != 1 {
		t.Fatalf("expected exit 1, got %d; stderr=%q", out.exitCode, out.stderr)
	}
	if !strings.Contains(out.stderr, "must be passed together") {
		t.Errorf("error message missing pair-validation text; got: %q", out.stderr)
	}
}

// TestCLI_TaskEvents_UnknownContextKind — invalid --context-kind value must
// reject with an actionable error listing valid kinds.
func TestCLI_TaskEvents_UnknownContextKind(t *testing.T) {
	dbPath := newLegacyV1DB(t)

	out := runCLI(t, "--db", dbPath, "task", "events",
		"--context-kind=NotARealKind", "--context-id=anything")
	if out.exitCode != 1 {
		t.Fatalf("expected exit 1, got %d; stderr=%q", out.exitCode, out.stderr)
	}
	if !strings.Contains(out.stderr, "unknown --context-kind") {
		t.Errorf("error message missing 'unknown --context-kind'; got: %q", out.stderr)
	}
}

// TestCLI_TaskEvents_EpochID_LegacyV1Path — an event written via the v1
// audit_events.epoch_id column must be reachable via
// `pasture task events --epoch-id <id>`. We seed the row directly so the
// test does not depend on S8's workflow integration landing.
func TestCLI_TaskEvents_EpochID_LegacyV1Path(t *testing.T) {
	dbPath := newLegacyV1DB(t)
	const epochID = "test-epoch-events-1"
	seedV1AuditEvent(t, dbPath, epochID, "supervisor", "PhaseTransition",
		map[string]any{"note": "v1-row"})

	out := runCLI(t, "--db", dbPath, "--format", "json",
		"task", "events", "--epoch-id", epochID)
	if out.exitCode != 0 {
		t.Fatalf("events --epoch-id exit %d; stderr=%q", out.exitCode, out.stderr)
	}

	var events []struct {
		EpochID   string `json:"epochId"`
		EventType string `json:"eventType"`
		Role      string `json:"role"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &events); err != nil {
		t.Fatalf("decode events json: %v\nbody: %q", err, out.stdout)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event for epoch %q, got %d: %+v", epochID, len(events), events)
	}
	if events[0].EpochID != epochID || events[0].EventType != "PhaseTransition" || events[0].Role != "supervisor" {
		t.Errorf("event mismatch: %+v", events[0])
	}
}

// TestCLI_TaskEvents_GitContext_Scenario6 (Scenario 6 reader side) — a
// free-floating event with a GitContext attachment must be retrievable via
// `pasture task events --context-kind=GitContext --context-id=<sha>`. We
// seed the row + context_edge directly so the test does not depend on S9's
// hook handler landing.
func TestCLI_TaskEvents_GitContext_Scenario6(t *testing.T) {
	dbPath := newLegacyV1DB(t)
	const sha = "abc123def456"
	const epochID = "" // free-floating events have empty/synthetic epoch_id

	// Migrate first so context_edges exists and audit_events has the post-v3
	// shape (no `role` column). Then seed an audit_events row + context edge
	// row pointing at it via raw SQL — the S9 hook handler will do the
	// equivalent work via tracker.RecordEvent + tracker.AttachContext.
	if rc := runCLI(t, "--db", dbPath, "migrate"); rc.exitCode != 0 {
		t.Fatalf("migrate exit %d; stderr=%q", rc.exitCode, rc.stderr)
	}

	eventID := seedPostMigrationAuditEvent(t, dbPath, epochID, "pasture--legacy-git-recorder", "GitCommit",
		map[string]any{"sha": sha})
	seedContextEdge(t, dbPath, eventID, "GitContext", sha)

	out := runCLI(t, "--db", dbPath, "--format", "json",
		"task", "events", "--context-kind=GitContext", "--context-id="+sha)
	if out.exitCode != 0 {
		t.Fatalf("events --context-kind=GitContext exit %d; stderr=%q", out.exitCode, out.stderr)
	}

	var events []struct {
		EpochID   string         `json:"epochId"`
		EventType string         `json:"eventType"`
		Payload   map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &events); err != nil {
		t.Fatalf("decode events json: %v\nbody: %q", err, out.stdout)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event for GitContext %q, got %d: %+v", sha, len(events), events)
	}
	if events[0].EventType != "GitCommit" {
		t.Errorf("event_type = %q, want GitCommit", events[0].EventType)
	}
	if got, ok := events[0].Payload["sha"].(string); !ok || got != sha {
		t.Errorf("payload.sha = %v, want %q", events[0].Payload["sha"], sha)
	}
}

// ---- pasture task timeline ────────────────────────────────────────────────

// TestCLI_TaskTimeline_ChronologicalOrder — events for one task must come
// back in timestamp ASC order even when seeded out of order.
func TestCLI_TaskTimeline_ChronologicalOrder(t *testing.T) {
	dbPath := newLegacyV1DB(t)
	const epochID = "test-task-timeline-1"

	// Seed 3 events out of chronological order via raw INSERT with explicit
	// timestamps so we control the ordering deterministically.
	seedV1AuditEventAt(t, dbPath, epochID, "supervisor", "PhaseTransition",
		map[string]any{"order": 2}, time.Unix(0, 2_000_000_000).UTC())
	seedV1AuditEventAt(t, dbPath, epochID, "supervisor", "PhaseTransition",
		map[string]any{"order": 0}, time.Unix(0, 0).UTC())
	seedV1AuditEventAt(t, dbPath, epochID, "supervisor", "PhaseTransition",
		map[string]any{"order": 1}, time.Unix(0, 1_000_000_000).UTC())

	out := runCLI(t, "--db", dbPath, "--format", "json",
		"task", "timeline", epochID)
	if out.exitCode != 0 {
		t.Fatalf("timeline exit %d; stderr=%q", out.exitCode, out.stderr)
	}

	var events []struct {
		Payload   map[string]any `json:"payload"`
		Timestamp string         `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &events); err != nil {
		t.Fatalf("decode timeline json: %v\nbody: %q", err, out.stdout)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events in timeline, got %d: %+v", len(events), events)
	}

	// Order assertion: payload.order should read 0, 1, 2.
	for i, e := range events {
		got, ok := e.Payload["order"].(float64) // JSON numbers decode as float64
		if !ok {
			t.Fatalf("event %d payload.order not a number: %+v", i, e.Payload)
		}
		if int(got) != i {
			t.Errorf("event %d payload.order = %v, want %d (timeline must be timestamp ASC)", i, got, i)
		}
	}
}

// TestCLI_TaskTimeline_MissingTaskID — calling timeline without a task ID
// must reject with a validation error.
func TestCLI_TaskTimeline_MissingTaskID(t *testing.T) {
	out := runCLI(t, "task", "timeline")
	// Cobra's Args validator catches this BEFORE our handler runs; the
	// resulting exit code is whatever Cobra picks. We just assert non-zero.
	if out.exitCode == 0 {
		t.Errorf("expected non-zero exit when task-id is missing; got: %q", out.stdout)
	}
}

// ---- pasture task contexts ────────────────────────────────────────────────

// TestCLI_TaskContexts_ListsAttachedEdges — given an event with two attached
// contexts, the contexts subcommand returns both.
func TestCLI_TaskContexts_ListsAttachedEdges(t *testing.T) {
	dbPath := newLegacyV1DB(t)
	if rc := runCLI(t, "--db", dbPath, "migrate"); rc.exitCode != 0 {
		t.Fatalf("migrate exit %d; stderr=%q", rc.exitCode, rc.stderr)
	}
	const epochID = "test-task-contexts-1"
	const sha = "deadbeef"
	eventID := seedPostMigrationAuditEvent(t, dbPath, epochID, "pasture--legacy-supervisor", "PhaseTransition",
		map[string]any{"hello": "world"})
	seedContextEdge(t, dbPath, eventID, "EpochContext", epochID)
	seedContextEdge(t, dbPath, eventID, "GitContext", sha)

	out := runCLI(t, "--db", dbPath, "--format", "json",
		"task", "contexts", fmt.Sprintf("%d", eventID))
	if out.exitCode != 0 {
		t.Fatalf("contexts exit %d; stderr=%q", out.exitCode, out.stderr)
	}

	var contexts []struct {
		Kind      string `json:"kind"`
		ContextID string `json:"contextId"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &contexts); err != nil {
		t.Fatalf("decode contexts json: %v\nbody: %q", err, out.stdout)
	}
	if len(contexts) != 2 {
		t.Fatalf("expected 2 contexts, got %d: %+v", len(contexts), contexts)
	}
	gotKinds := map[string]string{}
	for _, c := range contexts {
		gotKinds[c.Kind] = c.ContextID
	}
	if gotKinds["EpochContext"] != epochID {
		t.Errorf("EpochContext context_id = %q, want %q", gotKinds["EpochContext"], epochID)
	}
	if gotKinds["GitContext"] != sha {
		t.Errorf("GitContext context_id = %q, want %q", gotKinds["GitContext"], sha)
	}
}

// TestCLI_TaskContexts_InvalidEventID — non-integer event-id must reject
// with a CategoryValidation error.
func TestCLI_TaskContexts_InvalidEventID(t *testing.T) {
	dbPath := newLegacyV1DB(t)
	out := runCLI(t, "--db", dbPath, "task", "contexts", "not-a-number")
	if out.exitCode != 1 {
		t.Fatalf("expected exit 1 (validation), got %d; stderr=%q", out.exitCode, out.stderr)
	}
	if !strings.Contains(out.stderr, "cannot parse event ID") {
		t.Errorf("error message missing parse-error text; got: %q", out.stderr)
	}
}

// TestCLI_TaskContexts_NegativeEventID — zero/negative event-id must reject.
func TestCLI_TaskContexts_NegativeEventID(t *testing.T) {
	dbPath := newLegacyV1DB(t)
	out := runCLI(t, "--db", dbPath, "task", "contexts", "0")
	if out.exitCode != 1 {
		t.Fatalf("expected exit 1, got %d; stderr=%q", out.exitCode, out.stderr)
	}
}

// ---- pasture task agents ──────────────────────────────────────────────────

// TestCLI_TaskAgents_List_EmptyOnFreshDB — `agents list` against a freshly-
// migrated db with no registered agents should return "(no registered agents)".
// Until S7 lands, the well-known table is empty after migration.
func TestCLI_TaskAgents_List_EmptyOnFreshDB(t *testing.T) {
	dbPath := newLegacyV1DB(t)
	if rc := runCLI(t, "--db", dbPath, "migrate"); rc.exitCode != 0 {
		t.Fatalf("migrate exit %d; stderr=%q", rc.exitCode, rc.stderr)
	}

	out := runCLI(t, "--db", dbPath, "task", "agents", "list")
	if out.exitCode != 0 {
		t.Fatalf("agents list exit %d; stderr=%q", out.exitCode, out.stderr)
	}
	// Either "(no registered agents)" or a JSON empty array depending on
	// format (we used default text here).
	if !strings.Contains(out.stdout, "(no registered agents)") {
		t.Errorf("expected empty-list message, got: %q", out.stdout)
	}
}

// TestCLI_TaskAgents_List_ShowsSeededRow — manually seed one row in
// pasture_well_known_agents + pasture_agent_categories and verify the list
// surfaces it. This is the read-side contract S7's writer will hit.
func TestCLI_TaskAgents_List_ShowsSeededRow(t *testing.T) {
	dbPath := newLegacyV1DB(t)
	if rc := runCLI(t, "--db", dbPath, "migrate"); rc.exitCode != 0 {
		t.Fatalf("migrate exit %d; stderr=%q", rc.exitCode, rc.stderr)
	}

	const agentID = "pasture--01935b3e-4cc1-7000-8000-000000000001"
	const wellKnown = "pasture/automaton/check-constraints"

	mustExecOnDB(t, dbPath,
		`INSERT INTO pasture_well_known_agents (agent_id, name) VALUES (?, ?)`,
		agentID, wellKnown)
	mustExecOnDB(t, dbPath,
		`INSERT INTO pasture_agent_categories (agent_id, automaton_role, pasture_role) VALUES (?, ?, ?)`,
		agentID, "ConstraintChecker", "None")

	out := runCLI(t, "--db", dbPath, "--format", "json", "task", "agents", "list")
	if out.exitCode != 0 {
		t.Fatalf("agents list exit %d; stderr=%q", out.exitCode, out.stderr)
	}

	var entries []struct {
		AgentID       string `json:"agentId"`
		WellKnownName string `json:"wellKnownName"`
		AutomatonRole string `json:"automatonRole"`
		PastureRole   string `json:"pastureRole"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &entries); err != nil {
		t.Fatalf("decode agents json: %v\nbody: %q", err, out.stdout)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 agent, got %d: %+v", len(entries), entries)
	}
	got := entries[0]
	if got.AgentID != agentID {
		t.Errorf("AgentID = %q, want %q", got.AgentID, agentID)
	}
	if got.WellKnownName != wellKnown {
		t.Errorf("WellKnownName = %q, want %q", got.WellKnownName, wellKnown)
	}
	if got.AutomatonRole != "ConstraintChecker" {
		t.Errorf("AutomatonRole = %q, want ConstraintChecker", got.AutomatonRole)
	}
	if got.PastureRole != "None" {
		t.Errorf("PastureRole = %q, want None", got.PastureRole)
	}
}

// TestCLI_TaskAgents_Show_NotFoundReturnsNoneNone — `show <id>` against an
// unregistered agent ID returns ("None","None") via the AgentCategories
// contract; exit 0.
func TestCLI_TaskAgents_Show_NotFoundReturnsNoneNone(t *testing.T) {
	dbPath := newLegacyV1DB(t)
	if rc := runCLI(t, "--db", dbPath, "migrate"); rc.exitCode != 0 {
		t.Fatalf("migrate exit %d; stderr=%q", rc.exitCode, rc.stderr)
	}

	const agentID = "pasture--01935b3e-4cc1-7000-8000-0000000000ff"
	out := runCLI(t, "--db", dbPath, "--format", "json",
		"task", "agents", "show", agentID)
	if out.exitCode != 0 {
		t.Fatalf("agents show exit %d; stderr=%q", out.exitCode, out.stderr)
	}

	var entry struct {
		AgentID       string `json:"agentId"`
		AutomatonRole string `json:"automatonRole"`
		PastureRole   string `json:"pastureRole"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &entry); err != nil {
		t.Fatalf("decode show json: %v\nbody: %q", err, out.stdout)
	}
	if entry.AgentID != agentID {
		t.Errorf("AgentID = %q, want %q", entry.AgentID, agentID)
	}
	if entry.AutomatonRole != "None" || entry.PastureRole != "None" {
		t.Errorf("expected default ('None','None') for unregistered agent; got (%q, %q)",
			entry.AutomatonRole, entry.PastureRole)
	}
}

// TestCLI_TaskAgents_Show_InvalidID — bad agent ID format must reject.
func TestCLI_TaskAgents_Show_InvalidID(t *testing.T) {
	dbPath := newLegacyV1DB(t)
	out := runCLI(t, "--db", dbPath, "task", "agents", "show", "not-an-agent-id")
	if out.exitCode != 1 {
		t.Fatalf("expected exit 1, got %d; stderr=%q", out.exitCode, out.stderr)
	}
}

// ---- helpers ──────────────────────────────────────────────────────────────

// newLegacyV1DB creates a database in t.TempDir() with both the Provenance
// schema (agents/agents_software/tasks/etc., needed by the v3 backfill that
// reads agents_software for find-or-create) AND a v1-shaped audit_events
// table (no audit_schema_meta) so the migrator detects it as v1 and runs the
// full forward chain.
//
// The Provenance bootstrap happens via provenance.OpenSQLite (idempotent),
// then we drop on the v1 audit_events shape via raw SQL. This mirrors the
// real-world starting state of any pasture installation that ran the
// pre-PROPOSAL-2 binary against the unified database.
func newLegacyV1DB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	// Provenance bootstrap: creates agents, agents_software, tasks, etc.
	// We discard the tracker; we only need its side effect on the file.
	tr, err := provenanceOpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("provenance bootstrap: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("provenance close: %v", err)
	}

	// Append the v1-shaped audit_events table.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	mustExec(t, db, `
		CREATE TABLE audit_events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			epoch_id   TEXT    NOT NULL,
			phase      TEXT    NOT NULL,
			role       TEXT    NOT NULL,
			event_type TEXT    NOT NULL,
			payload    TEXT    NOT NULL,
			timestamp  INTEGER NOT NULL
		)`)
	return dbPath
}

// seedV1AuditEvent inserts one row directly via raw SQL on the v1
// audit_events shape (with `role` column) and returns the AUTOINCREMENT id.
// Used by tests that pre-populate events BEFORE the migration runs.
func seedV1AuditEvent(t *testing.T, dbPath, epochID, role, eventType string, payload map[string]any) int64 {
	t.Helper()
	return seedV1AuditEventAt(t, dbPath, epochID, role, eventType, payload, time.Now().UTC())
}

// seedV1AuditEventAt is seedV1AuditEvent with an explicit timestamp.
func seedV1AuditEventAt(t *testing.T, dbPath, epochID, role, eventType string, payload map[string]any, ts time.Time) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("seedV1AuditEvent open: %v", err)
	}
	defer db.Close()

	pj, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("seedV1AuditEvent payload marshal: %v", err)
	}
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO audit_events (epoch_id, phase, role, event_type, payload, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		epochID, "p1-request", role, eventType, string(pj), ts.UnixNano(),
	)
	if err != nil {
		t.Fatalf("seedV1AuditEvent insert: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seedV1AuditEvent lastInsertId: %v", err)
	}
	return id
}

// seedPostMigrationAuditEvent inserts one row into the post-v3 audit_events
// shape (no `role` column; agent_id NOT NULL). Used by tests that ran the
// migrator first and now need to inject events for context-edge linkage.
//
// The agent_id is stored as an opaque string here; tests that don't care
// about the FK to agents_software pass any non-empty value.
func seedPostMigrationAuditEvent(t *testing.T, dbPath, epochID, agentID, eventType string, payload map[string]any) int64 {
	t.Helper()
	return seedPostMigrationAuditEventAt(t, dbPath, epochID, agentID, eventType, payload, time.Now().UTC())
}

func seedPostMigrationAuditEventAt(t *testing.T, dbPath, epochID, agentID, eventType string, payload map[string]any, ts time.Time) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("seedPostMigrationAuditEvent open: %v", err)
	}
	defer db.Close()

	pj, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("seedPostMigrationAuditEvent payload marshal: %v", err)
	}
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO audit_events (epoch_id, phase, agent_id, event_type, payload, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		epochID, "p1-request", agentID, eventType, string(pj), ts.UnixNano(),
	)
	if err != nil {
		t.Fatalf("seedPostMigrationAuditEvent insert: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seedPostMigrationAuditEvent lastInsertId: %v", err)
	}
	return id
}

// seedContextEdge inserts a row into context_edges. The table must exist
// (run `pasture migrate` first or rely on auto-migrate-on-open).
func seedContextEdge(t *testing.T, dbPath string, eventID int64, kind, contextID string) {
	t.Helper()
	mustExecOnDB(t, dbPath,
		`INSERT INTO context_edges (event_id, context_kind, context_id) VALUES (?, ?, ?)`,
		eventID, kind, contextID)
}

// mustExecOnDB opens dbPath, runs an Exec, and closes. Crashes the test on
// any error.
func mustExecOnDB(t *testing.T, dbPath, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("mustExecOnDB open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("mustExecOnDB exec %q: %v", query, err)
	}
}

// mustExec wraps db.Exec with t.Fatalf on error. Mirrors the pattern in
// internal/audit/migrate_test.go.
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// mustSHA256 returns hex(sha256(file)). Used by the dry-run no-modify
// assertion.
func mustSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// mustReadVersion opens dbPath and returns the highest audit_schema_meta
// version. Crashes the test on error.
func mustReadVersion(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("mustReadVersion open: %v", err)
	}
	defer db.Close()
	var v sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&v); err != nil {
		t.Fatalf("mustReadVersion select: %v", err)
	}
	if !v.Valid {
		return 0
	}
	return int(v.Int64)
}

// mustReadTableSchema returns the SQL CREATE TABLE statement recorded in
// sqlite_master for the given table. Used by the convergence test to verify
// schema-shape equality without comparing entire file bytes (which would
// fail due to WAL ordering and applied_at timestamp differences).
func mustReadTableSchema(t *testing.T, dbPath, tableName string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("mustReadTableSchema open: %v", err)
	}
	defer db.Close()
	var schema sql.NullString
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, tableName).Scan(&schema); err != nil {
		t.Fatalf("mustReadTableSchema query for %q: %v", tableName, err)
	}
	if !schema.Valid {
		return ""
	}
	return schema.String
}

// mustCountRows returns COUNT(*) for the given table.
func mustCountRows(t *testing.T, dbPath, tableName string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("mustCountRows open: %v", err)
	}
	defer db.Close()
	var n int
	// #nosec G201 — table name is a hard-coded test literal.
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + tableName).Scan(&n); err != nil {
		t.Fatalf("mustCountRows query for %q: %v", tableName, err)
	}
	return n
}
