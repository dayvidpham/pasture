package receipt_test

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/provenance"
)

const envelopeStateEvidenceKind = provenance.EvidenceKind("pasture.lifecycle.definition-state.v2")

type countingDefinitionReader struct {
	receipt.DefinitionReader
	receipt.DirectEvidenceReader
	queryCalls int
}

func (r *countingDefinitionReader) QueryEvidence(query provenance.EvidenceQuery) (provenance.EvidencePage, error) {
	r.queryCalls++
	return r.DefinitionReader.QueryEvidence(query)
}

func TestOpenRepairsStaleActiveEntityProjection(t *testing.T) {
	ctx := context.Background()
	dbPath, cleanup := newBootstrappedTracker(t)
	defer cleanup()

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open tracker: %v", err)
	}
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, gateTestClock{}, &gateTestOperations{}, timeouts.TestProfile())
	if err != nil {
		t.Fatalf("wire receipt service: %v", err)
	}
	document, err := definitions.NewRuntimeContract(definitions.RuntimeContracts()[0])
	if err != nil {
		t.Fatalf("build runtime document: %v", err)
	}
	if _, err := receipt.EnsureActiveDefinition(ctx, service, document); err != nil {
		t.Fatalf("ensure active definition: %v", err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close tracker: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open projection database: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM lifecycle_active_entity; UPDATE lifecycle_active_projection SET applied_journal_id = 0`); err != nil {
		_ = db.Close()
		t.Fatalf("clear active projection: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close projection database: %v", err)
	}

	// Reopening runs the production startup repair from journal truth.
	reopened, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("reopen tracker: %v", err)
	}
	defer reopened.Close()
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen projection database: %v", err)
	}
	defer db.Close()
	active := receipt.SQLiteActiveEntityStore{DB: db}
	entity, found, err := active.Lookup(ctx, document.Kind(), document.Content())
	if err != nil || !found {
		t.Fatalf("startup repair did not restore the active row: found=%v err=%v", found, err)
	}
	if entity.Definition <= 0 || entity.State <= 0 {
		t.Fatalf("startup repair row = %+v, want positive journal coordinates", entity)
	}
	needs, err := active.NeedsRebuild(ctx)
	if err != nil {
		t.Fatalf("check startup repair freshness: %v", err)
	}
	if needs {
		t.Fatal("startup repair left the projection stale")
	}
}

func TestBridgeActiveMetamodelUsesDirectEvidenceOnHotPath(t *testing.T) {
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
		t.Fatalf("wire receipt service: %v", err)
	}
	if _, err := receipt.EnsureActiveMetamodel(ctx, service); err != nil {
		t.Fatalf("ensure active metamodel: %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open direct database: %v", err)
	}
	defer db.Close()
	definitionsReader, err := receipt.NewDefinitionReader(tracker.Journal())
	if err != nil {
		t.Fatalf("definition reader: %v", err)
	}
	reader := &countingDefinitionReader{
		DefinitionReader:     definitionsReader,
		DirectEvidenceReader: receipt.SQLiteEvidenceReader{DB: db},
	}
	ref, _, _, err := receipt.BridgeActiveMetamodel(ctx, reader)
	if err != nil {
		t.Fatalf("bridge active metamodel: %v", err)
	}
	if reader.queryCalls != 0 {
		t.Fatalf("bridge used %d generic evidence pages, want 0 direct primary-key reads", reader.queryCalls)
	}
	if ref.Definition.Definition <= 0 || ref.Definition.Kind == 0 {
		t.Fatalf("bridged ref = %+v, want a positive definition and kind", ref.Definition)
	}
}

func TestActiveEntityProjectionPopulatesOnActivationAndRebuilds(t *testing.T) {
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
		t.Fatalf("wire receipt service: %v", err)
	}
	document, err := definitions.NewRuntimeContract(definitions.RuntimeContracts()[0])
	if err != nil {
		t.Fatalf("build runtime document: %v", err)
	}
	ref, err := receipt.EnsureActiveDefinition(ctx, service, document)
	if err != nil {
		t.Fatalf("ensure active definition: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open projection database: %v", err)
	}
	defer db.Close()
	active := receipt.SQLiteActiveEntityStore{DB: db}

	entity, found, err := active.Lookup(ctx, document.Kind(), document.Content())
	if err != nil {
		t.Fatalf("lookup active projection: %v", err)
	}
	if !found {
		t.Fatal("activation did not populate the active projection")
	}
	if entity.Definition != ref.Definition || entity.Content != document.Content() || entity.Operation == "" || entity.State <= 0 {
		t.Fatalf("projection row = %+v, want definition %d content %x with an operation and state", entity, ref.Definition, document.Content())
	}

	// The direct reader must fetch the exact rows without the generic fact
	// page. This is the slice-1 hot path the projection sits beside.
	direct := receipt.SQLiteEvidenceReader{DB: db}
	definitionRow, found, err := direct.EvidenceAt(ctx, definitions.EvidenceKind, provenance.JournalID(ref.Definition))
	if err != nil || !found {
		t.Fatalf("direct evidence-at: found=%v err=%v", found, err)
	}
	if definitionRow.JournalID != provenance.JournalID(ref.Definition) || definitionRow.ProducingOperationID != entity.Operation {
		t.Fatalf("direct definition row = %+v, want journal %d operation %q", definitionRow, ref.Definition, entity.Operation)
	}
	stateRow, found, err := direct.EvidenceByOperation(ctx, envelopeStateEvidenceKind, entity.Operation)
	if err != nil || !found {
		t.Fatalf("direct evidence-by-operation: found=%v err=%v", found, err)
	}
	if stateRow.JournalID != provenance.JournalID(entity.State) {
		t.Fatalf("direct state row journal = %d, want %d", stateRow.JournalID, entity.State)
	}

	// Remove the projection and force the journal-backed rebuild.
	if _, err := db.ExecContext(ctx, `DELETE FROM lifecycle_active_entity; UPDATE lifecycle_active_projection SET applied_journal_id = 0`); err != nil {
		t.Fatalf("clear active projection: %v", err)
	}
	needs, err := active.NeedsRebuild(ctx)
	if err != nil {
		t.Fatalf("check rebuild need: %v", err)
	}
	if !needs {
		t.Fatal("a cleared projection with definition evidence must be stale")
	}
	reader, err := receipt.NewDefinitionReader(tracker.Journal())
	if err != nil {
		t.Fatalf("definition reader: %v", err)
	}
	if err := receipt.RebuildActiveEntities(ctx, reader, active); err != nil {
		t.Fatalf("rebuild active projection: %v", err)
	}
	rebuilt, found, err := active.Lookup(ctx, document.Kind(), document.Content())
	if err != nil || !found {
		t.Fatalf("lookup rebuilt projection: found=%v err=%v", found, err)
	}
	if rebuilt.Definition != ref.Definition || rebuilt.Operation != entity.Operation || rebuilt.State != entity.State {
		t.Fatalf("rebuilt projection = %+v, want %+v", rebuilt, entity)
	}
	needs, err = active.NeedsRebuild(ctx)
	if err != nil {
		t.Fatalf("check rebuilt freshness: %v", err)
	}
	if needs {
		t.Fatal("rebuilt projection still reports stale")
	}
}
