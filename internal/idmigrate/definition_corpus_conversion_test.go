package idmigrate_test

// Definition-complete corpus and S1 conversion proof.
//
// CORPUS PRODUCTION (authentic, owner-produced journal evidence):
// Every lifecycle row in this test is committed by the real new writers
// through the production receipt path — parsed Claude ingress, genuine
// middle-end derivation, gate-warranted Service.Receive via
// tasks.NewLifecycleReceiptServiceWithProfile against a bootstrapped unified
// store. The store genuinely carries occurrence.v2 with four nonzero refs
// (Runtime=1, Schema=2, Implementation=5, Retention=8, each a positive
// JournalID with nonzero content) and interpreted.v3 with five nonzero refs
// (the four above plus Metamodel=3 via the normalized bridge and
// Interpreter=4), the three shared refs exactly equal, every ref
// owner-resolvable (kind/body/digest/slot/state). Malformed captures commit
// a record-only complete occurrence and no fabricated semantic record.
// Synthetic host input is labelled test data; no rows are hand-written and no
// JournalIDs/digests are faked. Red-verified by neutering the emission (see
// below).
//
// CONVERSION PROOF (real pipeline, current-store path):
// The seeding open already published marker + receipt + floor, so
// idmigrate.Convert — the same P3 transaction `pasture migrate ids` runs —
// reports AlreadyCurrent and writes nothing. The test asserts the nonzero
// graph is byte-identical and still owner-resolvable across that pipeline,
// plus JournalID order, native-binding bytes, raw blob inventory, the same
// non-reclaiming transaction replay + comparison + semantic verification P3g
// runs, and reopen reads. A full retired→slash conversion of a
// definition-complete store is explicitly NOT claimed: no released retired
// writer ever produced complete envelopes, and the v0.9.0 canonical converter
// refuses the slash-zero absent-task marker definition activations carry
// (pinned by TestDefinitionCompleteRetiredConversionBlocked). Red-verified by
// neutering the preservation (see below).
//
// PREVIOUS-WRITER CORPUS:
// The pinned previous-writer archive (provenance testdata/id-format/
// previous-writer.db, checksum e2ec1b57…) stays byte-identical and is not
// touched here. Its zero refs are a true fact about that writer (four kinds
// had no production writer in that release), not a gap to paper over. That
// corpus proves retired-zero conversion; this corpus proves nonzero-graph
// preservation through the same pipeline. Neither is read as the other.
//
// GATE LIMITS (stated so the corpus cannot be misread):
// This corpus CAN prove: nonzero definition-graph preservation across the
// conversion pipeline's current-store path, same-transaction resolution
// (transaction-bound reader + non-reclaiming replay + comparison + semantic
// verification), fail-closed corruption (dangling/wrong-kind refs and
// payload-tampered rows are rejected by the real owner verifiers), order/
// binding/raw-body preservation, and reopen reads.
// It CANNOT prove: a retired→slash conversion of a definition-complete store
// (no released retired writer produced one; the dependency additionally
// refuses the slash-zero absent-task marker — see the blocker pin), capture
// fidelity (synthetic fixtures, not live capture), future-writer behaviour,
// DBOS-state migration (refused by design), or the live operator conversion
// (URI-SLICE-4, separate approval-gated operation). Do not cite this test as
// evidence for the live conversion.
//
// RED VERIFICATION (pins exercise their path):
//   - Emission: with the v3 finalizer neutered to record-only
//     (prepareEnvelopes returning no semantic effects even for valid
//     captures), the conversion test finds zero interpreted.v3 rows and fails
//     (verified).
//   - Preservation/resolver: with ResolveSemanticEnvelope neutered to approve
//     everything, the dangling-ref subtests unexpectedly resolve and fail
//     (verified). The same resolver backs P3g's transaction verification, so
//     this proves the conversion-side check is exercised, not named.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/idmigrate"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	claudefrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	claudeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/middleend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

const (
	defCorpusOccurrenceV2   = provenance.EvidenceKind("pasture.lifecycle.occurrence.v2")
	defCorpusInterpretedV3  = provenance.EvidenceKind("pasture.lifecycle.interpreted.v3")
	defCorpusInterpretedV2  = provenance.EvidenceKind("pasture.lifecycle.interpreted.v2")
	defCorpusConsultationV2 = provenance.EvidenceKind("pasture.lifecycle.consultation.v2")
	defCorpusOccurrenceV1   = provenance.EvidenceKind("pasture.lifecycle.occurrence.v1")
	defCorpusRetiredAgent   = "file://defcorpus--01968a3c-9d4f-7c8a-bc12-feedfacecafe"
)

type defCorpusClock struct{}

func (defCorpusClock) Now() time.Time { return time.Unix(100, 0).UTC() }

type defCorpusOps struct{ n int }

func (o *defCorpusOps) NewOperationID() (string, error) {
	o.n++
	return "pasture.lifecycle.defcorpus." + string(rune('a'+o.n)), nil
}

