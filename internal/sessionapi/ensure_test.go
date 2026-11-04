package sessionapi

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestEnsureFlightWaiters(t *testing.T) {
	db, err := sql.Open("sqlite", "file:ensure-waiters?mode=memory&cache=shared")
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE pasture_session_api_state(singleton_id INTEGER,dirty INTEGER,cache_version INTEGER); INSERT INTO pasture_session_api_state VALUES(0,0,2)`)
	require.NoError(t, err)
	s := NewService(db)
	started := make(chan struct{})
	release := make(chan struct{})
	waiting := make(chan struct{}, 2)
	var count atomic.Int64
	s.ensureWaiting = func() { waiting <- struct{}{} }
	s.Ensure = func(ctx context.Context) error {
		if count.Add(1) == 1 {
			close(started)
			<-release
			return context.Canceled
		}
		return nil
	}
	leader := make(chan error, 1)
	go func() { leader <- s.ensureFresh(context.Background()) }()
	<-started
	waiter := make(chan error, 1)
	go func() { waiter <- s.ensureFresh(context.Background()) }()
	<-waiting
	canceled, cancel := context.WithCancel(context.Background())
	canceledWait := make(chan error, 1)
	go func() { canceledWait <- s.ensureFresh(canceled) }()
	<-waiting
	cancel()
	require.ErrorIs(t, <-canceledWait, context.Canceled)
	close(release)
	require.True(t, errors.Is(<-leader, context.Canceled))
	require.NoError(t, <-waiter)
	require.EqualValues(t, 2, count.Load())
}

func TestEnsureFlightPropagatesFaultAndRechecksDirtyVersion(t *testing.T) {
	for _, mode := range []string{"fault", "dirty", "version"} {
		t.Run(mode, func(t *testing.T) {
			db, err := sql.Open("sqlite", "file:ensure-"+mode+"?mode=memory&cache=shared")
			require.NoError(t, err)
			defer db.Close()
			_, err = db.Exec(`CREATE TABLE pasture_session_api_state(singleton_id INTEGER,dirty INTEGER,cache_version INTEGER); INSERT INTO pasture_session_api_state VALUES(0,0,2)`)
			require.NoError(t, err)
			s := NewService(db)
			started := make(chan struct{})
			release := make(chan struct{})
			waiting := make(chan struct{}, 1)
			fault := errors.New("store fault")
			var calls atomic.Int64
			s.ensureWaiting = func() { waiting <- struct{}{} }
			s.Ensure = func(context.Context) error {
				if calls.Add(1) == 1 {
					close(started)
					<-release
					if mode == "fault" {
						return fault
					}
				}
				return nil
			}
			first := make(chan error, 1)
			go func() { first <- s.ensureFresh(context.Background()) }()
			<-started
			if mode == "dirty" {
				_, err = db.Exec(`UPDATE pasture_session_api_state SET dirty=1`)
				require.NoError(t, err)
			}
			if mode == "version" {
				_, err = db.Exec(`UPDATE pasture_session_api_state SET cache_version=3`)
				require.NoError(t, err)
			}
			second := make(chan error, 1)
			go func() { second <- s.ensureFresh(context.Background()) }()
			<-waiting
			close(release)
			a, b := <-first, <-second
			if mode == "fault" {
				require.ErrorIs(t, a, fault)
				require.ErrorIs(t, b, fault)
				require.EqualValues(t, 1, calls.Load())
			} else {
				require.NoError(t, a)
				require.NoError(t, b)
				require.EqualValues(t, 2, calls.Load())
			}
		})
	}
}
