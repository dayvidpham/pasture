package idmigrate

// session_binding_classification_test.go pins the classification DECISION for
// the session binding relations, not just their membership in some mirror.
//
// The two relations carry one declared identity and one opaque handle each:
//
//   - epoch_id names a task: the explicit bind refuses an epoch that does not
//     exist in tasks, and the inferred row is derived from a Provenance
//     ownership TaskID. It converts with the tasks it names.
//   - binding_id is a minted UUID (explicit) or a deterministic
//     "inferred-<sha256>" label (inferred). It carries no namespace, nothing
//     parses or resolves it, and rewriting the deterministic handle would break
//     its stability. It is preserved byte-exact.
//
// A future edit that flips either decision fails here by column name, before
// the broader schema walk can be satisfied by the wrong bucket.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/dbconn"
)

// TestSessionBindingColumnsAreClassifiedOnPurpose pins each binding column to
// exactly one classification. The schema walk answers "classified"; this answers
// "classified correctly".
func TestSessionBindingColumnsAreClassifiedOnPurpose(t *testing.T) {
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
	record("pasture-owned-array", mirrors.PastureOwnedArray)
	record("pasture-preserved", mirrors.PasturePreserved)
	record("pasture-special", mirrors.PastureSpecial)

	want := map[string]string{
		"pasture_session_epoch_binding.epoch_id":      "pasture-owned",
		"pasture_session_inferred_binding.epoch_id":   "pasture-owned",
		"pasture_session_epoch_binding.binding_id":    "pasture-preserved",
		"pasture_session_inferred_binding.binding_id": "pasture-preserved",
	}
	for key, class := range want {
		require.Equal(t, []string{class}, classes[key],
			"%s must be classified exactly once, as %s", key, class)
	}
}

// TestSessionBindingConversionConvertsEpochIDsAndPreservesBindingHandles is the
// behavioral half: with retired two-dash values seeded into all four columns,
// the production pasture conversion rewrites exactly the two epoch_id values and
// leaves both binding_id values byte-exact.
func TestSessionBindingConversionConvertsEpochIDsAndPreservesBindingHandles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "session-bindings.db")
	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	_, err = db.ExecContext(ctx, `CREATE TABLE pasture_session_epoch_binding (
		binding_id TEXT PRIMARY KEY,
		harness TEXT NOT NULL,
		session TEXT NOT NULL,
		epoch_id TEXT NOT NULL,
		role TEXT NOT NULL,
		bound_at INTEGER NOT NULL,
		unbound_at INTEGER,
		unbound_reason TEXT)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE TABLE pasture_session_inferred_binding (
		binding_id TEXT PRIMARY KEY,
		harness TEXT NOT NULL,
		session TEXT NOT NULL,
		epoch_id TEXT NOT NULL,
		role TEXT NOT NULL,
		bound_at INTEGER NOT NULL,
		unbound_at INTEGER,
		unbound_reason TEXT,
		inferred_at INTEGER NOT NULL,
		reason TEXT NOT NULL)`)
	require.NoError(t, err)

	explicitEpoch := retiredUUID("task-ns", 1)
	inferredEpoch := retiredUUID("task-ns", 2)
	// A retired-looking value in a PRESERVED column proves the preservation is a
	// decision rather than an accident of the value's shape.
	explicitBinding := retiredUUID("handle-ns", 3)
	inferredBinding := retiredUUID("handle-ns", 4)

	_, err = db.ExecContext(ctx, `
		INSERT INTO pasture_session_epoch_binding
			(binding_id, harness, session, epoch_id, role, bound_at)
		VALUES (?, 'claude-code', 'session-explicit', ?, 'implementer', 1)`,
		explicitBinding, explicitEpoch)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO pasture_session_inferred_binding
			(binding_id, harness, session, epoch_id, role, bound_at, inferred_at, reason)
		VALUES (?, 'claude-code', 'session-inferred', ?, 'owner-responsibility', 1, 1, 'claim-ownership')`,
		inferredBinding, inferredEpoch)
	require.NoError(t, err)

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	rewritten, err := convertPastureIdentities(ctx, tx)
	require.NoError(t, err)
	require.Equal(t, 2, rewritten, "exactly one epoch_id value in each binding relation converts")

	var gotExplicitEpoch, gotExplicitBinding string
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT epoch_id, binding_id FROM pasture_session_epoch_binding WHERE session = 'session-explicit'`).
		Scan(&gotExplicitEpoch, &gotExplicitBinding))
	require.Equal(t, "task-ns/00000000-0000-0000-0000-000000000001", gotExplicitEpoch,
		"an explicit binding's epoch_id names a task and must convert with it")
	require.Equal(t, explicitBinding, gotExplicitBinding,
		"an explicit binding_id is an opaque minted handle and must stay byte-exact")

	var gotInferredEpoch, gotInferredBinding string
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT epoch_id, binding_id FROM pasture_session_inferred_binding WHERE session = 'session-inferred'`).
		Scan(&gotInferredEpoch, &gotInferredBinding))
	require.Equal(t, "task-ns/00000000-0000-0000-0000-000000000002", gotInferredEpoch,
		"an inferred binding's epoch_id names a task and must convert with it")
	require.Equal(t, inferredBinding, gotInferredBinding,
		"an inferred binding_id is a deterministic derived handle and must stay byte-exact")
	require.NoError(t, tx.Rollback())
}
