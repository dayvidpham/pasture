package tasks

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

func TestExecutionDeclarationUsesStoredClaimAndRegisteredMLActor(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	models := provenance.DefaultModelRegistry().Models()
	require.NotEmpty(t, models)
	actor, err := store.RegisterMLAgent("execution-test", provenance.RoleWorker, models[0].Provider, models[0].Name)
	require.NoError(t, err)
	bindings := []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "execution-session"}}
	claimAs(t, store, actor.ID.String(), "execution-session")
	declaration := ExecutionActorDeclaration(t.Context(), store, gateHarness, bindings, ActorClaim("other/untrusted"), false)
	require.Equal(t, string(actor.Model.Name), declaration.Model.Value)
	require.Equal(t, string(actor.Model.Provider), declaration.Provider.Value)
	require.Equal(t, execution.RegistryRole, declaration.ProtocolRole.Member)
	metadata, err := execution.Normalize(execution.Inputs{Kind: execution.Selection, ActorRegistry: declaration})
	require.NoError(t, err)
	require.Equal(t, execution.Observed, metadata.Model.Status)
	require.Equal(t, execution.Observed, metadata.Provider.Status)
	require.Empty(t, ExecutionActorDeclaration(t.Context(), store, gateHarness, nil, ActorClaim(actor.ID.String()), false))
}
