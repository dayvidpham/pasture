package receipt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

// ActiveEntity is one durable activation of a compiled lifecycle document.
// It is a read projection: the journal remains the source of truth, and this
// row only records the exact journal coordinates an activating operation
// produced. Content is the key because the compiled body is known to the
// binary; the row proves that body was activated and supplies its journal IDs.
type ActiveEntity struct {
	Kind       model.DefinitionKind
	Content    model.ContentIdentity
	Definition model.DefinitionJournalID
	State      model.DefinitionStateID
	Operation  provenance.OperationID
}

// ActiveEntityStore is the hot-path activation lookup. A miss is not an error:
// callers fall back to the journal-backed ensure/bridge path and populate the
// projection. The journal remains authoritative.
type ActiveEntityStore interface {
	Lookup(ctx context.Context, kind model.DefinitionKind, content model.ContentIdentity) (ActiveEntity, bool, error)
	Put(ctx context.Context, entity ActiveEntity) error
	MarkApplied(ctx context.Context, journalID provenance.JournalID) error
}

// SQLiteActiveEntityStore is the persisted projection. Lookup validates that
// both journal rows still exist, so a restored or truncated journal falls back
// to the journal-backed path instead of returning a dangling cache hit.
type SQLiteActiveEntityStore struct {
	DB *sql.DB
}

func (s SQLiteActiveEntityStore) Lookup(ctx context.Context, kind model.DefinitionKind, content model.ContentIdentity) (ActiveEntity, bool, error) {
	if s.DB == nil {
		return ActiveEntity{}, false, structured(pasterrors.CategoryValidation,
			"The active lifecycle definition projection is unavailable.",
			"The projection store has no unified SQLite handle.",
			"Reading an active lifecycle definition (internal/lifecycle/receipt/active_entity.go in receipt.SQLiteActiveEntityStore.Lookup).",
			"The hot path falls back to the journal-backed definition bridge.",
			"Construct the receipt service through tasks.NewLifecycleReceiptService.",
			nil)
	}
	var (
		definition int64
		state      int64
		operation  string
	)
	err := s.DB.QueryRowContext(ctx, `
		SELECT a.definition_journal_id, a.state_journal_id, a.operation_id
		FROM lifecycle_active_entity a
		WHERE a.entity_kind = ?1 AND a.content_digest = ?2
		  AND EXISTS (SELECT 1 FROM journal_evidence d
		              WHERE d.journal_id = a.definition_journal_id
		                AND d.content_digest = a.content_digest)
		  AND EXISTS (SELECT 1 FROM journal_evidence s WHERE s.journal_id = a.state_journal_id)`,
		int64(kind), content[:]).Scan(&definition, &state, &operation)
	if err == sql.ErrNoRows {
		return ActiveEntity{}, false, nil
	}
	if err != nil {
		return ActiveEntity{}, false, structured(pasterrors.CategoryStorage,
			"The active lifecycle definition projection could not be read.",
			"SQLite rejected the active-entity lookup.",
			"Reading an active lifecycle definition (internal/lifecycle/receipt/active_entity.go in receipt.SQLiteActiveEntityStore.Lookup).",
			"The hot path falls back to the journal-backed definition bridge.",
			"Confirm the database is readable and retry; run 'pasture migrate' if the schema is behind.",
			err)
	}
	if definition <= 0 || state <= definition || operation == "" {
		return ActiveEntity{}, false, nil
	}
	return ActiveEntity{
		Kind:       kind,
		Content:    content,
		Definition: model.DefinitionJournalID(definition),
		State:      model.DefinitionStateID(state),
		Operation:  provenance.OperationID(operation),
	}, true, nil
}

