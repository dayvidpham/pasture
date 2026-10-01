package handlers_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
)

type fixedLifecycleClock struct{}

func (fixedLifecycleClock) Now() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

type fixedLifecycleOperations struct{ id string }

func (s fixedLifecycleOperations) NewOperationID() (string, error) { return s.id, nil }

type failedAfterCommit struct{ cause error }

func (b failedAfterCommit) AfterCommit(context.Context, handlers.CommitBoundary) error {
	return b.cause
}

// The gate-proof claim seeder and the capture session reader live in the internal
// test file hook_lifecycle_gate_test.go, exported as handlers.ClaimGateSession
// and handlers.GateSessionIdentity. They have one home so the claim this file
// seeds cannot drift from the claim the gate subjects are proven against.

func TestNativePostCommitFailurePreservesBothOutcomeAndError(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	bootstrap, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	_, err = bootstrap.Create("file://post-commit-error", "bootstrap", "initialize ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	require.NoError(t, bootstrap.Close())
	raw, err := os.ReadFile("../lifecycle/ingress/claude/testdata/fixtures/pre_tool_use_2_1_261.json")
	require.NoError(t, err)
	handlers.ClaimGateSession(t, dbPath, ir.HarnessClaudeCode, handlers.GateSessionIdentity(t, raw, "session_id"))
	cause := errors.New("post-commit observer failed")

	outcome, err := handlers.HookLifecycleNative(context.Background(), handlers.HookLifecycleInput{
		DBPath:      dbPath,
		Harness:     ir.HarnessClaudeCode,
		Event:       "PreToolUse",
		HostVersion: registration.ClaudeCode2_1_261().Version,
		Input:       bytes.NewReader(raw),
		Clock:       fixedLifecycleClock{},
		Operations:  fixedLifecycleOperations{id: "test.post-commit-error"},
		Barrier:     failedAfterCommit{cause: cause},
	})

	require.ErrorIs(t, err, cause)
	require.ErrorIs(t, err, handlers.ErrLifecycleCommittedWithoutContinuation)
	require.Equal(t, hostexit.ExitBlock, outcome.Exit)
	require.Empty(t, outcome.Stdout)
	require.Equal(t, backend.ReasonNoActiveAssignment.Message(), outcome.Stderr,
		"the refusal the host reads is the one the policy decided from the store, not one a caller supplied")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	consultation := queryOneEvidence(t, tracker, receipt.CurrentConsultationEvidenceKind())
	require.Contains(t, string(consultation.Payload), `"decision":{"decision":"deny","reason":"no-active-assignment"}`)
}

// This proves agreement for a REAL policy denial, not assignment-derived
// authority supplied by a caller. Neither real mapping has an evidenced refusal
// channel, so the durable reason must explain why the host continues.
func TestNativeUnenforcedDecisionAgreesWithDurableConsultation(t *testing.T) {
	codexMapping, err := runtime.Codex0_153_0Lifecycle().Mapping(runtime.CodexEventPreToolUse)
	require.NoError(t, err)
	openCodeMapping, err := runtime.OpenCode1_18_29Lifecycle().Mapping(runtime.OpenCodeEventToolExecuteBefore)
	require.NoError(t, err)
	for _, route := range []struct {
		harness         ir.HarnessID
		mapping         runtime.LifecycleEventMapping
		manifest        registration.Manifest
		fixture, stdout string
		sessionMembers  []string
		activations     func() ([]activation.Entry, error)
	}{
		{ir.HarnessCodex, codexMapping, registration.Codex0_153_0(), "codex/testdata/fixtures/pre_tool_use_0_153_0.json", `{"continue":true}`, []string{"session_id"}, activation.Codex0_153_0},
		{ir.HarnessOpenCode, openCodeMapping, registration.OpenCode1_18_29(), "opencode/testdata/fixtures/tool_execute_before_1_18_29.json", `{"decision":"proceed"}`, []string{"input.sessionID"}, activation.OpenCode1_18_29},
	} {
		t.Run(string(route.harness), func(t *testing.T) {
			require.Equal(t, runtime.CapabilityNone, route.mapping.Response())
			require.True(t, route.mapping.PreAction())
			require.Equal(t, runtime.SemanticGateConsultation, route.mapping.Semantic())
			require.Empty(t, route.mapping.Evidence().Source)
			require.Empty(t, route.mapping.AskEvidence().Source)
			entries, err := route.activations()
			require.NoError(t, err)
			event, err := ingress.EventByNativeName(route.manifest, route.mapping.NativeName())
			require.NoError(t, err)
			admitted := false
			for _, entry := range entries {
				if entry.Event == event.Kind {
					require.True(t, entry.IsValid())
					require.Equal(t, activation.Enabled, entry.State)
					admitted = true
				}
			}
			require.True(t, admitted, "actual activation catalog must admit the supplied fixture")
			raw, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", route.fixture))
			require.NoError(t, err)
			t.Run("bound-actor-with-no-assignment", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
				t.Setenv("PASTURE_DB_PATH", dbPath)
				bootstrap, err := tasks.OpenTaskTracker(dbPath)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, bootstrap.Close()) })
				_, err = bootstrap.Create("file://unenforced-decision", "bootstrap", "initialize ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
				require.NoError(t, err)
				require.NoError(t, bootstrap.Close())
				// The denial is now PRODUCED: a bound session whose registered
				// actor owns nothing is Deny(NoActiveAssignment), and a row with
				// no response capability carries it as Proceed/UnenforcedDeny.
				handlers.ClaimGateSession(t, dbPath, route.harness, handlers.GateSessionIdentity(t, raw, route.sessionMembers...))
				operation := "test.unenforced-decision"
				outcome, err := handlers.HookLifecycleNative(context.Background(), handlers.HookLifecycleInput{
					DBPath: dbPath, Harness: route.harness, Event: route.mapping.NativeName(), HostVersion: route.manifest.Version,
					Input: bytes.NewReader(raw), Clock: fixedLifecycleClock{}, Operations: fixedLifecycleOperations{id: operation},
				})
				require.NoError(t, err, "a valid but unenforceable decision is not a fault")
				require.Equal(t, hostexit.ExitContinue, outcome.Exit)
				require.Equal(t, route.stdout, string(outcome.Stdout))
				require.Empty(t, outcome.Stderr)
				tracker, err := tasks.OpenTaskTracker(dbPath)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, tracker.Close()) })
				page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
					Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
					Kinds:  []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1", "pasture.lifecycle.interpreted.v2", receipt.CurrentConsultationEvidenceKind()},
					Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
				})
				require.NoError(t, err)
				require.Nil(t, page.Next)
				require.Len(t, page.Rows, 3, "exactly one ordinary occurrence, interpretation and current consultation must commit")
				rows := make(map[provenance.EvidenceKind]provenance.EvidenceRow)
				for _, row := range page.Rows {
					require.Equal(t, provenance.OperationID(operation), row.ProducingOperationID)
					sum := sha256.Sum256(row.Payload)
					require.Equal(t, sum[:], row.ContentDigest)
					rows[row.EvidenceKind] = row
				}
				require.Len(t, rows, 3)
				occurrence := rows["pasture.lifecycle.occurrence.v1"]
				interpreted := rows["pasture.lifecycle.interpreted.v2"]
				consultation := rows[receipt.CurrentConsultationEvidenceKind()]
				require.NotZero(t, occurrence.ProducingOperationJournalID)
				require.Equal(t, occurrence.ProducingOperationJournalID, interpreted.ProducingOperationJournalID)
				require.Equal(t, interpreted.ProducingOperationJournalID, consultation.ProducingOperationJournalID)
				require.Less(t, occurrence.JournalID, interpreted.JournalID)
				require.Less(t, interpreted.JournalID, consultation.JournalID)
				var captured struct {
					Capture model.CaptureDisposition `json:"capture"`
					Body    string                   `json:"body_digest"`
				}
				require.NoError(t, json.Unmarshal(occurrence.Payload, &captured))
				require.Equal(t, model.CaptureValid, captured.Capture)
				require.Equal(t, fmt.Sprintf("sha256:%x", sha256.Sum256(raw)), captured.Body)
				// EvidenceRow omits result slots; reconstruct only the documented
				// effect slots. The decoder validates the payload's exact reference
				// and digest against the actual linked interpreted evidence.
				decoded, err := receipt.DecodeConsultation(provenance.Effect{
					Sort: provenance.EffectEvidence, ResultSlot: "consultation", EvidenceKind: consultation.EvidenceKind, Payload: consultation.Payload, ContentDigest: consultation.ContentDigest,
				}, provenance.Effect{
					Sort: provenance.EffectEvidence, ResultSlot: "interpreted", EvidenceKind: interpreted.EvidenceKind, Payload: interpreted.Payload, ContentDigest: interpreted.ContentDigest,
				})
				require.NoError(t, err)
				require.Equal(t, receipt.CurrentConsultationEvidenceKind(), decoded.Effect().EvidenceKind)
				stored, evaluated := decoded.Decision()
				require.True(t, evaluated, "current consultation must contain an evaluated decision")
				require.Equal(t, backend.DecisionProceed, stored.Kind(), "durable decision must agree with the host continuation")
				require.Equal(t, backend.ReasonUnenforcedDeny, stored.Reason(), "durable reason must retain the unenforced refusal, not claim legality")
			})
		})
	}
}

