package receipt_test

// Production pins for DEFS-SLICE-3: both complete envelopes in production.
//
// Every test here drives the real production path — parsed ingress, genuine
// middle-end derivation, gate-warranted Receive through
// tasks.NewLifecycleReceiptService against a bootstrapped store — and asserts
// on committed journal truth plus validated reads (projection rebuild,
// Records, close/reopen, transaction replay). Nothing here names a path
// without crossing it: each pin fails when its behavior is neutered (see the
// red-verification notes), which the recurring review weakness in this change
// demands.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	claudefrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	claudeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/middleend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/internal/writelane"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
)

const (
	envelopeOccurrenceV2   = provenance.EvidenceKind("pasture.lifecycle.occurrence.v2")
	envelopeOccurrenceV1   = provenance.EvidenceKind("pasture.lifecycle.occurrence.v1")
	envelopeInterpretedV3  = provenance.EvidenceKind("pasture.lifecycle.interpreted.v3")
	envelopeInterpretedV2  = provenance.EvidenceKind("pasture.lifecycle.interpreted.v2")
	envelopeConsultationV2 = provenance.EvidenceKind("pasture.lifecycle.consultation.v2")
)

type envelopeService struct {
	tracker protocol.TaskTracker
	service receipt.Service
	dbPath  string
}

func openEnvelopeStore(t *testing.T) (context.Context, *envelopeService) {
	t.Helper()
	ctx := context.Background()
	dbPath, cleanup := newBootstrappedTracker(t)
	t.Cleanup(cleanup)
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open tracker: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, gateTestClock{}, &gateTestOperations{}, timeouts.TestProfile())
	if err != nil {
		t.Fatalf("wire production receipt service: %v", err)
	}
	if _, err := receipt.EnsureActiveMetamodel(ctx, service); err != nil {
		t.Fatalf("ensure active metamodel: %v", err)
	}
	return ctx, &envelopeService{tracker: tracker, service: service, dbPath: dbPath}
}

func envelopeWarrant(t *testing.T, delivery receipt.Delivery) gate.Warrant {
	t.Helper()
	intent, refusal := gate.NewDeliveryIntent(delivery.Contract, delivery.Event)
	if refusal != nil {
		t.Fatalf("delivery intent: %v", refusal)
	}
	warrant, refusal := gate.Legalize(intent)
	if refusal != nil {
		t.Fatalf("legalize: %v", refusal)
	}
	return warrant
}

func parseEnvelopeFixture(t *testing.T, name, event string) receipt.Delivery {
	t.Helper()
	manifest := registration.ClaudeCode2_1_261()
	var selected registration.Event
	for _, candidate := range manifest.Events {
		if candidate.NativeName == event {
			selected = candidate
		}
	}
	if selected.Kind == 0 {
		t.Fatalf("event %q not registered", event)
	}
	raw, err := os.ReadFile(filepath.Join("..", "ingress", "claude", "testdata", "fixtures", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	capture := claudeingress.Parse(raw, selected, manifest.Version, model.OccurrenceEnvelopeRef{})
	if capture.Disposition != model.CaptureValid {
		t.Fatalf("fixture %s disposition = %v, want valid", name, capture.Disposition)
	}
	return capture.Delivery
}

// preparedObservation derives the genuine prepared pair for an observation
// arm: one prepared interpreted effect and no consultation.
func preparedObservation(t *testing.T, delivery receipt.Delivery) []provenance.Effect {
	t.Helper()
	binding, identities, err := claudefrontend.Bind(delivery.Event, delivery.Bindings)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	l2, err := binding.NewEvent(identities)
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	derived, err := middleend.Derive(l2, metamodel.Active())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if len(derived.Effects()) != 1 {
		t.Fatalf("observation derivation effects = %d, want exactly one prepared interpretation", len(derived.Effects()))
	}
	return derived.Effects()
}

// preparedGate derives the genuine prepared pair for a gate arm with an
// evaluated verdict, exactly as the handler commit tail does.
func preparedGate(t *testing.T, delivery receipt.Delivery, kind backend.DecisionKind, reason backend.DecisionReason) []provenance.Effect {
	t.Helper()
	binding, identities, err := claudefrontend.Bind(delivery.Event, delivery.Bindings)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	l2, err := binding.NewEvent(identities)
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	derived, err := middleend.Derive(l2, metamodel.Active())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	decision, err := backend.NewDecision(kind, reason)
	if err != nil {
		t.Fatalf("decision: %v", err)
	}
	effects, err := receipt.ReplaceConsultationDecision(derived.Effects(), decision)
	if err != nil {
		t.Fatalf("replace decision: %v", err)
	}
	if len(effects) != 2 {
		t.Fatalf("gate derivation effects = %d, want the prepared interpreted/consultation pair", len(effects))
	}
	return effects
}

func queryEnvelopeKind(t *testing.T, tracker protocol.TaskTracker, kind provenance.EvidenceKind) []provenance.EvidenceRow {
	t.Helper()
	page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  []provenance.EvidenceKind{kind},
		Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
	})
	if err != nil {
		t.Fatalf("query %s: %v", kind, err)
	}
	if page.Next != nil {
		t.Fatalf("query %s paged, want one bounded page in these pins", kind)
	}
	return page.Rows
}

