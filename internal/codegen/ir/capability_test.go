package ir_test

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capabilityFixtureInput struct {
	Name string `json:"name"`
}

type capabilityFixtureResult struct {
	Rendered bool `json:"rendered"`
}

func mustCapabilityCodec[T any](t testing.TB, schema string) ir.Codec[T] {
	t.Helper()
	codec, err := ir.NewJSONCodec[T](ir.SchemaID(schema), nil)
	require.NoError(t, err)
	return codec
}

func mustCapabilityEffects(t testing.TB, ids ...string) ir.EffectSet {
	t.Helper()
	effectIDs := make([]ir.EffectID, len(ids))
	for i, value := range ids {
		effectIDs[i] = mustEffectID(t, value)
	}
	effects, err := ir.NewEffectSet(effectIDs...)
	require.NoError(t, err)
	return effects
}

func mustCapabilitySemantics() ir.CapabilitySemantics {
	return ir.CapabilitySemantics{
		Summary:        "render a diagram from a validated typed input",
		Preconditions:  []string{"input is well-formed"},
		Postconditions: []string{"a rendered diagram artifact exists"},
		Result:         "the rendered diagram result",
	}
}

func assertActionableDiagnostic(t testing.TB, err error) {
	t.Helper()
	require.Error(t, err)
	for _, field := range []string{"what:", "why:", "where:", "phase:", "impact:", "fix:"} {
		assert.Contains(t, err.Error(), field)
	}
}

// TestDefineCapability_SuccessCarriesFullContract proves DefineCapability's
// happy path: every accessor returns exactly what was supplied, Semantics()
// and the underlying codecs are independently usable, and the returned
// descriptor reports itself valid.
func TestDefineCapability_SuccessCarriesFullContract(t *testing.T) {
	t.Parallel()

	id := ir.CapabilityID("acme.diagram.render/v1")
	version := ir.CapabilityContractVersion("1.0.0")
	semantics := mustCapabilitySemantics()
	effects := mustCapabilityEffects(t, "pasture.effect.write/v1")
	inputCodec := mustCapabilityCodec[capabilityFixtureInput](t, "acme.diagram.render-input/v1")
	outputCodec := mustCapabilityCodec[capabilityFixtureResult](t, "acme.diagram.render-output/v1")

	capability, err := ir.DefineCapability(id, version, semantics, effects, inputCodec, outputCodec)
	require.NoError(t, err)

	assert.True(t, capability.IsValid())
	assert.Equal(t, id, capability.ID())
	assert.Equal(t, version, capability.Version())
	assert.Equal(t, semantics.Summary, capability.Semantics().Summary)
	assert.Equal(t, semantics.Result, capability.Semantics().Result)
	assert.True(t, capability.Effects().Equal(effects))
	assert.Equal(t, ir.SchemaID("acme.diagram.render-input/v1"), capability.InputCodec().Schema())
	assert.Equal(t, ir.SchemaID("acme.diagram.render-output/v1"), capability.OutputCodec().Schema())

	// Semantics() must be a defensive copy: mutating the returned slice must
	// not corrupt the descriptor's own retained semantics.
	returned := capability.Semantics()
	returned.Preconditions[0] = "mutated"
	assert.Equal(t, "input is well-formed", capability.Semantics().Preconditions[0])
}

// TestDefineCapability_ZeroValueIsInvalid proves a Capability zero value
// (the only shape reachable without going through DefineCapability, since
// its fields are unexported) reports itself invalid.
func TestDefineCapability_ZeroValueIsInvalid(t *testing.T) {
	t.Parallel()
	var zero ir.Capability[capabilityFixtureInput, capabilityFixtureResult]
	assert.False(t, zero.IsValid())
}

