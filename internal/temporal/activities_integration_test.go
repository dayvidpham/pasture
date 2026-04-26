// Package temporal — Integration tests for S8 workflow integration
// (PROPOSAL-2 §7.11 / §7.12 / §11 Scenarios 2, 8a-8e, 13).
//
// Coverage:
//
//   - TestActivities_RecordTransition_TaskTrackerPath (Scenario 1 layered):
//     RecordTransition against a real unified tracker writes both an
//     audit_events row AND a context_edges(EpochContext, epochID) row.
//
//   - TestActivities_RecordTransition_AttributesToTransitionGate (Scenario 8b):
//     the recorded event's agent_id resolves (via JOIN) to a SoftwareAgent
//     with name "pasture/automaton/transition-gate/consensus".
//
//   - TestActivities_RecordAuditEvent_AttributesToConstraintChecker (Scenario 8a):
//     RecordAuditEvent with empty Role defaults to the
//     "pasture/automaton/check-constraints" well-known agent.
//
//   - TestActivities_RecordAuditEvent_AttributesToConsensusReached (Scenario 8d)
//     and TestActivities_RecordAuditEvent_AttributesToCreateFollowup (Scenario 8e):
//     explicit Role with first-class UAT-1 categories resolves to the
//     correct well-known agent.
//
//   - TestActivities_RecordAuditEvent_AttributesToHookHandler (Scenario 8c):
//     parameterised over the 9 Claude Code hook events; each resolves to
//     "pasture/automaton/hook/<name>".
//
//   - TestActivities_RecordTransition_RejectsMalformedEpochID (Scenario 13
//     activity-level): direct activity invocation with a free-string epochID
//     returns *StructuredError (CategoryValidation, "not a valid Provenance
//     TaskID" in What, "pasture task create REQUEST" in Fix), and no rows
//     leak to audit_events / context_edges / tasks.
//
//   - TestEpochWorkflow_SearchAttributes_R13Snapshot (Scenario 2): structural
//     snapshot of the workflow.go UpsertTypedSearchAttributes call sites —
//     verifies the SA keys + value-set order match the captured baseline.
//     Any refactor that moves, renames, or reorders the SA upserts will fail
//     this test, enforcing the R13 "byte-identical" binding.

package temporal_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/temporal"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// debugErr surfaces the Why field of a *pasterrors.StructuredError so test
// failures don't truncate the underlying cause. For non-structured errors it
// returns err.Error() unchanged.
func debugErr(err error) string {
	if err == nil {
		return "<nil>"
	}
	if se, ok := err.(*pasterrors.StructuredError); ok {
		return fmt.Sprintf("%s | why=%s | fix=%s", se.Error(), se.Why, se.Fix)
	}
	return err.Error()
}

// newTestActivityEnv constructs a Temporal test activity environment wired to
// the supplied Activities — the canonical way to exercise activities through
// their normal Temporal entry path (so activity.GetLogger and other
// activity-context APIs work). Activities are looked up by simple method name
// (Temporal default).
func newTestActivityEnv(t *testing.T, acts *temporal.Activities) *testsuite.TestActivityEnvironment {
	t.Helper()
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts)
	return env
}

// ─── Test fixtures ──────────────────────────────────────────────────────────

// newUnifiedTrackerForTest opens a fresh unified TaskTracker (Provenance +
// audit) backed by a SQLite file under t.TempDir(), runs S7's well-known
// agent registration, and returns the tracker + populated cache + cleanup.
//
// Each call mints a separate file so parallel test cases do not contend on
// the same db (each goroutine gets its own pasture.db inside its own TempDir).
//
// The tests deliberately use a real file (not in-memory SQLite) per the
// PROPOSAL-2 §10.3 / pasture/CLAUDE.md binding: "All file-backed integration
// tests use t.TempDir(), NOT /tmp or in-memory SQLite (the in-memory path
// bypasses WAL/busy_timeout/fsync — the exact mechanisms D11 relies on)."
func newUnifiedTrackerForTest(t *testing.T) (protocol.TaskTracker, *tasks.WellKnownAgentCache) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pasture.db")

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("tasks.OpenTaskTracker: %v", err)
	}
	t.Cleanup(func() {
		if cerr := tracker.Close(); cerr != nil {
			t.Logf("tracker.Close: %v", cerr)
		}
	})

	cache := tasks.NewWellKnownAgentCache()
	if regErr := tasks.RegisterWellKnownAgents(context.Background(), tracker, cache); regErr != nil {
		t.Fatalf("RegisterWellKnownAgents: %v", regErr)
	}
	if cache.Len() != tasks.WellKnownAgentCount {
		t.Fatalf("cache.Len() = %d, want %d", cache.Len(), tasks.WellKnownAgentCount)
	}
	return tracker, cache
}

