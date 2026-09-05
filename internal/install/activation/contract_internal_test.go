package activation

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func requireTypedActivationIndex(map[cell.Extension]ComponentActivation) {}

// compileTimeActivationIndexType intentionally reaches the concrete field so a
// regression to string keys fails compilation rather than only a runtime test.
func compileTimeActivationIndexType(activations ExhaustiveComponentActivations) {
	requireTypedActivationIndex(activations.byAxis)
}

func TestActivationIndexAndPublicLookupUseTypedExtensions(t *testing.T) {
	probe, err := NewCommandSchema("claude", "--version")
	if err != nil {
		t.Fatal(err)
	}
	native, err := NewNativePlugin("pasture", probe)
	if err != nil {
		t.Fatal(err)
	}

	extensions := []cell.Extension{cell.SkillsAxis(), cell.AgentsAxis(), cell.HooksAxis()}
	activations := make([]ComponentActivation, 0, len(extensions))
	for _, extension := range extensions {
		coordinate, err := cell.New(ir.HarnessClaudeCode, extension)
		if err != nil {
			t.Fatal(err)
		}
		activation, err := NewComponentActivation(coordinate, native)
		if err != nil {
			t.Fatal(err)
		}
		activations = append(activations, activation)
	}
	exhaustive, err := NewExhaustiveComponentActivations(activations[0], activations[1], activations[2])
	if err != nil {
		t.Fatal(err)
	}
	compileTimeActivationIndexType(exhaustive)

	host, err := runtime.ParseHostVersion("2.1.261")
	if err != nil {
		t.Fatal(err)
	}
	constraint, err := runtime.NewExactVersion(host)
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewActivationContractID("claude-code/activation@2.1.261")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := NewActivationContract(id, ir.HarnessClaudeCode, constraint, probe, exhaustive)
	if err != nil {
		t.Fatal(err)
	}

	for _, extension := range extensions {
		coordinate, err := cell.New(ir.HarnessClaudeCode, extension)
		if err != nil {
			t.Fatal(err)
		}
		descriptor, err := NewComponentDescriptor(coordinate)
		if err != nil {
			t.Fatal(err)
		}
		activation, err := LookupComponentActivation(contract, descriptor)
		if err != nil {
			t.Fatalf("lookup %s: %v", extension, err)
		}
		if got := activation.Cell().Extension(); got != extension {
			t.Errorf("lookup %s returned extension %s", extension, got)
		}
	}
}
