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
)

const (
	receiptCommand  = "pasture.lifecycle.receipt.append/v1"
	receiptSlot     = provenance.ResultSlotID("occurrence")
	receiptKind     = provenance.EvidenceKind("pasture.lifecycle.occurrence.v1")
	interpretedSlot = provenance.ResultSlotID("interpreted")
	interpretedKind = provenance.EvidenceKind("pasture.lifecycle.interpreted.v1")
)

type Identity struct {
	Actor     provenance.ActorID
	Authority provenance.JournalID
}

type IdentityResolver interface {
	ResolveLifecycleIdentity(context.Context) (Identity, error)
}

type BlobStore interface {
	Put(context.Context, digest.Digest, []byte) error
}

type SQLiteBlobStore struct{ DB *sql.DB }

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
	if _, err = tx.ExecContext(ctx, `INSERT INTO lifecycle_payload_blobs (digest, body, byte_count) VALUES (?, ?, ?) ON CONFLICT(digest) DO NOTHING`, ref.String(), body, len(body)); err != nil {
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
	if a.Observer != nil {
		a.Observer.ObserveOccurrenceCommit(a.Clock.Now().Sub(started), err)
	}
	if err != nil {
		if stderrors.Is(err, context.DeadlineExceeded) {
			return 0, model.IngressDeadlineError{Deadline: deadline, Elapsed: a.Clock.Now().Sub(started)}
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
