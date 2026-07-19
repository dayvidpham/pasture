package runtime_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/require"
)

type sampleInput struct {
	Value string `json:"value"`
}

type sampleOutput struct {
	Handle string `json:"handle"`
}

// altInput is a distinct Go type used to prove typed lookups reject a
// descriptor whose In type differs from the registered binding.
type altInput struct {
	Other int `json:"other"`
}

func mustCodec[T any](t *testing.T, schema string) ir.Codec[T] {
	t.Helper()
	codec, err := ir.NewJSONCodec[T](ir.SchemaID(schema), nil)
	require.NoError(t, err)
	return codec
}

func mustEffectSet(t *testing.T, ids ...string) ir.EffectSet {
	t.Helper()
	effectIDs := make([]ir.EffectID, 0, len(ids))
	for _, id := range ids {
		effectID, err := ir.NewEffectID(id)
		require.NoError(t, err)
		effectIDs = append(effectIDs, effectID)
	}
	set, err := ir.NewEffectSet(effectIDs...)
	require.NoError(t, err)
	return set
}

func mustOperationDescriptor[In, Out any](t *testing.T, id string) ir.OperationDescriptor[In, Out] {
	t.Helper()
	operationID, err := ir.NewSemanticOperationID(id)
	require.NoError(t, err)
	descriptor, err := ir.NewOperationDescriptor(
		operationID,
		mustCodec[In](t, id+".request/v1"),
		mustCodec[Out](t, id+".result/v1"),
		ir.DescriptorSemantics{Summary: "test operation " + id, Result: "test result"},
		mustEffectSet(t),
	)
	require.NoError(t, err)
	return descriptor
}

func mustEffectDescriptor[In, Out any](t *testing.T, id string) ir.EffectDescriptor[In, Out] {
	t.Helper()
	effectID, err := ir.NewEffectID(id)
	require.NoError(t, err)
	descriptor, err := ir.NewEffectDescriptor(
		effectID,
		mustCodec[In](t, id+".request/v1"),
		mustCodec[Out](t, id+".result/v1"),
		ir.DescriptorSemantics{Summary: "test effect " + id, Result: "test result"},
		mustEffectSet(t, id),
	)
	require.NoError(t, err)
	return descriptor
}

func mustCapability[In, Out any](t *testing.T, id, version string) ir.Capability[In, Out] {
	t.Helper()
	capability, err := ir.DefineCapability[In, Out](
		ir.CapabilityID(id),
		ir.CapabilityContractVersion(version),
		ir.DescriptorSemantics{Summary: "test capability " + id, Result: "test result"},
		mustEffectSet(t),
		mustCodec[In](t, id+".request/v1"),
		mustCodec[Out](t, id+".result/v1"),
	)
	require.NoError(t, err)
	return capability
}

func mustNativeCall(t *testing.T, name string) runtime.NativeCall {
	t.Helper()
	call, err := runtime.NewNativeCall(name, []string{"argument"}, "a native result", "inherits caller context")
	require.NoError(t, err)
	return call
}
