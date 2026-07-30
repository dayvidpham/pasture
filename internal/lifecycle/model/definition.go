package model

import (
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/provenance"
)

type DefinitionKind uint8

const (
	DefinitionRuntimeContract DefinitionKind = iota + 1
	DefinitionLifecycleSchema
	DefinitionCodebook
	DefinitionInterpreter
	DefinitionEpochImplementation
	DefinitionContextPolicy
	DefinitionContextPacketSchema
	DefinitionRetentionPolicy
)

type DefinitionID string
type DefinitionCodec uint8

const DefinitionCanonicalJSON DefinitionCodec = 1

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
	SnapshotJournalID provenance.JournalID
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
type CodebookDefinitionRef struct {
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
