package idmigrate

// project_binding_classification_test.go pins the classification DECISION for
// the explicit project-to-epoch mapping relation.
//
// The relation carries one declared identity and one opaque handle:
//
//   - epoch_id names a task: the project bind refuses an epoch that does not
//     exist in tasks, so it converts with the tasks it names.
//   - binding_id is a minted UUID handle. Nothing parses or resolves it, so it
//     is preserved byte-exact even when its value happens to look retired.
//
// The broad schema walk proves the relation is classified; these tests prove it
// is classified correctly and that the production conversion behaves as
// declared.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/dbconn"
)

func TestProjectBindingColumnsAreClassifiedOnPurpose(t *testing.T) {
	t.Parallel()
	mirrors := DeclaredConversionMirrors()
	classes := map[string][]string{}
	record := func(which string, cols []IdentityColumn) {
		for _, col := range cols {
			key := col.Table + "." + col.Column
			classes[key] = append(classes[key], which)
		}
	}
	record("pasture-owned", mirrors.PastureOwned)
	record("pasture-preserved", mirrors.PasturePreserved)

	want := map[string]string{
		"pasture_project_epoch_binding.epoch_id":   "pasture-owned",
		"pasture_project_epoch_binding.binding_id": "pasture-preserved",
	}
	for key, class := range want {
		require.Equal(t, []string{class}, classes[key],
			"%s must be classified exactly once, as %s", key, class)
	}
}

func TestProjectBindingConversionConvertsEpochIDAndPreservesBindingHandle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "project-bindings.db")
	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	_, err = db.ExecContext(ctx, `CREATE TABLE pasture_project_epoch_binding (
		binding_id TEXT PRIMARY KEY,
		project_hash TEXT NOT NULL,
		epoch_id TEXT NOT NULL,
		role TEXT NOT NULL,
		bound_at INTEGER NOT NULL,
		unbound_at INTEGER,
		unbound_reason TEXT)`)
	require.NoError(t, err)

	retiredEpoch := retiredUUID("task-ns", 7)
	// A retired-looking value in the PRESERVED column proves preservation is a
	// decision rather than an accident of the value's shape.
	preservedBinding := retiredUUID("handle-ns", 8)
	_, err = db.ExecContext(ctx, `
		INSERT INTO pasture_project_epoch_binding
			(binding_id, project_hash, epoch_id, role, bound_at)
		VALUES (?, ?, ?, 'worker', 1)`,
		preservedBinding, projectHashForClassificationTest, retiredEpoch)
	require.NoError(t, err)

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	rewritten, err := convertPastureIdentities(ctx, tx)
	require.NoError(t, err)
	require.Equal(t, 1, rewritten, "only the project epoch_id is a converted identity")

	var gotEpoch, gotBinding string
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT epoch_id, binding_id FROM pasture_project_epoch_binding`).Scan(&gotEpoch, &gotBinding))
	require.Equal(t, "task-ns/00000000-0000-0000-0000-000000000007", gotEpoch,
		"a project mapping's epoch_id names a task and must convert with it")
	require.Equal(t, preservedBinding, gotBinding,
		"a project binding_id is an opaque minted handle and must stay byte-exact")
	require.NoError(t, tx.Rollback())
}

// projectHashForClassificationTest is a valid 64-lowercase-hex Peasant project
// identity; the conversion never parses or rewrites it.
const projectHashForClassificationTest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
