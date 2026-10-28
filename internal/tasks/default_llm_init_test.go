package tasks

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/provadapter"
	"github.com/dayvidpham/pasture/internal/timeouts"
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

func TestDefaultLLMConcurrentNormalOpenConverges(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.db")
	store, err := OpenTaskTrackerContext(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM pasture_default_llm_initialization`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			tr, err := OpenTaskTrackerContext(t.Context(), path)
			if err == nil {
				err = tr.Close()
			}
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	store, err = OpenLifecycleTaskTrackerContext(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
}

func TestDefaultLLMInitializationCancellationWhileSQLiteWriterHeld(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.db")
	trail, err := audit.NewSqliteAuditTrailContext(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, trail.Close())
	plain, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = plain.Close() })
	counts := newGateStatementCounter("begin immediate")
	pool := sql.OpenDB(gateCountingConnector{dsn: dbconn.SharedDSNWithProfile(path, timeouts.TestProfile()), inner: &sqlite.Driver{}, counts: counts})
	pool.SetMaxOpenConns(1)
	tr, err := provenance.OpenBorrowedSQLiteContext(t.Context(), pool)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Close(); _ = pool.Close() })
	baseline := counts.count("begin immediate")
	holder, err := plain.Conn(t.Context())
	require.NoError(t, err)
	_, err = holder.ExecContext(t.Context(), `BEGIN IMMEDIATE`)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- ensureDefaultLLMActor(ctx, pool, tr, false) }()
	// Cleanup always joins the worker before releasing the pool or temp store.
	defer func() {
		cancel()
		_, _ = holder.ExecContext(context.Background(), `ROLLBACK`)
		_ = holder.Close()
		<-done
	}()
	require.Eventually(t, func() bool { return counts.count("begin immediate") > baseline }, 5*time.Second, time.Millisecond, "fixed registrar must enter the SQLite lock attempt")
	cancel()
	// The SQLite driver may need its busy wait to settle. Do not infer rollback
	// from cancellation; release the holder and observe the actual result.
	_, err = holder.ExecContext(context.Background(), `ROLLBACK`)
	require.NoError(t, err)
	result := <-done
	done <- result
	require.ErrorIs(t, result, context.Canceled)
	var count int
	require.NoError(t, plain.QueryRow(`SELECT COUNT(*) FROM pasture_default_llm_initialization`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, plain.QueryRow(`SELECT COUNT(*) FROM agents WHERE id=?`, provadapter.DefaultLLMActorID().String()).Scan(&count))
	require.Zero(t, count, "cancellation before transaction entry cannot activate later")
	require.NoError(t, ensureDefaultLLMActor(t.Context(), pool, tr, false))
}
