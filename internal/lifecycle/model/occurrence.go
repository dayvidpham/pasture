package model

import (
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
)

type CaptureDisposition uint8

const (
	CaptureValid CaptureDisposition = iota + 1
	CaptureMalformed
	CaptureDuplicateField
	CaptureInvalidUTF8
	CaptureTruncated
	CaptureOverLimit
	CaptureUnsupportedSchema
	CaptureEventMismatch
)

type NativeBindingKind uint8

const (
	BindingSession NativeBindingKind = iota + 1
	BindingTurn
	BindingRequest
	BindingToolCall
	BindingAgent
	BindingMessage
	BindingTask
	BindingWorktree
)

type NativeBinding struct {
	nonJournalValue
	Kind  NativeBindingKind
	Value string
}

// EvidencePayloadRef identifies a body in the content-addressed SQLite blob
// store. Every occurrence has one. Its digest is body identity, never occurrence
// identity; repeated deliveries may therefore share this value.
type EvidencePayloadRef struct {
	nonJournalValue
	Digest    digest.Digest
	Retention RetentionPolicyDefinitionRef
}

type OccurrenceRecord struct {
	OccurrenceID    OccurrenceID
	Kind            ContractEventKind
	RuntimeContract ir.RuntimeContractID
	Envelope        OccurrenceEnvelopeRef
	ReceivedAt      time.Time
	Actor           provenance.AgentID
	bindings        []NativeBinding
	Capture         CaptureDisposition
	Payload         EvidencePayloadRef
}

func (r OccurrenceRecord) JournalID() provenance.JournalID { return r.OccurrenceID.JournalID() }
func (r OccurrenceRecord) Bindings() []NativeBinding {
	return append([]NativeBinding(nil), r.bindings...)
}
