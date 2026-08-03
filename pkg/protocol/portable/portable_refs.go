// Package portable defines Pasture's portable cross-boundary identity types.
// It is deliberately dependency-free of pkg/protocol's TaskTracker facade (and
// therefore of github.com/dayvidpham/provenance): internal/codegen/ir compiles
// documents entirely in memory and must not pull in a durable-store client
// transitively through an unrelated identity type. See
// dependency_guard_test.go in internal/codegen/ir for the enforced boundary.
package portable

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// AssignmentRef is a portable logical assignment identity used by generated
// protocol documents. It is deliberately distinct from any durable-store ID.
type AssignmentRef struct{ value string }

// TaskRef is a portable logical task identity used by generated documents.
type TaskRef struct{ value string }

// RoleID is the portable role domain used by assignment continuations. It is
// separate from the legacy schema RoleId enum and from backend assignment roles.
type RoleID struct{ value string }

// MutationRef identifies one logical stateful invocation across retries. It is
// not a semantic operation ID or a durable-store idempotency key.
type MutationRef struct{ value string }

// AgentRef identifies a registered bootstrap agent at the portable IR boundary.
type AgentRef struct{ value string }

// NewAssignmentRef validates and constructs a portable assignment reference.
func NewAssignmentRef(value string) (AssignmentRef, error) {
	value, err := validatePortableRef("assignment reference", value)
	return AssignmentRef{value: value}, err
}

// NewTaskRef validates and constructs a portable task reference.
func NewTaskRef(value string) (TaskRef, error) {
	value, err := validatePortableRef("task reference", value)
	return TaskRef{value: value}, err
}

// NewRoleID validates and constructs a portable role identity.
func NewRoleID(value string) (RoleID, error) {
	value, err := validatePortableRef("role identity", value)
	return RoleID{value: value}, err
}

// NewMutationRef validates and constructs a portable logical mutation reference.
func NewMutationRef(value string) (MutationRef, error) {
	value, err := validatePortableRef("mutation reference", value)
	return MutationRef{value: value}, err
}

// NewAgentRef validates and constructs a portable bootstrap-agent reference.
func NewAgentRef(value string) (AgentRef, error) {
	value, err := validatePortableRef("agent reference", value)
	return AgentRef{value: value}, err
}

func validatePortableRef(domain, value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf(
			"what: %s is not valid UTF-8; why: portable identities must round-trip through JSON without replacement; where: protocol %s construction; phase: semantic validation; impact: the identity cannot cross a runtime boundary; fix: supply a valid UTF-8 non-empty identity",
			domain, domain,
		)
	}
	if value == "" || strings.TrimSpace(value) != value {
		return "", fmt.Errorf(
			"what: %s is empty or has surrounding whitespace; why: portable identities require an exact non-empty spelling; where: protocol %s construction; phase: semantic validation; impact: identity comparison would be ambiguous; fix: supply a non-empty identity without leading or trailing whitespace",
			domain, domain,
		)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", fmt.Errorf(
				"what: %s contains control character U+%04X; why: portable identities cannot contain control characters; where: protocol %s construction; phase: semantic validation; impact: the identity cannot be represented safely; fix: remove control characters from the identity",
				domain, r, domain,
			)
		}
	}
	return value, nil
}

func (r AssignmentRef) String() string { return r.value }
func (r TaskRef) String() string       { return r.value }
func (r RoleID) String() string        { return r.value }
func (r MutationRef) String() string   { return r.value }
func (r AgentRef) String() string      { return r.value }

func (r AssignmentRef) IsValid() bool {
	_, err := validatePortableRef("assignment reference", r.value)
	return err == nil
}
func (r TaskRef) IsValid() bool {
	_, err := validatePortableRef("task reference", r.value)
	return err == nil
}
func (r RoleID) IsValid() bool {
	_, err := validatePortableRef("role identity", r.value)
	return err == nil
}
func (r MutationRef) IsValid() bool {
	_, err := validatePortableRef("mutation reference", r.value)
	return err == nil
}
func (r AgentRef) IsValid() bool {
	_, err := validatePortableRef("agent reference", r.value)
	return err == nil
}

func marshalPortableRef(domain, value string) ([]byte, error) {
	if _, err := validatePortableRef(domain, value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func unmarshalPortableRef(domain string, data []byte) (string, error) {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", fmt.Errorf(
			"what: %s JSON is not a string; why: portable references use an exact JSON string; where: protocol %s decoding; phase: codec validation; impact: the reference cannot be reconstructed; fix: encode the reference as one JSON string: %w",
			domain, domain, err,
		)
	}
	return validatePortableRef(domain, value)
}

func (r AssignmentRef) MarshalJSON() ([]byte, error) {
	return marshalPortableRef("assignment reference", r.value)
}
func (r TaskRef) MarshalJSON() ([]byte, error) { return marshalPortableRef("task reference", r.value) }
func (r RoleID) MarshalJSON() ([]byte, error)  { return marshalPortableRef("role identity", r.value) }
func (r MutationRef) MarshalJSON() ([]byte, error) {
	return marshalPortableRef("mutation reference", r.value)
}
func (r AgentRef) MarshalJSON() ([]byte, error) {
	return marshalPortableRef("agent reference", r.value)
}

func (r *AssignmentRef) UnmarshalJSON(data []byte) error {
	value, err := unmarshalPortableRef("assignment reference", data)
	if err == nil {
		r.value = value
	}
	return err
}

func (r *TaskRef) UnmarshalJSON(data []byte) error {
	value, err := unmarshalPortableRef("task reference", data)
	if err == nil {
		r.value = value
	}
	return err
}

func (r *RoleID) UnmarshalJSON(data []byte) error {
	value, err := unmarshalPortableRef("role identity", data)
	if err == nil {
		r.value = value
	}
	return err
}

func (r *MutationRef) UnmarshalJSON(data []byte) error {
	value, err := unmarshalPortableRef("mutation reference", data)
	if err == nil {
		r.value = value
	}
	return err
}

func (r *AgentRef) UnmarshalJSON(data []byte) error {
	value, err := unmarshalPortableRef("agent reference", data)
	if err == nil {
		r.value = value
	}
	return err
}