type readTrackingLifecycleInput struct{ reads int }

func (r *readTrackingLifecycleInput) Read([]byte) (int, error) {
	r.reads++
	return 0, fmt.Errorf("test input must not be read before activation admission")
}

func TestHookLifecycleResponseRejectsWithheldOpenCodeBeforeInputAndStorage(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())
	input := &readTrackingLifecycleInput{}
	response, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
		DBPath: dbPath, Harness: ir.HarnessOpenCode, Event: "session.updated", HostVersion: registration.OpenCode1_18_29().Version,
		Input: input, Clock: fixedLifecycleClock{}, Operations: fixedLifecycleOperations{id: "test.withheld"},
	})
	require.ErrorContains(t, err, `OpenCode event "session.updated" is withheld (reason outside-target-set)`)
	require.False(t, response.IsValid())
	require.Zero(t, input.reads, "withheld event must be rejected before stdin access")
	_, statErr := os.Stat(dbPath)
	require.ErrorIs(t, statErr, os.ErrNotExist, "withheld event must be rejected before storage access")
}

func TestHookLifecycleResponseRejectsUncorrelatedClaudeElicitationBeforeInputAndStorage(t *testing.T) {
	t.Parallel()
	for _, event := range []string{"Elicitation", "ElicitationResult"} {
		event := event
		t.Run(event, func(t *testing.T) {
			t.Parallel()
			dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())
			input := &readTrackingLifecycleInput{}
			response, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
				DBPath: dbPath, Harness: ir.HarnessClaudeCode, Event: event, HostVersion: "2.1.261",
				Input: input, Clock: fixedLifecycleClock{}, Operations: fixedLifecycleOperations{id: "test.withheld.claude"},
			})
			require.ErrorContains(t, err, `Claude event "`+event+`" is withheld (reason missing-request-correlation)`)
			require.False(t, response.IsValid(), "withheld elicitation must emit no host response")
			require.Zero(t, input.reads, "withheld elicitation must be rejected before stdin access")
			_, statErr := os.Stat(dbPath)
			require.ErrorIs(t, statErr, os.ErrNotExist, "withheld elicitation must be rejected before storage access")
		})
	}
}