func decodeEnvelopeOccurrence(t *testing.T, payload []byte) occurrenceEnvelopePayload {
	t.Helper()
	var decoded occurrenceEnvelopePayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode occurrence payload: %v", err)
	}
	return decoded
}

type occurrenceEnvelopePayload struct {
	Contract string                      `json:"contract"`
	Event    model.ContractEventKind     `json:"event"`
	Envelope model.OccurrenceEnvelopeRef `json:"envelope"`
	Capture  model.CaptureDisposition    `json:"capture"`
}

func requireCompleteOccurrence(t *testing.T, envelope model.OccurrenceEnvelopeRef, contract ir.RuntimeContractID) {
	t.Helper()
	if err := model.ValidateOccurrenceEnvelope(envelope); err != nil {
		t.Fatalf("occurrence envelope incomplete: %v (envelope=%+v)", err, envelope)
	}
	if envelope.Runtime.Definition.Kind != model.DefinitionRuntimeContract ||
		envelope.Schema.Definition.Kind != model.DefinitionLifecycleSchema ||
		envelope.Implementation.Definition.Kind != model.DefinitionEpochImplementation ||
		envelope.Retention.Definition.Kind != model.DefinitionRetentionPolicy {
		t.Fatalf("occurrence envelope kinds wrong: %+v", envelope)
	}
	if envelope.Runtime.Contract != contract {
		t.Fatalf("occurrence runtime contract = %q, want delivery %q", envelope.Runtime.Contract, contract)
	}
}

func requireCompleteSemantic(t *testing.T, envelope model.SemanticEnvelopeRef, occurrence model.OccurrenceEnvelopeRef) {
	t.Helper()
	if err := model.ValidateSemanticEnvelope(envelope); err != nil {
		t.Fatalf("semantic envelope incomplete: %v", err)
	}
	if envelope.Runtime.Definition.Kind != model.DefinitionRuntimeContract ||
		envelope.Schema.Definition.Kind != model.DefinitionLifecycleSchema ||
		envelope.Metamodel.Definition.Kind != model.DefinitionMetamodel ||
		envelope.Interpreter.Definition.Kind != model.DefinitionInterpreter ||
		envelope.Implementation.Definition.Kind != model.DefinitionEpochImplementation {
		t.Fatalf("semantic envelope kinds wrong: %+v", envelope)
	}
	if !envelope.SharedRefsEqual(occurrence) {
		t.Fatalf("shared refs differ across the pair:\nsemantic=%+v\noccurrence=%+v", envelope, occurrence)
	}
}

