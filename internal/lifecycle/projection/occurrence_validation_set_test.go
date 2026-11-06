package projection

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

// TestOccurrenceValidationSetAdmitsOneDecisionPerDistinctKey pins the invariant
// the per-page set rests on: the admission decision is a function of the
// evidence kind, the four typed definition references and the delivery
// contract. Any change to one of them is a new decision; a change only in
// provenance (HostVersion) is not.
func TestOccurrenceValidationSetAdmitsOneDecisionPerDistinctKey(t *testing.T) {
	t.Parallel()

	contract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "claude-code/2.1.261")
	require.NoError(t, err)
	otherContract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "claude-code/2.1.262")
	require.NoError(t, err)
	ref := func(kind model.DefinitionKind, id int64) model.DefinitionRef {
		return model.DefinitionRef{Definition: model.DefinitionJournalID(id), Kind: kind, Content: model.ContentIdentity{byte(id)}}
	}
	base := func() model.OccurrenceEnvelopeRef {
		return model.OccurrenceEnvelopeRef{
			Runtime:        model.RuntimeContractDefinitionRef{Contract: contract, Definition: ref(model.DefinitionRuntimeContract, 1)},
			Schema:         model.LifecycleSchemaDefinitionRef{Definition: ref(model.DefinitionLifecycleSchema, 2)},
			Implementation: model.EpochImplementationRef{Definition: ref(model.DefinitionEpochImplementation, 3)},
			Retention:      model.RetentionPolicyDefinitionRef{Definition: ref(model.DefinitionRetentionPolicy, 4)},
		}
	}
	const v3 = provenance.EvidenceKind("pasture.lifecycle.occurrence.v3")
	key := func(envelope model.OccurrenceEnvelopeRef, c ir.RuntimeContractID, kind provenance.EvidenceKind) occurrenceEnvelopeKey {
		return occurrenceEnvelopeKeyOf(kind, c, envelope)
	}

	set := newOccurrenceValidationSet()
	admit := func(k occurrenceEnvelopeKey) bool {
		ran := false
		require.NoError(t, set.admitOnce(k, func() error { ran = true; return nil }))
		return ran
	}

	require.True(t, admit(key(base(), contract, v3)), "the first row of a decision resolves it")
	require.False(t, admit(key(base(), contract, v3)), "an identical decision is admitted once")

	for _, tc := range []struct {
		name     string
		mutate   func(*model.OccurrenceEnvelopeRef)
		contract ir.RuntimeContractID
		kind     provenance.EvidenceKind
	}{
		{"runtime definition", func(e *model.OccurrenceEnvelopeRef) {
			e.Runtime.Definition = ref(model.DefinitionRuntimeContract, 9)
		}, contract, v3},
		{"runtime contract", func(e *model.OccurrenceEnvelopeRef) {
			e.Runtime.Contract = otherContract
		}, contract, v3},
		{"schema", func(e *model.OccurrenceEnvelopeRef) {
			e.Schema.Definition = ref(model.DefinitionLifecycleSchema, 9)
		}, contract, v3},
		{"implementation", func(e *model.OccurrenceEnvelopeRef) {
			e.Implementation.Definition = ref(model.DefinitionEpochImplementation, 9)
		}, contract, v3},
		{"retention", func(e *model.OccurrenceEnvelopeRef) {
			e.Retention.Definition = ref(model.DefinitionRetentionPolicy, 9)
		}, contract, v3},
		{"definition content", func(e *model.OccurrenceEnvelopeRef) {
			e.Schema.Definition.Content = model.ContentIdentity{99}
		}, contract, v3},
		{"definition kind", func(e *model.OccurrenceEnvelopeRef) {
			e.Retention.Definition.Kind = model.DefinitionLifecycleSchema
		}, contract, v3},
		{"delivery contract", func(*model.OccurrenceEnvelopeRef) {}, otherContract, v3},
		{"evidence kind", func(*model.OccurrenceEnvelopeRef) {}, contract, provenance.EvidenceKind("pasture.lifecycle.occurrence.v2")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := base()
			tc.mutate(&envelope)
			require.True(t, admit(key(envelope, tc.contract, tc.kind)), "a changed decision must be admitted")
		})
	}

	// Provenance-only members are not admission inputs, so they must share the
	// decision rather than trigger a second resolution.
	provenanceOnly := base()
	provenanceOnly.HostVersion = "9.9.9"
	provenanceOnly.HostVersionSource = model.HostVersionSource("caller-supplied")
	require.False(t, admit(key(provenanceOnly, contract, v3)), "a provenance-only difference is the same decision")

	// A failed decision is recorded before it runs, so a later row can never
	// silently reuse it: the caller aborts on error.
	poisonedEnvelope := base()
	poisonedEnvelope.Runtime.Definition = ref(model.DefinitionRuntimeContract, 42)
	poisoned := key(poisonedEnvelope, contract, v3)
	require.Error(t, set.admitOnce(poisoned, func() error { return errors.New("boom") }))
	require.NoError(t, set.admitOnce(poisoned, func() error {
		t.Fatal("a failed decision must not run again")
		return nil
	}))
}
