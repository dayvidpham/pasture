package activation_test

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation/internal/proofgen"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/testutil"
)

func TestClaudeActivationIsCompleteAndExactlyPartitioned(t *testing.T) {
	t.Parallel()
	entries, err := activation.ClaudeCode2_1_261()
	require.NoError(t, err)
	require.Len(t, entries, len(registration.ClaudeCode2_1_261().Entries()), "one activation entry per registered Claude event")
	targets := make(map[model.ContractEventKind]struct{})
	for _, event := range activation.ClaudeCode2_1_261TargetEvents() {
		targets[event] = struct{}{}
	}
	require.Len(t, targets, 10)
	enabledTargets := map[model.ContractEventKind]struct{}{
		registration.EventSessionStart: {}, registration.EventSessionEnd: {},
		registration.EventPreToolUse: {}, registration.EventPostToolUse: {},
		registration.EventPostToolUseFailure: {}, registration.EventPostToolBatch: {},
		registration.EventPreCompact: {}, registration.EventPostCompact: {},
	}
	seen := make(map[model.ContractEventKind]struct{}, len(entries))
	enabled := 0
	missingCorrelation := 0
	outsideTarget := 0
	for _, entry := range entries {
		_, duplicate := seen[entry.Event]
		require.False(t, duplicate)
		seen[entry.Event] = struct{}{}
		require.True(t, entry.IsValid())
		switch entry.State {
		case activation.Enabled:
			enabled++
			require.Zero(t, entry.Reason)
			require.Contains(t, enabledTargets, entry.Event)
			captureEvent, captureOK := entry.CaptureProof.Event()
			productionEvent, productionOK := entry.ProductionProof.Event()
			require.True(t, captureOK)
			require.True(t, productionOK)
			require.Equal(t, entry.Event, captureEvent)
			require.Equal(t, entry.Event, productionEvent)
			require.NotEmpty(t, entry.CaptureProof.Name())
			require.NotEmpty(t, entry.ProductionProof.Name())
		case activation.Withheld:
			require.Zero(t, entry.CaptureProof)
			require.Zero(t, entry.ProductionProof)
			if _, target := targets[entry.Event]; target {
				missingCorrelation++
				require.Contains(t, []model.ContractEventKind{registration.EventElicitation, registration.EventElicitationResult}, entry.Event)
				require.Equal(t, activation.WithheldMissingRequestCorrelation, entry.Reason)
			} else {
				outsideTarget++
				require.Equal(t, activation.WithheldOutsideTargetSet, entry.Reason)
			}
		default:
			t.Fatalf("unexpected activation state %d", entry.State)
		}
	}
	require.Equal(t, 8, enabled)
	require.Equal(t, 2, missingCorrelation)
	require.Equal(t, len(entries)-len(targets), outsideTarget, "every registered event outside the declared target set is withheld outside-target-set")
	require.Equal(t, registration.EventSessionStart, entries[0].Event)
}

func TestActivationTargetEventsReturnsDefensiveCopyAndManifestIsFresh(t *testing.T) {
	t.Parallel()
	targets := activation.ClaudeCode2_1_261TargetEvents()
	require.Len(t, targets, 10)
	targets[0] = model.ContractEventKind(999)
	freshTargets := activation.ClaudeCode2_1_261TargetEvents()
	require.Equal(t, registration.EventSessionStart, freshTargets[0])

	entries, err := activation.ClaudeCode2_1_261()
	require.NoError(t, err)
	entries[0].State = activation.Withheld
	freshEntries, err := activation.ClaudeCode2_1_261()
	require.NoError(t, err)
	require.Equal(t, activation.Enabled, freshEntries[0].State)
}

func TestEnabledRequiresEventBoundProofs(t *testing.T) {
	t.Parallel()
	_, err := activation.NewEnabled(registration.EventSessionStart, 0, activation.ProductionProofSessionStart)
	require.ErrorContains(t, err, "event-bound authentic capture proof")
	_, err = activation.NewEnabled(registration.EventSessionEnd, activation.CaptureProofSessionStart, activation.ProductionProofSessionStart)
	require.ErrorContains(t, err, "bound to event")
	_, err = activation.NewEnabled(registration.EventSessionStart, activation.CaptureProofSessionStart, 0)
	require.ErrorContains(t, err, "event-bound production proof")
	_, err = activation.NewEnabled(registration.EventSessionStart, activation.CaptureProofSessionStart, activation.ProductionProofSessionStart)
	require.NoError(t, err)
}

