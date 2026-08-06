package opencode_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/frontend/opencode"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// ocGenericWhere is the truthful generic Where the hoisted strictest-common
// engine emits for OpenCode after L3. Before L3 the per-host frontend still
// stamps ".../opencode/opencode.go in opencode.Bind", so every Where pin below
// FAILS until the generic engine is wired — the expected TDD state.
const ocGenericWhere = "Binding a OpenCode lifecycle event (internal/lifecycle/frontend/frontend.go in frontend.Bind)."

// TestOpenCodeDuplicateClassAdmissionParity is the C1-C4 dup-class table for the
// OpenCode frontend. OpenCode has NO duplicate case in production today: the
// frontend folds !found||kind-mismatch into one error and never guards against a
// repeated native name, so a same-name duplicate is admitted at the frontend and
// only rejected later at the waist L2 transform (event.go verifyIdentities dedups
// on {kind, nativeName}). After the refactor Claude's dup guard is hoisted here.
//
// Admission is REJECT in every cell both before and after (pipeline-level):
//   - C1 same-kind dup: before = frontend admits, waist L2 rejects; after =
//     frontend rejects via hoisted dup guard. reject -> reject.
//   - C2 diff-kind dup: before = frontend folded-rejects (kind arm); after =
//     frontend dup guard fires BEFORE the kind check. reject -> reject.
//   - C3 wrong-kind-first: before = frontend folded-rejects; after = frontend
//     rejects via the separated kind-mismatch error. reject -> reject.
//
// Only the rejection SITE and TEXT change; no accepted input becomes rejected
// and no rejected input becomes accepted. FAILS until L3.
func TestOpenCodeDuplicateClassAdmissionParity(t *testing.T) {
	t.Parallel()

	// tool.execute.before declares sessionID(session=1) and callID(toolCall=4).
	const kind = registration.EventOpenCodeToolExecuteBefore

	tests := []struct {
		name         string
		bindings     []model.NativeBinding
		whatFragment string
	}{
		{
			name: "C1 valid-then-dup same kind",
			bindings: []model.NativeBinding{
				{Kind: model.BindingSession, NativeName: "sessionID", Value: "sess-one"},
				{Kind: model.BindingSession, NativeName: "sessionID", Value: "sess-two"},
			},
			whatFragment: `binding 1 repeats native field "sessionID"`,
		},
		{
			name: "C2 valid-then-dup different kind (dup fires before kind)",
			bindings: []model.NativeBinding{
				{Kind: model.BindingSession, NativeName: "sessionID", Value: "sess-one"},
				{Kind: model.BindingToolCall, NativeName: "sessionID", Value: "sess-two"},
			},
			whatFragment: `binding 1 repeats native field "sessionID"`,
		},
		{
			name: "C3 wrong-kind-first (separated kind-mismatch error)",
			bindings: []model.NativeBinding{
				{Kind: model.BindingToolCall, NativeName: "sessionID", Value: "sess-one"},
			},
			whatFragment: `binding 0 classifies native field "sessionID" as kind 4, but the runtime mapping declares kind 1`,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l1, identities, err := opencode.Bind(kind, tc.bindings)

			// Admission: rejected at the frontend with no L1 and no identities.
			require.Error(t, err, "class must be rejected at the frontend after the hoist")
			require.False(t, l1.IsValid())
			require.Empty(t, identities)

			var structured *pasterrors.StructuredError
			require.True(t, errors.As(err, &structured))
			require.Equal(t, pasterrors.CategoryValidation, structured.Category)
			require.Contains(t, structured.What, tc.whatFragment)
			// Truthful generic Where pin (P6 error-delta input).
			require.Equal(t, ocGenericWhere, structured.Where)
		})
	}
}
