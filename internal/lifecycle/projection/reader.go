package projection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
)

const interpretedKind = provenance.EvidenceKind("pasture.lifecycle.interpreted.v1")

type Reader struct {
	DB    *sql.DB
	Facts provenance.FactQueryAPI
}

func (r Reader) Records(ctx context.Context, query model.OccurrenceQuery) (model.LifecyclePage, error) {
	page, snapshot, err := r.occurrences(ctx, query)
	if err != nil {
		return model.LifecyclePage{}, err
	}
	associated, err := r.interpretations(page.Items, snapshot)
	if err != nil {
		return model.LifecyclePage{}, err
	}
	items := make([]model.LifecycleRecord, 0, len(page.Items))
	for _, occurrence := range page.Items {
		record, err := model.NewLifecycleRecord(occurrence, associated[occurrence.JournalID()])
		if err != nil {
			return model.LifecyclePage{}, err
		}
		items = append(items, record)
	}
	return model.LifecyclePage{Items: items, State: page.State}, nil
}

func (r Reader) interpretations(occurrences []model.OccurrenceRecord, snapshot provenance.JournalID) (map[provenance.JournalID][]model.InterpretedRecord, error) {
	result := make(map[provenance.JournalID][]model.InterpretedRecord, len(occurrences))
	if len(occurrences) == 0 {
		return result, nil
	}
	wanted := map[provenance.JournalID]struct{}{}
	for _, o := range occurrences {
		wanted[o.JournalID()] = struct{}{}
	}
	operationOccurrence := map[provenance.OperationID]model.OccurrenceID{}
	q := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}}, Kinds: []provenance.EvidenceKind{occurrenceEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize, SnapshotMaxJournalID: snapshot}}
	for {
		page, err := r.Facts.QueryEvidence(q)
		if err != nil {
			return nil, readerError("Lifecycle occurrence operations could not be resolved.", "The pinned Provenance occurrence query failed.", "No partial page is returned.", "Repair journal topology and retry.", err)
		}
		if page.SnapshotMaxJournalID != snapshot {
			return nil, snapshotDriftError()
		}
		for _, row := range page.Rows {
			if _, ok := wanted[row.JournalID]; ok {
				if prior, exists := operationOccurrence[row.ProducingOperationID]; exists && prior.JournalID() != row.JournalID {
					return nil, readerError("A lifecycle operation produced multiple occurrences.", "Operation association is ambiguous.", "No partial page is returned.", "Repair the corrupt operation.", nil)
				}
				operationOccurrence[row.ProducingOperationID] = model.OccurrenceID(row.JournalID)
			}
		}
		if page.Next == nil {
			break
		}
		q.Page.AfterJournalID = page.Next.AfterJournalID
	}
	operations := make([]provenance.OperationID, 0, len(operationOccurrence))
	for op := range operationOccurrence {
		operations = append(operations, op)
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i] < operations[j] })
	for start := 0; start < len(operations); start += provenance.MaxFactFilterValues {
		end := start + provenance.MaxFactFilterValues
		if end > len(operations) {
			end = len(operations)
		}
		fq := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}, OperationIDs: operations[start:end]}, Kinds: []provenance.EvidenceKind{interpretedKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize, SnapshotMaxJournalID: snapshot}}
		for {
			page, err := r.Facts.QueryEvidence(fq)
			if err != nil {
				return nil, readerError("Interpreted lifecycle evidence could not be read.", "The pinned operation-filtered Provenance query failed.", "No partial page is returned.", "Repair journal evidence and retry.", err)
			}
			if page.SnapshotMaxJournalID != snapshot {
				return nil, snapshotDriftError()
			}
			for _, row := range page.Rows {
				occurrence, ok := operationOccurrence[row.ProducingOperationID]
				if !ok {
					return nil, readerError("Interpreted evidence crossed operation boundaries.", "The producing operation is not associated with a selected occurrence.", "No partial page is returned.", "Repair the corrupt journal association.", nil)
				}
				if len(result[occurrence.JournalID()]) != 0 {
					return nil, readerError("A lifecycle occurrence has duplicate interpretations.", "Its operation produced more than one interpreted fact.", "No partial page is returned.", "Repair the corrupt operation.", nil)
				}
				sum := sha256.Sum256(row.Payload)
				if !bytes.Equal(sum[:], row.ContentDigest) {
					return nil, readerError("Interpreted lifecycle evidence failed digest validation.", "Stored digest differs from the exact payload.", "No partial page is returned.", "Restore canonical evidence.", nil)
				}
				decoded, err := receipt.DecodeInterpreted(model.InterpretationID(row.JournalID), occurrence, row.Payload)
				if err != nil {
					return nil, readerError("Interpreted lifecycle evidence is malformed or noncanonical.", err.Error(), "No partial page is returned.", "Repair the evidence producer and row.", err)
				}
				result[occurrence.JournalID()] = []model.InterpretedRecord{decoded}
			}
			if page.Next == nil {
				break
			}
			fq.Page.AfterJournalID = page.Next.AfterJournalID
		}
	}
	return result, nil
}

