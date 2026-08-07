package receipt_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/lifecycle/codebook"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/tasks"
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

// TestEnsureActiveCodebookIdempotent proves the steady-state path: a second
// EnsureActiveCodebook resolves the SAME journaled definition and commits no new
// activation operation.
func TestEnsureActiveCodebookIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath, cleanup := newBootstrappedTracker(t)
	defer cleanup()

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open tracker: %v", err)
	}
	defer tracker.Close()
	service, err := tasks.NewLifecycleReceiptService(tracker, gateTestClock{}, &gateTestOperations{})
	if err != nil {
		t.Fatalf("wire production receipt service: %v", err)
	}

	first, err := receipt.EnsureActiveCodebook(ctx, service)
	if err != nil {
		t.Fatalf("first EnsureActiveCodebook: %v", err)
	}
	if first.Definition.Definition == 0 || first.Definition.Kind != model.DefinitionCodebook || first.Definition.Content != codebook.Active().Content {
		t.Fatalf("first ensured ref = %#v, want a journaled codebook definition with the active content", first)
	}
	second, err := receipt.EnsureActiveCodebook(ctx, service)
	if err != nil {
		t.Fatalf("second EnsureActiveCodebook: %v", err)
	}
	if second != first {
		t.Fatalf("second ensured ref = %#v, want identical to first %#v (idempotent)", second, first)
	}
	if got := activationSnapshotCount(t, tracker); got != 1 {
		t.Fatalf("committed %d definition snapshots after two ensures, want exactly 1", got)
	}
}

// TestEnsureActiveCodebookRaceSafe is the STOP-1 benign-already-activated proof:
// N goroutines racing the FIRST activation (each with a distinct wall-clock
// RecordedAt) commit EXACTLY ONE definition-activation operation. Every caller
// resolves the same journaled definition and none errors — the deterministic
// content-derived operation identity makes the loser short-circuit benignly
// rather than raising ErrOperationConflict.
func TestEnsureActiveCodebookRaceSafe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath, cleanup := newBootstrappedTracker(t)
	defer cleanup()

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open tracker: %v", err)
	}
	defer tracker.Close()
	service, err := tasks.NewLifecycleReceiptService(tracker, &incrementingClock{}, &gateTestOperations{})
	if err != nil {
		t.Fatalf("wire production receipt service: %v", err)
	}

	const goroutines = 8
	refs := make([]model.CodebookDefinitionRef, goroutines)
	errs := make([]error, goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			refs[i], errs[i] = receipt.EnsureActiveCodebook(ctx, service)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d EnsureActiveCodebook returned an error (want benign-already-activated): %v", i, err)
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
