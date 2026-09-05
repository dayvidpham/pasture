package projection_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
)

// goldenV1InterpretedPayload is the SAME frozen committed interpreted.v1
// evidence payload pinned by the receipt package's golden_v1_test.go
// (goldenV1InterpretedSHA256). It is duplicated here as an explicit provenance
// pin: this integration test writes THESE EXACT bytes into a real store and
// proves they survive the full production read path unchanged. If the frozen
// bytes ever drift, goldenV1InterpretedSHA256 below fails first.
const goldenV1InterpretedPayload = `{"semantic":1,"identities":[{"kind":1,"value":"session-golden-pre-m5"}],"unresolved_facts":[],"contract":"claude-code/claude-code@2.1.210"}`

// goldenV1InterpretedSHA256 pins the golden fixture's provenance, matching the
// receipt package pin. A silent migration of committed v1 evidence fails here.
const goldenV1InterpretedSHA256 = "2038445fa70b46043ef21e0105f3ee1c0fd2bc4f6ebe749de8ecf24170e46563"

// persistedOccurrenceEnvelope is the canonical occurrence.v1 envelope shape the
// projection rebuild decodes (json tags identical to the producer's
// receipt.occurrencePayload and the reader's projection.occurrencePayload). The
// integration test builds one directly so it can commit a pre-M5 occurrence +
// interpreted.v1 pair the way the pre-M5 receipt service did, before Receive was
// tightened to interpreted.v2-only extras.
type persistedOccurrenceEnvelope struct {
	Contract string                      `json:"contract"`
	Event    model.ContractEventKind     `json:"event"`
	Envelope model.OccurrenceEnvelopeRef `json:"envelope"`
	Bindings []model.NativeBinding       `json:"bindings"`
	Capture  model.CaptureDisposition    `json:"capture"`
	Body     string                      `json:"body_digest"`
}

