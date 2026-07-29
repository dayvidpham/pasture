package lifecycle_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubFrontend exists to prove the Frontend contract is implementable exactly
// as declared — that the waist asks a frontend for nothing it cannot supply,
// and in particular that Parse needs no store, no clock and no effects.
type stubFrontend struct {
	harness ir.HarnessID
	event   lifecycle.Event
	err     error
}

func (s stubFrontend) Harness() ir.HarnessID { return s.harness }

func (s stubFrontend) Parse(_ []byte, _ lifecycle.NativeEventName) (lifecycle.Event, error) {
	return s.event, s.err
}

var _ lifecycle.Frontend = stubFrontend{}

func TestFrontendContractIsImplementableWithoutEffects(t *testing.T) {
	t.Parallel()

	binding := observationBinding(t)
	event, err := binding.NewEvent(lifecycle.NewDigest([]byte("payload")), []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
	})
	require.NoError(t, err)

	var frontend lifecycle.Frontend = stubFrontend{harness: event.Origin().Harness(), event: event}

	assert.Equal(t, event.Origin().Harness(), frontend.Harness(),
		"a frontend serves exactly one host; there is no runtime switch")

	parsed, err := frontend.Parse([]byte(`{"native":"payload"}`), event.Origin().NativeEventName())
	require.NoError(t, err)
	assert.True(t, parsed.IsValid())
	assert.Equal(t, event.Origin().ReplayKey(), parsed.Origin().ReplayKey())
}

func TestMaxNativePayloadBytesIsOneBoundForEveryHost(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 1<<20, lifecycle.MaxNativePayloadBytes,
		"the bound lives at the waist so Pasture's exposure is the same whatever a host sends")
}

func TestUnsupportedHarnessErrorNamesTheRequestedAndSupportedHosts(t *testing.T) {
	t.Parallel()

	const where = "Dispatching a native lifecycle payload (frontend_test.go)."
	err := lifecycle.UnsupportedHarnessError(
		ir.HarnessCodex,
		[]ir.HarnessID{ir.HarnessClaudeCode, ir.HarnessOpenCode},
		where,
	)

	var structured *pasterrors.StructuredError
	require.ErrorAs(t, err, &structured)
	assert.Equal(t, pasterrors.CategoryValidation, structured.Category)
	assert.Equal(t, where, structured.Where)

	// A legalization failure must say what was asked for AND what is available;
	// naming only one of the two leaves the operator guessing.
	assert.Contains(t, structured.What, string(ir.HarnessCodex))
	assert.Contains(t, structured.Fix, string(ir.HarnessClaudeCode))
	assert.Contains(t, structured.Fix, string(ir.HarnessOpenCode))
	assert.NotContains(t, structured.Fix, string(ir.HarnessCodex),
		"the unsupported host must not appear in the list of ones to use instead")
	assert.NotEmpty(t, structured.Impact, "the caller must be told nothing was recorded")
}

func TestUnsupportedHarnessErrorWithNoFrontendsAtAll(t *testing.T) {
	t.Parallel()

	err := lifecycle.UnsupportedHarnessError(ir.HarnessCodex, nil, "frontend_test.go")

	var structured *pasterrors.StructuredError
	require.ErrorAs(t, err, &structured)
	assert.Contains(t, structured.Fix, "no lifecycle frontends",
		"an empty supported set must say so rather than render an empty list")
}
