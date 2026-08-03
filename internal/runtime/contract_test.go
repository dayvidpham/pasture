package runtime_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fullCoreOperationBindings builds one native binding for every core operation
// kind from the shared pinned descriptors — the minimal exhaustive set.
func fullCoreOperationBindings(t *testing.T) []runtime.OperationBinding {
	t.Helper()
	bindings := make([]runtime.OperationBinding, 0, len(ir.AllOperationKinds()))
	for _, kind := range ir.AllOperationKinds() {
		descriptor, ok := runtime.CoreOperationDescriptorFor(kind)
		require.True(t, ok, "descriptor for %q", kind)
		binding, err := runtime.NativeOperationBinding(descriptor, mustNativeCall(t, "native-"+string(kind)))
		require.NoError(t, err)
		bindings = append(bindings, binding)
	}
	return bindings
}

func TestNewCoreRuntimeBindingsRequiresEveryCoreOperation(t *testing.T) {
	t.Parallel()
	all := fullCoreOperationBindings(t)

	_, err := runtime.NewCoreRuntimeBindings(all, nil)
	require.NoError(t, err, "the complete set is accepted")

	_, err = runtime.NewCoreRuntimeBindings(all[:len(all)-1], nil)
	require.Error(t, err, "a missing core operation is rejected")

	_, err = runtime.NewCoreRuntimeBindings(append(fullCoreOperationBindings(t), all[0]), nil)
	require.Error(t, err, "a duplicate core operation is rejected")
}

func TestNewCoreRuntimeBindingsRejectsNonCoreOperation(t *testing.T) {
	t.Parallel()
	descriptor := mustOperationDescriptor[sampleInput, sampleOutput](t, "pasture.custom.extension/v1")
	binding, err := runtime.NativeOperationBinding(descriptor, mustNativeCall(t, "extension"))
	require.NoError(t, err)

	_, err = runtime.NewCoreRuntimeBindings(append(fullCoreOperationBindings(t), binding), nil)
	require.Error(t, err, "an out-of-vocabulary core binding is rejected")
}

func TestNewRuntimeContractValidatesHarnessAgreement(t *testing.T) {
	t.Parallel()
	core, err := runtime.NewCoreRuntimeBindings(fullCoreOperationBindings(t), nil)
	require.NoError(t, err)
	constraint, err := runtime.NewExactVersion(mustParse(t, "2.1.210"))
	require.NoError(t, err)

	id, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "claude-code@2.1.210")
	require.NoError(t, err)

	_, err = runtime.NewRuntimeContract(id, ir.HarnessOpenCode, constraint, core)
	require.Error(t, err, "declared harness must match the identity's harness")

	contract, err := runtime.NewRuntimeContract(id, ir.HarnessClaudeCode, constraint, core)
	require.NoError(t, err)
	assert.Equal(t, ir.HarnessClaudeCode, contract.Harness())
	assert.True(t, contract.Supports(mustParse(t, "2.1.210")))
	assert.False(t, contract.Supports(mustParse(t, "2.1.211")))
}

func TestNewRuntimeContractRejectsDuplicateAndConflictingCapabilities(t *testing.T) {
	t.Parallel()
	core, err := runtime.NewCoreRuntimeBindings(fullCoreOperationBindings(t), nil)
	require.NoError(t, err)
	constraint, err := runtime.NewExactVersion(mustParse(t, "2.1.210"))
	require.NoError(t, err)
	id, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "claude-code@2.1.210")
	require.NoError(t, err)

	capability := mustCapability[sampleInput, sampleOutput](t, "pasture.cap.render/v1", "1.0.0")
	binding, err := runtime.NativeCapabilityBinding(capability, mustNativeCall(t, "Render"))
	require.NoError(t, err)
	rng, err := runtime.NewExactCapabilityVersion(ir.CapabilityContractVersion("1.0.0"))
	require.NoError(t, err)
	contribution, err := runtime.BindCapability(capability, rng, binding)
	require.NoError(t, err)

	// One contribution is accepted.
	_, err = runtime.NewRuntimeContract(id, ir.HarnessClaudeCode, constraint, core, contribution)
	require.NoError(t, err)

	// The same contribution twice is a duplicate.
	_, err = runtime.NewRuntimeContract(id, ir.HarnessClaudeCode, constraint, core, contribution, contribution)
	require.Error(t, err, "duplicate capability contribution rejected")
}
