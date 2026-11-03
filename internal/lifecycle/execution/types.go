// Package execution normalizes bounded execution evidence without consulting
// identity registries, changing authority, or performing I/O.
package execution

type Status string

const (
	Observed    Status = "observed"
	Unavailable Status = "unavailable"
	Invalid     Status = "invalid"
	Conflict    Status = "conflict"
)

type Source string

const (
	Host          Source = "host"
	Environment   Source = "environment"
	ActorRegistry Source = "actor-registry"
	None          Source = "none"
)

type Kind string

const (
	Primary    Kind = "primary"
	Compaction Kind = "compaction"
	Title      Kind = "title"
	Generate   Kind = "generate"
	Selection  Kind = "selection"
	Child      Kind = "child"
)

type Vocabulary string

const (
	NativeAgent Vocabulary = "native-agent"
	PastureRole Vocabulary = "pasture-role"
)

type Reason string

const (
	RejectedInput                      Reason = "rejected-input"
	ValidDisagreement                  Reason = "valid-disagreement"
	ValidDisagreementWithRejectedInput Reason = "valid-disagreement-with-rejected-input"
	UnpairedProvider                   Reason = "unpaired-provider"
	NoEvidence                         Reason = "no-evidence"
)

// Member identifies a literal source member, never caller-provided prose.
type Member string

const (
	HostProvider        Member = "model.providerID"
	HostModelID         Member = "model.id"
	HostModel           Member = "model"
	HostToModel         Member = "to_model"
	HostVariant         Member = "model.variant"
	HostAgent           Member = "agent"
	HostAgentType       Member = "agent_type"
	EnvironmentProvider Member = "PASTURE_EXECUTION_PROVIDER"
	EnvironmentModel    Member = "PASTURE_EXECUTION_MODEL"
	EnvironmentRole     Member = "PASTURE_EXECUTION_ROLE"
	RegistryProvider    Member = "provider"
	RegistryModel       Member = "model"
	RegistryRole        Member = "role"
)

const MaxValueBytes = 512
const MaxCandidates = 3

// Raw distinguishes an absent member from a supplied empty (invalid) value.
// Raw is ingress-only: never persist or log it.
type Raw struct {
	Present bool
	Member  Member
	Value   string
}

// Declaration has at most one observation per field. Inputs fixes source
// cardinality and priority; no map or last-wins bag can introduce extra sources.
type Declaration struct {
	Provider     Raw
	Model        Raw
	Variant      Raw
	Role         Raw
	ProtocolRole Raw
}

type Scope struct {
	AgentID   string `json:"agentId,omitempty"`
	TurnID    string `json:"turnId,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

type Inputs struct {
	Kind          Kind
	Scope         Scope
	Host          Declaration
	Environment   Declaration
	ActorRegistry Declaration
}

type Candidate struct {
	Source Source `json:"source"`
	Field  Member `json:"field"`
	Value  string `json:"value"`
}

type AttributionValue struct {
	Status     Status      `json:"status"`
	Value      string      `json:"value,omitempty"`
	Source     Source      `json:"source"`
	Field      Member      `json:"field,omitempty"`
	Vocabulary Vocabulary  `json:"vocabulary,omitempty"`
	Reason     Reason      `json:"reason,omitempty"`
	Candidates []Candidate `json:"candidates,omitempty"`
}

type Metadata struct {
	Kind Kind `json:"kind"`
	Scope
	Provider     AttributionValue `json:"provider"`
	Model        AttributionValue `json:"model"`
	Variant      AttributionValue `json:"variant"`
	Role         AttributionValue `json:"role"`
	ProtocolRole AttributionValue `json:"protocolRole"`
}

// HasEvidence reports whether an observation conveys any actual evidence,
// including rejected or conflicting evidence and unchosen candidates.
func HasEvidence(m Metadata) bool {
	for _, field := range [...]AttributionValue{m.Provider, m.Model, m.Variant, m.Role, m.ProtocolRole} {
		if field.Status == Observed || field.Status == Invalid || field.Status == Conflict || len(field.Candidates) != 0 {
			return true
		}
	}
	return false
}
