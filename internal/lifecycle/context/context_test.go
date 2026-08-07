package context_test

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	lifecyclecontext "github.com/dayvidpham/pasture/internal/lifecycle/context"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

// These tests exercise the production projection engine (context.Project)
// directly — it is the exact pure code path the `pasture hook lifecycle context`
// command runs. There is no test-only export: the command and these tests call
// the same function. Project's signature takes no context.Context, clock, or
// store, so its purity is compile-enforced; these tests pin its determinism,
// canonical encoding, and content bindings.

func mustContract(t *testing.T, harness ir.HarnessID, name string) ir.RuntimeContractID {
	t.Helper()
	contract, err := ir.NewRuntimeContractID(harness, name)
	if err != nil {
		t.Fatalf("construct runtime contract %s/%s: %v", harness, name, err)
	}
	return contract
}

func book(content byte) model.CodebookCoordinate {
	var c model.ContentIdentity
	c[0] = content
	return model.CodebookCoordinate{ID: "pasture.lifecycle.codebook", Version: 1, Content: c}
}

// recordV2 builds a committed LifecycleRecord whose interpretation carries a
// codebook coordinate (interpreted.v2), using the production model constructors.
func recordV2(t *testing.T, jid int64, contract ir.RuntimeContractID, coordinate model.CodebookCoordinate, identities ...waist.SemanticIdentity) model.LifecycleRecord {
	t.Helper()
	occ := model.NewOccurrenceRecord(
		model.OccurrenceID(jid),
		model.ContractEventKind(7),
		contract,
		model.OccurrenceEnvelopeRef{},
		time.Unix(0, jid).UTC(),
		provenance.AgentID{},
		nil,
		model.CaptureValid,
		model.EvidencePayloadRef{},
	)
	interpreted, err := model.NewInterpretedRecordWithCodebook(
		model.InterpretationID(jid), model.OccurrenceID(jid), runtime.SemanticObservation,
		identities, nil, contract, coordinate)
	if err != nil {
		t.Fatalf("construct interpreted.v2 record %d: %v", jid, err)
	}
	lifecycle, err := model.NewLifecycleRecord(occ, []model.InterpretedRecord{interpreted})
	if err != nil {
		t.Fatalf("construct lifecycle record %d: %v", jid, err)
	}
	return lifecycle
}

// recordV1 builds a committed LifecycleRecord whose interpretation predates the
// codebook (interpreted.v1) — Codebook() is false, so the projection discloses
// an unresolved coordinate.
func recordV1(t *testing.T, jid int64, contract ir.RuntimeContractID, identities ...waist.SemanticIdentity) model.LifecycleRecord {
	t.Helper()
	occ := model.NewOccurrenceRecord(
		model.OccurrenceID(jid), model.ContractEventKind(3), contract,
		model.OccurrenceEnvelopeRef{}, time.Unix(0, jid).UTC(), provenance.AgentID{}, nil,
		model.CaptureValid, model.EvidencePayloadRef{})
	interpreted, err := model.NewInterpretedRecord(
		model.InterpretationID(jid), model.OccurrenceID(jid), runtime.SemanticObservation,
		identities, nil, contract)
	if err != nil {
		t.Fatalf("construct interpreted.v1 record %d: %v", jid, err)
	}
	lifecycle, err := model.NewLifecycleRecord(occ, []model.InterpretedRecord{interpreted})
	if err != nil {
		t.Fatalf("construct lifecycle record %d: %v", jid, err)
	}
	return lifecycle
}

func id(kind runtime.NativeIdentityKind, value string) waist.SemanticIdentity {
	return waist.SemanticIdentity{Kind: kind, Value: value}
}

func link(linkID, from, to int64) model.LinkRecord {
	return model.LinkRecord{
		LinkID:  model.LifecycleLinkID(linkID),
		Harness: ir.HarnessClaudeCode,
		Kind:    runtime.IdentitySession,
		Value:   "S1",
		From:    model.OccurrenceID(from),
		To:      model.OccurrenceID(to),
	}
}

func scopedInput() lifecyclecontext.ContextInput {
	return lifecyclecontext.ContextInput{Scope: model.OccurrenceQuery{
		Bindings: []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "S1"}},
	}}
}

// TestProjectDeterministicAndCanonical: the same inputs (even shuffled) yield a
// byte-identical canonical projection and a stable digest; the digest binds the
// content, so different inputs produce a different digest.
func TestProjectDeterministicAndCanonical(t *testing.T) {
	t.Parallel()
	claude := mustContract(t, ir.HarnessClaudeCode, "claude-code/2.1.210")
	coordinate := book(0x11)
	records := []model.LifecycleRecord{
		recordV2(t, 30, claude, coordinate, id(runtime.IdentitySession, "S1")),
		recordV2(t, 10, claude, coordinate, id(runtime.IdentitySession, "S1")),
		recordV2(t, 20, claude, coordinate, id(runtime.IdentitySession, "S1")),
	}
	links := []model.LinkRecord{link(1, 10, 20), link(2, 20, 30)}

	a, err := lifecyclecontext.Project(scopedInput(), records, links)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	// Shuffle input order — the projection must be identical (records are sorted
	// by occurrence journal identity inside Project).
	shuffled := []model.LifecycleRecord{records[1], records[2], records[0]}
	b, err := lifecyclecontext.Project(scopedInput(), shuffled, links)
	if err != nil {
		t.Fatalf("Project(shuffled): %v", err)
	}

	aJSON, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal a: %v", err)
	}
	bJSON, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal b: %v", err)
	}
	if string(aJSON) != string(bJSON) {
		t.Fatalf("canonical projection must be order-independent:\n a=%s\n b=%s", aJSON, bJSON)
	}
	aDigest, err := a.Digest()
	if err != nil {
		t.Fatalf("digest a: %v", err)
	}
	bDigest, err := b.Digest()
	if err != nil {
		t.Fatalf("digest b: %v", err)
	}
	if aDigest != bDigest {
		t.Fatal("identical projections must share a digest")
	}
	// The digest is exactly sha256 of the canonical MarshalJSON bytes.
	if aDigest != model.ContentIdentity(sha256.Sum256(aJSON)) {
		t.Fatal("Digest must equal sha256 of the canonical MarshalJSON bytes")
	}

	// A different selection (different codebook content) changes the digest.
	other, err := lifecyclecontext.Project(scopedInput(),
		[]model.LifecycleRecord{recordV2(t, 10, claude, book(0x22), id(runtime.IdentitySession, "S1"))}, nil)
	if err != nil {
		t.Fatalf("Project(other): %v", err)
	}
	otherDigest, err := other.Digest()
	if err != nil {
		t.Fatalf("digest other: %v", err)
	}
	if otherDigest == aDigest {
		t.Fatal("different content must produce a different projection digest")
	}
}

