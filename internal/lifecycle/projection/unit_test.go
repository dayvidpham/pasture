package projection

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

// These are focused projection-PACKAGE unit tests (aura-plugins-zxvzjd): before
// this file the projection package reported "no test files" and LinkReader plus
// the dual-kind Reader association/error branches were exercised only through the
// cmd e2e. Each test drives the REAL projection reader (LinkReader.Links and the
// unexported Reader.interpretations, which is the core of Records) against a
// stubbed provenance.FactQueryAPI: the dependency is mocked, never the system
// under test, so a hand-crafted corrupt journal view can exercise error branches
// (snapshot drift, cross-operation association, duplicate-across-kinds,
// digest-validation) that a healthy real store cannot produce.

// canonicalV1Interpreted is a canonical interpreted.v1 payload (no codebook
// member) — the pre-M5 read shape DecodeInterpreted accepts.
const canonicalV1Interpreted = `{"semantic":1,"identities":[{"kind":1,"value":"session-v1"}],"unresolved_facts":[],"contract":"claude-code/claude-code@2.1.210"}`

// canonicalV2Interpreted is a canonical interpreted.v2 payload carrying a
// versioned codebook coordinate — the post-M5 read shape DecodeInterpretedV2
// accepts. Its content is a fixed nonzero sha256 hex so the coordinate is valid.
const canonicalV2Interpreted = `{"semantic":1,"identities":[{"kind":1,"value":"session-v2"}],"unresolved_facts":[],"contract":"claude-code/claude-code@2.1.210","codebook":{"id":"pasture.lifecycle.codebook","version":1,"content":"1111111111111111111111111111111111111111111111111111111111111111"}}`