// TestDefineCapability_RejectsInvalidContracts is the table-driven negative
// suite for the acceptance criterion "Invalid/duplicate IDs, invalid
// codecs/semantics/effects, and changed same-version contracts fail
// actionably." Duplicate/changed-contract cases have their own dedicated
// tests below because they require two sequential DefineCapability calls
// sharing process-global registry state.
func TestDefineCapability_RejectsInvalidContracts(t *testing.T) {
	t.Parallel()

	validEffects := mustCapabilityEffects(t, "pasture.effect.read/v1")
	validInput := mustCapabilityCodec[capabilityFixtureInput](t, "pasture.test.capability-invalid-input/v1")
	validOutput := mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.capability-invalid-output/v1")
	validSemantics := mustCapabilitySemantics()

	cases := []struct {
		name      string
		id        ir.CapabilityID
		version   ir.CapabilityContractVersion
		semantics ir.CapabilitySemantics
		effects   ir.EffectSet
		input     ir.Codec[capabilityFixtureInput]
		output    ir.Codec[capabilityFixtureResult]
	}{
		{
			name: "empty id", id: ir.CapabilityID(""), version: "1.0.0",
			semantics: validSemantics, effects: validEffects, input: validInput, output: validOutput,
		},
		{
			name: "non-namespaced id", id: ir.CapabilityID("render"), version: "1.0.0",
			semantics: validSemantics, effects: validEffects, input: validInput, output: validOutput,
		},
		{
			name: "control character id", id: ir.CapabilityID("acme.diagram.\x00render/v1"), version: "1.0.0",
			semantics: validSemantics, effects: validEffects, input: validInput, output: validOutput,
		},
		{
			name: "empty version", id: ir.CapabilityID("acme.diagram.bad-version-1/v1"), version: "",
			semantics: validSemantics, effects: validEffects, input: validInput, output: validOutput,
		},
		{
			name: "non-semver version", id: ir.CapabilityID("acme.diagram.bad-version-2/v1"), version: "v1",
			semantics: validSemantics, effects: validEffects, input: validInput, output: validOutput,
		},
		{
			name: "two-part version", id: ir.CapabilityID("acme.diagram.bad-version-3/v1"), version: "1.0",
			semantics: validSemantics, effects: validEffects, input: validInput, output: validOutput,
		},
		{
			name: "leading zero version", id: ir.CapabilityID("acme.diagram.bad-version-4/v1"), version: "1.00.0",
			semantics: validSemantics, effects: validEffects, input: validInput, output: validOutput,
		},
		{
			name: "nil input codec", id: ir.CapabilityID("acme.diagram.nil-input/v1"), version: "1.0.0",
			semantics: validSemantics, effects: validEffects, input: nil, output: validOutput,
		},
		{
			name: "nil output codec", id: ir.CapabilityID("acme.diagram.nil-output/v1"), version: "1.0.0",
			semantics: validSemantics, effects: validEffects, input: validInput, output: nil,
		},
		{
			name: "missing summary", id: ir.CapabilityID("acme.diagram.no-summary/v1"), version: "1.0.0",
			semantics: ir.CapabilitySemantics{Result: "result"}, effects: validEffects, input: validInput, output: validOutput,
		},
		{
			name: "missing result", id: ir.CapabilityID("acme.diagram.no-result/v1"), version: "1.0.0",
			semantics: ir.CapabilitySemantics{Summary: "summary"}, effects: validEffects, input: validInput, output: validOutput,
		},
		{
			name: "zero-value effect set", id: ir.CapabilityID("acme.diagram.zero-effects/v1"), version: "1.0.0",
			semantics: validSemantics, effects: ir.EffectSet{}, input: validInput, output: validOutput,
		},
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := ir.DefineCapability(testCase.id, testCase.version, testCase.semantics, testCase.effects, testCase.input, testCase.output)
			assertActionableDiagnostic(t, err)
		})
	}
}

