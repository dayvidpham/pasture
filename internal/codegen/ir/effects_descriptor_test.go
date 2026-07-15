package ir_test

import (
	"encoding/json"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEffectSetCanonicalCodecEqualityAndCompatibility(t *testing.T) {
	t.Parallel()

	read := mustEffectID(t, "pasture.effect.read/v1")
	write := mustEffectID(t, "pasture.effect.write/v1")
	set, err := ir.NewEffectSet(write, read, write)
	require.NoError(t, err)
	required, err := ir.NewEffectSet(read)
	require.NoError(t, err)
	equal, err := ir.NewEffectSet(read, write)
	require.NoError(t, err)
	empty, err := ir.NewEffectSet()
	require.NoError(t, err)

	assert.Equal(t, []ir.EffectID{read, write}, set.IDs())
	assert.True(t, set.Equal(equal))
	assert.False(t, set.CompatibleWith(required), "additional effects change descriptor semantics")
	assert.True(t, set.CompatibleWith(equal))
	assert.False(t, required.CompatibleWith(set))
	assert.False(t, set.Compatible(empty))
	assert.True(t, set.ContainsAll(required))
	assert.True(t, set.ContainsAll(empty))
	assert.False(t, (ir.EffectSet{}).IsValid())

	encoded, err := json.Marshal(set)
	require.NoError(t, err)
	assert.Equal(t, `["pasture.effect.read/v1","pasture.effect.write/v1"]`, string(encoded))
	var decoded ir.EffectSet
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.True(t, set.Equal(decoded))

	for _, invalid := range []string{
		`["pasture.effect.write/v1","pasture.effect.read/v1"]`,
		`["pasture.effect.read/v1","pasture.effect.read/v1"]`,
		`["not-namespaced"]`,
		`null`,
	} {
		assert.Error(t, json.Unmarshal([]byte(invalid), &decoded), invalid)
	}

	ids := set.IDs()
	ids[0] = write
	assert.Equal(t, read, set.IDs()[0], "EffectSet IDs must be defensively copied")
}

func TestOpaqueDescriptorsCarryTypedCodecsSemanticsAndEffects(t *testing.T) {
	t.Parallel()

	type input struct {
		Name string `json:"name"`
	}
	type output struct {
		Created bool `json:"created"`
	}
	inputCodec, err := ir.NewJSONCodec[input]("pasture.test.input/v1", func(value input) error {
		if value.Name == "" {
			return assert.AnError
		}
		return nil
	})
	require.NoError(t, err)
	outputCodec, err := ir.NewJSONCodec[output]("pasture.test.output/v1", nil)
	require.NoError(t, err)
	effects, err := ir.NewEffectSet(mustEffectID(t, "pasture.effect.write/v1"))
	require.NoError(t, err)
	operationID, err := ir.NewSemanticOperationID("pasture.test.create/v1")
	require.NoError(t, err)
	semantics := ir.DescriptorSemantics{
		Summary: "create a portable value", Preconditions: []string{"name is present"},
		Postconditions: []string{"value exists"}, Result: "created result",
	}
	descriptor, err := ir.NewOperationDescriptor(operationID, inputCodec, outputCodec, semantics, effects)
	require.NoError(t, err)
	assert.True(t, descriptor.IsValid())
	assert.Equal(t, operationID, descriptor.ID())
	assert.True(t, descriptor.Effects().Equal(effects))
	assert.Equal(t, ir.SchemaID("pasture.test.input/v1"), descriptor.InputCodec().Schema())

	semantics.Preconditions[0] = "mutated"
	assert.Equal(t, "name is present", descriptor.Semantics().Preconditions[0])
	copySemantics := descriptor.Semantics()
	copySemantics.Postconditions[0] = "mutated"
	assert.Equal(t, "value exists", descriptor.Semantics().Postconditions[0])

	encoded, err := descriptor.InputCodec().Encode(input{Name: "item"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"item"}`, string(encoded))
	decoded, err := descriptor.InputCodec().Decode(encoded)
	require.NoError(t, err)
	assert.Equal(t, "item", decoded.Name)
	_, err = descriptor.InputCodec().Decode([]byte(`{"name":"item","unknown":true}`))
	assert.Error(t, err)
	_, err = descriptor.InputCodec().Decode([]byte(`{"name":"item"} {}`))
	assert.Error(t, err)
	_, err = descriptor.InputCodec().Encode(input{})
	assert.Error(t, err)

	assert.False(t, (ir.OperationDescriptor[input, output]{}).IsValid())
	_, err = ir.NewOperationDescriptor(ir.SemanticOperationID{}, inputCodec, outputCodec, semantics, effects)
	assert.Error(t, err)
	_, err = ir.NewOperationDescriptor(operationID, inputCodec, outputCodec, ir.DescriptorSemantics{}, effects)
	assert.Error(t, err)
}

func mustEffectID(t testing.TB, value string) ir.EffectID {
	t.Helper()
	id, err := ir.NewEffectID(value)
	require.NoError(t, err)
	return id
}
