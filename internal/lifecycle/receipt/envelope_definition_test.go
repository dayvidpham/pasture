package receipt_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

func TestRuntimeDefinitionsProductionRoundTrip(t *testing.T) {
	t.Parallel()
	for _, contract := range definitions.RuntimeContracts() {
		t.Run(contract.String(), func(t *testing.T) {
			t.Parallel()
			document, err := definitions.NewRuntimeContract(contract)
			if err != nil {
				t.Fatal(err)
			}
			path, cleanup := newBootstrappedTracker(t)
			defer cleanup()
			tracker, err := tasks.OpenTaskTracker(path)
			if err != nil {
				t.Fatal(err)
			}
			service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, gateTestClock{}, &gateTestOperations{}, timeouts.TestProfile())
			if err != nil {
				t.Fatal(err)
			}
			ref, err := receipt.EnsureActiveDefinition(context.Background(), service, document)
			if err != nil {
				t.Fatal(err)
			}
			second, err := receipt.EnsureActiveDefinition(context.Background(), service, document)
			if err != nil || second != ref {
				t.Fatalf("duplicate: %v %v", second, err)
			}
			if err := tracker.Close(); err != nil {
				t.Fatal(err)
			}
			tracker, err = tasks.OpenTaskTracker(path)
			if err != nil {
				t.Fatal(err)
			}
			defer tracker.Close()
			reader, err := receipt.NewDefinitionReader(tracker.Journal())
			if err != nil {
				t.Fatal(err)
			}
			snapshot, state, err := receipt.ResolveDefinition(reader, ref)
			if err != nil {
				t.Fatal(err)
			}
			if ref.Definition <= 0 || ref.Kind != model.DefinitionRuntimeContract || ref.Content != model.ContentIdentity(sha256.Sum256(snapshot.Body())) || !bytes.Equal(snapshot.Body(), document.Body()) || state.LatestState <= model.DefinitionStateID(ref.Definition) {
				t.Fatalf("invalid resolved snapshot/state: %+v %+v", snapshot, state)
			}
		})
	}
}
