package activation_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
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

// openCode2Enabled is the enabled 2.0.20 set in registration order: the nine
// coordinates whose authentic capture was cleared in
// internal/lifecycle/ingress/opencode/testdata/fixtures/CLEARANCE.md.
var openCode2Enabled = []model.ContractEventKind{
	registration.EventOpenCode2SessionPrompt,
	registration.EventOpenCode2SessionContext,
	registration.EventOpenCode2SessionTitle,
	registration.EventOpenCode2SessionModelRequest,
	registration.EventOpenCode2SessionHttpRequest,
	registration.EventOpenCode2SessionHttpResponse,
	registration.EventOpenCode2ToolExecuteBefore,
	registration.EventOpenCode2ToolExecuteAfter,
	registration.EventOpenCode2PermissionEvaluate,
}

// openCode2MissingFixture is the withheld missing-fixture remainder: the seven
// coordinates that did not fire in the 2.0.20 capture sitting.
var openCode2MissingFixture = []model.ContractEventKind{
	registration.EventOpenCode2SessionCreated,
	registration.EventOpenCode2SessionCompaction,
	registration.EventOpenCode2SessionGenerate,
	registration.EventOpenCode2SessionExperimentalWsHandshake,
	registration.EventOpenCode2SessionExperimentalWsSend,
	registration.EventOpenCode2SessionExperimentalWsReceive,
	registration.EventOpenCode2SessionRetry,
}

const openCodeClearance = "internal/lifecycle/ingress/opencode/testdata/fixtures/CLEARANCE.md"

// TestOpenCodeV2ActivationEnablesClearedRowsAndWithholdsTheRestByReason pins
// the 2.0.20 posture: nine enabled rows each carry both proofs, the shell
// create row is withheld by the recorded unclearable-payload decision, and
// the seven non-fired rows stay withheld missing-fixture.
func TestOpenCodeV2ActivationEnablesClearedRowsAndWithholdsTheRestByReason(t *testing.T) {
	t.Parallel()
	entries, err := activation.OpenCode2_0_20()
	require.NoError(t, err)
	require.Len(t, entries, 17)
	var enabled, missing []model.ContractEventKind
	var unclearable []model.ContractEventKind
	for _, entry := range entries {
		require.True(t, entry.IsValid())
		switch {
		case entry.State == activation.Enabled:
			enabled = append(enabled, entry.Event)
			require.NotZero(t, entry.CaptureProof)
			require.NotZero(t, entry.ProductionProof)
			require.NotEmpty(t, entry.CaptureProof.Name())
			require.NotEmpty(t, entry.ProductionProof.Name())
			require.Empty(t, entry.Clearance)
		case entry.Reason == activation.WithheldMissingFixture:
			missing = append(missing, entry.Event)
			require.Zero(t, entry.CaptureProof)
			require.Zero(t, entry.ProductionProof)
			require.Empty(t, entry.Clearance)
		case entry.Reason == activation.WithheldUnclearablePayload:
			unclearable = append(unclearable, entry.Event)
			require.Zero(t, entry.CaptureProof)
			require.Zero(t, entry.ProductionProof)
			require.Equal(t, openCodeClearance, entry.Clearance)
		default:
			t.Fatalf("event %d carries unexpected state %v reason %v", entry.Event, entry.State, entry.Reason)
		}
	}
	require.Equal(t, openCode2Enabled, enabled)
	require.Equal(t, openCode2MissingFixture, missing)
	require.Equal(t, []model.ContractEventKind{registration.EventOpenCode2ShellCreateBefore}, unclearable)
}

