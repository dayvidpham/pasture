package tasks

import "testing"

func TestEffectiveModeDefaultsToNormal(t *testing.T) {
	ps := mustPolicySet(t)
	cursor, err := EffectiveMode(ps, nil)
	if err != nil {
		t.Fatalf("EffectiveMode(nil): %v", err)
	}
	if cursor.Mode != InteractionNormal {
		t.Fatalf("default mode = %q, want normal", cursor.Mode)
	}
	if cursor.Entry != nil {
		t.Fatalf("default cursor entry = %v, want nil", *cursor.Entry)
	}
}

func TestEffectiveModeIgnoresNonModeEntries(t *testing.T) {
	ps := mustPolicySet(t)
	// A ledger with only non-mode entries yields the default cursor.
	entries := []DecisionLedgerEntry{
		{ID: "dl-1", Decision: DecisionEncoding{Kind: "pasture.other/v1"}},
		{ID: "dl-2", Decision: DecisionEncoding{Kind: "pasture.other/v1"}},
	}
	cursor, err := EffectiveMode(ps, entries)
	if err != nil {
		t.Fatalf("EffectiveMode: %v", err)
	}
	if cursor.Mode != InteractionNormal || cursor.Entry != nil {
		t.Fatalf("cursor = %+v, want default normal/nil", cursor)
	}
}

func TestEffectiveModeSelectsLatestEntry(t *testing.T) {
	ps := mustPolicySet(t)
	entries := []DecisionLedgerEntry{
		modeEntry(t, ps, "m1", InteractionNormal, InteractionAFK),
		{ID: "dl-x", Decision: DecisionEncoding{Kind: "pasture.other/v1"}},
		modeEntry(t, ps, "m2", InteractionAFK, InteractionNormal),
		modeEntry(t, ps, "m3", InteractionNormal, InteractionAFK),
	}
	cursor, err := EffectiveMode(ps, entries)
	if err != nil {
		t.Fatalf("EffectiveMode: %v", err)
	}
	if cursor.Mode != InteractionAFK {
		t.Fatalf("mode = %q, want afk", cursor.Mode)
	}
	if cursor.Entry == nil || *cursor.Entry != "m3" {
		t.Fatalf("cursor entry = %v, want m3", cursor.Entry)
	}
}

// TestEffectiveModeAllFourTransitions confirms every source/target pair — including the
// same-mode transitions — is a real successor cursor.
func TestEffectiveModeAllFourTransitions(t *testing.T) {
	ps := mustPolicySet(t)
	cases := []struct {
		name string
		seq  []DecisionLedgerEntry
		want InteractionMode
		last string
	}{
		{"normal-afk", []DecisionLedgerEntry{modeEntry(t, ps, "a", InteractionNormal, InteractionAFK)}, InteractionAFK, "a"},
		{"afk-afk", []DecisionLedgerEntry{
			modeEntry(t, ps, "a", InteractionNormal, InteractionAFK),
			modeEntry(t, ps, "b", InteractionAFK, InteractionAFK),
		}, InteractionAFK, "b"},
		{"afk-normal", []DecisionLedgerEntry{
			modeEntry(t, ps, "a", InteractionNormal, InteractionAFK),
			modeEntry(t, ps, "b", InteractionAFK, InteractionNormal),
		}, InteractionNormal, "b"},
		{"normal-normal", []DecisionLedgerEntry{
			modeEntry(t, ps, "a", InteractionNormal, InteractionNormal),
		}, InteractionNormal, "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cursor, err := EffectiveMode(ps, tc.seq)
			if err != nil {
				t.Fatalf("EffectiveMode: %v", err)
			}
			if cursor.Mode != tc.want {
				t.Fatalf("mode = %q, want %q", cursor.Mode, tc.want)
			}
			if cursor.Entry == nil || *cursor.Entry != DecisionLedgerEntryID(tc.last) {
				t.Fatalf("cursor entry = %v, want %q", cursor.Entry, tc.last)
			}
		})
	}
}

// TestEffectiveModeRejectsBrokenChain confirms a mode entry whose recorded source mode does
// not match the running effective mode is reported, never silently applied.
func TestEffectiveModeRejectsBrokenChain(t *testing.T) {
	ps := mustPolicySet(t)
	// First entry claims to transition FROM afk, but the default running mode is normal.
	entries := []DecisionLedgerEntry{
		modeEntry(t, ps, "m1", InteractionAFK, InteractionNormal),
	}
	if _, err := EffectiveMode(ps, entries); err == nil {
		t.Fatal("expected broken-chain error, got nil")
	}
}

func TestInteractionModeChangedValidation(t *testing.T) {
	if err := validateInteractionModeChanged(InteractionModeChanged{From: InteractionNormal, To: InteractionAFK}); err != nil {
		t.Fatalf("valid change rejected: %v", err)
	}
	if err := validateInteractionModeChanged(InteractionModeChanged{From: "bogus", To: InteractionAFK}); err == nil {
		t.Fatal("expected invalid source mode to be rejected")
	}
	if err := validateInteractionModeChanged(InteractionModeChanged{From: InteractionNormal, To: ""}); err == nil {
		t.Fatal("expected empty target mode to be rejected")
	}
}