// TestProjectSummarizesChainsCodebooksAndUnresolved: the projection carries the
// per-host chain edge count from committed links, the deduplicated codebook
// coordinates present, and discloses a pre-M5 (v1) record with an unresolved
// (empty) codebook.
func TestProjectSummarizesChainsCodebooksAndUnresolved(t *testing.T) {
	t.Parallel()
	claude := mustContract(t, ir.HarnessClaudeCode, "claude-code/2.1.210")
	coordinate := book(0x33)
	records := []model.LifecycleRecord{
		recordV2(t, 10, claude, coordinate, id(runtime.IdentitySession, "S1")),
		recordV2(t, 20, claude, coordinate, id(runtime.IdentitySession, "S1")),
		recordV1(t, 30, claude, id(runtime.IdentitySession, "S1")),
	}
	links := []model.LinkRecord{link(1, 10, 20), link(2, 20, 30)}
	projection, err := lifecyclecontext.Project(scopedInput(), records, links)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out struct {
		Records []struct {
			Occurrence int64  `json:"occurrence"`
			Codebook   string `json:"codebook"`
		} `json:"records"`
		Chains []struct {
			Harness string `json:"harness"`
			Kind    string `json:"kind"`
			Value   string `json:"value"`
			Edges   int    `json:"edges"`
		} `json:"chains"`
		Codebooks []struct {
			ID      string `json:"id"`
			Version uint32 `json:"version"`
			Content string `json:"content"`
		} `json:"codebooks"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal projection: %v", err)
	}
	if len(out.Records) != 3 {
		t.Fatalf("expected 3 record summaries, got %d", len(out.Records))
	}
	unresolved := 0
	for _, r := range out.Records {
		if r.Occurrence == 30 {
			if r.Codebook != "" {
				t.Fatalf("a pre-M5 v1 record must disclose an empty (unresolved) codebook, got %q", r.Codebook)
			}
			unresolved++
		}
	}
	if unresolved != 1 {
		t.Fatalf("expected exactly one unresolved (v1) record, got %d", unresolved)
	}
	if len(out.Codebooks) != 1 {
		t.Fatalf("expected one deduplicated codebook coordinate, got %d", len(out.Codebooks))
	}
	if len(out.Chains) != 1 {
		t.Fatalf("expected one per-host session chain, got %d: %+v", len(out.Chains), out.Chains)
	}
	if out.Chains[0].Edges != 2 || out.Chains[0].Kind != "session" || out.Chains[0].Value != "S1" {
		t.Fatalf("chain summary lost its edge count or identity: %+v", out.Chains[0])
	}
	if out.Truncated {
		t.Fatal("a small selection must not be marked truncated")
	}
}

// TestProjectBoundsAndMarksTruncation: a selection wider than MaxProjectionRecords
// is disclosed bounded with an explicit truncation marker rather than releasing
// an unbounded projection.
func TestProjectBoundsAndMarksTruncation(t *testing.T) {
	t.Parallel()
	claude := mustContract(t, ir.HarnessClaudeCode, "claude-code/2.1.210")
	coordinate := book(0x44)
	records := make([]model.LifecycleRecord, lifecyclecontext.MaxProjectionRecords+5)
	for i := range records {
		records[i] = recordV2(t, int64(i+1), claude, coordinate, id(runtime.IdentitySession, "S1"))
	}
	projection, err := lifecyclecontext.Project(scopedInput(), records, nil)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out struct {
		Records   []json.RawMessage `json:"records"`
		Truncated bool              `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Records) != lifecyclecontext.MaxProjectionRecords {
		t.Fatalf("projection must be bounded to %d records, got %d", lifecyclecontext.MaxProjectionRecords, len(out.Records))
	}
	if !out.Truncated {
		t.Fatal("an over-bound selection must be explicitly marked truncated")
	}
}

// TestZeroProjectionMarshalRefused: the zero ContextProjection is not constructed
// and refuses to marshal, so a disclosure can never release or fingerprint an
// unbuilt projection.
func TestZeroProjectionMarshalRefused(t *testing.T) {
	t.Parallel()
	var zero lifecyclecontext.ContextProjection
	if zero.IsValid() {
		t.Fatal("the zero projection must not be valid")
	}
	if _, err := json.Marshal(zero); err == nil {
		t.Fatal("marshaling an unconstructed projection must be refused")
	}
}
