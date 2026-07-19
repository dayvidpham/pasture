package tasks

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

func TestValidateImplUATPayloadForcesChangesRequested(t *testing.T) {
	// A REPLACE resolution with an accepted verdict is rejected.
	replaceAccepted := ImplUATPayload{
		ReportedVerdict: ImplUATAccepted,
		LedgerDecisions: []LedgerDecisionResolution{{Target: "j-1", Kind: ResolutionReplace}},
	}
	if err := validateImplUATPayload(replaceAccepted); err == nil {
		t.Fatal("expected REPLACE+accepted to be rejected")
	}
	// FIX-NOW feedback with an accepted verdict is rejected.
	fixNowAccepted := ImplUATPayload{
		ReportedVerdict: ImplUATAccepted,
		Feedback:        []UATFeedbackItem{{ID: "fb-1", FixNow: true}},
	}
	if err := validateImplUATPayload(fixNowAccepted); err == nil {
		t.Fatal("expected FIX-NOW+accepted to be rejected")
	}
	// The same payloads with changes_requested are accepted.
	ok := replaceAccepted
	ok.ReportedVerdict = ImplUATChangesRequested
	if err := validateImplUATPayload(ok); err != nil {
		t.Fatalf("REPLACE+changes_requested rejected: %v", err)
	}
}

func TestValidateImplUATPayloadRejectsDuplicateResolution(t *testing.T) {
	dup := ImplUATPayload{
		ReportedVerdict: ImplUATChangesRequested,
		HeldAnswers: []HeldQuestionResolution{
			{Target: "hq-1", Kind: ResolutionConfirm},
			{Target: "hq-1", Kind: ResolutionDefer},
		},
	}
	if err := validateImplUATPayload(dup); err == nil {
		t.Fatal("expected duplicate held-answer target to be rejected")
	}
}

func TestValidatePlanDeferredByAFKRejections(t *testing.T) {
	base := PlanDeferredByAFK{
		Snapshot:      validSnapshot(),
		HeldQuestions: []HeldUATQuestion{{ID: "hq-1", Stable: true}},
		ModeEntry:     "m1",
	}
	if err := validatePlanDeferredByAFK(base); err != nil {
		t.Fatalf("valid deferral rejected: %v", err)
	}

	noAnchor := base
	noAnchor.ModeEntry = ""
	if err := validatePlanDeferredByAFK(noAnchor); err == nil {
		t.Fatal("expected missing mode entry to be rejected")
	}

	noStable := base
	noStable.HeldQuestions = []HeldUATQuestion{{ID: "hq-1", Stable: false}}
	if err := validatePlanDeferredByAFK(noStable); err == nil {
		t.Fatal("expected no-stable-question deferral to be rejected")
	}

	fixNow := base
	fixNow.Feedback = []UATFeedbackItem{{ID: "fb-1", FixNow: true}}
	if err := validatePlanDeferredByAFK(fixNow); err == nil {
		t.Fatal("expected FIX-NOW feedback deferral to be rejected")
	}
}

func TestValidatePlanAcceptedRejectsFixNow(t *testing.T) {
	p := PlanAccepted{
		Snapshot: validSnapshot(),
		Feedback: []UATFeedbackItem{{ID: "fb-1", FixNow: true}},
	}
	if err := validatePlanAccepted(p); err == nil {
		t.Fatal("expected accepted-with-FIX-NOW to be rejected")
	}
}

func TestSetInteractionModeCommandValidate(t *testing.T) {
	valid := SetInteractionModeCommand{
		Epoch:          "epoch-1",
		Desired:        InteractionAFK,
		ExpectedLedger: "L1",
		Report:         ir.ReportedUserDecision{},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid command rejected: %v", err)
	}
	cases := []struct {
		name string
		cmd  SetInteractionModeCommand
	}{
		{"empty-epoch", SetInteractionModeCommand{Desired: InteractionAFK, ExpectedLedger: "L1"}},
		{"invalid-mode", SetInteractionModeCommand{Epoch: "e", Desired: "bogus", ExpectedLedger: "L1"}},
		{"empty-ledger", SetInteractionModeCommand{Epoch: "e", Desired: InteractionAFK}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cmd.Validate(); err == nil {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
		})
	}
}
