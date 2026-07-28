package protocol_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

type taskAssignmentTransferSurface interface {
	TransferTaskAssignment(context.Context, protocol.TransferTaskAssignmentRequest) (protocol.TransferTaskAssignmentResult, error)
}

var _ taskAssignmentTransferSurface = (protocol.TaskTracker)(nil)

func TestTaskAssignmentTransferPublicDTOsContainOnlySemanticFields(t *testing.T) {
	t.Parallel()

	assertStructFields(t, reflect.TypeFor[protocol.TransferTaskAssignmentRequest](), []struct {
		name   string
		typeOf reflect.Type
	}{
		{"TaskID", reflect.TypeFor[provenance.TaskID]()},
		{"Slot", reflect.TypeFor[provenance.AssignmentSlotID]()},
		{"NextAssignmentID", reflect.TypeFor[provenance.AssignmentID]()},
		{"ActorID", reflect.TypeFor[provenance.ActorID]()},
		{"NextOccupant", reflect.TypeFor[provenance.ActorID]()},
	})
	assertStructFields(t, reflect.TypeFor[protocol.TaskAssignmentState](), []struct {
		name   string
		typeOf reflect.Type
	}{
		{"TaskID", reflect.TypeFor[provenance.TaskID]()},
		{"Slot", reflect.TypeFor[provenance.AssignmentSlotID]()},
		{"AssignmentID", reflect.TypeFor[provenance.AssignmentID]()},
		{"Occupant", reflect.TypeFor[provenance.ActorID]()},
	})
	assertStructFields(t, reflect.TypeFor[protocol.TransferTaskAssignmentResult](), []struct {
		name   string
		typeOf reflect.Type
	}{
		{"Previous", reflect.TypeFor[protocol.TaskAssignmentState]()},
		{"Next", reflect.TypeFor[protocol.TaskAssignmentState]()},
		{"Replayed", reflect.TypeFor[bool]()},
	})

	for _, dto := range []reflect.Type{
		reflect.TypeFor[protocol.TransferTaskAssignmentRequest](),
		reflect.TypeFor[protocol.TaskAssignmentState](),
		reflect.TypeFor[protocol.TransferTaskAssignmentResult](),
	} {
		for index := range dto.NumField() {
			field := dto.Field(index)
			for _, forbidden := range []string{"journal", "authority", "operation", "sql", "dbos", "storage"} {
				if strings.Contains(strings.ToLower(field.Name), forbidden) {
					t.Errorf("%s.%s leaks %q into the public task-assignment contract", dto.Name(), field.Name, forbidden)
				}
			}
			for _, forbidden := range []reflect.Type{
				reflect.TypeFor[provenance.JournalID](),
				reflect.TypeFor[provenance.OperationID](),
				reflect.TypeFor[provenance.OperationAuthorityID](),
			} {
				if field.Type == forbidden {
					t.Errorf("%s.%s leaks %s into the public task-assignment contract", dto.Name(), field.Name, field.Type)
				}
			}
		}
	}
}

func TestTaskAssignmentTransferErrorDoesNotExposePersistenceDetails(t *testing.T) {
	t.Parallel()

	typeOf := reflect.TypeFor[protocol.TaskAssignmentTransferError]()
	if typeOf.NumField() != 2 || typeOf.Field(0).Name != "Kind" || typeOf.Field(0).Type != reflect.TypeFor[protocol.TaskAssignmentTransferErrorKind]() || typeOf.Field(1).PkgPath == "" {
		t.Fatalf("TaskAssignmentTransferError fields = %v, want exported Kind and an unexported cause", typeOf)
	}
}

func assertStructFields(t *testing.T, got reflect.Type, want []struct {
	name   string
	typeOf reflect.Type
}) {
	t.Helper()
	if got.NumField() != len(want) {
		t.Fatalf("%s field count = %d, want %d", got.Name(), got.NumField(), len(want))
	}
	for index, expected := range want {
		field := got.Field(index)
		if field.Name != expected.name || field.Type != expected.typeOf {
			t.Errorf("%s field %d = %s %s, want %s %s", got.Name(), index, field.Name, field.Type, expected.name, expected.typeOf)
		}
	}
}