func (s SQLiteActiveEntityStore) Put(ctx context.Context, entity ActiveEntity) error {
	if s.DB == nil {
		return structured(pasterrors.CategoryValidation,
			"The active lifecycle definition projection is unavailable.",
			"The projection store has no unified SQLite handle.",
			"Recording an active lifecycle definition (internal/lifecycle/receipt/active_entity.go in receipt.SQLiteActiveEntityStore.Put).",
			"The hook falls back to the journal-backed definition bridge.",
			"Construct the receipt service through tasks.NewLifecycleReceiptService.",
			nil)
	}
	if entity.Kind == 0 || entity.Content == (model.ContentIdentity{}) || entity.Definition <= 0 || entity.State <= model.DefinitionStateID(entity.Definition) || entity.Operation == "" {
		return structured(pasterrors.CategoryValidation,
			"The active lifecycle definition projection is incomplete.",
			"An active entity needs a kind, content digest, positive definition/state journal IDs and a producing operation.",
			"Recording an active lifecycle definition (internal/lifecycle/receipt/active_entity.go in receipt.SQLiteActiveEntityStore.Put).",
			"The projection row was not written; the hook falls back to the journal-backed bridge.",
			"Resolve the activation through its owner before recording the projection.",
			nil)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin active lifecycle definition projection write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO lifecycle_active_entity
			(entity_kind, content_digest, definition_journal_id, state_journal_id, operation_id, generation)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6)
		ON CONFLICT(entity_kind, content_digest) DO UPDATE SET
			definition_journal_id = excluded.definition_journal_id,
			state_journal_id = excluded.state_journal_id,
			operation_id = excluded.operation_id,
			generation = excluded.generation
		WHERE excluded.generation >= lifecycle_active_entity.generation`,
		int64(entity.Kind), entity.Content[:], int64(entity.Definition), int64(entity.State), string(entity.Operation), int64(entity.State)); err != nil {
		return fmt.Errorf("write active lifecycle definition projection: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO lifecycle_active_projection (singleton, applied_journal_id)
		VALUES (1, ?1)
		ON CONFLICT(singleton) DO UPDATE SET
			applied_journal_id = MAX(lifecycle_active_projection.applied_journal_id, excluded.applied_journal_id)`,
		int64(entity.State)); err != nil {
		return fmt.Errorf("advance active lifecycle definition projection watermark: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit active lifecycle definition projection: %w", err)
	}
	return nil
}

