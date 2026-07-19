package tasks

import "testing"

func afkCursor(entry string) InteractionModeCursor {
	id := DecisionLedgerEntryID(entry)
	return InteractionModeCursor{Entry: &id, Mode: InteractionAFK}
}

func TestEvaluatePlanDeferralEligible(t *testing.T) {
	err := EvaluatePlanDeferral(PlanDeferralInput{
		Mode:          afkCursor("m1"),
		HeldQuestions: []HeldUATQuestion{{ID: "hq-1", Question: "open", Stable: true}},
		Snapshot:      validSnapshot(),
	})
	if err != nil {
		t.Fatalf("eligible deferral rejected: %v", err)
	}
}

func TestEvaluatePlanDeferralRejections(t *testing.T) {
	stable := []HeldUATQuestion{{ID: "hq-1", Stable: true}}
	cases := []struct {
		name string
		in   PlanDeferralInput
	}{
		{"normal-mode", PlanDeferralInput{Mode: InteractionModeCursor{Mode: InteractionNormal}, HeldQuestions: stable, Snapshot: validSnapshot()}},
		{"afk-no-anchor", PlanDeferralInput{Mode: InteractionModeCursor{Mode: InteractionAFK}, HeldQuestions: stable, Snapshot: validSnapshot()}},
		{"no-stable-question", PlanDeferralInput{Mode: afkCursor("m1"), HeldQuestions: []HeldUATQuestion{{ID: "hq-1", Stable: false}}, Snapshot: validSnapshot()}},
		{"fix-now-feedback", PlanDeferralInput{Mode: afkCursor("m1"), HeldQuestions: stable, Feedback: []UATFeedbackItem{{ID: "fb", FixNow: true}}, Snapshot: validSnapshot()}},
		{"empty-snapshot", PlanDeferralInput{Mode: afkCursor("m1"), HeldQuestions: stable, Snapshot: PlanUATSnapshot{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := EvaluatePlanDeferral(tc.in); err == nil {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
		})
	}
}

func deferred(modeEntryID string) PlanDeferredByAFK {
	return PlanDeferredByAFK{
		Snapshot:      validSnapshot(),
		HeldQuestions: []HeldUATQuestion{{ID: "hq-1", Question: "open", Stable: true}},
		ModeEntry:     DecisionLedgerEntryID(modeEntryID),
	}
}

// TestEvaluateRatifyStillCurrentAFK confirms ratification succeeds only while the exact
// anchoring AFK entry is still the latest mode entry.
func TestEvaluateRatifyStillCurrentAFK(t *testing.T) {
	ps := mustPolicySet(t)
	ledger := []DecisionLedgerEntry{modeEntry(t, ps, "m1", InteractionNormal, InteractionAFK)}
	if err := EvaluateRatify(RatifyInput{Deferred: deferred("m1"), CurrentLedger: ledger}); err != nil {
		t.Fatalf("ratify against current afk entry rejected: %v", err)
	}
}

// TestEvaluateRatifyAFKThenNormalCannotRatify — AFK R1 -> defer -> normal R2 cannot ratify.
func TestEvaluateRatifyAFKThenNormalCannotRatify(t *testing.T) {
	ps := mustPolicySet(t)
	ledger := []DecisionLedgerEntry{
		modeEntry(t, ps, "m1", InteractionNormal, InteractionAFK),
		modeEntry(t, ps, "m2", InteractionAFK, InteractionNormal),
	}
	if err := EvaluateRatify(RatifyInput{Deferred: deferred("m1"), CurrentLedger: ledger}); err == nil {
		t.Fatal("expected ratify to fail after mode returned to normal")
	}
}

// TestEvaluateRatifyLaterAFKCannotReviveOriginal — AFK R1 -> defer -> normal R2 -> AFK R3
// cannot revive the R1 deferral; a fresh deferral under R3 is required.
func TestEvaluateRatifyLaterAFKCannotReviveOriginal(t *testing.T) {
	ps := mustPolicySet(t)
	ledger := []DecisionLedgerEntry{
		modeEntry(t, ps, "m1", InteractionNormal, InteractionAFK),
		modeEntry(t, ps, "m2", InteractionAFK, InteractionNormal),
		modeEntry(t, ps, "m3", InteractionNormal, InteractionAFK),
	}
	if err := EvaluateRatify(RatifyInput{Deferred: deferred("m1"), CurrentLedger: ledger}); err == nil {
		t.Fatal("expected stale R1 deferral to be ineligible under R3")
	}
	// A fresh deferral anchored to R3 is eligible.
	if err := EvaluateRatify(RatifyInput{Deferred: deferred("m3"), CurrentLedger: ledger}); err != nil {
		t.Fatalf("fresh R3 deferral rejected: %v", err)
	}
}
