package runtime_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupOperationBindingNative(t *testing.T) {
	t.Parallel()
	contract := runtime.ClaudeCode2_1_210()
	descriptor, ok := runtime.CoreOperationDescriptorFor(ir.OperationDelegateAssignment)
	require.True(t, ok)

	binding, err := runtime.LookupOperationBinding(contract, descriptor)
	require.NoError(t, err)
	assert.Equal(t, effects.RuntimeClassNative, binding.Class())
	call, isNative := binding.Native()
	require.True(t, isNative)
	assert.Equal(t, "Agent", call.CallName())
	assert.NotEmpty(t, call.ResultSemantics())
	_, isMediated := binding.Mediated()
	assert.False(t, isMediated)
}

func TestLookupOperationBindingMediatedAndSemantic(t *testing.T) {
	t.Parallel()
	claude := runtime.ClaudeCode2_1_210()
	collect, ok := runtime.CoreOperationDescriptorFor(ir.OperationCollectAssignmentResults)
	require.True(t, ok)
	binding, err := runtime.LookupOperationBinding(claude, collect)
	require.NoError(t, err)
	assert.Equal(t, effects.RuntimeClassParentMediated, binding.Class())
	mediated, isMediated := binding.Mediated()
	require.True(t, isMediated)
	assert.NotEmpty(t, mediated.Mediator())

	opencode := runtime.OpenCode1_17_18()
	cont, ok := runtime.CoreOperationDescriptorFor(ir.OperationContinueAssignment)
	require.True(t, ok)
	semBinding, err := runtime.LookupOperationBinding(opencode, cont)
	require.NoError(t, err)
	assert.Equal(t, effects.RuntimeClassSemanticInstruction, semBinding.Class())
	sem, isSem := semBinding.Semantic()
	require.True(t, isSem)
	assert.NotEmpty(t, sem.InstructionTemplate())
}

func TestLookupOperationBindingUnsupportedFailsActionably(t *testing.T) {
	t.Parallel()
	opencode := runtime.OpenCode1_17_18()
	stop, ok := runtime.CoreOperationDescriptorFor(ir.OperationStopAssignment)
	require.True(t, ok)

	binding, err := runtime.LookupOperationBinding(opencode, stop)
	require.Error(t, err, "unsupported stop must not fall through to a native call")
	assert.Nil(t, binding)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestLookupOperationBindingUnbound(t *testing.T) {
	t.Parallel()
	contract := runtime.ClaudeCode2_1_210()
	unknown := mustOperationDescriptor[sampleInput, sampleOutput](t, "pasture.custom.unbound/v1")

	binding, err := runtime.LookupOperationBinding(contract, unknown)
	require.Error(t, err)
	assert.Nil(t, binding)
	assert.Contains(t, err.Error(), "unbound")
}

func TestLookupOperationBindingTypeMismatch(t *testing.T) {
	t.Parallel()
	contract := runtime.ClaudeCode2_1_210()
	coreID, ok := ir.CoreOperationID(ir.OperationInvokeSkill)
	require.True(t, ok)
	// Same descriptor identity, different In type: must not reuse the binding.
	mismatch := mustOperationDescriptor[altInput, runtime.OrchestrationResult](t, coreID.String())

	binding, err := runtime.LookupOperationBinding(contract, mismatch)
	require.Error(t, err)
	assert.Nil(t, binding)
	assert.Contains(t, err.Error(), "incompatible")
}

func TestLookupOperationBindingZeroContract(t *testing.T) {
	t.Parallel()
	descriptor, ok := runtime.CoreOperationDescriptorFor(ir.OperationInvokeSkill)
	require.True(t, ok)
	binding, err := runtime.LookupOperationBinding(runtime.RuntimeContract{}, descriptor)
	require.Error(t, err)
	assert.Nil(t, binding)
}

func TestLookupEffectBinding(t *testing.T) {
	t.Parallel()
	descriptor := mustEffectDescriptor[sampleInput, sampleOutput](t, "pasture.effect.fs.write/v1")
	native, err := runtime.NativeEffectBinding(descriptor, mustNativeCall(t, "write-file"))
	require.NoError(t, err)

	unsupportedDescriptor := mustEffectDescriptor[sampleInput, sampleOutput](t, "pasture.effect.shell.pipe/v1")
	unsupported, err := runtime.UnsupportedEffectBinding(unsupportedDescriptor, "sh -c pipelines have no modeled semantics")
	require.NoError(t, err)

	core, err := runtime.NewCoreRuntimeBindings(fullCoreOperationBindings(t), []runtime.EffectBinding{native, unsupported})
	require.NoError(t, err)
	id, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "claude-code@2.1.210")
	require.NoError(t, err)
	constraint, err := runtime.NewExactVersion(mustParse(t, "2.1.210"))
	require.NoError(t, err)
	contract, err := runtime.NewRuntimeContract(id, ir.HarnessClaudeCode, constraint, core)
	require.NoError(t, err)

	binding, err := runtime.LookupEffectBinding(contract, descriptor)
	require.NoError(t, err)
	assert.Equal(t, effects.RuntimeClassNative, binding.Class())

	// An unsupported shell construct fails actionably; it never hides in sh -c.
	failed, err := runtime.LookupEffectBinding(contract, unsupportedDescriptor)
	require.Error(t, err)
	assert.Nil(t, failed)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestLookupCapabilityBinding(t *testing.T) {
	t.Parallel()
	capability := mustCapability[sampleInput, sampleOutput](t, "pasture.cap.diagram/v1", "1.2.0")
	binding, err := runtime.NativeCapabilityBinding(capability, mustNativeCall(t, "RenderDiagram"))
	require.NoError(t, err)
	rng, err := runtime.NewCapabilityVersionRange(ir.CapabilityContractVersion("1.0.0"), ir.CapabilityContractVersion("1.9.0"))
	require.NoError(t, err)
	contribution, err := runtime.BindCapability(capability, rng, binding)
	require.NoError(t, err)

	core, err := runtime.NewCoreRuntimeBindings(fullCoreOperationBindings(t), nil)
	require.NoError(t, err)
	id, err := ir.NewRuntimeContractID(ir.HarnessOpenCode, "opencode@1.17.18")
	require.NoError(t, err)
	constraint, err := runtime.NewExactVersion(mustParse(t, "1.17.18"))
	require.NoError(t, err)
	contract, err := runtime.NewRuntimeContract(id, ir.HarnessOpenCode, constraint, core, contribution)
	require.NoError(t, err)

	resolved, err := runtime.LookupCapabilityBinding(contract, capability)
	require.NoError(t, err)
	assert.Equal(t, capability.ID(), resolved.CapabilityID())
	assert.Equal(t, effects.RuntimeClassNative, resolved.Class())

	// A capability whose version is out of the bound range fails.
	outOfRange := mustCapability[sampleInput, sampleOutput](t, "pasture.cap.diagram/v1", "2.0.0")
	failed, err := runtime.LookupCapabilityBinding(contract, outOfRange)
	require.Error(t, err)
	assert.Nil(t, failed)
}
