package receipt_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	claudeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/provenance"
)

// TestRawOriginOccurrenceReadsBackThroughProductionStore proves the raw
// ingestion origin carrier end-to-end through the PRODUCTION wiring: a REAL
// native fixture parsed by the production ingress parse path, stamped with the
// raw origin, committed through gate-warranted Receive on the
// tasks.NewLifecycleReceiptService production opener against a real
// bootstrapped store, then read back through the production projection rebuild
// and reader (the gate_integration_test.go real-store pattern applied to the
// origin carrier).
//
// The committed occurrence.v1 payload must carry origin "raw" in BOTH the
// receipt payload member and the envelope member (ratified UAT-Q4), the
// evidence kind must stay occurrence.v1 (no new raw kind), and the read-back
// must survive the tolerant projection decode.
func TestRawOriginOccurrenceReadsBackThroughProductionStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath, cleanup := newBootstrappedTracker(t)
	defer cleanup()

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open tracker: %v", err)
	}
	defer tracker.Close()
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, gateTestClock{}, &gateTestOperations{}, timeouts.TestProfile())
	if err != nil {
		t.Fatalf("wire production receipt service: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join("..", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_210.json"))
	if err != nil {
		t.Fatalf("read native fixture: %v", err)
	}
	delivery := claudeingress.Parse(raw, registration.ClaudeCode2_1_210().Events[0], registration.ClaudeCode2_1_210().Version, model.OccurrenceEnvelopeRef{}).Delivery
	delivery.Origin = acceptance.OriginRaw
	delivery.Envelope.Origin = acceptance.OriginRaw

	intent, refusal := gate.NewDeliveryIntent(delivery.Contract, delivery.Event)
	if refusal != nil {
		t.Fatalf("build delivery intent: %v", refusal)
	}
	warrant, refusal := gate.Legalize(intent)
	if refusal != nil {
		t.Fatalf("legalize delivery intent: %v", refusal)
	}
	if _, err := service.Receive(ctx, warrant, delivery); err != nil {
		t.Fatalf("Receive raw-origin delivery through production service: %v", err)
	}

	page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  []provenance.EvidenceKind{provenance.EvidenceKind("pasture.lifecycle.occurrence.v1")},
		Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
	})
	if err != nil {
		t.Fatalf("query committed occurrence evidence: %v", err)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("committed %d occurrence rows, want exactly 1", len(page.Rows))
	}
	var decoded struct {
		Envelope model.OccurrenceEnvelopeRef `json:"envelope"`
		Origin   acceptance.CaptureOrigin    `json:"origin"`
	}
	if err := json.Unmarshal(page.Rows[0].Payload, &decoded); err != nil {
		t.Fatalf("decode read-back occurrence: %v", err)
	}
	if decoded.Origin != acceptance.OriginRaw {
		t.Fatalf("read-back payload origin = %q, want %q (raw disclosed on receipt)", decoded.Origin, acceptance.OriginRaw)
	}
	if decoded.Envelope.Origin != acceptance.OriginRaw {
		t.Fatalf("read-back envelope origin = %q, want %q (raw disclosed on envelope)", decoded.Envelope.Origin, acceptance.OriginRaw)
	}
	if !decoded.Envelope.Origin.IsValid() {
		t.Fatalf("read-back origin %q is not a valid closed-enum member", decoded.Envelope.Origin)
	}

	// The disposable projection rebuild must decode the origin-carrying payload
	// tolerantly (unknown members at baseline are now known, and pre-origin
	// records without the member remain decodable), and the production reader
	// must surface the raw origin on the occurrence envelope.
	if err := tasks.RebuildLifecycleOccurrences(ctx, tracker); err != nil {
		t.Fatalf("rebuild occurrences: %v", err)
	}
	reader, err := tasks.NewLifecycleReader(tracker)
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	occurrences, err := reader.Occurrences(ctx, model.OccurrenceQuery{Page: model.PageRequest{Size: model.MaxPageSize}})
	if err != nil {
		t.Fatalf("read occurrences: %v", err)
	}
	records := occurrences.Records()
	if len(records) != 1 {
		t.Fatalf("projection holds %d occurrence records, want exactly 1", len(records))
	}
	if records[0].Envelope.Origin != acceptance.OriginRaw {
		t.Fatalf("projected envelope origin = %q, want %q (raw disclosed on projection read-back)", records[0].Envelope.Origin, acceptance.OriginRaw)
	}
	if records[0].Capture != model.CaptureValid {
		t.Fatalf("projected capture disposition = %v, want %v (existing disposition reused)", records[0].Capture, model.CaptureValid)
	}
}
