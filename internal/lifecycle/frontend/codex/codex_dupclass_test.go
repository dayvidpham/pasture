package codex_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/frontend/codex"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// codexGenericWhere is the truthful generic Where the hoisted strictest-common
// engine emits for Codex after L3. Before L3 the per-host frontend still stamps
// ".../codex/codex.go in codex.Bind", so every Where pin below FAILS until the
// generic engine is wired — the expected TDD state.
const codexGenericWhere = "Binding the Codex lifecycle event (internal/lifecycle/frontend/frontend.go in frontend.Bind)."

// TestCodexDuplicateClassAdmissionParity is the C1-C4 dup-class table for the
// Codex frontend. Like OpenCode, Codex has NO duplicate case in production
// today (folded !found||kind-mismatch, no dup guard). Admission is REJECT in
// every cell both before and after (pipeline-level); only the rejection SITE
// and TEXT change. FAILS until L3.
func TestCodexDuplicateClassAdmissionParity(t *testing.T) {
	t.Parallel()

	// PreToolUse declares session_id(session=1), turn_id(turn=2),
	// tool_use_id(toolCall=4).
	const kind = registration.EventCodexPreToolUse

	tests := []struct {
		name         string
		bindings     []model.NativeBinding
		whatFragment string
	}{
		{
			name: "C1 valid-then-dup same kind",
			bindings: []model.NativeBinding{
				{Kind: model.BindingSession, NativeName: "session_id", Value: "sess-one"},
				{Kind: model.BindingSession, NativeName: "session_id", Value: "sess-two"},
			},
			whatFragment: `binding 1 repeats native field "session_id"`,
		},
		{
			name: "C2 valid-then-dup different kind (dup fires before kind)",
			bindings: []model.NativeBinding{
				{Kind: model.BindingSession, NativeName: "session_id", Value: "sess-one"},
				{Kind: model.BindingToolCall, NativeName: "session_id", Value: "sess-two"},
			},
			whatFragment: `binding 1 repeats native field "session_id"`,
		},
		{
			name: "C3 wrong-kind-first (separated kind-mismatch error)",
			bindings: []model.NativeBinding{
				{Kind: model.BindingToolCall, NativeName: "session_id", Value: "sess-one"},
			},
			whatFragment: `binding 0 classifies native field "session_id" as kind 4, but the runtime mapping declares kind 1`,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l1, identities, err := codex.Bind(kind, tc.bindings)

			require.Error(t, err, "class must be rejected at the frontend after the hoist")
			require.False(t, l1.IsValid())
			require.Empty(t, identities)

			var structured *pasterrors.StructuredError
			require.True(t, errors.As(err, &structured))
			require.Equal(t, pasterrors.CategoryValidation, structured.Category)
			require.Contains(t, structured.What, tc.whatFragment)
			require.Equal(t, codexGenericWhere, structured.Where)
		})
	}
}