func TestActivationConstructorsRejectReservedZeroAndInvalidCombinations(t *testing.T) {
	t.Parallel()
	_, err := activation.NewWithheld(0, activation.WithheldMissingFixture)
	require.Error(t, err)
	_, err = activation.NewWithheld(registration.EventSessionStart, 0)
	require.Error(t, err)
	require.False(t, (activation.Entry{}).IsValid())
	require.False(t, (activation.CaptureProof(99)).IsValid())
	require.False(t, (activation.ProductionProof(99)).IsValid())
	require.Equal(t, "missing-request-correlation", activation.WithheldMissingRequestCorrelation.String())
}

// TestEveryDeclaredProofSitsInsideItsHarnessOrdinalRange walks every declared
// capture and production proof of every harness and checks its ordinal against
// the range the generator enforces for that harness. The population is the
// declaration tables themselves, so a new arm is covered the day it is
// declared; the control is that each harness contributed at least one arm.
func TestEveryDeclaredProofSitsInsideItsHarnessOrdinalRange(t *testing.T) {
	t.Parallel()
	ranges := map[ir.HarnessID]proofgen.Harness{}
	for _, harness := range proofgen.Harnesses {
		ranges[harness.ID] = harness
	}
	seen := map[ir.HarnessID]int{}
	for _, arm := range activation.CaptureProofArms() {
		r, known := ranges[arm.Harness]
		require.True(t, known, "capture proof %q is declared for a harness with no ordinal range", arm.Arm)
		require.True(t, int(arm.Proof) >= r.Low && int(arm.Proof) <= r.High, "capture proof %q has ordinal %d outside the %s range %d-%d", arm.Arm, arm.Proof, r.Label, r.Low, r.High)
		harness, ok := arm.Proof.Harness()
		require.True(t, ok)
		require.Equal(t, arm.Harness, harness, "capture proof %q must report the harness whose file declares it", arm.Arm)
		seen[arm.Harness]++
	}
	for _, arm := range activation.ProductionProofArms() {
		r, known := ranges[arm.Harness]
		require.True(t, known, "production proof %q is declared for a harness with no ordinal range", arm.Arm)
		require.True(t, int(arm.Proof) >= r.Low && int(arm.Proof) <= r.High, "production proof %q has ordinal %d outside the %s range %d-%d", arm.Arm, arm.Proof, r.Label, r.Low, r.High)
		harness, ok := arm.Proof.Harness()
		require.True(t, ok)
		require.Equal(t, arm.Harness, harness, "production proof %q must report the harness whose file declares it", arm.Arm)
		seen[arm.Harness]++
	}
	for _, harness := range proofgen.Harnesses {
		require.NotZero(t, seen[harness.ID], "%s declared no proof at all, so the range check above asserted nothing for it", harness.Label)
	}
	require.Equal(t, activation.CaptureProof(100), activation.CaptureProofCodexSessionStart, "the Codex range starts at 100")
	require.Equal(t, activation.CaptureProof(200), activation.CaptureProofOpenCodeSessionCreated, "the OpenCode range starts at 200")
	require.Equal(t, activation.ProductionProof(1), activation.ProductionProofSessionStart, "the Claude Code range starts at 1")
}

