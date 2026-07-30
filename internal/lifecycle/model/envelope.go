package model

type OccurrenceEnvelopeRef struct {
	nonJournalValue
	Runtime        RuntimeContractDefinitionRef
	Schema         LifecycleSchemaDefinitionRef
	Implementation EpochImplementationRef
	Retention      RetentionPolicyDefinitionRef
}

type SemanticEnvelopeRef struct {
	nonJournalValue
	Runtime        RuntimeContractDefinitionRef
	Schema         LifecycleSchemaDefinitionRef
	Codebook       CodebookDefinitionRef
	Interpreter    InterpreterDefinitionRef
	Implementation EpochImplementationRef
}
