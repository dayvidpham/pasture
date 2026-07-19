package tasks

import (
	"encoding/hex"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"
)

// validSnapshotUATTaskID is a fixed UAT task id so validSnapshot() is stable across the
// multiple calls a single test makes (build vs. compare).
var validSnapshotUATTaskID = provenance.TaskID{
	Namespace: "aura-plugins",
	UUID:      uuid.MustParse("018f4b30-1e3a-7c9a-8f3b-000000000001"),
}

// mustPolicySet builds a fresh production policy set or fails the test.
func mustPolicySet(t *testing.T) PolicySet {
	t.Helper()
	ps, err := NewProductionPolicySet()
	if err != nil {
		t.Fatalf("NewProductionPolicySet: %v", err)
	}
	return ps
}

// modeEntry builds a decision-ledger entry carrying a canonical mode-change decision.
func modeEntry(t *testing.T, ps PolicySet, id string, from, to InteractionMode) DecisionLedgerEntry {
	t.Helper()
	draft, err := ps.DraftModeChange(InteractionModeChanged{From: from, To: to})
	if err != nil {
		t.Fatalf("DraftModeChange(%s->%s): %v", from, to, err)
	}
	return DecisionLedgerEntry{
		ID:    DecisionLedgerEntryID(id),
		Epoch: "epoch-1",
		Actor: DecisionAttribution{
			DeciderKind:  provenance.AgentKindHuman,
			RecorderKind: provenance.AgentKindSoftware,
		},
		Decision: draft.encoding(),
	}
}

// validSnapshot is a well-formed plan-UAT snapshot the payload tests reuse.
func validSnapshot() PlanUATSnapshot {
	return PlanUATSnapshot{
		ID:            "puat-1",
		UATTaskID:     validSnapshotUATTaskID,
		Proposal:      "prop-r1",
		DecisionEntry: "dl-1",
		InputLedger:   "L1",
		OutputLedger:  "L2",
	}
}

func TestNewProductionPolicySetRegistersFiveKinds(t *testing.T) {
	ps := mustPolicySet(t)
	manifest := ps.Catalog.Manifest()
	if len(manifest) != 5 {
		t.Fatalf("catalog manifest has %d entries, want 5", len(manifest))
	}
	want := map[DecisionKindID]bool{
		DecisionInteractionModeChanged:  true,
		DecisionPlanUATAccepted:         true,
		DecisionPlanUATChangesRequested: true,
		DecisionPlanUATDeferredByAFK:    true,
		DecisionImplementationUAT:       true,
	}
	for _, e := range manifest {
		if !want[e.Kind] {
			t.Errorf("unexpected registered kind %q", e.Kind)
		}
		delete(want, e.Kind)
		if e.Codec != policyCanonicalJSONCodec {
			t.Errorf("kind %q uses codec %q, want %q", e.Kind, e.Codec, policyCanonicalJSONCodec)
		}
	}
	for k := range want {
		t.Errorf("kind %q was not registered", k)
	}
}

// TestSchemaDigestGolden pins the exact schema digest of every #49 decision kind. Any
// change to a payload's field-shape descriptor changes its digest and fails this test —
// the deliberate version-bump signal the #43 base contract requires.
func TestSchemaDigestGolden(t *testing.T) {
	ps := mustPolicySet(t)
	cases := []struct {
		name   string
		schema DecisionSchemaDigest
		hex    string
	}{
		{"mode-changed", ps.modeChanged.Schema(), "7e02eb66a7c3119a89445c23ab8abd13106720c4f7d74997ec3ccf1a8077980b"},
		{"plan-accepted", ps.planAccepted.Schema(), "e91e64ac26c52cd07be3968065af7b88f6f555ea8144c71ccb6002b518ee09c1"},
		{"plan-changes", ps.planChanges.Schema(), "8a4183a5abcf6dd988fdb5520f7db7b8246c2a72e31af473d7c73263d4305ca8"},
		{"plan-deferred", ps.planDeferred.Schema(), "a712b9bd98bb5439f1e2bf66309b5c2fea243beaab75c17eb3bb23e525eb6c34"},
		{"impl-uat", ps.implementationUAT.Schema(), "51d18de9b61799805a2cf42ae1bef2870216550a212c7714d31e1981d7968a05"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hex.EncodeToString(tc.schema[:])
			if got != tc.hex {
				t.Fatalf("schema digest = %s, want %s", got, tc.hex)
			}
			if tc.schema == (DecisionSchemaDigest{}) {
				t.Fatalf("schema digest is zero")
			}
		})
	}
}