func TestHookLifecycleResponseOpenCodeCommitsBeforeReturning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, fixture, event string
		wantResponse         bool
		wantEvidence         []provenance.EvidenceKind
	}{
		{name: "observation", fixture: "session_created_1_18_29.json", event: "session.created", wantEvidence: []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1", "pasture.lifecycle.interpreted.v2"}},
		{name: "gate", fixture: "tool_execute_before_1_18_29.json", event: "tool.execute.before", wantResponse: true, wantEvidence: []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1", "pasture.lifecycle.interpreted.v2", receipt.CurrentConsultationEvidenceKind()}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "opencode", "testdata", "fixtures", test.fixture))
			require.NoError(t, err)
			dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
			bootstrap, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			_, err = bootstrap.Create("file://handler-test", "bootstrap", "initialize lifecycle system identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
			require.NoError(t, err)
			require.NoError(t, bootstrap.Close())
			response, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
				DBPath: dbPath, Harness: ir.HarnessOpenCode, Event: test.event, HostVersion: registration.OpenCode1_18_29().Version,
				Input: bytes.NewReader(raw), Clock: fixedLifecycleClock{}, Operations: fixedLifecycleOperations{id: "test." + test.name},
			})
			require.NoError(t, err)
			require.Equal(t, test.wantResponse, response.IsValid())
			if test.wantResponse {
				require.Equal(t, backend.DecisionProceed, response.Decision())
				require.JSONEq(t, `{"decision":"proceed"}`, string(requireMarshalResponse(t, response)))
			}

			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			defer tracker.Close()
			page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
				Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
				Kinds:  test.wantEvidence, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
			})
			require.NoError(t, err)
			require.Len(t, page.Rows, len(test.wantEvidence), "returning from the handler must imply every expected effect is already readable")
			for _, row := range page.Rows {
				require.Equal(t, provenance.OperationID("test."+test.name), row.ProducingOperationID)
			}
			if test.wantResponse {
				occurrence := queryOneEvidence(t, tracker, "pasture.lifecycle.occurrence.v1")
				interpreted := queryOneEvidence(t, tracker, "pasture.lifecycle.interpreted.v2")
				consultation := queryOneEvidence(t, tracker, receipt.CurrentConsultationEvidenceKind())
				require.Equal(t, occurrence.ProducingOperationJournalID, interpreted.ProducingOperationJournalID)
				require.Equal(t, interpreted.ProducingOperationJournalID, consultation.ProducingOperationJournalID)
				require.Less(t, interpreted.JournalID, consultation.JournalID, "interpreted evidence must precede consultation evidence in the committed operation")
			}
		})
	}
}