// newRequestTaskID creates a REQUEST task in the unified tracker and returns
// its ID as a string suitable for use as an epoch_id (i.e., satisfies
// provenance.ParseTaskID). This mirrors the production flow where the user
// runs `pasture task create REQUEST` before `pasture-msg epoch start`.
func newRequestTaskID(t *testing.T, tracker protocol.TaskTracker) string {
	t.Helper()
	req, err := tracker.Create(
		"aura-plugins-s8-test",
		"S8 integration test request",
		"created by activities_integration_test.go",
		provenance.TaskTypeFeature,
		provenance.PriorityMedium,
		provenance.PhaseRequest,
	)
	if err != nil {
		t.Fatalf("tracker.Create: %v", err)
	}
	return req.ID.String()
}

// ─── Scenario 1 (layered): unified Tracker path writes audit_events +
// context_edges atomically ─────────────────────────────────────────────────

func TestActivities_RecordTransition_TaskTrackerPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, cache := newUnifiedTrackerForTest(t)
	epochID := newRequestTaskID(t, tracker)

	acts := &temporal.Activities{
		Tracker:         tracker,
		WellKnownAgents: cache,
	}
	env := newTestActivityEnv(t, acts)

	rec := types.TransitionRecord{
		FromPhase:    protocol.PhaseRequest,
		ToPhase:      protocol.PhaseElicit,
		Timestamp:    time.Now().UTC(),
		TriggeredBy:  "architect",
		ConditionMet: "classification confirmed",
		Success:      true,
	}
	if _, err := env.ExecuteActivity(acts.RecordTransition, epochID, rec); err != nil {
		t.Fatalf("RecordTransition: %v", err)
	}

	// Layer A: audit_events row exists.
	events, err := tracker.QueryEvents(ctx, epochID, nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit_events row count = %d, want 1", len(events))
	}
	if events[0].EventType != protocol.EventPhaseTransition {
		t.Errorf("event.EventType = %q, want %q", events[0].EventType, protocol.EventPhaseTransition)
	}

	// Layer B: context_edges row exists for (EpochContext, epochID) — verified
	// via Timeline lookup which JOINs context_edges with audit_events.
	tlEvents, err := tracker.Timeline(ctx, protocol.ContextEpoch, epochID)
	if err != nil {
		t.Fatalf("Timeline: %s", debugErr(err))
	}
	if len(tlEvents) != 1 {
		t.Errorf("Timeline(EpochContext, %q) returned %d events, want 1 (context_edges row missing — AttachContext did not run)", epochID, len(tlEvents))
	}
}

// ─── Scenario 8b: TransitionGate attribution ────────────────────────────────

func TestActivities_RecordTransition_AttributesToTransitionGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, cache := newUnifiedTrackerForTest(t)
	epochID := newRequestTaskID(t, tracker)

	acts := &temporal.Activities{
		Tracker:         tracker,
		WellKnownAgents: cache,
	}
	env := newTestActivityEnv(t, acts)

	rec := types.TransitionRecord{
		FromPhase:   protocol.PhaseRequest,
		ToPhase:     protocol.PhaseElicit,
		Timestamp:   time.Now().UTC(),
		TriggeredBy: "architect",
		Success:     true,
	}
	if _, err := env.ExecuteActivity(acts.RecordTransition, epochID, rec); err != nil {
		t.Fatalf("RecordTransition: %v", err)
	}

	// Timeline returns the event with Role populated from agents_software.name.
	tlEvents, err := tracker.Timeline(ctx, protocol.ContextEpoch, epochID)
	if err != nil {
		t.Fatalf("Timeline: %s", debugErr(err))
	}
	if len(tlEvents) != 1 {
		t.Fatalf("Timeline returned %d events, want 1", len(tlEvents))
	}

	wantName := "pasture/automaton/transition-gate/consensus"
	if tlEvents[0].Role != wantName {
		t.Errorf("event.Role (= agents_software.name) = %q, want %q (Scenario 8b: RecordTransition must attribute to the consensus transition-gate well-known agent)", tlEvents[0].Role, wantName)
	}

	// Cross-check: the well-known cache resolves the same name to a non-zero AgentID.
	id, ok := cache.Get(wantName)
	if !ok {
		t.Errorf("cache.Get(%q): missing — S7 well-known agent registration did not include this name", wantName)
	}
	if id.UUID.String() == "00000000-0000-0000-0000-000000000000" {
		t.Errorf("cache.Get(%q) returned zero AgentID — registration failed silently", wantName)
	}
}