// TestDefineCapability_DuplicateSameContractIsRejected proves that even a
// byte-identical second registration of the same (id, version) pair fails —
// there is exactly one canonical declaration site.
func TestDefineCapability_DuplicateSameContractIsRejected(t *testing.T) {
	t.Parallel()

	id := ir.CapabilityID("acme.diagram.duplicate-same/v1")
	version := ir.CapabilityContractVersion("1.0.0")
	semantics := mustCapabilitySemantics()
	effects := mustCapabilityEffects(t, "pasture.effect.write/v1")
	input := mustCapabilityCodec[capabilityFixtureInput](t, "pasture.test.duplicate-same-input/v1")
	output := mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.duplicate-same-output/v1")

	_, err := ir.DefineCapability(id, version, semantics, effects, input, output)
	require.NoError(t, err)

	_, err = ir.DefineCapability(id, version, semantics, effects, input, output)
	assertActionableDiagnostic(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

// alternateCapabilityFixtureInput is a Go type distinct from
// capabilityFixtureInput, used only to prove a same-(id, version)
// registration whose generic In type parameter differs is a rejected
// "changed contract", not merely one whose codec schema string differs.
type alternateCapabilityFixtureInput struct {
	Different string `json:"different"`
}

// TestDefineCapability_ChangedSameVersionContractIsRejected proves the
// "changed same-version contracts fail actionably" acceptance criterion
// across every dimension capabilityContract.equal() compares — not just
// effects — each in its own subtest with a distinct capability identity, so
// a future regression in any single comparison (e.g. a refactor of the &&
// chain that accidentally drops one field) is caught by its own failing
// subtest rather than being masked by only ever exercising one dimension.
func TestDefineCapability_ChangedSameVersionContractIsRejected(t *testing.T) {
	t.Parallel()

	baseSemantics := mustCapabilitySemantics()
	baseEffects := mustCapabilityEffects(t, "pasture.effect.write/v1")
	baseVersion := ir.CapabilityContractVersion("1.0.0")

	t.Run("effects differ", func(t *testing.T) {
		t.Parallel()
		id := ir.CapabilityID("acme.diagram.changed-contract-effects/v1")
		input := mustCapabilityCodec[capabilityFixtureInput](t, "pasture.test.changed-contract-effects-input/v1")
		output := mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.changed-contract-effects-output/v1")

		_, err := ir.DefineCapability(id, baseVersion, baseSemantics, mustCapabilityEffects(t, "pasture.effect.write/v1"), input, output)
		require.NoError(t, err)
		_, err = ir.DefineCapability(id, baseVersion, baseSemantics, mustCapabilityEffects(t, "pasture.effect.read/v1"), input, output)
		assertActionableDiagnostic(t, err)
		assert.Contains(t, err.Error(), "different contract")
	})

	t.Run("input type differs", func(t *testing.T) {
		t.Parallel()
		id := ir.CapabilityID("acme.diagram.changed-contract-in-type/v1")
		output := mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.changed-contract-in-type-output/v1")

		_, err := ir.DefineCapability(id, baseVersion, baseSemantics, baseEffects,
			mustCapabilityCodec[capabilityFixtureInput](t, "pasture.test.changed-contract-in-type-input/v1"), output)
		require.NoError(t, err)
		_, err = ir.DefineCapability(id, baseVersion, baseSemantics, baseEffects,
			mustCapabilityCodec[alternateCapabilityFixtureInput](t, "pasture.test.changed-contract-in-type-input-alt/v1"), output)
		assertActionableDiagnostic(t, err)
		assert.Contains(t, err.Error(), "different contract")
	})

	t.Run("input schema differs, same type", func(t *testing.T) {
		t.Parallel()
		id := ir.CapabilityID("acme.diagram.changed-contract-in-schema/v1")
		output := mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.changed-contract-in-schema-output/v1")

		_, err := ir.DefineCapability(id, baseVersion, baseSemantics, baseEffects,
			mustCapabilityCodec[capabilityFixtureInput](t, "pasture.test.changed-contract-in-schema-input-a/v1"), output)
		require.NoError(t, err)
		_, err = ir.DefineCapability(id, baseVersion, baseSemantics, baseEffects,
			mustCapabilityCodec[capabilityFixtureInput](t, "pasture.test.changed-contract-in-schema-input-b/v1"), output)
		assertActionableDiagnostic(t, err)
		assert.Contains(t, err.Error(), "different contract")
	})

	t.Run("output schema differs, same type", func(t *testing.T) {
		t.Parallel()
		id := ir.CapabilityID("acme.diagram.changed-contract-out-schema/v1")
		input := mustCapabilityCodec[capabilityFixtureInput](t, "pasture.test.changed-contract-out-schema-input/v1")

		_, err := ir.DefineCapability(id, baseVersion, baseSemantics, baseEffects, input,
			mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.changed-contract-out-schema-output-a/v1"))
		require.NoError(t, err)
		_, err = ir.DefineCapability(id, baseVersion, baseSemantics, baseEffects, input,
			mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.changed-contract-out-schema-output-b/v1"))
		assertActionableDiagnostic(t, err)
		assert.Contains(t, err.Error(), "different contract")
	})

	semanticsCases := []struct {
		name  string
		alter func(ir.CapabilitySemantics) ir.CapabilitySemantics
	}{
		{name: "semantics summary differs", alter: func(s ir.CapabilitySemantics) ir.CapabilitySemantics {
			s.Summary = "a completely different summary"
			return s
		}},
		{name: "semantics result differs", alter: func(s ir.CapabilitySemantics) ir.CapabilitySemantics {
			s.Result = "a completely different result contract"
			return s
		}},
		{name: "semantics preconditions differ", alter: func(s ir.CapabilitySemantics) ir.CapabilitySemantics {
			s.Preconditions = []string{"a completely different precondition"}
			return s
		}},
		{name: "semantics postconditions differ", alter: func(s ir.CapabilitySemantics) ir.CapabilitySemantics {
			s.Postconditions = []string{"a completely different postcondition"}
			return s
		}},
	}
	for index, semanticsCase := range semanticsCases {
		semanticsCase := semanticsCase
		t.Run(semanticsCase.name, func(t *testing.T) {
			t.Parallel()
			id := ir.CapabilityID(fmt.Sprintf("acme.diagram.changed-contract-semantics-%d/v1", index))
			input := mustCapabilityCodec[capabilityFixtureInput](t, fmt.Sprintf("pasture.test.changed-contract-semantics-%d-input/v1", index))
			output := mustCapabilityCodec[capabilityFixtureResult](t, fmt.Sprintf("pasture.test.changed-contract-semantics-%d-output/v1", index))

			_, err := ir.DefineCapability(id, baseVersion, baseSemantics, baseEffects, input, output)
			require.NoError(t, err)
			_, err = ir.DefineCapability(id, baseVersion, semanticsCase.alter(baseSemantics), baseEffects, input, output)
			assertActionableDiagnostic(t, err)
			assert.Contains(t, err.Error(), "different contract")
		})
	}
}

// TestDefineCapability_DifferentVersionOfSameIDIsAllowed proves capability
// versioning itself is not a conflict: two different versions of the same
// CapabilityID may register different contracts (here, different Go types
// entirely), exactly as #40's version-bounded runtime contracts require.
func TestDefineCapability_DifferentVersionOfSameIDIsAllowed(t *testing.T) {
	t.Parallel()

	id := ir.CapabilityID("acme.diagram.versioned/v1")
	semantics := mustCapabilitySemantics()
	effects := mustCapabilityEffects(t, "pasture.effect.write/v1")

	type inputV2 struct {
		Name  string `json:"name"`
		Scale int    `json:"scale"`
	}

	v1, err := ir.DefineCapability(
		id, ir.CapabilityContractVersion("1.0.0"), semantics, effects,
		mustCapabilityCodec[capabilityFixtureInput](t, "pasture.test.versioned-input-v1/v1"),
		mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.versioned-output-v1/v1"),
	)
	require.NoError(t, err)

	v2, err := ir.DefineCapability(
		id, ir.CapabilityContractVersion("2.0.0"), semantics, effects,
		mustCapabilityCodec[inputV2](t, "pasture.test.versioned-input-v2/v1"),
		mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.versioned-output-v2/v1"),
	)
	require.NoError(t, err)

	assert.Equal(t, id, v1.ID())
	assert.Equal(t, id, v2.ID())
	assert.Equal(t, ir.CapabilityContractVersion("1.0.0"), v1.Version())
	assert.Equal(t, ir.CapabilityContractVersion("2.0.0"), v2.Version())
}

// TestDefineCapability_ConcurrentDistinctIDsAllSucceed exercises the
// registry's mutex under the race detector: many goroutines defining
// distinct capability identities concurrently must all succeed.
func TestDefineCapability_ConcurrentDistinctIDsAllSucceed(t *testing.T) {
	t.Parallel()

	const n = 32

	// Every fixture value is built up front, on the test goroutine: testify's
	// require/t.Helper() calls (inside mustCapabilityCodec/mustCapabilityEffects)
	// are only supported from the goroutine running the test function, so
	// none of that construction may happen inside the spawned goroutines
	// below — only the DefineCapability call under test does.
	type fixture struct {
		id      ir.CapabilityID
		effects ir.EffectSet
		input   ir.Codec[capabilityFixtureInput]
		output  ir.Codec[capabilityFixtureResult]
	}
	semantics := mustCapabilitySemantics()
	fixtures := make([]fixture, n)
	for i := range fixtures {
		fixtures[i] = fixture{
			id:      ir.CapabilityID(fmt.Sprintf("pasture.test.concurrent-distinct-%d/v1", i)),
			effects: mustCapabilityEffects(t, "pasture.effect.write/v1"),
			input:   mustCapabilityCodec[capabilityFixtureInput](t, fmt.Sprintf("pasture.test.concurrent-distinct-in-%d/v1", i)),
			output:  mustCapabilityCodec[capabilityFixtureResult](t, fmt.Sprintf("pasture.test.concurrent-distinct-out-%d/v1", i)),
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			f := fixtures[index]
			_, err := ir.DefineCapability(f.id, ir.CapabilityContractVersion("1.0.0"), semantics, f.effects, f.input, f.output)
			errs[index] = err
		}(i)
	}
	wg.Wait()

	for index, err := range errs {
		assert.NoError(t, err, "index %d", index)
	}
}

// TestDefineCapability_ConcurrentSameIDExactlyOneWins proves the registry
// serializes conflicting registrations of the same (id, version) pair
// correctly under concurrency: exactly one of many racing callers succeeds.
func TestDefineCapability_ConcurrentSameIDExactlyOneWins(t *testing.T) {
	t.Parallel()

	const n = 16
	id := ir.CapabilityID("pasture.test.concurrent-same-id/v1")
	version := ir.CapabilityContractVersion("1.0.0")
	semantics := mustCapabilitySemantics()
	effects := mustCapabilityEffects(t, "pasture.effect.write/v1")
	input := mustCapabilityCodec[capabilityFixtureInput](t, "pasture.test.concurrent-same-id-input/v1")
	output := mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.concurrent-same-id-output/v1")

	var wg sync.WaitGroup
	var successes int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := ir.DefineCapability(id, version, semantics, effects, input, output); err == nil {
				atomic.AddInt32(&successes, 1)
			}
		}()
	}
	wg.Wait()

	assert.EqualValues(t, 1, successes, "exactly one concurrent registration of the same capability identity and version must win")
}

// TestMustDefineCapability_ReturnsOnSuccess proves the happy path of the
// panicking static-declaration form.
func TestMustDefineCapability_ReturnsOnSuccess(t *testing.T) {
	t.Parallel()

	capability := ir.MustDefineCapability[capabilityFixtureInput, capabilityFixtureResult](
		ir.CapabilityID("acme.diagram.must-success/v1"),
		ir.CapabilityContractVersion("1.0.0"),
		mustCapabilitySemantics(),
		mustCapabilityEffects(t, "pasture.effect.write/v1"),
		mustCapabilityCodec[capabilityFixtureInput](t, "pasture.test.must-success-input/v1"),
		mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.must-success-output/v1"),
	)
	assert.True(t, capability.IsValid())
}

// TestMustDefineCapability_PanicsWithDefineCapabilitysOwnDiagnostic proves
// MustDefineCapability panics with exactly the actionable error
// DefineCapability itself returns, not a generic wrapped message.
func TestMustDefineCapability_PanicsWithDefineCapabilitysOwnDiagnostic(t *testing.T) {
	t.Parallel()

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		ir.MustDefineCapability[capabilityFixtureInput, capabilityFixtureResult](
			ir.CapabilityID("not-namespaced"),
			ir.CapabilityContractVersion("1.0.0"),
			mustCapabilitySemantics(),
			mustCapabilityEffects(t, "pasture.effect.write/v1"),
			mustCapabilityCodec[capabilityFixtureInput](t, "pasture.test.must-panic-input/v1"),
			mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.must-panic-output/v1"),
		)
	}()

	require.NotNil(t, recovered, "MustDefineCapability must panic on an invalid static declaration")
	panicErr, ok := recovered.(error)
	require.True(t, ok, "panic value must be the error DefineCapability returned, got %T", recovered)
	assertActionableDiagnostic(t, panicErr)
	assert.Contains(t, panicErr.Error(), "not-namespaced")

	directErr, defineErr := ir.DefineCapability(
		ir.CapabilityID("not-namespaced"), ir.CapabilityContractVersion("1.0.0"), mustCapabilitySemantics(),
		mustCapabilityEffects(t, "pasture.effect.write/v1"),
		mustCapabilityCodec[capabilityFixtureInput](t, "pasture.test.must-panic-direct-input/v1"),
		mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.must-panic-direct-output/v1"),
	)
	assert.False(t, directErr.IsValid())
	require.Error(t, defineErr)
	assert.Equal(t, defineErr.Error(), panicErr.Error(), "MustDefineCapability must panic with exactly DefineCapability's own diagnostic")
}

func mustInvokeToolCapability(t testing.TB, id string) ir.Capability[capabilityFixtureInput, capabilityFixtureResult] {
	t.Helper()
	capability, err := ir.DefineCapability(
		ir.CapabilityID(id), ir.CapabilityContractVersion("1.0.0"), mustCapabilitySemantics(),
		mustCapabilityEffects(t, "pasture.effect.write/v1"),
		mustCapabilityCodec[capabilityFixtureInput](t, id+"-input/v1"),
		mustCapabilityCodec[capabilityFixtureResult](t, id+"-output/v1"),
	)
	require.NoError(t, err)
	return capability
}

// TestInvokeTool_ProducesValidatedSemanticOperation proves the InvokeTool
// happy path end to end: the resulting SemanticOperation reports the
// escape-hatch kind and the capability's own fixed protocol identity, enters
// a Document successfully, and canonicalizes to exactly capability identity,
// contract version, and encoded input — never a raw native tool name.
func TestInvokeTool_ProducesValidatedSemanticOperation(t *testing.T) {
	t.Parallel()

	capability := mustInvokeToolCapability(t, "acme.diagram.invoke-success/v1")
	operation := ir.InvokeTool(capability, capabilityFixtureInput{Name: "roadmap"})

	kind, err := ir.SemanticOperationKind(operation)
	require.NoError(t, err)
	assert.Equal(t, ir.OperationInvokeTool, kind)

	identity, err := ir.SemanticOperationIdentity(operation)
	require.NoError(t, err)
	assert.Equal(t, "pasture.orchestration.invoke-tool/v1", identity.String())

	part, err := ir.Operation(operation, mustLocation(t, "invoke-tool", 0))
	require.NoError(t, err)
	require.NotNil(t, part)

	canonical, err := ir.CanonicalSemanticOperation(operation)
	require.NoError(t, err)

	var wire struct {
		Kind    string `json:"kind"`
		ID      string `json:"id"`
		Payload struct {
			Capability string          `json:"capability"`
			Version    string          `json:"version"`
			Input      json.RawMessage `json:"input"`
		} `json:"payload"`
		Results json.RawMessage `json:"results"`
	}
	require.NoError(t, json.Unmarshal(canonical, &wire))
	assert.Equal(t, "invoke_tool", wire.Kind)
	assert.Equal(t, "pasture.orchestration.invoke-tool/v1", wire.ID)
	assert.Equal(t, "acme.diagram.invoke-success/v1", wire.Payload.Capability)
	assert.Equal(t, "1.0.0", wire.Payload.Version)
	assert.JSONEq(t, `{"name":"roadmap"}`, string(wire.Payload.Input))

	// Every core operation's wire form emits "results":[] (never null), even
	// with zero declared result slots — invoke_tool's canonical shape must
	// stay uniform, not null only for this one variant.
	assert.Equal(t, "[]", string(wire.Results))

	// The payload must carry exactly these three keys — never a fourth,
	// native-harness-shaped field.
	var payload map[string]json.RawMessage
	rawPayload, err := json.Marshal(wire.Payload)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(rawPayload, &payload))
	assert.ElementsMatch(t, []string{"capability", "version", "input"}, mapKeys(payload))

	typedOperation, ok := operation.(ir.InvokeToolOperation)
	require.True(t, ok)
	assert.Equal(t, ir.CapabilityID("acme.diagram.invoke-success/v1"), typedOperation.Capability())
	assert.Equal(t, ir.CapabilityContractVersion("1.0.0"), typedOperation.Version())
	assert.Equal(t, capability.InputCodec().Schema(), typedOperation.InputSchema())
	assert.Equal(t, capability.OutputCodec().Schema(), typedOperation.OutputSchema())
	assert.JSONEq(t, `{"name":"roadmap"}`, string(typedOperation.EncodedInput()))
}

func mapKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

// TestInvokeTool_ZeroValueCapabilityDefersActionableError proves InvokeTool's
// no-error signature does not silently drop an invalid capability: the
// failure is retained and surfaced the moment the operation is validated.
func TestInvokeTool_ZeroValueCapabilityDefersActionableError(t *testing.T) {
	t.Parallel()

	var zero ir.Capability[capabilityFixtureInput, capabilityFixtureResult]
	operation := ir.InvokeTool(zero, capabilityFixtureInput{Name: "roadmap"})
	require.NotNil(t, operation)

	_, err := ir.SemanticOperationKind(operation)
	assertActionableDiagnostic(t, err)

	_, err = ir.Operation(operation, mustLocation(t, "invoke-tool-invalid", 0))
	assertActionableDiagnostic(t, err)
}

// TestInvokeTool_EncodeFailureDefersActionableError proves an input the
// capability's own codec validator rejects is retained as a deferred error
// rather than silently entering the operation.
func TestInvokeTool_EncodeFailureDefersActionableError(t *testing.T) {
	t.Parallel()

	strictInput, err := ir.NewJSONCodec[capabilityFixtureInput](ir.SchemaID("pasture.test.invoke-strict-input/v1"), func(value capabilityFixtureInput) error {
		if value.Name == "" {
			return assert.AnError
		}
		return nil
	})
	require.NoError(t, err)
	capability, err := ir.DefineCapability(
		ir.CapabilityID("acme.diagram.invoke-strict/v1"), ir.CapabilityContractVersion("1.0.0"), mustCapabilitySemantics(),
		mustCapabilityEffects(t, "pasture.effect.write/v1"), strictInput,
		mustCapabilityCodec[capabilityFixtureResult](t, "pasture.test.invoke-strict-output/v1"),
	)
	require.NoError(t, err)

	operation := ir.InvokeTool(capability, capabilityFixtureInput{})
	_, err = ir.CanonicalSemanticOperation(operation)
	assertActionableDiagnostic(t, err)
}