func queryOneEvidence(t *testing.T, tracker interface{ Journal() provenance.Journal }, kind provenance.EvidenceKind) provenance.EvidenceRow {
	t.Helper()
	page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  []provenance.EvidenceKind{kind}, Page: provenance.FactPageRequest{Limit: 1},
	})
	require.NoError(t, err)
	require.Len(t, page.Rows, 1)
	return page.Rows[0]
}

func TestHookLifecycleResponsePersistenceFailureReturnsNoResponse(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "opencode", "testdata", "fixtures", "tool_execute_before_1_18_29.json"))
	require.NoError(t, err)
	directory := filepath.Join(t.TempDir(), "database-directory")
	require.NoError(t, os.Mkdir(directory, 0o700))
	response, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
		DBPath: directory, Harness: ir.HarnessOpenCode, Event: "tool.execute.before", HostVersion: registration.OpenCode1_18_29().Version,
		Input: bytes.NewReader(raw), Clock: fixedLifecycleClock{}, Operations: fixedLifecycleOperations{id: "test.failure"},
	})
	require.Error(t, err)
	require.False(t, response.IsValid(), "a response must not escape when persistence fails")
}

func requireMarshalResponse(t *testing.T, response backend.HostResponse) []byte {
	t.Helper()
	raw, err := json.Marshal(response)
	require.NoError(t, err)
	return raw
}
