package ir_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/testutil"
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

// productionVersion is the host version the tree's production runtime contract
// records for harness, read from the one root, so a sample contract id in a
// test never restates a version.
func productionVersion(t testing.TB, harness ir.HarnessID) string {
	t.Helper()
	root, err := artifact.ProductionRuntimeContract(harness)
	require.NoError(t, err)
	_, version, ok := strings.Cut(root.String(), "@")
	require.True(t, ok, "production runtime contract %s has no version", root)
	return version
}

// mustProductionVersion is productionVersion for fixture builders that run
// before any testing.TB exists; it panics instead of failing a test.
func mustProductionVersion(harness ir.HarnessID) string {
	root, err := artifact.ProductionRuntimeContract(harness)
	if err != nil {
		panic(err)
	}
	_, version, ok := strings.Cut(root.String(), "@")
	if !ok {
		panic("production runtime contract has no version: " + root.String())
	}
	return version
}

// differentVersion is a host version one patch above the recorded production
// version for harness. A control that must differ from the production contract
// is derived from it, so a moved root can never make the control collide with
// the value it is meant to differ from.
func differentVersion(t testing.TB, harness ir.HarnessID) string {
	t.Helper()
	production, err := runtime.ParseHostVersion(productionVersion(t, harness))
	require.NoError(t, err)
	return testutil.Bump(t, production, 0, 0, 1).String()
}