func snapshotDriftError() error {
	return readerError("The lifecycle fact snapshot drifted.", "Provenance returned a watermark different from the occurrence page.", "No partial page is returned.", "Restart pagination after repairing journal reads.", nil)
}

func (r Reader) Payload(ctx context.Context, ref digest.Digest) ([]byte, error) {
	if r.DB == nil || ref.Algorithm() != digest.SHA256 || ref.Validate() != nil {
		return nil, readerError("The lifecycle payload request is invalid.", "A canonical sha256 digest and unified database are required.", "No payload was read.", "Pass a canonical sha256 digest.", nil)
	}
	var body []byte
	var count int
	if err := r.DB.QueryRowContext(ctx, `SELECT body, byte_count FROM lifecycle_payload_blobs WHERE digest=?`, ref.String()).Scan(&body, &count); err != nil {
		return nil, readerError("The lifecycle payload could not be read.", "The digest is missing or SQLite rejected the lookup.", "No payload was returned.", "Verify the digest and database.", err)
	}
	if len(body) != count || digest.FromBytes(body) != ref {
		return nil, readerError("The lifecycle payload failed integrity validation.", "Stored byte count or digest differs from the body.", "Corrupt bytes were not returned.", "Restore the blob from trusted evidence.", nil)
	}
	return append([]byte(nil), body...), nil
}

func (r Reader) Occurrences(ctx context.Context, q model.OccurrenceQuery) (model.OccurrencePage, error) {
	page, _, err := r.occurrences(ctx, q)
	return page, err
}

