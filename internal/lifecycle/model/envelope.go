package model

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
}

type SemanticEnvelopeRef struct {
	nonJournalValue
	Runtime        RuntimeContractDefinitionRef
	Schema         LifecycleSchemaDefinitionRef
	Metamodel      LifecycleMetamodelRef
	Interpreter    InterpreterDefinitionRef
	Implementation EpochImplementationRef
}
