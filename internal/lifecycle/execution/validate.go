package execution

import (
	"errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

var invalidMetadata = errors.New("execution metadata is malformed or exceeds its bounds; restore the occurrence from a verified backup")

// ValidateMetadata checks a decoded attribution without exposing supplied evidence.
func ValidateMetadata(m Metadata) error {
	if !m.Kind.IsValid() {
		return invalidMetadata
	}
	for _, v := range []string{m.AgentID, m.TurnID, m.RequestID} {
		if v != "" && !ValidValue(v) {
			return invalidMetadata
		}
	}
	fields := []struct {
		value      AttributionValue
		dim        dimension
		vocabulary Vocabulary
	}{
		{m.Provider, providerDimension, ""}, {m.Model, modelDimension, ""},
		{m.Variant, variantDimension, ""}, {m.Role, roleDimension, NativeAgent},
		{m.ProtocolRole, protocolRoleDimension, PastureRole},
	}
	for _, f := range fields {
		v := f.value
		if !v.Status.IsValid() || !v.Source.IsValid() || v.Vocabulary != f.vocabulary || len(v.Candidates) > MaxCandidates {
			return invalidMetadata
		}
		prior := 0
		for _, c := range v.Candidates {
			rank := 0
			switch c.Source {
			case Host:
				rank = 1
			case Environment:
				rank = 2
			case ActorRegistry:
				rank = 3
			}
			if rank <= prior || !allowed(c.Source, f.dim, c.Field) || !ValidValue(c.Value) || (f.dim == protocolRoleDimension && !protocol.RoleId(c.Value).IsValid()) {
				return invalidMetadata
			}
			prior = rank
		}
		switch v.Status {
		case Observed:
			if !allowed(v.Source, f.dim, v.Field) || !ValidValue(v.Value) || (f.dim == protocolRoleDimension && !protocol.RoleId(v.Value).IsValid()) || v.Reason != "" || len(v.Candidates) != 0 {
				return invalidMetadata
			}
		case Invalid:
			if !allowed(v.Source, f.dim, v.Field) || v.Value != "" || v.Reason != RejectedInput {
				return invalidMetadata
			}
		case Conflict:
			if v.Source != None || v.Field != "" || v.Value != "" || (v.Reason != ValidDisagreement && v.Reason != ValidDisagreementWithRejectedInput) || len(v.Candidates) < 2 {
				return invalidMetadata
			}
			distinct := false
			for _, c := range v.Candidates[1:] {
				if c.Value != v.Candidates[0].Value {
					distinct = true
				}
			}
			if !distinct {
				return invalidMetadata
			}
		case Unavailable:
			if v.Source != None || v.Field != "" || v.Value != "" || (v.Reason != NoEvidence && (f.dim != providerDimension || v.Reason != UnpairedProvider)) {
				return invalidMetadata
			}
			if (v.Reason == NoEvidence && len(v.Candidates) != 0) || (v.Reason == UnpairedProvider && len(v.Candidates) == 0) {
				return invalidMetadata
			}
		}
	}
	if m.Provider.Status == Observed && m.Model.Status != Observed {
		return invalidMetadata
	}
	return nil
}