// canonicalLink is a canonical committed link payload DecodeLink accepts (its
// field order matches the receipt package's linkPayload encoder).
func canonicalLink(from, to int64) string {
	return `{"harness":"claude-code","kind":1,"value":"S1","from":` + itoa(from) + `,"to":` + itoa(to) + `}`
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// stubFacts is a controllable provenance.FactQueryAPI. It routes evidence
// queries by kind so one stub can serve the occurrence-association query, the
// operation-filtered interpreted query, and the link query. Fields let a test
// inject exactly the corrupt or drifted view a branch requires.
type stubFacts struct {
	occurrenceRows  []provenance.EvidenceRow
	interpretedRows []provenance.EvidenceRow
	linkRows        []provenance.EvidenceRow

	snapshot            provenance.JournalID
	interpretedSnapshot provenance.JournalID // 0 => use snapshot
	linkPageSnapshot    provenance.JournalID // 0 => use snapshot (drift injection)

	rawInterpreted bool // return interpretedRows unfiltered (corrupt-journal simulation)
	err            error

	calls []provenance.EvidenceQuery
}

func (s *stubFacts) QueryDecisions(provenance.DecisionQuery) (provenance.DecisionPage, error) {
	return provenance.DecisionPage{}, nil
}

func (s *stubFacts) QueryEvidence(q provenance.EvidenceQuery) (provenance.EvidencePage, error) {
	s.calls = append(s.calls, q)
	if s.err != nil {
		return provenance.EvidencePage{}, s.err
	}
	switch {
	case kindsContain(q.Kinds, occurrenceEvidenceKind):
		return provenance.EvidencePage{Rows: s.occurrenceRows, SnapshotMaxJournalID: s.snapshot}, nil
	case kindsContain(q.Kinds, lineageLinkKind):
		snap := s.snapshot
		rows := s.linkRows
		if q.Page.Limit == 1 { // watermark probe returns no rows
			rows = nil
		} else if s.linkPageSnapshot != 0 {
			snap = s.linkPageSnapshot // inject drift on the paginated read
		}
		return provenance.EvidencePage{Rows: rows, SnapshotMaxJournalID: snap}, nil
	default: // interpreted kinds (v1 + v2)
		snap := s.snapshot
		if s.interpretedSnapshot != 0 {
			snap = s.interpretedSnapshot
		}
		rows := s.interpretedRows
		if !s.rawInterpreted {
			rows = filterByOperations(rows, q.Filter.OperationIDs)
		}
		return provenance.EvidencePage{Rows: rows, SnapshotMaxJournalID: snap}, nil
	}
}

func kindsContain(kinds []provenance.EvidenceKind, want provenance.EvidenceKind) bool {
	for _, k := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

func filterByOperations(rows []provenance.EvidenceRow, ops []provenance.OperationID) []provenance.EvidenceRow {
	if len(ops) == 0 {
		return nil
	}
	want := make(map[provenance.OperationID]struct{}, len(ops))
	for _, op := range ops {
		want[op] = struct{}{}
	}
	out := make([]provenance.EvidenceRow, 0, len(rows))
	for _, row := range rows {
		if _, ok := want[row.ProducingOperationID]; ok {
			out = append(out, row)
		}
	}
	return out
}

func evidenceRow(jid int64, op string, kind provenance.EvidenceKind, payload string) provenance.EvidenceRow {
	sum := sha256.Sum256([]byte(payload))
	return provenance.EvidenceRow{
		JournalID:            provenance.JournalID(jid),
		ProducingOperationID: provenance.OperationID(op),
		EvidenceKind:         kind,
		ContentDigest:        sum[:],
		Payload:              []byte(payload),
	}
}

func occurrence(t *testing.T, jid int64) model.OccurrenceRecord {
	t.Helper()
	contract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "claude-code/2.1.210")
	if err != nil {
		t.Fatalf("construct runtime contract: %v", err)
	}
	return model.NewOccurrenceRecord(
		model.OccurrenceID(jid), model.ContractEventKind(3), contract,
		model.OccurrenceEnvelopeRef{}, time.Unix(0, jid).UTC(), provenance.AgentID{}, nil,
		model.CaptureValid, model.EvidencePayloadRef{})
}

// ---------------------------------------------------------------------------
// LinkReader
// ---------------------------------------------------------------------------

// TestLinkReaderValidatesDigestsAndSorts is the F18 read-discipline proof: every
// committed link row is digest-validated against its canonical payload before
// decoding, and the result is returned sorted by committed journal identity.
func TestLinkReaderValidatesDigestsAndSorts(t *testing.T) {
	t.Parallel()
	facts := &stubFacts{
		snapshot: 42,
		linkRows: []provenance.EvidenceRow{
			evidenceRow(9, "op-1", lineageLinkKind, canonicalLink(20, 30)),
			evidenceRow(5, "op-1", lineageLinkKind, canonicalLink(10, 20)),
		},
	}
	links, err := LinkReader{Facts: facts}.Links()
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("read %d links, want 2", len(links))
	}
	if links[0].LinkID.JournalID() != 5 || links[1].LinkID.JournalID() != 9 {
		t.Fatalf("links not sorted by journal id: %d, %d", links[0].LinkID.JournalID(), links[1].LinkID.JournalID())
	}
	if links[0].From != model.OccurrenceID(10) || links[0].To != model.OccurrenceID(20) {
		t.Fatalf("first link endpoints = (%d,%d), want (10,20)", links[0].From, links[0].To)
	}
	if links[0].Kind != runtime.IdentitySession || links[0].Harness != ir.HarnessClaudeCode {
		t.Fatalf("first link identity = %v/%v, want session/claude-code", links[0].Kind, links[0].Harness)
	}
}

// TestLinkReaderRefusesDigestMismatch: a link row whose stored digest differs
// from its payload is refused before decoding (F18), not silently returned.
func TestLinkReaderRefusesDigestMismatch(t *testing.T) {
	t.Parallel()
	bad := evidenceRow(5, "op-1", lineageLinkKind, canonicalLink(10, 20))
	bad.ContentDigest = make([]byte, sha256.Size) // zeroed digest ≠ sha256(payload)
	facts := &stubFacts{snapshot: 42, linkRows: []provenance.EvidenceRow{bad}}
	_, err := LinkReader{Facts: facts}.Links()
	if err == nil || !strings.Contains(err.Error(), "digest validation") {
		t.Fatalf("expected a digest-validation refusal, got %v", err)
	}
}

