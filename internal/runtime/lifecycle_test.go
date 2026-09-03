package runtime_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/codegen/scan"
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
	MaxVersion string                   `yaml:"max_version"`
	Lower      string                   `yaml:"lower"`
	Higher     string                   `yaml:"higher"`
	EventOrder []string                 `yaml:"event_order"`
	Axes       lifecycleAxesFixture     `yaml:"axes"`
	Identities lifecycleIdentityFixture `yaml:"identities"`
	Unresolved map[string][]string      `yaml:"unresolved_by_event"`
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
	maxVersion := want.MaxVersion
	if maxVersion == "" {
		maxVersion = want.Version
	}
	assert.Equal(t, maxVersion, contract.Versions().Max().String())
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
		wantUnresolved := want.Unresolved[name]
		if wantUnresolved == nil {
			wantUnresolved = []string{}
		}
		actualUnresolved := mapping.UnresolvedIdentities()
		actualUnresolvedNames := make([]string, len(actualUnresolved))
		for index, kind := range actualUnresolved {
			actualUnresolvedNames[index] = kind.String()
		}
		assert.Equal(t, wantUnresolved, actualUnresolvedNames, "unresolved identity contract for %s", name)
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
		if len(actualUnresolved) > 0 {
			actualUnresolved[0] = runtime.IdentitySession
			again, err := contract.Mapping(event)
			require.NoError(t, err)
			assert.Equal(t, wantUnresolved, []string{again.UnresolvedIdentities()[0].String()})
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
			runtime.Codex0_146_0Lifecycle(),
			runtime.CodexLifecycleEvents(),
			func(event runtime.CodexLifecycleEvent) string { return event.NativeName() },
		)
	})

	t.Run("opencode", func(t *testing.T) {
		t.Parallel()
		assertLifecycleContract(
			t,
			lifecycleFixtureFor(t, fixture, "opencode"),
			runtime.OpenCode1_18_10Lifecycle(),
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
	// PostToolBatch still consults the gate, but the host reference does not
	// state that it blocks on exit 2, so the row cites no evidence and runs as
	// report-and-continue.
	assert.Equal(t, runtime.FailureReportAndContinue, batch.Failure())
	assert.False(t, batch.Evidence().IsPresent())
	assert.Equal(t, []runtime.NativeIdentityKind{runtime.IdentityToolCall}, batch.UnresolvedIdentities())
	unresolved := batch.UnresolvedIdentities()
	unresolved[0] = runtime.IdentitySession
	assert.Equal(t, []runtime.NativeIdentityKind{runtime.IdentityToolCall}, batch.UnresolvedIdentities(), "unresolved identity metadata must be defensively copied")

	sessionStart, err := contract.Mapping(runtime.ClaudeEventSessionStart)
	require.NoError(t, err)
	assert.Empty(t, sessionStart.UnresolvedIdentities())

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
	contract := runtime.Codex0_146_0Lifecycle()

	pre, err := contract.Mapping(runtime.CodexEventPreToolUse)
	require.NoError(t, err)
	assert.Equal(t, runtime.SurfaceCodexStrictCommandJSON, pre.Surface())
	assert.Equal(t, runtime.MutationInput, pre.Mutation())
	assert.Equal(t, runtime.OrderConcurrentNative, pre.Order())
	assert.Equal(t, runtime.ReconcileNoAdapterMerge, pre.Reconciliation())
	// No Codex row cites host evidence yet, so the strict blocking exit is not
	// claimed and the row runs as report-and-continue.
	assert.Equal(t, runtime.FailureReportAndContinue, pre.Failure())
	assert.False(t, pre.Evidence().IsPresent())

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
	contract := runtime.OpenCode1_18_10Lifecycle()

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

// TestFailureModeIsTheOnlyFailureVocabulary pins the single failure-mode enum
// of the tree. Before this, three FailureMode types existed (this one, one in
// internal/lifecycle/registration and one in the ingress host contract), and
// the two small ones folded six native behaviors into two. A fold is silent: a
// generated adapter cannot tell an OpenCode plugin throw from a Claude exit-2
// block once both are labelled the same. The zero value must stay invalid so an
// unset field can never read as a real native behavior.
func TestFailureModeIsTheOnlyFailureVocabulary(t *testing.T) {
	t.Parallel()

	var unset runtime.FailureMode
	assert.False(t, unset.IsValid(), "the zero FailureMode must never be a valid native behavior")
	assert.Equal(t, "", unset.String(), "the zero FailureMode must not name a native behavior")
	assert.False(t, runtime.FailureMode(uint8(runtime.FailureObserveOnly)+1).IsValid(),
		"a value above the last declared arm must never be valid")

	arms := []runtime.FailureMode{
		runtime.FailureReportAndContinue,
		runtime.FailureExitTwoBlocks,
		runtime.FailureStrictHook,
		runtime.FailureStrictExitTwoBlocks,
		runtime.FailureThrowFailFast,
		runtime.FailureObserveOnly,
	}
	seen := make(map[string]runtime.FailureMode, len(arms))
	for _, arm := range arms {
		assert.True(t, arm.IsValid(), "declared arm %d must be valid", uint8(arm))
		name := arm.String()
		require.NotEmpty(t, name, "declared arm %d must have a name", uint8(arm))
		previous, duplicate := seen[name]
		require.False(t, duplicate,
			"arms %d and %d share the name %q, so a generated manifest could not tell them apart",
			uint8(previous), uint8(arm), name)
		seen[name] = arm
	}
	assert.Len(t, seen, 6, "the failure vocabulary has exactly six arms")
}

// TestNoSecondFailureVocabularyIsDeclaredAnywhere is the OTHER half of the
// single-vocabulary claim, and it is the half a value cannot show.
//
// TestFailureModeIsTheOnlyFailureVocabulary proves the arms of THIS enum are
// distinct and that its zero value is refused. It cannot prove that no SECOND
// enum exists, because a second type in another package changes nothing this
// package can observe. That is exactly how the tree acquired three of them: two
// small enums folded six native behaviours into two, and a generated adapter
// could no longer tell an OpenCode plugin throw from a Claude exit-2 block.
//
// So the absence is asserted over the SOURCE. A new declaration turns this RED
// on the day it is written.
func TestNoSecondFailureVocabularyIsDeclaredAnywhere(t *testing.T) {
	t.Parallel()

	root, err := scan.ModuleRoot()
	require.NoError(t, err)

	owner := filepath.Join(root, "internal", "runtime")
	self := filepath.Join(owner, "lifecycle_test.go")
	declaration := regexp.MustCompile(`(?m)^\s*type\s+\w*FailureMode\s`)

	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "legacy" || path == owner {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || path == self {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if declaration.MatchString(string(body)) {
			relative, _ := filepath.Rel(root, path)
			t.Errorf("%s declares a second failure-mode vocabulary; "+
				"one vocabulary is what lets a generated manifest tell an OpenCode plugin throw "+
				"from a Claude exit-2 block, and a second one folds them back together silently",
				relative)
		}
		return nil
	}))
}

// TestEveryRegisteredRowIsDeclaredAndAnUnknownCoordinateIsNot pins the fact
// LifecycleFailurePolicy.Declared rests on: every policy a lookup returns
// carries a valid Semantic, and a coordinate the build does not declare is not
// found at all. The hook command's fallback for such a coordinate builds a
// policy with the zero Semantic, so Declared is what separates "treated as
// observe-only" from "declares observe-only" in its diagnostic and its record.
//
// WHAT IT VISITS: every event of the three pinned lifecycle contracts, looked
// up by its native name exactly as the hook command looks it up. The three are
// written here by hand, and they are the three cases of the switch in
// LookupLifecycleFailure; a fourth case added there must be added here.
// WHAT IT DOES NOT READ: a contract this build does not pin, and a harness
// that switch does not dispatch on.
//
// MUTATION: make Declared return true, or let lookupLifecycleFailure leave
// Semantic unset. This test turns RED.
func TestEveryRegisteredRowIsDeclaredAndAnUnknownCoordinateIsNot(t *testing.T) {
	t.Parallel()

	rows := 0
	rows += assertEveryRowDeclared(t, ir.HarnessClaudeCode, runtime.ClaudeCode2_1_210Lifecycle(), runtime.ClaudeLifecycleEvents())
	rows += assertEveryRowDeclared(t, ir.HarnessCodex, runtime.Codex0_146_0Lifecycle(), runtime.CodexLifecycleEvents())
	rows += assertEveryRowDeclared(t, ir.HarnessOpenCode, runtime.OpenCode1_18_10Lifecycle(), runtime.OpenCodeLifecycleEvents())
	require.NotZero(t, rows, "no contract yielded a row, so nothing above was asserted")

	for _, coordinate := range []struct {
		harness ir.HarnessID
		event   string
	}{
		{harness: ir.HarnessID("gemini"), event: "NotAnEvent"},
		{harness: ir.HarnessClaudeCode, event: "NotAnEvent"},
	} {
		policy, found := runtime.LookupLifecycleFailure(coordinate.harness, coordinate.event)
		assert.False(t, found, "%s %s is not declared by this build and must not be found", coordinate.harness, coordinate.event)
		assert.False(t, policy.Declared(),
			"the policy returned for an undeclared coordinate must read as undeclared, or the hook "+
				"command's fallback would be reported as a declaration")
	}
}

// assertEveryRowDeclared looks up every event of one contract by its native
// name and requires the returned policy to read as declared. It returns the
// number of rows it checked.
func assertEveryRowDeclared[E comparable](
	t *testing.T,
	harness ir.HarnessID,
	contract runtime.LifecycleContract[E],
	events []E,
) int {
	t.Helper()
	rows := 0
	for _, event := range events {
		mapping, err := contract.Mapping(event)
		if err != nil {
			continue
		}
		policy, found := runtime.LookupLifecycleFailure(harness, mapping.NativeName())
		require.True(t, found, "%s %s is a registered row and must be found", harness, mapping.NativeName())
		assert.True(t, policy.Declared(),
			"%s %s is a registered row and its policy must read as declared; a declared row with an "+
				"invalid Semantic would be reported to the operator as undeclared", harness, mapping.NativeName())
		rows++
	}
	return rows
}
