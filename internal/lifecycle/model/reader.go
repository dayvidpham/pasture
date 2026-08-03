package model

import (
	"context"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	digest "github.com/opencontainers/go-digest"
)

type OccurrenceQuery struct {
	nonJournalValue
	Contracts []ir.RuntimeContractID
	Events    []ContractEventKind
	Bindings  []NativeBinding
	Page      PageRequest
}

func (q OccurrenceQuery) BindingFilter() []NativeBinding {
	return append([]NativeBinding(nil), q.Bindings...)
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

type LifecyclePage struct {
	nonJournalValue
	Items []LifecycleRecord
	State PageState
}

func (p LifecyclePage) Records() []LifecycleRecord { return append([]LifecycleRecord(nil), p.Items...) }

// LifecycleReader exposes bounded occurrence history. Blob bodies are resolved
// separately by their EvidencePayloadRef, so a reader never implies atomicity
// between the idempotent blob write and the later occurrence commit.
type LifecycleReader interface {
	Occurrences(context.Context, OccurrenceQuery) (OccurrencePage, error)
	Records(context.Context, OccurrenceQuery) (LifecyclePage, error)
	Payload(context.Context, digest.Digest) ([]byte, error)
}
