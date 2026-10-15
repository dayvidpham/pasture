package idmigrate_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/idmigrate"
	"github.com/dayvidpham/pasture/internal/testutil"
)

// retiredElement returns a recognizable retired identity element for n.
func retiredElement(namespace string, n int) string {
	return fmt.Sprintf("%s--00000000-0000-0000-0000-%012d", namespace, n)
}

// TestConvertedStoreLeavesNoRetiredBytesInGovernedAuditArrays is the end-to-end
// array-aware acceptance: a real operator store carrying the canonical JSON
// identity arrays the governed-allocation writer produces converts through the
// production entry point, and the raw column bytes carry no retired two-dash form
// anywhere afterwards. The dry run reports the array columns the conversion
// rewrites, so the plan and the conversion agree.
func TestConvertedStoreLeavesNoRetiredBytesInGovernedAuditArrays(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, db := testutil.RetiredPopulatedStoreWithClaim(t)

	childTasks := `["` + retiredElement("task-ns", 1) + `","` + retiredElement("task-ns", 2) + `"]`
	occupants := `["` + retiredElement("actor-ns", 3) + `"]`
	assignments := `["assignment-a","assignment-b"]`
	_, err := db.ExecContext(ctx,
		`INSERT INTO pasture_governed_allocation_audit (operation_id, closure_anchor, child_task_id, child_assignment_id, occupant_id) VALUES (?,?,?,?,?)`,
		"op-1", 1, childTasks, assignments, occupants)
	require.NoError(t, err)

	preview, err := idmigrate.DryRun(ctx, db, path)
	require.NoError(t, err)
	require.Equal(t, 1, preview.Columns["pasture_governed_allocation_audit.child_task_id"],
		"the dry run must report the convertible identity array")
	require.Equal(t, 1, preview.Columns["pasture_governed_allocation_audit.occupant_id"])

	_, err = idmigrate.Convert(ctx, db, idmigrate.ConvertOptions{StorePath: path})
	require.NoError(t, err)

	var gotTasks, gotAssignments, gotOccupants string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT child_task_id, child_assignment_id, occupant_id FROM pasture_governed_allocation_audit WHERE operation_id='op-1'`).
		Scan(&gotTasks, &gotAssignments, &gotOccupants))

	require.NotContains(t, gotTasks, "--", "no retired two-dash form may survive in child_task_id")
	require.NotContains(t, gotOccupants, "--", "no retired two-dash form may survive in occupant_id")
	require.Equal(t, assignments, gotAssignments, "the opaque assignment array must stay byte-exact")
	require.Equal(t, `["task-ns/00000000-0000-0000-0000-000000000001","task-ns/00000000-0000-0000-0000-000000000002"]`, gotTasks)
	require.Equal(t, `["actor-ns/00000000-0000-0000-0000-000000000003"]`, gotOccupants)
}
