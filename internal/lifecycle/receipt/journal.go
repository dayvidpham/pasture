package receipt

import (
	"context"
	"database/sql"
	stderrors "errors"
	"fmt"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
	"modernc.org/sqlite"
)

const (
	receiptCommand  = "pasture.lifecycle.receipt.append/v1"
	receiptSlot     = provenance.ResultSlotID("occurrence")
	receiptKind     = provenance.EvidenceKind("pasture.lifecycle.occurrence.v1")
	interpretedSlot = provenance.ResultSlotID("interpreted")
	interpretedKind = provenance.EvidenceKind("pasture.lifecycle.interpreted.v1")
	sqliteBusyCode  = 5
)

type Identity struct {
	Actor     provenance.ActorID
	Authority provenance.JournalID
}

type IdentityResolver interface {
	ResolveLifecycleIdentity(context.Context) (Identity, error)
}

// BlobStore is the NARROW WRITE door on the payload blob table: one method,
// which stores a content-addressed body and stamps the instant it was written.
// The receipt service is its only caller and depends on nothing else about the
// store, so a fake that records the call satisfies it. The delete door is a
// separate type, PayloadReclaimer, which nothing on a write path constructs.
type BlobStore interface {
	Put(context.Context, digest.Digest, []byte) error
}

// SQLiteBlobStore writes and inspects payload blobs on the unified store. It
// holds its own clock for the written-at stamp: the wall clock unless a
// scripted clock is injected through NewSQLiteBlobStore, so a store built as
// a bare literal stamps real time and a test can choose the instant.
type SQLiteBlobStore struct {
	DB    *sql.DB
	clock Clock
}

// BlobStoreOption configures a SQLiteBlobStore at construction.
type BlobStoreOption func(*SQLiteBlobStore)

// WithPayloadClock makes the store stamp written_at from the given clock
// instead of the wall clock. It exists for tests that must place a blob at a
// chosen instant relative to the reclaim window; production wires no clock
// and stamps real time.
func WithPayloadClock(clock Clock) BlobStoreOption {
	return func(s *SQLiteBlobStore) { s.clock = clock }
}

// NewSQLiteBlobStore builds a store on the unified handle with the options
// applied. Without options it is identical to the literal SQLiteBlobStore{DB: db}.
func NewSQLiteBlobStore(db *sql.DB, options ...BlobStoreOption) SQLiteBlobStore {
	store := SQLiteBlobStore{DB: db}
	for _, option := range options {
		option(&store)
	}
	return store
}

// writeInstant is the instant a Put stamps. The wall clock is the default
// because the stamp's only reader, the orphan reclaim, ages a blob against a
// snapshot instant taken from the same wall clock in production; a store
// built without a clock must therefore stamp real time, never zero, or every
// fresh blob would read as a legacy row older than any bound.
//
// This is one of the two INJECTED clocks the age bound reads (the other is the
// rebuild's snapshot clock). The writer-window refusal in Service.Receive reads
// the WALL clock instead, because it judges a context deadline; see
// Service.boundedWriter for the boundary between the two, and script both
// injected clocks in any test that scripts one.
func (s SQLiteBlobStore) writeInstant() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock.Now()
}

const MaxReclaimablePayloads = 256

