package receipt_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

const definitionEvidenceKind = provenance.EvidenceKind("pasture.lifecycle.definition.v1")

// incrementingClock returns a distinct wall-clock stamp on every call. Using it
// in the concurrency proof shows that a DIFFERING operation-level RecordedAt
// (audit/display only, not part of the replay identity) still collapses two
// racing first activations into ONE committed operation.
type incrementingClock struct {
	mu sync.Mutex
	n  int64
}

func (c *incrementingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return time.Unix(1000+c.n, c.n).UTC()
}

func newBootstrappedTracker(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pasture.db")
	bootstrap, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open bootstrap tracker: %v", err)
	}
	if _, err := bootstrap.Create("file://definition-test", "bootstrap", "initialize the persisted ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
		t.Fatalf("bootstrap identity: %v", err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatalf("close bootstrap tracker: %v", err)
	}
	return dbPath, func() {}
}

func activationSnapshotCount(t *testing.T, tracker protocol.TaskTracker) int {
	t.Helper()
	page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  []provenance.EvidenceKind{definitionEvidenceKind},
		Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
	})
	if err != nil {
		t.Fatalf("query definition evidence: %v", err)
	}
	return len(page.Rows)
}

// TestEnsureActiveMetamodelIdempotent proves the steady-state path: a second
// EnsureActiveMetamodel resolves the SAME journaled definition and commits no new
// activation operation.
func TestEnsureActiveMetamodelIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath, cleanup := newBootstrappedTracker(t)
	defer cleanup()

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open tracker: %v", err)
	}
	defer tracker.Close()
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, gateTestClock{}, &gateTestOperations{}, timeouts.TestProfile())
	if err != nil {
		t.Fatalf("wire production receipt service: %v", err)
	}

	first, err := receipt.EnsureActiveMetamodel(ctx, service)
	if err != nil {
		t.Fatalf("first EnsureActiveMetamodel: %v", err)
	}
	if first.Definition.Definition == 0 || first.Definition.Kind != model.DefinitionMetamodel || first.Definition.Content != metamodel.Active().Content {
		t.Fatalf("first ensured ref = %#v, want a journaled metamodel definition with the active content", first)
	}
	second, err := receipt.EnsureActiveMetamodel(ctx, service)
	if err != nil {
		t.Fatalf("second EnsureActiveMetamodel: %v", err)
	}
	if second != first {
		t.Fatalf("second ensured ref = %#v, want identical to first %#v (idempotent)", second, first)
	}
	if got := activationSnapshotCount(t, tracker); got != 1 {
		t.Fatalf("committed %d definition snapshots after two ensures, want exactly 1", got)
	}
}

// TestEnsureActiveMetamodelRaceSafe is the STOP-1 benign-already-activated proof:
// N goroutines racing the FIRST activation (each with a distinct wall-clock
// RecordedAt) commit EXACTLY ONE definition-activation operation. Every caller
// resolves the same journaled definition and none errors — the deterministic
// content-derived operation identity makes the loser short-circuit benignly
// rather than raising ErrOperationConflict.
func TestEnsureActiveMetamodelRaceSafe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath, cleanup := newBootstrappedTracker(t)
	defer cleanup()

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open tracker: %v", err)
	}
	defer tracker.Close()
	// This proof races `goroutines` first-activations against ONE SQLite pool.
	// TestProfile()'s 500ms inner busy timeout (and 2s caller ingress) bound how
	// long a goroutine may wait merely to LEASE a connection; under CI
	// shared-runner CPU contention a straggler can exceed that window and surface
	// "caller deadline expired while leasing a SQLite connection" even though the
	// activation itself is correct — a lease-timing artifact, not a logic bug.
	// Widen the busy/ingress/start-slice windows for THIS lease-contention proof
	// only, keeping the required busy < ingress < start_slice ordering. Production
	// ProductionProfile() is untouched, and the STOP-1 assertions below (exactly
	// one committed op, one shared ref, no errors) are unchanged.
	raceProfile, err := timeouts.New(timeouts.Test, 5*time.Second, 10*time.Second, 15*time.Second, 60*time.Second)
	if err != nil {
		t.Fatalf("construct contention-tolerant timeout profile: %v", err)
	}
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, &incrementingClock{}, &gateTestOperations{}, raceProfile)
	if err != nil {
		t.Fatalf("wire production receipt service: %v", err)
	}

	const goroutines = 8
	refs := make([]model.LifecycleMetamodelRef, goroutines)
	errs := make([]error, goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			refs[i], errs[i] = receipt.EnsureActiveMetamodel(ctx, service)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d EnsureActiveMetamodel returned an error (want benign-already-activated): %v", i, err)
		}
		if refs[i] != refs[0] {
			t.Fatalf("goroutine %d resolved ref %#v, want the single shared definition %#v", i, refs[i], refs[0])
		}
	}
	if refs[0].Definition.Definition == 0 {
		t.Fatalf("racing ensures resolved a zero definition journal id: %#v", refs[0])
	}
	if got := activationSnapshotCount(t, tracker); got != 1 {
		t.Fatalf("racing first deliveries committed %d definition-activation operations, want EXACTLY 1 (benign-already-activated)", got)
	}
}
