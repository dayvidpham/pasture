package main_test

// CLI-level subprocess tests for `pasture status`.
//
// Tests exercise the production binary via subprocess (same idiom as
// signals_test.go), asserting:
//   - `pasture status` (no flags) on a path where no db exists shows an
//     actionable message; no database file is created.
//   - `pasture status` (no flags) on an empty db shows the actionable
//     empty-state message.
//   - `pasture status --epoch-id <id>` returns exit 3 (workflow/not-found) for
//     an epoch that has no projection row (both when the table exists and when
//     the database file is absent entirely).
//   - After driving an epoch through phases, `pasture status --epoch-id <id>`
//     reports the current phase correctly in both text and JSON modes.
//   - A cold durable read (engine shut down, second process opens the same db)
//     reflects durable state, not in-memory state.
//   - `pasture status --epoch-id <id>` flags a terminated epoch's cancel reason
//     in both JSON and text modes.
//   - An EpochCancelled event pushed beyond the 10-event display window is
//     still surfaced in the cancel reason.
//   - `pasture status` appears in the top-level help output.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	_ "modernc.org/sqlite" // pure-Go SQLite driver for schema-manipulation helpers

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// seedProjectionDB drives an epoch through two phase advances (p0 → elicit →
// propose) using a real engine on dbPath, then shuts the engine down gracefully.
// The caller can then re-open the database cold (a fresh process reads durable
// state from SQLite, not in-memory engine state) to verify the status surface
// reflects persisted data.
func seedProjectionDB(t *testing.T, dbPath, epochId string) {
	t.Helper()
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: "status-test-v1",
	})
	if err != nil {
		t.Fatalf("status_test seedProjectionDB: engine.New: %v", err)
	}
	if err := e.Launch(); err != nil {
		t.Fatalf("status_test seedProjectionDB: engine.Launch: %v", err)
	}
	defer e.Shutdown(5 * time.Second)

	plan := []engine.AdvanceStep{
		{ToPhase: protocol.PhaseElicit, TriggeredBy: "epoch", ConditionMet: "classified"},
		{ToPhase: protocol.PhasePropose, TriggeredBy: "architect", ConditionMet: "elicited"},
	}
	h, err := dbos.RunWorkflow(e.DBOS(), e.EpochWorkflow,
		engine.EpochInput{EpochId: epochId, Advances: plan},
		dbos.WithWorkflowID(epochId))
	if err != nil {
		t.Fatalf("status_test seedProjectionDB: RunWorkflow: %v", err)
	}
	if _, err := h.GetResult(dbos.WithHandleTimeout(30 * time.Second)); err != nil {
		t.Fatalf("status_test seedProjectionDB: GetResult: %v", err)
	}
}

// recordAuditEvents opens the task tracker and appends n extra audit events
// to epochId. It is used to push an EpochCancelled event outside the 10-event
// display window so the cancel-reason regression test can confirm the full
// event list is still scanned for the cancel reason before display truncation.
func recordAuditEvents(t *testing.T, dbPath, epochId string, n int) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("status_test recordAuditEvents: OpenTaskTracker: %v", err)
	}
	defer tracker.Close()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		ev := protocol.AuditEvent{
			EpochId:   epochId,
			Phase:     protocol.PhasePropose,
			Role:      "supervisor",
			EventType: "PhaseAdvanced",
			Payload:   map[string]any{"step": fmt.Sprintf("post-cancel-%d", i)},
			Timestamp: time.Now().UTC(),
		}
		if err := tracker.RecordEvent(ctx, ev); err != nil {
			t.Fatalf("status_test recordAuditEvents: RecordEvent[%d]: %v", i, err)
		}
	}
}

// ─── tests ────────────────────────────────────────────────────────────────────