// ─── Scenario 8a: ConstraintChecker attribution (default for RecordAuditEvent) ─

func TestActivities_RecordAuditEvent_AttributesToConstraintChecker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, cache := newUnifiedTrackerForTest(t)
	epochID := newRequestTaskID(t, tracker)

	acts := &temporal.Activities{
		Tracker:         tracker,
		WellKnownAgents: cache,
	}
	env := newTestActivityEnv(t, acts)

	// Empty Role → default to ConstraintChecker (the most common
	// RecordAuditEvent caller is the constraint-violation path).
	ev := protocol.AuditEvent{
		EpochID:   epochID,
		Phase:     protocol.PhaseRequest,
		EventType: protocol.EventConstraintChecked,
		Payload:   map[string]any{"violation": "none"},
		Timestamp: time.Now().UTC(),
	}
	if _, err := env.ExecuteActivity(acts.RecordAuditEvent, ev); err != nil {
		t.Fatalf("RecordAuditEvent: %v", err)
	}

	tlEvents, err := tracker.Timeline(ctx, protocol.ContextEpoch, epochID)
	if err != nil {
		t.Fatalf("Timeline: %s", debugErr(err))
	}
	if len(tlEvents) != 1 {
		t.Fatalf("Timeline returned %d events, want 1", len(tlEvents))
	}

	wantName := "pasture/automaton/check-constraints"
	if tlEvents[0].Role != wantName {
		t.Errorf("event.Role (= agents_software.name) = %q, want %q (Scenario 8a: RecordAuditEvent default attribution must be ConstraintChecker)", tlEvents[0].Role, wantName)
	}
}

// ─── Scenario 8d: ConsensusReached (UAT-1 first-class category) ─────────────

func TestActivities_RecordAuditEvent_AttributesToConsensusReached(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, cache := newUnifiedTrackerForTest(t)
	epochID := newRequestTaskID(t, tracker)

	acts := &temporal.Activities{
		Tracker:         tracker,
		WellKnownAgents: cache,
	}
	env := newTestActivityEnv(t, acts)

	wantName := "pasture/automaton/consensus-reached"
	ev := protocol.AuditEvent{
		EpochID:   epochID,
		Phase:     protocol.PhaseReview,
		Role:      wantName, // Explicit attribution to the UAT-1 first-class category.
		EventType: protocol.EventVoteRecorded,
		Payload:   map[string]any{"axis": "correctness", "vote": "accept"},
		Timestamp: time.Now().UTC(),
	}
	if _, err := env.ExecuteActivity(acts.RecordAuditEvent, ev); err != nil {
		t.Fatalf("RecordAuditEvent: %v", err)
	}

	tlEvents, err := tracker.Timeline(ctx, protocol.ContextEpoch, epochID)
	if err != nil {
		t.Fatalf("Timeline: %s", debugErr(err))
	}
	if len(tlEvents) != 1 {
		t.Fatalf("Timeline returned %d events, want 1", len(tlEvents))
	}

	if tlEvents[0].Role != wantName {
		t.Errorf("event.Role = %q, want %q (Scenario 8d: ConsensusReached attribution; UAT-1 dropped the generic Derivation category in favour of this concrete one)", tlEvents[0].Role, wantName)
	}
}

// ─── Scenario 8e: CreateFollowup (UAT-1 first-class category) ───────────────