// TestLinkReaderRefusesSnapshotDrift: if a page's watermark differs from the
// pinned snapshot (a concurrent write tore the view), the read is refused rather
// than returning a partial page.
func TestLinkReaderRefusesSnapshotDrift(t *testing.T) {
	t.Parallel()
	facts := &stubFacts{
		snapshot:         42,
		linkPageSnapshot: 43, // paginated read reports a drifted watermark
		linkRows:         []provenance.EvidenceRow{evidenceRow(5, "op-1", lineageLinkKind, canonicalLink(10, 20))},
	}
	_, err := LinkReader{Facts: facts}.Links()
	if err == nil || !strings.Contains(err.Error(), "snapshot drifted") {
		t.Fatalf("expected a snapshot-drift refusal, got %v", err)
	}
}

// TestLinkReaderRefusesMalformedLink: a noncanonical/self-referential link row is
// refused by DecodeLink rather than admitted.
func TestLinkReaderRefusesMalformedLink(t *testing.T) {
	t.Parallel()
	facts := &stubFacts{
		snapshot: 42,
		linkRows: []provenance.EvidenceRow{evidenceRow(5, "op-1", lineageLinkKind, canonicalLink(10, 10))},
	}
	_, err := LinkReader{Facts: facts}.Links()
	if err == nil || !strings.Contains(err.Error(), "malformed or noncanonical") {
		t.Fatalf("expected a malformed-link refusal, got %v", err)
	}
}

// TestLinkReaderEmptyWhenNoLinks: a zero watermark means no committed links, so
// the reader returns an empty result without a page read.
func TestLinkReaderEmptyWhenNoLinks(t *testing.T) {
	t.Parallel()
	facts := &stubFacts{snapshot: 0}
	links, err := LinkReader{Facts: facts}.Links()
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if links != nil {
		t.Fatalf("expected no links, got %d", len(links))
	}
}

