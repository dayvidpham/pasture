package provadapter

import (
	"testing"

	"github.com/google/uuid"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/pkg/protocol/portable"
)

// mustTaskRef / mustAssignmentRef etc. construct valid portable refs for tests.
func mustTaskRef(t *testing.T, v string) portable.TaskRef {
	t.Helper()
	r, err := portable.NewTaskRef(v)
	if err != nil {
		t.Fatalf("NewTaskRef(%q): %v", v, err)
	}
	return r
}

func mustAssignmentRef(t *testing.T, v string) portable.AssignmentRef {
	t.Helper()
	r, err := portable.NewAssignmentRef(v)
	if err != nil {
		t.Fatalf("NewAssignmentRef(%q): %v", v, err)
	}
	return r
}

func mustRoleID(t *testing.T, v string) portable.RoleID {
	t.Helper()
	r, err := portable.NewRoleID(v)
	if err != nil {
		t.Fatalf("NewRoleID(%q): %v", v, err)
	}
	return r
}

func mustMutationRef(t *testing.T, v string) portable.MutationRef {
	t.Helper()
	r, err := portable.NewMutationRef(v)
	if err != nil {
		t.Fatalf("NewMutationRef(%q): %v", v, err)
	}
	return r
}

func mustAgentRef(t *testing.T, v string) portable.AgentRef {
	t.Helper()
	r, err := portable.NewAgentRef(v)
	if err != nil {
		t.Fatalf("NewAgentRef(%q): %v", v, err)
	}
	return r
}

func TestTaskRefRoundTrip(t *testing.T) {
	id := provenance.TaskID{Namespace: "aura-plugins", UUID: uuid.Must(uuid.NewV7())}

	ref, err := TaskRefFromID(id)
	if err != nil {
		t.Fatalf("TaskRefFromID: %v", err)
	}
	if ref.String() != id.String() {
		t.Fatalf("wire form drift: ref=%q id=%q", ref.String(), id.String())
	}
	back, err := TaskIDFromRef(ref)
	if err != nil {
		t.Fatalf("TaskIDFromRef: %v", err)
	}
	if back != id {
		t.Fatalf("round-trip drift: got %+v want %+v", back, id)
	}
}

// TestTaskIDFromRef_WrongDomain proves a portable ref that is not a
// namespace--uuid task identity is rejected instead of coerced.
func TestTaskIDFromRef_WrongDomain(t *testing.T) {
	// A syntactically valid portable ref that is NOT a namespace--uuid task id.
	ref := mustTaskRef(t, "not-a-task-identity")
	if _, err := TaskIDFromRef(ref); err == nil {
		t.Fatalf("expected wrong-domain rejection for %q", ref.String())
	}
	// The zero TaskRef is rejected too.
	if _, err := TaskIDFromRef(portable.TaskRef{}); err == nil {
		t.Fatalf("expected zero-value TaskRef rejection")
	}
}

func TestAssignmentRefRoundTrip(t *testing.T) {
	cases := []string{"assignment.worker.s3-2", "ns--" + uuid.Must(uuid.NewV7()).String(), "any-opaque-episode"}
	for _, v := range cases {
		ref := mustAssignmentRef(t, v)
		id, err := AssignmentIDFromRef(ref)
		if err != nil {
			t.Fatalf("AssignmentIDFromRef(%q): %v", v, err)
		}
		if string(id) != v {
			t.Fatalf("AssignmentID drift: got %q want %q", string(id), v)
		}
		back, err := AssignmentRefFromID(id)
		if err != nil {
			t.Fatalf("AssignmentRefFromID(%q): %v", v, err)
		}
		if back.String() != v {
			t.Fatalf("round-trip drift: got %q want %q", back.String(), v)
		}
	}
	// Empty domains are rejected on both directions.
	if _, err := AssignmentIDFromRef(portable.AssignmentRef{}); err == nil {
		t.Fatalf("expected zero AssignmentRef rejection")
	}
	if _, err := AssignmentRefFromID(provenance.AssignmentID("")); err == nil {
		t.Fatalf("expected empty AssignmentID rejection")
	}
}

func TestRoleIDSlotRoundTrip(t *testing.T) {
	role := mustRoleID(t, "owner-responsibility")
	slot, err := SlotIDFromRoleID(role)
	if err != nil {
		t.Fatalf("SlotIDFromRoleID: %v", err)
	}
	if slot != provenance.SlotOwnerResponsibility {
		t.Fatalf("slot drift: got %q want %q", slot, provenance.SlotOwnerResponsibility)
	}
	back, err := RoleIDFromSlotID(slot)
	if err != nil {
		t.Fatalf("RoleIDFromSlotID: %v", err)
	}
	if back.String() != role.String() {
		t.Fatalf("round-trip drift: got %q want %q", back.String(), role.String())
	}
	if _, err := SlotIDFromRoleID(portable.RoleID{}); err == nil {
		t.Fatalf("expected zero RoleID rejection")
	}
	if _, err := RoleIDFromSlotID(provenance.AssignmentSlotID("")); err == nil {
		t.Fatalf("expected empty AssignmentSlotID rejection")
	}
}

func TestMutationRefOperationRoundTrip(t *testing.T) {
	ref := mustMutationRef(t, "pasture.mutation.close--task-1")
	op, err := OperationIDFromRef(ref)
	if err != nil {
		t.Fatalf("OperationIDFromRef: %v", err)
	}
	if string(op) != ref.String() {
		t.Fatalf("OperationID drift: got %q want %q", string(op), ref.String())
	}
	back, err := MutationRefFromID(op)
	if err != nil {
		t.Fatalf("MutationRefFromID: %v", err)
	}
	if back.String() != ref.String() {
		t.Fatalf("round-trip drift: got %q want %q", back.String(), ref.String())
	}
	if _, err := OperationIDFromRef(portable.MutationRef{}); err == nil {
		t.Fatalf("expected zero MutationRef rejection")
	}
	if _, err := MutationRefFromID(provenance.OperationID("")); err == nil {
		t.Fatalf("expected empty OperationID rejection")
	}
}

func TestActorIDFromAgentRef(t *testing.T) {
	actor := provenance.ActorID{Namespace: "aura-plugins", UUID: uuid.Must(uuid.NewV7())}
	ref := mustAgentRef(t, actor.String())
	got, err := ActorIDFromAgentRef(ref)
	if err != nil {
		t.Fatalf("ActorIDFromAgentRef: %v", err)
	}
	if got != actor {
		t.Fatalf("actor drift: got %+v want %+v", got, actor)
	}
	// A syntactically valid portable ref that is not namespace--uuid is rejected.
	if _, err := ActorIDFromAgentRef(mustAgentRef(t, "bootstrap-without-uuid")); err == nil {
		t.Fatalf("expected wrong-domain AgentRef rejection")
	}
	if _, err := ActorIDFromAgentRef(portable.AgentRef{}); err == nil {
		t.Fatalf("expected zero AgentRef rejection")
	}
}