// TestWithheldReasonsForUserDecisionsAreValidDistinctAndClosed pins the two
// decision reasons and the derived population of the enum.
func TestWithheldReasonsForUserDecisionsAreValidDistinctAndClosed(t *testing.T) {
	t.Parallel()
	require.True(t, activation.WithheldNoReachableTrigger.IsValid())
	require.True(t, activation.WithheldUnclearablePayload.IsValid())
	require.True(t, activation.WithheldProviderHook.IsValid())
	require.True(t, activation.WithheldNotEmittedByHost.IsValid())
	require.True(t, activation.WithheldEmittedOutsideTransport.IsValid())
	require.False(t, activation.WithheldReasonInvalid.IsValid(), "the zero value is invalid")
	require.Equal(t, "no-reachable-trigger", activation.WithheldNoReachableTrigger.String())
	require.Equal(t, "unclearable-payload", activation.WithheldUnclearablePayload.String())
	require.Equal(t, "provider-hook", activation.WithheldProviderHook.String())
	require.Equal(t, "not-emitted-by-host", activation.WithheldNotEmittedByHost.String())
	require.Equal(t, "emitted-outside-transport", activation.WithheldEmittedOutsideTransport.String())
	require.NotEqual(t, activation.WithheldNoReachableTrigger.String(), activation.WithheldUnclearablePayload.String())
	require.True(t, activation.WithheldNoReachableTrigger.RequiresClearance())
	require.True(t, activation.WithheldUnclearablePayload.RequiresClearance())
	require.True(t, activation.WithheldProviderHook.RequiresClearance())
	require.True(t, activation.WithheldNotEmittedByHost.RequiresClearance())
	require.True(t, activation.WithheldEmittedOutsideTransport.RequiresClearance())
	require.False(t, activation.WithheldOutsideTargetSet.RequiresClearance())

	all := activation.AllWithheldReasons()
	require.Equal(t, []activation.WithheldReason{
		activation.WithheldMissingFixture, activation.WithheldOutsideTargetSet, activation.WithheldUnverifiedBuild,
		activation.WithheldProductionProofMissing, activation.WithheldMissingRequestCorrelation,
		activation.WithheldNoReachableTrigger, activation.WithheldUnclearablePayload,
		activation.WithheldProviderHook, activation.WithheldNotEmittedByHost, activation.WithheldEmittedOutsideTransport,
	}, all, "the derived population is exactly the ten arms")
	require.False(t, activation.WithheldReason(len(all)+1).IsValid(), "the arm after the last one is invalid")
}

// TestADecisionReasonRequiresACommittedClearancePath is the corpus rule for
// the two decision reasons: a row that uses one without a CLEARANCE.md path is
// a hard error, and the path is held to the same rule a sidecar clearance is.
func TestADecisionReasonRequiresACommittedClearancePath(t *testing.T) {
	t.Parallel()
	const clearance = "internal/lifecycle/ingress/claude/testdata/CLEARANCE.md"
	for _, reason := range []activation.WithheldReason{
		activation.WithheldNoReachableTrigger, activation.WithheldUnclearablePayload,
		activation.WithheldProviderHook, activation.WithheldNotEmittedByHost, activation.WithheldEmittedOutsideTransport,
	} {
		_, err := activation.NewWithheld(registration.EventSetup, reason)
		require.ErrorContains(t, err, "records a user decision and requires the committed CLEARANCE.md path")
		_, err = activation.NewWithheldByDecision(registration.EventSetup, reason, "")
		require.ErrorContains(t, err, "clearance is empty")
		_, err = activation.NewWithheldByDecision(registration.EventSetup, reason, "tracker-item-ab12cd")
		require.ErrorContains(t, err, "bare task-tracker id")
		entry, err := activation.NewWithheldByDecision(registration.EventSetup, reason, clearance)
		require.NoError(t, err)
		require.True(t, entry.IsValid())
		require.Equal(t, clearance, entry.Clearance)
		entry.Clearance = ""
		require.False(t, entry.IsValid(), "a decision entry that lost its clearance is invalid")
	}
	_, err := activation.NewWithheldByDecision(registration.EventSetup, activation.WithheldOutsideTargetSet, clearance)
	require.ErrorContains(t, err, "is not a user decision and carries no clearance path")
	entry, err := activation.NewWithheld(registration.EventSetup, activation.WithheldOutsideTargetSet)
	require.NoError(t, err)
	entry.Clearance = clearance
	require.False(t, entry.IsValid(), "a non-decision entry that names a clearance is invalid")
	enabled, err := activation.NewEnabled(registration.EventSessionStart, activation.CaptureProofSessionStart, activation.ProductionProofSessionStart)
	require.NoError(t, err)
	enabled.Clearance = clearance
	require.False(t, enabled.IsValid(), "an enabled entry that names a clearance is invalid")
}

