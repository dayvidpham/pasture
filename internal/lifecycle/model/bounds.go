package model

import (
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/provenance"
)

const (
	MaxPageSize           = 256
	MaxQueryFilterValues  = 64
	MaxNativePayloadBytes = 1 << 20
)

// nonJournalValue is an AST-visible declaration marker. Exported structs which
// are values rather than journal records embed it; the static classifier can
// therefore distinguish them without maintaining an exception list.
type nonJournalValue struct{}

type PageSize uint16

func NewPageSize(size uint16) (PageSize, error) {
	if size == 0 || size > MaxPageSize {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The requested lifecycle page size %d is outside the supported range.", size),
			Why:      fmt.Sprintf("Lifecycle pages must contain between 1 and %d records so reads stay bounded.", MaxPageSize),
			Where:    "Creating a page size (internal/lifecycle/model/bounds.go in model.NewPageSize).",
			Impact:   "The query was rejected before storage was accessed.",
			Fix:      fmt.Sprintf("Choose a page size from 1 through %d.", MaxPageSize),
		}
	}
	return PageSize(size), nil
}

type QueryFingerprint [32]byte

type Cursor struct {
	nonJournalValue
	SnapshotJournalID provenance.JournalID
	LastJournalID     provenance.JournalID
	QueryFingerprint  QueryFingerprint
}

type PageRequest struct {
	nonJournalValue
	Size   PageSize
	Cursor *Cursor
}

type TruncationReason uint8

const (
	TruncatedPageSize TruncationReason = iota + 1
	TruncatedDepthLimit
	TruncatedNodeLimit
	TruncatedEdgeLimit
	TruncatedHistoryBound
)

type PageState struct {
	nonJournalValue
	Next      *Cursor
	Truncated bool
	Reason    TruncationReason
}

type CursorErrorKind uint8

const (
	CursorInvalidPageSize CursorErrorKind = iota + 1
	CursorQueryMismatch
	CursorSnapshotFuture
	CursorStale
	CursorTampered
	CursorUnsupportedVersion
	CursorInvalidLineageBounds
)
