package hostcontract_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func TestOpenCodeHostContractDerivesTypedRuntimeCatalog(t *testing.T) {
	t.Parallel()

	runtimeEvents := runtime.OpenCodeLifecycleEvents()
	contract := hostcontract.OpenCode1_18_10()
	require.Len(t, runtimeEvents, 47)
	require.Len(t, contract.Events, len(runtimeEvents))

	identityEvents := 0
	for index, runtimeEvent := range runtimeEvents {
		require.Equal(t, runtimeEvent.NativeName(), contract.Events[index].Name)
		if len(contract.Events[index].Identities) == 0 {
			continue
		}
		identityEvents++
		require.Contains(t, []runtime.OpenCodeLifecycleEvent{
			runtime.OpenCodeEventSessionCreated,
			runtime.OpenCodeEventToolExecuteBefore,
		}, runtimeEvent)
	}
	require.Equal(t, 2, identityEvents)
}
