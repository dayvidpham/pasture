package codegen

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// The column renderer is white-box tested because its three states are the
// contract a later equality check depends on: a value nobody has derived yet,
// a source that is genuinely absent, and a derived value must render as three
// different strings, or that check could compare blanks to blanks and pass
// without reading anything.

func TestActivationColumnStatesRenderAsThreeDistinctStrings(t *testing.T) {
	t.Parallel()
	unset, err := renderActivationColumn(activationColumnUnset, "")
	require.NoError(t, err)
	absent, err := renderActivationColumn(activationColumnAbsent, "")
	require.NoError(t, err)
	none, err := renderActivationColumn(activationColumnValue, "none")
	require.NoError(t, err)

	require.Equal(t, "unset", unset, "a not-yet-derived column renders the literal token unset")
	require.Equal(t, "", absent, "a genuinely absent source renders the empty string")
	require.Equal(t, "none", none, "a value derived as none renders none")
	require.NotEqual(t, unset, absent, "unset and absent are different states")
	require.NotEqual(t, unset, none, "unset and a derived none are different states")
	require.NotEqual(t, absent, none, "absent and a derived none are different states")
	require.Equal(t, ActivationColumnUnset, unset)

	_, err = renderActivationColumn(activationColumnValue, "")
	require.ErrorContains(t, err, "a derived column value is empty; an empty string is reserved for a genuinely absent source")
	_, err = renderActivationColumn(activationColumnValue, "unset")
	require.ErrorContains(t, err, `spells the unset token "unset"`)
	_, err = renderActivationColumn(activationColumnState(0), "")
	require.ErrorContains(t, err, "is not one of unset, absent or value")
}

// TestFailureEvidenceColumnFollowsTheDeclaredBlockingModeAndTheCitation pins
// the derivation of the evidence column: a non-blocking row is absent, a
// blocking row with a citation carries it, a blocking row without one is unset.
func TestFailureEvidenceColumnFollowsTheDeclaredBlockingModeAndTheCitation(t *testing.T) {
	t.Parallel()
	cited := runtime.LifecycleFailurePolicy{Blocking: runtime.Blocking, Evidence: runtime.FailureEvidence{Source: "https://docs.claude.com/en/docs/claude-code/hooks"}}
	uncited := runtime.LifecycleFailurePolicy{Blocking: runtime.Blocking}

	got, err := failureEvidenceColumn(runtime.LifecycleFailurePolicy{Blocking: runtime.NonBlocking})
	require.NoError(t, err)
	require.Equal(t, "", got, "an uncited non-blocking row has no evidence source")

	got, err = failureEvidenceColumn(cited)
	require.NoError(t, err)
	require.Equal(t, "https://docs.claude.com/en/docs/claude-code/hooks", got, "a blocking row carries its citation")

	got, err = failureEvidenceColumn(uncited)
	require.NoError(t, err)
	require.Equal(t, "unset", got, "a blocking row without a citation is not yet derived")

	uncited.Blocking = runtime.ConditionallyBlocking
	got, err = failureEvidenceColumn(uncited)
	require.NoError(t, err)
	require.Equal(t, "unset", got, "a conditionally blocking row needs evidence like a blocking one")

	capability, err := responseCapabilityColumn(runtime.CapabilityUnset)
	require.NoError(t, err)
	require.Equal(t, "unset", capability, "unset means derivation has not run")
	policy, ok := runtime.LookupLifecycleFailure(ir.HarnessCodex, "PreToolUse")
	require.True(t, ok)
	capability, err = responseCapabilityColumn(policy.Response)
	require.NoError(t, err)
	require.Equal(t, "none", capability, "a derived None must not render unset or empty")
	_, err = responseCapabilityColumn(runtime.ResponseCapability(255))
	require.Error(t, err)
}

func TestPostCompactReportUsesRuntimeEvidenceEvenWhenCatalogueObserves(t *testing.T) {
	t.Parallel()
	manifest := registration.Codex0_153_0()
	var event registration.Event
	for _, candidate := range manifest.Events {
		if candidate.NativeName == "PostCompact" {
			event = candidate
		}
	}
	require.NotZero(t, event.Kind)
	require.Equal(t, registration.NonBlocking, event.Blocking, "this is the measured catalogue/profile divergence")
	policy, ok := runtime.LookupLifecycleFailure(ir.HarnessCodex, event.NativeName)
	require.True(t, ok)
	require.Equal(t, runtime.Blocking, policy.Blocking)
	state, err := activation.NewWithheld(event.Kind, activation.WithheldOutsideTargetSet)
	require.NoError(t, err)
	row, err := activationSupportEntryFor(ir.HarnessCodex, event, state)
	require.NoError(t, err)
	if policy.Evidence.IsPresent() {
		require.Equal(t, policy.Evidence.Source, row.FailureEvidence, "a profile citation must survive the real report row builder")
	} else {
		require.Equal(t, "unset", row.FailureEvidence, "a declared gate waiting for evidence is not genuinely absent")
	}
	// Inject the reviewer's citation at the emitter input, not a second
	// implementation. Profile-site mutation proves the shipped row as well.
	policy.Evidence = runtime.FailureEvidence{Source: "internal/lifecycle/nativeresponse/nativeresponse.go"}
	got, err := failureEvidenceColumn(policy)
	require.NoError(t, err)
	require.Equal(t, policy.Evidence.Source, got)
	policy.Blocking = runtime.NonBlocking
	got, err = failureEvidenceColumn(policy)
	require.NoError(t, err)
	require.Equal(t, policy.Evidence.Source, got, "even observation evidence must not disappear from an audit")
}

