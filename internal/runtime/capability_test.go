package runtime_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/require"
)

func TestBindCapabilityRejectsVersionOutsideRange(t *testing.T) {
	t.Parallel()
	capability := mustCapability[sampleInput, sampleOutput](t, "pasture.cap.oob/v1", "2.0.0")
	binding, err := runtime.NativeCapabilityBinding(capability, mustNativeCall(t, "Oob"))
	require.NoError(t, err)
	rng, err := runtime.NewCapabilityVersionRange(ir.CapabilityContractVersion("1.0.0"), ir.CapabilityContractVersion("1.9.0"))
	require.NoError(t, err)

	_, err = runtime.BindCapability(capability, rng, binding)
	require.Error(t, err, "capability version outside the bound range is rejected")
}

func TestBindCapabilityRejectsNilBinding(t *testing.T) {
	t.Parallel()
	capability := mustCapability[sampleInput, sampleOutput](t, "pasture.cap.nilbind/v1", "1.0.0")
	rng, err := runtime.NewExactCapabilityVersion(ir.CapabilityContractVersion("1.0.0"))
	require.NoError(t, err)

	_, err = runtime.BindCapability[sampleInput, sampleOutput](capability, rng, nil)
	require.Error(t, err, "a nil binding cannot cross the BindCapability boundary")
}

func TestBindCapabilityRejectsMismatchedBinding(t *testing.T) {
	t.Parallel()
	bound := mustCapability[sampleInput, sampleOutput](t, "pasture.cap.bound/v1", "1.0.0")
	other := mustCapability[sampleInput, sampleOutput](t, "pasture.cap.other/v1", "1.0.0")
	binding, err := runtime.NativeCapabilityBinding(other, mustNativeCall(t, "Other"))
	require.NoError(t, err)
	rng, err := runtime.NewExactCapabilityVersion(ir.CapabilityContractVersion("1.0.0"))
	require.NoError(t, err)

	_, err = runtime.BindCapability(bound, rng, binding)
	require.Error(t, err, "a binding for a different capability is rejected")
}

func TestBindCapabilityAcceptsMediatedAndSemantic(t *testing.T) {
	t.Parallel()
	capability := mustCapability[sampleInput, sampleOutput](t, "pasture.cap.mediated/v1", "1.0.0")
	mediated, err := runtime.NewMediatedLowering("parent orchestrator", "the parent performs the capability")
	require.NoError(t, err)
	binding, err := runtime.MediatedCapabilityBinding(capability, mediated)
	require.NoError(t, err)
	rng, err := runtime.NewExactCapabilityVersion(ir.CapabilityContractVersion("1.0.0"))
	require.NoError(t, err)

	_, err = runtime.BindCapability(capability, rng, binding)
	require.NoError(t, err)
}
