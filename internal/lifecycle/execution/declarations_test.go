package execution_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
	"github.com/stretchr/testify/require"
)

func TestInvocationPreservesInvalidityAndModelAnchoring(t *testing.T) {
	t.Parallel()
	provider, model, bad := "route", "other-model", strings.Repeat("x", execution.MaxValueBytes+1)
	invocation := execution.BoundedInvocation(&provider, &model, &bad)
	require.Empty(t, *invocation.Role, "invalid text is not retained")
	metadata, err := execution.Normalize(execution.Inputs{Kind: execution.Primary, Host: execution.Declaration{Model: execution.Raw{Present: true, Member: execution.HostModelID, Value: "host-model"}}, Environment: invocation.Declaration()})
	require.NoError(t, err)
	require.Equal(t, execution.Conflict, metadata.Model.Status)
	require.Equal(t, execution.Unavailable, metadata.Provider.Status)
	require.Equal(t, execution.Invalid, metadata.ProtocolRole.Status)
}