// TestReceiveCommitsBothCompleteEnvelopes is the core production pin: a valid
// observation delivery commits occurrence.v2 with four nonzero refs plus
// interpreted.v3 with five nonzero refs and three exactly shared refs, every
// ref owner-resolvable, and both envelopes exposed through rebuild, Records,
// close/reopen, and the non-reclaiming transaction replay. Red-verified by
// neutering the emission (occurrence kind back to v1; finalizer returning
// record-only): every assertion below fails.
func TestReceiveCommitsBothCompleteEnvelopes(t *testing.T) {
	t.Parallel()
	ctx, store := openEnvelopeStore(t)
	delivery := parseEnvelopeFixture(t, "session_start_2_1_261.json", "SessionStart")
	prepared := preparedObservation(t, delivery)

	committed, err := store.service.Receive(ctx, envelopeWarrant(t, delivery), delivery, prepared...)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if committed.JournalID() == 0 {
		t.Fatal("Receive returned a zero occurrence identity")
	}

	occurrences := queryEnvelopeKind(t, store.tracker, envelopeOccurrenceV2)
	if len(occurrences) != 1 {
		t.Fatalf("occurrence.v2 rows = %d, want exactly one", len(occurrences))
	}
	occurrence := decodeEnvelopeOccurrence(t, occurrences[0].Payload)
	requireCompleteOccurrence(t, occurrence.Envelope, delivery.Contract)
	if occurrence.Capture != model.CaptureValid {
		t.Fatalf("occurrence capture = %v, want valid", occurrence.Capture)
	}

	interpreted := queryEnvelopeKind(t, store.tracker, envelopeInterpretedV3)
	if len(interpreted) != 1 {
		t.Fatalf("interpreted.v3 rows = %d, want exactly one", len(interpreted))
	}
	if interpreted[0].ProducingOperationID != occurrences[0].ProducingOperationID {
		t.Fatal("interpreted.v3 committed in a different operation than its occurrence; want one atomic pair")
	}
	decoded, err := receipt.DecodeInterpretedV3(1, 1, interpreted[0].Payload)
	if err != nil {
		t.Fatalf("DecodeInterpretedV3: %v", err)
	}
	semantic, ok := decoded.SemanticEnvelope()
	if !ok {
		t.Fatal("decoded v3 reports no semantic envelope")
	}
	requireCompleteSemantic(t, semantic, occurrence.Envelope)

	// No legacy emission on the new path: neither the all-zero v1 occurrence
	// nor a durable v2 interpretation may appear.
	if legacy := queryEnvelopeKind(t, store.tracker, envelopeOccurrenceV1); len(legacy) != 0 {
		t.Fatalf("occurrence.v1 rows = %d, want zero (new writer never emits v1)", len(legacy))
	}
	if legacy := queryEnvelopeKind(t, store.tracker, envelopeInterpretedV2); len(legacy) != 0 {
		t.Fatalf("interpreted.v2 rows = %d, want zero (prepared v2 finalizes to v3, never commits)", len(legacy))
	}

	// Every ref resolves through its real owner: the four occurrence refs and
	// the interpreter ref through the definition resolver, the metamodel ref
	// through the normalized bridge, each with nonzero content.
	reader, err := receipt.NewDefinitionReader(store.tracker.Journal())
	if err != nil {
		t.Fatalf("definition reader: %v", err)
	}
	for name, ref := range map[string]model.DefinitionRef{
		"runtime": occurrence.Envelope.Runtime.Definition, "schema": occurrence.Envelope.Schema.Definition,
		"implementation": occurrence.Envelope.Implementation.Definition, "retention": occurrence.Envelope.Retention.Definition,
		"interpreter": semantic.Interpreter.Definition,
	} {
		snapshot, state, err := receipt.ResolveDefinition(reader, ref)
		if err != nil {
			t.Fatalf("resolve %s ref: %v", name, err)
		}
		if len(snapshot.Body()) == 0 || ref.Content == (model.ContentIdentity{}) {
			t.Fatalf("resolved %s ref has empty body or content", name)
		}
		if snapshot.Body()[0] == 0 && len(snapshot.Body()) == 1 {
			t.Fatalf("resolved %s body is a placeholder", name)
		}
		_ = state
	}
	// The committed interpreter ref must identify the compiled artifact that
	// executed the derivation — the middle end pins it at Derive time and the
	// finalizer ensures that exact document, never a store-latest substitute.
	compiled, err := definitions.NewInterpreter()
	if err != nil {
		t.Fatalf("compiled interpreter: %v", err)
	}
	interpreterSnapshot, _, err := receipt.ResolveDefinition(reader, semantic.Interpreter.Definition)
	if err != nil {
		t.Fatalf("resolve interpreter ref: %v", err)
	}
	if !bytes.Equal(interpreterSnapshot.Body(), compiled.Body()) {
		t.Fatal("finalized interpreter ref does not identify the compiled derivation artifact")
	}
	metaSnapshot, _, err := receipt.ResolveMetamodelRef(reader, semantic.Metamodel.Definition, 0)
	if err != nil {
		t.Fatalf("resolve metamodel ref: %v", err)
	}
	if len(metaSnapshot.Body()) == 0 {
		t.Fatal("resolved metamodel body is empty")
	}
	bridged, _, _, err := receipt.BridgeActiveMetamodel(ctx, reader)
	if err != nil {
		t.Fatalf("bridge active metamodel: %v", err)
	}
	if bridged.Definition != semantic.Metamodel.Definition {
		t.Fatalf("bridged metamodel ref %+v != semantic ref %+v", bridged.Definition, semantic.Metamodel.Definition)
	}

	assertBothEnvelopesReadable(t, ctx, store, committed.JournalID(), delivery.Contract, true)
}

