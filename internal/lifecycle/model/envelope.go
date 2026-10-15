package model

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/acceptance/origin"
)

type OccurrenceEnvelopeRef struct {
	nonJournalValue
	Runtime RuntimeContractDefinitionRef
	// HostVersion records the version observed at ingress. It is provenance,
	// not an admission check; payloads are retained even when this value falls
	// outside the currently described range.
	HostVersion string
	// HostVersionSource is optional provenance, never running-process
	// attestation. Omission preserves the exact encoding of legacy envelopes.
	HostVersionSource HostVersionSource `json:"hostVersionSource,omitempty"`
	Schema            LifecycleSchemaDefinitionRef
	Implementation    EpochImplementationRef
	Retention         RetentionPolicyDefinitionRef
	// Origin records the capture provenance origin of the occurrence (M4 raw
	// ingestion carrier). It is provenance-only: the write gate never observes
	// it. The empty value means the native sentinel (authentic-capture) for
	// records committed before origin marking; the member is omitted from the
	// JSON encoding when unset so pre-origin envelopes stay byte-identical.
	Origin origin.CaptureOrigin `json:"origin,omitempty"`
}

type SemanticEnvelopeRef struct {
	nonJournalValue
	Runtime        RuntimeContractDefinitionRef
	Schema         LifecycleSchemaDefinitionRef
	Metamodel      LifecycleMetamodelRef
	Interpreter    InterpreterDefinitionRef
	Implementation EpochImplementationRef
}

// ValidateOccurrenceEnvelope proves an occurrence envelope is structurally
// complete: four nonzero refs with the exact owner kind per slot. Resolution
// (body, digest, slot, state) is enforced at the receipt and read boundaries.
func ValidateOccurrenceEnvelope(envelope OccurrenceEnvelopeRef) error {
	slots := []struct {
		name string
		ref  DefinitionRef
		kind DefinitionKind
	}{
		{"runtime", envelope.Runtime.Definition, DefinitionRuntimeContract},
		{"schema", envelope.Schema.Definition, DefinitionLifecycleSchema},
		{"implementation", envelope.Implementation.Definition, DefinitionEpochImplementation},
		{"retention", envelope.Retention.Definition, DefinitionRetentionPolicy},
	}
	for _, slot := range slots {
		if slot.ref.Definition <= 0 || slot.ref.Kind != slot.kind || slot.ref.Content == (ContentIdentity{}) {
			return fmt.Errorf("lifecycle occurrence envelope slot %q is not a complete nonzero owner reference", slot.name)
		}
	}
	return nil
}

// SharedRefsEqual reports whether the three refs the semantic envelope shares
// with its associated occurrence envelope are exactly equal. Individually
// valid but different paired refs fail the pair.
func (s SemanticEnvelopeRef) SharedRefsEqual(occurrence OccurrenceEnvelopeRef) bool {
	return s.Runtime.Definition == occurrence.Runtime.Definition &&
		s.Schema.Definition == occurrence.Schema.Definition &&
		s.Implementation.Definition == occurrence.Implementation.Definition
}