func TestModeChangeRoundTrip(t *testing.T) {
	ps := mustPolicySet(t)
	for _, tc := range []InteractionModeChanged{
		{From: InteractionNormal, To: InteractionAFK},
		{From: InteractionAFK, To: InteractionNormal},
		{From: InteractionNormal, To: InteractionNormal},
		{From: InteractionAFK, To: InteractionAFK},
	} {
		draft, err := ps.DraftModeChange(tc)
		if err != nil {
			t.Fatalf("DraftModeChange(%+v): %v", tc, err)
		}
		enc := draft.encoding()
		if err := ps.Catalog.ValidateStored(enc); err != nil {
			t.Fatalf("ValidateStored(%+v): %v", tc, err)
		}
		got, err := DecodeDecision(ps.Catalog, ps.modeChanged, enc)
		if err != nil {
			t.Fatalf("DecodeDecision(%+v): %v", tc, err)
		}
		if got != tc {
			t.Fatalf("round-trip = %+v, want %+v", got, tc)
		}
	}
}

func TestImplUATPayloadRoundTrip(t *testing.T) {
	ps := mustPolicySet(t)
	payload := ImplUATPayload{
		ReportedVerdict: ImplUATChangesRequested,
		Interactions:    []UATInteraction{{Prompt: "q?", Response: "a"}},
		Feedback:        []UATFeedbackItem{{ID: "fb-1", Body: "b", FixNow: true}},
		HeldAnswers:     []HeldQuestionResolution{{Target: "hq-1", Kind: ResolutionConfirm}},
		PlanFeedback:    []DeferredFeedbackResolution{{Target: "pf-1", Kind: ResolutionDefer}},
		LedgerDecisions: []LedgerDecisionResolution{{Target: "dl-1", Kind: ResolutionReplace, Note: "superseded"}},
	}
	draft, err := ps.DraftImplementationUAT(payload)
	if err != nil {
		t.Fatalf("DraftImplementationUAT: %v", err)
	}
	enc := draft.encoding()
	if err := ps.Catalog.ValidateStored(enc); err != nil {
		t.Fatalf("ValidateStored: %v", err)
	}
	got, err := DecodeDecision(ps.Catalog, ps.implementationUAT, enc)
	if err != nil {
		t.Fatalf("DecodeDecision: %v", err)
	}
	if got.ReportedVerdict != payload.ReportedVerdict ||
		len(got.Interactions) != 1 || got.Interactions[0] != payload.Interactions[0] ||
		len(got.LedgerDecisions) != 1 || got.LedgerDecisions[0] != payload.LedgerDecisions[0] {
		t.Fatalf("round-trip = %+v, want %+v", got, payload)
	}
}

func TestPlanUATPayloadsRoundTrip(t *testing.T) {
	ps := mustPolicySet(t)

	accepted, err := ps.planAccepted.Draft(PlanAccepted{Snapshot: validSnapshot()})
	if err != nil {
		t.Fatalf("draft accepted: %v", err)
	}
	if err := ps.Catalog.ValidateStored(accepted.encoding()); err != nil {
		t.Fatalf("ValidateStored accepted: %v", err)
	}
	gotAccepted, err := DecodeDecision(ps.Catalog, ps.planAccepted, accepted.encoding())
	if err != nil {
		t.Fatalf("decode accepted: %v", err)
	}
	if gotAccepted.Snapshot != validSnapshot() {
		t.Fatalf("accepted snapshot = %+v", gotAccepted.Snapshot)
	}

	deferred := PlanDeferredByAFK{
		Snapshot:      validSnapshot(),
		HeldQuestions: []HeldUATQuestion{{ID: "hq-1", Question: "still open?", Stable: true}},
		ModeEntry:     "mode-1",
	}
	draft, err := ps.planDeferred.Draft(deferred)
	if err != nil {
		t.Fatalf("draft deferred: %v", err)
	}
	got, err := DecodeDecision(ps.Catalog, ps.planDeferred, draft.encoding())
	if err != nil {
		t.Fatalf("decode deferred: %v", err)
	}
	if got.ModeEntry != "mode-1" || len(got.HeldQuestions) != 1 {
		t.Fatalf("deferred round-trip = %+v", got)
	}
}

