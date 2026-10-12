package tasks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestTrackerWaitingCallerWriteGateCounterexample(t *testing.T) {
	t.Parallel()
	opened, err := OpenTaskTrackerContext(t.Context(), filepath.Join(t.TempDir(), "gate.db"))
	require.NoError(t, err)
	tracker := opened.(*trackerImpl)
	tracker.writeMu.Lock()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	released := false
	var attachErr error
	t.Cleanup(func() {
		cancel()
		if !released {
			tracker.writeMu.Unlock()
		}
		<-done
		_ = tracker.Close()
	})
	cancel()
	go func() {
		defer close(done)
		attachErr = tracker.AttachContext(ctx, 1, protocol.ContextKind("epoch"), "context")
	}()
	const schedulingTolerance = 75 * time.Millisecond
	select {
	case <-done:
	case <-time.After(schedulingTolerance):
		tracker.writeMu.Unlock()
		released = true
		<-done
		t.Fatal("AttachContext(ctx) stayed blocked on tracker write gate despite cancelled caller")
	}
	require.ErrorIs(t, attachErr, context.Canceled)
}

type gateCountingTrail struct {
	audit.Trail
	delegations atomic.Int32
}

func (s *gateCountingTrail) RecordEvent(context.Context, protocol.AuditEvent) error {
	s.delegations.Add(1)
	return errors.New("unexpected cancelled event delegation")
}
func (s *gateCountingTrail) RecordEventReturningId(context.Context, protocol.AuditEvent) (int64, error) {
	s.delegations.Add(1)
	return 0, errors.New("unexpected cancelled event-ID delegation")
}
func (s *gateCountingTrail) RecordSessionEntries(context.Context, []protocol.SessionEntry) error {
	s.delegations.Add(1)
	return errors.New("unexpected cancelled batch delegation")
}

func TestTrackerContextMethodsCancelAtWriteGateWithoutDelegation(t *testing.T) {
	type gateFixtureName string
	const fixture gateFixtureName = "write_gate_context"
	var cases []struct {
		Name   string `yaml:"name"`
		Method string `yaml:"method"`
	}
	// testutil imports tasks for acceptance fixtures. Preserve its typed names,
	// strict decoding and fail-fast semantics locally to avoid an import cycle.
	file, err := os.Open(filepath.Join("testdata", string(fixture)+".yaml"))
	require.NoError(t, err, "open write-gate context fixture")
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	require.NoError(t, decoder.Decode(&cases), "decode write-gate context fixture")
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			trail := &gateCountingTrail{}
			tracker := &trackerImpl{trail: trail}
			tracker.writeMu.Lock()
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			entered := make(chan struct{})
			var callErr error
			released := false
			t.Cleanup(func() {
				cancel()
				if !released {
					tracker.writeMu.Unlock()
				}
				<-done
			})
			go func() {
				defer close(done)
				close(entered)
				switch tc.Method {
				case "event":
					callErr = tracker.RecordEvent(ctx, protocol.AuditEvent{})
				case "event_id":
					var id int64
					id, callErr = tracker.RecordEventReturningId(ctx, protocol.AuditEvent{})
					if id != 0 {
						callErr = errors.New("cancelled gate returned an event ID")
					}
				case "sessions":
					callErr = tracker.RecordSessionEntries(ctx, nil)
				case "attach":
					callErr = tracker.AttachContext(ctx, 1, protocol.ContextKind("epoch"), "context")
				case "transfer":
					_, callErr = tracker.TransferTaskAssignment(ctx, protocol.TransferTaskAssignmentRequest{})
				default:
					callErr = errors.New("unknown write gate fixture method")
				}
			}()
			<-entered
			cancel()
			const schedulingTolerance = 150 * time.Millisecond
			select {
			case <-done:
			case <-time.After(schedulingTolerance):
				tracker.writeMu.Unlock()
				released = true
				<-done
				t.Fatal("cancelled context method waited for the active writer to release the gate")
			}
			require.ErrorIs(t, callErr, context.Canceled)
			require.Zero(t, trail.delegations.Load())
		})
	}
}

func TestWriteGateCancellationDoesNotConsumeAnUnlockedToken(t *testing.T) {
	t.Parallel()
	tracker := &trackerImpl{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	unlock, err := tracker.lockWriteContext(ctx)
	require.Nil(t, unlock)
	require.ErrorIs(t, err, context.Canceled)
	live, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	unlock, err = tracker.lockWriteContext(live)
	require.NoError(t, err)
	unlock()
	// Compatibility acquisition uses the identical gate after a cancelled
	// caller, and releases it for the next context-bearing acquisition.
	tracker.lockWrite()()
	unlock, err = tracker.lockWriteContext(live)
	require.NoError(t, err)
	unlock()
	unlock, err = tracker.lockWriteContext(nil)
	require.Nil(t, unlock)
	require.Error(t, err)
}

func TestWriteGateDeadlineWhileAnotherWriterRetainsOwnership(t *testing.T) {
	t.Parallel()
	tracker := &trackerImpl{}
	tracker.writeMu.Lock()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	done := make(chan struct{})
	var gateErr error
	var unlock func()
	released := false
	t.Cleanup(func() {
		cancel()
		if !released {
			tracker.writeMu.Unlock()
		}
		<-done
	})
	go func() { defer close(done); unlock, gateErr = tracker.lockWriteContext(ctx) }()
	const schedulingTolerance = 150 * time.Millisecond
	select {
	case <-done:
	case <-time.After(25*time.Millisecond + schedulingTolerance):
		tracker.writeMu.Unlock()
		released = true
		<-done
		t.Fatal("write gate did not observe the caller deadline while its owner remained active")
	}
	require.Nil(t, unlock)
	require.ErrorIs(t, gateErr, context.DeadlineExceeded)
	tracker.writeMu.Unlock()
	released = true
	live, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	unlock, gateErr = tracker.lockWriteContext(live)
	require.NoError(t, gateErr)
	unlock()
}