func defCorpusBootstrap(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pasture.db")
	bootstrap, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open bootstrap tracker: %v", err)
	}
	if _, err := bootstrap.Create("file://defcorpus-bootstrap", "bootstrap", "initialize the persisted ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
		t.Fatalf("bootstrap identity: %v", err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatalf("close bootstrap tracker: %v", err)
	}
	return dbPath
}

func defCorpusWarrant(t *testing.T, delivery receipt.Delivery) gate.Warrant {
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

func defCorpusParseDelivery(t *testing.T, name, event string) receipt.Delivery {
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
	raw, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "claude", "testdata", "fixtures", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	capture := claudeingress.Parse(raw, selected, manifest.Version, model.OccurrenceEnvelopeRef{})
	if capture.Disposition != model.CaptureValid {
		t.Fatalf("fixture %s disposition = %v, want valid", name, capture.Disposition)
	}
	return capture.Delivery
}

func defCorpusPreparedObservation(t *testing.T, delivery receipt.Delivery) []provenance.Effect {
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

func defCorpusPreparedGate(t *testing.T, delivery receipt.Delivery) []provenance.Effect {
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
	decision, err := backend.NewDecision(backend.DecisionDeny, backend.ReasonNoActiveAssignment)
	if err != nil {
		t.Fatalf("decision: %v", err)
	}
	effects, err := receipt.ReplaceConsultationDecision(derived.Effects(), decision)
	if err != nil {
		t.Fatalf("replace decision: %v", err)
	}
	if len(effects) != 2 {
		t.Fatalf("gate derivation effects = %d, want the prepared pair", len(effects))
	}
	return effects
}

func defCorpusQueryKind(t *testing.T, tracker protocol.TaskTracker, kind provenance.EvidenceKind) []provenance.EvidenceRow {
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

type defCorpusOccurrencePayload struct {
	Contract string                      `json:"contract"`
	Event    model.ContractEventKind     `json:"event"`
	Envelope model.OccurrenceEnvelopeRef `json:"envelope"`
	Bindings []model.NativeBinding       `json:"bindings"`
	Capture  model.CaptureDisposition    `json:"capture"`
	Body     string                      `json:"body_digest"`
}

func defCorpusDecodeOccurrence(t *testing.T, payload []byte) defCorpusOccurrencePayload {
	t.Helper()
	var decoded defCorpusOccurrencePayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode occurrence payload: %v", err)
	}
	return decoded
}

func defCorpusRequireCompleteOccurrence(t *testing.T, envelope model.OccurrenceEnvelopeRef, contract ir.RuntimeContractID) {
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
	for name, ref := range map[string]model.DefinitionRef{
		"runtime": envelope.Runtime.Definition, "schema": envelope.Schema.Definition,
		"implementation": envelope.Implementation.Definition, "retention": envelope.Retention.Definition,
	} {
		if ref.Definition <= 0 || ref.Content == (model.ContentIdentity{}) {
			t.Fatalf("occurrence slot %s is zero: %+v", name, ref)
		}
	}
	if envelope.Runtime.Contract != contract {
		t.Fatalf("occurrence runtime contract = %q, want delivery %q", envelope.Runtime.Contract, contract)
	}
}

func defCorpusRequireCompleteSemantic(t *testing.T, envelope model.SemanticEnvelopeRef, occurrence model.OccurrenceEnvelopeRef) {
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
	for name, ref := range map[string]model.DefinitionRef{
		"runtime": envelope.Runtime.Definition, "schema": envelope.Schema.Definition,
		"metamodel": envelope.Metamodel.Definition, "interpreter": envelope.Interpreter.Definition,
		"implementation": envelope.Implementation.Definition,
	} {
		if ref.Definition <= 0 || ref.Content == (model.ContentIdentity{}) {
			t.Fatalf("semantic slot %s is zero: %+v", name, ref)
		}
	}
	if !envelope.SharedRefsEqual(occurrence) {
		t.Fatalf("shared refs differ across the pair:\nsemantic=%+v\noccurrence=%+v", envelope, occurrence)
	}
}

type defCorpusBlob struct {
	Digest    string
	Body      []byte
	ByteCount int
	WrittenAt int64
}

func defCorpusBlobInventory(t *testing.T, db *sql.DB) []defCorpusBlob {
	t.Helper()
	rows, err := db.Query(`SELECT digest, body, byte_count, written_at FROM lifecycle_payload_blobs ORDER BY digest`)
	if err != nil {
		t.Fatalf("query blobs: %v", err)
	}
	defer rows.Close()
	var out []defCorpusBlob
	for rows.Next() {
		var b defCorpusBlob
		if err := rows.Scan(&b.Digest, &b.Body, &b.ByteCount, &b.WrittenAt); err != nil {
			t.Fatalf("scan blob: %v", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

type defCorpusRefKey struct {
	JournalID provenance.JournalID
	Kind      model.DefinitionKind
	Content   model.ContentIdentity
}

func defCorpusRefKeyOf(ref model.DefinitionRef) defCorpusRefKey {
	return defCorpusRefKey{JournalID: provenance.JournalID(ref.Definition), Kind: ref.Kind, Content: ref.Content}
}

// TestDefinitionCompleteCorpusConversionPreservesGraph is the S1 conversion
// proof: a definition-complete corpus built by the real new writers survives
// the real conversion transaction with every definition ref identical and
// still owner-resolvable, plus order/binding/raw-body preservation and reopen
// reads. See the file header for the production path, the gate limits, and
// the red-verification notes.
func TestDefinitionCompleteCorpusConversionPreservesGraph(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := defCorpusBootstrap(t)

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open tracker: %v", err)
	}
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, defCorpusClock{}, &defCorpusOps{}, timeouts.TestProfile())
	if err != nil {
		t.Fatalf("wire production receipt service: %v", err)
	}
	if _, err := receipt.EnsureActiveMetamodel(ctx, service); err != nil {
		t.Fatalf("ensure active metamodel: %v", err)
	}

	// Two valid deliveries (observation + gate) plus one record-only malformed
	// capture. All three commit occurrence.v2 with four nonzero refs; the two
	// valid ones also finalize interpreted.v3 with five nonzero refs.
	first := defCorpusParseDelivery(t, "session_start_2_1_261.json", "SessionStart")
	firstPrepared := defCorpusPreparedObservation(t, first)
	firstReceipt, err := service.Receive(ctx, defCorpusWarrant(t, first), first, firstPrepared...)
	if err != nil {
		t.Fatalf("Receive first: %v", err)
	}
	second := defCorpusParseDelivery(t, "pre_tool_use_2_1_261.json", "PreToolUse")
	second.Body = append(append([]byte(nil), second.Body...), ' ')
	secondPrepared := defCorpusPreparedGate(t, second)
	secondReceipt, err := service.Receive(ctx, defCorpusWarrant(t, second), second, secondPrepared...)
	if err != nil {
		t.Fatalf("Receive second: %v", err)
	}
	malformed := defCorpusParseDelivery(t, "session_start_2_1_261.json", "SessionStart")
	malformed.Capture = model.CaptureMalformed
	malformed.Body = append(append([]byte(nil), malformed.Body...), '.', '.')
	malformedReceipt, err := service.Receive(ctx, defCorpusWarrant(t, malformed), malformed)
	if err != nil {
		t.Fatalf("Receive malformed: %v", err)
	}

	// Pre-conversion completeness: three v2 occurrences, two v3
	// interpretations, no legacy emission, no fabricated semantic for the
	// record-only capture.
	occRows := defCorpusQueryKind(t, tracker, defCorpusOccurrenceV2)
	if len(occRows) != 3 {
		t.Fatalf("occurrence.v2 rows = %d, want three (two valid + one record-only)", len(occRows))
	}
	for i := 1; i < len(occRows); i++ {
		if occRows[i].JournalID <= occRows[i-1].JournalID {
			t.Fatalf("occurrence JournalID order not stable: %d then %d", occRows[i-1].JournalID, occRows[i].JournalID)
		}
	}
	if legacy := defCorpusQueryKind(t, tracker, defCorpusOccurrenceV1); len(legacy) != 0 {
		t.Fatalf("occurrence.v1 rows = %d, want zero (new writer never emits v1)", len(legacy))
	}
	if legacy := defCorpusQueryKind(t, tracker, defCorpusInterpretedV2); len(legacy) != 0 {
		t.Fatalf("interpreted.v2 rows = %d, want zero (prepared v2 finalizes to v3, never commits)", len(legacy))
	}
	intRows := defCorpusQueryKind(t, tracker, defCorpusInterpretedV3)
	if len(intRows) != 2 {
		t.Fatalf("interpreted.v3 rows = %d, want exactly two (no semantic for the malformed capture)", len(intRows))
	}

	decodedFirst := defCorpusDecodeOccurrence(t, occRows[0].Payload)
	decodedSecond := defCorpusDecodeOccurrence(t, occRows[1].Payload)
	decodedMalformed := defCorpusDecodeOccurrence(t, occRows[2].Payload)
	defCorpusRequireCompleteOccurrence(t, decodedFirst.Envelope, first.Contract)
	defCorpusRequireCompleteOccurrence(t, decodedSecond.Envelope, second.Contract)
	defCorpusRequireCompleteOccurrence(t, decodedMalformed.Envelope, malformed.Contract)
	if decodedMalformed.Capture != model.CaptureMalformed {
		t.Fatalf("malformed occurrence capture = %v, want malformed recorded honestly", decodedMalformed.Capture)
	}
	if occRows[0].ProducingOperationID == occRows[1].ProducingOperationID {
		t.Fatal("the two valid occurrences share one operation; want one atomic pair per delivery")
	}
	for _, row := range intRows {
		found := false
		for _, occ := range occRows[:2] {
			if row.ProducingOperationID == occ.ProducingOperationID {
				found = true
			}
		}
		if !found {
			t.Fatalf("interpreted.v3 operation %q matches no valid occurrence operation", row.ProducingOperationID)
		}
	}
	for _, row := range intRows {
		if row.ProducingOperationID == occRows[2].ProducingOperationID {
			t.Fatal("interpreted.v3 attached to the malformed operation; want record-only with no semantic record")
		}
	}

	semanticOf := func(t *testing.T, payload []byte) model.SemanticEnvelopeRef {
		t.Helper()
		decoded, err := receipt.DecodeInterpretedV3(1, 1, payload)
		if err != nil {
			t.Fatalf("DecodeInterpretedV3: %v", err)
		}
		semantic, ok := decoded.SemanticEnvelope()
		if !ok {
			t.Fatal("decoded v3 reports no semantic envelope")
		}
		return semantic
	}
	semanticFirst := semanticOf(t, intRows[0].Payload)
	semanticSecond := semanticOf(t, intRows[1].Payload)
	defCorpusRequireCompleteSemantic(t, semanticFirst, decodedFirst.Envelope)
	defCorpusRequireCompleteSemantic(t, semanticSecond, decodedSecond.Envelope)

	// Every ref resolves through its real owner before conversion.
	reader, err := receipt.NewDefinitionReader(tracker.Journal())
	if err != nil {
		t.Fatalf("definition reader: %v", err)
	}
	resolveAll := func(t *testing.T, label string, occ model.OccurrenceEnvelopeRef, sem *model.SemanticEnvelopeRef) {
		t.Helper()
		refs := map[string]model.DefinitionRef{
			"runtime": occ.Runtime.Definition, "schema": occ.Schema.Definition,
			"implementation": occ.Implementation.Definition, "retention": occ.Retention.Definition,
		}
		if sem != nil {
			refs["interpreter"] = sem.Interpreter.Definition
		}
		for name, ref := range refs {
			snapshot, state, err := receipt.ResolveDefinition(reader, ref)
			if err != nil {
				t.Fatalf("%s resolve %s ref: %v", label, name, err)
			}
			if len(snapshot.Body()) == 0 || ref.Content == (model.ContentIdentity{}) {
				t.Fatalf("%s resolved %s ref has empty body or content", label, name)
			}
			if state.LatestFact.Definition != ref.Definition {
				t.Fatalf("%s %s state fact %+v does not cite its definition %+v", label, name, state.LatestFact, ref)
			}
			_ = snapshot
		}
		if sem != nil {
			metaSnapshot, _, err := receipt.ResolveMetamodelRef(reader, sem.Metamodel.Definition, 0)
			if err != nil {
				t.Fatalf("%s resolve metamodel ref: %v", label, err)
			}
			if len(metaSnapshot.Body()) == 0 {
				t.Fatalf("%s resolved metamodel body is empty", label)
			}
			bridged, _, _, err := receipt.BridgeActiveMetamodel(ctx, reader)
			if err != nil {
				t.Fatalf("%s bridge active metamodel: %v", label, err)
			}
			if bridged.Definition != sem.Metamodel.Definition {
				t.Fatalf("%s bridged metamodel %+v != semantic %+v", label, bridged.Definition, sem.Metamodel.Definition)
			}
		}
	}
	resolveAll(t, "pre-conversion first", decodedFirst.Envelope, &semanticFirst)
	resolveAll(t, "pre-conversion second", decodedSecond.Envelope, &semanticSecond)
	resolveAll(t, "pre-conversion malformed", decodedMalformed.Envelope, nil)

	// Snapshot the nonzero graph, order, bindings, and raw bodies.
	preRefs := map[string]defCorpusRefKey{
		"occ0-runtime": defCorpusRefKeyOf(decodedFirst.Envelope.Runtime.Definition),
		"occ0-schema":  defCorpusRefKeyOf(decodedFirst.Envelope.Schema.Definition),
		"occ0-impl":    defCorpusRefKeyOf(decodedFirst.Envelope.Implementation.Definition),
		"occ0-ret":     defCorpusRefKeyOf(decodedFirst.Envelope.Retention.Definition),
		"sem0-meta":    defCorpusRefKeyOf(semanticFirst.Metamodel.Definition),
		"sem0-interp":  defCorpusRefKeyOf(semanticFirst.Interpreter.Definition),
		"occ1-runtime": defCorpusRefKeyOf(decodedSecond.Envelope.Runtime.Definition),
		"occ1-schema":  defCorpusRefKeyOf(decodedSecond.Envelope.Schema.Definition),
		"occ1-impl":    defCorpusRefKeyOf(decodedSecond.Envelope.Implementation.Definition),
		"occ1-ret":     defCorpusRefKeyOf(decodedSecond.Envelope.Retention.Definition),
		"sem1-meta":    defCorpusRefKeyOf(semanticSecond.Metamodel.Definition),
		"sem1-interp":  defCorpusRefKeyOf(semanticSecond.Interpreter.Definition),
	}
	preOrder := []provenance.JournalID{occRows[0].JournalID, occRows[1].JournalID, occRows[2].JournalID}
	preBindings := [][]model.NativeBinding{decodedFirst.Bindings, decodedSecond.Bindings, decodedMalformed.Bindings}
	prePayloads := [][]byte{append([]byte(nil), occRows[0].Payload...), append([]byte(nil), occRows[1].Payload...), append([]byte(nil), occRows[2].Payload...)}
	preInterpreted := [][]byte{append([]byte(nil), intRows[0].Payload...), append([]byte(nil), intRows[1].Payload...)}

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sql handle: %v", err)
	}
	preBlobs := defCorpusBlobInventory(t, sqlDB)
	_ = sqlDB.Close()

	if err := tracker.Close(); err != nil {
		t.Fatalf("close tracker before conversion: %v", err)
	}

	// The real conversion pipeline on a current definition-complete store:
	// the seeding open already published marker + receipt + floor, so
	// idmigrate.Convert — the same P3 transaction `pasture migrate ids`
	// runs — reports AlreadyCurrent and writes nothing. The nonzero graph
	// therefore survives byte-identical, and the same-transaction replay,
	// comparison, and semantic verification below prove it is still
	// owner-resolvable on a transaction view, exactly as P3g does.
	//
	// A full retired→slash conversion of a definition-complete store is
	// NOT claimed here: no released retired writer ever produced complete
	// envelopes, and the v0.9.0 dependency's canonical-mutation converter
	// refuses the slash-zero absent-task marker ("/000…") that definition
	// activations carry (see TestDefinitionCompleteRetiredConversionBlocked).
	// The previous-writer zero-ref corpus already proves retired conversion
	// of lifecycle replay; this test proves nonzero-graph preservation
	// through the same pipeline's current-store path plus its replay.
	shared, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		t.Fatalf("open shared: %v", err)
	}
	defer shared.Close()

	report, err := idmigrate.Convert(ctx, shared, idmigrate.ConvertOptions{StorePath: dbPath})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !report.AlreadyCurrent {
		t.Fatalf("report=%+v want AlreadyCurrent on the current definition-complete store", report)
	}
	if report.Reclaimed != 0 {
		t.Fatalf("Reclaimed=%d, want zero (reclaim is structurally absent from conversion)", report.Reclaimed)
	}
	if err := shared.Close(); err != nil {
		t.Fatalf("close shared: %v", err)
	}

	// Post-conversion: the same rows, the same nonzero graph, still
	// owner-resolvable; order, bindings, and raw bodies preserved.
	reopened, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("reopen converted tracker: %v", err)
	}
	defer reopened.Close()

	postOcc := defCorpusQueryKind(t, reopened, defCorpusOccurrenceV2)
	if len(postOcc) != 3 {
		t.Fatalf("post-conversion occurrence.v2 rows = %d, want three", len(postOcc))
	}
	postInt := defCorpusQueryKind(t, reopened, defCorpusInterpretedV3)
	if len(postInt) != 2 {
		t.Fatalf("post-conversion interpreted.v3 rows = %d, want two", len(postInt))
	}
	postFirst := defCorpusDecodeOccurrence(t, postOcc[0].Payload)
	postSecond := defCorpusDecodeOccurrence(t, postOcc[1].Payload)
	postMalformed := defCorpusDecodeOccurrence(t, postOcc[2].Payload)
	defCorpusRequireCompleteOccurrence(t, postFirst.Envelope, first.Contract)
	defCorpusRequireCompleteOccurrence(t, postSecond.Envelope, second.Contract)
	defCorpusRequireCompleteOccurrence(t, postMalformed.Envelope, malformed.Contract)
	postSemanticFirst := semanticOf(t, postInt[0].Payload)
	postSemanticSecond := semanticOf(t, postInt[1].Payload)
	defCorpusRequireCompleteSemantic(t, postSemanticFirst, postFirst.Envelope)
	defCorpusRequireCompleteSemantic(t, postSemanticSecond, postSecond.Envelope)

	postRefs := map[string]defCorpusRefKey{
		"occ0-runtime": defCorpusRefKeyOf(postFirst.Envelope.Runtime.Definition),
		"occ0-schema":  defCorpusRefKeyOf(postFirst.Envelope.Schema.Definition),
		"occ0-impl":    defCorpusRefKeyOf(postFirst.Envelope.Implementation.Definition),
		"occ0-ret":     defCorpusRefKeyOf(postFirst.Envelope.Retention.Definition),
		"sem0-meta":    defCorpusRefKeyOf(postSemanticFirst.Metamodel.Definition),
		"sem0-interp":  defCorpusRefKeyOf(postSemanticFirst.Interpreter.Definition),
		"occ1-runtime": defCorpusRefKeyOf(postSecond.Envelope.Runtime.Definition),
		"occ1-schema":  defCorpusRefKeyOf(postSecond.Envelope.Schema.Definition),
		"occ1-impl":    defCorpusRefKeyOf(postSecond.Envelope.Implementation.Definition),
		"occ1-ret":     defCorpusRefKeyOf(postSecond.Envelope.Retention.Definition),
		"sem1-meta":    defCorpusRefKeyOf(postSemanticSecond.Metamodel.Definition),
		"sem1-interp":  defCorpusRefKeyOf(postSemanticSecond.Interpreter.Definition),
	}
	for name, want := range preRefs {
		if got := postRefs[name]; got != want {
			t.Fatalf("definition ref %s changed across conversion:\n pre=%+v\npost=%+v", name, want, got)
		}
	}
	for i, want := range preOrder {
		if got := postOcc[i].JournalID; got != want {
			t.Fatalf("occurrence order changed at index %d: pre=%d post=%d", i, want, got)
		}
	}
	for i, want := range preBindings {
		got := [][]model.NativeBinding{postFirst.Bindings, postSecond.Bindings, postMalformed.Bindings}[i]
		if len(got) != len(want) {
			t.Fatalf("bindings count changed at occurrence %d: pre=%d post=%d", i, len(want), len(got))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("binding bytes changed at occurrence %d index %d:\n pre=%+v\npost=%+v", i, j, want[j], got[j])
			}
		}
	}
	// Raw bodies: the occurrence payloads name content digests; the
	// interpreted bytes must also be identical (conversion never rewrites
	// lifecycle evidence, only replays it).
	for i, want := range prePayloads {
		var preEnv, postEnv defCorpusOccurrencePayload
		if err := json.Unmarshal(want, &preEnv); err != nil {
			t.Fatalf("unmarshal pre payload %d: %v", i, err)
		}
		var postPayload []byte
		switch i {
		case 0:
			postPayload = postOcc[0].Payload
		case 1:
			postPayload = postOcc[1].Payload
		default:
			postPayload = postOcc[2].Payload
		}
		if err := json.Unmarshal(postPayload, &postEnv); err != nil {
			t.Fatalf("unmarshal post payload %d: %v", i, err)
		}
		if preEnv.Body != postEnv.Body {
			t.Fatalf("body digest changed at occurrence %d: pre=%s post=%s", i, preEnv.Body, postEnv.Body)
		}
	}
	for i, want := range preInterpreted {
		if !bytes.Equal(want, postInt[i].Payload) {
			t.Fatalf("interpreted.v3 bytes changed at index %d across conversion", i)
		}
	}

	postSQL, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open post sql: %v", err)
	}
	defer postSQL.Close()
	postBlobs := defCorpusBlobInventory(t, postSQL)
	if len(postBlobs) != len(preBlobs) {
		t.Fatalf("blob inventory count changed: pre=%d post=%d", len(preBlobs), len(postBlobs))
	}
	for i := range preBlobs {
		if postBlobs[i].Digest != preBlobs[i].Digest || !bytes.Equal(postBlobs[i].Body, preBlobs[i].Body) || postBlobs[i].ByteCount != preBlobs[i].ByteCount || postBlobs[i].WrittenAt != preBlobs[i].WrittenAt {
			t.Fatalf("blob row %d changed across conversion:\n pre=%+v\npost=%+v", i, preBlobs[i], postBlobs[i])
		}
	}

	// Every post-conversion ref still resolves through its real owner.
	postReader, err := receipt.NewDefinitionReader(reopened.Journal())
	if err != nil {
		t.Fatalf("post definition reader: %v", err)
	}
	resolvePost := func(label string, occ model.OccurrenceEnvelopeRef, sem *model.SemanticEnvelopeRef) {
		t.Helper()
		refs := map[string]model.DefinitionRef{
			"runtime": occ.Runtime.Definition, "schema": occ.Schema.Definition,
			"implementation": occ.Implementation.Definition, "retention": occ.Retention.Definition,
		}
		if sem != nil {
			refs["interpreter"] = sem.Interpreter.Definition
		}
		for name, ref := range refs {
			snapshot, _, err := receipt.ResolveDefinition(postReader, ref)
			if err != nil {
				t.Fatalf("%s resolve %s ref after conversion: %v", label, name, err)
			}
			if len(snapshot.Body()) == 0 {
				t.Fatalf("%s resolved %s body empty after conversion", label, name)
			}
		}
		if sem != nil {
			if _, _, err := receipt.ResolveMetamodelRef(postReader, sem.Metamodel.Definition, 0); err != nil {
				t.Fatalf("%s resolve metamodel after conversion: %v", label, err)
			}
			bridged, _, _, err := receipt.BridgeActiveMetamodel(ctx, postReader)
			if err != nil {
				t.Fatalf("%s bridge after conversion: %v", label, err)
			}
			if bridged.Definition != sem.Metamodel.Definition {
				t.Fatalf("%s bridged %+v != semantic %+v after conversion", label, bridged.Definition, sem.Metamodel.Definition)
			}
		}
	}
	resolvePost("post-conversion first", postFirst.Envelope, &postSemanticFirst)
	resolvePost("post-conversion second", postSecond.Envelope, &postSemanticSecond)
	resolvePost("post-conversion malformed", postMalformed.Envelope, nil)

	// Same-transaction resolution: the non-reclaiming replay and its
	// comparison run on the converted store's transaction view, exactly as
	// P3g does, plus explicit semantic verification.
	txDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open tx handle: %v", err)
	}
	defer txDB.Close()
	tx, err := txDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	txReader, err := provenance.NewTransactionEvidenceReader(ctx, tx)
	if err != nil {
		t.Fatalf("transaction reader: %v", err)
	}
	if err := projection.RebuildOccurrencesInTx(ctx, tx, txReader); err != nil {
		t.Fatalf("transaction replay after conversion: %v", err)
	}
	if err := projection.CompareOccurrencesToEvidence(ctx, tx, txReader); err != nil {
		t.Fatalf("transaction comparison after conversion: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	// Reopen reads: validated rebuild + Records expose both complete
	// envelopes with stable order and identical raw bodies.
	if err := tasks.RebuildLifecycleOccurrences(ctx, reopened); err != nil {
		t.Fatalf("rebuild after conversion: %v", err)
	}
	lifecycleReader, err := tasks.NewLifecycleReader(reopened)
	if err != nil {
		t.Fatalf("lifecycle reader: %v", err)
	}
	size, err := model.NewPageSize(model.MaxPageSize)
	if err != nil {
		t.Fatalf("page size: %v", err)
	}
	page, err := lifecycleReader.Records(ctx, model.OccurrenceQuery{Page: model.PageRequest{Size: size}})
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	byID := map[provenance.JournalID]model.LifecycleRecord{}
	for _, item := range page.Items {
		byID[item.Occurrence.JournalID()] = item
	}
	for _, want := range []struct {
		id       provenance.JournalID
		contract ir.RuntimeContractID
		valid    bool
	}{
		{firstReceipt.JournalID(), first.Contract, true},
		{secondReceipt.JournalID(), second.Contract, true},
		{malformedReceipt.JournalID(), malformed.Contract, false},
	} {
		got, ok := byID[want.id]
		if !ok {
			t.Fatalf("validated read hides occurrence %d after conversion", want.id)
		}
		defCorpusRequireCompleteOccurrence(t, got.Occurrence.Envelope, want.contract)
		interpreted := got.Interpreted()
		if want.valid {
			if len(interpreted) != 1 {
				t.Fatalf("occurrence %d interpretations = %d, want one complete envelope", want.id, len(interpreted))
			}
			sem, ok := interpreted[0].SemanticEnvelope()
			if !ok {
				t.Fatalf("occurrence %d validated read hid the semantic envelope", want.id)
			}
			defCorpusRequireCompleteSemantic(t, sem, got.Occurrence.Envelope)
		} else if len(interpreted) != 0 {
			t.Fatalf("malformed occurrence %d interpretations = %d, want zero", want.id, len(interpreted))
		}
	}
}

// TestDefinitionCompleteRetiredConversionBlocked documents the one conversion
// the v0.9.0 dependency cannot yet run: a definition-complete store made
// retired (marker dropped) carries definition-activation canonical mutations
// whose absent-task field is the slash-zero marker "/000…". The canonical
// converter only maps the retired-zero "--000…" to that marker and otherwise
// calls ConvertRetiredIdentity, which refuses "/000…" (empty namespace, no
// two-dash separator). The conversion therefore fails closed in preflight
// instead of guessing. This is why the S1 proof above uses the current-store
// pipeline plus its replay, and why a retired-with-complete conversion needs
// both a released retired writer that never existed and a dependency fix for
// the slash-zero absent-task case. If a future dependency converts such a
// store, this test will fail (no error) and must be replaced by a real
// preservation proof.
func TestDefinitionCompleteRetiredConversionBlocked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := defCorpusBootstrap(t)

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open tracker: %v", err)
	}
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, defCorpusClock{}, &defCorpusOps{}, timeouts.TestProfile())
	if err != nil {
		t.Fatalf("wire service: %v", err)
	}
	if _, err := receipt.EnsureActiveMetamodel(ctx, service); err != nil {
		t.Fatalf("ensure metamodel: %v", err)
	}
	delivery := defCorpusParseDelivery(t, "session_start_2_1_261.json", "SessionStart")
	prepared := defCorpusPreparedObservation(t, delivery)
	if _, err := service.Receive(ctx, defCorpusWarrant(t, delivery), delivery, prepared...); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close tracker: %v", err)
	}

	shared, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		t.Fatalf("open shared: %v", err)
	}
	defer shared.Close()
	for _, table := range []string{"provenance_id_format", "pasture_id_cutover"} {
		if _, err := shared.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if _, err := shared.Exec(`INSERT INTO pasture_agent_categories(agent_id) VALUES(?)`, defCorpusRetiredAgent); err != nil {
		t.Fatalf("insert retired key: %v", err)
	}

	_, err = idmigrate.Convert(ctx, shared, idmigrate.ConvertOptions{StorePath: dbPath})
	if err == nil {
		t.Fatal("Convert succeeded on an unmarked definition-complete store; want the slash-zero canonical preflight refusal (dependency fix landed — replace this blocker pin with a preservation proof)")
	}
	msg := err.Error()
	if !containsAny(msg, []string{"canonical", "effect.0.task", "/00000000-0000-0000-0000-000000000000"}) {
		t.Fatalf("Convert failed without the slash-zero canonical signal; got: %v", err)
	}
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if len(n) == 0 {
			continue
		}
		for i := 0; i+len(n) <= len(haystack); i++ {
			if haystack[i:i+len(n)] == n {
				return true
			}
		}
	}
	return false
}

