package runtime_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const lifecycleContractsFixture testutil.FixtureName = "lifecycle_contracts"

type lifecycleFixture struct {
	Contracts   []lifecycleContractFixture `yaml:"contracts"`
	Unsupported unsupportedFixture         `yaml:"unsupported"`
	NoAdapters  []string                   `yaml:"no_adapters"`
}

type lifecycleContractFixture struct {
	Harness    string                   `yaml:"harness"`
	ID         string                   `yaml:"id"`
	Version    string                   `yaml:"version"`
	Lower      string                   `yaml:"lower"`
	Higher     string                   `yaml:"higher"`
	EventOrder []string                 `yaml:"event_order"`
	Axes       lifecycleAxesFixture     `yaml:"axes"`
	Identities lifecycleIdentityFixture `yaml:"identities"`
}

type lifecycleAxesFixture struct {
	Semantic       map[string][]string `yaml:"semantic"`
	Surface        map[string][]string `yaml:"surface"`
	Blocking       map[string][]string `yaml:"blocking"`
	Mutation       map[string][]string `yaml:"mutation"`
	Order          map[string][]string `yaml:"order"`
	Reconciliation map[string][]string `yaml:"reconciliation"`
	Failure        map[string][]string `yaml:"failure"`
	StopLoop       map[string][]string `yaml:"stop_loop"`
}

type lifecycleIdentityFixture struct {
	Default []string            `yaml:"default"`
	ByEvent map[string][]string `yaml:"by_event"`
}

type unsupportedFixture struct {
	Harness       string   `yaml:"harness"`
	ErrorContains []string `yaml:"error_contains"`
}

func loadLifecycleFixture(t *testing.T) lifecycleFixture {
	t.Helper()
	var fixture lifecycleFixture
	testutil.LoadFixtures(t, lifecycleContractsFixture, &fixture)
	return fixture
}

func lifecycleFixtureFor(t *testing.T, fixture lifecycleFixture, harness string) lifecycleContractFixture {
	t.Helper()
	for _, contract := range fixture.Contracts {
		if contract.Harness == harness {
			return contract
		}
	}
	t.Fatalf("lifecycle fixture has no contract for harness %q", harness)
	return lifecycleContractFixture{}
}

func axisValue(t *testing.T, axisName, event string, groups map[string][]string, known map[string]struct{}) string {
	t.Helper()
	var found string
	for value, events := range groups {
		for _, candidate := range events {
			if _, ok := known[candidate]; !ok {
				t.Errorf("%s axis %q references unknown event %q", axisName, value, candidate)
			}
			if candidate != event {
				continue
			}
			if found != "" {
				t.Errorf("event %q appears in %s groups %q and %q", event, axisName, found, value)
			}
			found = value
		}
	}
	require.NotEmpty(t, found, "event %q must appear in exactly one %s group", event, axisName)
	return found
}

func identityStrings(fields []runtime.NativeIdentityField) []string {
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		requirement := "optional"
		if field.Required() {
			requirement = "required"
		}
		result = append(result, fmt.Sprintf("%s:%s:%s", field.Kind(), field.NativeName(), requirement))
	}
	return result
}

