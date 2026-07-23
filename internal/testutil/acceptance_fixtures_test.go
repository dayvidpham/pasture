package testutil

import (
	"testing"

	"github.com/dayvidpham/provenance"
)

func TestAcceptanceStoreUsesProductionFileBackedOpenerAndReopens(t *testing.T) {
	store := OpenAcceptanceStore(t)
	created, err := store.Tracker.Create("acceptance-fixture", "persisted", "production API", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	store.Close(t)
	store.Reopen(t)
	got, err := store.Tracker.Show(created.ID)
	if err != nil {
		t.Fatalf("Show after reopen: %v", err)
	}
	if got.ID != created.ID || got.Title != "persisted" {
		t.Fatalf("reopened task = %#v, want %#v", got, created)
	}
}
