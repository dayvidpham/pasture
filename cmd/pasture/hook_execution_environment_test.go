package main

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
	"github.com/stretchr/testify/require"
)

func TestHookExecutionEnvironmentSnapshotsBoundedDeclarations(t *testing.T) {
	t.Parallel()
	values := map[string]string{"PASTURE_EXECUTION_PROVIDER": "route", "PASTURE_EXECUTION_MODEL": "model", "PASTURE_EXECUTION_ROLE": strings.Repeat("x", execution.MaxValueBytes+1)}
	env, err := HookEnvironmentFromOS(func(key string) (string, bool) { value, ok := values[key]; return value, ok })
	require.NoError(t, err)
	values["PASTURE_EXECUTION_MODEL"] = "changed"
	require.Equal(t, "model", *env.Execution.Model)
	require.Empty(t, *env.Execution.Role)
	result, err := execution.Normalize(execution.Inputs{Kind: execution.Selection, Environment: env.Execution.Declaration()})
	require.NoError(t, err)
	require.Equal(t, execution.Observed, result.Provider.Status)
	require.Equal(t, execution.Invalid, result.ProtocolRole.Status)
}
