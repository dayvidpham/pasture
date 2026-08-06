package inventory

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// swapRows temporarily replaces the generated-projection slices with test
// fixtures and restores them on cleanup. It lets these white-box tests exercise
// the real Table() assembly/validation path against crafted rows regardless of
// what the generated *.gen.go files contribute — the validation logic is
// production code, not a test-only export.
func swapRows(t *testing.T, cmd, life, native []Row) {
	t.Helper()
	oc, ol, on := commandRows, lifecycleEventRows, nativeFunctionRows
	commandRows, lifecycleEventRows, nativeFunctionRows = cmd, life, native
	t.Cleanup(func() {
		commandRows, lifecycleEventRows, nativeFunctionRows = oc, ol, on
	})
}

// TestTableRejectsDuplicateKey proves the uniqueness invariant: two generated
// sources contributing the same (harness, kind, id) coordinate must fail with
// an actionable duplicate error. This is the guard that makes drift between two
// generators an immediate build failure rather than a silently doubled table.
func TestTableRejectsDuplicateKey(t *testing.T) {
	dup := Row{Key{Harness: ir.HarnessClaudeCode, Kind: KindCommand, ID: "cmd-work-impl"}}
	// Same coordinate emitted by two different generated projections.
	swapRows(t, []Row{dup}, []Row{dup}, nil)

	_, err := Table()
	if err == nil {
		t.Fatalf("Table() accepted a duplicate (harness, kind, id) coordinate; the uniqueness invariant must reject it")
	}
	if !strings.Contains(err.Error(), "duplicate row key") {
		t.Fatalf("duplicate error is not actionable about the collision; got: %v", err)
	}
	if !strings.Contains(err.Error(), "cmd-work-impl") {
		t.Fatalf("duplicate error does not name the collided coordinate id; got: %v", err)
	}
}

// TestTableRejectsOutOfRangeKind proves the closed-axis invariant: a generated
// row whose Kind is not one of the closed set fails the build. This catches a
// generated projection drifting from the {command, lifecycle-event,
// native-function} vocabulary.
func TestTableRejectsOutOfRangeKind(t *testing.T) {
	bad := Row{Key{Harness: ir.HarnessCodex, Kind: Kind("mystery-axis"), ID: "x"}}
	swapRows(t, nil, nil, []Row{bad})

	_, err := Table()
	if err == nil {
		t.Fatalf("Table() accepted an out-of-range Kind; the closed-axis invariant must reject it")
	}
	if !strings.Contains(err.Error(), "out-of-range Kind") {
		t.Fatalf("closed-axis error is not actionable; got: %v", err)
	}
}

// TestTableIsDeterministicallySorted proves the table is ordered by
// (Harness, Kind, ID) regardless of the order the generated projections
// contribute their rows, so committed artifacts are byte-stable across runs.
func TestTableIsDeterministicallySorted(t *testing.T) {
	late := Row{Key{Harness: ir.HarnessCodex, Kind: KindNativeFunction, ID: "request-input"}}
	early := Row{Key{Harness: ir.HarnessClaudeCode, Kind: KindCommand, ID: "cmd-explore"}}
	// Contribute them out of final order (native first, command second).
	swapRows(t, []Row{}, []Row{}, []Row{late})
	commandRows = []Row{early}

	got, err := Table()
	if err != nil {
		t.Fatalf("Table(): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}
	if got[0].Key != early.Key || got[1].Key != late.Key {
		t.Fatalf("rows not sorted by (Harness, Kind, ID): got %+v then %+v", got[0].Key, got[1].Key)
	}
}

// TestKindIsValid pins the closed axis set.
func TestKindIsValid(t *testing.T) {
	for _, k := range []Kind{KindCommand, KindLifecycleEvent, KindNativeFunction} {
		if !k.IsValid() {
			t.Errorf("Kind %q must be valid", k)
		}
	}
	if Kind("").IsValid() || Kind("command ").IsValid() {
		t.Errorf("only the three closed axes may be valid")
	}
}