func TestActivities_RecordAuditEvent_AttributesToCreateFollowup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, cache := newUnifiedTrackerForTest(t)
	epochID := newRequestTaskID(t, tracker)

	acts := &temporal.Activities{
		Tracker:         tracker,
		WellKnownAgents: cache,
	}
	env := newTestActivityEnv(t, acts)

	wantName := "pasture/automaton/create-followup"
	ev := protocol.AuditEvent{
		EpochID:   epochID,
		Phase:     protocol.PhaseRatify,
		Role:      wantName,
		EventType: protocol.EventSliceStarted,
		Payload:   map[string]any{"followup_id": "aura-plugins-followup-1"},
		Timestamp: time.Now().UTC(),
	}
	if _, err := env.ExecuteActivity(acts.RecordAuditEvent, ev); err != nil {
		t.Fatalf("RecordAuditEvent: %v", err)
	}

	tlEvents, err := tracker.Timeline(ctx, protocol.ContextEpoch, epochID)
	if err != nil {
		t.Fatalf("Timeline: %s", debugErr(err))
	}
	if len(tlEvents) != 1 {
		t.Fatalf("Timeline returned %d events, want 1", len(tlEvents))
	}
	if tlEvents[0].Role != wantName {
		t.Errorf("event.Role = %q, want %q (Scenario 8e: CreateFollowup attribution; UAT-1 first-class category)", tlEvents[0].Role, wantName)
	}
}

// ─── Scenario 8c: HookHandler attribution (parameterised over 9 hooks) ─────

func TestActivities_RecordAuditEvent_AttributesToHookHandler(t *testing.T) {
	t.Parallel()

	// Canonical 9 Claude Code hook events from PROPOSAL-2 §7.7.2 (mirrored
	// in internal/tasks/well_known_registry.go's claudeCodeHookEvents).
	hookEvents := []string{
		"SessionStart",
		"UserPromptSubmit",
		"PreToolUse",
		"PostToolUse",
		"Notification",
		"Stop",
		"SubagentStop",
		"PreCompact",
		"SessionEnd",
	}

	for _, hookName := range hookEvents {
		hookName := hookName // capture for parallel
		t.Run(hookName, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			tracker, cache := newUnifiedTrackerForTest(t)
			epochID := newRequestTaskID(t, tracker)

			acts := &temporal.Activities{
				Tracker:         tracker,
				WellKnownAgents: cache,
			}
			env := newTestActivityEnv(t, acts)

			wantName := "pasture/automaton/hook/" + hookName
			ev := protocol.AuditEvent{
				EpochID:   epochID,
				Phase:     protocol.PhaseWorkerSlices,
				Role:      wantName,
				EventType: protocol.EventSliceStarted,
				Payload:   map[string]any{"hook": hookName},
				Timestamp: time.Now().UTC(),
			}
			if _, err := env.ExecuteActivity(acts.RecordAuditEvent, ev); err != nil {
				t.Fatalf("RecordAuditEvent: %v", err)
			}

			tlEvents, err := tracker.Timeline(ctx, protocol.ContextEpoch, epochID)
			if err != nil {
				t.Fatalf("Timeline: %+v", err)
			}
			if len(tlEvents) != 1 {
				t.Fatalf("Timeline returned %d events, want 1", len(tlEvents))
			}
			if tlEvents[0].Role != wantName {
				t.Errorf("event.Role = %q, want %q (Scenario 8c: HookHandler attribution for %q)", tlEvents[0].Role, wantName, hookName)
			}
		})
	}
}

// ─── Scenario 13 activity-level: malformed epochID rejection ────────────────

