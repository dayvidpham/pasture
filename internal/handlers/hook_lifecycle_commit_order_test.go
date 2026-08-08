package handlers

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/tasks"
)

type commitOrderClock struct{}

func (commitOrderClock) Now() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

type commitOrderOperations struct{}

func (commitOrderOperations) NewOperationID() (string, error) { return "test.commit-order", nil }

func TestDeliveryCommitActivatesMetamodelBeforeBindFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	_, err = tracker.Create("file://commit-order-test", "bootstrap", "initialize lifecycle identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)

	service, err := tasks.NewLifecycleReceiptService(tracker, commitOrderClock{}, commitOrderOperations{})
	require.NoError(t, err)
	manifest := frontendRegistry[ir.HarnessClaudeCode].manifest
	require.NotEmpty(t, manifest.Events)
	event := manifest.Events[0]
	wantErr := errors.New("bind failed after metamodel activation")
	dispatch := frontendRegistry[ir.HarnessClaudeCode]
	dispatch.bind = func(model.ContractEventKind, []model.NativeBinding) (waist.L1, []waist.Identity, error) {
		return waist.L1{}, nil, wantErr
	}

	_, err = deliveryCommit(context.Background(), service, dispatch, event, receipt.Delivery{Contract: manifest.Contract, Event: event.Kind})
	require.ErrorIs(t, err, wantErr)
	_, journaled, err := receipt.ResolveActiveMetamodel(tracker.Journal())
	require.NoError(t, err)
	require.True(t, journaled, "legacy commit ordering activates the metamodel before bind/derive failure")

	page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1"},
		Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
	})
	require.NoError(t, err)
	require.Empty(t, page.Rows, "bind failure must stop before the occurrence receipt")
}
