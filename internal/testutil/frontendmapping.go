package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// AssertEventMappingsCoverRegistration holds a harness frontend's event mapping
// TOTAL and CORRECT over its generated registration: every registered event
// binds, and it binds to the runtime profile event that carries the same native
// name, which is what makes the pairing right rather than merely present. The
// population is the registration manifest itself, so an event registered later
// is covered without an edit; a manifest with no events is refused.
func AssertEventMappingsCoverRegistration[E comparable](
	t *testing.T,
	manifest registration.Manifest,
	contract runtime.LifecycleContract[E],
	bind func(model.ContractEventKind, []model.NativeBinding) (waist.L1, []waist.Identity, error),
) {
	t.Helper()
	require.NotEmpty(t, manifest.Events, "the %s registration declares at least one event", manifest.Harness)
	byNativeName := make(map[string]E, len(contract.Events()))
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		require.NoError(t, err)
		byNativeName[mapping.NativeName()] = event
	}
	for _, entry := range manifest.Events {
		runtimeEvent, declared := byNativeName[entry.NativeName]
		require.True(t, declared, "the %s runtime profile declares no row named %q for registered event kind %d", manifest.Harness, entry.NativeName, entry.Kind)
		want, err := waist.BindEvent(contract, runtimeEvent)
		require.NoError(t, err, entry.NativeName)
		got, _, err := bind(entry.Kind, nil)
		require.NoError(t, err, "registered %s event %q (kind %d) has no frontend mapping", manifest.Harness, entry.NativeName, entry.Kind)
		require.Equal(t, want, got, "registered %s event %q is mapped to a runtime row with a different native name", manifest.Harness, entry.NativeName)
	}
}
