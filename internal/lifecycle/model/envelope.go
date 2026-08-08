package model

import "github.com/dayvidpham/pasture/internal/acceptance/origin"

type OccurrenceEnvelopeRef struct {
	nonJournalValue
	Runtime RuntimeContractDefinitionRef
	// HostVersion records the version observed at ingress. It is provenance,
	// not an admission check; payloads are retained even when this value falls
	// outside the currently described range.
	HostVersion    string
	Schema         LifecycleSchemaDefinitionRef
	Implementation EpochImplementationRef
	Retention      RetentionPolicyDefinitionRef
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
