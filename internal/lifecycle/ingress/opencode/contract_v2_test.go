package opencode_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// TestV2CatalogIsSourceDerived pins the OpenCode 2.0.20 registration surface
// to its two version roots: the generated manifest records the runtime
// contract's version floor, and it carries the host contract's whole
// catalogue. It is the v2 analogue of the v1 manifest-version pin.
func TestV2CatalogIsSourceDerived(t *testing.T) {
	t.Parallel()
	manifest := registration.OpenCode2_0_20()
	require.Equal(t, runtime.OpenCode2_0_20().Versions().Min().String(), manifest.Version, "the registration manifest and the runtime contract record one host version")
	source := hostcontract.OpenCode2_0_20().Events
	require.NotEmpty(t, source, "the OpenCode v2 host contract declares at least one event")
	require.Len(t, manifest.Events, len(source), "the generated manifest is the host contract's whole catalogue")
}

// TestV2CatalogIdentitiesAreSourceDeclared pins the deliberate relaxation
// from the v1 capture-proof rule: v2 identity bindings follow the host's
// declared 2.0.20 payload fields (a readonly sessionID on every session
// payload, sessionID plus the call id on both tool hooks, sessionID on the
// permission evaluation) rather than capture-proved coordinates. The
// committed OpenCode 2.0.20 capture sitting (with inventory, substitution,
// secret scan and clearance) turns these source-declared bindings into
// proved ones when it lands; until then no v2 row may gain or lose an
// identity without updating this policy.
func TestV2CatalogIdentitiesAreSourceDeclared(t *testing.T) {
	t.Parallel()
	manifest := registration.OpenCode2_0_20()
	for _, event := range manifest.Events {
		bindings := map[model.NativeBindingKind]int{}
		for _, identity := range event.Identities {
			require.True(t, identity.Required, "v2 identity bindings are required correlation on %q", event.NativeName)
			bindings[identity.Binding]++
		}
		switch event.NativeName {
		case "tool.execute.before", "tool.execute.after":
			require.Equal(t, map[model.NativeBindingKind]int{model.BindingSession: 1, model.BindingToolCall: 1}, bindings, "the v2 tool rows bind session plus call")
			require.Len(t, event.AllowedFields, 2, "the v2 tool rows allow exactly their identity fields on %q", event.NativeName)
		case "shell.create.before":
			require.Empty(t, event.Identities, "the shell hook declares no session field, so %q binds none", event.NativeName)
			require.Empty(t, event.AllowedFields, "the shell hook allows no identity fields on %q", event.NativeName)
		default:
			require.Equal(t, map[model.NativeBindingKind]int{model.BindingSession: 1}, bindings, "the v2 row for %q binds exactly the session identity", event.NativeName)
			require.Len(t, event.AllowedFields, 1, "the v2 row for %q allows exactly its identity field", event.NativeName)
		}
	}
}
