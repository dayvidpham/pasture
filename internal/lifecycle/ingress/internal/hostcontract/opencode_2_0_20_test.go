package hostcontract_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func TestOpenCode2HostContractDerivesTypedRuntimeCatalog(t *testing.T) {
	t.Parallel()

	runtimeEvents := runtime.OpenCode2LifecycleEvents()
	contract := hostcontract.OpenCode2_0_20()
	require.Len(t, runtimeEvents, 17)
	require.Len(t, contract.Events, len(runtimeEvents))
	require.Equal(t, "2.0.20", contract.Version)

	sessionBound := 0
	callBound := 0
	for index, runtimeEvent := range runtimeEvents {
		require.Equal(t, runtimeEvent.NativeName(), contract.Events[index].Name)
		for _, identity := range contract.Events[index].Identities {
			require.True(t, identity.Binding.IsValid(), "event %q carries an invalid identity binding kind", contract.Events[index].Name)
			require.True(t, identity.Required, "v2 identity bindings are required correlation, got optional on %q", contract.Events[index].Name)
			switch identity.Binding {
			case model.BindingSession:
				sessionBound++
			case model.BindingToolCall:
				callBound++
			default:
				t.Fatalf("event %q carries an unexpected identity binding %q", contract.Events[index].Name, identity.Binding)
			}
		}
		_ = runtimeEvent
	}
	// Identity follows the host's declared payload fields: every v2 event but
	// the shell hook binds the session identity, and both tool rows also bind
	// the call identity.
	require.Equal(t, 16, sessionBound, "every v2 event but shell.create.before binds a session identity")
	require.Equal(t, 2, callBound, "both v2 tool rows bind the call identity")
	require.Empty(t, contractEventByName(t, contract, "shell.create.before").Identities)
	require.Empty(t, contractEventByName(t, contract, "shell.create.before").Fields)
}

func contractEventByName(t *testing.T, contract hostcontract.Contract, name string) hostcontract.Event {
	t.Helper()
	for _, event := range contract.Events {
		if event.Name == name {
			return event
		}
	}
	t.Fatalf("the v2 contract declares no event %q", name)
	return hostcontract.Event{}
}

// TestOpenCode2NativeNamesAreSpelledAsTheHostEmitsThem pins the seventeen
// OpenCode 2.0.20 coordinates against the host's own schema and plugin types
// at the v2.0.20 tag: the session.created bus event (SessionEvent.Created in
// packages/schema/src/session-event.ts), twelve session hooks (SessionHooks),
// two tool hooks (ToolHooks), one permission hook (PermissionHooks) and one
// shell hook (ShellHooks), each qualified by its emitting domain. Three
// coordinates repeat v1 native names with version-qualified symbols because
// their v2 payload shapes differ.
func TestOpenCode2NativeNamesAreSpelledAsTheHostEmitsThem(t *testing.T) {
	t.Parallel()

	contract := hostcontract.OpenCode2_0_20()
	names := map[string]string{}
	for _, event := range contract.Events {
		names[event.Name] = event.Symbol
	}
	require.Len(t, names, 17)
	for _, name := range []string{
		"session.created",
		"session.prompt", "session.context", "session.compaction", "session.generate",
		"session.title", "session.model.request", "session.http.request", "session.http.response",
		"session.experimental.ws.handshake", "session.experimental.ws.send", "session.experimental.ws.receive",
		"session.retry", "tool.execute.before", "tool.execute.after",
		"permission.evaluate", "shell.create.before",
	} {
		symbol, found := names[name]
		require.True(t, found, "the v2 contract declares coordinate %q", name)
		require.True(t, strings.HasPrefix(symbol, "EventOpenCode2"), "the symbol for %q is version-qualified, got %q", name, symbol)
	}
	require.Equal(t, "EventOpenCode2SessionCreated", names["session.created"])
	require.Equal(t, "EventOpenCode2ToolExecuteBefore", names["tool.execute.before"])
	require.Equal(t, "EventOpenCode2PermissionEvaluate", names["permission.evaluate"])
	require.Equal(t, "session.created", runtime.OpenCode2EventSessionCreated.NativeName())
	require.Equal(t, "permission.evaluate", runtime.OpenCode2EventPermissionEvaluate.NativeName())
}

// TestOpenCode2LifecycleProfile consults the typed runtime profile the v2
// host contract derives from: sixteen blocking gate consultations plus one
// session-start observation, every row without response-channel evidence so
// each derives CapabilityNone, and only the session observation and the
// post-hoc tool row stand outside the pre-action set.
func TestOpenCode2LifecycleProfile(t *testing.T) {
	t.Parallel()

	contract := runtime.OpenCode2_0_20Lifecycle()
	require.True(t, contract.IsValid())
	events := contract.Events()
	require.Len(t, events, 17)

	for _, event := range events {
		mapping, err := contract.Mapping(event)
		require.NoError(t, err)
		require.True(t, mapping.IsValid(), "the v2 row for %q is a valid lifecycle mapping", mapping.NativeName())
		require.Equal(t, runtime.CapabilityNone, mapping.Response(), "the v2 row for %q carries no proved denial channel", mapping.NativeName())
		switch event {
		case runtime.OpenCode2EventSessionCreated:
			require.Equal(t, runtime.SemanticObservation, mapping.Semantic())
			require.Equal(t, runtime.NonBlocking, mapping.Blocking())
			require.False(t, mapping.PreAction())
		case runtime.OpenCode2EventToolExecuteAfter:
			require.Equal(t, runtime.SemanticGateConsultation, mapping.Semantic())
			require.Equal(t, runtime.Blocking, mapping.Blocking())
			require.False(t, mapping.PreAction(), "the post-hoc tool row consults no pending action")
		default:
			require.Equal(t, runtime.SemanticGateConsultation, mapping.Semantic())
			require.Equal(t, runtime.Blocking, mapping.Blocking())
			require.True(t, mapping.PreAction(), "the v2 hook row for %q is a pre-action gate", mapping.NativeName())
		}
	}
}
