package model

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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

func (k NativeBindingKind) IsValid() bool { return k >= BindingSession && k <= BindingWorktree }
func (k NativeBindingKind) String() string {
	if !k.IsValid() {
		return ""
	}
	return [...]string{"", "session", "turn", "request", "tool-call", "agent", "message", "task", "worktree"}[k]
}
func ParseNativeBindingKind(token string) (NativeBindingKind, error) {
	for k := BindingSession; k <= BindingWorktree; k++ {
		if k.String() == token {
			return k, nil
		}
	}
	return 0, fmt.Errorf("unknown lifecycle binding kind %q", token)
}
func ValidateNativeBinding(binding NativeBinding) error {
	if !binding.Kind.IsValid() {
		return fmt.Errorf("binding kind %d is not declared", binding.Kind)
	}
	if err := ValidateBindingText("native name", binding.NativeName); err != nil {
		return err
	}
	return ValidateBindingText("value", binding.Value)
}
func ValidateBindingText(field, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("binding %s is not valid UTF-8", field)
	}
	if len(value) < 1 || len(value) > 512 {
		return fmt.Errorf("binding %s must contain 1 through 512 bytes", field)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("binding %s has leading or trailing padding", field)
	}
	for _, r := range value {
		if r == 0 || r < 0x20 || r == 0x7f {
			return fmt.Errorf("binding %s contains a NUL or control character", field)
		}
	}
	return nil
}

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
	Kind       NativeBindingKind
	NativeName string
	Value      string
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

// NewOccurrenceRecord constructs the immutable public view used by replay
// projections. Bindings are defensively copied at the boundary.
func NewOccurrenceRecord(id OccurrenceID, kind ContractEventKind, contract ir.RuntimeContractID, envelope OccurrenceEnvelopeRef, receivedAt time.Time, actor provenance.AgentID, bindings []NativeBinding, capture CaptureDisposition, payload EvidencePayloadRef) OccurrenceRecord {
	return OccurrenceRecord{OccurrenceID: id, Kind: kind, RuntimeContract: contract, Envelope: envelope, ReceivedAt: receivedAt, Actor: actor, bindings: append([]NativeBinding(nil), bindings...), Capture: capture, Payload: payload}
}
