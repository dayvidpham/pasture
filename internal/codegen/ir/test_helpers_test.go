package ir_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/pkg/protocol/portable"
	"github.com/stretchr/testify/require"
)

func mustLocation(t testing.TB, section string, stop int) ir.Location {
	t.Helper()
	location, err := ir.NewLocation("worker-implement", "skills/worker/SKILL.md", section, ir.SourceRange{Start: 0, Stop: stop})
	require.NoError(t, err)
	return location
}

func mustTaskRef(t testing.TB, value string) portable.TaskRef {
	t.Helper()
	reference, err := portable.NewTaskRef(value)
	require.NoError(t, err)
	return reference
}

func mustAssignmentRef(t testing.TB, value string) portable.AssignmentRef {
	t.Helper()
	reference, err := portable.NewAssignmentRef(value)
	require.NoError(t, err)
	return reference
}

func mustContract(t testing.TB, harness ir.HarnessID, value string) ir.RuntimeContractID {
	t.Helper()
	contract, err := ir.NewRuntimeContractID(harness, value)
	require.NoError(t, err)
	return contract
}
