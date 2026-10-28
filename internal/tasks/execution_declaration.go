package tasks

import (
	"context"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

// ExecutionActorDeclaration reads immutable registry evidence without changing
// the claim or granting authority. A prior session claim wins over a later
// declaration; an implicit generic actor never contributes model metadata.
func ExecutionActorDeclaration(ctx context.Context, tracker protocol.TaskTracker, harness ir.HarnessID, bindings []model.NativeBinding, attempted ActorClaim, implicit bool) execution.Declaration {
	t, ok := tracker.(*trackerImpl)
	if !ok || ctx.Err() != nil {
		return execution.Declaration{}
	}
	session, err := lifecycleSession(bindings)
	if err != nil || session == "" {
		return execution.Declaration{}
	}
	actor, present, err := t.readLifecycleSessionClaim(ctx, harness, session)
	if err != nil {
		return execution.Declaration{}
	}
	if !present {
		if implicit {
			return execution.Declaration{}
		}
		actor = string(attempted)
	}
	id, err := provenance.ParseActorID(actor)
	if err != nil {
		return execution.Declaration{}
	}
	if cached, ok := t.executionAgents.Load(actor); ok {
		return cached.(execution.Declaration)
	}
	ml, err := tracker.MLAgent(id)
	if err != nil {
		return execution.Declaration{}
	}
	result := execution.Declaration{
		Provider:     execution.Raw{Present: true, Member: execution.RegistryProvider, Value: string(ml.Model.Provider)},
		Model:        execution.Raw{Present: true, Member: execution.RegistryModel, Value: string(ml.Model.Name)},
		ProtocolRole: execution.Raw{Present: true, Member: execution.RegistryRole, Value: ml.Role.String()},
	}
	t.executionAgents.Store(actor, result)
	return result
}
