package execution

// Invocation is the bounded, untrusted declaration carried by a hook. The
// member names are fixed by the receiver, never supplied by the caller.
type Invocation struct {
	Provider *string `json:"provider,omitempty"`
	Model    *string `json:"model,omitempty"`
	Role     *string `json:"role,omitempty"`
}

// BoundedInvocation preserves presence of malformed input without retaining
// its raw bytes. Empty strings deliberately normalize as invalid.
func BoundedInvocation(provider, model, role *string) Invocation {
	bounded := func(value *string) *string {
		if value == nil {
			return nil
		}
		v := *value
		if !ValidValue(v) {
			v = ""
		}
		return &v
	}
	return Invocation{Provider: bounded(provider), Model: bounded(model), Role: bounded(role)}
}

func (i Invocation) Declaration() Declaration {
	raw := func(value *string, member Member) Raw {
		if value == nil {
			return Raw{}
		}
		return Raw{Present: true, Member: member, Value: *value}
	}
	i = BoundedInvocation(i.Provider, i.Model, i.Role)
	return Declaration{Provider: raw(i.Provider, EnvironmentProvider), Model: raw(i.Model, EnvironmentModel), ProtocolRole: raw(i.Role, EnvironmentRole)}
}