// TestLinkReaderUnwiredRefused: an unwired reader (no fact query dependency) is
// refused with an actionable error rather than panicking.
func TestLinkReaderUnwiredRefused(t *testing.T) {
	t.Parallel()
	_, err := LinkReader{}.Links()
	if err == nil || !strings.Contains(err.Error(), "incompletely wired") {
		t.Fatalf("expected an incompletely-wired refusal, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Dual-kind Reader (interpretations association site)
// ---------------------------------------------------------------------------

// TestReaderDispatchesV1AndV2ByKind proves the dual-kind association site: a v1
// interpreted row decodes through DecodeInterpreted (no codebook coordinate) and
// a v2 row through DecodeInterpretedV2 (carrying its coordinate), each associated
// to its own occurrence. It also asserts the interpreted association query is
// issued for BOTH interpreted kinds, matching the watermark site which queries
// the same interpretedKinds set.
func TestReaderDispatchesV1AndV2ByKind(t *testing.T) {
	t.Parallel()
	facts := &stubFacts{
		snapshot: 100,
		occurrenceRows: []provenance.EvidenceRow{
			evidenceRow(10, "op-v1", occurrenceEvidenceKind, `{}`),
			evidenceRow(20, "op-v2", occurrenceEvidenceKind, `{}`),
		},
		interpretedRows: []provenance.EvidenceRow{
			evidenceRow(11, "op-v1", interpretedKind, canonicalV1Interpreted),
			evidenceRow(21, "op-v2", interpretedKindV2, canonicalV2Interpreted),
		},
	}
	reader := Reader{Facts: facts}
	result, err := reader.interpretations([]model.OccurrenceRecord{occurrence(t, 10), occurrence(t, 20)}, 100)
	if err != nil {
		t.Fatalf("interpretations: %v", err)
	}
	v1 := result[10]
	if len(v1) != 1 {
		t.Fatalf("occurrence 10 has %d interpretations, want 1", len(v1))
	}
	if _, ok := v1[0].Codebook(); ok {
		t.Fatal("interpreted.v1 record must report NO codebook coordinate")
	}
	if v1[0].Identities()[0].Value != "session-v1" {
		t.Fatalf("v1 identity = %q, want session-v1", v1[0].Identities()[0].Value)
	}
	v2 := result[20]
	if len(v2) != 1 {
		t.Fatalf("occurrence 20 has %d interpretations, want 1", len(v2))
	}
	book, ok := v2[0].Codebook()
	if !ok {
		t.Fatal("interpreted.v2 record must report its codebook coordinate")
	}
	if book.Version != 1 || string(book.ID) != "pasture.lifecycle.codebook" {
		t.Fatalf("v2 codebook coordinate = %#v, want the versioned active id", book)
	}

	// The association query must ask for BOTH interpreted kinds (the same set the
	// watermark site uses); otherwise a v1-only or v2-only store loses records.
	var interpretedQuery *provenance.EvidenceQuery
	for i := range facts.calls {
		if kindsContain(facts.calls[i].Kinds, interpretedKind) || kindsContain(facts.calls[i].Kinds, interpretedKindV2) {
			interpretedQuery = &facts.calls[i]
			break
		}
	}
	if interpretedQuery == nil {
		t.Fatal("no interpreted association query was issued")
	}
	if !kindsContain(interpretedQuery.Kinds, interpretedKind) || !kindsContain(interpretedQuery.Kinds, interpretedKindV2) {
		t.Fatalf("interpreted association query kinds = %v, want both v1 and v2", interpretedQuery.Kinds)
	}
}

// TestReaderRefusesDuplicateInterpretationAcrossKinds proves the
// exactly-one-interpretation invariant holds ACROSS kinds: an occurrence whose
// operation produced both a v1 and a v2 interpreted fact is refused as a
// duplicate rather than silently keeping one.
func TestReaderRefusesDuplicateInterpretationAcrossKinds(t *testing.T) {
	t.Parallel()
	facts := &stubFacts{
		snapshot:       100,
		occurrenceRows: []provenance.EvidenceRow{evidenceRow(10, "op-dup", occurrenceEvidenceKind, `{}`)},
		interpretedRows: []provenance.EvidenceRow{
			evidenceRow(11, "op-dup", interpretedKind, canonicalV1Interpreted),
			evidenceRow(12, "op-dup", interpretedKindV2, canonicalV2Interpreted),
		},
	}
	_, err := Reader{Facts: facts}.interpretations([]model.OccurrenceRecord{occurrence(t, 10)}, 100)
	if err == nil || !strings.Contains(err.Error(), "duplicate interpretations") {
		t.Fatalf("expected a duplicate-across-kinds refusal, got %v", err)
	}
}

// TestReaderRefusesCrossOperationInterpreted proves the association-boundary
// guard: an interpreted fact whose producing operation is not associated with any
// selected occurrence is refused rather than mis-attributed.
func TestReaderRefusesCrossOperationInterpreted(t *testing.T) {
	t.Parallel()
	facts := &stubFacts{
		snapshot:       100,
		occurrenceRows: []provenance.EvidenceRow{evidenceRow(10, "op-a", occurrenceEvidenceKind, `{}`)},
		// The interpreted query returns a row from an unassociated operation
		// (corrupt journal): rawInterpreted bypasses the operation filter.
		rawInterpreted:  true,
		interpretedRows: []provenance.EvidenceRow{evidenceRow(11, "op-orphan", interpretedKind, canonicalV1Interpreted)},
	}
	_, err := Reader{Facts: facts}.interpretations([]model.OccurrenceRecord{occurrence(t, 10)}, 100)
	if err == nil || !strings.Contains(err.Error(), "crossed operation boundaries") {
		t.Fatalf("expected a cross-operation refusal, got %v", err)
	}
}

// TestReaderRefusesInterpretedDigestMismatch: an interpreted row whose stored
// digest differs from its payload fails digest validation before decode.
func TestReaderRefusesInterpretedDigestMismatch(t *testing.T) {
	t.Parallel()
	bad := evidenceRow(11, "op-a", interpretedKind, canonicalV1Interpreted)
	bad.ContentDigest = make([]byte, sha256.Size)
	facts := &stubFacts{
		snapshot:        100,
		occurrenceRows:  []provenance.EvidenceRow{evidenceRow(10, "op-a", occurrenceEvidenceKind, `{}`)},
		interpretedRows: []provenance.EvidenceRow{bad},
	}
	_, err := Reader{Facts: facts}.interpretations([]model.OccurrenceRecord{occurrence(t, 10)}, 100)
	if err == nil || !strings.Contains(err.Error(), "digest validation") {
		t.Fatalf("expected a digest-validation refusal, got %v", err)
	}
}

// TestReaderRefusesSnapshotDriftAtAssociation: if the occurrence-association
// query reports a watermark different from the pinned snapshot, the read is
// refused rather than returning a torn association.
func TestReaderRefusesSnapshotDriftAtAssociation(t *testing.T) {
	t.Parallel()
	facts := &stubFacts{
		snapshot:       99, // occurrence query drifts from the pinned 100
		occurrenceRows: []provenance.EvidenceRow{evidenceRow(10, "op-a", occurrenceEvidenceKind, `{}`)},
	}
	_, err := Reader{Facts: facts}.interpretations([]model.OccurrenceRecord{occurrence(t, 10)}, 100)
	if err == nil || !strings.Contains(err.Error(), "snapshot drifted") {
		t.Fatalf("expected a snapshot-drift refusal, got %v", err)
	}
}

// TestReaderRefusesAmbiguousOccurrenceAssociation: a single operation that
// produced more than one selected occurrence is a corrupt association and is
// refused.
func TestReaderRefusesAmbiguousOccurrenceAssociation(t *testing.T) {
	t.Parallel()
	facts := &stubFacts{
		snapshot: 100,
		occurrenceRows: []provenance.EvidenceRow{
			evidenceRow(10, "op-shared", occurrenceEvidenceKind, `{}`),
			evidenceRow(11, "op-shared", occurrenceEvidenceKind, `{}`),
		},
	}
	_, err := Reader{Facts: facts}.interpretations([]model.OccurrenceRecord{occurrence(t, 10), occurrence(t, 11)}, 100)
	if err == nil || !strings.Contains(err.Error(), "produced multiple occurrences") {
		t.Fatalf("expected an ambiguous-association refusal, got %v", err)
	}
}

// TestReaderInterpretationsEmptyForNoOccurrences: with no selected occurrences the
// reader issues no queries and returns an empty association map.
func TestReaderInterpretationsEmptyForNoOccurrences(t *testing.T) {
	t.Parallel()
	facts := &stubFacts{snapshot: 100}
	result, err := Reader{Facts: facts}.interpretations(nil, 100)
	if err != nil {
		t.Fatalf("interpretations: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected an empty association map, got %d entries", len(result))
	}
	if len(facts.calls) != 0 {
		t.Fatalf("expected no fact queries for an empty occurrence set, got %d", len(facts.calls))
	}
}

// TestInterpretedKindsCoversBothKinds pins the dual-kind admission set used at
// BOTH read sites (the watermark query in occurrences and the association query
// in interpretations both range over interpretedKinds). If a kind is dropped, a
// v1-only or v2-only store silently reads back nothing.
func TestInterpretedKindsCoversBothKinds(t *testing.T) {
	t.Parallel()
	if len(interpretedKinds) != 2 {
		t.Fatalf("interpretedKinds has %d entries, want exactly v1 and v2", len(interpretedKinds))
	}
	if !kindsContain(interpretedKinds, interpretedKind) || !kindsContain(interpretedKinds, interpretedKindV2) {
		t.Fatalf("interpretedKinds = %v, want both interpreted.v1 and interpreted.v2", interpretedKinds)
	}
}
