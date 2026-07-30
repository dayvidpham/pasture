package tasks

import (
	"context"
	"database/sql"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/provadapter"
	"github.com/dayvidpham/pasture/pkg/protocol"
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
	if err := validatePersistedGenesisAuthority(t.prov.Journal(), actor, authority); err != nil {
		return receipt.Identity{}, err
	}
	return receipt.Identity{Actor: actor, Authority: authority}, nil
}

type lifecycleReceiptStore interface {
	protocol.TaskTracker
	receipt.IdentityResolver
	auditDBHandle() *sql.DB
}

// NewLifecycleReceiptService wires the production receipt path to the unified
// database and Provenance journal while keeping clocks and operation IDs injected.
func NewLifecycleReceiptService(tracker protocol.TaskTracker, clock receipt.Clock, operations receipt.OperationIDSource) (receipt.Service, error) {
	store, ok := tracker.(lifecycleReceiptStore)
	if !ok || store.auditDBHandle() == nil {
		return receipt.Service{}, &pasterrors.StructuredError{Category: pasterrors.CategoryValidation, What: "The supplied tracker cannot host lifecycle receipts.", Why: "Receipt storage needs the unified SQLite blob handle, Provenance journal, and read-only persisted identity resolver.", Where: "Wiring lifecycle ingress (internal/tasks/lifecycle_identity.go in tasks.NewLifecycleReceiptService).", Impact: "No delivery can be recorded through this service.", Fix: "Use the tracker returned by tasks.OpenTaskTracker."}
	}
	return receipt.Service{Blobs: receipt.SQLiteBlobStore{DB: store.auditDBHandle()}, Appender: receipt.JournalAppender{Journal: store.Journal(), Deadline: receipt.DefaultIngressDeadline, Clock: clock}, Identity: store, Clock: clock, Operations: operations}, nil
}