func (s SQLiteBlobStore) Put(ctx context.Context, ref digest.Digest, body []byte) error {
	if s.DB == nil {
		return structured(pasterrors.CategoryValidation, "The lifecycle payload store is unavailable.", "The receipt service was constructed without a SQLite handle.", "Writing the content-addressed payload blob (internal/lifecycle/receipt/journal.go in receipt.SQLiteBlobStore.Put).", "No payload or occurrence was recorded.", "Open the receipt service through the unified Pasture database opener.", nil)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return structured(pasterrors.CategoryStorage, "The lifecycle payload transaction could not start.", "SQLite did not grant a transaction for the bounded payload write.", "Writing the content-addressed payload blob (internal/lifecycle/receipt/journal.go in receipt.SQLiteBlobStore.Put).", "No occurrence was committed.", "Confirm the database is writable, run `pasture migrate`, and retry the delivery.", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	// The stamp is REFRESHED on a repeated Put and never moved back. A writer
	// re-delivering a body whose digest an old orphan already carries is in
	// flight between this write and its journal append exactly like a first
	// writer, so the age the reclaim reads must be this writer's instant, not
	// the orphan's; a stamp left at the old instant would let a read command
	// delete the blob under an append that then names an absent blob.
	if _, err = tx.ExecContext(ctx, `INSERT INTO lifecycle_payload_blobs (digest, body, byte_count, written_at) VALUES (?, ?, ?, ?) ON CONFLICT(digest) DO UPDATE SET written_at = max(lifecycle_payload_blobs.written_at, excluded.written_at)`, ref.String(), body, len(body), s.writeInstant().UnixNano()); err != nil {
		return structured(pasterrors.CategoryStorage, "The lifecycle payload blob could not be stored.", "SQLite rejected the content-addressed blob write before the occurrence transaction began.", "Writing the content-addressed payload blob (internal/lifecycle/receipt/journal.go in receipt.SQLiteBlobStore.Put).", "No occurrence was committed; a previous identical blob, if any, is unchanged.", "Run `pasture migrate`, confirm the database is writable, and retry the delivery.", err)
	}
	if err = tx.Commit(); err != nil {
		return structured(pasterrors.CategoryStorage, "The lifecycle payload blob could not be committed.", "SQLite rejected the bounded blob transaction before the occurrence transaction began.", "Writing the content-addressed payload blob (internal/lifecycle/receipt/journal.go in receipt.SQLiteBlobStore.Put).", "No occurrence was committed.", "Confirm storage health and retry the delivery.", err)
	}
	committed = true
	return nil
}

func (s SQLiteBlobStore) Exists(ctx context.Context, ref digest.Digest) (bool, error) {
	if s.DB == nil {
		return false, structured(pasterrors.CategoryValidation, "The lifecycle payload store is unavailable.", "Existence checks require the unified SQLite handle.", "Inspecting a lifecycle payload blob (internal/lifecycle/receipt/journal.go in receipt.SQLiteBlobStore.Exists).", "No storage state was inspected.", "Open the blob store through the unified Pasture database opener.", nil)
	}
	var exists int
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lifecycle_payload_blobs WHERE digest = ?)`, ref.String()).Scan(&exists); err != nil {
		return false, structured(pasterrors.CategoryStorage, "The lifecycle payload blob could not be inspected.", "SQLite rejected the bounded digest existence query.", "Inspecting a lifecycle payload blob (internal/lifecycle/receipt/journal.go in receipt.SQLiteBlobStore.Exists).", "The caller cannot determine whether the body is retained.", "Confirm database health and retry.", err)
	}
	return exists == 1, nil
}

// Reclaimable returns committed payload blobs referenced by no occurrence.
// The bounded query is the future reclamation pass's identification seam; it
// does not delete anything.
func (s SQLiteBlobStore) Reclaimable(ctx context.Context, limit int) ([]digest.Digest, error) {
	if s.DB == nil || limit <= 0 || limit > MaxReclaimablePayloads {
		return nil, structured(pasterrors.CategoryValidation, "The reclaimable lifecycle payload query is invalid.", fmt.Sprintf("A unified SQLite handle and limit from 1 through %d are required.", MaxReclaimablePayloads), "Listing reclaimable lifecycle payloads (internal/lifecycle/receipt/journal.go in receipt.SQLiteBlobStore.Reclaimable).", "No storage state was inspected or changed.", fmt.Sprintf("Open the production blob store and choose a limit from 1 through %d.", MaxReclaimablePayloads), nil)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT b.digest FROM lifecycle_payload_blobs b LEFT JOIN lifecycle_occurrences o ON o.payload_digest = b.digest WHERE o.journal_id IS NULL ORDER BY b.digest LIMIT ?`, limit)
	if err != nil {
		return nil, structured(pasterrors.CategoryStorage, "Reclaimable lifecycle payloads could not be listed.", "SQLite rejected the bounded orphan-identification query.", "Listing reclaimable lifecycle payloads (internal/lifecycle/receipt/journal.go in receipt.SQLiteBlobStore.Reclaimable).", "No payload was deleted; reclamation cannot proceed until inspection succeeds.", "Confirm database health and retry.", err)
	}
	defer rows.Close()
	refs := make([]digest.Digest, 0, limit)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		ref, err := digest.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("stored lifecycle payload digest %q is invalid: %w", raw, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return refs, nil
}

// ReclaimableCount returns HOW MANY committed payload blobs no occurrence
// names. It is the same set Reclaimable enumerates, counted rather than
// listed, so the two can never disagree: both read one predicate, a blob with
// no occurrence naming its digest.
//
// It is COUNTED IN SQLITE and not bounded by MaxReclaimablePayloads. The bound
// on Reclaimable exists because a future reclamation pass must delete in
// bounded batches; a count deletes nothing, and a count that stopped at 256
// would under-report exactly when the number matters, which is the one thing
// an operator-facing number may never do.
//
// It reads the occurrence projection, so the caller must have rebuilt that
// projection from the journal first. Against a projection that was never
// rebuilt EVERY blob looks unnamed, and the answer would be the largest and
// most alarming wrong number this store can produce.
func (s SQLiteBlobStore) ReclaimableCount(ctx context.Context) (int64, error) {
	if s.DB == nil {
		return 0, structured(pasterrors.CategoryValidation, "The orphan payload count is unavailable.", "Counting unnamed payload blobs requires the unified SQLite handle.", "Counting orphan lifecycle payloads (internal/lifecycle/receipt/journal.go in receipt.SQLiteBlobStore.ReclaimableCount).", "No storage state was inspected or changed.", "Open the blob store through the unified Pasture database opener.", nil)
	}
	var count int64
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM lifecycle_payload_blobs b LEFT JOIN lifecycle_occurrences o ON o.payload_digest = b.digest WHERE o.journal_id IS NULL`).Scan(&count); err != nil {
		return 0, structured(pasterrors.CategoryStorage, "Orphan lifecycle payloads could not be counted.", "SQLite rejected the orphan-count query.", "Counting orphan lifecycle payloads (internal/lifecycle/receipt/journal.go in receipt.SQLiteBlobStore.ReclaimableCount).", "The operator cannot learn how many payload blobs are unreferenced; nothing was deleted.", "Run `pasture migrate`, confirm the database is readable, and retry.", err)
	}
	return count, nil
}

type JournalAppender struct {
	Journal  provenance.Journal
	Deadline time.Duration
	Clock    Clock
	Observer CommitObserver
}

type CommitObserver interface{ ObserveOccurrenceCommit(time.Duration, error) }

func (a JournalAppender) Append(ctx context.Context, in provenance.OperationInput) (model.OccurrenceID, error) {
	contextJournal, ok := a.Journal.(provenance.ContextJournal)
	if !ok {
		return 0, structured(pasterrors.CategoryValidation, "The lifecycle journal cannot enforce the ingress deadline.", "The configured Provenance journal does not implement ContextJournal.ApplyContext, so using it would silently wait for the connection's longer busy timeout.", "Committing a lifecycle occurrence (internal/lifecycle/receipt/journal.go in receipt.JournalAppender.Append).", "No occurrence was recorded; an already committed payload blob may remain as a reclaimable orphan.", "Use the pinned Provenance journal implementation that provides ContextJournal.ApplyContext.", nil)
	}
	deadline := a.Deadline
	if deadline <= 0 {
		return 0, structured(pasterrors.CategoryValidation, "The lifecycle journal appender has no ingress deadline.", "Timeouts must come from one validated injected profile; silently defaulting here could invert SQLite retry and caller deadlines.", "Committing a lifecycle occurrence (internal/lifecycle/receipt/journal.go in receipt.JournalAppender.Append).", "No occurrence was recorded; an already committed payload blob may remain as a reclaimable orphan.", "Construct the receipt service with a validated timeout profile.", nil)
	}
	if a.Clock == nil {
		return 0, structured(pasterrors.CategoryValidation, "The lifecycle journal appender has no clock.", "Deadline accounting must use the injected process clock so timeout behavior is deterministic and testable.", "Committing a lifecycle occurrence (internal/lifecycle/receipt/journal.go in receipt.JournalAppender.Append).", "No occurrence was recorded; an already committed payload blob may remain as a reclaimable orphan.", "Construct the journal appender with the receipt service clock.", nil)
	}
	started := a.Clock.Now()
	bounded, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	result, err := contextJournal.ApplyContext(bounded, in)
	var boundedErr error
	if err != nil {
		boundedErr = bounded.Err()
	}
	elapsed := a.Clock.Now().Sub(started)
	if a.Observer != nil {
		a.Observer.ObserveOccurrenceCommit(elapsed, err)
	}
	if err != nil {
		switch {
		case stderrors.Is(boundedErr, context.DeadlineExceeded):
			// Provenance v0.0.5 makes the caller deadline the authoritative
			// bound on contended writes: a writer that stayed contended until
			// the deadline returns the typed context error joined with the
			// SQLite busy error. Contention is therefore diagnosed from the
			// error chain, not from which timer happened to fire first.
			var busyErr *sqlite.Error
			if stderrors.As(err, &busyErr) && busyErr.Code() == sqliteBusyCode {
				return 0, model.IngressContentionError{Elapsed: elapsed, Cause: err}
			}
			return 0, model.IngressDeadlineError{Deadline: deadline, Elapsed: elapsed}
		case stderrors.Is(boundedErr, context.Canceled):
			cause := err
			if !stderrors.Is(cause, context.Canceled) {
				cause = stderrors.Join(cause, boundedErr)
			}
			return 0, structured(pasterrors.CategoryStorage, "The lifecycle occurrence could not be committed.", "The Provenance journal rejected the occurrence operation after its payload blob was safely stored.", "Committing a lifecycle occurrence (internal/lifecycle/receipt/journal.go in receipt.JournalAppender.Append).", "No receipt is available; the payload blob may remain as a reclaimable orphan.", "Inspect the error details, repair the database or operation input, and retry with the same operation identity only when the input is unchanged.", cause)
		case boundedErr == nil:
			var sqliteErr *sqlite.Error
			if stderrors.As(err, &sqliteErr) && sqliteErr.Code() == sqliteBusyCode {
				return 0, model.IngressContentionError{Elapsed: elapsed, Cause: err}
			}
		}
		return 0, structured(pasterrors.CategoryStorage, "The lifecycle occurrence could not be committed.", "The Provenance journal rejected the occurrence operation after its payload blob was safely stored.", "Committing a lifecycle occurrence (internal/lifecycle/receipt/journal.go in receipt.JournalAppender.Append).", "No receipt is available; the payload blob may remain as a reclaimable orphan.", "Inspect the error details, repair the database or operation input, and retry with the same operation identity only when the input is unchanged.", err)
	}
	for _, slot := range result.ResultSlots {
		if slot.Slot == receiptSlot && slot.ProducedJournalID > 0 {
			return model.OccurrenceID(slot.ProducedJournalID), nil
		}
	}
	return 0, structured(pasterrors.CategoryStorage, "The lifecycle occurrence committed without its receipt identity.", "The journal result did not contain the mandatory occurrence result slot.", "Committing a lifecycle occurrence (internal/lifecycle/receipt/journal.go in receipt.JournalAppender.Append).", "The caller cannot claim a receipt even though the operation reported success.", "Use a compatible Provenance journal and inspect the committed operation before retrying.", fmt.Errorf("missing result slot %q", receiptSlot))
}

func structured(category pasterrors.Category, what, why, where, impact, fix string, cause error) error {
	return &pasterrors.StructuredError{Category: category, What: what, Why: why, Where: where, Impact: impact, Fix: fix, Cause: cause}
}

// PayloadReclaimer is the NARROW DELETE door on the payload blob table, and
// the only one. It is a separate type from SQLiteBlobStore on purpose: that
// type's doc says read-only inspection plus the receipt write, and a delete
// arriving on it would make the doc false and the value unsafe to hand around.
//
// It runs inside a transaction it is handed, never on a bare handle, because
// the one caller that constructs it is the projection rebuild's reclaim, which
// may delete only against a projection it has just rebuilt in that same
// transaction. Against a stale projection a blob that a journal row names
// looks named by nothing, and deleting it would leave a journal row naming an
// absent blob, the one state the blob-before-journal write order exists to
// prevent.
type PayloadReclaimer struct{ Tx *sql.Tx }

// ReclaimOrphansWrittenBefore deletes, oldest first and at most limit, the
// payload blobs that no lifecycle_occurrences row names and whose written_at
// is before the given instant, in unix nanoseconds. It returns the digests it
// deleted. A legacy blob, stamped 0 by the migration that added the column, is
// older than any instant and is deleted as soon as nothing names it.
func (r PayloadReclaimer) ReclaimOrphansWrittenBefore(ctx context.Context, before int64, limit int) ([]digest.Digest, error) {
	if r.Tx == nil || limit <= 0 {
		return nil, structured(pasterrors.CategoryValidation, "The orphan payload reclaim is invalid.", "A transaction and a positive limit are required; the reclaim never runs on a bare handle, because it must delete only against a projection rebuilt in the same transaction.", "Reclaiming orphan lifecycle payloads (internal/lifecycle/receipt/journal.go in receipt.PayloadReclaimer.ReclaimOrphansWrittenBefore).", "Nothing was deleted.", "Construct the reclaimer from the projection rebuild's own transaction with a limit of at least 1.", nil)
	}
	rows, err := r.Tx.QueryContext(ctx, `DELETE FROM lifecycle_payload_blobs WHERE digest IN (SELECT b.digest FROM lifecycle_payload_blobs b LEFT JOIN lifecycle_occurrences o ON o.payload_digest = b.digest WHERE o.journal_id IS NULL AND b.written_at < ? ORDER BY b.written_at ASC, b.digest ASC LIMIT ?) RETURNING digest`, before, limit)
	if err != nil {
		return nil, structured(pasterrors.CategoryStorage, "Orphan lifecycle payloads could not be reclaimed.", "SQLite rejected the bounded orphan delete.", "Reclaiming orphan lifecycle payloads (internal/lifecycle/receipt/journal.go in receipt.PayloadReclaimer.ReclaimOrphansWrittenBefore).", "Nothing was deleted; the enclosing savepoint is rolled back by the caller.", "Run `pasture migrate` so the payload blob table carries its written-at stamp, confirm database health, and run a read command again.", err)
	}
	defer rows.Close()
	deleted := make([]digest.Digest, 0, limit)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		ref, err := digest.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("reclaimed lifecycle payload digest %q is invalid: %w", raw, err)
		}
		deleted = append(deleted, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return deleted, nil
}