// TestDraftPlanUATLowersByVerdict confirms PlanUATDecision lowers to the kind its verdict
// implies, and that a deferral is rejected unless AFK-anchored with a stable held question.
func TestDraftPlanUATLowersByVerdict(t *testing.T) {
	ps := mustPolicySet(t)
	entry := DecisionLedgerEntryID("mode-1")

	acc, err := ps.DraftPlanUAT(PlanUATDecision{Snapshot: validSnapshot(), ReportedVerdict: PlanUATAccepted})
	if err != nil {
		t.Fatalf("accepted: %v", err)
	}
	if err := requireKind(ps, acc, DecisionPlanUATAccepted); err != nil {
		t.Fatal(err)
	}

	chg, err := ps.DraftPlanUAT(PlanUATDecision{
		Snapshot:        validSnapshot(),
		ReportedVerdict: PlanUATChangesRequested,
		Feedback:        []UATFeedbackItem{{ID: "fb-1", Body: "x", FixNow: true}},
	})
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if err := requireKind(ps, chg, DecisionPlanUATChangesRequested); err != nil {
		t.Fatal(err)
	}

	def, err := ps.DraftPlanUAT(PlanUATDecision{
		Snapshot:        validSnapshot(),
		ReportedVerdict: PlanUATDeferredByAFK,
		HeldQuestions:   []HeldUATQuestion{{ID: "hq-1", Question: "open", Stable: true}},
		Mode:            InteractionModeCursor{Entry: &entry, Mode: InteractionAFK},
	})
	if err != nil {
		t.Fatalf("deferred: %v", err)
	}
	if err := requireKind(ps, def, DecisionPlanUATDeferredByAFK); err != nil {
		t.Fatal(err)
	}

	// A deferral in normal mode is rejected.
	if _, err := ps.DraftPlanUAT(PlanUATDecision{
		Snapshot:        validSnapshot(),
		ReportedVerdict: PlanUATDeferredByAFK,
		HeldQuestions:   []HeldUATQuestion{{ID: "hq-1", Stable: true}},
		Mode:            InteractionModeCursor{Mode: InteractionNormal},
	}); err == nil {
		t.Fatal("expected deferral in normal mode to be rejected")
	}
}

// requireKind confirms a draft carries the expected kind by validating it against the
// catalog under that kind's stored encoding.
func requireKind(ps PolicySet, draft DecisionDraft, want DecisionKindID) error {
	enc := draft.encoding()
	if enc.Kind != want {
		return errKindMismatch{got: enc.Kind, want: want}
	}
	return ps.Catalog.ValidateStored(enc)
}

type errKindMismatch struct{ got, want DecisionKindID }

func (e errKindMismatch) Error() string {
	return "draft kind " + string(e.got) + ", want " + string(e.want)
}