func (r Reader) occurrences(ctx context.Context, query model.OccurrenceQuery) (model.OccurrencePage, provenance.JournalID, error) {
	if r.DB == nil || r.Facts == nil {
		return model.OccurrencePage{}, 0, readerError("The lifecycle reader is incompletely wired.", "Database and Provenance fact query dependencies are required.", "No records were read.", "Construct it through tasks.NewLifecycleReader.", nil)
	}
	normalized, err := normalizeQuery(query)
	if err != nil {
		return model.OccurrencePage{}, 0, err
	}
	fingerprint := queryFingerprint(normalized)
	after, snapshot := provenance.JournalID(0), provenance.JournalID(0)
	if normalized.Page.Cursor != nil {
		if normalized.Page.Cursor.QueryFingerprint != fingerprint {
			return model.OccurrencePage{}, 0, readerError("The lifecycle cursor belongs to a different query.", "Its normalized filters do not match.", "No records were read.", "Restart without the cursor.", nil)
		}
		after, snapshot = normalized.Page.Cursor.LastJournalID, normalized.Page.Cursor.SnapshotJournalID
		if snapshot <= 0 || after < 0 || after > snapshot {
			return model.OccurrencePage{}, 0, readerError("The lifecycle cursor bounds are invalid.", "Journal positions must be positive and ordered.", "No records were read.", "Use a cursor emitted by this command.", nil)
		}
	} else {
		watermark, err := r.Facts.QueryEvidence(provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}}, Kinds: []provenance.EvidenceKind{interpretedKind}, Page: provenance.FactPageRequest{Limit: 1}})
		if err != nil {
			return model.OccurrencePage{}, 0, readerError("The lifecycle reader could not establish a global snapshot.", "Provenance rejected the watermark query.", "No records were read.", "Repair journal reads.", err)
		}
		snapshot = watermark.SnapshotMaxJournalID
	}
	statement := `SELECT journal_id,contract,event_kind,received_at,actor_id,capture_disposition,payload_digest,envelope_json FROM lifecycle_occurrences WHERE journal_id>? AND journal_id<=?`
	args := []any{after, snapshot}
	if len(normalized.Contracts) > 0 {
		statement += ` AND contract IN (` + placeholders(len(normalized.Contracts)) + `)`
		for _, v := range normalized.Contracts {
			args = append(args, v.String())
		}
	}
	if len(normalized.Events) > 0 {
		statement += ` AND event_kind IN (` + placeholders(len(normalized.Events)) + `)`
		for _, v := range normalized.Events {
			args = append(args, v)
		}
	}
	for _, b := range normalized.Bindings {
		statement += ` AND EXISTS (SELECT 1 FROM lifecycle_occurrence_bindings b WHERE b.journal_id=lifecycle_occurrences.journal_id AND b.binding_kind=? AND b.native_name=? AND b.binding_value=?)`
		args = append(args, b.Kind, []byte(b.NativeName), []byte(b.Value))
	}
	statement += ` ORDER BY journal_id LIMIT ?`
	args = append(args, int(normalized.Page.Size)+1)
	rows, err := r.DB.QueryContext(ctx, statement, args...)
	if err != nil {
		return model.OccurrencePage{}, 0, readerError("Lifecycle occurrences could not be read.", "SQLite rejected the bounded projection query.", "No partial page is returned.", "Run pasture migrate, rebuild, and retry.", err)
	}
	type pendingOccurrence struct {
		jid, received                        int64
		contractText, actorText, payloadText string
		event                                uint16
		capture                              uint8
		envelopeJSON                         []byte
	}
	pending := make([]pendingOccurrence, 0, int(normalized.Page.Size)+1)
	for rows.Next() {
		var item pendingOccurrence
		if err := rows.Scan(&item.jid, &item.contractText, &item.event, &item.received, &item.actorText, &item.capture, &item.payloadText, &item.envelopeJSON); err != nil {
			return model.OccurrencePage{}, 0, readerError("A projected lifecycle occurrence could not be decoded.", "The row does not match schema v7.", "No partial page is returned.", "Rebuild projections.", err)
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return model.OccurrencePage{}, 0, err
	}
	if err := rows.Close(); err != nil {
		return model.OccurrencePage{}, 0, err
	}
	items := make([]model.OccurrenceRecord, 0, len(pending))
	for _, item := range pending {
		var contract ir.RuntimeContractID
		if err := json.Unmarshal([]byte(fmt.Sprintf("%q", item.contractText)), &contract); err != nil {
			return model.OccurrencePage{}, 0, err
		}
		actor, err := provenance.ParseAgentID(item.actorText)
		if err != nil {
			return model.OccurrencePage{}, 0, err
		}
		var envelope model.OccurrenceEnvelopeRef
		if err := json.Unmarshal(item.envelopeJSON, &envelope); err != nil {
			return model.OccurrencePage{}, 0, err
		}
		bindings, err := readBindings(ctx, r.DB, provenance.JournalID(item.jid))
		if err != nil {
			return model.OccurrencePage{}, 0, err
		}
		ref, err := digest.Parse(item.payloadText)
		if err != nil {
			return model.OccurrencePage{}, 0, err
		}
		items = append(items, model.NewOccurrenceRecord(model.OccurrenceID(item.jid), model.ContractEventKind(item.event), contract, envelope, time.Unix(0, item.received).UTC(), actor, bindings, model.CaptureDisposition(item.capture), model.EvidencePayloadRef{Digest: ref, Retention: envelope.Retention}))
	}
	page := model.OccurrencePage{Items: items}
	if len(items) > int(normalized.Page.Size) {
		page.Items = items[:int(normalized.Page.Size)]
		last := page.Items[len(page.Items)-1].JournalID()
		page.State = model.PageState{Next: &model.Cursor{SnapshotJournalID: snapshot, LastJournalID: last, QueryFingerprint: fingerprint}, Truncated: true, Reason: model.TruncatedPageSize}
	}
	return page, snapshot, nil
}