// TestInvokeTool_TypedNilPointerIsRejected proves InvokeToolOperation joins
// every other SemanticOperation variant's typed-nil-pointer protection: a
// *ir.InvokeToolOperation(nil) is a non-nil SemanticOperation interface
// value whose accessors must return the closed-sum diagnostic, never panic.
func TestInvokeTool_TypedNilPointerIsRejected(t *testing.T) {
	t.Parallel()

	var typedNil ir.SemanticOperation = (*ir.InvokeToolOperation)(nil)

	require.NotPanics(t, func() {
		_, err := ir.SemanticOperationKind(typedNil)
		assert.Error(t, err)
	})
	require.NotPanics(t, func() {
		_, err := ir.SemanticOperationIdentity(typedNil)
		assert.Error(t, err)
	})
	require.NotPanics(t, func() {
		_, err := ir.CanonicalSemanticOperation(typedNil)
		assert.Error(t, err)
	})
	require.NotPanics(t, func() {
		_, err := ir.Operation(typedNil, mustLocation(t, "invoke-tool-typed-nil", 0))
		assert.Error(t, err)
	})
}

// TestCapabilityIDAndVersion_IsValid is a focused table over the two new ID
// domains' IsValid accessors, independent of DefineCapability.
func TestCapabilityIDAndVersion_IsValid(t *testing.T) {
	t.Parallel()

	assert.True(t, ir.CapabilityID("acme.diagram.render/v1").IsValid())
	assert.False(t, ir.CapabilityID("").IsValid())
	assert.False(t, ir.CapabilityID("not-namespaced").IsValid())
	assert.False(t, ir.CapabilityID(" acme.diagram.render/v1").IsValid())

	assert.True(t, ir.CapabilityContractVersion("1.0.0").IsValid())
	assert.True(t, ir.CapabilityContractVersion("0.0.1").IsValid())
	assert.False(t, ir.CapabilityContractVersion("").IsValid())
	assert.False(t, ir.CapabilityContractVersion("1.0").IsValid())
	assert.False(t, ir.CapabilityContractVersion("1.0.0.0").IsValid())
	assert.False(t, ir.CapabilityContractVersion("01.0.0").IsValid())
	assert.False(t, ir.CapabilityContractVersion("1.0.0-beta").IsValid())
}
