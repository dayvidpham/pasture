package tasks

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

type observedColdRegistration struct {
	protocol.TaskTracker
	entered chan struct{}
	wantCtx context.Context
	t       *testing.T
}

func (o observedColdRegistration) RegisterSoftwareAgentContext(ctx context.Context, ns, name, version, source string) (provenance.SoftwareAgent, error) {
	if ctx != o.wantCtx {
		o.t.Error("cold registration replaced the caller's context")
	}
	close(o.entered)
	return o.TaskTracker.RegisterSoftwareAgentContext(ctx, ns, name, version, source)
}

func TestColdRegistrationWaitingCallerCounterexample(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cold.db")
	tracker, err := OpenTaskTrackerContext(t.Context(), path)
	require.NoError(t, err)
	db := tracker.(*trackerImpl).auditDB
	t.Cleanup(func() { _ = tracker.Close() })
	spec := WellKnownAgents()[0]
	_, err = db.ExecContext(t.Context(), "DELETE FROM pasture_well_known_agents WHERE name = ?", spec.Name)
	require.NoError(t, err)
	var agentsBefore, softwareBefore int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM agents").Scan(&agentsBefore))
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM agents_software").Scan(&softwareBefore))
	holder, err := dbconn.OpenSharedDBContextWithProfile(t.Context(), path, timeouts.ProductionProfile())
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Close() })
	holder.SetMaxOpenConns(1)
	_, err = holder.Exec("BEGIN IMMEDIATE")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	entered := make(chan struct{})
	done := make(chan struct{})
	var registrationErr error
	var registeredID provenance.AgentID
	t.Cleanup(func() { cancel(); _, _ = holder.Exec("ROLLBACK"); joinWriteGateWorker(t, done) })
	go func() {
		defer close(done)
		registeredID, registrationErr = ensureWellKnownAgent(ctx, db, observedColdRegistration{tracker, entered, ctx, t}, spec)
	}()
	select {
	case <-entered: // completed lookup miss and actual registration call entry
	case <-done:
		t.Fatalf("cold lookup failed before registration entry: %v", registrationErr)
	case <-time.After(5 * time.Second):
		t.Fatal("cold registration did not reach its dependency")
	}
	cancel()
	const schedulingTolerance = 30 * time.Millisecond
	// This SQL cancellation bound is one full production busy tier plus a
	// separate propagation allowance, not the much shorter gate-only bound.
	// Retain the allowance demonstrated by the real-holder proofs rather than
	// increasing it merely to match a sibling test's different wait shape.
	select {
	case <-done:
	case <-time.After(timeouts.ProductionProfile().SQLiteBusy() + schedulingTolerance):
		_, _ = holder.Exec("ROLLBACK")
		joinWriteGateWorker(t, done)
		t.Fatal("cold registration did not finish within one busy tier after caller cancellation with holder still locked")
	}
	require.ErrorIs(t, registrationErr, context.Canceled)
	require.Equal(t, provenance.AgentID{}, registeredID)
	var agentsAfter, softwareAfter, bindings int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM agents").Scan(&agentsAfter))
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM agents_software").Scan(&softwareAfter))
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM pasture_well_known_agents WHERE name = ?", spec.Name).Scan(&bindings))
	require.Equal(t, agentsBefore, agentsAfter)
	require.Equal(t, softwareBefore, softwareAfter)
	require.Zero(t, bindings)
	_, err = holder.Exec("ROLLBACK")
	require.NoError(t, err)
	// The failed invocation's context is not retained by the live tracker.
	id, err := ensureWellKnownAgent(t.Context(), db, tracker, spec)
	require.NoError(t, err)
	warmID, err := ensureWellKnownAgent(t.Context(), db, tracker, spec)
	require.NoError(t, err)
	require.Equal(t, id, warmID)
	legacy, err := tracker.RegisterSoftwareAgent("registration-context-test", "legacy-after-cancel", "1", "test")
	require.NoError(t, err)
	_, err = tracker.Agent(legacy.ID)
	require.NoError(t, err)
}

// An external legacy backend still satisfies provenance.Tracker, but must not
// silently receive context-free work when a context-aware call is requested.
type legacyRegistrationBackend struct{ provenance.Tracker }

func TestSoftwareRegistrationContextRejectsUnsupportedBackend(t *testing.T) {
	t.Parallel()
	tracker := &trackerImpl{prov: legacyRegistrationBackend{}}
	agent, err := tracker.RegisterSoftwareAgentContext(t.Context(), "test", "unsupported", "1", "test")
	require.Equal(t, provenance.SoftwareAgent{}, agent)
	var structured *pasterrors.StructuredError
	require.ErrorAs(t, err, &structured)
	require.Equal(t, pasterrors.CategoryConfig, structured.Category)
	require.Contains(t, structured.Why, "lacks context-aware software registration")
}

func TestSoftwareRegistrationContextRejectsCancelledAndNilBeforeDelegation(t *testing.T) {
	t.Parallel()
	tracker := &trackerImpl{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	agent, err := tracker.RegisterSoftwareAgentContext(ctx, "test", "cancelled", "1", "test")
	require.Equal(t, provenance.SoftwareAgent{}, agent)
	require.ErrorIs(t, err, context.Canceled)
	agent, err = tracker.RegisterSoftwareAgentContext(nil, "test", "nil", "1", "test")
	require.Equal(t, provenance.SoftwareAgent{}, agent)
	var structured *pasterrors.StructuredError
	require.ErrorAs(t, err, &structured)
	require.Equal(t, pasterrors.CategoryValidation, structured.Category)
	require.Contains(t, structured.Why, "nil context")
}
