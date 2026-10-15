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

// The remaining owner documents activate through the production receipt
// service wiring: the same tasks-built service production deliveries use. Each
// kind journals and resolves through the gated writer the runtime document
// uses; no kind gets a parallel mechanism.
func TestRemainingDefinitionsProductionRoundTrip(t *testing.T) {
	t.Parallel()
	owners := []struct {
		name      string
		kind      model.DefinitionKind
		construct func() (definitions.Document, error)
	}{
		{"schema", model.DefinitionLifecycleSchema, definitions.NewLifecycleSchema},
		{"epoch-implementation", model.DefinitionEpochImplementation, definitions.NewEpochImplementation},
		{"retention", model.DefinitionRetentionPolicy, definitions.NewRetentionPolicy},
		{"interpreter", model.DefinitionInterpreter, definitions.NewInterpreter},
	}
	for _, owner := range owners {
		t.Run(owner.name, func(t *testing.T) {
			t.Parallel()
			document, err := owner.construct()
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
			if ref.Definition <= 0 || ref.Kind != owner.kind || ref.Content != model.ContentIdentity(sha256.Sum256(snapshot.Body())) || !bytes.Equal(snapshot.Body(), document.Body()) || state.LatestState <= model.DefinitionStateID(ref.Definition) {
				t.Fatalf("invalid resolved snapshot/state: %+v %+v", snapshot, state)
			}
		})
	}
}

func TestActiveForContractProductionRoundTrip(t *testing.T) {
	t.Parallel()
	for _, contract := range definitions.RuntimeContracts() {
		t.Run(contract.String(), func(t *testing.T) {
			t.Parallel()
			documents, err := definitions.ActiveForContract(contract)
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
			for _, document := range []definitions.Document{documents.Runtime, documents.Schema, documents.EpochImplementation, documents.Retention, documents.Interpreter} {
				ref, err := receipt.EnsureActiveDefinition(context.Background(), service, document)
				if err != nil {
					t.Fatal(err)
				}
				reader, err := receipt.NewDefinitionReader(tracker.Journal())
				if err != nil {
					t.Fatal(err)
				}
				snapshot, _, err := receipt.ResolveDefinition(reader, ref)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(snapshot.Body(), document.Body()) {
					t.Fatalf("resolved body does not match the contracted owner document for kind %d", document.Kind())
				}
			}
			if err := tracker.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