// TestActivationSupportEntryForIsTheOneRowBuilder pins the row shape every
// report shares, including the clearance column of a decision row and the
// refusals for an entry that does not belong to its event.
func TestActivationSupportEntryForIsTheOneRowBuilder(t *testing.T) {
	t.Parallel()
	manifest := registration.ClaudeCode2_1_261()
	var sessionStart, setup, preToolUse registration.Event
	for _, event := range manifest.Events {
		switch event.NativeName {
		case "SessionStart":
			sessionStart = event
		case "Setup":
			setup = event
		case "PreToolUse":
			preToolUse = event
		}
	}
	require.NotZero(t, sessionStart.Kind)
	require.NotZero(t, setup.Kind)
	require.NotZero(t, preToolUse.Kind)

	enabled, err := activation.NewEnabled(sessionStart.Kind, activation.CaptureProofSessionStart, activation.ProductionProofSessionStart)
	require.NoError(t, err)
	row, err := activationSupportEntryFor(ir.HarnessClaudeCode, sessionStart, enabled)
	require.NoError(t, err)
	require.Equal(t, activationSupportEntry{Event: "SessionStart", State: "enabled", CaptureProof: activation.CaptureProofSessionStart.Name(), ProductionProof: activation.ProductionProofSessionStart.Name(), ResponseCapability: "none", FailureEvidence: ""}, row)

	gate, err := activation.NewEnabled(preToolUse.Kind, activation.CaptureProofPreToolUse, activation.ProductionProofPreToolUse)
	require.NoError(t, err)
	row, err = activationSupportEntryFor(ir.HarnessClaudeCode, preToolUse, gate)
	require.NoError(t, err)
	require.Equal(t, "https://docs.claude.com/en/docs/claude-code/hooks", row.FailureEvidence, "the cited Claude gate row carries its citation, not the unset token")

	const clearance = "internal/lifecycle/ingress/claude/testdata/CLEARANCE.md"
	decision, err := activation.NewWithheldByDecision(setup.Kind, activation.WithheldNoReachableTrigger, clearance)
	require.NoError(t, err)
	row, err = activationSupportEntryFor(ir.HarnessClaudeCode, setup, decision)
	require.NoError(t, err)
	require.Equal(t, activationSupportEntry{Event: "Setup", State: "withheld", Reason: "no-reachable-trigger", Clearance: clearance, ResponseCapability: "none", FailureEvidence: ""}, row)

	_, err = activationSupportEntryFor(ir.HarnessClaudeCode, setup, enabled)
	require.ErrorContains(t, err, "pair each report row with its own entry")
	_, err = activationSupportEntryFor(ir.HarnessClaudeCode, setup, activation.Entry{})
	require.ErrorContains(t, err, "is invalid")
	withheldSetup, err := activation.NewWithheld(setup.Kind, activation.WithheldOutsideTargetSet)
	require.NoError(t, err)
	_, err = activationSupportEntryFor(ir.HarnessCodex, setup, withheldSetup)
	require.ErrorContains(t, err, `has no pinned lifecycle profile row for harness "codex"`, "Codex has no Setup event, so the Claude row cannot be reported under the Codex profile")
}

// TestActivationSupportEntryForRendersEachNewWithholdingArm pins that the one
// shared row builder carries every new withholding arm through to its report
// sentence, with no per-harness hand list: activationSupportEntryFor is the
// single builder that emitClaudeHooks, the Codex manifest emitter and the
// OpenCode manifest emitter all call, so a reason that renders here renders
// in all three committed activation reports. Removing one arm's String()
// case turns this red, quoting the arm's rendered reason text.
func TestActivationSupportEntryForRendersEachNewWithholdingArm(t *testing.T) {
	t.Parallel()
	manifest := registration.ClaudeCode2_1_261()
	var setup registration.Event
	for _, event := range manifest.Events {
		if event.NativeName == "Setup" {
			setup = event
		}
	}
	require.NotZero(t, setup.Kind)

	const clearance = "internal/lifecycle/ingress/claude/testdata/CLEARANCE.md"
	for _, tc := range []struct {
		reason activation.WithheldReason
		want   string
	}{
		{activation.WithheldProviderHook, "provider-hook"},
		{activation.WithheldNotEmittedByHost, "not-emitted-by-host"},
		{activation.WithheldEmittedOutsideTransport, "emitted-outside-transport"},
		{activation.WithheldTriggerNotExercised, "trigger-not-exercised"},
	} {
		decision, err := activation.NewWithheldByDecision(setup.Kind, tc.reason, clearance)
		require.NoError(t, err)
		row, err := activationSupportEntryFor(ir.HarnessClaudeCode, setup, decision)
		require.NoError(t, err)
		require.Equal(t, activationSupportEntry{Event: "Setup", State: "withheld", Reason: tc.want, Clearance: clearance, ResponseCapability: "none", FailureEvidence: ""}, row, "reason %q must render as %q in the shared row builder", tc.reason, tc.want)
	}
}