func readBindings(ctx context.Context, db *sql.DB, jid provenance.JournalID) ([]model.NativeBinding, error) {
	rows, err := db.QueryContext(ctx, `SELECT binding_kind,native_name,binding_value FROM lifecycle_occurrence_bindings WHERE journal_id=? ORDER BY binding_index`, jid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.NativeBinding
	for rows.Next() {
		var kind uint8
		var name, value []byte
		if err := rows.Scan(&kind, &name, &value); err != nil {
			return nil, err
		}
		out = append(out, model.NativeBinding{Kind: model.NativeBindingKind(kind), NativeName: string(name), Value: string(value)})
	}
	return out, rows.Err()
}

func normalizeQuery(q model.OccurrenceQuery) (model.OccurrenceQuery, error) {
	if q.Page.Size == 0 || int(q.Page.Size) > model.MaxPageSize {
		return model.OccurrenceQuery{}, readerError("The lifecycle page size is invalid.", "Reads must be explicitly bounded.", "No records were read.", fmt.Sprintf("Choose 1 through %d.", model.MaxPageSize), nil)
	}
	if len(q.Contracts) > model.MaxQueryFilterValues || len(q.Events) > model.MaxQueryFilterValues || len(q.Bindings) > model.MaxQueryFilterValues {
		return model.OccurrenceQuery{}, readerError("A lifecycle filter exceeds its static bound.", "Each axis accepts at most 64 values.", "No records were read.", "Reduce each filter axis to 64 values.", nil)
	}
	out := q
	out.Contracts = append([]ir.RuntimeContractID(nil), q.Contracts...)
	sort.Slice(out.Contracts, func(i, j int) bool { return out.Contracts[i].String() < out.Contracts[j].String() })
	out.Contracts = dedup(out.Contracts, func(a, b ir.RuntimeContractID) bool { return a == b })
	for _, v := range out.Contracts {
		if !v.IsValid() {
			return model.OccurrenceQuery{}, readerError("A lifecycle contract filter is invalid.", "Only constructor-valid runtime contracts may be queried.", "No records were read.", "Use an exact runtime contract ID.", nil)
		}
	}
	out.Events = append([]model.ContractEventKind(nil), q.Events...)
	sort.Slice(out.Events, func(i, j int) bool { return out.Events[i] < out.Events[j] })
	out.Events = dedup(out.Events, func(a, b model.ContractEventKind) bool { return a == b })
	for _, v := range out.Events {
		if v == 0 {
			return model.OccurrenceQuery{}, readerError("A lifecycle event filter is invalid.", "Event zero is not declared.", "No records were read.", "Use a typed event ordinal.", nil)
		}
	}
	out.Bindings = append([]model.NativeBinding(nil), q.Bindings...)
	sort.Slice(out.Bindings, func(i, j int) bool {
		a, b := out.Bindings[i], out.Bindings[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.NativeName != b.NativeName {
			return a.NativeName < b.NativeName
		}
		return a.Value < b.Value
	})
	out.Bindings = dedup(out.Bindings, func(a, b model.NativeBinding) bool {
		return a.Kind == b.Kind && a.NativeName == b.NativeName && a.Value == b.Value
	})
	for _, b := range out.Bindings {
		if model.ValidateNativeBinding(b) != nil {
			return model.OccurrenceQuery{}, readerError("A lifecycle binding filter is invalid.", "Kind, UTF-8 text, controls, padding, or byte bounds violate the exact BLOB contract.", "No records were read.", "Use a declared kind and 1..512 unpadded printable UTF-8 bytes.", nil)
		}
	}
	return out, nil
}

// QueryFingerprint returns the normalized semantic filter identity used by cursors.
func QueryFingerprint(q model.OccurrenceQuery) (model.QueryFingerprint, error) {
	normalized, err := normalizeQuery(q)
	if err != nil {
		return model.QueryFingerprint{}, err
	}
	return queryFingerprint(normalized), nil
}

func dedup[T any](in []T, equal func(T, T) bool) []T {
	out := in[:0]
	for _, v := range in {
		if len(out) == 0 || !equal(out[len(out)-1], v) {
			out = append(out, v)
		}
	}
	return out
}
func queryFingerprint(q model.OccurrenceQuery) model.QueryFingerprint {
	h := sha256.New()
	write := func(v []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(v)))
		h.Write(n[:])
		h.Write(v)
	}
	for _, v := range q.Contracts {
		write([]byte(v.String()))
	}
	for _, v := range q.Events {
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(v))
		write(b[:])
	}
	for _, v := range q.Bindings {
		write([]byte{byte(v.Kind)})
		write([]byte(v.NativeName))
		write([]byte(v.Value))
	}
	var out model.QueryFingerprint
	copy(out[:], h.Sum(nil))
	return out
}
func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
func readerError(what, why, impact, fix string, cause error) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryStorage, What: what, Why: why, Where: "Reading lifecycle records (internal/lifecycle/projection/reader.go).", Impact: impact, Fix: fix, Cause: cause}
}

var _ model.LifecycleReader = Reader{}
