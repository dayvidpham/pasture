package provadapter

import (
	"fmt"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/pkg/protocol/portable"
)

// authority.go derives a Provenance OperationAuthorityID (the opaque alternate
// key carried as an operation's authority) from a portable authority identity,
// and resolves a mutation continuation's retained initiating identity to the
// historical ActorID used for committed replay/lookup.
//
// Non-aliasing is the core invariant (pasture#14). A single actor may hold many
// authorities over its lifetime: a bootstrap root, and any number of assignment
// episodes over different responsibility slots and different tasks. Provenance
// keys per-effect authorization on the authority, so if two DISTINCT authorities
// of the same actor collapsed to one OperationAuthorityID, an effect authorized
// under one could be replayed under the other. The derivations below therefore
// encode the authority KIND plus, for an assignment, its (assignment, slot, task)
// triple into the key using length-delimited fields, which is injective: no
// choice of field values for one authority can spell the key of a different one.

// authorityKeyVersion namespaces the derivation so a future encoding change is a
// new, non-colliding key space rather than a silent reinterpretation.
const authorityKeyVersion = "pasture.authority.v1"

// BootstrapAuthorityID derives the non-aliasing OperationAuthorityID for a
// portable bootstrap-agent authority (the genesis/system root an operation
// executes under). It shares no key space with any assignment authority.
func BootstrapAuthorityID(agent portable.AgentRef) (provenance.OperationAuthorityID, error) {
	if !agent.IsValid() {
		return "", authorityError("bootstrap", "the bootstrap agent reference is zero or invalid",
			"construct it with portable.NewAgentRef")
	}
	return provenance.OperationAuthorityID(
		authorityKeyVersion + ".bootstrap:" + lenField(agent.String())), nil
}

// AssignmentAuthorityID derives the non-aliasing OperationAuthorityID for a
// portable assignment authority, preserving the assignment episode identity, its
// responsibility slot, and its task. Two authorities of the same actor over a
// different slot, a different task, or a different (predecessor/successor)
// assignment yield distinct keys and therefore cannot alias.
func AssignmentAuthorityID(assignment portable.AssignmentRef, slot portable.RoleID, task portable.TaskRef) (provenance.OperationAuthorityID, error) {
	if !assignment.IsValid() {
		return "", authorityError("assignment", "the assignment reference is zero or invalid",
			"construct it with portable.NewAssignmentRef")
	}
	if !slot.IsValid() {
		return "", authorityError("assignment", "the responsibility slot (RoleID) is zero or invalid",
			"construct it with portable.NewRoleID")
	}
	if !task.IsValid() {
		return "", authorityError("assignment", "the task reference is zero or invalid",
			"construct it with portable.NewTaskRef")
	}
	return provenance.OperationAuthorityID(
		authorityKeyVersion + ".assignment:" +
			lenField(assignment.String()) +
			lenField(slot.String()) +
			lenField(task.String())), nil
}

// HistoricalActorID resolves a completed MutationContinuation's retained
// initiating identity to the historical ActorID used for its committed
// replay/lookup. It uses the identity the continuation RETAINED at initiation and
// never re-resolves whichever assignment happens to be current later: a bootstrap
// continuation resolves its retained AgentRef directly; an assignment
// continuation resolves its retained AssignmentRef through the caller-supplied
// resolveAssignment (the historical-occupant lookup), which #43 backs with a
// point-in-time store query keyed on the retained ref — not the live owner.
func HistoricalActorID(
	c ir.MutationContinuation,
	resolveAssignment func(portable.AssignmentRef) (provenance.ActorID, error),
) (provenance.ActorID, error) {
	if !c.IsValid() {
		return provenance.ActorID{}, authorityError("continuation",
			"the mutation continuation is zero or invalid",
			"construct it with ir.NewMutationContinuation")
	}
	authority := c.Authority()
	if agent, ok := ir.BootstrapAuthority(authority); ok {
		actor, err := ActorIDFromAgentRef(agent)
		if err != nil {
			return provenance.ActorID{}, fmt.Errorf(
				"provadapter: cannot resolve historical actor for a bootstrap continuation: %w", err)
		}
		return actor, nil
	}
	if assignment, ok := ir.AssignmentAuthority(authority); ok {
		if resolveAssignment == nil {
			return provenance.ActorID{}, authorityError("continuation",
				"an assignment continuation needs a historical-occupant resolver but none was supplied",
				"pass the #43 point-in-time resolver that maps the RETAINED assignment reference to the actor "+
					"that held it, never the current owner")
		}
		actor, err := resolveAssignment(assignment)
		if err != nil {
			return provenance.ActorID{}, fmt.Errorf(
				"provadapter: cannot resolve historical actor for assignment continuation %q: %w",
				assignment.String(), err)
		}
		return actor, nil
	}
	return provenance.ActorID{}, authorityError("continuation",
		"the continuation's retained authority is neither a bootstrap agent nor an assignment",
		"construct the authority with ir.BootstrapActor or ir.InitiatingAssignment")
}

// lenField encodes one field as "<byte-length>:<value>:" so a concatenation of
// fields is injective (unambiguously decodable), preventing a delimiter embedded
// in a value from spelling a neighbouring field boundary and aliasing two
// distinct authorities onto one key.
func lenField(value string) string {
	return fmt.Sprintf("%d:%s:", len(value), value)
}

func authorityError(kind, why, fix string) error {
	return fmt.Errorf(
		"provadapter: cannot derive %s authority — what: %s; why: a Provenance operation authority "+
			"must be a valid, non-aliasing key; where: internal/provadapter authority derivation; "+
			"when: at the pasture#14 adapter boundary before any store call; impact: no operation is "+
			"authorized under an invalid or ambiguous authority; fix: %s",
		kind, why, fix)
}
