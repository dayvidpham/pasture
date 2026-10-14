// lane_host.go assembles the task-side dependencies of the daemon's lifecycle
// write lane.
//
// WHAT LIVES HERE, AND WHY IT IS NOTHING ELSE. The served path needs one
// long-lived store handle, one reusable receipt-service constructor, and the
// gate reader factory. The first two are exactly the two things that must NOT
// be built per request — the whole point of the lane is that the hook firehose
// stops opening the store — so their construction is validated ONCE, here,
// where a reader can see it. The daemon's HTTP-less server, its socket and its
// election are NOT here: they are the daemon's, and this package must stay free
// of any transport.
//
// The handlers package cannot own this assembly because handlers already
// imports tasks (the receipt service and the gate reader are tasks
// constructors); putting the assembly there would invert the dependency. So the
// pieces travel as a value and the daemon joins them.
package tasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// LaneClock is the wall clock the daemon's served receipts are stamped with.
// It is the daemon's own, and the daemon is a long-lived process, so the clock
// is a value with no state and needs no per-request construction.
type LaneClock struct{}

// Now reports the current wall-clock instant in UTC.
func (LaneClock) Now() time.Time { return time.Now().UTC() }

// LaneOperationIDs mints lifecycle operation identities for served records from
// the operating system's random source. It is injected rather than global so a
// test can script it exactly as the direct path's proofs do.
type LaneOperationIDs struct{}

// NewOperationID returns one fresh lifecycle operation identity.
func (LaneOperationIDs) NewOperationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create a served lifecycle operation identity from the operating system random source: %w", err)
	}
	return "pasture.lifecycle.lane." + hex.EncodeToString(value[:]), nil
}

// OpenLifecycleLaneTracker opens the daemon's one long-lived store handle for
// the served lifecycle path.
//
// IT IS THE SAME HANDLE THE HOOK PATH USED TO OPEN PER EVENT. That per-event
// open is what the container diagnosis measured as the convoy's amortised cost
// and what the O(1) ledger relieved but did not remove; serving from one handle
// removes it from the firehose entirely.
func OpenLifecycleLaneTracker(ctx context.Context, dbPath string, profile timeouts.Profile) (protocol.TaskTracker, error) {
	if err := profile.Validate(); err != nil {
		return nil, fmt.Errorf("open the lifecycle lane store: the timeout profile is invalid, so the store's own lock budget cannot be chosen: %w", err)
	}
	tracker, err := OpenTaskTrackerContextWithOptions(ctx, dbPath, WithTimeoutProfile(profile))
	if err != nil {
		return nil, fmt.Errorf("open the lifecycle lane store at %q: %w", dbPath, err)
	}
	return tracker, nil
}

// LifecycleLaneHost is the validated task-side assembly of one daemon's served
// lifecycle path. One instance serves every request for the life of the daemon.
type LifecycleLaneHost struct {
	tracker    protocol.TaskTracker
	clock      receipt.Clock
	operations receipt.OperationIDSource
	profile    timeouts.Profile
}

// NewLifecycleLaneHost validates and returns the assembly. The receipt-service
// construction is exercised once here so a tracker that cannot host lifecycle
// receipts is refused at STARTUP rather than on the first event a user triggers.
func NewLifecycleLaneHost(tracker protocol.TaskTracker, clock receipt.Clock, operations receipt.OperationIDSource, profile timeouts.Profile) (*LifecycleLaneHost, error) {
	if tracker == nil {
		return nil, fmt.Errorf("assemble the lifecycle lane host: no store handle was supplied; the served path reads the gate and commits receipts from one long-lived tracker")
	}
	if clock == nil || operations == nil {
		return nil, fmt.Errorf("assemble the lifecycle lane host: the clock and the operation identity source are both required, because every served receipt needs an attributable time and a distinct identity")
	}
	if err := profile.Validate(); err != nil {
		return nil, fmt.Errorf("assemble the lifecycle lane host: the timeout profile is invalid: %w", err)
	}
	if _, err := NewLifecycleReceiptServiceWithProfile(tracker, clock, operations, profile); err != nil {
		return nil, fmt.Errorf("assemble the lifecycle lane host: this store cannot host lifecycle receipts, so no event could be served: %w", err)
	}
	return &LifecycleLaneHost{tracker: tracker, clock: clock, operations: operations, profile: profile}, nil
}

// Tracker returns the one long-lived store handle.
func (h *LifecycleLaneHost) Tracker() protocol.TaskTracker { return h.tracker }

// Clock returns the clock served receipts are stamped with.
func (h *LifecycleLaneHost) Clock() receipt.Clock { return h.clock }

// Operations returns the operation identity source served receipts use.
func (h *LifecycleLaneHost) Operations() receipt.OperationIDSource { return h.operations }

// Profile returns the ordered timeout profile the served path runs under.
func (h *LifecycleLaneHost) Profile() timeouts.Profile { return h.profile }

// ReceiptService builds the reusable receipt service. It is cheap and may be
// called per request, exactly as the direct path does; every field is immutable
// except the per-call Outcome the served path sets.
func (h *LifecycleLaneHost) ReceiptService() (receipt.Service, error) {
	return NewLifecycleReceiptServiceWithProfile(h.tracker, h.clock, h.operations, h.profile)
}

// NewGateReaderFactory returns the production gate reader factory, bound by
// name so the daemon's wiring is the same constructor the direct path uses:
// tasks.NewGateReader. A fresh reader is built per request because its session
// identity comes from the payload.
func (h *LifecycleLaneHost) NewGateReaderFactory() func(protocol.TaskTracker, ir.HarnessID, string) (gateauthority.Reader, error) {
	return NewGateReader
}

// Journal returns the unified journal the served path writes through, exposed
// so a daemon can report what it is serving without reaching into the tracker.
func (h *LifecycleLaneHost) Journal() provenance.Journal { return h.tracker.Journal() }