func TestActivities_RecordTransition_RejectsMalformedEpochID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tracker, cache := newUnifiedTrackerForTest(t)

	acts := &temporal.Activities{
		Tracker:         tracker,
		WellKnownAgents: cache,
	}
	env := newTestActivityEnv(t, acts)

	rec := types.TransitionRecord{
		FromPhase:    protocol.PhaseRequest,
		ToPhase:      protocol.PhaseElicit,
		Timestamp:    time.Now().UTC(),
		TriggeredBy:  "bad-actor",
		ConditionMet: "smuggled in",
		Success:      true,
	}
	_, err := env.ExecuteActivity(acts.RecordTransition, "not-a-task-id", rec)
	if err == nil {
		t.Fatal("RecordTransition: expected validation error for malformed epochID, got nil")
	}

	// Temporal's TestActivityEnvironment wraps the activity's returned error
	// in an *ActivityError → *ApplicationError chain (the original Go error
	// type is encoded into the Type() field on ApplicationError, NOT
	// preserved via Unwrap — see the Temporal SDK error.go doc comment).
	// The serialised error message carries the StructuredError.Error() output
	// (which is "<category>: <what>" — not the full plain-language Report).
	// We assert on:
	//
	//   1. "validation error" — the CategoryValidation marker (Category
	//      prefix on .Error()).
	//   2. The plain-English What sentence from validateEpochID — the
	//      load-bearing user-visible substring from §11 Scenario 13.
	//   3. "type: StructuredError" — Temporal's record of the original Go
	//      error type (lets workflow callers detect the wrapped shape via
	//      applicationErr.Type() in production).
	//
	// The Fix field assertion ("pasture task create REQUEST") lives in
	// TestEpochStart_MalformedEpochID_Rejected (handlers package) and the
	// CLI subprocess test (cmd/pasture-msg/main_test.go) — both inspect the
	// raw *StructuredError BEFORE Temporal serialises it through the activity
	// boundary, so all fields are visible via Report.
	msg := err.Error()
	if !strings.Contains(msg, "validation error") {
		t.Errorf("error message %q missing 'validation error' (CategoryValidation marker)", msg)
	}
	if !strings.Contains(msg, "The epoch ID \"not-a-task-id\" is not valid.") {
		t.Errorf("error message %q missing the plain-language What sentence (with the bad ID surfaced)", msg)
	}
	if !strings.Contains(msg, "type: StructuredError") {
		t.Errorf("error chain %q does not record 'type: StructuredError' (Temporal-side type identifier — workflow callers use applicationErr.Type() to detect the wrapped shape)", msg)
	}

	// Should not: any row leaks to audit_events / context_edges / tasks for
	// the malformed ID (Scenario 13 "Should not" clause).
	tlEvents, qerr := tracker.Timeline(ctx, protocol.ContextEpoch, "not-a-task-id")
	if qerr != nil {
		t.Fatalf("Timeline: %v", qerr)
	}
	if len(tlEvents) != 0 {
		t.Errorf("Timeline(EpochContext, %q) returned %d events; want 0 (no row should leak for malformed epoch_id)", "not-a-task-id", len(tlEvents))
	}

	qEvents, qerr := tracker.QueryEvents(ctx, "not-a-task-id", nil, nil)
	if qerr != nil {
		t.Fatalf("QueryEvents: %v", qerr)
	}
	if len(qEvents) != 0 {
		t.Errorf("QueryEvents(%q) returned %d events; want 0 (no row should leak)", "not-a-task-id", len(qEvents))
	}
}

// ─── Scenario 13 activity-level: malformed epochID rejection at RecordAuditEvent ─

func TestActivities_RecordAuditEvent_RejectsMalformedEpochID(t *testing.T) {
	t.Parallel()
	tracker, cache := newUnifiedTrackerForTest(t)

	acts := &temporal.Activities{
		Tracker:         tracker,
		WellKnownAgents: cache,
	}
	env := newTestActivityEnv(t, acts)

	ev := protocol.AuditEvent{
		EpochID:   "not-a-task-id",
		Phase:     protocol.PhaseRequest,
		EventType: protocol.EventVoteRecorded,
		Payload:   map[string]any{},
		Timestamp: time.Now().UTC(),
	}
	_, err := env.ExecuteActivity(acts.RecordAuditEvent, ev)
	if err == nil {
		t.Fatal("RecordAuditEvent: expected validation error for malformed epochID, got nil")
	}
	// See TestActivities_RecordTransition_RejectsMalformedEpochID for the
	// rationale on asserting against the serialised error message instead of
	// errors.As (Temporal does not preserve the original Go error type
	// through the activity-boundary wrap).
	msg := err.Error()
	if !strings.Contains(msg, "validation error") {
		t.Errorf("error message %q missing 'validation error' (CategoryValidation marker)", msg)
	}
	if !strings.Contains(msg, "The epoch ID \"not-a-task-id\" is not valid.") {
		t.Errorf("error message %q missing the plain-language What sentence (with the bad ID surfaced)", msg)
	}
}

// ─── Scenario 2: R13 SearchAttribute snapshot ───────────────────────────────

