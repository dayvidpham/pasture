package gate_test

import (
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

func validContract(t *testing.T) ir.RuntimeContractID {
	t.Helper()
	contract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, registration.ClaudeCode2_1_261().Version)
	if err != nil {
		t.Fatalf("build runtime contract: %v", err)
	}
	return contract
}

func nonzeroContent() model.ContentIdentity {
	var c model.ContentIdentity
	c[0] = 0x11
	return c
}

// validIntent constructs a well-formed intent for each class, used by tests
// that then legalize it. It fails the test if construction refuses.
func validIntent(t *testing.T, class gate.WriteClass) gate.WriteIntent {
	t.Helper()
	var (
		intent  gate.WriteIntent
		refusal *gate.Refusal
	)
	switch class {
	case gate.WriteDeliveryReceipt:
		intent, refusal = gate.NewDeliveryIntent(validContract(t), 1)
	case gate.WriteDefinitionActivation:
		intent, refusal = gate.NewDefinitionActivationIntent(nonzeroContent())
	case gate.WriteLineageLinks:
		intent, refusal = gate.NewLineageIntent(ir.HarnessClaudeCode, 1)
	case gate.WriteDisclosure:
		intent, refusal = gate.NewDisclosureIntent(nonzeroContent())
	default:
		t.Fatalf("unhandled write class %s", class)
	}
	if refusal != nil {
		t.Fatalf("well-formed %s intent was refused: %v", class, refusal)
	}
	return intent
}

func allClasses() []gate.WriteClass {
	return []gate.WriteClass{gate.WriteDeliveryReceipt, gate.WriteDefinitionActivation, gate.WriteLineageLinks, gate.WriteDisclosure}
}

// TestConstructorsIssueIntentForValidCoordinates is a structural guard: each
// constructor returns an intent tagged with its class for well-formed input.
func TestConstructorsIssueIntentForValidCoordinates(t *testing.T) {
	t.Parallel()
	for _, class := range allClasses() {
		class := class
		t.Run(class.String(), func(t *testing.T) {
			t.Parallel()
			intent := validIntent(t, class)
			if intent.Class() != class {
				t.Fatalf("intent class = %s, want %s", intent.Class(), class)
			}
		})
	}
}

// TestConstructorsRejectMalformedCoordinates pins per-class well-formedness:
// every constructor refuses malformed coordinates with a typed *gate.Refusal
// whose reason is RefusalInvalidIntent. FAILS against the L1 stubs (which skip
// validation) until L3 implements the real constructors.
func TestConstructorsRejectMalformedCoordinates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		build func() (gate.WriteIntent, *gate.Refusal)
	}{
		{"delivery without contract", func() (gate.WriteIntent, *gate.Refusal) {
			return gate.NewDeliveryIntent(ir.RuntimeContractID{}, 1)
		}},
		{"delivery without event", func() (gate.WriteIntent, *gate.Refusal) {
			return gate.NewDeliveryIntent(validContract(t), 0)
		}},
		{"definition without content", func() (gate.WriteIntent, *gate.Refusal) {
			return gate.NewDefinitionActivationIntent(model.ContentIdentity{})
		}},
		{"lineage without host", func() (gate.WriteIntent, *gate.Refusal) {
			return gate.NewLineageIntent(ir.HarnessID("not-a-harness"), 1)
		}},
		{"lineage empty edge set", func() (gate.WriteIntent, *gate.Refusal) {
			return gate.NewLineageIntent(ir.HarnessClaudeCode, 0)
		}},
		{"lineage over the per-operation cap", func() (gate.WriteIntent, *gate.Refusal) {
			return gate.NewLineageIntent(ir.HarnessClaudeCode, gate.MaxLinksPerOperation+1)
		}},
		{"disclosure without scope", func() (gate.WriteIntent, *gate.Refusal) {
			return gate.NewDisclosureIntent(model.ContentIdentity{})
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			intent, refusal := tc.build()
			if refusal == nil {
				t.Fatalf("malformed coordinates were accepted, got intent %#v; want a typed *gate.Refusal", intent)
			}
			if refusal.Reason() != gate.RefusalInvalidIntent {
				t.Fatalf("refusal reason = %s, want %s", refusal.Reason(), gate.RefusalInvalidIntent)
			}
			assertActionable(t, refusal)
		})
	}
}

// TestLegalizeIssuesWarrantForEachClass is a structural guard: Legalize issues a
// valid warrant of the right class for every well-formed enumerated intent.
func TestLegalizeIssuesWarrantForEachClass(t *testing.T) {
	t.Parallel()
	for _, class := range allClasses() {
		class := class
		t.Run(class.String(), func(t *testing.T) {
			t.Parallel()
			warrant, refusal := gate.Legalize(validIntent(t, class))
			if refusal != nil {
				t.Fatalf("Legalize refused a well-formed %s intent: %v", class, refusal)
			}
			if !warrant.IsValid() {
				t.Fatalf("Legalize issued an invalid warrant for %s", class)
			}
			if warrant.Class() != class {
				t.Fatalf("warrant class = %s, want %s", warrant.Class(), class)
			}
		})
	}
}

// TestLegalizeRefusesUnenumeratedIntent pins that the zero-value / unenumerated
// intent cannot be legalized. FAILS against the L1 stub (which issues a warrant
// unconditionally) until L3.
func TestLegalizeRefusesUnenumeratedIntent(t *testing.T) {
	t.Parallel()
	warrant, refusal := gate.Legalize(gate.WriteIntent{})
	if refusal == nil {
		t.Fatal("Legalize accepted the zero-value intent; want a typed *gate.Refusal")
	}
	if refusal.Reason() != gate.RefusalUnknownClass {
		t.Fatalf("refusal reason = %s, want %s", refusal.Reason(), gate.RefusalUnknownClass)
	}
	if warrant.IsValid() {
		t.Fatal("Legalize issued a valid warrant for the zero-value intent")
	}
	assertActionable(t, refusal)
}

