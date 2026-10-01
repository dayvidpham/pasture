package hostcontract_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func TestOpenCode2HostContractDerivesTypedRuntimeCatalog(t *testing.T) {
	t.Parallel()

	runtimeEvents := runtime.OpenCode2LifecycleEvents()
	contract := hostcontract.OpenCode2_0_20()
	require.Len(t, runtimeEvents, 16)
	require.Len(t, contract.Events, len(runtimeEvents))
	require.Equal(t, "2.0.20", contract.Version)

	identityEvents := 0
	for index, runtimeEvent := range runtimeEvents {
		require.Equal(t, runtimeEvent.NativeName(), contract.Events[index].Name)
		if len(contract.Events[index].Identities) == 0 {
			continue
		}
		identityEvents++
		require.Contains(t, []runtime.OpenCode2LifecycleEvent{
			runtime.OpenCode2EventPermissionEvaluate,
			runtime.OpenCode2EventToolExecuteBefore,
		}, runtimeEvent)
	}
	require.Equal(t, 2, identityEvents)
}

// TestOpenCode2NativeNamesAreSpelledAsTheHostTriggersThem pins the sixteen
// OpenCode 2.0.20 hook coordinates against the host's own plugin types at the
// v2.0.20 tag: twelve session hooks (SessionHooks), two tool hooks
// (ToolHooks), one permission hook (PermissionHooks) and one shell hook
// (ShellHooks), each qualified by its registering domain. The two tool
// coordinates repeat v1 native names with version-qualified symbols because
// their v2 payload shapes differ; every other coordinate is new in v2.
func TestOpenCode2NativeNamesAreSpelledAsTheHostTriggersThem(t *testing.T) {
	t.Parallel()

	contract := hostcontract.OpenCode2_0_20()
	names := map[string]string{}
	for _, event := range contract.Events {
		names[event.Name] = event.Symbol
	}
	require.Len(t, names, 16)
	for _, name := range []string{
		"session.prompt", "session.context", "session.compaction", "session.generate",
		"session.title", "session.model.request", "session.http.request", "session.http.response",
		"session.experimental.ws.handshake", "session.experimental.ws.send", "session.experimental.ws.receive",
		"session.retry", "tool.execute.before", "tool.execute.after",
		"permission.evaluate", "shell.create.before",
	} {
		symbol, found := names[name]
		require.True(t, found, "the v2 contract declares hook coordinate %q", name)
		require.True(t, strings.HasPrefix(symbol, "EventOpenCode2"), "the symbol for %q is version-qualified, got %q", name, symbol)
	}
	require.Equal(t, "EventOpenCode2ToolExecuteBefore", names["tool.execute.before"])
	require.Equal(t, "EventOpenCode2PermissionEvaluate", names["permission.evaluate"])
	require.Equal(t, "session.prompt", runtime.OpenCode2EventSessionPrompt.NativeName())
	require.Equal(t, "permission.evaluate", runtime.OpenCode2EventPermissionEvaluate.NativeName())
}
