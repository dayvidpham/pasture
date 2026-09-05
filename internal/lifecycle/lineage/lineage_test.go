package lineage_test

import (
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/lineage"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

// These tests exercise the production derivation engine (lineage.DeriveLinks)
// directly — it is the exact code path the `pasture hook lifecycle lineage`
// command runs to compute missing edges. There is no test-only export: the
// command and these tests call the same function.

func mustContract(t *testing.T, harness ir.HarnessID, name string) ir.RuntimeContractID {
	t.Helper()
	contract, err := ir.NewRuntimeContractID(harness, name)
	if err != nil {
		t.Fatalf("construct runtime contract %s/%s: %v", harness, name, err)
	}
	return contract
}

// occurrence builds a committed LifecycleRecord for one occurrence carrying the
// given native identities, using the production model constructors.
func occurrence(t *testing.T, jid int64, contract ir.RuntimeContractID, identities ...waist.SemanticIdentity) model.LifecycleRecord {
	t.Helper()
	occ := model.NewOccurrenceRecord(
		model.OccurrenceID(jid),
		model.ContractEventKind(1),
		contract,
		model.OccurrenceEnvelopeRef{},
		time.Unix(0, jid).UTC(),
		provenance.AgentID{},
		nil,
		model.CaptureValid,
		model.EvidencePayloadRef{},
	)
	var interpreted []model.InterpretedRecord
	if len(identities) > 0 {
		record, err := model.NewInterpretedRecord(
			model.InterpretationID(jid),
			model.OccurrenceID(jid),
			runtime.SemanticObservation,
			identities,
			nil,
			contract,
		)
		if err != nil {
			t.Fatalf("construct interpreted record for occurrence %d: %v", jid, err)
		}
		interpreted = []model.InterpretedRecord{record}
	}
	lifecycle, err := model.NewLifecycleRecord(occ, interpreted)
	if err != nil {
		t.Fatalf("construct lifecycle record for occurrence %d: %v", jid, err)
	}
	return lifecycle
}

func id(kind runtime.NativeIdentityKind, value string) waist.SemanticIdentity {
	return waist.SemanticIdentity{Kind: kind, Value: value}
}

// asRecords commits the derived facts into model.LinkRecords, as the write path
// would, so a follow-up DeriveLinks can be given the now-committed set.
func asRecords(facts []lineage.LinkFact) []model.LinkRecord {
	records := make([]model.LinkRecord, len(facts))
	for i, f := range facts {
		records[i] = model.LinkRecord{
			LinkID:  model.LifecycleLinkID(int64(i) + 1),
			Harness: f.Harness,
			Kind:    f.Kind,
			Value:   f.Value,
			From:    f.From,
			To:      f.To,
		}
	}
	return records
}

func edge(f lineage.LinkFact) [4]any {
	return [4]any{f.Kind, f.Value, f.From.JournalID(), f.To.JournalID()}
}

// TestDeriveLinksPredecessorChain: three occurrences sharing one session
// identity on one harness produce the two immediately-preceding edges
// (o1->o2, o2->o3), From always the immediately preceding occurrence.
func TestDeriveLinksPredecessorChain(t *testing.T) {
	t.Parallel()
	claude := mustContract(t, ir.HarnessClaudeCode, registration.ClaudeCode2_1_210().Contract.String())
	records := []model.LifecycleRecord{
		occurrence(t, 10, claude, id(runtime.IdentitySession, "S1")),
		occurrence(t, 20, claude, id(runtime.IdentitySession, "S1")),
		occurrence(t, 30, claude, id(runtime.IdentitySession, "S1")),
	}
	facts, err := lineage.DeriveLinks(records, nil)
	if err != nil {
		t.Fatalf("DeriveLinks: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("expected 2 predecessor edges, got %d: %+v", len(facts), facts)
	}
	if facts[0].From.JournalID() != 10 || facts[0].To.JournalID() != 20 {
		t.Fatalf("first edge should be 10->20, got %d->%d", facts[0].From.JournalID(), facts[0].To.JournalID())
	}
	if facts[1].From.JournalID() != 20 || facts[1].To.JournalID() != 30 {
		t.Fatalf("second edge should be 20->30 (From is the immediately preceding occurrence), got %d->%d", facts[1].From.JournalID(), facts[1].To.JournalID())
	}
	for _, f := range facts {
		if f.Harness != ir.HarnessClaudeCode || f.Kind != runtime.IdentitySession || f.Value != "S1" {
			t.Fatalf("edge lost its identity coordinates: %+v", f)
		}
	}
}

// TestDeriveLinksIdempotentReRunYieldsNothing: after the derived edges are
// committed, a second derivation over the same records returns nothing — the
// read-side materialize-then-no-op property, content-addressed with no cursor.
func TestDeriveLinksIdempotentReRunYieldsNothing(t *testing.T) {
	t.Parallel()
	claude := mustContract(t, ir.HarnessClaudeCode, registration.ClaudeCode2_1_210().Contract.String())
	records := []model.LifecycleRecord{
		occurrence(t, 10, claude, id(runtime.IdentitySession, "S1")),
		occurrence(t, 20, claude, id(runtime.IdentitySession, "S1")),
		occurrence(t, 30, claude, id(runtime.IdentitySession, "S1")),
	}
	first, err := lineage.DeriveLinks(records, nil)
	if err != nil {
		t.Fatalf("first DeriveLinks: %v", err)
	}
	committed := asRecords(first)
	second, err := lineage.DeriveLinks(records, committed)
	if err != nil {
		t.Fatalf("second DeriveLinks: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("re-run over already-committed links must yield nothing, got %d: %+v", len(second), second)
	}
	// A third run with the same committed set is still a no-op (pure/stable).
	third, err := lineage.DeriveLinks(records, committed)
	if err != nil {
		t.Fatalf("third DeriveLinks: %v", err)
	}
	if len(third) != 0 {
		t.Fatalf("third run must remain a no-op, got %d", len(third))
	}
}

// TestDeriveLinksIncrementalCommitsOnlyNewEdges: given the first edge already
// committed, a later occurrence extends the chain by exactly the new edge.
func TestDeriveLinksIncrementalCommitsOnlyNewEdges(t *testing.T) {
	t.Parallel()
	claude := mustContract(t, ir.HarnessClaudeCode, registration.ClaudeCode2_1_210().Contract.String())
	base := []model.LifecycleRecord{
		occurrence(t, 10, claude, id(runtime.IdentitySession, "S1")),
		occurrence(t, 20, claude, id(runtime.IdentitySession, "S1")),
	}
	committed := asRecords(mustDerive(t, base, nil)) // 10->20 committed
	extended := append(base, occurrence(t, 30, claude, id(runtime.IdentitySession, "S1")))
	facts := mustDerive(t, extended, committed)
	if len(facts) != 1 {
		t.Fatalf("expected exactly the new 20->30 edge, got %d: %+v", len(facts), facts)
	}
	if facts[0].From.JournalID() != 20 || facts[0].To.JournalID() != 30 {
		t.Fatalf("new edge should be 20->30, got %d->%d", facts[0].From.JournalID(), facts[0].To.JournalID())
	}
}

// TestDeriveLinksPerHostChains: the same identity value on two harnesses forms
// two independent chains; no edge crosses harnesses.
func TestDeriveLinksPerHostChains(t *testing.T) {
	t.Parallel()
	claude := mustContract(t, ir.HarnessClaudeCode, registration.ClaudeCode2_1_210().Contract.String())
	opencode := mustContract(t, ir.HarnessOpenCode, registration.OpenCode1_18_10().Contract.String())
	records := []model.LifecycleRecord{
		occurrence(t, 10, claude, id(runtime.IdentitySession, "shared")),
		occurrence(t, 20, opencode, id(runtime.IdentitySession, "shared")),
		occurrence(t, 30, claude, id(runtime.IdentitySession, "shared")),
		occurrence(t, 40, opencode, id(runtime.IdentitySession, "shared")),
	}
	facts := mustDerive(t, records, nil)
	if len(facts) != 2 {
		t.Fatalf("expected one edge per harness (2 total), got %d: %+v", len(facts), facts)
	}
	for _, f := range facts {
		switch f.Harness {
		case ir.HarnessClaudeCode:
			if f.From.JournalID() != 10 || f.To.JournalID() != 30 {
				t.Fatalf("claude chain should be 10->30, got %d->%d", f.From.JournalID(), f.To.JournalID())
			}
		case ir.HarnessOpenCode:
			if f.From.JournalID() != 20 || f.To.JournalID() != 40 {
				t.Fatalf("opencode chain should be 20->40, got %d->%d", f.From.JournalID(), f.To.JournalID())
			}
		default:
			t.Fatalf("unexpected harness in edge: %+v", f)
		}
	}
}

// TestDeriveLinksParallelIdentityKinds: an occurrence carrying two identity
// kinds threads two independent chains at once.
func TestDeriveLinksParallelIdentityKinds(t *testing.T) {
	t.Parallel()
	claude := mustContract(t, ir.HarnessClaudeCode, registration.ClaudeCode2_1_210().Contract.String())
	records := []model.LifecycleRecord{
		occurrence(t, 10, claude, id(runtime.IdentitySession, "S1"), id(runtime.IdentityToolCall, "T1")),
		occurrence(t, 20, claude, id(runtime.IdentitySession, "S1"), id(runtime.IdentityToolCall, "T1")),
	}
	facts := mustDerive(t, records, nil)
	if len(facts) != 2 {
		t.Fatalf("expected one edge per identity kind (2), got %d: %+v", len(facts), facts)
	}
	seen := map[runtime.NativeIdentityKind]bool{}
	for _, f := range facts {
		if f.From.JournalID() != 10 || f.To.JournalID() != 20 {
			t.Fatalf("both edges should be 10->20, got %d->%d", f.From.JournalID(), f.To.JournalID())
		}
		seen[f.Kind] = true
	}
	if !seen[runtime.IdentitySession] || !seen[runtime.IdentityToolCall] {
		t.Fatalf("expected both session and tool-call chains, got %+v", seen)
	}
}

// TestDeriveLinksContentAddressedDedup: an edge already committed is not
// re-emitted even when the reverse or an unrelated edge is present; dedup is on
// (harness, kind, value, from, to).
func TestDeriveLinksContentAddressedDedup(t *testing.T) {
	t.Parallel()
	claude := mustContract(t, ir.HarnessClaudeCode, registration.ClaudeCode2_1_210().Contract.String())
	records := []model.LifecycleRecord{
		occurrence(t, 10, claude, id(runtime.IdentitySession, "S1")),
		occurrence(t, 20, claude, id(runtime.IdentitySession, "S1")),
		occurrence(t, 30, claude, id(runtime.IdentitySession, "S1")),
	}
	// Pre-commit only the middle edge (20->30); expect exactly 10->20 back.
	committed := []model.LinkRecord{{
		LinkID:  model.LifecycleLinkID(99),
		Harness: ir.HarnessClaudeCode,
		Kind:    runtime.IdentitySession,
		Value:   "S1",
		From:    model.OccurrenceID(20),
		To:      model.OccurrenceID(30),
	}}
	facts := mustDerive(t, records, committed)
	if len(facts) != 1 {
		t.Fatalf("expected only the uncommitted 10->20 edge, got %d: %+v", len(facts), facts)
	}
	if facts[0].From.JournalID() != 10 || facts[0].To.JournalID() != 20 {
		t.Fatalf("expected 10->20, got %d->%d", facts[0].From.JournalID(), facts[0].To.JournalID())
	}
}

// TestDeriveLinksDeterministicRegardlessOfInputOrder: shuffled input yields the
// identical edge set (records are sorted by occurrence journal order).
func TestDeriveLinksDeterministicRegardlessOfInputOrder(t *testing.T) {
	t.Parallel()
	claude := mustContract(t, ir.HarnessClaudeCode, registration.ClaudeCode2_1_210().Contract.String())
	ordered := []model.LifecycleRecord{
		occurrence(t, 10, claude, id(runtime.IdentitySession, "S1")),
		occurrence(t, 20, claude, id(runtime.IdentitySession, "S1")),
		occurrence(t, 30, claude, id(runtime.IdentitySession, "S1")),
	}
	shuffled := []model.LifecycleRecord{ordered[2], ordered[0], ordered[1]}
	a := mustDerive(t, ordered, nil)
	b := mustDerive(t, shuffled, nil)
	if len(a) != len(b) {
		t.Fatalf("edge count differs by input order: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if edge(a[i]) != edge(b[i]) {
			t.Fatalf("edge %d differs by input order: %+v vs %+v", i, a[i], b[i])
		}
		if a[i].ContentID() != b[i].ContentID() {
			t.Fatalf("content address differs by input order at %d", i)
		}
	}
}

// TestDeriveLinksSingletonChainHasNoEdge: one occurrence for an identity has no
// predecessor, so no edge is produced.
func TestDeriveLinksSingletonChainHasNoEdge(t *testing.T) {
	t.Parallel()
	claude := mustContract(t, ir.HarnessClaudeCode, registration.ClaudeCode2_1_210().Contract.String())
	records := []model.LifecycleRecord{
		occurrence(t, 10, claude, id(runtime.IdentitySession, "only")),
	}
	facts := mustDerive(t, records, nil)
	if len(facts) != 0 {
		t.Fatalf("a singleton chain must produce no edge, got %d: %+v", len(facts), facts)
	}
}

// TestDeriveLinksCorruptHarnessIsActionable: a committed occurrence whose
// contract names no enabled harness produces an actionable error, not a silent
// skip or a zero-harness edge.
func TestDeriveLinksCorruptHarnessIsActionable(t *testing.T) {
	t.Parallel()
	corrupt := model.NewOccurrenceRecord(
		model.OccurrenceID(10),
		model.ContractEventKind(1),
		ir.RuntimeContractID{}, // zero contract -> Harness() invalid
		model.OccurrenceEnvelopeRef{},
		time.Unix(0, 10).UTC(),
		provenance.AgentID{},
		nil,
		model.CaptureValid,
		model.EvidencePayloadRef{},
	)
	record, err := model.NewLifecycleRecord(corrupt, nil)
	if err != nil {
		t.Fatalf("construct corrupt lifecycle record: %v", err)
	}
	_, err = lineage.DeriveLinks([]model.LifecycleRecord{record}, nil)
	if err == nil {
		t.Fatal("expected an actionable error for a corrupt occurrence harness, got nil")
	}
	for _, want := range []string{"lineage", "occurrence 10", "harness"} {
		if !contains(err.Error(), want) {
			t.Fatalf("error is not actionable, missing %q: %v", want, err)
		}
	}
}

func mustDerive(t *testing.T, records []model.LifecycleRecord, committed []model.LinkRecord) []lineage.LinkFact {
	t.Helper()
	facts, err := lineage.DeriveLinks(records, committed)
	if err != nil {
		t.Fatalf("DeriveLinks: %v", err)
	}
	return facts
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
