package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// ActivateLaneTestDefinitions prepares immutable owner documents, not an
// occurrence. Lane routing and ACK-loss tests exercise a receipt commit against
// an initialized store, not first-delivery definition publication. Keep setup
// outside their request deadlines without changing the service's commit budget.
func ActivateLaneTestDefinitions(t *testing.T, service receipt.Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	// Service and JournalAppender are values: this changes definition setup
	// only, never the original service or the measured request's budget.
	service.Appender.Deadline = 30 * time.Second
	_, err := receipt.EnsureActiveMetamodel(ctx, service)
	require.NoError(t, err, "initialize the lane fixture's metamodel")
	contract := registration.ClaudeCode__2_1_261().Contract
	_, err = receipt.EnsureOccurrenceEnvelope(ctx, service, contract, model.OccurrenceEnvelopeRef{})
	require.NoError(t, err, "initialize the lane fixture's occurrence owners")
	runtimeContract, err := definitions.RuntimeContractForDelivery(contract)
	require.NoError(t, err)
	documents, err := definitions.ActiveForContract(runtimeContract)
	require.NoError(t, err)
	_, err = receipt.EnsureActiveDefinition(ctx, service, documents.Interpreter)
	require.NoError(t, err, "initialize the lane fixture's interpreter")
}
