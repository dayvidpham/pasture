package execution

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

func (s Status) IsValid() bool {
	return s == Observed || s == Unavailable || s == Invalid || s == Conflict
}

func (s Source) IsValid() bool {
	return s == Host || s == Environment || s == ActorRegistry || s == None
}

func (k Kind) IsValid() bool {
	return k == Primary || k == Compaction || k == Title || k == Generate || k == Selection || k == Child
}

func (v Vocabulary) IsValid() bool { return v == NativeAgent || v == PastureRole }

func (r Reason) IsValid() bool {
	return r == RejectedInput || r == ValidDisagreement || r == ValidDisagreementWithRejectedInput || r == UnpairedProvider || r == NoEvidence
}

// ValidValue checks byte bounds without rewriting evidence. A valid value need
// not name a catalogued model or a known native agent.
func ValidValue(value string) bool {
	if value == "" || len(value) > MaxValueBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type dimension uint8

const (
	providerDimension dimension = iota
	modelDimension
	variantDimension
	roleDimension
	protocolRoleDimension
)

func allowed(source Source, dim dimension, member Member) bool {
	switch source {
	case Host:
		switch dim {
		case providerDimension:
			return member == HostProvider
		case modelDimension:
			return member == HostModelID || member == HostModel || member == HostToModel
		case variantDimension:
			return member == HostVariant
		case roleDimension:
			return member == HostAgent || member == HostAgentType
		}
	case Environment:
		switch dim {
		case providerDimension:
			return member == EnvironmentProvider
		case modelDimension:
			return member == EnvironmentModel
		case protocolRoleDimension:
			return member == EnvironmentRole
		}
	case ActorRegistry:
		switch dim {
		case providerDimension:
			return member == RegistryProvider
		case modelDimension:
			return member == RegistryModel
		case protocolRoleDimension:
			return member == RegistryRole
		}
	}
	return false
}

type observation struct {
	candidate Candidate
	present   bool
	valid     bool
	eligible  bool
}

// Normalize constructs evidence using fixed H/E/R priority. Structural input
// errors refuse construction with content-free diagnostics; malformed optional
// values instead produce Invalid fields. Neither path retains rejected raw text.
// Scope is native metadata, not an authority or parent-session binding.
func Normalize(in Inputs) (Metadata, error) {
	if !in.Kind.IsValid() {
		return Metadata{}, errors.New("execution normalization: unsupported kind before construction; use a supported execution kind")
	}
	for _, value := range [...]string{in.Scope.AgentID, in.Scope.TurnID, in.Scope.RequestID} {
		if value != "" && !ValidValue(value) {
			return Metadata{}, errors.New("execution normalization: invalid native scope before construction; supply bounded UTF-8 coordinates without controls or padding")
		}
	}
	declarations := [MaxCandidates]Declaration{in.Host, in.Environment, in.ActorRegistry}
	sources := [MaxCandidates]Source{Host, Environment, ActorRegistry}
	var fields [5][MaxCandidates]observation
	for i, d := range declarations {
		for dim, raw := range [...]Raw{d.Provider, d.Model, d.Variant, d.Role, d.ProtocolRole} {
			if !raw.Present {
				if raw.Value != "" || raw.Member != "" {
					return Metadata{}, errors.New("execution normalization: absent member carries data before construction; mark supplied evidence present or clear its ingress value and member")
				}
				continue
			}
			if !allowed(sources[i], dimension(dim), raw.Member) {
				return Metadata{}, errors.New("execution normalization: unsupported source member before construction; use the allowlisted member for this source and field")
			}
			o := observation{present: true, candidate: Candidate{Source: sources[i], Field: raw.Member}}
			o.valid = ValidValue(raw.Value) && (dimension(dim) != protocolRoleDimension || protocol.RoleId(raw.Value).IsValid())
			o.eligible = o.valid
			if o.valid {
				o.candidate.Value = raw.Value
			}
			fields[dim][i] = o
		}
	}
	model := normalizeField(fields[modelDimension], false)
	for i := range sources {
		m := fields[modelDimension][i]
		fields[providerDimension][i].eligible = model.Status == Observed && m.valid && m.candidate.Value == model.Value
	}
	role := normalizeField(fields[roleDimension], false)
	role.Vocabulary = NativeAgent
	protocolRole := normalizeField(fields[protocolRoleDimension], false)
	protocolRole.Vocabulary = PastureRole
	return Metadata{
		Kind: in.Kind, Scope: in.Scope, Model: model,
		Provider: normalizeField(fields[providerDimension], true),
		Variant:  normalizeField(fields[variantDimension], false),
		Role:     role, ProtocolRole: protocolRole,
	}, nil
}

func normalizeField(observations [MaxCandidates]observation, provider bool) AttributionValue {
	result := AttributionValue{Status: Unavailable, Source: None, Reason: NoEvidence}
	var winner, rejected *observation
	var disagree bool
	for i := range observations {
		o := &observations[i]
		if !o.present {
			continue
		}
		if !o.valid {
			if rejected == nil {
				rejected = o
			}
			continue
		}
		result.Candidates = append(result.Candidates, o.candidate)
		if o.eligible {
			if winner == nil {
				winner = o
			} else if winner.candidate.Value != o.candidate.Value {
				disagree = true
			}
		}
	}
	switch {
	case disagree:
		result.Status, result.Reason = Conflict, ValidDisagreement
		if rejected != nil {
			result.Reason = ValidDisagreementWithRejectedInput
		}
	case rejected != nil:
		result.Status, result.Reason = Invalid, RejectedInput
		result.Source, result.Field = rejected.candidate.Source, rejected.candidate.Field
	case winner != nil:
		result.Status, result.Source, result.Field, result.Value = Observed, winner.candidate.Source, winner.candidate.Field, winner.candidate.Value
		result.Reason, result.Candidates = "", nil
	case provider && len(result.Candidates) != 0:
		result.Reason = UnpairedProvider
	}
	return result
}