// TestPersistedV1InterpretedReadsBackThroughProductionReader closes the S2
// residual review gap (aura-plugins-iz9a7b): before this test, no single test
// wrote a v1 interpreted record into a REAL store and read it back through the
// production projection.Reader. v1 compatibility was proven only compositionally
// (SHA-256 golden decode + dual-kind dispatch) and the kind-agnostic store path
// only via v2 e2e.
//
// This test commits ONE operation carrying a pasture.lifecycle.occurrence.v1
// effect and the SHA-256-pinned pasture.lifecycle.interpreted.v1 golden payload
// into a real temp SQLite store, rebuilds the disposable occurrence projection,
// and reads it back through the exact production reader
// (tasks.NewLifecycleReader → projection.Reader). It asserts the v1 record
// decodes UNCHANGED (its golden identities, semantic, and contract survive), that
// it carries NO metamodel coordinate (Metamodel() reports false), and that the
// production text formatter discloses "metamodel=unresolved (pre-M5)" rather than
// inventing a coordinate.
//
// Because Records() runs the cursorless read, it traverses BOTH dual-kind sites:
// the global watermark query (occurrences()) and the operation-filtered
// interpreted association query (interpretations()), each of which admits BOTH
// interpreted.v1 and interpreted.v2. A single v1 record read back end-to-end
// therefore proves the dual-kind reader associates a v1 record correctly at both
// sites — the exact path a v1-only store exercises.
func TestPersistedV1InterpretedReadsBackThroughProductionReader(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The golden bytes must match the frozen receipt-package pin.
	sum := sha256.Sum256([]byte(goldenV1InterpretedPayload))
	if got := hex.EncodeToString(sum[:]); got != goldenV1InterpretedSHA256 {
		t.Fatalf("golden v1 payload provenance drifted: sha256 = %s, pinned %s", got, goldenV1InterpretedSHA256)
	}

	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	// Bootstrap the persisted system identity so the store is fully functional
	// and the real identity resolver can attribute the committed operation.
	bootstrap, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open bootstrap tracker: %v", err)
	}
	if _, err := bootstrap.Create("file://projection-persisted-v1-test", "bootstrap", "initialize the persisted ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
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

	occurrenceID := commitPreM5V1Record(ctx, t, tracker)

	if err := tasks.RebuildLifecycleOccurrences(ctx, tracker); err != nil {
		t.Fatalf("rebuild occurrences: %v", err)
	}
	reader, err := tasks.NewLifecycleReader(tracker)
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	page, err := reader.Records(ctx, model.OccurrenceQuery{Page: model.PageRequest{Size: model.MaxPageSize}})
	if err != nil {
		t.Fatalf("read records: %v", err)
	}
	records := page.Records()
	if len(records) != 1 {
		t.Fatalf("production reader returned %d records, want exactly the one committed v1 occurrence", len(records))
	}
	record := records[0]
	if record.Occurrence.JournalID() != occurrenceID.JournalID() {
		t.Fatalf("read occurrence journal id = %d, want %d", record.Occurrence.JournalID(), occurrenceID.JournalID())
	}

	interpreted := record.Interpreted()
	if len(interpreted) != 1 {
		t.Fatalf("v1 occurrence read back with %d interpretations, want exactly one (associated at both dual-kind sites)", len(interpreted))
	}
	got := interpreted[0]
	if got.Semantic() != runtime.SemanticObservation {
		t.Fatalf("read-back interpreted semantic = %v, want observation (golden decoded unchanged)", got.Semantic())
	}
	if got.Contract().String() != "claude-code/claude-code@2.1.210" {
		t.Fatalf("read-back interpreted contract = %q, want claude-code/claude-code@2.1.210", got.Contract())
	}
	identities := got.Identities()
	if len(identities) != 1 || identities[0].Kind != runtime.IdentitySession || identities[0].Value != "session-golden-pre-m5" {
		t.Fatalf("read-back interpreted identities = %#v, want the single golden session identity", identities)
	}
	if manifest, ok := got.Metamodel(); ok {
		t.Fatalf("persisted interpreted.v1 record reported a metamodel coordinate %#v, want none (pre-M5, unresolved)", manifest)
	}

	// The production text read surface discloses the pre-M5 unresolved metamodel
	// rather than inventing one.
	var buf bytes.Buffer
	if err := formatters.HookLifecycle(&buf, page, "text"); err != nil {
		t.Fatalf("render lifecycle page: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("metamodel=unresolved (pre-M5)")) {
		t.Fatalf("production text read surface did not disclose the pre-M5 unresolved metamodel:\n%s", buf.String())
	}
}

// commitPreM5V1Record appends ONE operation carrying an occurrence.v1 effect and
// the golden interpreted.v1 effect through the real store's context journal,
// exactly as the pre-M5 receipt service committed an occurrence and its
// interpretation together. It resolves the persisted lifecycle identity through
// the production resolver so the committed operation is attributable, then
// canonicalizes and re-digests the effects the same way the production commit
// pipeline does. It returns the committed occurrence journal identity.
func commitPreM5V1Record(ctx context.Context, t *testing.T, tracker protocol.TaskTracker) model.OccurrenceID {
	t.Helper()

	resolver, ok := tracker.(receipt.IdentityResolver)
	if !ok {
		t.Fatalf("tracker %T does not resolve lifecycle identity", tracker)
	}
	identity, err := resolver.ResolveLifecycleIdentity(ctx)
	if err != nil {
		t.Fatalf("resolve lifecycle identity: %v", err)
	}

	// This record is FROZEN at the host version that wrote it. The literal below
	// is a historical fact about a record committed by the pre-M5 receipt
	// service, not a ceiling that moves: it never follows the current host
	// version root, and a bump of that root must leave this line alone. The id
	// is used only to build the occurrence envelope of the record this test
	// commits; it is never compared with the frozen interpreted bytes or with
	// their sha256 pin.
	contract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "claude-code/2.1.210")
	if err != nil {
		t.Fatalf("construct runtime contract: %v", err)
	}

	// The occurrence row references its content-addressed payload blob under a
	// foreign key, so the blob must exist before the projection is rebuilt. Store
	// it through the production blob store (receipt.SQLiteBlobStore) obtained from
	// the same unified database the reader reads.
	body := []byte("pre-m5-golden-v1-body")
	bodyRef := digest.FromBytes(body)
	if err := (receipt.SQLiteBlobStore{DB: auditDB(t, tracker)}).Put(ctx, bodyRef, body); err != nil {
		t.Fatalf("store occurrence payload blob: %v", err)
	}

	occPayload, err := json.Marshal(persistedOccurrenceEnvelope{
		Contract: contract.String(),
		Event:    model.ContractEventKind(3),
		// Runtime.Contract must be a constructor-valid contract: a zero
		// ir.RuntimeContractID refuses to marshal, so the envelope carries the
		// same contract the occurrence names (the shape a real committed
		// occurrence stores and the rebuild re-marshals).
		Envelope: model.OccurrenceEnvelopeRef{Runtime: model.RuntimeContractDefinitionRef{Contract: contract}},
		Bindings: nil,
		Capture:  model.CaptureValid,
		Body:     bodyRef.String(),
	})
	if err != nil {
		t.Fatalf("encode occurrence envelope: %v", err)
	}
	interpretedPayload := []byte(goldenV1InterpretedPayload)

	occDigest := sha256.Sum256(occPayload)
	interpretedDigest := sha256.Sum256(interpretedPayload)
	command := sha256.Sum256(append([]byte("pasture.lifecycle.receipt.append/v1\x00"), occPayload...))
	authority := identity.Authority
	input := provenance.OperationInput{
		OperationID:        provenance.OperationID("pasture.lifecycle.receipt.persisted-golden-v1"),
		ActorID:            identity.Actor,
		AuthorityJournalID: &authority,
		CommandDigest:      command[:],
		RecordedAt:         time.Unix(0, 1).UTC().UnixNano(),
		Effects: []provenance.Effect{
			{Sort: provenance.EffectEvidence, ResultSlot: provenance.ResultSlotID("occurrence"), EvidenceKind: provenance.EvidenceKind("pasture.lifecycle.occurrence.v1"), ContentDigest: occDigest[:], Payload: append(json.RawMessage(nil), occPayload...)},
			{Sort: provenance.EffectEvidence, ResultSlot: provenance.ResultSlotID("interpreted"), EvidenceKind: provenance.EvidenceKind("pasture.lifecycle.interpreted.v1"), ContentDigest: interpretedDigest[:], Payload: append(json.RawMessage(nil), interpretedPayload...)},
		},
	}
	// Canonicalize and re-digest over the journal's normalized payloads exactly
	// as the production commit pipeline does, so the committed rows survive the
	// reader's per-row digest validation and canonical-payload decode.
	canonical, err := provenance.Canonicalize(input)
	if err != nil {
		t.Fatalf("canonicalize operation: %v", err)
	}
	input.Effects = canonical.NormalizedEffects()
	for index := range input.Effects {
		if input.Effects[index].Sort == provenance.EffectEvidence {
			sum := sha256.Sum256(input.Effects[index].Payload)
			input.Effects[index].ContentDigest = append([]byte(nil), sum[:]...)
		}
	}

	journal, ok := tracker.Journal().(provenance.ContextJournal)
	if !ok {
		t.Fatalf("store journal %T does not implement provenance.ContextJournal", tracker.Journal())
	}
	result, err := journal.ApplyContext(ctx, input)
	if err != nil {
		t.Fatalf("commit pre-M5 v1 operation: %v", err)
	}
	var occurrenceID model.OccurrenceID
	for _, slot := range result.ResultSlots {
		if slot.Slot == provenance.ResultSlotID("occurrence") {
			occurrenceID = model.OccurrenceID(slot.ProducedJournalID)
		}
	}
	if occurrenceID.JournalID() == 0 {
		t.Fatalf("committed operation produced no occurrence result slot: %#v", result.ResultSlots)
	}
	return occurrenceID
}

// auditDB returns the unified projection database handle the production reader is
// backed by, so a test can seed the content-addressed payload blob store the
// occurrence projection references. It reaches it through the exact production
// wiring (tasks.NewLifecycleReader → projection.Reader) rather than a test-only
// accessor.
func auditDB(t *testing.T, tracker protocol.TaskTracker) *sql.DB {
	t.Helper()
	reader, err := tasks.NewLifecycleReader(tracker)
	if err != nil {
		t.Fatalf("new reader for db handle: %v", err)
	}
	concrete, ok := reader.(projection.Reader)
	if !ok || concrete.DB == nil {
		t.Fatalf("production reader %T does not expose the unified database handle", reader)
	}
	return concrete.DB
}
