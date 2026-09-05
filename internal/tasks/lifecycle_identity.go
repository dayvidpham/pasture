package tasks

import (
	"context"
	"database/sql"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/provadapter"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

// ResolveLifecycleIdentity reads the already-persisted ingress actor and
// authority without invoking actor activation or acquiring a write lock.
func (t *trackerImpl) ResolveLifecycleIdentity(context.Context) (receipt.Identity, error) {
	if err := t.ensurePastureTablesOnce(); err != nil {
		return receipt.Identity{}, err
	}
	actor, authority, found, err := readSystemIdentity(t.auditDB)
	if err != nil {
		return receipt.Identity{}, err
	}
	if !found {
		return receipt.Identity{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Lifecycle ingress cannot resolve Pasture's persisted system identity.",
			Why:      "Receipt writes are read-only with respect to actor activation, and the required bootstrap identity has not been established yet.",
			Where:    "Resolving lifecycle ingress identity (internal/tasks/lifecycle_identity.go in tasks.ResolveLifecycleIdentity).",
			Impact:   "No lifecycle occurrence was committed; an already stored payload may remain as a reclaimable orphan.",
			Fix:      "Run a normal Pasture command once to initialize the unified store, then retry the host delivery.",
		}
	}
	expected := provadapter.PastureSystemDefaultActorID()
	if actor != expected {
		return receipt.Identity{}, &pasterrors.StructuredError{Category: pasterrors.CategoryStorage, What: "Lifecycle ingress found an unexpected persisted system actor.", Why: fmt.Sprintf("The store names %q but this build requires %q.", actor.String(), expected.String()), Where: "Resolving lifecycle ingress identity (internal/tasks/lifecycle_identity.go in tasks.ResolveLifecycleIdentity).", Impact: "No lifecycle occurrence was committed.", Fix: "Inspect the pasture_system_identity row and repair it only through a reviewed migration."}
	}
	if err := validatePersistedGenesisAuthorityReadOnly(t.prov.Journal(), authority); err != nil {
		return receipt.Identity{}, err
	}
	return receipt.Identity{Actor: actor, Authority: authority}, nil
}

func validatePersistedGenesisAuthorityReadOnly(j provenance.Journal, authority provenance.JournalID) error {
	committed, err := j.LookupCommitted(pastureSystemGenesisOperationID)
	if err != nil {
		return err
	}
	if committed.Kind != provenance.CommittedExact {
		return fmt.Errorf("persisted lifecycle identity cites absent genesis operation %q", pastureSystemGenesisOperationID)
	}
	for _, slot := range committed.ResultSlots {
		if slot.Slot == pastureSystemGenesisResultSlot && slot.ProducedJournalID == authority {
			return nil
		}
	}
	return fmt.Errorf("persisted lifecycle identity authority %d does not match genesis result", authority)
}

// NewLifecycleReader returns the public bounded lifecycle reader backed by the
// disposable replay-derived projection.
func NewLifecycleReader(tracker protocol.TaskTracker) (model.LifecycleReader, error) {
	store, ok := tracker.(lifecycleReceiptStore)
	if !ok || store.auditDBHandle() == nil {
		return nil, &pasterrors.StructuredError{Category: pasterrors.CategoryValidation, What: "The supplied tracker cannot serve lifecycle reads.", Why: "The bounded reader needs the unified projection database handle.", Where: "Wiring lifecycle reads (internal/tasks/lifecycle_identity.go in tasks.NewLifecycleReader).", Impact: "No lifecycle records were read.", Fix: "Use the tracker returned by tasks.OpenTaskTracker."}
	}
	return projection.Reader{DB: store.auditDBHandle(), Facts: store.Journal().Facts()}, nil
}

// NewLifecycleBlobStore binds the payload blob store to the unified database
// for READ-ONLY inspection. It is the seam an operator read surface uses to
// ask how many payload blobs no occurrence names; it grants no write path that
// receipt.Service does not already own.
func NewLifecycleBlobStore(tracker protocol.TaskTracker) (receipt.SQLiteBlobStore, error) {
	store, ok := tracker.(lifecycleReceiptStore)
	if !ok || store.auditDBHandle() == nil {
		return receipt.SQLiteBlobStore{}, &pasterrors.StructuredError{Category: pasterrors.CategoryValidation, What: "The supplied tracker cannot inspect lifecycle payload blobs.", Why: "Blob inspection needs the unified SQLite handle that the tracker holds.", Where: "Wiring lifecycle payload inspection (internal/tasks/lifecycle_identity.go in tasks.NewLifecycleBlobStore).", Impact: "No payload blob was inspected or changed.", Fix: "Use the tracker returned by tasks.OpenTaskTracker."}
	}
	return receipt.SQLiteBlobStore{DB: store.auditDBHandle()}, nil
}

// RebuildLifecycleOccurrences derives the disposable occurrence projection
// exclusively from journal truth.
func RebuildLifecycleOccurrences(ctx context.Context, tracker protocol.TaskTracker) error {
	store, ok := tracker.(lifecycleReceiptStore)
	if !ok || store.auditDBHandle() == nil {
		return &pasterrors.StructuredError{Category: pasterrors.CategoryValidation, What: "The supplied tracker cannot rebuild lifecycle occurrences.", Why: "Projection replay needs the unified journal and database handle.", Where: "Wiring lifecycle replay (internal/tasks/lifecycle_identity.go in tasks.RebuildLifecycleOccurrences).", Impact: "No projection rows were changed.", Fix: "Use the tracker returned by tasks.OpenTaskTracker."}
	}
	return projection.RebuildOccurrences(ctx, store.Journal(), store.auditDBHandle())
}

type lifecycleReceiptStore interface {
	protocol.TaskTracker
	receipt.IdentityResolver
	auditDBHandle() *sql.DB
}

// NewLifecycleReceiptService wires the production receipt path to the unified
// database and Provenance journal while keeping clocks and operation IDs injected.
func NewLifecycleReceiptService(tracker protocol.TaskTracker, clock receipt.Clock, operations receipt.OperationIDSource) (receipt.Service, error) {
	return NewLifecycleReceiptServiceWithProfile(tracker, clock, operations, timeouts.ProductionProfile())
}

func NewLifecycleReceiptServiceWithProfile(tracker protocol.TaskTracker, clock receipt.Clock, operations receipt.OperationIDSource, profile timeouts.Profile) (receipt.Service, error) {
	if err := profile.Validate(); err != nil {
		return receipt.Service{}, &pasterrors.StructuredError{Category: pasterrors.CategoryValidation, What: "The lifecycle receipt timeout profile is invalid.", Why: err.Error(), Where: "Wiring lifecycle ingress (internal/tasks/lifecycle_identity.go in tasks.NewLifecycleReceiptServiceWithProfile).", Impact: "No delivery can be recorded through this service.", Fix: "Use a constructor-validated production or test timeout profile."}
	}
	store, ok := tracker.(lifecycleReceiptStore)
	if !ok || store.auditDBHandle() == nil {
		return receipt.Service{}, &pasterrors.StructuredError{Category: pasterrors.CategoryValidation, What: "The supplied tracker cannot host lifecycle receipts.", Why: "Receipt storage needs the unified SQLite blob handle, Provenance journal, and read-only persisted identity resolver.", Where: "Wiring lifecycle ingress (internal/tasks/lifecycle_identity.go in tasks.NewLifecycleReceiptService).", Impact: "No delivery can be recorded through this service.", Fix: "Use the tracker returned by tasks.OpenTaskTracker."}
	}
	return receipt.Service{Window: profile.WorkflowResult(), Blobs: receipt.SQLiteBlobStore{DB: store.auditDBHandle()}, Appender: receipt.JournalAppender{Journal: store.Journal(), Deadline: profile.Ingress(), Clock: clock}, Identity: store, Clock: clock, Operations: operations}, nil
}