// assertBothEnvelopesReadable proves read/rebuild/reopen expose BOTH complete
// owner-resolved envelopes: projection rebuild, validated Records, payload
// retention agreement, close/reopen, and the non-reclaiming transaction replay
// with its comparison.
func assertBothEnvelopesReadable(t *testing.T, ctx context.Context, store *envelopeService, occurrence provenance.JournalID, contract ir.RuntimeContractID, wantInterpretation bool) {
	t.Helper()
	checkRecords := func(t *testing.T, tracker protocol.TaskTracker, label string) {
		t.Helper()
		if err := tasks.RebuildLifecycleOccurrences(ctx, tracker); err != nil {
			t.Fatalf("%s rebuild: %v", label, err)
		}
		reader, err := tasks.NewLifecycleReader(tracker)
		if err != nil {
			t.Fatalf("%s reader: %v", label, err)
		}
		size, err := model.NewPageSize(model.MaxPageSize)
		if err != nil {
			t.Fatalf("%s page size: %v", label, err)
		}
		page, err := reader.Records(ctx, model.OccurrenceQuery{Page: model.PageRequest{Size: size}})
		if err != nil {
			t.Fatalf("%s records: %v", label, err)
		}
		var found *model.LifecycleRecord
		for index := range page.Items {
			if page.Items[index].Occurrence.JournalID() == occurrence {
				found = &page.Items[index]
			}
		}
		if found == nil {
			t.Fatalf("%s: occurrence %d not exposed by validated read", label, occurrence)
		}
		requireCompleteOccurrence(t, found.Occurrence.Envelope, contract)
		if found.Occurrence.Payload.Retention.Definition != found.Occurrence.Envelope.Retention.Definition {
			t.Fatalf("%s: payload retention %+v != envelope retention %+v", label, found.Occurrence.Payload.Retention.Definition, found.Occurrence.Envelope.Retention.Definition)
		}
		interpreted := found.Interpreted()
		if wantInterpretation {
			if len(interpreted) != 1 {
				t.Fatalf("%s: interpretations = %d, want exactly one with a complete envelope", label, len(interpreted))
			}
			semantic, ok := interpreted[0].SemanticEnvelope()
			if !ok {
				t.Fatalf("%s: validated read hid the semantic envelope", label)
			}
			requireCompleteSemantic(t, semantic, found.Occurrence.Envelope)
		} else if len(interpreted) != 0 {
			t.Fatalf("%s: interpretations = %d, want zero for a record-only occurrence", label, len(interpreted))
		}
	}
	checkRecords(t, store.tracker, "rebuild/read")
	if err := store.tracker.Close(); err != nil {
		t.Fatalf("close tracker: %v", err)
	}
	reopened, err := tasks.OpenTaskTracker(store.dbPath)
	if err != nil {
		t.Fatalf("reopen tracker: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	checkRecords(t, reopened, "reopen/read")

	// The non-reclaiming transaction replay verifies both envelopes and the
	// consultation linkage on the same transaction view before any migration
	// marker, then the comparison proves the replayed projection equals the
	// converted journal truth.
	db, err := sql.Open("sqlite", store.dbPath)
	if err != nil {
		t.Fatalf("open sql handle: %v", err)
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	txReader, err := provenance.NewTransactionEvidenceReader(ctx, tx)
	if err != nil {
		t.Fatalf("transaction reader: %v", err)
	}
	if err := projection.RebuildOccurrencesInTx(ctx, tx, txReader); err != nil {
		t.Fatalf("transaction replay: %v", err)
	}
	if err := projection.CompareOccurrencesToEvidence(ctx, tx, txReader); err != nil {
		t.Fatalf("transaction comparison: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit tx: %v", err)
	}
}

// TestReceiveCommitsGatePairWithFinalConsultation proves the evaluated gate
// path finalizes the consultation against the FINAL v3 bytes: the committed
// consultation decodes against the committed v3, never against the prepared
// v2, and the evaluated verdict survives finalization unchanged.
// Red-verified by emitting the incoming consultation bytes unchanged: the
// prepared-v2 linkage assertion passes while the final-v3 one fails.
func TestReceiveCommitsGatePairWithFinalConsultation(t *testing.T) {
	t.Parallel()
	ctx, store := openEnvelopeStore(t)
	delivery := parseEnvelopeFixture(t, "pre_tool_use_2_1_261.json", "PreToolUse")
	prepared := preparedGate(t, delivery, backend.DecisionDeny, backend.ReasonNoActiveAssignment)

	if _, err := store.service.Receive(ctx, envelopeWarrant(t, delivery), delivery, prepared...); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	occurrences := queryEnvelopeKind(t, store.tracker, envelopeOccurrenceV2)
	interpreted := queryEnvelopeKind(t, store.tracker, envelopeInterpretedV3)
	consultations := queryEnvelopeKind(t, store.tracker, envelopeConsultationV2)
	if len(occurrences) != 1 || len(interpreted) != 1 || len(consultations) != 1 {
		t.Fatalf("rows = %d/%d/%d, want one occurrence.v2, one interpreted.v3 and one consultation", len(occurrences), len(interpreted), len(consultations))
	}
	operation := occurrences[0].ProducingOperationID
	if interpreted[0].ProducingOperationID != operation || consultations[0].ProducingOperationID != operation {
		t.Fatal("the pair did not commit in one existing delivery operation")
	}
	final := provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: provenance.ResultSlotID("interpreted"), EvidenceKind: envelopeInterpretedV3, ContentDigest: interpreted[0].ContentDigest, Payload: interpreted[0].Payload}
	finalized := provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: provenance.ResultSlotID("consultation"), EvidenceKind: envelopeConsultationV2, ContentDigest: consultations[0].ContentDigest, Payload: consultations[0].Payload}
	consultation, err := receipt.DecodeConsultation(finalized, final)
	if err != nil {
		t.Fatalf("committed consultation does not reference the FINAL v3 bytes: %v", err)
	}
	decision, ok := consultation.Decision()
	if !ok || decision.Kind() != backend.DecisionDeny || decision.Reason() != backend.ReasonNoActiveAssignment {
		t.Fatalf("finalized decision = %+v ok=%v, want the evaluated deny", decision, ok)
	}
	// The prepared v2 bytes the handler supplied must NOT anchor the committed
	// consultation: finalization re-emits the reference, never passes it through.
	if _, err := receipt.DecodeConsultation(finalized, prepared[0]); err == nil {
		t.Fatal("committed consultation still references the prepared v2 bytes; want reference finalization against v3")
	}
	assertBothEnvelopesReadable(t, ctx, store, occurrences[0].JournalID, delivery.Contract, true)
}

// TestReceiveRecordOnlyMalformedCommitsCompleteOccurrence proves malformed and
// unsupported captures commit a record-only complete occurrence.v2 with no
// fabricated semantic record, and that prepared effects supplied for a
// non-valid capture are refused outright rather than committed or dropped.
func TestReceiveRecordOnlyMalformedCommitsCompleteOccurrence(t *testing.T) {
	t.Parallel()
	ctx, store := openEnvelopeStore(t)
	delivery := parseEnvelopeFixture(t, "session_start_2_1_261.json", "SessionStart")
	delivery.Capture = model.CaptureMalformed

	before := len(queryEnvelopeKind(t, store.tracker, envelopeOccurrenceV2))
	committed, err := store.service.Receive(ctx, envelopeWarrant(t, delivery), delivery)
	if err != nil {
		t.Fatalf("Receive malformed: %v", err)
	}
	occurrences := queryEnvelopeKind(t, store.tracker, envelopeOccurrenceV2)
	if len(occurrences) != before+1 {
		t.Fatalf("occurrence.v2 rows = %d, want one new record-only occurrence", len(occurrences))
	}
	occurrence := decodeEnvelopeOccurrence(t, occurrences[len(occurrences)-1].Payload)
	requireCompleteOccurrence(t, occurrence.Envelope, delivery.Contract)
	if occurrence.Capture != model.CaptureMalformed {
		t.Fatalf("occurrence capture = %v, want malformed recorded honestly", occurrence.Capture)
	}
	for _, kind := range []provenance.EvidenceKind{envelopeInterpretedV3, envelopeInterpretedV2, envelopeConsultationV2} {
		if rows := queryEnvelopeKind(t, store.tracker, kind); len(rows) != 0 {
			t.Fatalf("kind %s rows = %d, want zero: no semantic record for a malformed capture", kind, len(rows))
		}
	}

	// Prepared effects for a non-valid capture are refused before any I/O, so
	// no semantic row can attach to a capture that has no interpretation.
	observation := parseEnvelopeFixture(t, "session_start_2_1_261.json", "SessionStart")
	prepared := preparedObservation(t, observation)
	malformed := observation
	malformed.Capture = model.CaptureUnsupportedSchema
	if _, err := store.service.Receive(ctx, envelopeWarrant(t, malformed), malformed, prepared...); err == nil {
		t.Fatal("Receive committed prepared effects for an unsupported capture; want refusal")
	}
	if got := len(queryEnvelopeKind(t, store.tracker, envelopeOccurrenceV2)); got != before+1 {
		t.Fatalf("occurrence.v2 rows = %d after refused write, want no new row", got)
	}

	assertBothEnvelopesReadable(t, ctx, store, committed.JournalID(), delivery.Contract, false)
}

// TestReceiveRefusesForgedSuppliedRefsBeforeAnyAppend proves caller-supplied
// nonzero refs must equal the owner documents for the write: a dangling,
// wrong-kind, content-mismatched or wrong-contract ref is refused with
// nothing appended. Red-verified by removing the supplied check: every case
// below commits.
func TestReceiveRefusesForgedSuppliedRefsBeforeAnyAppend(t *testing.T) {
	t.Parallel()
	ctx, store := openEnvelopeStore(t)
	delivery := parseEnvelopeFixture(t, "session_start_2_1_261.json", "SessionStart")
	prepared := preparedObservation(t, delivery)
	if _, err := store.service.Receive(ctx, envelopeWarrant(t, delivery), delivery, prepared...); err != nil {
		t.Fatalf("seed Receive: %v", err)
	}
	occurrences := queryEnvelopeKind(t, store.tracker, envelopeOccurrenceV2)
	if len(occurrences) != 1 {
		t.Fatalf("seed rows = %d, want one", len(occurrences))
	}
	genuine := decodeEnvelopeOccurrence(t, occurrences[0].Payload).Envelope

	forged := func(mutate func(*model.OccurrenceEnvelopeRef)) receipt.Delivery {
		t.Helper()
		envelope := genuine
		mutate(&envelope)
		out := delivery
		out.Envelope = envelope
		out.Body = append(append([]byte(nil), delivery.Body...), '.')
		return out
	}
	dangling := genuine.Runtime.Definition
	dangling.Definition = model.DefinitionJournalID(provenance.JournalID(dangling.Definition) + 1<<20)
	mismatched := genuine.Schema.Definition
	mismatched.Content[0] ^= 1
	cases := []struct {
		name     string
		delivery receipt.Delivery
	}{
		{"dangling runtime id", forged(func(e *model.OccurrenceEnvelopeRef) { e.Runtime.Definition = dangling })},
		{"wrong-kind schema", forged(func(e *model.OccurrenceEnvelopeRef) {
			e.Schema.Definition.Kind = model.DefinitionInterpreter
		})},
		{"content-mismatched schema", forged(func(e *model.OccurrenceEnvelopeRef) { e.Schema.Definition = mismatched })},
		{"wrong contract", forged(func(e *model.OccurrenceEnvelopeRef) {
			contract, err := ir.NewRuntimeContractID(ir.HarnessCodex, "codex/0.153.0")
			if err != nil {
				t.Fatalf("codex contract: %v", err)
			}
			e.Runtime.Contract = contract
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.service.Receive(ctx, envelopeWarrant(t, tc.delivery), tc.delivery); err == nil {
				t.Fatalf("Receive accepted %s; want refusal before any append", tc.name)
			}
			if got := len(queryEnvelopeKind(t, store.tracker, envelopeOccurrenceV2)); got != 1 {
				t.Fatalf("occurrence.v2 rows = %d after refused write, want exactly the seed row", got)
			}
		})
	}
}

// TestTransactionReplayRejectsDuplicateInterpretations proves the
// non-reclaiming replay fails closed with nothing projected when one
// operation carries two interpretations, and the caller rolls back.
func TestTransactionReplayRejectsDuplicateInterpretations(t *testing.T) {
	t.Parallel()
	ctx, store := openEnvelopeStore(t)
	delivery := parseEnvelopeFixture(t, "session_start_2_1_261.json", "SessionStart")
	prepared := preparedObservation(t, delivery)
	if _, err := store.service.Receive(ctx, envelopeWarrant(t, delivery), delivery, prepared...); err != nil {
		t.Fatalf("seed Receive: %v", err)
	}

	resolver, ok := store.tracker.(receipt.IdentityResolver)
	if !ok {
		t.Fatal("tracker does not resolve lifecycle identity")
	}
	identity, err := resolver.ResolveLifecycleIdentity(ctx)
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	occurrences := queryEnvelopeKind(t, store.tracker, envelopeOccurrenceV2)
	genuine := decodeEnvelopeOccurrence(t, occurrences[0].Payload)
	occPayload, err := json.Marshal(map[string]any{
		"contract": genuine.Contract, "event": genuine.Event, "envelope": genuine.Envelope,
		"bindings": nil, "capture": genuine.Capture, "body_digest": digest.FromBytes(delivery.Body).String(),
	})
	if err != nil {
		t.Fatalf("encode occurrence: %v", err)
	}
	occDigest := sha256.Sum256(occPayload)
	command := sha256.Sum256(append([]byte("pasture.lifecycle.receipt.append/v1\x00"), occPayload...))
	authority := identity.Authority
	duplicate := append([]byte(nil), prepared[0].Payload...)
	duplicateDigest := sha256.Sum256(duplicate)
	// The journal refuses two effects with one result slot at its canonical
	// boundary, so the duplicate is seeded under a shadow slot: the replay
	// counts interpreted FACTS per operation by evidence kind, not by slot
	// spelling, and must still fail closed.
	input := provenance.OperationInput{
		OperationID:        provenance.OperationID("pasture.lifecycle.envelope-test.duplicate-interpretation"),
		ActorID:            identity.Actor,
		AuthorityJournalID: &authority,
		CommandDigest:      command[:],
		RecordedAt:         time.Now().UTC().UnixNano(),
		Effects: []provenance.Effect{
			{Sort: provenance.EffectEvidence, ResultSlot: provenance.ResultSlotID("occurrence"), EvidenceKind: envelopeOccurrenceV2, ContentDigest: occDigest[:], Payload: append(json.RawMessage(nil), occPayload...)},
			{Sort: provenance.EffectEvidence, ResultSlot: provenance.ResultSlotID("interpreted"), EvidenceKind: envelopeInterpretedV2, ContentDigest: duplicateDigest[:], Payload: append(json.RawMessage(nil), duplicate...)},
			{Sort: provenance.EffectEvidence, ResultSlot: provenance.ResultSlotID("interpreted-shadow"), EvidenceKind: envelopeInterpretedV2, ContentDigest: duplicateDigest[:], Payload: append(json.RawMessage(nil), duplicate...)},
		},
	}
	contextJournal, ok := store.tracker.Journal().(provenance.ContextJournal)
	if !ok {
		t.Fatal("journal has no context apply")
	}
	if _, err := contextJournal.ApplyContext(ctx, input); err != nil {
		t.Fatalf("seed duplicate operation: %v", err)
	}

	db, err := sql.Open("sqlite", store.dbPath)
	if err != nil {
		t.Fatalf("open sql handle: %v", err)
	}
	defer db.Close()
	before := countEnvelopeRows(t, db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	txReader, err := provenance.NewTransactionEvidenceReader(ctx, tx)
	if err != nil {
		t.Fatalf("transaction reader: %v", err)
	}
	if err := projection.RebuildOccurrencesInTx(ctx, tx, txReader); err == nil {
		t.Fatal("transaction replay accepted duplicate interpretations; want fail-closed before projection")
	}
	_ = tx.Rollback()
	if got := countEnvelopeRows(t, db); got != before {
		t.Fatalf("projection rows changed across a failed replay, want rollback to preserve them")
	}
}

func countEnvelopeRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&count); err != nil {
		t.Fatalf("count occurrences: %v", err)
	}
	return count
}

// TestV2OriginOmissionAndRawCarriage pins the origin carrier on the v2
// writer: an unset origin is omitted (never emitted empty), and a raw origin
// rides both the payload and envelope members of the committed v2 bytes.
func TestV2OriginOmissionAndRawCarriage(t *testing.T) {
	t.Parallel()
	ctx, store := openEnvelopeStore(t)
	delivery := parseEnvelopeFixture(t, "session_start_2_1_261.json", "SessionStart")
	if _, err := store.service.Receive(ctx, envelopeWarrant(t, delivery), delivery); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	rows := queryEnvelopeKind(t, store.tracker, envelopeOccurrenceV2)
	if len(rows) != 1 {
		t.Fatalf("occurrence.v2 rows = %d, want one", len(rows))
	}
	if bytes.Contains(rows[0].Payload, []byte("origin")) {
		t.Fatalf("unset-origin v2 payload carries an origin member:\n%s", rows[0].Payload)
	}
	raw := parseEnvelopeFixture(t, "session_start_2_1_261.json", "SessionStart")
	raw.Origin = "raw"
	raw.Envelope.Origin = "raw"
	raw.Body = append(append([]byte(nil), raw.Body...), ' ')
	if _, err := store.service.Receive(ctx, envelopeWarrant(t, raw), raw); err != nil {
		t.Fatalf("Receive raw: %v", err)
	}
	rows = queryEnvelopeKind(t, store.tracker, envelopeOccurrenceV2)
	if len(rows) != 2 {
		t.Fatalf("occurrence.v2 rows = %d, want two", len(rows))
	}
	if got := strings.Count(string(rows[1].Payload), `"origin":"raw"`); got != 2 {
		t.Fatalf("raw-origin v2 payload carries origin %d times, want payload + envelope (2)", got)
	}
}

// TestWindowAcceptanceCommitsOnTheProductionPath pins the writer-window
// acceptance arms on the real opener: a context with no deadline and a
// deadline inside the window both commit.
func TestWindowAcceptanceCommitsOnTheProductionPath(t *testing.T) {
	t.Parallel()
	_, store := openEnvelopeStore(t)
	delivery := parseEnvelopeFixture(t, "session_start_2_1_261.json", "SessionStart")
	if _, err := store.service.Receive(context.Background(), envelopeWarrant(t, delivery), delivery); err != nil {
		t.Fatalf("no-deadline Receive: %v", err)
	}
	windowed, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	second := delivery
	second.Body = append(append([]byte(nil), delivery.Body...), ' ')
	if _, err := store.service.Receive(windowed, envelopeWarrant(t, second), second); err != nil {
		t.Fatalf("inside-window Receive: %v", err)
	}
	if got := len(queryEnvelopeKind(t, store.tracker, envelopeOccurrenceV2)); got != 2 {
		t.Fatalf("occurrence.v2 rows = %d, want two accepted deliveries", got)
	}
}

// TestResolveSemanticEnvelopeRefusesCorruptSlots proves each semantic slot
// independently rejects dangling and wrong-kind refs, and that individually
// valid but different paired shared refs fail. Every case fails closed with
// no partial validated result. Red-verified by resolving against a resolver
// that approves everything: each dangling case then passes.
func TestResolveSemanticEnvelopeRefusesCorruptSlots(t *testing.T) {
	t.Parallel()
	ctx, store := openEnvelopeStore(t)
	delivery := parseEnvelopeFixture(t, "session_start_2_1_261.json", "SessionStart")
	prepared := preparedObservation(t, delivery)
	committed, err := store.service.Receive(ctx, envelopeWarrant(t, delivery), delivery, prepared...)
	if err != nil {
		t.Fatalf("seed Receive: %v", err)
	}
	if err := tasks.RebuildLifecycleOccurrences(ctx, store.tracker); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	reader, err := tasks.NewLifecycleReader(store.tracker)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	size, err := model.NewPageSize(model.MaxPageSize)
	if err != nil {
		t.Fatalf("page size: %v", err)
	}
	page, err := reader.Records(ctx, model.OccurrenceQuery{Page: model.PageRequest{Size: size}})
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	var occurrence model.OccurrenceRecord
	var interpreted model.InterpretedRecord
	for _, item := range page.Items {
		if item.Occurrence.JournalID() == committed.JournalID() {
			occurrence = item.Occurrence
			interpreted = item.Interpreted()[0]
		}
	}
	if occurrence.JournalID() == 0 || len(interpreted.Semantic().String()) == 0 {
		t.Fatal("seed record not exposed by validated read")
	}
	manifest, ok := interpreted.Metamodel()
	if !ok {
		t.Fatal("seed interpretation carries no metamodel coordinate")
	}
	defs, err := receipt.NewDefinitionReader(store.tracker.Journal())
	if err != nil {
		t.Fatalf("definition reader: %v", err)
	}
	dangling := func(ref model.DefinitionRef) model.DefinitionRef {
		out := ref
		out.Definition = model.DefinitionJournalID(provenance.JournalID(ref.Definition) + 1<<28)
		out.Content[0] ^= 0xff
		if out.Content == (model.ContentIdentity{}) {
			out.Content[0] = 1
		}
		return out
	}
	genuine, ok := interpreted.SemanticEnvelope()
	if !ok {
		t.Fatal("seed interpretation carries no semantic envelope")
	}
	slots := []struct {
		name string
		get  func(model.SemanticEnvelopeRef) model.DefinitionRef
		set  func(*model.SemanticEnvelopeRef, model.DefinitionRef)
	}{
		{"runtime", func(e model.SemanticEnvelopeRef) model.DefinitionRef { return e.Runtime.Definition }, func(e *model.SemanticEnvelopeRef, r model.DefinitionRef) { e.Runtime.Definition = r }},
		{"schema", func(e model.SemanticEnvelopeRef) model.DefinitionRef { return e.Schema.Definition }, func(e *model.SemanticEnvelopeRef, r model.DefinitionRef) { e.Schema.Definition = r }},
		{"metamodel", func(e model.SemanticEnvelopeRef) model.DefinitionRef { return e.Metamodel.Definition }, func(e *model.SemanticEnvelopeRef, r model.DefinitionRef) { e.Metamodel.Definition = r }},
		{"interpreter", func(e model.SemanticEnvelopeRef) model.DefinitionRef { return e.Interpreter.Definition }, func(e *model.SemanticEnvelopeRef, r model.DefinitionRef) { e.Interpreter.Definition = r }},
		{"implementation", func(e model.SemanticEnvelopeRef) model.DefinitionRef { return e.Implementation.Definition }, func(e *model.SemanticEnvelopeRef, r model.DefinitionRef) { e.Implementation.Definition = r }},
	}
	for _, slot := range slots {
		slot := slot
		t.Run("dangling "+slot.name, func(t *testing.T) {
			envelope := genuine
			slot.set(&envelope, dangling(slot.get(genuine)))
			record, err := model.NewInterpretedRecordWithEnvelope(1, 1, interpreted.Semantic(), interpreted.Identities(), interpreted.UnresolvedFacts(), interpreted.Contract(), manifest, envelope)
			if err != nil {
				t.Fatalf("structurally valid dangling envelope refused at construction: %v", err)
			}
			if err := receipt.ResolveSemanticEnvelope(defs, occurrence, record, 0); err == nil {
				t.Fatalf("dangling %s ref resolved; want fail-closed", slot.name)
			}
		})
		t.Run("wrong-kind "+slot.name, func(t *testing.T) {
			envelope := genuine
			bad := slot.get(genuine)
			bad.Kind = model.DefinitionContextPolicy
			slot.set(&envelope, bad)
			if _, err := model.NewInterpretedRecordWithEnvelope(1, 1, interpreted.Semantic(), interpreted.Identities(), interpreted.UnresolvedFacts(), interpreted.Contract(), manifest, envelope); err == nil {
				t.Fatalf("wrong-kind %s envelope constructed; want structural refusal", slot.name)
			}
		})
	}
	t.Run("valid but different shared runtime", func(t *testing.T) {
		other, err := definitions.ActiveForContract(runtime.OpenCode1_18_29Lifecycle().ID())
		if err != nil {
			t.Fatalf("opencode documents: %v", err)
		}
		foreign, err := receipt.EnsureActiveDefinition(ctx, store.service, other.Runtime)
		if err != nil {
			t.Fatalf("ensure foreign runtime: %v", err)
		}
		envelope := genuine
		envelope.Runtime.Definition = foreign
		record, err := model.NewInterpretedRecordWithEnvelope(1, 1, interpreted.Semantic(), interpreted.Identities(), interpreted.UnresolvedFacts(), interpreted.Contract(), manifest, envelope)
		if err != nil {
			t.Fatalf("paired-mismatch envelope refused at construction: %v", err)
		}
		if err := receipt.ResolveSemanticEnvelope(defs, occurrence, record, 0); err == nil {
			t.Fatal("valid-but-different shared runtime refs paired; want fail-closed")
		}
	})
}

// TestTurnScopeSharesDefinitionEnsuresAcrossRecords proves the lane-turn
// share: two deliveries prepared under one turn scope activate each owner
// document exactly once, and the second delivery resolves nothing new. The
// direct path (no scope) always ensures standalone.
func TestTurnScopeSharesDefinitionEnsuresAcrossRecords(t *testing.T) {
	t.Parallel()
	ctx, store := openEnvelopeStore(t)
	first := parseEnvelopeFixture(t, "session_start_2_1_261.json", "SessionStart")
	second := first
	second.Body = append(append([]byte(nil), first.Body...), ' ')
	countDefinitions := func() int {
		t.Helper()
		rows := queryEnvelopeKind(t, store.tracker, "pasture.lifecycle.definition.v2")
		states := queryEnvelopeKind(t, store.tracker, "pasture.lifecycle.definition-state.v2")
		return len(rows) + len(states)
	}
	before := countDefinitions()
	scope := writelane.NewTurnScope()
	turn := writelane.ContextWithTurnScope(ctx, scope)
	if _, err := store.service.Receive(turn, envelopeWarrant(t, first), first, preparedObservation(t, first)...); err != nil {
		t.Fatalf("first turn Receive: %v", err)
	}
	afterFirst := countDefinitions()
	if afterFirst <= before {
		t.Fatalf("first turn committed no definition activations (%d -> %d)", before, afterFirst)
	}
	if _, err := store.service.Receive(turn, envelopeWarrant(t, second), second, preparedObservation(t, second)...); err != nil {
		t.Fatalf("second turn Receive: %v", err)
	}
	if got := countDefinitions(); got != afterFirst {
		t.Fatalf("second turn committed new definition rows (%d -> %d); want full turn sharing", afterFirst, got)
	}
}
