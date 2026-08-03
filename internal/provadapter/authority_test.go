package provadapter

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/pkg/protocol/portable"
)

// TestAuthorityID_NonAliasing proves that authorities of the SAME actor over
// different kinds, slots, tasks, or assignment episodes never collapse to one
// OperationAuthorityID. Each distinct logical authority must map to a distinct key.
func TestAuthorityID_NonAliasing(t *testing.T) {
	agent := mustAgentRef(t, "aura-plugins--"+uuid.Must(uuid.NewV7()).String())

	taskA := mustTaskRef(t, "aura-plugins--"+uuid.Must(uuid.NewV7()).String())
	taskB := mustTaskRef(t, "aura-plugins--"+uuid.Must(uuid.NewV7()).String())
	slotOwner := mustRoleID(t, "owner-responsibility")
	slotReview := mustRoleID(t, "review-responsibility")
	asgP := mustAssignmentRef(t, "assignment-predecessor")
	asgS := mustAssignmentRef(t, "assignment-successor")

	keys := map[string]provenance.OperationAuthorityID{}
	add := func(name string, id provenance.OperationAuthorityID, err error) {
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for existing, v := range keys {
			if v == id {
				t.Fatalf("aliasing: %s and %s both produced authority key %q", name, existing, id)
			}
		}
		keys[name] = id
	}

	boot, err := BootstrapAuthorityID(agent)
	add("bootstrap", boot, err)

	// Same actor, same slot, same task, different assignment episode -> distinct.
	id1, err := AssignmentAuthorityID(asgP, slotOwner, taskA)
	add("asgP/owner/taskA", id1, err)
	id2, err := AssignmentAuthorityID(asgS, slotOwner, taskA)
	add("asgS/owner/taskA", id2, err)

	// Same assignment identity, different slot -> distinct.
	id3, err := AssignmentAuthorityID(asgP, slotReview, taskA)
	add("asgP/review/taskA", id3, err)

	// Same assignment + slot, different task -> distinct.
	id4, err := AssignmentAuthorityID(asgP, slotOwner, taskB)
	add("asgP/owner/taskB", id4, err)

	// Determinism: identical inputs reproduce the identical key.
	repeat, err := AssignmentAuthorityID(asgP, slotOwner, taskA)
	if err != nil {
		t.Fatalf("repeat: %v", err)
	}
	if repeat != id1 {
		t.Fatalf("non-deterministic authority key: %q != %q", repeat, id1)
	}
}

// TestAuthorityID_NoDelimiterAliasing proves the length-delimited encoding is
// injective: values whose naive concatenation would collide produce distinct keys.
func TestAuthorityID_NoDelimiterAliasing(t *testing.T) {
	slot := mustRoleID(t, "s")
	task := mustTaskRef(t, "aura-plugins--"+uuid.Must(uuid.NewV7()).String())

	// "a:1" + "b" vs "a" + "1:b" style boundary ambiguity.
	x, err := AssignmentAuthorityID(mustAssignmentRef(t, "a:1"), slot, task)
	if err != nil {
		t.Fatalf("x: %v", err)
	}
	y, err := AssignmentAuthorityID(mustAssignmentRef(t, "a"), mustRoleID(t, "1"), task)
	if err != nil {
		t.Fatalf("y: %v", err)
	}
	if x == y {
		t.Fatalf("delimiter aliasing: distinct authorities produced identical key %q", x)
	}
}

func TestAuthorityID_RejectsInvalid(t *testing.T) {
	if _, err := BootstrapAuthorityID(portable.AgentRef{}); err == nil {
		t.Fatalf("expected zero AgentRef rejection")
	}
	task := mustTaskRef(t, "aura-plugins--"+uuid.Must(uuid.NewV7()).String())
	if _, err := AssignmentAuthorityID(portable.AssignmentRef{}, mustRoleID(t, "s"), task); err == nil {
		t.Fatalf("expected zero AssignmentRef rejection")
	}
	if _, err := AssignmentAuthorityID(mustAssignmentRef(t, "a"), portable.RoleID{}, task); err == nil {
		t.Fatalf("expected zero RoleID rejection")
	}
	if _, err := AssignmentAuthorityID(mustAssignmentRef(t, "a"), mustRoleID(t, "s"), portable.TaskRef{}); err == nil {
		t.Fatalf("expected zero TaskRef rejection")
	}
}