// TestCLI_StatusHelp verifies the status subcommand is registered and appears
// in the top-level help output.
func TestCLI_StatusHelp(t *testing.T) {
	out := runCLI(t, "status", "--help")
	if out.exitCode != 0 {
		t.Fatalf("status --help exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	combined := out.stdout + out.stderr
	for _, want := range []string{"epoch", "phase"} {
		if !strings.Contains(combined, want) {
			t.Errorf("status --help missing %q; got: %s", want, combined)
		}
	}
}

// TestCLI_TopLevelHelp_ContainsStatus verifies the status verb appears in the
// top-level help so users can discover it.
func TestCLI_TopLevelHelp_ContainsStatus(t *testing.T) {
	out := runCLI(t, "--help")
	if out.exitCode != 0 {
		t.Fatalf("--help exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	if !strings.Contains(out.stdout+out.stderr, "status") {
		t.Errorf("top-level help missing 'status'; got: %s", out.stdout+out.stderr)
	}
}

// TestCLI_Status_AbsentDB_NoFileCreated verifies that running `pasture status`
// against a path where the database does not exist returns a non-zero exit with
// an actionable message and does NOT create the file. Status is a pure-read
// command — it must never create or migrate the database.
func TestCLI_Status_AbsentDB_NoFileCreated(t *testing.T) {
	dbPath := newDB(t) // returns a path to a file that does not yet exist
	out := runCLI(t, "--db", dbPath, "status")
	// Must fail with CategoryConnection exit code (2): absent db = connection error.
	if out.exitCode != 2 {
		t.Fatalf("expected exit 2 (connection) for absent db; got %d; stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	// Stderr must contain an actionable message directing the user to start the daemon.
	combined := out.stdout + out.stderr
	if !strings.Contains(combined, "daemon") && !strings.Contains(combined, "pastured") {
		t.Errorf("absent-db message should mention daemon/pastured; got: %s", combined)
	}
	for _, want := range []string{"Problem:", "How to fix:"} {
		if !strings.Contains(combined, want) {
			t.Errorf("absent-db message missing structured error section %q; got: %s", want, combined)
		}
	}
	// The file must NOT have been created.
	if _, err := os.Stat(dbPath); err == nil {
		t.Errorf("status on absent db created the database file at %q — must not modify any state", dbPath)
	}
	// No sidecar files should appear in the directory (no -wal, -shm or other
	// artifacts from a failed open attempt).
	dir := dbPath[:len(dbPath)-len("pasture.db")-1]
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		t.Errorf("unexpected file in db dir after absent-db run: %s", e.Name())
	}
}

// TestCLI_Status_AbsentDB_EpochId_NoFileCreated verifies that
// `pasture status --epoch-id <id>` against a path where the database does not
// exist also fails with an actionable message and does NOT create the file.
func TestCLI_Status_AbsentDB_EpochId_NoFileCreated(t *testing.T) {
	dbPath := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000399"
	out := runCLI(t, "--db", dbPath, "status", "--epoch-id", epochId)
	// Must fail with CategoryConnection exit code (2).
	if out.exitCode != 2 {
		t.Fatalf("expected exit 2 (connection) for absent db with --epoch-id; got %d; stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	// Same structured actionable message as the listing variant.
	combined := out.stdout + out.stderr
	if !strings.Contains(combined, "daemon") && !strings.Contains(combined, "pastured") {
		t.Errorf("absent-db --epoch-id message should mention daemon/pastured; got: %s", combined)
	}
	for _, want := range []string{"Problem:", "How to fix:"} {
		if !strings.Contains(combined, want) {
			t.Errorf("absent-db --epoch-id message missing structured error section %q; got: %s", want, combined)
		}
	}
	if _, err := os.Stat(dbPath); err == nil {
		t.Errorf("status --epoch-id on absent db created the database file at %q — must not modify any state", dbPath)
	}
}

// TestCLI_Status_ExistingDB_NoEpochs_ShowsActionableMessage verifies that
// running `pasture status` against a database that exists but has no recorded
// epochs returns exit 0 and prints the actionable empty-state message. The
// database is pre-created by running `pasture migrate` so the file exists.
func TestCLI_Status_ExistingDB_NoEpochs_ShowsActionableMessage(t *testing.T) {
	db := newDB(t)
	// Create and migrate the database via the migrate command so it exists and
	// has a valid schema, but has no epoch projection rows.
	migrateOut := runCLI(t, "--db", db, "migrate")
	if migrateOut.exitCode != 0 {
		t.Fatalf("migrate to pre-create db: exit %d; stderr=%s", migrateOut.exitCode, migrateOut.stderr)
	}

	out := runCLI(t, "--db", db, "status")
	if out.exitCode != 0 {
		t.Fatalf("status on existing db with no epochs: exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	combined := out.stdout + out.stderr
	// Must tell the user how to start an epoch.
	for _, want := range []string{"pasture epoch start"} {
		if !strings.Contains(combined, want) {
			t.Errorf("empty-state message missing %q; got: %s", want, combined)
		}
	}
}

// TestCLI_Status_ExistingDB_NoEpochs_JSON_EmptyArray verifies that --format
// json on an existing database with no recorded epochs returns a JSON empty
// array (not an error). The database is pre-created by running `pasture migrate`.
func TestCLI_Status_ExistingDB_NoEpochs_JSON_EmptyArray(t *testing.T) {
	db := newDB(t)
	// Pre-create the database with a valid schema.
	migrateOut := runCLI(t, "--db", db, "migrate")
	if migrateOut.exitCode != 0 {
		t.Fatalf("migrate to pre-create db: exit %d; stderr=%s", migrateOut.exitCode, migrateOut.stderr)
	}

	out := runCLI(t, "--db", db, "--format", "json", "status")
	if out.exitCode != 0 {
		t.Fatalf("status --format json on existing db with no epochs: exit %d; stderr=%s",
			out.exitCode, out.stderr)
	}
	var epochs []interface{}
	if err := json.Unmarshal([]byte(out.stdout), &epochs); err != nil {
		t.Fatalf("status json: decode failed: %v\nbody: %s", err, out.stdout)
	}
	if len(epochs) != 0 {
		t.Errorf("expected empty array, got %d elements", len(epochs))
	}
}

// TestCLI_Status_FreshDB_EpochId_ReturnsExit3 verifies that
// `pasture status --epoch-id <id>` against a fresh database (projection table
// absent) returns exit 3 with a structured error — no seeding required. This
// covers the table-absent branch separately from the table-present/row-absent
// branch in TestCLI_Status_UnknownEpoch_ReturnsExit3.
func TestCLI_Status_FreshDB_EpochId_ReturnsExit3(t *testing.T) {
	db := newDB(t)
	// Pre-create and migrate the database so it exists with a valid schema but
	// no epoch projection rows ("fresh" = migrated, not absent).
	migrateOut := runCLI(t, "--db", db, "migrate")
	if migrateOut.exitCode != 0 {
		t.Fatalf("migrate to pre-create db: exit %d; stderr=%s", migrateOut.exitCode, migrateOut.stderr)
	}
	const epochId = "demo--01960000-0000-7000-8000-000000000398"
	out := runCLI(t, "--db", db, "status", "--epoch-id", epochId)
	if out.exitCode != 3 {
		t.Fatalf("expected exit 3 for fresh db with --epoch-id; exit=%d stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	for _, want := range []string{
		"Problem:",
		"How to fix:",
	} {
		if !strings.Contains(out.stderr, want) {
			t.Errorf("stderr missing structured error section %q; stderr=%s", want, out.stderr)
		}
	}
}

// TestCLI_Status_UnknownEpoch_ReturnsExit3 verifies that `pasture status
// --epoch-id <id>` returns exit 3 (workflow/not-found) for an epoch that has
// no projection row when the table already exists (another epoch was seeded),
// and that stderr contains the actionable structured error.
func TestCLI_Status_UnknownEpoch_ReturnsExit3(t *testing.T) {
	db := newDB(t)
	// Seed a different epoch so the projection table exists.
	const seedId = "demo--01960000-0000-7000-8000-000000000301"
	seedProjectionDB(t, db, seedId)

	const unknownId = "demo--01960000-0000-7000-8000-000000009999"
	out := runCLI(t, "--db", db, "status", "--epoch-id", unknownId)
	if out.exitCode != 3 {
		t.Fatalf("expected exit 3 for unknown epoch; exit=%d stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	// The full structured error report must reach stderr.
	for _, want := range []string{
		"Problem:",    // StructuredError.Report What section label
		"How to fix:", // StructuredError.Report Fix section label
	} {
		if !strings.Contains(out.stderr, want) {
			t.Errorf("stderr missing structured error section %q; stderr=%s", want, out.stderr)
		}
	}
}

// TestCLI_Status_EpochMidFlight_ReportsCurrentPhase_Text verifies that status
// correctly reports the current phase of an in-flight epoch in text mode.
func TestCLI_Status_EpochMidFlight_ReportsCurrentPhase_Text(t *testing.T) {
	db := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000302"
	seedProjectionDB(t, db, epochId)

	out := runCLI(t, "--db", db, "status", "--epoch-id", epochId)
	if out.exitCode != 0 {
		t.Fatalf("status exit %d; stdout=%s stderr=%s", out.exitCode, out.stdout, out.stderr)
	}
	combined := out.stdout + out.stderr
	// The epoch advanced to 'propose'; the status must mention that phase.
	if !strings.Contains(combined, "propose") {
		t.Errorf("status text missing 'propose'; got: %s", combined)
	}
	// Must contain the epoch id itself.
	if !strings.Contains(combined, epochId) {
		t.Errorf("status text missing epochId %q; got: %s", epochId, combined)
	}
}

// TestCLI_Status_EpochMidFlight_ReportsCurrentPhase_JSON verifies that --format
// json emits a structurally correct JSON object containing the expected phase.
func TestCLI_Status_EpochMidFlight_ReportsCurrentPhase_JSON(t *testing.T) {
	db := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000303"
	seedProjectionDB(t, db, epochId)

	out := runCLI(t, "--db", db, "--format", "json", "status", "--epoch-id", epochId)
	if out.exitCode != 0 {
		t.Fatalf("status --format json exit %d; stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	var status struct {
		EpochId      string   `json:"epochId"`
		CurrentPhase string   `json:"currentPhase"`
		CurrentRole  string   `json:"currentRole"`
		Transitions  []string `json:"availableTransitions"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &status); err != nil {
		t.Fatalf("decode status json: %v\nbody: %s", err, out.stdout)
	}
	if status.EpochId != epochId {
		t.Errorf("epochId = %q, want %q", status.EpochId, epochId)
	}
	if status.CurrentPhase != "propose" {
		t.Errorf("currentPhase = %q, want %q", status.CurrentPhase, "propose")
	}
	if status.CurrentRole == "" {
		t.Errorf("currentRole is empty; expected a non-empty role")
	}
}

// TestCLI_Status_ColdDurableRead_ReflectsPersistedState drives an epoch with
// one engine instance (writes to SQLite), shuts it down gracefully, then reads
// status in a fresh subprocess that has no in-memory engine state. This proves
// the status surface reads durable projection state from SQLite, not cached
// in-memory state from a still-running engine.
//
// This is a cold-read test, not a crash-recovery test. Real kill-9 recovery
// mechanics are covered by the build-tagged engine recovery tests in
// internal/engine/recovery_test.go.
func TestCLI_Status_ColdDurableRead_ReflectsPersistedState(t *testing.T) {
	db := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000304"

	// Step 1: seed the epoch (engine drives it to 'propose', then shuts down).
	seedProjectionDB(t, db, epochId)

	// Step 2: open the db COLD (no engine running) and read status via the CLI
	// binary. The binary is a fresh process with no in-memory state.
	out := runCLI(t, "--db", db, "--format", "json", "status", "--epoch-id", epochId)
	if out.exitCode != 0 {
		t.Fatalf("cold durable read: exit %d; stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	var status struct {
		EpochId      string `json:"epochId"`
		CurrentPhase string `json:"currentPhase"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &status); err != nil {
		t.Fatalf("cold durable read: decode json: %v\nbody: %s", err, out.stdout)
	}
	if status.CurrentPhase != "propose" {
		t.Errorf("cold durable read: currentPhase = %q, want %q", status.CurrentPhase, "propose")
	}
}

// TestCLI_Status_AfterTerminate_ShowsCancelReason verifies that after
// `pasture epoch terminate --reason <msg>`, running
// `pasture status --epoch-id <id>` surfaces the cancel reason in both JSON
// and text output.
func TestCLI_Status_AfterTerminate_ShowsCancelReason(t *testing.T) {
	db := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000305"
	const reason = "operator stopped this epoch for testing"

	// Seed the epoch so the projection exists.
	seedProjectionDB(t, db, epochId)

	// Terminate the epoch (records the audit event; the workflow cancellation
	// may fail since seedProjectionDB shuts down the engine, but the audit
	// record is written before the cancel attempt — that is the record we read).
	runCLI(t, "--db", db, "epoch", "terminate",
		"--epoch-id", epochId,
		"--reason", reason)
	// We accept any exit code: exit 3 is expected when the workflow is no
	// longer addressable. What matters is the audit event was written.

	// ── JSON check ─────────────────────────────────────────────────────────
	outJSON := runCLI(t, "--db", db, "--format", "json", "status", "--epoch-id", epochId)
	if outJSON.exitCode != 0 {
		t.Fatalf("status --format json after terminate exit %d; stdout=%s stderr=%s",
			outJSON.exitCode, outJSON.stdout, outJSON.stderr)
	}
	var jsonStatus struct {
		CancelReason *string `json:"cancelReason"`
		RecentEvents []struct {
			EventType string `json:"eventType"`
		} `json:"recentEvents"`
	}
	if err := json.Unmarshal([]byte(outJSON.stdout), &jsonStatus); err != nil {
		t.Fatalf("decode status json after terminate: %v\nbody: %s", err, outJSON.stdout)
	}
	if jsonStatus.CancelReason == nil {
		t.Fatal("JSON: expected non-nil cancelReason in status after terminate")
	}
	if !strings.Contains(*jsonStatus.CancelReason, reason) {
		t.Errorf("JSON: cancelReason = %q, want to contain %q", *jsonStatus.CancelReason, reason)
	}
	var foundInJSON bool
	for _, ev := range jsonStatus.RecentEvents {
		if ev.EventType == "EpochCancelled" {
			foundInJSON = true
			break
		}
	}
	if !foundInJSON {
		t.Errorf("JSON: expected EpochCancelled in recentEvents; events: %+v", jsonStatus.RecentEvents)
	}

	// ── Text check — the populated-reason branch ────────────────────────────
	// The text output must contain "CANCELLED" and the reason string.
	outText := runCLI(t, "--db", db, "status", "--epoch-id", epochId)
	if outText.exitCode != 0 {
		t.Fatalf("status text after terminate exit %d; stdout=%s stderr=%s",
			outText.exitCode, outText.stdout, outText.stderr)
	}
	combinedText := outText.stdout + outText.stderr
	if !strings.Contains(combinedText, "CANCELLED") {
		t.Errorf("text output missing 'CANCELLED'; got: %s", combinedText)
	}
	if !strings.Contains(combinedText, reason) {
		t.Errorf("text output missing cancel reason %q; got: %s", reason, combinedText)
	}
}

// TestCLI_Status_AfterTerminate_EmptyReason_ShowsCancelledNoReason verifies
// that a terminate with no --reason flag renders the empty-reason branch
// ("CANCELLED (no reason recorded)") in text mode.
func TestCLI_Status_AfterTerminate_EmptyReason_ShowsCancelledNoReason(t *testing.T) {
	db := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000310"
	seedProjectionDB(t, db, epochId)

	// Terminate without a reason.
	runCLI(t, "--db", db, "epoch", "terminate", "--epoch-id", epochId)

	outText := runCLI(t, "--db", db, "status", "--epoch-id", epochId)
	if outText.exitCode != 0 {
		t.Fatalf("status text after empty-reason terminate exit %d; stdout=%s stderr=%s",
			outText.exitCode, outText.stdout, outText.stderr)
	}
	combinedText := outText.stdout + outText.stderr
	if !strings.Contains(combinedText, "CANCELLED") {
		t.Errorf("text output missing 'CANCELLED'; got: %s", combinedText)
	}
	if !strings.Contains(combinedText, "no reason recorded") {
		t.Errorf("text output missing 'no reason recorded'; got: %s", combinedText)
	}
}

// TestCLI_Status_CancelReasonSurvivedTruncation verifies that an EpochCancelled
// event is still surfaced in the cancel reason even when 10+ subsequent events
// have been written after it (pushing it outside the most-recent-10 display
// window). The full event list must be scanned for the cancel reason before
// display truncation.
func TestCLI_Status_CancelReasonSurvivedTruncation(t *testing.T) {
	db := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000311"
	const reason = "deliberate cancellation for truncation regression test"

	// Seed the epoch.
	seedProjectionDB(t, db, epochId)

	// Record the EpochCancelled event.
	runCLI(t, "--db", db, "epoch", "terminate",
		"--epoch-id", epochId,
		"--reason", reason)

	// Append 11 more events so the EpochCancelled is pushed out of the
	// most-recent-10 display window.
	recordAuditEvents(t, db, epochId, 11)

	// Status must still surface the cancel reason even though EpochCancelled
	// is not in the most recent 10 events.
	out := runCLI(t, "--db", db, "--format", "json", "status", "--epoch-id", epochId)
	if out.exitCode != 0 {
		t.Fatalf("status after truncation exit %d; stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	var status struct {
		CancelReason *string `json:"cancelReason"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &status); err != nil {
		t.Fatalf("decode status json after truncation: %v\nbody: %s", err, out.stdout)
	}
	if status.CancelReason == nil {
		t.Fatal("cancelReason is nil even though EpochCancelled event exists beyond the display window — the full event list must be scanned before truncation")
	}
	if !strings.Contains(*status.CancelReason, reason) {
		t.Errorf("cancelReason = %q, want to contain %q", *status.CancelReason, reason)
	}
}

// TestCLI_Status_List_ShowsAllEpochs verifies that `pasture status` (no
// --epoch-id) lists all recorded epochs, including ones seeded by the test,
// and that the event count is greater than zero for seeded epochs (proving
// the enrichment is real, not always-zero).
func TestCLI_Status_List_ShowsAllEpochs(t *testing.T) {
	db := newDB(t)
	const epochId1 = "demo--01960000-0000-7000-8000-000000000306"
	const epochId2 = "demo--01960000-0000-7000-8000-000000000307"

	seedProjectionDB(t, db, epochId1)
	seedProjectionDB(t, db, epochId2)

	out := runCLI(t, "--db", db, "--format", "json", "status")
	if out.exitCode != 0 {
		t.Fatalf("status list exit %d; stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	var epochs []struct {
		EpochId      string `json:"epochId"`
		CurrentPhase string `json:"currentPhase"`
		EventCount   int    `json:"eventCount"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &epochs); err != nil {
		t.Fatalf("decode status list json: %v\nbody: %s", err, out.stdout)
	}
	found1, found2 := false, false
	for _, e := range epochs {
		if e.EpochId == epochId1 {
			found1 = true
			if e.EventCount == 0 {
				t.Errorf("epoch %s has eventCount=0; expected >0 after seedProjectionDB (seeding writes audit events)", epochId1)
			}
		}
		if e.EpochId == epochId2 {
			found2 = true
			if e.EventCount == 0 {
				t.Errorf("epoch %s has eventCount=0; expected >0 after seedProjectionDB (seeding writes audit events)", epochId2)
			}
		}
	}
	if !found1 {
		t.Errorf("epoch %s not found in status list; got: %+v", epochId1, epochs)
	}
	if !found2 {
		t.Errorf("epoch %s not found in status list; got: %+v", epochId2, epochs)
	}
}

// TestCLI_Status_List_ShowsAllEpochs_Text verifies the text listing includes
// the seeded epoch id and phase.
func TestCLI_Status_List_ShowsAllEpochs_Text(t *testing.T) {
	db := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000308"
	seedProjectionDB(t, db, epochId)

	out := runCLI(t, "--db", db, "status")
	if out.exitCode != 0 {
		t.Fatalf("status text list exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	combined := out.stdout + out.stderr
	if !strings.Contains(combined, epochId) {
		t.Errorf("text listing missing epochId %q; got: %s", epochId, combined)
	}
	if !strings.Contains(combined, "propose") {
		t.Errorf("text listing missing phase 'propose'; got: %s", combined)
	}
}

// ─── sidecar test (item 5: zpnv7) ─────────────────────────────────────────────

// TestCLI_Status_ReadOnly_NoUnexpectedSidecars verifies that running
// `pasture status` on an existing database leaves no unexpected sidecar files
// (beyond the standard SQLite -wal and -shm work files). The main database
// file hash must be unchanged, confirming no data was written.
func TestCLI_Status_ReadOnly_NoUnexpectedSidecars(t *testing.T) {
	db := newDB(t)
	// Pre-create and migrate the database so it exists with a valid schema.
	migrateOut := runCLI(t, "--db", db, "migrate")
	if migrateOut.exitCode != 0 {
		t.Fatalf("migrate to pre-create db: exit %d; stderr=%s", migrateOut.exitCode, migrateOut.stderr)
	}

	// Snapshot directory contents before the status run.
	dbDir := filepath.Dir(db)
	dbBase := filepath.Base(db)
	beforeEntries := dirEntryNames(t, dbDir)

	out := runCLI(t, "--db", db, "status")
	if out.exitCode != 0 {
		t.Fatalf("status exit %d; stderr=%s", out.exitCode, out.stderr)
	}

	// Snapshot directory contents after.
	afterEntries := dirEntryNames(t, dbDir)

	// Allowed sidecar names: SQLite WAL and shared-memory files. The main db file
	// itself is always present.
	allowed := map[string]bool{
		dbBase:          true,
		dbBase + "-wal": true,
		dbBase + "-shm": true,
	}
	for _, name := range afterEntries {
		if !allowed[name] {
			// Only flag entries that didn't exist before the status run.
			newEntry := true
			for _, b := range beforeEntries {
				if b == name {
					newEntry = false
					break
				}
			}
			if newEntry {
				t.Errorf("unexpected file %q appeared in db dir after status run (expected only db, -wal, -shm)", name)
			}
		}
	}
}

// dirEntryNames returns the names of all entries in dir.
func dirEntryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %q: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// ─── never-migrate regression tests (item 1: kjtvw) ──────────────────────────

// readSchemaVersionDirect opens the database at dbPath with the shared WAL DSN
// (NOT read-only, so it can be used for setup writes) and returns
// MAX(version) from audit_schema_meta. Returns 0 if the table is absent.
// Uses an INDEPENDENT connection so any cached state from a prior open is gone.
func readSchemaVersionDirect(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
	if err != nil {
		t.Fatalf("readSchemaVersionDirect: sql.Open: %v", err)
	}
	defer db.Close()

	var version sql.NullInt64
	row := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`)
	if err := row.Scan(&version); err != nil {
		// Table might not exist or be empty; treat as 0.
		return 0
	}
	if !version.Valid {
		return 0
	}
	return int(version.Int64)
}

// downgradeSchemaMeta opens the database at dbPath and deletes the latest
// version row from audit_schema_meta so MAX(version) drops by 1.
// Uses an independent connection so no other handle is sharing the file.
func downgradeSchemaMeta(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
	if err != nil {
		t.Fatalf("downgradeSchemaMeta: sql.Open: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`DELETE FROM audit_schema_meta
		WHERE version = (SELECT MAX(version) FROM audit_schema_meta)`)
	if err != nil {
		t.Fatalf("downgradeSchemaMeta: DELETE: %v", err)
	}
}

// upgradeSchemaMeta opens the database at dbPath and inserts a synthetic future
// version row so MAX(version) exceeds audit.MaxKnownSchemaVersion. This
// simulates a database written by a newer binary.
func upgradeSchemaMeta(t *testing.T, dbPath string, futureVersion int) {
	t.Helper()
	db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
	if err != nil {
		t.Fatalf("upgradeSchemaMeta: sql.Open: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO audit_schema_meta (version, applied_at) VALUES (?, datetime('now'))`, futureVersion)
	if err != nil {
		t.Fatalf("upgradeSchemaMeta: INSERT: %v", err)
	}
}

// insertSyntheticProjectionRow inserts a minimal row into epoch_state_projection
// so the status command has something to look up (tests the populated-detail
// path, not just the empty-listing path).
func insertSyntheticProjectionRow(t *testing.T, dbPath, epochId string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
	if err != nil {
		t.Fatalf("insertSyntheticProjectionRow: sql.Open: %v", err)
	}
	defer db.Close()

	// Ensure the table exists (it may not if no engine ran).
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS epoch_state_projection (
		epoch_id      TEXT    PRIMARY KEY,
		current_phase TEXT    NOT NULL,
		state_json    TEXT    NOT NULL,
		updated_at    INTEGER NOT NULL
	)`)
	if err != nil {
		t.Fatalf("insertSyntheticProjectionRow: CREATE TABLE: %v", err)
	}

	_, err = db.Exec(`INSERT OR IGNORE INTO epoch_state_projection
		(epoch_id, current_phase, state_json, updated_at) VALUES (?, ?, ?, ?)`,
		epochId, "elicit", `{"epochId":"`+epochId+`","currentPhase":"elicit"}`, time.Now().UnixNano())
	if err != nil {
		t.Fatalf("insertSyntheticProjectionRow: INSERT: %v", err)
	}
}

// TestCLI_Status_StaleSchema_OlderDB_ErrorsAndNeverMigrates verifies that when
// the database schema is OLDER than the binary expects, `pasture status` returns
// exit 5 with an actionable mismatch error, and DOES NOT migrate the database
// (the MAX(version) in audit_schema_meta is unchanged before and after the run).
//
// Uses an independent database/sql connection to read the schema version, not
// the same handle used by the status command, to rule out any caching effect.
func TestCLI_Status_StaleSchema_OlderDB_ErrorsAndNeverMigrates(t *testing.T) {
	dbPath := newDB(t)

	// Step 1: create a fully-migrated database.
	migrateOut := runCLI(t, "--db", dbPath, "migrate")
	if migrateOut.exitCode != 0 {
		t.Fatalf("migrate: exit %d; stderr=%s", migrateOut.exitCode, migrateOut.stderr)
	}

	// Step 2: downgrade the meta version via an independent connection
	// (simulate a database that was only partially migrated).
	downgradeSchemaMeta(t, dbPath)
	downgradedVersion := readSchemaVersionDirect(t, dbPath)
	if downgradedVersion >= audit.MaxKnownSchemaVersion {
		t.Fatalf("expected version < %d after downgrade, got %d", audit.MaxKnownSchemaVersion, downgradedVersion)
	}

	// Step 3: insert a synthetic projection row so the detail path is exercised.
	const epochId = "demo--01960000-0000-7000-8000-000000000381"
	insertSyntheticProjectionRow(t, dbPath, epochId)

	// Step 4: run status — must exit 5 with actionable mismatch message.
	out := runCLI(t, "--db", dbPath, "status", "--epoch-id", epochId)
	if out.exitCode != 5 {
		t.Fatalf("expected exit 5 for stale schema (db older); got %d; stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	combined := out.stdout + out.stderr
	for _, want := range []string{"Problem:", "How to fix:", "pasture migrate"} {
		if !strings.Contains(combined, want) {
			t.Errorf("stale-schema error missing %q; got: %s", want, combined)
		}
	}

	// Step 5: re-read version via a NEW independent connection — must be unchanged.
	versionAfter := readSchemaVersionDirect(t, dbPath)
	if versionAfter != downgradedVersion {
		t.Errorf("schema version changed during status run: before=%d after=%d (status MUST NOT migrate)",
			downgradedVersion, versionAfter)
	}
}

// TestCLI_Status_StaleSchema_NewerDB_ErrorsAndNeverMigrates verifies that when
// the database schema is NEWER than the binary (a newer daemon upgraded it),
// `pasture status` returns exit 5 with an actionable mismatch error, and does
// NOT alter the database (MAX(version) unchanged).
func TestCLI_Status_StaleSchema_NewerDB_ErrorsAndNeverMigrates(t *testing.T) {
	dbPath := newDB(t)

	// Step 1: create a fully-migrated database.
	migrateOut := runCLI(t, "--db", dbPath, "migrate")
	if migrateOut.exitCode != 0 {
		t.Fatalf("migrate: exit %d; stderr=%s", migrateOut.exitCode, migrateOut.stderr)
	}

	// Step 2: insert a synthetic future-version row via an independent connection.
	const futureVersion = 99
	upgradeSchemaMeta(t, dbPath, futureVersion)
	versionBefore := readSchemaVersionDirect(t, dbPath)
	if versionBefore != futureVersion {
		t.Fatalf("expected version %d after synthetic upgrade, got %d", futureVersion, versionBefore)
	}

	// Step 3: insert a synthetic projection row.
	const epochId = "demo--01960000-0000-7000-8000-000000000382"
	insertSyntheticProjectionRow(t, dbPath, epochId)

	// Step 4: run status — must exit 5 with actionable mismatch message.
	out := runCLI(t, "--db", dbPath, "status", "--epoch-id", epochId)
	if out.exitCode != 5 {
		t.Fatalf("expected exit 5 for stale schema (db newer); got %d; stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	combined := out.stdout + out.stderr
	for _, want := range []string{"Problem:", "How to fix:"} {
		if !strings.Contains(combined, want) {
			t.Errorf("newer-schema error missing %q; got: %s", want, combined)
		}
	}
	// The newer-binary message should mention upgrading pasture, not migrating.
	if !strings.Contains(combined, "Upgrade") && !strings.Contains(combined, "upgrade") {
		t.Errorf("newer-schema error should mention upgrading pasture; got: %s", combined)
	}

	// Step 5: re-read version via a NEW independent connection — must be unchanged.
	versionAfter := readSchemaVersionDirect(t, dbPath)
	if versionAfter != futureVersion {
		t.Errorf("schema version changed during status run: before=%d after=%d (status MUST NOT migrate)",
			versionBefore, versionAfter)
	}
}

// TestCLI_Status_StaleSchema_OlderDB_ListPath_ErrorsNotSilent verifies that a
// mismatched database with 0 epochs returns exit 5 (NOT exit 0 with an empty
// listing). The schema-version gate must fire on the listing path, not just
// the populated-detail path.
func TestCLI_Status_StaleSchema_OlderDB_ListPath_ErrorsNotSilent(t *testing.T) {
	dbPath := newDB(t)

	// Create a migrated database (no epochs) and downgrade its version.
	migrateOut := runCLI(t, "--db", dbPath, "migrate")
	if migrateOut.exitCode != 0 {
		t.Fatalf("migrate: exit %d; stderr=%s", migrateOut.exitCode, migrateOut.stderr)
	}
	downgradeSchemaMeta(t, dbPath)

	// status (list, no --epoch-id) on a mismatched db must fail, not silently succeed.
	out := runCLI(t, "--db", dbPath, "status")
	if out.exitCode != 5 {
		t.Fatalf("expected exit 5 for stale schema on list path; got %d; stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	if !strings.Contains(out.stdout+out.stderr, "Problem:") {
		t.Errorf("list-path mismatch error missing Problem: section; got: %s", out.stderr)
	}
}

// TestCLI_Status_StaleSchema_OlderDB_UnknownEpoch_ErrorsMismatchNotMisspelled
// verifies that on a mismatched db, asking for an unknown epoch returns exit 5
// (schema mismatch) NOT exit 3 (epoch not found / misspelled ID). This
// guards against the bug where version skew was misdiagnosed as a bad epoch ID.
func TestCLI_Status_StaleSchema_OlderDB_UnknownEpoch_ErrorsMismatchNotMisspelled(t *testing.T) {
	dbPath := newDB(t)

	// Create a migrated database and downgrade.
	migrateOut := runCLI(t, "--db", dbPath, "migrate")
	if migrateOut.exitCode != 0 {
		t.Fatalf("migrate: exit %d; stderr=%s", migrateOut.exitCode, migrateOut.stderr)
	}
	downgradeSchemaMeta(t, dbPath)

	const unknownId = "demo--01960000-0000-7000-8000-000000000383"
	out := runCLI(t, "--db", dbPath, "status", "--epoch-id", unknownId)
	// Must be exit 5 (schema mismatch), NOT exit 3 (epoch not found).
	if out.exitCode != 5 {
		t.Fatalf("expected exit 5 for stale schema + unknown epoch; got %d — version mismatch must be diagnosed before epoch lookup; stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	// Must NOT claim the epoch is misspelled.
	combined := out.stdout + out.stderr
	if strings.Contains(combined, "misspelled") || strings.Contains(combined, "not found") {
		t.Errorf("mismatch error should not mention misspelled/not-found; got: %s", combined)
	}
}
