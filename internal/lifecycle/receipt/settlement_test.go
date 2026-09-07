package receipt_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	claudefrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	claudeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/middleend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/nativeresponse"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/provenance"
)

// This adapter always calls the real journal. It can hold its successful
// return AFTER SQLite COMMIT, or inject a duplicate task effect that makes
// the real transaction roll back after the receipt effects. Neither arm is
// a Reader or a fabricated committed result.
type settlementJournal struct {
	provenance.ContextJournal
	committed chan struct{}
	release   chan struct{}
	duplicate *provenance.TaskID
}

func (j settlementJournal) ApplyContext(ctx context.Context, input provenance.OperationInput) (provenance.CommittedResult, error) {
	if j.duplicate != nil {
		input.Effects = append(append([]provenance.Effect(nil), input.Effects...), provenance.Effect{
			Sort:     provenance.EffectTaskCreate,
			TaskID:   *j.duplicate,
			Title:    "duplicate existing bootstrap task",
			Type:     provenance.TaskTypeTask,
			Priority: provenance.PriorityMedium,
			Phase:    provenance.PhaseUnscoped,
		})
	}
	result, err := j.ContextJournal.ApplyContext(ctx, input)
	if err == nil && j.committed != nil {
		close(j.committed)
		<-j.release
	}
	return result, err
}

func TestReceiptSettlementCoversRealCommitReturnGapCancellationAndRollback(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"commit-return-gap", "expiry-before-entry", "real-rollback"} {
		t.Run(scenario, func(t *testing.T) {
			// Real scratch store, persisted system identity and production service.
			dbPath := filepath.Join(t.TempDir(), "pasture.db")
			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			defer tracker.Close()
			bootstrap, err := tracker.Create("file://settlement-proof", "bootstrap", "initialize system identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
			require.NoError(t, err)
			service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, gateTestClock{}, &gateTestOperations{}, timeouts.TestProfile())
			require.NoError(t, err)
			_, err = receipt.EnsureActiveMetamodel(context.Background(), service)
			require.NoError(t, err)

			// Authentic ingress and genuine middle-end effects. The supplied Deny
			// proves commit/transport mechanics, NOT assignment/Reader policy.
			manifest := registration.ClaudeCode2_1_261()
			var event registration.Event
			for _, candidate := range manifest.Events {
				if candidate.NativeName == "PreToolUse" {
					event = candidate
				}
			}
			require.NotZero(t, event.Kind)
			raw, err := os.ReadFile("../ingress/claude/testdata/fixtures/pre_tool_use_2_1_261.json")
			require.NoError(t, err)
			capture := claudeingress.Parse(raw, event, manifest.Version, model.OccurrenceEnvelopeRef{})
			require.Equal(t, model.CaptureValid, capture.Disposition)
			binding, identities, err := claudefrontend.Bind(capture.Delivery.Event, capture.Delivery.Bindings)
			require.NoError(t, err)
			l2, err := binding.NewEvent(identities)
			require.NoError(t, err)
			derived, err := middleend.Derive(l2, metamodel.Active())
			require.NoError(t, err)
			decision, err := backend.NewDecision(backend.DecisionDeny, backend.ReasonNoActiveAssignment)
			require.NoError(t, err)
			effects, err := receipt.ReplaceConsultationDecision(derived.Effects(), decision)
			require.NoError(t, err)
			response, err := backend.NewHostResponse(decision)
			require.NoError(t, err)
			mapping, err := runtime.ClaudeCode2_1_261Lifecycle().Mapping(runtime.ClaudeEventPreToolUse)
			require.NoError(t, err)
			want, err := nativeresponse.EncodeClaude(mapping, response)
			require.NoError(t, err)
			intent, refusal := gate.NewDeliveryIntent(capture.Delivery.Contract, capture.Delivery.Event)
			require.Nil(t, refusal)
			warrant, refusal := gate.Legalize(intent)
			require.Nil(t, refusal)
			fence := receipt.NewCommitSettlement()
			service.Settlement = fence
			service.Outcome = want

			query := provenance.EvidenceQuery{
				Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
				Kinds:  []provenance.EvidenceKind{receipt.CurrentConsultationEvidenceKind(), "pasture.lifecycle.interpreted.v2"},
				Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
			}
			if scenario == "expiry-before-entry" {
				// Even a late caller with a live context cannot enter after expiry
				// has chosen fault. This tests admission, not just ctx.Err().
				<-fence.Expire()
				_, err := service.Receive(context.Background(), warrant, capture.Delivery, effects...)
				require.Error(t, err)
				_, committed := fence.CommittedOutcome()
				require.False(t, committed)
				page, err := tracker.Journal().Facts().QueryEvidence(query)
				require.NoError(t, err)
				require.Empty(t, page.Rows, "expiry must prevent a later consultation commit")
				return
			}

			journal, ok := tracker.Journal().(provenance.ContextJournal)
			require.True(t, ok)
			if scenario == "real-rollback" {
				service.Appender.Journal = settlementJournal{ContextJournal: journal, duplicate: &bootstrap.ID}
				_, err := service.Receive(context.Background(), warrant, capture.Delivery, effects...)
				require.Error(t, err)
				<-fence.Expire()
				_, committed := fence.CommittedOutcome()
				require.False(t, committed, "a failed real write must not publish Deny")
				page, err := tracker.Journal().Facts().QueryEvidence(query)
				require.NoError(t, err)
				require.Empty(t, page.Rows, "receipt effects must roll back with the failed operation")
				return
			}

			// Hold the actual successful Apply return. The real rows are already
			// readable while the production Service has not received the result.
			held := make(chan struct{})
			release := make(chan struct{})
			service.Appender.Journal = settlementJournal{ContextJournal: journal, committed: held, release: release}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				_, err := service.Receive(ctx, warrant, capture.Delivery, effects...)
				finished <- err
			}()
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			select {
			case <-held:
			case err := <-finished:
				t.Fatalf("append ended before the hold: %v", err)
			case <-time.After(time.Minute):
				t.Fatal("real append did not reach its return barrier")
			}
			page, err := tracker.Journal().Facts().QueryEvidence(query)
			require.NoError(t, err)
			require.Len(t, page.Rows, 2)
			var storedInterpreted, storedConsultation provenance.Effect
			for _, row := range page.Rows {
				effect := provenance.Effect{Sort: provenance.EffectEvidence, EvidenceKind: row.EvidenceKind, Payload: row.Payload, ContentDigest: row.ContentDigest}
				if row.EvidenceKind == receipt.CurrentConsultationEvidenceKind() {
					effect.ResultSlot = "consultation"
					storedConsultation = effect
				} else {
					effect.ResultSlot = "interpreted"
					storedInterpreted = effect
				}
			}
			stored, err := receipt.DecodeConsultation(storedConsultation, storedInterpreted)
			require.NoError(t, err)
			storedDecision, evaluated := stored.Decision()
			require.True(t, evaluated)
			require.Equal(t, decision, storedDecision)

			cancel()
			settled := fence.Expire()
			select {
			case <-settled:
				t.Fatal("expiry escaped the real COMMIT-to-Apply-return gap")
			default:
			}
			_, published := fence.CommittedOutcome()
			require.False(t, published, "a candidate is not published before Apply returns")
			close(release)
			select {
			case <-settled:
			case <-time.After(time.Minute):
				t.Fatal("settlement did not publish after releasing Apply")
			}
			require.NoError(t, <-finished)
			got, published := fence.CommittedOutcome()
			require.True(t, published)
			require.Equal(t, want, got, "expiry must preserve the full committed Deny Outcome")
		})
	}
}
