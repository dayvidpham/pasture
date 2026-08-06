package claude_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/frontend/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// claudeGenericWhere is the truthful generic Where the strictest-common engine
// emits for Claude after L3. This is the FROZEN NEW CLAUDE PIN reviewed in the
// P6 error-delta pass. Before L3 the Claude frontend stamps the OLD Where
// ".../claude/claude.go in claude.Bind", so these Where pins FAIL until the
// generic engine is wired — the expected TDD state.
//
// OLD (4679f0a): "Binding a Claude lifecycle event (internal/lifecycle/frontend/claude/claude.go in claude.Bind)."
// NEW (this pin): "Binding a Claude lifecycle event (internal/lifecycle/frontend/frontend.go in frontend.Bind)."
const claudeGenericWhere = "Binding a Claude lifecycle event (internal/lifecycle/frontend/frontend.go in frontend.Bind)."

// TestClaudeDuplicateClassControl is the C4 control: Claude already has the dup
// guard and the separated undeclared/kind errors, so its ADMISSION is unchanged
// by the hoist (every class was and remains rejected at the frontend). The only
// Claude delta is the Where line (file/func path), unified across every error
// site. The What/Why/Impact/Fix text is unchanged. FAILS until L3 on the Where
// pins only.
func TestClaudeDuplicateClassControl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		kind     model.ContractEventKind
		bindings []model.NativeBinding
		wantWhat string // exact frozen new What (unchanged from 4679f0a)
	}{
		{
			name: "C1 valid-then-dup same kind",
			kind: registration.EventSessionStart,
			bindings: []model.NativeBinding{
				{Kind: model.BindingSession, NativeName: "session_id", Value: "one"},
				{Kind: model.BindingSession, NativeName: "session_id", Value: "two"},
			},
			wantWhat: `Claude event "SessionStart" binding 1 repeats native field "session_id".`,
		},
		{
			name: "C2 valid-then-dup different kind (dup fires before kind)",
			kind: registration.EventSessionStart,
			bindings: []model.NativeBinding{
				{Kind: model.BindingSession, NativeName: "session_id", Value: "one"},
				{Kind: model.BindingRequest, NativeName: "session_id", Value: "two"},
			},
			wantWhat: `Claude event "SessionStart" binding 1 repeats native field "session_id".`,
		},
		{
			name: "C3 wrong-kind-first (separated kind-mismatch error)",
			kind: registration.EventSessionStart,
			bindings: []model.NativeBinding{
				{Kind: model.BindingRequest, NativeName: "session_id", Value: "one"},
			},
			wantWhat: `Claude event "SessionStart" binding 0 classifies native field "session_id" as kind 3, but the runtime mapping declares kind 1.`,
		},
		{
			name:     "unknown ordinal (pre-loop site shares the unified Where)",
			kind:     model.ContractEventKind(31),
			bindings: nil,
			wantWhat: "Claude lifecycle event ordinal 31 is not declared.",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l1, identities, err := claude.Bind(tc.kind, tc.bindings)

			require.Error(t, err)
			require.False(t, l1.IsValid())
			require.Empty(t, identities)

			var structured *pasterrors.StructuredError
			require.True(t, errors.As(err, &structured))
			require.Equal(t, pasterrors.CategoryValidation, structured.Category)
			require.Equal(t, tc.wantWhat, structured.What, "Claude What text is unchanged by the hoist")
			require.Equal(t, claudeGenericWhere, structured.Where, "Claude Where is the unified generic location")
		})
	}
}