// buildContinuation constructs a genuine ir.MutationContinuation with the given
// retained authority.
func buildContinuation(t *testing.T, authority ir.MutationAuthority) ir.MutationContinuation {
	t.Helper()
	scope, err := ir.NewRootScope("root")
	if err != nil {
		t.Fatalf("NewRootScope: %v", err)
	}
	snapshot, err := ir.SnapshotBindings(ir.NewRuntimeBindings(), scope)
	if err != nil {
		t.Fatalf("SnapshotBindings: %v", err)
	}
	op, err := ir.NewSemanticOperationID("pasture.op.test")
	if err != nil {
		t.Fatalf("NewSemanticOperationID: %v", err)
	}
	request, err := ir.NewResolvedMutationRequest(op, ir.SchemaID("pasture.schema.test"), []byte(`{"k":"v"}`))
	if err != nil {
		t.Fatalf("NewResolvedMutationRequest: %v", err)
	}
	command, err := ir.DigestCanonicalCommand([]byte(`{"k":"v"}`))
	if err != nil {
		t.Fatalf("DigestCanonicalCommand: %v", err)
	}
	ref := mustMutationRef(t, "pasture.mutation.test-1")
	c, err := ir.NewMutationContinuation(ref, authority, request, command, nil, snapshot)
	if err != nil {
		t.Fatalf("NewMutationContinuation: %v", err)
	}
	return c
}

// TestHistoricalActorID_Bootstrap proves a bootstrap continuation resolves its
// retained AgentRef directly to the historical ActorID.
func TestHistoricalActorID_Bootstrap(t *testing.T) {
	actor := provenance.ActorID{Namespace: "aura-plugins", UUID: uuid.Must(uuid.NewV7())}
	authority, err := ir.BootstrapActor(mustAgentRef(t, actor.String()))
	if err != nil {
		t.Fatalf("BootstrapActor: %v", err)
	}
	c := buildContinuation(t, authority)

	// The resolver must NOT be consulted for a bootstrap continuation.
	got, err := HistoricalActorID(c, func(portable.AssignmentRef) (provenance.ActorID, error) {
		t.Fatalf("assignment resolver called for a bootstrap continuation")
		return provenance.ActorID{}, nil
	})
	if err != nil {
		t.Fatalf("HistoricalActorID: %v", err)
	}
	if got != actor {
		t.Fatalf("historical actor drift: got %+v want %+v", got, actor)
	}
}

// TestHistoricalActorID_Assignment proves an assignment continuation resolves the
// RETAINED assignment reference through the supplied historical resolver — never a
// current owner — and threads exactly that reference to it.
func TestHistoricalActorID_Assignment(t *testing.T) {
	retained := mustAssignmentRef(t, "assignment-initiating")
	historical := provenance.ActorID{Namespace: "aura-plugins", UUID: uuid.Must(uuid.NewV7())}

	authority, err := ir.InitiatingAssignment(retained)
	if err != nil {
		t.Fatalf("InitiatingAssignment: %v", err)
	}
	c := buildContinuation(t, authority)

	var sawRef string
	got, err := HistoricalActorID(c, func(ref portable.AssignmentRef) (provenance.ActorID, error) {
		sawRef = ref.String()
		return historical, nil
	})
	if err != nil {
		t.Fatalf("HistoricalActorID: %v", err)
	}
	if sawRef != retained.String() {
		t.Fatalf("resolver saw %q, want the retained ref %q", sawRef, retained.String())
	}
	if got != historical {
		t.Fatalf("historical actor drift: got %+v want %+v", got, historical)
	}
}

// TestHistoricalActorID_AssignmentNoResolver proves an assignment continuation
// without a resolver is rejected (it must never fabricate an actor).
func TestHistoricalActorID_AssignmentNoResolver(t *testing.T) {
	authority, err := ir.InitiatingAssignment(mustAssignmentRef(t, "a"))
	if err != nil {
		t.Fatalf("InitiatingAssignment: %v", err)
	}
	c := buildContinuation(t, authority)
	if _, err := HistoricalActorID(c, nil); err == nil {
		t.Fatalf("expected rejection when no assignment resolver is supplied")
	}
}

// TestHistoricalActorID_ResolverError propagates the resolver's error.
func TestHistoricalActorID_ResolverError(t *testing.T) {
	authority, err := ir.InitiatingAssignment(mustAssignmentRef(t, "a"))
	if err != nil {
		t.Fatalf("InitiatingAssignment: %v", err)
	}
	c := buildContinuation(t, authority)
	sentinel := errors.New("historical occupant not found")
	if _, err := HistoricalActorID(c, func(portable.AssignmentRef) (provenance.ActorID, error) {
		return provenance.ActorID{}, sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped resolver error, got %v", err)
	}
}
