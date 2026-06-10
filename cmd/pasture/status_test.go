package main_test

// CLI-level subprocess tests for `pasture status`.
//
// Tests exercise the production binary via subprocess (same idiom as
// signals_test.go), asserting:
//   - `pasture status` (no flags) on an empty db shows the actionable
//     empty-state message.
//   - `pasture status --epoch <id>` returns exit 3 (workflow/not-found) for
//     an epoch that has no projection row.
//   - After driving an epoch through phases, `pasture status --epoch <id>`
//     reports the current phase correctly in both text and JSON modes.
//   - After a crash+resume cycle (the engine is shut down and a second process
//     opens the same db cold), status reflects durable state, not in-memory state.
//   - `pasture status --epoch <id>` flags a terminated epoch's cancel reason.
//   - `pasture status` appears in the top-level help output.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// seedProjectionDB drives an epoch through two phase advances (p0 → elicit →
// propose) using a real engine on dbPath, then shuts the engine down. The
// caller can then re-open the database cold (simulating a crash+resume cycle)
// to test that status reads durable projection state.
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

// TestCLI_Status_EmptyDB_ShowsActionableMessage verifies that running `pasture
// status` against a fresh database (no epochs yet) returns exit 0 and prints
// the actionable empty-state message guiding users how to start an epoch.
func TestCLI_Status_EmptyDB_ShowsActionableMessage(t *testing.T) {
	db := newDB(t)
	out := runCLI(t, "--db", db, "status")
	if out.exitCode != 0 {
		t.Fatalf("status on empty db exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	combined := out.stdout + out.stderr
	// Must tell the user how to start an epoch.
	for _, want := range []string{"pasture epoch start"} {
		if !strings.Contains(combined, want) {
			t.Errorf("empty-state message missing %q; got: %s", want, combined)
		}
	}
}

// TestCLI_Status_EmptyDB_JSON_EmptyArray verifies that --format json on an
// empty db returns a JSON empty array (not an error).
func TestCLI_Status_EmptyDB_JSON_EmptyArray(t *testing.T) {
	db := newDB(t)
	out := runCLI(t, "--db", db, "--format", "json", "status")
	if out.exitCode != 0 {
		t.Fatalf("status --format json on empty db exit %d; stderr=%s",
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

// TestCLI_Status_UnknownEpoch_ReturnsExit3 verifies that `pasture status
// --epoch <id>` returns exit 3 (workflow/not-found) for an epoch that has no
// projection row, and that stderr contains the actionable structured error.
func TestCLI_Status_UnknownEpoch_ReturnsExit3(t *testing.T) {
	db := newDB(t)
	// Seed a different epoch so the projection table exists.
	const seedId = "demo--01960000-0000-7000-8000-000000000301"
	seedProjectionDB(t, db, seedId)

	const unknownId = "demo--01960000-0000-7000-8000-000000009999"
	out := runCLI(t, "--db", db, "status", "--epoch", unknownId)
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

	out := runCLI(t, "--db", db, "status", "--epoch", epochId)
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

	out := runCLI(t, "--db", db, "--format", "json", "status", "--epoch", epochId)
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

// TestCLI_Status_AfterCrashResume_ReflectsDurableState is the crash+resume test:
// it drives an epoch with one engine instance (writes to disk), shuts it down,
// then opens the same database cold in a subprocess and asserts that `pasture
// status --epoch <id>` still reports the durable projected phase. This proves
// the status surface reads durable state from SQLite, not in-memory state.
func TestCLI_Status_AfterCrashResume_ReflectsDurableState(t *testing.T) {
	db := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000304"

	// Step 1: seed the epoch (engine drives it to 'propose', then shuts down —
	// simulating a process crash after the work is durably persisted).
	seedProjectionDB(t, db, epochId)

	// Step 2: open the db COLD (no engine running) and read status via the CLI
	// binary. The binary is a fresh process that has never seen in-memory state.
	out := runCLI(t, "--db", db, "--format", "json", "status", "--epoch", epochId)
	if out.exitCode != 0 {
		t.Fatalf("status after crash exit %d; stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	var status struct {
		EpochId      string `json:"epochId"`
		CurrentPhase string `json:"currentPhase"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &status); err != nil {
		t.Fatalf("decode status json after crash: %v\nbody: %s", err, out.stdout)
	}
	if status.CurrentPhase != "propose" {
		t.Errorf("crash+resume: currentPhase = %q, want %q", status.CurrentPhase, "propose")
	}
}

// TestCLI_Status_AfterTerminate_ShowsCancelReason verifies that after
// `pasture epoch terminate --reason <msg>`, running `pasture status --epoch <id>`
// surfaces the cancel reason in the output. Uses the text path so the search
// is format-agnostic.
func TestCLI_Status_AfterTerminate_ShowsCancelReason(t *testing.T) {
	db := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000305"
	const reason = "operator stopped this epoch for testing"

	// Seed the epoch so the projection exists.
	seedProjectionDB(t, db, epochId)

	// Terminate the epoch (records the audit event, the cancel itself may fail
	// since the workflow is no longer running after seedProjectionDB shuts it
	// down — that's OK; the audit record is written before the cancel attempt).
	runCLI(t, "--db", db, "epoch", "terminate",
		"--epoch-id", epochId,
		"--reason", reason)
	// We accept any exit code here: exit 3 is expected when the workflow is no
	// longer addressable. What matters is the audit event was written.

	// Now status must surface the cancel reason.
	out := runCLI(t, "--db", db, "--format", "json", "status", "--epoch", epochId)
	if out.exitCode != 0 {
		t.Fatalf("status after terminate exit %d; stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	var status struct {
		CancelReason *string `json:"cancelReason"`
		RecentEvents []struct {
			EventType string `json:"eventType"`
		} `json:"recentEvents"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &status); err != nil {
		t.Fatalf("decode status json after terminate: %v\nbody: %s", err, out.stdout)
	}

	// The cancel reason must be present and carry the expected text.
	if status.CancelReason == nil {
		t.Fatal("expected non-nil cancelReason in status after terminate")
	}
	if !strings.Contains(*status.CancelReason, reason) {
		t.Errorf("cancelReason = %q, want to contain %q", *status.CancelReason, reason)
	}

	// The EpochCancelled event must also appear in recentEvents.
	var found bool
	for _, ev := range status.RecentEvents {
		if ev.EventType == "EpochCancelled" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected EpochCancelled in recentEvents; events: %+v", status.RecentEvents)
	}
}

// TestCLI_Status_List_ShowsAllEpochs verifies that `pasture status` (no
// --epoch) lists all recorded epochs, including one seeded by the test.
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
	}
	if err := json.Unmarshal([]byte(out.stdout), &epochs); err != nil {
		t.Fatalf("decode status list json: %v\nbody: %s", err, out.stdout)
	}
	found1, found2 := false, false
	for _, e := range epochs {
		if e.EpochId == epochId1 {
			found1 = true
		}
		if e.EpochId == epochId2 {
			found2 = true
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

// ─── unused import guard ──────────────────────────────────────────────────────

// Ensure the engine + dbos imports (used in seedProjectionDB) don't get
// pruned by a linter's dead-import check. The functions above reference them
// directly, so this is a documentation marker only.
var _ = filepath.Join