// TestCanonicalPayloadGolden pins the exact canonical-JSON wire bytes of one fixed payload
// per #49 decision kind, decoded straight off the descriptor's real encode path (the actual
// Go struct fields and json tags) rather than the hand-maintained shape string
// policySchemaDigest hashes. TestSchemaDigestGolden only reddens when the shape STRING is
// bumped; this test additionally reddens on a field rename/add/remove or json-tag change
// that a shape-string bump was forgotten for, because it compares the literal wire bytes.
func TestCanonicalPayloadGolden(t *testing.T) {
	ps := mustPolicySet(t)
	snapshot := validSnapshot()
	interactions := []UATInteraction{{Prompt: "q?", Response: "a"}}
	feedback := []UATFeedbackItem{{ID: "fb-1", Body: "b", FixNow: true}}

	modeChanged, err := ps.DraftModeChange(InteractionModeChanged{From: InteractionNormal, To: InteractionAFK})
	if err != nil {
		t.Fatalf("DraftModeChange: %v", err)
	}
	accepted, err := ps.planAccepted.Draft(PlanAccepted{Snapshot: snapshot, Interactions: interactions, Feedback: nil})
	if err != nil {
		t.Fatalf("draft accepted: %v", err)
	}
	changes, err := ps.planChanges.Draft(PlanChangesRequested{Snapshot: snapshot, Interactions: interactions, Feedback: feedback})
	if err != nil {
		t.Fatalf("draft changes: %v", err)
	}
	deferred, err := ps.planDeferred.Draft(PlanDeferredByAFK{
		Snapshot:      snapshot,
		Interactions:  interactions,
		HeldQuestions: []HeldUATQuestion{{ID: "hq-1", Question: "still open?", Stable: true}},
		ModeEntry:     "mode-1",
	})
	if err != nil {
		t.Fatalf("draft deferred: %v", err)
	}
	implUAT, err := ps.implementationUAT.Draft(ImplUATPayload{
		ReportedVerdict: ImplUATChangesRequested,
		Interactions:    interactions,
		Feedback:        feedback,
		HeldAnswers:     []HeldQuestionResolution{{Target: "hq-1", Kind: ResolutionConfirm}},
		PlanFeedback:    []DeferredFeedbackResolution{{Target: "pf-1", Kind: ResolutionDefer}},
		LedgerDecisions: []LedgerDecisionResolution{{Target: "dl-1", Kind: ResolutionReplace, Note: "superseded"}},
	})
	if err != nil {
		t.Fatalf("draft impl-uat: %v", err)
	}

	cases := []struct {
		name string
		got  []byte
		want string
	}{
		{"mode-changed", modeChanged.encoding().Payload, `{"from":"normal","to":"afk"}`},
		{"plan-accepted", accepted.encoding().Payload, `{"snapshot":{"id":"puat-1","uatTaskId":{"Namespace":"aura-plugins","UUID":"018f4b30-1e3a-7c9a-8f3b-000000000001"},"proposal":"prop-r1","decisionEntry":"dl-1","inputLedger":"L1","outputLedger":"L2"},"interactions":[{"prompt":"q?","response":"a"}],"feedback":null}`},
		{"plan-changes", changes.encoding().Payload, `{"snapshot":{"id":"puat-1","uatTaskId":{"Namespace":"aura-plugins","UUID":"018f4b30-1e3a-7c9a-8f3b-000000000001"},"proposal":"prop-r1","decisionEntry":"dl-1","inputLedger":"L1","outputLedger":"L2"},"interactions":[{"prompt":"q?","response":"a"}],"feedback":[{"id":"fb-1","body":"b","fixNow":true}]}`},
		{"plan-deferred", deferred.encoding().Payload, `{"snapshot":{"id":"puat-1","uatTaskId":{"Namespace":"aura-plugins","UUID":"018f4b30-1e3a-7c9a-8f3b-000000000001"},"proposal":"prop-r1","decisionEntry":"dl-1","inputLedger":"L1","outputLedger":"L2"},"interactions":[{"prompt":"q?","response":"a"}],"feedback":null,"heldQuestions":[{"id":"hq-1","question":"still open?","stable":true}],"modeEntry":"mode-1"}`},
		{"impl-uat", implUAT.encoding().Payload, `{"reportedVerdict":2,"interactions":[{"prompt":"q?","response":"a"}],"feedback":[{"id":"fb-1","body":"b","fixNow":true}],"heldAnswers":[{"target":"hq-1","kind":1,"note":""}],"planFeedback":[{"target":"pf-1","kind":2,"note":""}],"ledgerDecisions":[{"target":"dl-1","kind":3,"note":"superseded"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(tc.got)
			if got != tc.want {
				t.Fatalf("canonical payload =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}