// TestAuthorizeAdmitsMatchingWarrant is a structural guard: a valid warrant
// authorizes a write of its own class.
func TestAuthorizeAdmitsMatchingWarrant(t *testing.T) {
	t.Parallel()
	for _, class := range allClasses() {
		class := class
		t.Run(class.String(), func(t *testing.T) {
			t.Parallel()
			warrant, refusal := gate.Legalize(validIntent(t, class))
			if refusal != nil {
				t.Fatalf("Legalize refused: %v", refusal)
			}
			if got := gate.Authorize(warrant, class); got != nil {
				t.Fatalf("Authorize refused a matching %s warrant: %v", class, got)
			}
		})
	}
}

// TestAuthorizeRefusesZeroWarrant pins that a commit surface refuses an ungated
// (zero-value) warrant. FAILS against the L1 stub (which authorizes everything)
// until L3.
func TestAuthorizeRefusesZeroWarrant(t *testing.T) {
	t.Parallel()
	refusal := gate.Authorize(gate.Warrant{}, gate.WriteDeliveryReceipt)
	if refusal == nil {
		t.Fatal("Authorize admitted a zero-value warrant; want a typed *gate.Refusal")
	}
	if refusal.Reason() != gate.RefusalInvalidIntent {
		t.Fatalf("refusal reason = %s, want %s", refusal.Reason(), gate.RefusalInvalidIntent)
	}
	assertActionable(t, refusal)
}

// TestAuthorizeRefusesClassMismatch pins that a warrant only admits its own
// class. FAILS against the L1 stub until L3.
func TestAuthorizeRefusesClassMismatch(t *testing.T) {
	t.Parallel()
	lineage, refusal := gate.Legalize(validIntent(t, gate.WriteLineageLinks))
	if refusal != nil {
		t.Fatalf("Legalize refused a lineage intent: %v", refusal)
	}
	got := gate.Authorize(lineage, gate.WriteDeliveryReceipt)
	if got == nil {
		t.Fatal("Authorize admitted a lineage warrant for a delivery-receipt write; want a typed *gate.Refusal")
	}
	if got.Reason() != gate.RefusalClassMismatch {
		t.Fatalf("refusal reason = %s, want %s", got.Reason(), gate.RefusalClassMismatch)
	}
	assertActionable(t, got)
}

// TestRefusalNeverMapsToConnectionExitCode pins D5's reconciliation: a
// gate.Refusal is a distinct typed value, NOT a *pasterrors.StructuredError, so
// pasterrors.ExitCode can never map it to exit code 2 (CategoryConnection). It
// obtains a real refusal from the commit-surface API. FAILS against the L1 stub
// (which returns no refusal) until L3.
func TestRefusalNeverMapsToConnectionExitCode(t *testing.T) {
	t.Parallel()
	refusal := gate.Authorize(gate.Warrant{}, gate.WriteDeliveryReceipt)
	if refusal == nil {
		t.Fatal("expected a typed *gate.Refusal from Authorize on a zero warrant")
	}
	var err error = refusal
	var structured *pasterrors.StructuredError
	if stderrors.As(err, &structured) {
		t.Fatal("a gate.Refusal must not be a *pasterrors.StructuredError; it must carry no error Category")
	}
	if code := pasterrors.ExitCode(err); code == 2 {
		t.Fatalf("gate.Refusal mapped to exit code 2 (CategoryConnection collision); got %d, want 1", code)
	}
	if code := pasterrors.ExitCode(err); code != 1 {
		t.Fatalf("gate.Refusal exit code = %d, want the default 1", code)
	}
}

// TestWriteIntentCannotEncodeOrigin is the type-level origin-unrepresentable
// guard (the M4 seam). WriteIntent must carry nothing but its write class: no
// field may name or type capture origin, trust, or actor. This mirrors the
// compile-time writeIntentShape lock in gate.go — if either fails, a field was
// added and the origin seam must be reconsidered. Green by construction from L1.
func TestWriteIntentCannotEncodeOrigin(t *testing.T) {
	t.Parallel()
	rt := reflect.TypeOf(gate.WriteIntent{})
	if rt.NumField() != 1 {
		t.Fatalf("WriteIntent has %d fields, want exactly 1 (the write class); a new field could smuggle in capture origin", rt.NumField())
	}
	field := rt.Field(0)
	if field.Type != reflect.TypeOf(gate.WriteClass(0)) {
		t.Fatalf("WriteIntent's only field is type %s, want gate.WriteClass", field.Type)
	}
	forbidden := []string{"origin", "trust", "actor", "capture", "source", "principal", "identity"}
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name)
		typeName := strings.ToLower(rt.Field(i).Type.String())
		for _, banned := range forbidden {
			if strings.Contains(name, banned) || strings.Contains(typeName, banned) {
				t.Fatalf("WriteIntent field %q (type %s) resembles capture origin %q; the gate must stay origin-blind", rt.Field(i).Name, rt.Field(i).Type, banned)
			}
		}
	}
}

// assertActionable checks a refusal message carries the actionable
// what/why/where/impact/fix shape required by C-actionable-errors.
func assertActionable(t *testing.T, refusal *gate.Refusal) {
	t.Helper()
	msg := refusal.Error()
	for _, want := range []string{"refused", "Why:", "Where:", "Impact:", "Fix:"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal message %q is missing the %q section; refusals must be actionable", msg, want)
		}
	}
}