// TestOpenCodeV2EnabledProofsCiteCommittedFixturesAndProductionTest pins each
// enabled row to the exact committed fixture basename its capture proof
// names and to the built-binary production proof arm for that coordinate.
func TestOpenCodeV2EnabledProofsCiteCommittedFixturesAndProductionTest(t *testing.T) {
	t.Parallel()
	want := map[model.ContractEventKind][2]string{
		registration.EventOpenCode2SessionPrompt:       {"opencode_session_prompt_2_0_20.1.json", "session.prompt"},
		registration.EventOpenCode2SessionContext:      {"opencode_session_context_2_0_20.1.json", "session.context"},
		registration.EventOpenCode2SessionTitle:        {"opencode_session_title_2_0_20.1.json", "session.title"},
		registration.EventOpenCode2SessionModelRequest: {"opencode_session_model_request_2_0_20.2.json", "session.model.request"},
		registration.EventOpenCode2SessionHttpRequest:  {"opencode_session_http_request_2_0_20.2.json", "session.http.request"},
		registration.EventOpenCode2SessionHttpResponse: {"opencode_session_http_response_2_0_20.1.json", "session.http.response"},
		registration.EventOpenCode2ToolExecuteBefore:   {"opencode_tool_execute_before_2_0_20.1.json", "tool.execute.before"},
		registration.EventOpenCode2ToolExecuteAfter:    {"opencode_tool_execute_after_2_0_20.1.json", "tool.execute.after"},
		registration.EventOpenCode2PermissionEvaluate:  {"opencode_permission_evaluate_2_0_20.2.json", "permission.evaluate"},
	}
	entries, err := activation.OpenCode2_0_20()
	require.NoError(t, err)
	seen := 0
	for _, entry := range entries {
		expected, ok := want[entry.Event]
		if !ok {
			continue
		}
		seen++
		require.Equal(t, activation.Enabled, entry.State)
		require.True(t, strings.HasPrefix(entry.CaptureProof.Name(), "internal/lifecycle/ingress/opencode/testdata/fixtures/"+expected[0]+" "), "capture proof %q", entry.CaptureProof.Name())
		require.Equal(t, "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/"+expected[1], entry.ProductionProof.Name())
	}
	require.Equal(t, len(want), seen)
}

func TestOpenCodeV2ActivationTargetEventsAreDefensive(t *testing.T) {
	t.Parallel()
	targets := activation.OpenCode2_0_20TargetEvents()
	require.Len(t, targets, 17)
	targets[0] = 0
	require.Equal(t, registration.EventOpenCode2SessionCreated, activation.OpenCode2_0_20TargetEvents()[0])
}

// TestOpenCodeV2ActivationIsExhaustiveInRegistrationOrder pins the v2 report
// shape: seventeen entries, one per generated 2.0.20 event, in registration
// order, each a valid row. A second derivation is fresh, so no caller can mutate the static
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
	}
	require.Equal(t, activation.Withheld, entries[0].State, "session.created did not fire and stays withheld")
	entries[0].State = activation.Enabled
	fresh, err := activation.OpenCode2_0_20()
	require.NoError(t, err)
	require.Equal(t, activation.Withheld, fresh[0].State, "the manifest derivation must be fresh")
}

// TestOpenCodeV2PerRowCapabilityDerivesNoneWhileFixturesAreAbsent pins the
// per-row capability without needing any fixture: every 2.0.20 coordinate
// derives CapabilityNone through the version-exact v2 runtime profile, and
// the failure-evidence posture splits exactly on blocking mode (the bus
// observation carries no evidence source, every hook row awaits its
// citation). Enabling a row by its proofs claims no capability: the
// cleared permission evaluate capture answered allow, so it cites no deny
// evidence and the row keeps CapabilityNone. Rows resolve through
// OpenCode2_0_20Lifecycle by native name, never through the version-blind
// failure lookup that serves the command-line fault policy, so the three
// coordinates both versions share assert their v2 rows.
func TestOpenCodeV2PerRowCapabilityDerivesNoneWhileFixturesAreAbsent(t *testing.T) {
	t.Parallel()
	entries, err := activation.OpenCode2_0_20()
	require.NoError(t, err)
	manifest := registration.OpenCode2_0_20().Entries()
	byKind := make(map[model.ContractEventKind]string, len(manifest))
	for _, event := range manifest {
		byKind[event.Kind] = event.NativeName
	}
	contract := runtime.OpenCode2_0_20Lifecycle()
	byName := make(map[string]runtime.LifecycleEventMapping, len(manifest))
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		require.NoError(t, err)
		byName[mapping.NativeName()] = mapping
	}
	require.Len(t, byName, 17, "the v2 lifecycle contract must declare seventeen version-exact rows")
	require.Len(t, entries, 17)
	for _, entry := range entries {
		nativeName, found := byKind[entry.Event]
		require.True(t, found, "activation entry for kind %d has no registered native name", entry.Event)
		mapping, ok := byName[nativeName]
		require.True(t, ok, "coordinate %q has no version-exact v2 runtime row", nativeName)
		require.True(t, mapping.Response().IsValid(), "coordinate %q carries an invalid capability", nativeName)
		require.Equal(t, runtime.CapabilityNone, mapping.Response(), "coordinate %q must derive none until its citation lands", nativeName)
		if nativeName == "session.created" {
			require.Equal(t, runtime.NonBlocking, mapping.Blocking())
			require.False(t, mapping.Evidence().IsPresent(), "the bus observation must cite no evidence")
			continue
		}
		require.Equal(t, runtime.Blocking, mapping.Blocking(), "hook coordinate %q must stay a blocking gate", nativeName)
		require.False(t, mapping.Evidence().IsPresent(), "hook coordinate %q must cite no response-channel evidence yet", nativeName)
	}
}

