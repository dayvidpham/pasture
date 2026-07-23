package testutil

import (
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

type AcceptanceStore struct {
	Path    string
	Tracker protocol.TaskTracker
}

// OpenAcceptanceStore creates a file-backed store through the production opener.
// Callers seed it through Tracker APIs; this helper intentionally exposes no SQL.
func OpenAcceptanceStore(t *testing.T) *AcceptanceStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(path)
	if err != nil {
		t.Fatalf("OpenAcceptanceStore: production OpenTaskTracker(%q) failed: %v", path, err)
	}
	store := &AcceptanceStore{Path: path, Tracker: tracker}
	t.Cleanup(func() {
		if store.Tracker != nil {
			if err := store.Tracker.Close(); err != nil {
				t.Errorf("OpenAcceptanceStore cleanup: close %q: %v", path, err)
			}
		}
	})
	return store
}

func (s *AcceptanceStore) Close(t *testing.T) {
	t.Helper()
	if s.Tracker == nil {
		return
	}
	if err := s.Tracker.Close(); err != nil {
		t.Fatalf("AcceptanceStore.Close(%q): %v", s.Path, err)
	}
	s.Tracker = nil
}

func (s *AcceptanceStore) Reopen(t *testing.T) {
	t.Helper()
	if s.Tracker != nil {
		t.Fatalf("AcceptanceStore.Reopen(%q): store is still open; close it first so restart behavior is authentic", s.Path)
	}
	tracker, err := tasks.OpenTaskTracker(s.Path)
	if err != nil {
		t.Fatalf("AcceptanceStore.Reopen: production OpenTaskTracker(%q) failed: %v", s.Path, err)
	}
	s.Tracker = tracker
}