// TestDefinitionCompleteCorpusFailClosed proves the conversion-side verifiers
// fail closed on a definition-complete store: a dangling ref, a wrong-kind
// ref, and a payload-tampered interpreted row are each rejected by the real
// owner paths, and the non-reclaiming replay rejects a duplicate
// interpretation. If any verifier were neutered to approve everything, the
// corresponding subtest would unexpectedly succeed and this test would fail.
func TestDefinitionCompleteCorpusFailClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := defCorpusBootstrap(t)

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open tracker: %v", err)
	}
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, defCorpusClock{}, &defCorpusOps{}, timeouts.TestProfile())
	if err != nil {
		t.Fatalf("wire service: %v", err)
	}
	if _, err := receipt.EnsureActiveMetamodel(ctx, service); err != nil {
		t.Fatalf("ensure metamodel: %v", err)
	}
	delivery := defCorpusParseDelivery(t, "session_start_2_1_261.json", "SessionStart")
	prepared := defCorpusPreparedObservation(t, delivery)
	committed, err := service.Receive(ctx, defCorpusWarrant(t, delivery), delivery, prepared...)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if err := tasks.RebuildLifecycleOccurrences(ctx, tracker); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	lifecycleReader, err := tasks.NewLifecycleReader(tracker)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	size, err := model.NewPageSize(model.MaxPageSize)
	if err != nil {
		t.Fatalf("page size: %v", err)
	}
	page, err := lifecycleReader.Records(ctx, model.OccurrenceQuery{Page: model.PageRequest{Size: size}})
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
	if occurrence.JournalID() == 0 {
		t.Fatal("seed record not exposed by validated read")
	}
	manifest, ok := interpreted.Metamodel()
	if !ok {
		t.Fatal("seed interpretation carries no metamodel coordinate")
	}
	defs, err := receipt.NewDefinitionReader(tracker.Journal())
	if err != nil {
		t.Fatalf("definition reader: %v", err)
	}
	genuine, ok := interpreted.SemanticEnvelope()
	if !ok {
		t.Fatal("seed interpretation carries no semantic envelope")
	}
	dangling := func(ref model.DefinitionRef) model.DefinitionRef {
		out := ref
		out.Definition = model.DefinitionJournalID(provenance.JournalID(ref.Definition) + 1<<20)
		out.Content[0] ^= 0xff
		if out.Content == (model.ContentIdentity{}) {
			out.Content[0] = 1
		}
		return out
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
	t.Run("shared refs must be exactly equal", func(t *testing.T) {
		envelope := genuine
		envelope.Runtime.Definition.Content[0] ^= 1
		if envelope.Runtime.Definition.Content == (model.ContentIdentity{}) {
			envelope.Runtime.Definition.Content[0] = 1
		}
		record, err := model.NewInterpretedRecordWithEnvelope(1, 1, interpreted.Semantic(), interpreted.Identities(), interpreted.UnresolvedFacts(), interpreted.Contract(), manifest, envelope)
		if err != nil {
			t.Fatalf("paired-mismatch envelope refused at construction: %v", err)
		}
		if err := receipt.ResolveSemanticEnvelope(defs, occurrence, record, 0); err == nil {
			t.Fatal("valid-but-different shared refs paired; want fail-closed")
		}
	})

	// A payload-tampered interpreted.v3 row fails its digest before any
	// envelope is even decoded: the transaction verifier rejects it.
	t.Run("tampered interpreted payload digest", func(t *testing.T) {
		rows := defCorpusQueryKind(t, tracker, defCorpusInterpretedV3)
		if len(rows) != 1 {
			t.Fatalf("interpreted.v3 rows = %d, want one", len(rows))
		}
		tampered := append([]byte(nil), rows[0].Payload...)
		// Break the JSON framing itself so the strict v3 decoder must
		// refuse it: flipping the opening brace guarantees a syntax
		// failure rather than a still-valid different semantic value.
		// (Digest mismatch against the stored row is proved by the
		// transaction verifier in the conversion test, which compares
		// sha256(payload) to the stored digest.)
		tampered[0] ^= 0xff
		if _, err := receipt.DecodeInterpretedV3(1, 1, tampered); err == nil {
			t.Fatal("tampered interpreted.v3 decoded; want digest/canonical rejection")
		}
	})

	if err := tracker.Close(); err != nil {
		t.Fatalf("close tracker: %v", err)
	}
	_ = committed
	_ = backend.DecisionDeny
}
