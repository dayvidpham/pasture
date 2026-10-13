package model

import (
	"crypto/sha256"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/provenance"
)

type DefinitionKind uint8

const (
	DefinitionRuntimeContract DefinitionKind = iota + 1
	DefinitionLifecycleSchema
	DefinitionMetamodel
	DefinitionInterpreter
	DefinitionEpochImplementation
	DefinitionContextPolicy
	DefinitionContextPacketSchema
	DefinitionRetentionPolicy
)

type DefinitionID string
type DefinitionCodec uint8

const DefinitionCanonicalJSON DefinitionCodec = 1

// IsEnvelopeDefinitionKind is the closed new-writer admission set. Metamodel
// activation retains its separate legacy owner and operation recipe.
func IsEnvelopeDefinitionKind(kind DefinitionKind) bool {
	switch kind {
	case DefinitionRuntimeContract, DefinitionLifecycleSchema, DefinitionInterpreter, DefinitionEpochImplementation, DefinitionRetentionPolicy:
		return true
	default:
		return false
	}
}

// NewDefinitionSnapshot owns defensive body construction after evidence resolution.
func NewDefinitionSnapshot(ref DefinitionRef, id DefinitionID, version uint32, codec DefinitionCodec, body []byte) (DefinitionSnapshot, error) {
	if ref.Definition <= 0 || (!IsEnvelopeDefinitionKind(ref.Kind) && ref.Kind != DefinitionMetamodel) || id == "" || version == 0 || codec != DefinitionCanonicalJSON || len(body) == 0 || ref.Content != ContentIdentity(sha256.Sum256(body)) {
		return DefinitionSnapshot{}, definitionConstructionError("NewDefinitionSnapshot", "The resolved journal coordinate, owner metadata or content digest is invalid.")
	}
	return DefinitionSnapshot{Ref: ref, ID: id, Version: version, Codec: codec, body: append([]byte(nil), body...)}, nil
}

type DefinitionRef struct {
	nonJournalValue
	Definition DefinitionJournalID
	Kind       DefinitionKind
	Content    ContentIdentity
}

type DefinitionSnapshot struct {
	Ref     DefinitionRef
	ID      DefinitionID
	Version uint32
	Codec   DefinitionCodec
	body    []byte
}

func (s DefinitionSnapshot) Body() []byte { return append([]byte(nil), s.body...) }

type DefinitionLifecycleEvent uint8

const (
	DefinitionActivated DefinitionLifecycleEvent = iota + 1
	DefinitionRetiredEvent
)

type DefinitionStateFact struct {
	StateID    DefinitionStateID
	Definition DefinitionJournalID
	Event      DefinitionLifecycleEvent
	RecordedAt time.Time
}

type DefinitionStatus uint8

const (
	DefinitionStatusActive DefinitionStatus = iota + 1
	DefinitionStatusRetired
)

type DefinitionStateRecord struct {
	Ref               DefinitionRef
	ID                DefinitionID
	Version           uint32
	Status            DefinitionStatus
	LatestState       DefinitionStateID
	LatestFact        DefinitionStateFact
	SnapshotJournalID provenance.JournalID
}

func NewDefinitionStateRecord(snapshot DefinitionSnapshot, fact DefinitionStateFact, watermark provenance.JournalID) (DefinitionStateRecord, error) {
	if snapshot.Ref.Definition <= 0 || fact.Definition != snapshot.Ref.Definition || fact.StateID <= DefinitionStateID(fact.Definition) || provenance.JournalID(fact.StateID) > watermark || fact.Event != DefinitionActivated || fact.RecordedAt.IsZero() {
		return DefinitionStateRecord{}, definitionConstructionError("NewDefinitionStateRecord", "The resolved activation coordinate, event, timestamp or snapshot watermark is invalid.")
	}
	return DefinitionStateRecord{Ref: snapshot.Ref, ID: snapshot.ID, Version: snapshot.Version, Status: DefinitionStatusActive, LatestState: fact.StateID, LatestFact: fact, SnapshotJournalID: watermark}, nil
}

func definitionConstructionError(owner, reason string) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryValidation,
		What: "The resolved definition could not be constructed.", Why: reason,
		Where:  "internal/lifecycle/model/definition.go in model." + owner + " during resolved model construction",
		Impact: "No definition snapshot or state was returned; the journal was not changed.",
		Fix:    "Resolve the matching snapshot/state through the definition owner; restore damaged evidence from a verified backup."}
}

type RuntimeContractDefinitionRef struct {
	nonJournalValue
	Definition DefinitionRef
	Contract   ir.RuntimeContractID
}
type LifecycleSchemaDefinitionRef struct {
	nonJournalValue
	Definition DefinitionRef
}
type LifecycleMetamodelRef struct {
	nonJournalValue
	Definition DefinitionRef
}
type InterpreterDefinitionRef struct {
	nonJournalValue
	Definition DefinitionRef
}
type EpochImplementationRef struct {
	nonJournalValue
	Definition DefinitionRef
}
type ContextPolicyDefinitionRef struct {
	nonJournalValue
	Definition DefinitionRef
}
type ContextPacketSchemaRef struct {
	nonJournalValue
	Definition DefinitionRef
}

// RetentionPolicyDefinitionRef identifies the versioned store-all SQLite policy.
// The MVP has no alternate retention mode.
type RetentionPolicyDefinitionRef struct {
	nonJournalValue
	Definition DefinitionRef
}