func assertLifecycleContract[E comparable](
	t *testing.T,
	want lifecycleContractFixture,
	contract runtime.LifecycleContract[E],
	events []E,
	nativeName func(E) string,
) {
	t.Helper()
	require.True(t, contract.IsValid())
	assert.Equal(t, want.ID, contract.ID().String())
	assert.Equal(t, want.Harness, string(contract.Harness()))
	assert.Equal(t, want.Version, contract.Versions().Min().String())
	assert.Equal(t, want.Version, contract.Versions().Max().String())
	assert.True(t, contract.Supports(mustParse(t, want.Version)))
	assert.False(t, contract.Supports(mustParse(t, want.Lower)))
	assert.False(t, contract.Supports(mustParse(t, want.Higher)))

	actualEvents := contract.Events()
	require.Equal(t, events, actualEvents, "the contract and exported typed enumeration must share one order")
	actualNames := make([]string, len(actualEvents))
	known := make(map[string]struct{}, len(actualEvents))
	for index, event := range actualEvents {
		name := nativeName(event)
		actualNames[index] = name
		if _, duplicate := known[name]; duplicate {
			t.Fatalf("typed lifecycle enumeration repeats native name %q", name)
		}
		known[name] = struct{}{}
	}
	require.Equal(t, want.EventOrder, actualNames)

	for _, event := range actualEvents {
		name := nativeName(event)
		mapping, err := contract.Mapping(event)
		require.NoError(t, err, "mapping %s", name)
		assert.Equal(t, name, mapping.NativeName())
		assert.Equal(t, axisValue(t, "semantic", name, want.Axes.Semantic, known), mapping.Semantic().String())
		assert.Equal(t, axisValue(t, "surface", name, want.Axes.Surface, known), mapping.Surface().String())
		assert.Equal(t, axisValue(t, "blocking", name, want.Axes.Blocking, known), mapping.Blocking().String())
		assert.Equal(t, axisValue(t, "mutation", name, want.Axes.Mutation, known), mapping.Mutation().String())
		assert.Equal(t, axisValue(t, "order", name, want.Axes.Order, known), mapping.Order().String())
		assert.Equal(t, axisValue(t, "reconciliation", name, want.Axes.Reconciliation, known), mapping.Reconciliation().String())
		assert.Equal(t, axisValue(t, "failure", name, want.Axes.Failure, known), mapping.Failure().String())
		assert.Equal(t, axisValue(t, "stop-loop", name, want.Axes.StopLoop, known), mapping.StopLoop().String())

		wantIdentities := want.Identities.Default
		if override, ok := want.Identities.ByEvent[name]; ok {
			wantIdentities = override
		}
		identities := mapping.Identities()
		assert.Equal(t, wantIdentities, identityStrings(identities), "identity contract for %s", name)
		for _, identity := range identities {
			assert.True(t, identity.IsValid())
			lowerName := strings.ToLower(identity.NativeName())
			for _, authorityName := range []string{"journal", "revision", "evidence", "publication", "review", "actor", "assignment", "repository", "git_ref"} {
				assert.NotContains(t, lowerName, authorityName, "native event identity must not transport Pasture authority")
			}
		}

		// Returned identity slices are defensive: a consumer cannot mutate the
		// contract table shared by subsequent code generation.
		if len(identities) > 0 {
			identities[0] = runtime.NativeIdentityField{}
			again, err := contract.Mapping(event)
			require.NoError(t, err)
			assert.Equal(t, wantIdentities, identityStrings(again.Identities()))
		}
	}

	// Returned event order is defensive as well.
	require.NotEmpty(t, actualEvents)
	originalFirst := actualEvents[0]
	var zero E
	actualEvents[0] = zero
	assert.Equal(t, originalFirst, contract.Events()[0])
	_, err := contract.Mapping(zero)
	require.Error(t, err, "the zero typed event must never fall through to a native name")
}

func TestPinnedLifecycleContractsMatchStrictFixture(t *testing.T) {
	t.Parallel()
	fixture := loadLifecycleFixture(t)

	t.Run("claude-code", func(t *testing.T) {
		t.Parallel()
		assertLifecycleContract(
			t,
			lifecycleFixtureFor(t, fixture, "claude-code"),
			runtime.ClaudeCode2_1_210Lifecycle(),
			runtime.ClaudeLifecycleEvents(),
			func(event runtime.ClaudeLifecycleEvent) string { return event.NativeName() },
		)
	})

	t.Run("codex", func(t *testing.T) {
		t.Parallel()
		assertLifecycleContract(
			t,
			lifecycleFixtureFor(t, fixture, "codex"),
			runtime.Codex0_144_1Lifecycle(),
			runtime.CodexLifecycleEvents(),
			func(event runtime.CodexLifecycleEvent) string { return event.NativeName() },
		)
	})

	t.Run("opencode", func(t *testing.T) {
		t.Parallel()
		assertLifecycleContract(
			t,
			lifecycleFixtureFor(t, fixture, "opencode"),
			runtime.OpenCode1_17_18Lifecycle(),
			runtime.OpenCodeLifecycleEvents(),
			func(event runtime.OpenCodeLifecycleEvent) string { return event.NativeName() },
		)
	})
}

func TestClaudeLifecyclePreservesBatchRequestAndStopSemantics(t *testing.T) {
	t.Parallel()
	contract := runtime.ClaudeCode2_1_210Lifecycle()

	batch, err := contract.Mapping(runtime.ClaudeEventPostToolBatch)
	require.NoError(t, err)
	assert.Equal(t, runtime.SemanticGateConsultation, batch.Semantic())
	assert.Equal(t, runtime.Blocking, batch.Blocking())
	assert.Equal(t, runtime.FailureExitTwoBlocks, batch.Failure())

	permission, err := contract.Mapping(runtime.ClaudeEventPermissionRequest)
	require.NoError(t, err)
	assert.Equal(t, runtime.SemanticGateConsultation, permission.Semantic(), "a generic permission event does not manufacture a user decision")
	assert.True(t, hasNativeIdentity(permission, runtime.IdentityRequest, "request_id"))

	response, err := contract.Mapping(runtime.ClaudeEventElicitationResult)
	require.NoError(t, err)
	assert.Equal(t, runtime.SemanticExplicitHumanResponse, response.Semantic())
	assert.True(t, hasNativeIdentity(response, runtime.IdentityRequest, "request_id"))

	for _, event := range []runtime.ClaudeLifecycleEvent{runtime.ClaudeEventStop, runtime.ClaudeEventSubagentStop} {
		mapping, err := contract.Mapping(event)
		require.NoError(t, err)
		assert.Equal(t, runtime.StopLoopConsultWhenInactive, mapping.StopLoop())
	}
}