// TestEpochWorkflow_SearchAttributes_R13Snapshot is the byte-identical
// preservation test for the Temporal SearchAttribute dual-write at the
// workflow boundary. PROPOSAL-2 §7.11 is BINDING: the
// workflow.UpsertTypedSearchAttributes call sites in
// internal/temporal/workflow.go MUST stay unchanged across S8.
//
// The test asserts on the source file directly because:
//
//   - The Temporal Go SDK's testsuite environment does not surface SAs from
//     workflow history through a public API; we can only inspect them via
//     the operator service against a real Temporal cluster, which is out of
//     scope for unit tests.
//
//   - The "byte-identical" wording in §7.11 / §11 Scenario 2 is a SOURCE-LEVEL
//     binding — the workflow code must still call UpsertTypedSearchAttributes
//     with the same six SA keys in the same order, with the same value-set
//     expressions. A source-snapshot test catches any refactor (rename, add,
//     remove, reorder) that would change the wire format.
//
// The expected snapshot is the bytes of the two UpsertTypedSearchAttributes
// blocks captured at S8 entry (commit 2a064b1 + ff4d703 + c62f855 + 9af3f9d
// state). If a future change adds, removes, or reorders SA keys, this test
// must be updated alongside that change AND verified by an operator against a
// real Temporal cluster (per §11 Scenario 2's "snapshot diff against captured
// pre-implementation SA values").
func TestEpochWorkflow_SearchAttributes_R13Snapshot(t *testing.T) {
	t.Parallel()
	// Find the workflow.go source file relative to this test (we are in
	// internal/temporal/, so workflow.go is in the same directory).
	src, err := os.ReadFile("workflow.go")
	if err != nil {
		t.Fatalf("read workflow.go: %v", err)
	}
	got := string(src)

	// Snapshot 1: initial-state SA upsert. Six keys (immutable EpochID set
	// once + the four phase-keyed ones).
	wantInitial := `if err := workflow.UpsertTypedSearchAttributes(ctx,
		saEpochIDKey.ValueSet(input.EpochID),
		saPhaseKey.ValueSet(string(initialPhase)),
		saRoleKey.ValueSet(string(w.sm.State().CurrentRole)),
		saStatusKey.ValueSet("running"),
		saDomainKey.ValueSet(domain),
	)`
	if !strings.Contains(got, wantInitial) {
		t.Errorf("R13 snapshot mismatch: workflow.go does not contain the expected initial UpsertTypedSearchAttributes block.\n\nWant (substring):\n%s\n\nIf the SA upsert was intentionally changed, update this snapshot AND verify the new wire format against a real Temporal cluster (PROPOSAL-2 §11 Scenario 2 'byte-identical' binding).", wantInitial)
	}

	// Snapshot 2: per-transition SA upsert. Five keys (EpochID is immutable
	// and not re-upserted; LastEventType is added).
	wantTransition := `if upsertErr := workflow.UpsertTypedSearchAttributes(ctx,
			saPhaseKey.ValueSet(string(current)),
			saRoleKey.ValueSet(string(w.sm.State().CurrentRole)),
			saStatusKey.ValueSet(status),
			saDomainKey.ValueSet(phaseDomain[current]),
			saLastEventKey.ValueSet(string(protocol.EventPhaseTransition)),
		)`
	if !strings.Contains(got, wantTransition) {
		t.Errorf("R13 snapshot mismatch: workflow.go does not contain the expected per-transition UpsertTypedSearchAttributes block.\n\nWant (substring):\n%s", wantTransition)
	}

	// Snapshot 3: the typed SA key declarations at file top (paranoid — if
	// these change, the value types change and the wire format changes).
	wantKeys := []string{
		`saEpochIDKey   = temporalsdk.NewSearchAttributeKeyString(SAEpochID)`,
		`saPhaseKey     = temporalsdk.NewSearchAttributeKeyKeyword(SAPhase)`,
		`saRoleKey      = temporalsdk.NewSearchAttributeKeyKeyword(SARole)`,
		`saStatusKey    = temporalsdk.NewSearchAttributeKeyKeyword(SAStatus)`,
		`saDomainKey    = temporalsdk.NewSearchAttributeKeyKeyword(SADomain)`,
		`saLastEventKey = temporalsdk.NewSearchAttributeKeyKeyword(SALastEventType)`,
	}
	for _, k := range wantKeys {
		if !strings.Contains(got, k) {
			t.Errorf("R13 snapshot mismatch: workflow.go missing typed SA key declaration:\n%s", k)
		}
	}
}
