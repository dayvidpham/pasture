package provadapter

import (
	"fmt"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/pkg/protocol/portable"
)

// refs.go converts between the portable protocol identity domain
// (pkg/protocol/portable) and the Provenance durable identity domains. The
// conversions are total, side-effect-free, and typed: the Go type system alone
// prevents passing (say) an AssignmentRef where a TaskRef is expected, and each
// conversion re-validates through the portable constructor so a value that does
// not belong to the target domain is rejected rather than silently coerced.
//
// The portable package is deliberately dependency-free of Provenance (see its
// package doc and internal/codegen/ir's dependency_guard_test). Keeping the
// conversions HERE — in the adapter, which may depend on both — preserves that
// boundary: the compiler-side portable identities never transitively import a
// durable-store client.
//
// Wire-form correspondence. A portable identity's exact spelling IS its
// Provenance wire form:
//   - TaskRef  <-> TaskID           via the "namespace--uuid" wire form.
//   - AssignmentRef <-> AssignmentID (opaque episode identity string).
//   - RoleID   <-> AssignmentSlotID  (responsibility-slot name string).
//   - MutationRef <-> OperationID    (idempotency alternate-key string).
//   - AgentRef  -> ActorID           via the "namespace--uuid" wire form.
//
// NOTE (issue #14 text vs delivered surface): issue #14 names the role target
// "AssignmentRoleID". The delivered Provenance surface at main@7b3451a exposes
// the responsibility-slot domain as AssignmentSlotID (the single seeded slot is
// SlotOwnerResponsibility); there is no exported AssignmentRoleID type. This
// adapter therefore maps the portable RoleID onto AssignmentSlotID, the delivered
// role/slot domain. The mapping is 1:1 and lossless.

// TaskIDFromRef converts a portable TaskRef to a Provenance TaskID. The TaskRef's
// spelling must be a valid "namespace--uuid" wire form; a portable reference that
// is not a task identity (wrong domain) is rejected.
func TaskIDFromRef(ref portable.TaskRef) (provenance.TaskID, error) {
	if !ref.IsValid() {
		return provenance.TaskID{}, refError("task", "TaskRef", ref.String(),
			"the portable task reference is empty, whitespace-padded, or contains control characters",
			"construct it with portable.NewTaskRef before converting")
	}
	id, err := provenance.ParseTaskID(ref.String())
	if err != nil {
		return provenance.TaskID{}, refError("task", "TaskRef", ref.String(),
			"the portable task reference is not a Provenance \"namespace--uuid\" task identity",
			"supply a task reference in namespace--uuid form (e.g. from TaskRefFromID)")
	}
	return id, nil
}

// TaskRefFromID converts a Provenance TaskID back to a portable TaskRef. It
// round-trips exactly with TaskIDFromRef.
func TaskRefFromID(id provenance.TaskID) (portable.TaskRef, error) {
	ref, err := portable.NewTaskRef(id.String())
	if err != nil {
		return portable.TaskRef{}, refError("task", "TaskID", id.String(),
			"the Provenance task id does not render to a valid portable reference",
			"ensure the TaskID has a non-empty namespace and a valid UUID")
	}
	return ref, nil
}

// AssignmentIDFromRef converts a portable AssignmentRef to a Provenance
// AssignmentID (the opaque, transition-invariant responsibility-episode identity).
func AssignmentIDFromRef(ref portable.AssignmentRef) (provenance.AssignmentID, error) {
	if !ref.IsValid() {
		return "", refError("assignment", "AssignmentRef", ref.String(),
			"the portable assignment reference is empty, whitespace-padded, or contains control characters",
			"construct it with portable.NewAssignmentRef before converting")
	}
	return provenance.AssignmentID(ref.String()), nil
}

// AssignmentRefFromID converts a Provenance AssignmentID back to a portable
// AssignmentRef. It round-trips exactly with AssignmentIDFromRef.
func AssignmentRefFromID(id provenance.AssignmentID) (portable.AssignmentRef, error) {
	ref, err := portable.NewAssignmentRef(string(id))
	if err != nil {
		return portable.AssignmentRef{}, refError("assignment", "AssignmentID", string(id),
			"the Provenance assignment id is empty, whitespace-padded, or contains control characters",
			"supply a non-empty assignment id without surrounding whitespace")
	}
	return ref, nil
}

