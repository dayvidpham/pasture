package model

import (
	"context"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

type OccurrenceQuery struct {
	nonJournalValue
	Contracts []ir.RuntimeContractID
	Events    []ContractEventKind
	Page      PageRequest
}

func (q OccurrenceQuery) ContractFilter() []ir.RuntimeContractID {
	return append([]ir.RuntimeContractID(nil), q.Contracts...)
}
func (q OccurrenceQuery) EventFilter() []ContractEventKind {
	return append([]ContractEventKind(nil), q.Events...)
}

type OccurrencePage struct {
	nonJournalValue
	Items []OccurrenceRecord
	State PageState
}

func (p OccurrencePage) Records() []OccurrenceRecord {
	return append([]OccurrenceRecord(nil), p.Items...)
}

// LifecycleReader exposes bounded occurrence history. Blob bodies are resolved
// separately by their EvidencePayloadRef, so a reader never implies atomicity
// between the idempotent blob write and the later occurrence commit.
type LifecycleReader interface {
	Occurrences(context.Context, OccurrenceQuery) (OccurrencePage, error)
}