func TestCodexLifecyclePreservesStrictMutationAndConcurrencyWithoutMerge(t *testing.T) {
	t.Parallel()
	contract := runtime.Codex0_144_1Lifecycle()

	pre, err := contract.Mapping(runtime.CodexEventPreToolUse)
	require.NoError(t, err)
	assert.Equal(t, runtime.SurfaceCodexStrictCommandJSON, pre.Surface())
	assert.Equal(t, runtime.MutationInput, pre.Mutation())
	assert.Equal(t, runtime.OrderConcurrentNative, pre.Order())
	assert.Equal(t, runtime.ReconcileNoAdapterMerge, pre.Reconciliation())
	assert.Equal(t, runtime.FailureStrictExitTwoBlocks, pre.Failure())

	post, err := contract.Mapping(runtime.CodexEventPostToolUse)
	require.NoError(t, err)
	assert.Equal(t, runtime.MutationOutput, post.Mutation())

	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		require.NoError(t, err)
		assert.Equal(t, runtime.ReconcileNoAdapterMerge, mapping.Reconciliation())
		for _, identity := range mapping.Identities() {
			assert.NotEqual(t, "transcript_path", identity.NativeName(), "unstable Codex transcripts are never identity or adapter input")
		}
	}
}

func TestOpenCodeLifecycleSeparatesNamedHandlersFromCatchAllAndSSE(t *testing.T) {
	t.Parallel()
	contract := runtime.OpenCode1_17_18Lifecycle()

	named, err := contract.Mapping(runtime.OpenCodeEventToolExecuteBefore)
	require.NoError(t, err)
	assert.Equal(t, runtime.SemanticGateConsultation, named.Semantic())
	assert.Equal(t, runtime.SurfaceOpenCodeNamedOutput, named.Surface())
	assert.Equal(t, runtime.Blocking, named.Blocking())
	assert.Equal(t, runtime.MutationOutputObject, named.Mutation())
	assert.Equal(t, runtime.OrderSequentialLoad, named.Order())
	assert.Equal(t, runtime.ReconcileSequentialMutation, named.Reconciliation())
	assert.Equal(t, runtime.FailureThrowFailFast, named.Failure())

	observed, err := contract.Mapping(runtime.OpenCodeEventSessionCreated)
	require.NoError(t, err)
	assert.Equal(t, runtime.SemanticObservation, observed.Semantic())
	assert.Equal(t, runtime.SurfaceOpenCodeCatchAllSSE, observed.Surface())
	assert.Equal(t, runtime.NonBlocking, observed.Blocking())
	assert.Equal(t, runtime.MutationNone, observed.Mutation())
	assert.Equal(t, runtime.FailureObserveOnly, observed.Failure())

	permissionReply, err := contract.Mapping(runtime.OpenCodeEventPermissionReplied)
	require.NoError(t, err)
	assert.Equal(t, runtime.SemanticObservation, permissionReply.Semantic(), "a generic permission reply is not a Pasture user-gate response")
}

func TestAntigravityLifecycleContractFailsActionablyAndPiHasNoAdapter(t *testing.T) {
	t.Parallel()
	fixture := loadLifecycleFixture(t)
	require.Equal(t, "antigravity", fixture.Unsupported.Harness)

	err := runtime.AntigravityLifecycleContract()
	require.Error(t, err)
	assert.ErrorIs(t, err, runtime.ErrLifecycleAdapterUnsupported)
	for _, fragment := range fixture.Unsupported.ErrorContains {
		assert.Contains(t, err.Error(), fragment)
	}

	// Pi is intentionally absent from the production constructor set. This
	// fixture declaration keeps that non-adapter decision visible beside the
	// three supported profiles and the Antigravity unsupported result.
	assert.Equal(t, []string{"pi"}, fixture.NoAdapters)
}

func TestZeroLifecycleContractFailsClosed(t *testing.T) {
	t.Parallel()
	var contract runtime.LifecycleContract[runtime.ClaudeLifecycleEvent]
	assert.False(t, contract.IsValid())
	_, err := contract.Mapping(runtime.ClaudeEventSessionStart)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero contract")
}

func hasNativeIdentity(mapping runtime.LifecycleEventMapping, kind runtime.NativeIdentityKind, name string) bool {
	for _, identity := range mapping.Identities() {
		if identity.Kind() == kind && identity.NativeName() == name && identity.Required() {
			return true
		}
	}
	return false
}
