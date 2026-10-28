package tasks_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"
)

func TestPublishedProvenanceFixedRegistrationContextCapability(t *testing.T) {
	t.Parallel()
	tracker, err := provenance.OpenSQLite(filepath.Join(t.TempDir(), "provenance.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tracker.Close() })
	registrar, ok := tracker.(provenance.FixedSoftwareAgentRegistrarContext)
	if !ok {
		t.Fatal("published SQLite tracker lacks context-aware fixed registration")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = registrar.RegisterFixedSoftwareAgentContext(ctx, provenance.FixedSoftwareAgentRegistration{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled registration: got %v, want context.Canceled", err)
	}
}
