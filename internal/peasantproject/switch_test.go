package peasantproject

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubSource struct {
	mu       sync.Mutex
	status   Status
	projects map[Key]Project
	lookups  int
	closed   int
}

func newStubSource() *stubSource {
	return &stubSource{
		status: Status{Enabled: true, Source: SourceDB, State: StateOK},
		projects: map[Key]Project{
			{Harness: "claude-code", Session: "s"}: {Name: "org/repo", Hash: "hash"},
		},
	}
}

func (s *stubSource) Lookup(_ context.Context, keys []Key) (map[Key]Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookups++
	out := make(map[Key]Project, len(keys))
	for _, key := range keys {
		if project, ok := s.projects[key]; ok {
			out[key] = project
		}
	}
	return out, nil
}

func (s *stubSource) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *stubSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed++
	return nil
}

func (s *stubSource) counters() (lookups, closed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookups, s.closed
}

func TestSwitchStartsDisabledAndNeverTouchesTheOpener(t *testing.T) {
	t.Parallel()

	opens := 0
	stub := newStubSource()
	sw := NewSwitch(func(context.Context) (Source, error) {
		opens++
		return stub, nil
	}, nil)
	t.Cleanup(func() { _ = sw.Close() })

	status := sw.Status()
	require.False(t, status.Enabled)
	require.Equal(t, SourceDisabled, status.Source)
	require.Empty(t, status.State, "off is disabled, never unavailable")
	require.Contains(t, status.Message, "turn it on from the web console")

	projects, err := sw.Lookup(context.Background(), []Key{{Harness: "claude-code", Session: "s"}})
	require.NoError(t, err)
	require.Empty(t, projects)
	require.Zero(t, opens, "lookup while off must not open the source")
	lookups, closed := stub.counters()
	require.Zero(t, lookups)
	require.Zero(t, closed)
}

func TestSwitchEnableDisableReopensLazily(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var sources []*stubSource
	sw := NewSwitch(func(context.Context) (Source, error) {
		mu.Lock()
		defer mu.Unlock()
		source := newStubSource()
		sources = append(sources, source)
		return source, nil
	}, nil)
	t.Cleanup(func() { _ = sw.Close() })
	ctx := context.Background()

	require.NoError(t, sw.SetEnabled(ctx, true))
	require.True(t, sw.Status().Enabled)
	projects, err := sw.Lookup(ctx, []Key{{Harness: "claude-code", Session: "s"}})
	require.NoError(t, err)
	require.Equal(t, "org/repo", projects[Key{Harness: "claude-code", Session: "s"}].Name)
	mu.Lock()
	require.Len(t, sources, 1)
	first := sources[0]
	mu.Unlock()

	require.NoError(t, sw.SetEnabled(ctx, false))
	status := sw.Status()
	require.False(t, status.Enabled)
	require.Equal(t, SourceDisabled, status.Source)
	_, closed := first.counters()
	require.Equal(t, 1, closed, "turning off must close the open source")
	_, err = sw.Lookup(ctx, []Key{{Harness: "claude-code", Session: "s"}})
	require.NoError(t, err)
	lookups, _ := first.counters()
	require.Equal(t, 1, lookups, "lookup while off must not touch the closed source")

	require.NoError(t, sw.SetEnabled(ctx, true))
	mu.Lock()
	require.Len(t, sources, 2, "turning on again must open a fresh source")
	mu.Unlock()
	require.True(t, sw.Status().Enabled)
}

func TestSwitchEnableErrorStaysOff(t *testing.T) {
	t.Parallel()

	openErr := errors.New("no Peasant database was found at /tmp/peasant.db")
	sw := NewSwitch(func(context.Context) (Source, error) {
		return nil, openErr
	}, nil)
	t.Cleanup(func() { _ = sw.Close() })

	err := sw.SetEnabled(context.Background(), true)
	require.ErrorIs(t, err, openErr)
	status := sw.Status()
	require.False(t, status.Enabled)
	require.Equal(t, SourceDisabled, status.Source)
}

func TestSwitchNilOpenerResultStaysOff(t *testing.T) {
	t.Parallel()

	sw := NewSwitch(func(context.Context) (Source, error) { return nil, nil }, nil)
	t.Cleanup(func() { _ = sw.Close() })
	require.Error(t, sw.SetEnabled(context.Background(), true))
	require.False(t, sw.Status().Enabled)
}

func TestSwitchCloseIsIdempotentAndRefusesFurtherChanges(t *testing.T) {
	t.Parallel()

	stub := newStubSource()
	sw := NewSwitch(nil, stub)
	require.NoError(t, sw.Close())
	_, closed := stub.counters()
	require.Equal(t, 1, closed)
	require.NoError(t, sw.Close(), "a second close is harmless")
	_, closed = stub.counters()
	require.Equal(t, 1, closed)
	require.ErrorIs(t, sw.SetEnabled(context.Background(), true), ErrSwitchClosed)
	require.False(t, sw.Status().Enabled)
}

func TestSwitchSetEnabledWhileAlreadyOnIsANoOp(t *testing.T) {
	t.Parallel()

	stub := newStubSource()
	opens := 0
	sw := NewSwitch(func(context.Context) (Source, error) {
		opens++
		return stub, nil
	}, stub)
	t.Cleanup(func() { _ = sw.Close() })
	require.NoError(t, sw.SetEnabled(context.Background(), true))
	require.Zero(t, opens, "an already-open source is not reopened")
	_, closed := stub.counters()
	require.Zero(t, closed)
}

func TestSwitchConcurrentLookupsAndToggles(t *testing.T) {
	t.Parallel()

	sw := NewSwitch(func(context.Context) (Source, error) { return newStubSource(), nil }, nil)
	t.Cleanup(func() { _ = sw.Close() })
	ctx := context.Background()
	keys := []Key{{Harness: "claude-code", Session: "s"}}

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = sw.Lookup(ctx, keys)
		}()
		go func() {
			defer wg.Done()
			_ = sw.SetEnabled(ctx, true)
			_ = sw.SetEnabled(ctx, false)
		}()
	}
	wg.Wait()
	// The only guarantee while toggling is that it stays usable.
	require.NotPanics(t, func() { _, _ = sw.Lookup(ctx, keys) })
}

func TestDefaultDBPathUsesXDGThenHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	require.Equal(t, filepath.Join(tmp, "peasant", "peasant.db"), DefaultDBPath())

	t.Setenv("XDG_DATA_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.Equal(t, filepath.Join(home, ".local", "share", "peasant", "peasant.db"), DefaultDBPath())
}
