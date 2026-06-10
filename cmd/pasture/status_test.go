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
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

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
	// Must fail: no db = no epochs, and the absent-db path is an error, not
	// an empty listing.
	if out.exitCode == 0 {
		t.Fatalf("expected non-zero exit for absent db; stdout=%s", out.stdout)
	}
	// Stderr must contain an actionable message directing the user.
	combined := out.stdout + out.stderr
	if !strings.Contains(combined, "daemon") && !strings.Contains(combined, "pastured") {
		t.Errorf("absent-db message should mention daemon/pastured; got: %s", combined)
	}
	// The file must NOT have been created.
	if _, err := os.Stat(dbPath); err == nil {
		t.Errorf("status on absent db created the database file at %q — must not modify any state", dbPath)
	}
}

// TestCLI_Status_AbsentDB_EpochId_NoFileCreated verifies that
// `pasture status --epoch-id <id>` against a path where the database does not
// exist also fails with an actionable message and does NOT create the file.
func TestCLI_Status_AbsentDB_EpochId_NoFileCreated(t *testing.T) {
	dbPath := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000399"
	out := runCLI(t, "--db", dbPath, "status", "--epoch-id", epochId)
	if out.exitCode == 0 {
		t.Fatalf("expected non-zero exit for absent db with --epoch-id; stdout=%s", out.stdout)
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
