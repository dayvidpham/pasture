package projection

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
)

type Reader struct{ DB *sql.DB }

func (r Reader) Occurrences(ctx context.Context, query model.OccurrenceQuery) (model.OccurrencePage, error) {
	if r.DB == nil {
		return model.OccurrencePage{}, readerError("The lifecycle reader has no database handle.", "It was constructed outside the unified store opener.", "No records were read.", "Construct the reader with the opened projection database.", nil)
	}
	if query.Page.Size == 0 || int(query.Page.Size) > model.MaxPageSize {
		return model.OccurrencePage{}, readerError("The lifecycle occurrence page size is invalid.", "Reads must be explicitly bounded.", "No records were read.", fmt.Sprintf("Choose a page size from 1 through %d.", model.MaxPageSize), nil)
	}
	fingerprint := queryFingerprint(query)
	after, snapshot := provenance.JournalID(0), provenance.JournalID(^uint64(0)>>1)
	if query.Page.Cursor != nil {
		if query.Page.Cursor.QueryFingerprint != fingerprint {
			return model.OccurrencePage{}, readerError("The lifecycle cursor belongs to a different query.", "Its contract or event filters do not match this request.", "No records were read.", "Restart pagination without the cursor.", nil)
		}
		after, snapshot = query.Page.Cursor.LastJournalID, query.Page.Cursor.SnapshotJournalID
	} else {
		if err := r.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(journal_id), 0) FROM lifecycle_occurrences`).Scan(&snapshot); err != nil {
			return model.OccurrencePage{}, readerError("The lifecycle reader could not establish a stable snapshot.", "SQLite rejected the bounded projection watermark query.", "No records were read.", "Rebuild projections and retry.", err)
		}
	}
	statement := `SELECT journal_id, contract, event_kind, received_at, actor_id, capture_disposition, payload_digest, envelope_json, bindings_json FROM lifecycle_occurrences WHERE journal_id > ? AND journal_id <= ?`
	args := []any{after, snapshot}
	if contracts := query.ContractFilter(); len(contracts) > 0 {
		statement += ` AND contract IN (` + placeholders(len(contracts)) + `)`
		for _, contract := range contracts {
			args = append(args, contract.String())
		}
	}
	if events := query.EventFilter(); len(events) > 0 {
		statement += ` AND event_kind IN (` + placeholders(len(events)) + `)`
		for _, event := range events {
			args = append(args, event)
		}
	}
	statement += ` ORDER BY journal_id LIMIT ?`
	args = append(args, int(query.Page.Size)+1)
	rows, err := r.DB.QueryContext(ctx, statement, args...)
	if err != nil {
		return model.OccurrencePage{}, readerError("Lifecycle occurrences could not be read.", "SQLite rejected the bounded projection query.", "No partial page is returned.", "Run `pasture migrate`, rebuild projections, and retry.", err)
	}
	defer rows.Close()
	items := make([]model.OccurrenceRecord, 0, int(query.Page.Size)+1)
	for rows.Next() {
		var jid, received int64
		var contractText, actorText, payloadText string
		var event uint16
		var capture uint8
		var envelopeJSON, bindingsJSON []byte
		if err := rows.Scan(&jid, &contractText, &event, &received, &actorText, &capture, &payloadText, &envelopeJSON, &bindingsJSON); err != nil {
			return model.OccurrencePage{}, readerError("A projected lifecycle occurrence could not be decoded.", "SQLite returned a row that does not match the versioned projection schema.", "No partial page is returned.", "Rebuild projections and retry.", err)
		}
		var contract ir.RuntimeContractID
		if err := json.Unmarshal([]byte(fmt.Sprintf("%q", contractText)), &contract); err != nil {
			return model.OccurrencePage{}, err
		}
		actor, err := provenance.ParseAgentID(actorText)
		if err != nil {
			return model.OccurrencePage{}, err
		}
		var envelope model.OccurrenceEnvelopeRef
		if err := json.Unmarshal(envelopeJSON, &envelope); err != nil {
			return model.OccurrencePage{}, err
		}
		var bindings []model.NativeBinding
		if err := json.Unmarshal(bindingsJSON, &bindings); err != nil {
			return model.OccurrencePage{}, err
		}
		payloadDigest, err := digest.Parse(payloadText)
		if err != nil {
			return model.OccurrencePage{}, err
		}
		items = append(items, model.NewOccurrenceRecord(model.OccurrenceID(jid), model.ContractEventKind(event), contract, envelope, time.Unix(0, received).UTC(), actor, bindings, model.CaptureDisposition(capture), model.EvidencePayloadRef{Digest: payloadDigest, Retention: envelope.Retention}))
	}
	if err := rows.Err(); err != nil {
		return model.OccurrencePage{}, err
	}
	page := model.OccurrencePage{Items: items}
	if len(items) > int(query.Page.Size) {
		page.Items = items[:int(query.Page.Size)]
		last := page.Items[len(page.Items)-1].JournalID()
		page.State = model.PageState{Next: &model.Cursor{SnapshotJournalID: snapshot, LastJournalID: last, QueryFingerprint: fingerprint}, Truncated: true, Reason: model.TruncatedPageSize}
	}
	return page, nil
}

func queryFingerprint(query model.OccurrenceQuery) model.QueryFingerprint {
	h := sha256.New()
	var value [2]byte
	for _, contract := range query.ContractFilter() {
		h.Write([]byte(contract.String()))
		h.Write([]byte{0})
	}
	for _, event := range query.EventFilter() {
		binary.BigEndian.PutUint16(value[:], uint16(event))
		h.Write(value[:])
	}
	var out model.QueryFingerprint
	copy(out[:], h.Sum(nil))
	return out
}

func placeholders(count int) string { return strings.TrimSuffix(strings.Repeat("?,", count), ",") }

func readerError(what, why, impact, fix string, cause error) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryStorage, What: what, Why: why, Where: "Reading lifecycle occurrences (internal/lifecycle/projection/reader.go in projection.Reader.Occurrences).", Impact: impact, Fix: fix, Cause: cause}
}

var _ model.LifecycleReader = Reader{}
