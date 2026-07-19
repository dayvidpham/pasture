package tasks

import (
	"testing"

	"github.com/dayvidpham/provenance"
)

// judgmentEntry builds a decision-ledger entry attributed to the given decider kind. Only
// Actor.DeciderKind matters for material-judgment classification.
func judgmentEntry(id string, decider provenance.AgentKind) DecisionLedgerEntry {
	return DecisionLedgerEntry{
		ID:    DecisionLedgerEntryID(id),
		Actor: DecisionAttribution{DeciderKind: decider},
	}
}

func TestMaterialAgentJudgmentsFiltersHuman(t *testing.T) {
	entries := []DecisionLedgerEntry{
		judgmentEntry("j-ml", provenance.AgentKindMachineLearning),
		judgmentEntry("u-1", provenance.AgentKindHuman),
		judgmentEntry("j-sw", provenance.AgentKindSoftware),
		judgmentEntry("j-ml", provenance.AgentKindMachineLearning), // duplicate id
	}
	got := MaterialAgentJudgments(entries)
	if len(got) != 2 || got[0] != "j-ml" || got[1] != "j-sw" {
		t.Fatalf("material judgments = %v, want [j-ml j-sw]", got)
	}
}

func TestRequiredDecisionsUnionMinusResolutions(t *testing.T) {
	ledger := []DecisionLedgerEntry{
		judgmentEntry("j-1", provenance.AgentKindMachineLearning),
		judgmentEntry("j-2", provenance.AgentKindSoftware),
		judgmentEntry("u-1", provenance.AgentKindHuman), // user decision, not required
	}
	deferredPlan := &PlanDeferredByAFK{
		HeldQuestions: []HeldUATQuestion{{ID: "hq-1", Stable: true}, {ID: "hq-2", Stable: true}},
		Feedback:      []UATFeedbackItem{{ID: "fb-1"}, {ID: "fb-2"}},
	}
	prior := []ImplUATPayload{{
		LedgerDecisions: []LedgerDecisionResolution{{Target: "j-1", Kind: ResolutionConfirm}},
		HeldAnswers:     []HeldQuestionResolution{{Target: "hq-1", Kind: ResolutionDefer}},
		PlanFeedback:    []DeferredFeedbackResolution{{Target: "fb-2", Kind: ResolutionReplace}},
	}}

	got := RequiredDecisions(CoverageInput{LedgerEntries: ledger, DeferredPlan: deferredPlan, PriorResolutions: prior})
	want := []RequiredRef{
		{Kind: RequiredLedgerDecision, ID: "j-2"},
		{Kind: RequiredHeldQuestion, ID: "hq-2"},
		{Kind: RequiredDeferredFeedback, ID: "fb-1"},
	}
	if len(got) != len(want) {
		t.Fatalf("required = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("required[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestRequiredDecisionsEmpty(t *testing.T) {
	got := RequiredDecisions(CoverageInput{})
	if len(got) != 0 {
		t.Fatalf("expected empty required set, got %v", got)
	}
}

// TestCoverageDigestStableUnderPermutation confirms permuting inputs does not change the
// digest, while a changed value does.
func TestCoverageDigestStableUnderPermutation(t *testing.T) {
	refsA := []RequiredRef{
		{Kind: RequiredLedgerDecision, ID: "j-2"},
		{Kind: RequiredHeldQuestion, ID: "hq-1"},
		{Kind: RequiredDeferredFeedback, ID: "fb-1"},
	}
	refsB := []RequiredRef{
		{Kind: RequiredDeferredFeedback, ID: "fb-1"},
		{Kind: RequiredLedgerDecision, ID: "j-2"},
		{Kind: RequiredHeldQuestion, ID: "hq-1"},
	}
	payloadA := ImplUATPayload{
		ReportedVerdict: ImplUATAccepted,
		LedgerDecisions: []LedgerDecisionResolution{{Target: "j-2", Kind: ResolutionConfirm}},
		HeldAnswers:     []HeldQuestionResolution{{Target: "hq-1", Kind: ResolutionDefer}},
	}
	payloadB := ImplUATPayload{
		ReportedVerdict: ImplUATAccepted,
		HeldAnswers:     []HeldQuestionResolution{{Target: "hq-1", Kind: ResolutionDefer}},
		LedgerDecisions: []LedgerDecisionResolution{{Target: "j-2", Kind: ResolutionConfirm}},
	}
	specA := CoverageDigestSpec{RequiredRefs: refsA, Payload: payloadA, PlanDecision: "p1", IntegrationSet: "iset", InputLedger: "L1"}
	specB := CoverageDigestSpec{RequiredRefs: refsB, Payload: payloadB, PlanDecision: "p1", IntegrationSet: "iset", InputLedger: "L1"}

	digA, err := ComputeCoverageDigest(specA)
	if err != nil {
		t.Fatalf("ComputeCoverageDigest A: %v", err)
	}
	digB, err := ComputeCoverageDigest(specB)
	if err != nil {
		t.Fatalf("ComputeCoverageDigest B: %v", err)
	}
	if digA != digB {
		t.Fatalf("digest changed under permutation: %x vs %x", digA, digB)
	}

	// Any changed input yields a different digest.
	for _, mutate := range []func(s CoverageDigestSpec) CoverageDigestSpec{
		func(s CoverageDigestSpec) CoverageDigestSpec { s.InputLedger = "L2"; return s },
		func(s CoverageDigestSpec) CoverageDigestSpec { s.PlanDecision = "p2"; return s },
		func(s CoverageDigestSpec) CoverageDigestSpec { s.IntegrationSet = "other"; return s },
		func(s CoverageDigestSpec) CoverageDigestSpec {
			s.RequiredRefs = append([]RequiredRef{{Kind: RequiredLedgerDecision, ID: "j-9"}}, s.RequiredRefs...)
			return s
		},
		func(s CoverageDigestSpec) CoverageDigestSpec {
			p := s.Payload
			p.ReportedVerdict = ImplUATChangesRequested
			s.Payload = p
			return s
		},
	} {
		mutated, err := ComputeCoverageDigest(mutate(specA))
		if err != nil {
			t.Fatalf("ComputeCoverageDigest mutated: %v", err)
		}
		if mutated == digA {
			t.Fatal("expected mutated input to change the digest")
		}
	}
}

func TestVerifyCoverage(t *testing.T) {
	spec := CoverageDigestSpec{
		RequiredRefs:   []RequiredRef{{Kind: RequiredLedgerDecision, ID: "j-1"}},
		Payload:        ImplUATPayload{ReportedVerdict: ImplUATAccepted},
		PlanDecision:   "p1",
		IntegrationSet: "iset",
		InputLedger:    "L1",
	}
	dig, err := ComputeCoverageDigest(spec)
	if err != nil {
		t.Fatalf("ComputeCoverageDigest: %v", err)
	}
	if err := VerifyCoverage(dig, spec); err != nil {
		t.Fatalf("VerifyCoverage(matching): %v", err)
	}
	// A later ledger append adds a required ref, invalidating the accepted digest.
	stale := spec
	stale.RequiredRefs = append(append([]RequiredRef{}, spec.RequiredRefs...), RequiredRef{Kind: RequiredLedgerDecision, ID: "j-late"})
	if err := VerifyCoverage(dig, stale); err == nil {
		t.Fatal("expected VerifyCoverage to fail against a changed required set")
	}
}