// TestWithheldReasonStringMirrorsEveryArm applies the shared enum-sync helper:
// every arm the sentinel-bounded population yields must have a non-empty,
// distinct String(). An arm added above the sentinel without a String() case
// turns this RED naming the arm.
func TestWithheldReasonStringMirrorsEveryArm(t *testing.T) {
	t.Parallel()
	testutil.RequireEnumMirrorComplete(t, testutil.EnumMirror[activation.WithheldReason]{
		Subject: "activation.WithheldReason -> String()",
		Arms:    activation.AllWithheldReasons(),
		Mirror: func(arm activation.WithheldReason) (string, bool) {
			text := arm.String()
			return text, text != ""
		},
		Describe: func(arm activation.WithheldReason) string { return fmt.Sprintf("WithheldReason(%d)", arm) },
	})
}

// TestGeneratedProofConstantsMirrorTheDeclarationTablesBothWays applies the
// shared enum-sync helper to the proof enums against the three target files:
// every declared arm has a generated constant of the same ordinal, and every
// generated constant has a declared arm. A declaration row added without
// regeneration, or a generated constant edited by hand, turns this RED
// naming the arm.
func TestGeneratedProofConstantsMirrorTheDeclarationTablesBothWays(t *testing.T) {
	t.Parallel()
	generatedCapture := activation.GeneratedCaptureProofs()
	declaredCapture := activation.CaptureProofArms()
	testutil.RequireEnumMirrorComplete(t, testutil.EnumMirror[activation.CaptureProofArm]{
		Subject: "declared capture proof arms (the three target files) -> generated CaptureProof constants",
		Arms:    declaredCapture,
		Mirror: func(arm activation.CaptureProofArm) (string, bool) {
			generated, ok := generatedCapture[arm.Arm]
			if !ok || generated != arm.Proof {
				return "", false
			}
			return fmt.Sprintf("CaptureProof%s=%d", arm.Arm, generated), true
		},
		Describe: func(arm activation.CaptureProofArm) string {
			return fmt.Sprintf("%s capture arm %q ordinal %d (run make generate)", arm.Harness, arm.Arm, arm.Proof)
		},
	})
	declaredCaptureByArm := map[string]activation.CaptureProof{}
	for _, arm := range declaredCapture {
		declaredCaptureByArm[arm.Arm] = arm.Proof
	}
	generatedCaptureNames := make([]string, 0, len(generatedCapture))
	for name := range generatedCapture {
		generatedCaptureNames = append(generatedCaptureNames, name)
	}
	sort.Strings(generatedCaptureNames)
	testutil.RequireEnumMirrorComplete(t, testutil.EnumMirror[string]{
		Subject: "generated CaptureProof constants -> declared capture proof arms",
		Arms:    generatedCaptureNames,
		Mirror: func(name string) (string, bool) {
			declared, ok := declaredCaptureByArm[name]
			if !ok || declared != generatedCapture[name] {
				return "", false
			}
			return fmt.Sprintf("%s=%d", name, declared), true
		},
	})

	generatedProduction := activation.GeneratedProductionProofs()
	declaredProduction := activation.ProductionProofArms()
	testutil.RequireEnumMirrorComplete(t, testutil.EnumMirror[activation.ProductionProofArm]{
		Subject: "declared production proof arms (the three target files) -> generated ProductionProof constants",
		Arms:    declaredProduction,
		Mirror: func(arm activation.ProductionProofArm) (string, bool) {
			generated, ok := generatedProduction[arm.Arm]
			if !ok || generated != arm.Proof {
				return "", false
			}
			return fmt.Sprintf("ProductionProof%s=%d", arm.Arm, generated), true
		},
		Describe: func(arm activation.ProductionProofArm) string {
			return fmt.Sprintf("%s production arm %q ordinal %d (run make generate)", arm.Harness, arm.Arm, arm.Proof)
		},
	})
	declaredProductionByArm := map[string]activation.ProductionProof{}
	for _, arm := range declaredProduction {
		declaredProductionByArm[arm.Arm] = arm.Proof
	}
	generatedProductionNames := make([]string, 0, len(generatedProduction))
	for name := range generatedProduction {
		generatedProductionNames = append(generatedProductionNames, name)
	}
	sort.Strings(generatedProductionNames)
	testutil.RequireEnumMirrorComplete(t, testutil.EnumMirror[string]{
		Subject: "generated ProductionProof constants -> declared production proof arms",
		Arms:    generatedProductionNames,
		Mirror: func(name string) (string, bool) {
			declared, ok := declaredProductionByArm[name]
			if !ok || declared != generatedProduction[name] {
				return "", false
			}
			return fmt.Sprintf("%s=%d", name, declared), true
		},
	})
}
