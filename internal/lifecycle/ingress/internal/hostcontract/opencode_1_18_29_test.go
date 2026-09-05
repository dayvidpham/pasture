package hostcontract_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func TestOpenCodeHostContractDerivesTypedRuntimeCatalog(t *testing.T) {
	t.Parallel()

	runtimeEvents := runtime.OpenCodeLifecycleEvents()
	contract := hostcontract.OpenCode1_18_29()
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

// TestOpenCodeNativeNamesAreSpelledAsTheHostEmitsThem pins the two OpenCode
// event types that are not spelled with dots alone, against the host's own
// source at 1.18.10 and 1.18.29 (the text is identical at both):
//   - "installation.update-available" (packages/schema/src/installation-event.ts)
//     is hyphenated. The tree once spelled it with an underscore, which the host
//     never sends, so the row could not match a live payload.
//   - "experimental.provider.small_model" (packages/opencode/src/provider/provider.ts,
//     the plugin trigger name) carries an underscore, and that is the host's spelling.
//
// The Go identifier derived from either name carries an underscore, so the
// corrected hyphenated name renames nothing.
func TestOpenCodeNativeNamesAreSpelledAsTheHostEmitsThem(t *testing.T) {
	t.Parallel()

	contract := hostcontract.OpenCode1_18_29()
	symbols := map[string]string{}
	var hyphenated, underscored []string
	for _, event := range contract.Events {
		symbols[event.Name] = event.Symbol
		if strings.Contains(event.Name, "-") {
			hyphenated = append(hyphenated, event.Name)
		}
		if strings.Contains(event.Name, "_") {
			underscored = append(underscored, event.Name)
		}
	}
	require.Equal(t, []string{"installation.update-available"}, hyphenated, "exactly one OpenCode event type is hyphenated, spelled as the host emits it")
	require.Equal(t, []string{"experimental.provider.small_model"}, underscored, "exactly one OpenCode event type carries an underscore, spelled as the host emits it")
	require.Equal(t, "EventOpenCodeInstallationUpdate_available", symbols["installation.update-available"], "the identifier derived from the hyphenated name keeps its underscore form")
	require.Equal(t, "EventOpenCodeExperimentalProviderSmall_model", symbols["experimental.provider.small_model"])
	require.Equal(t, "installation.update-available", runtime.OpenCodeEventInstallationUpdateAvailable.NativeName())
}