// TestOpenCodeV2ExpectedFixtureNamesFollowTheCaptureRule pins the capture
// naming rule without needing any fixture file: each native coordinate maps
// to opencode_<snake>_2_0_20.1.json in
// internal/lifecycle/ingress/opencode/testdata/fixtures/, exercised through
// the production CaptureStem namer rather than a re-implemented spelling.
// The rule fixes the stem; the committed sequence number follows the
// smallest-authentic-file policy, so the permission evaluate fixture is .2.
func TestOpenCodeV2ExpectedFixtureNamesFollowTheCaptureRule(t *testing.T) {
	t.Parallel()
	manifest := registration.OpenCode2_0_20().Entries()
	require.Len(t, manifest, 17)
	seen := make(map[string]struct{}, len(manifest))
	for _, event := range manifest {
		stem, ok := handlers.CaptureStem(ir.HarnessOpenCode, event.NativeName, "2.0.20")
		require.True(t, ok, "coordinate %q must form a production capture stem", event.NativeName)
		basename := stem + ".1.json"
		_, duplicate := seen[basename]
		require.False(t, duplicate, "two coordinates map to one expected fixture %q", basename)
		seen[basename] = struct{}{}
		if event.NativeName == "permission.evaluate" {
			require.Equal(t, "opencode_permission_evaluate_2_0_20", stem)
		}
	}
	require.Len(t, seen, 17, "every coordinate needs its own expected fixture")
}

// TestOpenCodeV2ProofsBindOnlyTheEnabledSet pins the proof side: every
// declared proof bound to a 2.0.20 event belongs to the enabled set, and each
// enabled event has exactly one capture and one production proof.
func TestOpenCodeV2ProofsBindOnlyTheEnabledSet(t *testing.T) {
	t.Parallel()
	enabled := make(map[model.ContractEventKind]struct{}, len(openCode2Enabled))
	for _, kind := range openCode2Enabled {
		enabled[kind] = struct{}{}
	}
	v2Kinds := make(map[model.ContractEventKind]struct{}, 17)
	for _, kind := range activation.OpenCode2_0_20TargetEvents() {
		v2Kinds[kind] = struct{}{}
	}
	require.Len(t, v2Kinds, 17)
	captures := map[model.ContractEventKind]int{}
	for _, arm := range activation.CaptureProofArms() {
		if _, v2 := v2Kinds[arm.Event]; v2 {
			_, ok := enabled[arm.Event]
			require.True(t, ok, "capture proof %q binds a withheld 2.0.20 event", arm.Arm)
			captures[arm.Event]++
		}
	}
	productions := map[model.ContractEventKind]int{}
	for _, arm := range activation.ProductionProofArms() {
		if _, v2 := v2Kinds[arm.Event]; v2 {
			_, ok := enabled[arm.Event]
			require.True(t, ok, "production proof %q binds a withheld 2.0.20 event", arm.Arm)
			productions[arm.Event]++
		}
	}
	for _, kind := range openCode2Enabled {
		require.Equal(t, 1, captures[kind])
		require.Equal(t, 1, productions[kind])
	}
}