func (s SQLiteActiveEntityStore) MarkApplied(ctx context.Context, journalID provenance.JournalID) error {
	if s.DB == nil {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO lifecycle_active_projection (singleton, applied_journal_id)
		VALUES (1, ?1)
		ON CONFLICT(singleton) DO UPDATE SET
			applied_journal_id = MAX(lifecycle_active_projection.applied_journal_id, excluded.applied_journal_id)`,
		int64(journalID))
	if err != nil {
		return fmt.Errorf("mark active lifecycle definition projection applied: %w", err)
	}
	return nil
}

// NeedsRebuild reports whether definition/state evidence is newer than the
// projection watermark. The dedicated evidence-kind index makes this a
// bounded lookup rather than a scan of every lifecycle event.
func (s SQLiteActiveEntityStore) NeedsRebuild(ctx context.Context) (bool, error) {
	if s.DB == nil {
		return false, nil
	}
	latest, err := s.latestDefinitionEvidence(ctx)
	if err != nil {
		return false, err
	}
	var applied int64
	err = s.DB.QueryRowContext(ctx, `SELECT applied_journal_id FROM lifecycle_active_projection WHERE singleton = 1`).Scan(&applied)
	if err == sql.ErrNoRows {
		applied = 0
	} else if err != nil {
		return false, fmt.Errorf("read active lifecycle definition projection watermark: %w", err)
	}
	return latest > applied, nil
}

func (s SQLiteActiveEntityStore) latestDefinitionEvidence(ctx context.Context) (int64, error) {
	var latest sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `
		SELECT MAX(journal_id) FROM journal_evidence
		WHERE evidence_kind IN (?1, ?2, ?3, ?4)`,
		string(definitionKind), string(definitions.EvidenceKind),
		string(definitionStateKind), string(envelopeStateKind)).Scan(&latest)
	if err != nil {
		return 0, fmt.Errorf("read latest lifecycle definition evidence: %w", err)
	}
	if !latest.Valid {
		return 0, nil
	}
	return latest.Int64, nil
}

// RebuildActiveEntities reconstructs the active projection from journal
// truth. It is the recovery path for an upgraded store, a restored journal, or
// a projection write that did not land. Only definition and definition-state
// evidence is read; ordinary lifecycle occurrences are irrelevant here.
func RebuildActiveEntities(ctx context.Context, reader DefinitionReader, store ActiveEntityStore) error {
	if reader == nil || store == nil {
		return structured(pasterrors.CategoryValidation,
			"The active lifecycle definition projection cannot be rebuilt.",
			"The rebuild needs a definition reader and an active-entity store.",
			"Rebuilding active lifecycle definitions (internal/lifecycle/receipt/active_entity.go in receipt.RebuildActiveEntities).",
			"No projection rows were changed.",
			"Construct both dependencies through the unified receipt service.",
			nil)
	}
	query := provenance.EvidenceQuery{
		Kinds: []provenance.EvidenceKind{definitionKind, definitions.EvidenceKind, definitionStateKind, envelopeStateKind},
		Page:  provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
	}
	states := make(map[provenance.OperationID]provenance.EvidenceRow)
	definitionsByOperation := make(map[provenance.OperationID]provenance.EvidenceRow)
	var latest provenance.JournalID
	for {
		page, err := reader.QueryEvidence(query)
		if err != nil {
			return fmt.Errorf("read lifecycle definition evidence for rebuild: %w", err)
		}
		for _, row := range page.Rows {
			if row.JournalID > latest {
				latest = row.JournalID
			}
			if row.ProducingOperationID == "" {
				continue
			}
			switch row.EvidenceKind {
			case definitionStateKind, envelopeStateKind:
				if existing, ok := states[row.ProducingOperationID]; !ok || row.JournalID > existing.JournalID {
					states[row.ProducingOperationID] = row
				}
			case definitionKind, definitions.EvidenceKind:
				if existing, ok := definitionsByOperation[row.ProducingOperationID]; !ok || row.JournalID > existing.JournalID {
					definitionsByOperation[row.ProducingOperationID] = row
				}
			}
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	for operation, definitionRow := range definitionsByOperation {
		stateRow, ok := states[operation]
		if !ok || stateRow.JournalID <= definitionRow.JournalID {
			continue
		}
		kind, content, err := activeEntityIdentity(definitionRow)
		if err != nil {
			return err
		}
		if err := store.Put(ctx, ActiveEntity{
			Kind:       kind,
			Content:    content,
			Definition: model.DefinitionJournalID(definitionRow.JournalID),
			State:      model.DefinitionStateID(stateRow.JournalID),
			Operation:  operation,
		}); err != nil {
			return err
		}
	}
	return store.MarkApplied(ctx, latest)
}

func activeEntityIdentity(row provenance.EvidenceRow) (model.DefinitionKind, model.ContentIdentity, error) {
	switch row.EvidenceKind {
	case definitions.EvidenceKind:
		document, err := definitions.Decode(row.Payload)
		if err != nil {
			return 0, model.ContentIdentity{}, fmt.Errorf("decode definition evidence row %d for active projection: %w", row.JournalID, err)
		}
		digest := sha256.Sum256(document.Body())
		content := model.ContentIdentity(digest)
		if document.Content() != content || len(row.ContentDigest) != len(content) || !bytes.Equal(row.ContentDigest, content[:]) {
			return 0, model.ContentIdentity{}, fmt.Errorf("definition evidence row %d content digest does not match its body", row.JournalID)
		}
		return document.Kind(), content, nil
	case definitionKind:
		normalized, err := definitions.NormalizePayload(definitionKind, row.Payload)
		if err != nil {
			return 0, model.ContentIdentity{}, fmt.Errorf("normalize metamodel evidence row %d for active projection: %w", row.JournalID, err)
		}
		if _, err := metamodel.DecodeBody(normalized); err != nil {
			return 0, model.ContentIdentity{}, fmt.Errorf("decode metamodel evidence row %d for active projection: %w", row.JournalID, err)
		}
		digest := sha256.Sum256(normalized)
		content := model.ContentIdentity(digest)
		if len(row.ContentDigest) != len(content) || !bytes.Equal(row.ContentDigest, content[:]) {
			return 0, model.ContentIdentity{}, fmt.Errorf("metamodel evidence row %d content digest does not match its normalized body", row.JournalID)
		}
		return model.DefinitionMetamodel, content, nil
	default:
		return 0, model.ContentIdentity{}, fmt.Errorf("evidence row %d has unsupported definition kind %q", row.JournalID, row.EvidenceKind)
	}
}

// DirectEvidenceReader is the primary-key evidence door used by the definition
// bridge. It is deliberately narrower than DefinitionReader: it can fetch one
// known row by journal ID or producing operation, but it cannot enumerate or
// filter history.
type DirectEvidenceReader interface {
	EvidenceAt(ctx context.Context, kind provenance.EvidenceKind, journalID provenance.JournalID) (provenance.EvidenceRow, bool, error)
	EvidenceByOperation(ctx context.Context, kind provenance.EvidenceKind, operation provenance.OperationID) (provenance.EvidenceRow, bool, error)
}

// SQLiteEvidenceReader implements the direct door over the unified database.
// The evidence row is joined to its journal anchor and producing operation by
// primary key, never through journal_attributed or a generic fact page.
type SQLiteEvidenceReader struct {
	DB *sql.DB
}

const directEvidenceSelect = `
	SELECT e.journal_id, j.recorded_at, e.task_id, e.evidence_kind,
	       e.content_digest, e.payload, j.produced_by_operation_journal_id, jo.operation_id
	FROM journal_evidence e
	JOIN journal j ON j.journal_id = e.journal_id
	LEFT JOIN journal_operations jo ON jo.journal_id = j.produced_by_operation_journal_id`

func (s SQLiteEvidenceReader) EvidenceAt(ctx context.Context, kind provenance.EvidenceKind, journalID provenance.JournalID) (provenance.EvidenceRow, bool, error) {
	if s.DB == nil {
		return provenance.EvidenceRow{}, false, structured(pasterrors.CategoryValidation,
			"The direct lifecycle evidence reader is unavailable.",
			"The reader has no unified SQLite handle.",
			"Reading exact lifecycle definition evidence (internal/lifecycle/receipt/active_entity.go in receipt.SQLiteEvidenceReader.EvidenceAt).",
			"The definition bridge falls back to the journal-backed evidence page.",
			"Construct the receipt service through tasks.NewLifecycleReceiptService.",
			nil)
	}
	return scanDirectEvidence(s.DB.QueryRowContext(ctx, directEvidenceSelect+`
		WHERE e.journal_id = ?1 AND e.evidence_kind = ?2`,
		int64(journalID), string(kind)))
}

func (s SQLiteEvidenceReader) EvidenceByOperation(ctx context.Context, kind provenance.EvidenceKind, operation provenance.OperationID) (provenance.EvidenceRow, bool, error) {
	if s.DB == nil {
		return provenance.EvidenceRow{}, false, structured(pasterrors.CategoryValidation,
			"The direct lifecycle evidence reader is unavailable.",
			"The reader has no unified SQLite handle.",
			"Reading lifecycle definition evidence by operation (internal/lifecycle/receipt/active_entity.go in receipt.SQLiteEvidenceReader.EvidenceByOperation).",
			"The definition bridge falls back to the journal-backed evidence page.",
			"Construct the receipt service through tasks.NewLifecycleReceiptService.",
			nil)
	}
	return scanDirectEvidence(s.DB.QueryRowContext(ctx, directEvidenceSelect+`
		WHERE jo.operation_id = ?1 AND e.evidence_kind = ?2
		ORDER BY e.journal_id DESC LIMIT 1`,
		string(operation), string(kind)))
}

func scanDirectEvidence(row *sql.Row) (provenance.EvidenceRow, bool, error) {
	var (
		journalID  int64
		recordedAt int64
		taskID     sql.NullString
		kind       string
		digest     []byte
		payload    []byte
		producer   sql.NullInt64
		operation  sql.NullString
	)
	if err := row.Scan(&journalID, &recordedAt, &taskID, &kind, &digest, &payload, &producer, &operation); err != nil {
		if err == sql.ErrNoRows {
			return provenance.EvidenceRow{}, false, nil
		}
		return provenance.EvidenceRow{}, false, fmt.Errorf("read direct lifecycle evidence: %w", err)
	}
	out := provenance.EvidenceRow{
		JournalID:                   provenance.JournalID(journalID),
		RecordedAt:                  time.Unix(0, recordedAt).UTC(),
		EvidenceKind:                provenance.EvidenceKind(kind),
		ContentDigest:               append([]byte(nil), digest...),
		Payload:                     append([]byte(nil), payload...),
		ProducingOperationJournalID: provenance.JournalID(producer.Int64),
		ProducingOperationID:        provenance.OperationID(operation.String),
	}
	// The definition bridge does not consume TaskID; leave it nil rather than
	// inventing a conversion the Provenance type does not expose here.
	_ = taskID
	return out, true, nil
}
