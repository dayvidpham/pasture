package activation_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func TestOpenCodeActivationRequiresBothExactProofs(t *testing.T) {
	t.Parallel()
	entries, err := activation.OpenCode1_18_29()
	require.NoError(t, err)
	require.Len(t, entries, 47)

	enabled := make([]model.ContractEventKind, 0, 2)
	for _, entry := range entries {
		require.True(t, entry.IsValid())
		if entry.State == activation.Enabled {
			enabled = append(enabled, entry.Event)
			require.NotEmpty(t, entry.CaptureProof.Name())
			require.NotEmpty(t, entry.ProductionProof.Name())
			continue
		}
		require.Equal(t, activation.WithheldOutsideTargetSet, entry.Reason)
		require.Zero(t, entry.CaptureProof)
		require.Zero(t, entry.ProductionProof)
	}
	require.Equal(t, []model.ContractEventKind{
		registration.EventOpenCodeSessionCreated,
		registration.EventOpenCodeToolExecuteBefore,
	}, enabled)
}

func TestOpenCodeCatalogMembershipDoesNotActivateAnotherEvent(t *testing.T) {
	t.Parallel()
	entries, err := activation.OpenCode1_18_29()
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.Event == registration.EventOpenCodeSessionUpdated {
			require.Equal(t, activation.Withheld, entry.State)
			return
		}
	}
	t.Fatal("generated source catalog omitted session.updated")
}

func TestOpenCodeActivationTargetEventsAreDefensive(t *testing.T) {
	t.Parallel()
	targets := activation.OpenCode1_18_29TargetEvents()
	require.Len(t, targets, 2)
	targets[0] = 0
	require.Equal(t, registration.EventOpenCodeSessionCreated, activation.OpenCode1_18_29TargetEvents()[0])
}

func TestOpenCodeV2ActivationWithholdsEveryRowForMissingFixture(t *testing.T) {
	t.Parallel()
	entries, err := activation.OpenCode2_0_20()
	require.NoError(t, err)
	require.Len(t, entries, 17)
	for _, entry := range entries {
		require.True(t, entry.IsValid())
		require.Equal(t, activation.Withheld, entry.State)
		require.Equal(t, activation.WithheldMissingFixture, entry.Reason)
		require.Zero(t, entry.CaptureProof)
		require.Zero(t, entry.ProductionProof)
	}
}

func TestOpenCodeV2ActivationTargetEventsAreDefensive(t *testing.T) {
	t.Parallel()
	targets := activation.OpenCode2_0_20TargetEvents()
	require.Len(t, targets, 17)
	targets[0] = 0
	require.Equal(t, registration.EventOpenCode2SessionCreated, activation.OpenCode2_0_20TargetEvents()[0])
}

// TestOpenCodeV2ActivationIsExhaustiveInRegistrationOrder pins the v2 report
// shape without needing any fixture: seventeen entries, one per generated
// 2.0.20 event, in registration order, each a valid withheld row with no
// proofs. A second derivation is fresh, so no caller can mutate the static
// table through a returned slice.
func TestOpenCodeV2ActivationIsExhaustiveInRegistrationOrder(t *testing.T) {
	t.Parallel()
	entries, err := activation.OpenCode2_0_20()
	require.NoError(t, err)
	manifest := registration.OpenCode2_0_20().Entries()
	require.Len(t, manifest, 17)
	require.Len(t, entries, len(manifest))
	seen := make(map[model.ContractEventKind]struct{}, len(entries))
	for index, entry := range entries {
		require.True(t, entry.IsValid(), "row %d is not a valid activation entry", index)
		require.Equal(t, manifest[index].Kind, entry.Event, "row %d leaves registration order", index)
		_, duplicate := seen[entry.Event]
		require.False(t, duplicate, "row %d repeats event kind %d", index, entry.Event)
		seen[entry.Event] = struct{}{}
		require.Equal(t, activation.Withheld, entry.State)
		require.Equal(t, activation.WithheldMissingFixture, entry.Reason)
		require.Zero(t, entry.CaptureProof)
		require.Zero(t, entry.ProductionProof)
		require.Empty(t, entry.Clearance)
	}
	entries[0].State = activation.Enabled
	fresh, err := activation.OpenCode2_0_20()
	require.NoError(t, err)
	require.Equal(t, activation.Withheld, fresh[0].State, "the manifest derivation must be fresh")
}

