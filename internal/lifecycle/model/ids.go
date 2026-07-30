// Package model defines the target-neutral lifecycle journal contract.
package model

import (
	"time"

	"github.com/dayvidpham/provenance"
)

type ContentIdentity [32]byte

type OccurrenceID provenance.JournalID
type InterpretationID provenance.JournalID
type LifecycleFactID provenance.JournalID
type UnresolvedFactID provenance.JournalID
type LifecycleLinkID provenance.JournalID
type TransitionCandidateID provenance.JournalID
type CapabilityID provenance.JournalID
type CapabilityStateID provenance.JournalID
type IssuedRequestID provenance.JournalID
type PhaseStateID provenance.JournalID
type DefinitionJournalID provenance.JournalID
type DefinitionStateID provenance.JournalID
type DisclosurePlanID provenance.JournalID
type DisclosureAttemptID provenance.JournalID
type DisclosureResultID provenance.JournalID

func (id OccurrenceID) JournalID() provenance.JournalID          { return provenance.JournalID(id) }
func (id InterpretationID) JournalID() provenance.JournalID      { return provenance.JournalID(id) }
func (id LifecycleFactID) JournalID() provenance.JournalID       { return provenance.JournalID(id) }
func (id UnresolvedFactID) JournalID() provenance.JournalID      { return provenance.JournalID(id) }
func (id LifecycleLinkID) JournalID() provenance.JournalID       { return provenance.JournalID(id) }
func (id TransitionCandidateID) JournalID() provenance.JournalID { return provenance.JournalID(id) }
func (id CapabilityID) JournalID() provenance.JournalID          { return provenance.JournalID(id) }
func (id CapabilityStateID) JournalID() provenance.JournalID     { return provenance.JournalID(id) }
func (id IssuedRequestID) JournalID() provenance.JournalID       { return provenance.JournalID(id) }
func (id PhaseStateID) JournalID() provenance.JournalID          { return provenance.JournalID(id) }
func (id DefinitionJournalID) JournalID() provenance.JournalID   { return provenance.JournalID(id) }
func (id DefinitionStateID) JournalID() provenance.JournalID     { return provenance.JournalID(id) }
func (id DisclosurePlanID) JournalID() provenance.JournalID      { return provenance.JournalID(id) }
func (id DisclosureAttemptID) JournalID() provenance.JournalID   { return provenance.JournalID(id) }
func (id DisclosureResultID) JournalID() provenance.JournalID    { return provenance.JournalID(id) }

type ContractEventKind uint16
type NativeFieldID uint16

type Clock interface{ Now() time.Time }
type OperationIDSource interface {
	NewOperationID() (provenance.OperationID, error)
}

type Confidence uint8

const (
	ConfidenceObserved Confidence = iota + 1
	ConfidenceCorrelated
	ConfidenceVerified
)

type IngressDeadlineError struct {
	nonJournalValue
	Deadline time.Duration
	Elapsed  time.Duration
}

func (e IngressDeadlineError) Error() string {
	return "the lifecycle occurrence commit could not acquire the shared database writer before its deadline; the payload blob may remain as a reclaimable orphan, no occurrence was recorded, and the operator should reduce concurrent hook ingress or retry the delivery"
}
