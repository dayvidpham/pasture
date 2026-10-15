package idmigrate

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/dbconn"
)

// retiredUUID returns a recognizable retired identity for element n.
func retiredUUID(namespace string, n int) string {
	return fmt.Sprintf("%s--00000000-0000-0000-0000-%012d", namespace, n)
}

// TestDryRunContextCountMatchesTheConversion pins the BLOCKER fix: the dry run's
// kind-filtered context count equals what the dependency's conversion actually
// rewrites. A retired identity under a parsed kind converts; the same value under
// an unparsed kind (git) and an already-current value are left byte-exact.
func TestDryRunContextCountMatchesTheConversion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "contexts.db")
	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	// A populated tasks table makes InspectIDFormat report a retired, populated
	// store so ConvertIDsInTx does not short-circuit. It must carry every owned
	// column the conversion queries for the table (id and owner_id), or SQLite's
	// double-quoted-literal fallback would feed the column name back as a value.
	_, err = db.ExecContext(ctx, `CREATE TABLE tasks (id TEXT, owner_id TEXT)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO tasks (id, owner_id) VALUES (?, ?)`,
		retiredUUID("seed-ns", 1), retiredUUID("seed-ns", 2))
	require.NoError(t, err)

	converted := retiredUUID("conv-ns", 10)
	slash := "conv-ns/00000000-0000-0000-0000-000000000011"
	unparsed := retiredUUID("git-ns", 12)

	for _, col := range previewDependencyContextColumns {
		_, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %q (context_kind TEXT, %q TEXT)`, col.table, col.column))
		require.NoError(t, err)
		for _, row := range []struct{ kind, identity string }{
			{"task", converted}, // parsed kind: converts
			{"git", unparsed},   // unparsed kind: preserved byte-exact
			{"task", slash},     // already current: unchanged
		} {
			_, err := db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %q (context_kind, %q) VALUES (?, ?)`, col.table, col.column), row.kind, row.identity)
			require.NoError(t, err)
		}
	}

	// The dry-run count sees exactly the one convertible row per column.
	for _, col := range previewDependencyContextColumns {
		count, err := countRetiredContext(ctx, db, col.table, col.column)
		require.NoError(t, err)
		require.Equal(t, 1, count, "dry-run context count for %s.%s", col.table, col.column)
	}

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = provenance.ConvertIDsInTx(ctx, tx, provenance.IDConversionOptions{})
	require.NoError(t, err)
	for _, col := range previewDependencyContextColumns {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT context_kind, %q FROM %q`, col.column, col.table))
		require.NoError(t, err)
		for rows.Next() {
			var kind, identity string
			require.NoError(t, rows.Scan(&kind, &identity))
			switch kind {
			case "task":
				if identity == slash {
					require.Equal(t, slash, identity)
				} else {
					require.Equal(t, "conv-ns/00000000-0000-0000-0000-000000000010", identity,
						"a parsed-kind retired context identity must convert")
				}
			case "git":
				require.Equal(t, unparsed, identity, "an unparsed-kind context identity must be preserved byte-exact")
			}
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}
	require.NoError(t, tx.Rollback())
}

// TestGovernedAuditIdentityArraysConvertWithNoRetiredBytesLeft pins the
// array-aware conversion: a store carrying the canonical JSON identity arrays
// the governed-allocation writer produces converts every element, and the raw
// bytes carry no retired two-dash form afterwards. The dry-run array count
// matches the rows the production path rewrites.
func TestGovernedAuditIdentityArraysConvertWithNoRetiredBytesLeft(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "governed.db")
	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	_, err = db.ExecContext(ctx, `CREATE TABLE pasture_governed_allocation_audit (
		operation_id TEXT PRIMARY KEY,
		child_task_id TEXT NOT NULL,
		child_assignment_id TEXT NOT NULL,
		occupant_id TEXT NOT NULL)`)
	require.NoError(t, err)

	childTasks := `["` + retiredUUID("task-ns", 1) + `","` + retiredUUID("task-ns", 2) + `"]`
	occupants := `["` + retiredUUID("actor-ns", 3) + `"]`
	assignments := `["assignment-a","assignment-b"]`
	_, err = db.ExecContext(ctx,
		`INSERT INTO pasture_governed_allocation_audit (operation_id, child_task_id, child_assignment_id, occupant_id) VALUES (?,?,?,?)`,
		"op-1", childTasks, assignments, occupants)
	require.NoError(t, err)

	for _, col := range pastureIdentityArrayColumns {
		count, err := countRetiredArray(ctx, db, col.table, col.column)
		require.NoError(t, err)
		require.Equal(t, 1, count, "dry-run array count for %s.%s", col.table, col.column)
	}

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	rewritten, err := convertPastureIdentities(ctx, tx)
	require.NoError(t, err)
	require.Equal(t, 2, rewritten, "both array columns hold one convertible row")

	var gotTasks, gotAssignments, gotOccupants string
	require.NoError(t, tx.QueryRowContext(ctx,
		`SELECT child_task_id, child_assignment_id, occupant_id FROM pasture_governed_allocation_audit WHERE operation_id='op-1'`).
		Scan(&gotTasks, &gotAssignments, &gotOccupants))
	require.NoError(t, tx.Rollback())

	require.NotContains(t, gotTasks, "--", "no retired two-dash form may survive in child_task_id")
	require.NotContains(t, gotOccupants, "--", "no retired two-dash form may survive in occupant_id")
	require.Equal(t, assignments, gotAssignments, "the opaque assignment array must stay byte-exact")
	require.Equal(t, `["task-ns/00000000-0000-0000-0000-000000000001","task-ns/00000000-0000-0000-0000-000000000002"]`, gotTasks)
	require.Equal(t, `["actor-ns/00000000-0000-0000-0000-000000000003"]`, gotOccupants)
}

// TestGovernedAuditArrayLeavesOpaqueValuesByteExact pins that a non-identity
// element in a declared array is preserved, so conversion never guesses.
func TestGovernedAuditArrayLeavesOpaqueValuesByteExact(t *testing.T) {
	t.Parallel()
	raw := `["opaque-label","task-ns--00000000-0000-0000-0000-000000000001"]`
	converted, changed, err := convertIdentityArrayValue(raw)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, `["opaque-label","task-ns/00000000-0000-0000-0000-000000000001"]`, converted)

	same, changed, err := convertIdentityArrayValue(`["opaque-label","already/current"]`)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, `["opaque-label","already/current"]`, same)
}
