package projection

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

// TestOccurrenceValidationSetAdmitsOneDecisionPerEnvelope pins the invariant the
// per-page set rests on: two rows naming the same four definitions under the
// same contract are the same admission decision, so the second must not resolve
// them again. Any difference in a reference or the contract is a different
// decision and must be admitted.
func TestOccurrenceValidationSetAdmitsOneDecisionPerEnvelope(t *testing.T) {
	t.Parallel()

	contract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "claude-code/2.1.261")
	require.NoError(t, err)
	ref := func(kind model.DefinitionKind, id int64) model.DefinitionRef {
		return model.DefinitionRef{Definition: model.DefinitionJournalID(id), Kind: kind, Content: model.ContentIdentity{byte(id)}}
	}
	envelope := func(runtimeID int64) model.OccurrenceEnvelopeRef {
		return model.OccurrenceEnvelopeRef{
			Runtime:        model.RuntimeContractDefinitionRef{Contract: contract, Definition: ref(model.DefinitionRuntimeContract, runtimeID)},
			Schema:         model.LifecycleSchemaDefinitionRef{Definition: ref(model.DefinitionLifecycleSchema, 2)},
			Implementation: model.EpochImplementationRef{Definition: ref(model.DefinitionEpochImplementation, 3)},
			Retention:      model.RetentionPolicyDefinitionRef{Definition: ref(model.DefinitionRetentionPolicy, 4)},
		}
	}

	set := newOccurrenceValidationSet()
	require.True(t, set.first(occurrenceEnvelopeKeyOf(contract, envelope(1))), "the first row of a decision resolves it")
	require.False(t, set.first(occurrenceEnvelopeKeyOf(contract, envelope(1))), "a row naming the same definitions is the same decision")
	require.True(t, set.first(occurrenceEnvelopeKeyOf(contract, envelope(9))), "a new runtime definition version is a new decision")
	otherContract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "claude-code/2.1.262")
	require.NoError(t, err)
	require.True(t, set.first(occurrenceEnvelopeKeyOf(otherContract, envelope(1))), "a different delivery contract is a new decision")
}
