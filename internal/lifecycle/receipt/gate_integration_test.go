package receipt_test

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	claudeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/provenance"
)

type gateTestClock struct{}

func (gateTestClock) Now() time.Time { return time.Unix(100, 0).UTC() }

type gateTestOperations struct{ n int }

func (o *gateTestOperations) NewOperationID() (string, error) {
	o.n++
	return "pasture.lifecycle.gate-test." + string(rune('a'+o.n)), nil
}

// TestReceiveGateRefusalLeavesStoreEmpty is the B-directive read-back-EMPTY
// proof on the REAL production write path. It wires the sole durable writer via
// the production constructor (tasks.NewLifecycleReceiptService) against a real
// unified store, presents an ungated and then a class-mismatched warrant to a
// genuine parsed delivery, asserts each is refused with a typed *gate.Refusal,
// and then REBUILDS the projection and queries it to prove nothing was written —
// not merely that an error was returned.
//
// The built CLI cannot itself emit a gate.Refusal on delivery because the
// handler always legalizes a well-formed delivery intent; the gate.Refusal is a
// write-plane guarantee exercised here against the exact production Receive with
// real dependencies (no test-only export, no mock store).
//
// FAILS against the L1 stub (Authorize permits, so the delivery commits and the
// read-back is non-empty) until L3.
func TestReceiveGateRefusalLeavesStoreEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pasture.db")

	// Bootstrap the persisted system identity so the store is fully functional
	// and a valid delivery WOULD commit — the read-back-empty result then proves
	// the gate refusal, not a broken store.
	bootstrap, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open bootstrap tracker: %v", err)
	}
	if _, err := bootstrap.Create("file://gate-integration-test", "bootstrap", "initialize the persisted ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
		t.Fatalf("bootstrap identity: %v", err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatalf("close bootstrap tracker: %v", err)
	}

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open tracker: %v", err)
	}
	defer tracker.Close()
	service, err := tasks.NewLifecycleReceiptService(tracker, gateTestClock{}, &gateTestOperations{})
	if err != nil {
		t.Fatalf("wire production receipt service: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join("..", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_210.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	delivery := claudeingress.Parse(raw, registration.ClaudeCode2_1_210().Events[0], "2.1.210", model.OccurrenceEnvelopeRef{}).Delivery

	t.Run("zero warrant", func(t *testing.T) {
		_, err := service.Receive(ctx, gate.Warrant{}, delivery)
		assertGateRefusal(t, err, gate.RefusalInvalidIntent)
	})
	t.Run("class-mismatched warrant", func(t *testing.T) {
		_, err := service.Receive(ctx, mismatchedWarrant(t), delivery)
		assertGateRefusal(t, err, gate.RefusalClassMismatch)
	})

	// Read back through the production projection: the refused writes must have
	// committed nothing.
	if err := tasks.RebuildLifecycleOccurrences(ctx, tracker); err != nil {
		t.Fatalf("rebuild occurrences: %v", err)
	}
	reader, err := tasks.NewLifecycleReader(tracker)
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	page, err := reader.Occurrences(ctx, model.OccurrenceQuery{Page: model.PageRequest{Size: model.MaxPageSize}})
	if err != nil {
		t.Fatalf("read occurrences: %v", err)
	}
	if got := len(page.Records()); got != 0 {
		t.Fatalf("store holds %d occurrence records after gate-refused writes, want 0 (read-back-empty)", got)
	}
}

func assertGateRefusal(t *testing.T, err error, wantReason gate.RefusalReason) {
	t.Helper()
	if err == nil {
		t.Fatal("Receive committed a gate-refused write; want a typed *gate.Refusal")
	}
	var refusal *gate.Refusal
	if !stderrors.As(err, &refusal) {
		t.Fatalf("Receive error = %v (%T), want *gate.Refusal", err, err)
	}
	if refusal.Reason() != wantReason {
		t.Fatalf("refusal reason = %s, want %s", refusal.Reason(), wantReason)
	}
}

func mismatchedWarrant(t *testing.T) gate.Warrant {
	t.Helper()
	intent, refusal := gate.NewLineageIntent(ir.HarnessClaudeCode, 1)
	if refusal != nil {
		t.Fatalf("build lineage intent: %v", refusal)
	}
	warrant, refusal := gate.Legalize(intent)
	if refusal != nil {
		t.Fatalf("legalize lineage intent: %v", refusal)
	}
	return warrant
}
