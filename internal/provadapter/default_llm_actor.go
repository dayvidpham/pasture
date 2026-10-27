package provadapter

import "github.com/dayvidpham/provenance"

// DefaultLLMActorName names the generic, attribution-only LLM software actor.
const DefaultLLMActorName = "pasture/default-llm"

// DefaultLLMActorID addresses reserved ordinal one, independently of the
// ordinal-zero system committer and its bootstrap authority.
func DefaultLLMActorID() provenance.ActorID {
	return provenance.ActorID{Namespace: PastureSystemNamespace, UUID: [16]byte{15: 1}}
}

// IsDefaultLLMActor classifies only the exact reserved identity. Names, execution
// metadata and neighboring reserved identities cannot confer this classification.
func IsDefaultLLMActor(id provenance.ActorID) bool {
	return id == DefaultLLMActorID()
}

// DefaultLLMActorEntry returns a fresh fixed software manifest descriptor. Its
// immutable metadata describes purpose, not a fabricated provider, model or role.
func DefaultLLMActorEntry() provenance.FixedActorEntry {
	return provenance.FixedActorEntry{
		ActorID: DefaultLLMActorID(), Namespace: PastureSystemNamespace,
		ActorKind: provenance.AgentKindSoftware, Name: DefaultLLMActorName,
		Metadata: `{"classification":"generic-llm","purpose":"execution-attribution","version":1}`,
	}
}

// DefaultLLMActorRegistration constructs the atomic fixed-registration request
// for schema-versioned initialization. Pass it to RegisterFixedSoftwareAgent
// outside a caller-owned transaction: that operation owns its transaction and
// rejects conflicting claims, manifests or software rows. Constructing this
// request does not activate an actor or change the ordinal-zero bootstrap path.
func DefaultLLMActorRegistration() provenance.FixedSoftwareAgentRegistration {
	return provenance.FixedSoftwareAgentRegistration{
		Claim: PastureSystemClaim(), Entry: DefaultLLMActorEntry(),
		AgentName: DefaultLLMActorName, Version: pastureSystemVersion, Source: pastureSystemSource,
	}
}
