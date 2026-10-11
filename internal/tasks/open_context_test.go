package tasks

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/stretchr/testify/require"
)

func TestUnifiedContextOpenCancelledFirstUse(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "locked.db")
	holder, err := dbconn.OpenDefaultDB(path)
	require.NoError(t, err)
	defer holder.Close()
	holder.SetMaxOpenConns(1)
	_, err = holder.Exec("CREATE TABLE holder (v INTEGER); BEGIN IMMEDIATE; INSERT INTO holder VALUES (1)")
	require.NoError(t, err)
	defer holder.Exec("ROLLBACK")
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	tracker, err := OpenTaskTrackerContext(ctx, path)
	require.Nil(t, tracker)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	const schedulerTolerance = 100 * time.Millisecond
	require.Less(t, time.Since(start), timeouts.ProductionProfile().SQLiteBusy()+schedulerTolerance)
}

func TestUnifiedContextOpenRejectsNilAndCancelled(t *testing.T) {
	t.Parallel()
	tracker, err := OpenTaskTrackerContext(nil, "unused.db")
	require.Nil(t, tracker)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	tracker, err = OpenTaskTrackerContext(ctx, "unused.db")
	require.Nil(t, tracker)
	require.ErrorIs(t, err, context.Canceled)
}

func TestTrackerContextInitializationRejectsCancelledProbe(t *testing.T) {
	t.Parallel()
	db, err := dbconn.OpenSharedDBContextWithProfile(t.Context(), filepath.Join(t.TempDir(), "probe.db"), timeouts.ProductionProfile())
	require.NoError(t, err)
	defer db.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	tracker, err := newTrackerImplContext(ctx, nil, nil, db, true)
	require.Nil(t, tracker)
	require.ErrorIs(t, err, context.Canceled)
}
