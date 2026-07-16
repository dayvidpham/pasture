package ir_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllOperationKindsIsDefensiveExactAndUnique proves the accessor's three
// guarantees: it returns a fresh copy on every call (mutating one result
// cannot corrupt another), the set has no duplicates, and every kind in it
// resolves through CoreOperationID — the closed set and the ID table can
// never silently drift apart.
func TestAllOperationKindsIsDefensiveExactAndUnique(t *testing.T) {
	t.Parallel()

	first := ir.AllOperationKinds()
	require.NotEmpty(t, first)
	first[0] = ir.OperationKind("forged")

	second := ir.AllOperationKinds()
	assert.NotEqual(t, ir.OperationKind("forged"), second[0], "AllOperationKinds must return a fresh defensive copy on every call")

	seen := make(map[ir.OperationKind]struct{}, len(second))
	for _, kind := range second {
		_, duplicate := seen[kind]
		assert.False(t, duplicate, "operation kind %q is duplicated", kind)
		seen[kind] = struct{}{}

		id, ok := ir.CoreOperationID(kind)
		assert.True(t, ok, "operation kind %q has no CoreOperationID entry", kind)
		assert.True(t, id.IsValid())
	}

	// Exact membership: every declared OperationKind constant is present and
	// nothing else is.
	assert.ElementsMatch(t, []ir.OperationKind{
		ir.OperationInvokeSkill,
		ir.OperationDelegateAssignment,
		ir.OperationContinueAssignment,
		ir.OperationSendAssignmentMessage,
		ir.OperationCollectAssignmentResults,
		ir.OperationStopAssignment,
		ir.OperationRequestUserDecision,
	}, second)
}

// TestSemanticOperationAccessorsRejectNil proves the closed operation sum's
// nil handling is real and consistent across every accessor, not just
// canonicalSemanticOperation's internal use.
func TestSemanticOperationAccessorsRejectNil(t *testing.T) {
	t.Parallel()

	_, err := ir.SemanticOperationKind(nil)
	require.Error(t, err)
	for _, field := range []string{"what:", "why:", "where:", "phase:", "impact:", "fix:"} {
		assert.Contains(t, err.Error(), field)
	}

	_, err = ir.SemanticOperationIdentity(nil)
	require.Error(t, err)

	_, err = ir.CanonicalSemanticOperation(nil)
	require.Error(t, err)

	_, err = ir.Operation(nil, mustLocation(t, "nil-operation", 0))
	require.Error(t, err)
}

// TestSemanticOperationAccessorsRejectTypedNilPointers proves the second,
// more subtle nil shape: every non-decision SemanticOperation variant
// (InvokeSkill, DelegateAssignment, ContinueAssignment,
// SendAssignmentMessage, CollectAssignmentResults, StopAssignment) has value
// receivers, so a *T also satisfies SemanticOperation for each — a typed-nil
// *InvokeSkill (etc.) produces a non-nil interface value (operation == nil
// is false) whose underlying pointer is nil. Every accessor must return the
// closed-sum diagnostic for this shape too, not panic on nil-pointer
// dereference inside validateOperation()/canonicalOperation().
func TestSemanticOperationAccessorsRejectTypedNilPointers(t *testing.T) {
	t.Parallel()

	variants := map[string]ir.SemanticOperation{
		"InvokeSkill":              (*ir.InvokeSkill)(nil),
		"DelegateAssignment":       (*ir.DelegateAssignment)(nil),
		"ContinueAssignment":       (*ir.ContinueAssignment)(nil),
		"SendAssignmentMessage":    (*ir.SendAssignmentMessage)(nil),
		"CollectAssignmentResults": (*ir.CollectAssignmentResults)(nil),
		"StopAssignment":           (*ir.StopAssignment)(nil),
	}
	for name, variant := range variants {
		name, variant := name, variant
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.NotPanics(t, func() {
				_, err := ir.SemanticOperationKind(variant)
				assert.Error(t, err)
			})
			require.NotPanics(t, func() {
				_, err := ir.SemanticOperationIdentity(variant)
				assert.Error(t, err)
			})
			require.NotPanics(t, func() {
				_, err := ir.CanonicalSemanticOperation(variant)
				assert.Error(t, err)
			})
			require.NotPanics(t, func() {
				_, err := ir.Operation(variant, mustLocation(t, "typed-nil-operation", 0))
				assert.Error(t, err)
			})
		})
	}
}
