package tasks

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/provadapter"
)

type cancelAfterFixedRegistration struct {
	provenance.Tracker
	cancel context.CancelFunc
}

func (tr cancelAfterFixedRegistration) RegisterFixedSoftwareAgentContext(ctx context.Context, reg provenance.FixedSoftwareAgentRegistration) (provenance.SoftwareAgent, error) {
	agent, err := tr.Tracker.(provenance.FixedSoftwareAgentRegistrarContext).RegisterFixedSoftwareAgentContext(ctx, reg)
	tr.cancel()
	return agent, err
}

func TestDefaultLLMInitializationAndHookReadOnly(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.db")
	hook, err := OpenLifecycleTaskTrackerContext(t.Context(), path)
	require.Error(t, err)
	require.Nil(t, hook)
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM agents WHERE id=?`, provadapter.DefaultLLMActorID().String()).Scan(&count))
	require.Zero(t, count, "an uninitialized hook must never register the actor")
	require.NoError(t, db.Close())

	store, err := OpenTaskTrackerContext(t.Context(), path)
	require.NoError(t, err)
	agent, err := store.SoftwareAgent(provadapter.DefaultLLMActorID())
	require.NoError(t, err)
	require.Equal(t, provadapter.DefaultLLMActorName, agent.Name)
	require.NoError(t, store.Close())
	hook, err = OpenLifecycleTaskTrackerContext(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, hook.Close())
	store, err = OpenTaskTrackerContext(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
}

func TestDefaultLLMInitializationCanceledOpen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.db")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	store, err := OpenTaskTrackerContext(ctx, path)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, store)
	store, err = OpenTaskTrackerContext(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
}

func TestDefaultLLMInitializationRetriesAndRefusesConflict(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.db")
	store, err := OpenTaskTrackerContext(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	// A crash after actor commit but before the marker leaves an exact actor.
	_, err = db.Exec(`DELETE FROM pasture_default_llm_initialization`)
	require.NoError(t, err)
	hook, err := OpenLifecycleTaskTrackerContext(t.Context(), path)
	require.Error(t, err)
	require.Nil(t, hook)
	store, err = OpenTaskTrackerContext(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	// A conflicting fixed manifest cannot be marked as successful.
	_, err = db.Exec(`DELETE FROM pasture_default_llm_initialization`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE fixed_actor_manifest_entries SET metadata='{"classification":"other"}' WHERE actor_id=?`, provadapter.DefaultLLMActorID().String())
	require.NoError(t, err)
	store, err = OpenTaskTrackerContext(t.Context(), path)
	require.Error(t, err)
	require.Nil(t, store)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pasture_default_llm_initialization`).Scan(&count))
	require.Zero(t, count)
}

func TestDefaultLLMInitializationCancellationAfterRegistration(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	_, err := store.auditDB.Exec(`DELETE FROM pasture_default_llm_initialization`)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err = ensureDefaultLLMActor(ctx, store.auditDB, cancelAfterFixedRegistration{Tracker: store.prov, cancel: cancel}, false)
	require.ErrorIs(t, err, context.Canceled)
	var count int
	require.NoError(t, store.auditDB.QueryRow(`SELECT COUNT(*) FROM pasture_default_llm_initialization`).Scan(&count))
	require.Zero(t, count)
	_, err = store.prov.SoftwareAgent(provadapter.DefaultLLMActorID())
	require.NoError(t, err, "a committed actor may remain after cancellation")
	require.NoError(t, ensureDefaultLLMActor(t.Context(), store.auditDB, store.prov, false))
}

func TestDefaultLLMInitializationUpgradesSchema11(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.db")
	store, err := OpenTaskTrackerContext(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = db.Exec(`DROP TABLE pasture_default_llm_initialization`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM audit_schema_meta WHERE version=12`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	store, err = OpenTaskTrackerContext(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	hook, err := OpenLifecycleTaskTrackerContext(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, hook.Close())
}