// TestOpenCodeV2PerRowCapabilityDerivesNoneWhileFixturesAreAbsent pins the
// per-row capability without needing any fixture: every 2.0.20 coordinate
// derives CapabilityNone through the pinned runtime profile, and the
// failure-evidence posture splits exactly on blocking mode (the bus
// observation carries no evidence source, every hook row awaits its
// citation). The activation entries above stay withheld for the same
// reason: no row carries a proof.
func TestOpenCodeV2PerRowCapabilityDerivesNoneWhileFixturesAreAbsent(t *testing.T) {
	t.Parallel()
	entries, err := activation.OpenCode2_0_20()
	require.NoError(t, err)
	manifest := registration.OpenCode2_0_20().Entries()
	byKind := make(map[model.ContractEventKind]string, len(manifest))
	for _, event := range manifest {
		byKind[event.Kind] = event.NativeName
	}
	require.Len(t, entries, 17)
	for _, entry := range entries {
		nativeName, found := byKind[entry.Event]
		require.True(t, found, "activation entry for kind %d has no registered native name", entry.Event)
		policy, ok := runtime.LookupLifecycleFailure(ir.HarnessOpenCode, nativeName)
		require.True(t, ok, "coordinate %q has no pinned runtime row", nativeName)
		require.True(t, policy.Response.IsValid(), "coordinate %q carries an invalid capability", nativeName)
		require.Equal(t, runtime.CapabilityNone, policy.Response, "coordinate %q must derive none until its citation lands", nativeName)
		if nativeName == "session.created" {
			require.Equal(t, runtime.NonBlocking, policy.Blocking)
			require.False(t, policy.Evidence.IsPresent(), "the bus observation must cite no evidence")
			continue
		}
		require.Equal(t, runtime.Blocking, policy.Blocking, "hook coordinate %q must stay a blocking gate", nativeName)
		require.False(t, policy.Evidence.IsPresent(), "hook coordinate %q must cite no response-channel evidence yet", nativeName)
	}
}

// TestOpenCodeV2ExpectedFixtureNamesFollowTheCaptureRule pins the capture
// naming rule without needing any fixture file: each native coordinate maps
// to opencode_<snake>_2_0_20.1.json in
// internal/lifecycle/ingress/opencode/testdata/fixtures/, where the snake
// spells dots with underscores. The permission evaluate coordinate maps to
// exactly opencode_permission_evaluate_2_0_20.1.json.
func TestOpenCodeV2ExpectedFixtureNamesFollowTheCaptureRule(t *testing.T) {
	t.Parallel()
	manifest := registration.OpenCode2_0_20().Entries()
	require.Len(t, manifest, 17)
	const fixtureDir = "internal/lifecycle/ingress/opencode/testdata/fixtures"
	seen := make(map[string]struct{}, len(manifest))
	for _, event := range manifest {
		snake := strings.ReplaceAll(event.NativeName, ".", "_")
		require.NotContains(t, snake, ".", "coordinate %q must spell fully with underscores", event.NativeName)
		require.NotContains(t, snake, "-", "coordinate %q must spell fully with underscores", event.NativeName)
		basename := "opencode_" + snake + "_2_0_20.1.json"
		require.True(t, strings.HasPrefix(basename, "opencode_"), "basename %q must carry the harness prefix", basename)
		require.True(t, strings.HasSuffix(basename, "_2_0_20.1.json"), "basename %q must carry the versioned sequence suffix", basename)
		_, duplicate := seen[basename]
		require.False(t, duplicate, "two coordinates map to one expected fixture %q", basename)
		seen[basename] = struct{}{}
		if event.NativeName == "permission.evaluate" {
			require.Equal(t, "opencode_permission_evaluate_2_0_20.1.json", basename)
			require.Equal(t, fixtureDir+"/opencode_permission_evaluate_2_0_20.1.json", fixtureDir+"/"+basename)
		}
	}
	require.Len(t, seen, 17, "every coordinate needs its own expected fixture")
}

// TestOpenCodeV2DeclaresNoProofsYet pins the withheld posture from the proof
// side: no declared capture or production proof is bound to a 2.0.20 event,
// so no withheld row can be enabled by referencing an existing proof. When
// the capture lands, enabling a row declares its proofs first; that flip is
// data, and this test names the exact growth to expect.
func TestOpenCodeV2DeclaresNoProofsYet(t *testing.T) {
	t.Parallel()
	v2Kinds := make(map[model.ContractEventKind]struct{}, 17)
	for _, kind := range activation.OpenCode2_0_20TargetEvents() {
		v2Kinds[kind] = struct{}{}
	}
	require.Len(t, v2Kinds, 17)
	for _, arm := range activation.CaptureProofArms() {
		_, bound := v2Kinds[arm.Event]
		require.False(t, bound, "capture proof %q is already bound to a 2.0.20 event", arm.Arm)
	}
	for _, arm := range activation.ProductionProofArms() {
		_, bound := v2Kinds[arm.Event]
		require.False(t, bound, "production proof %q is already bound to a 2.0.20 event", arm.Arm)
	}
}