// SlotIDFromRoleID converts a portable RoleID to a Provenance AssignmentSlotID
// (the responsibility-slot domain; see the package NOTE on AssignmentRoleID).
func SlotIDFromRoleID(role portable.RoleID) (provenance.AssignmentSlotID, error) {
	if !role.IsValid() {
		return "", refError("role", "RoleID", role.String(),
			"the portable role identity is empty, whitespace-padded, or contains control characters",
			"construct it with portable.NewRoleID before converting")
	}
	return provenance.AssignmentSlotID(role.String()), nil
}

// RoleIDFromSlotID converts a Provenance AssignmentSlotID back to a portable
// RoleID. It round-trips exactly with SlotIDFromRoleID.
func RoleIDFromSlotID(slot provenance.AssignmentSlotID) (portable.RoleID, error) {
	role, err := portable.NewRoleID(string(slot))
	if err != nil {
		return portable.RoleID{}, refError("role", "AssignmentSlotID", string(slot),
			"the Provenance assignment slot id is empty, whitespace-padded, or contains control characters",
			"supply a non-empty slot id without surrounding whitespace")
	}
	return role, nil
}

// OperationIDFromRef converts a portable MutationRef to a Provenance OperationID
// (the caller-supplied idempotency alternate key). The MutationRef identifies one
// logical stateful invocation across retries; a stable ref maps to a stable
// OperationID, which is exactly what Apply's replay short-circuit keys on.
func OperationIDFromRef(ref portable.MutationRef) (provenance.OperationID, error) {
	if !ref.IsValid() {
		return "", refError("mutation", "MutationRef", ref.String(),
			"the portable mutation reference is empty, whitespace-padded, or contains control characters",
			"construct it with portable.NewMutationRef before converting")
	}
	return provenance.OperationID(ref.String()), nil
}

// MutationRefFromID converts a Provenance OperationID back to a portable
// MutationRef. It round-trips exactly with OperationIDFromRef.
func MutationRefFromID(op provenance.OperationID) (portable.MutationRef, error) {
	ref, err := portable.NewMutationRef(string(op))
	if err != nil {
		return portable.MutationRef{}, refError("mutation", "OperationID", string(op),
			"the Provenance operation id is empty, whitespace-padded, or contains control characters",
			"supply a non-empty operation id without surrounding whitespace")
	}
	return ref, nil
}

// ActorIDFromAgentRef converts a portable bootstrap AgentRef to a Provenance
// ActorID. The AgentRef's spelling must be a valid "namespace--uuid" wire form.
// This is the one-directional bootstrap-actor resolution the authority and
// continuation paths reuse; there is no reverse ActorID -> AgentRef helper
// because a live ActorID is not, by itself, a portable bootstrap-agent identity.
func ActorIDFromAgentRef(agent portable.AgentRef) (provenance.ActorID, error) {
	if !agent.IsValid() {
		return provenance.ActorID{}, refError("agent", "AgentRef", agent.String(),
			"the portable agent reference is empty, whitespace-padded, or contains control characters",
			"construct it with portable.NewAgentRef before converting")
	}
	id, err := provenance.ParseActorID(agent.String())
	if err != nil {
		return provenance.ActorID{}, refError("agent", "AgentRef", agent.String(),
			"the portable agent reference is not a Provenance \"namespace--uuid\" actor identity",
			"supply a bootstrap agent reference in namespace--uuid form")
	}
	return id, nil
}

// refError builds a six-part actionable conversion error naming the domain, the
// source type, the offending value, why it failed, and how to fix it.
func refError(domain, sourceType, value, why, fix string) error {
	return fmt.Errorf(
		"provadapter: cannot convert %s identity — what: the %s value %q is not a valid %s identity; "+
			"why: %s; where: internal/provadapter portable<->Provenance identity conversion; "+
			"when: at the pasture#14 adapter boundary before any store call; "+
			"impact: no Provenance read or write is attempted with a mis-domained identity; fix: %s",
		domain, sourceType, value, domain, why, fix)
}
