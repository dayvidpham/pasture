package protocol

// This file defines the public review/role enums consumed across the toolkit
// (engine, handlers, codegen, formatters). They are part of the public API so
// the durable engine and its consumers share one canonical definition without
// routing through an internal package.

// ─── VoteType ────────────────────────────────────────────────────────────────

// VoteType represents a binary review vote.
// Values match schema.xml <enum name="VoteType"> entries.
type VoteType string

const (
	VoteAccept VoteType = "ACCEPT"
	VoteRevise VoteType = "REVISE"
)

// AllVoteTypes is the ordered slice of all valid VoteType values.
var AllVoteTypes = []VoteType{VoteAccept, VoteRevise}

// IsValid reports whether v is a known VoteType value.
func (v VoteType) IsValid() bool {
	switch v {
	case VoteAccept, VoteRevise:
		return true
	}
	return false
}

// ─── ReviewAxis ──────────────────────────────────────────────────────────────

// ReviewAxis identifies a semantic dimension of a code review vote.
// Values are lowercase wire-format strings used in JSON serialization.
type ReviewAxis string

const (
	AxisCorrectness ReviewAxis = "correctness"
	AxisTestQuality ReviewAxis = "test_quality"
	AxisElegance    ReviewAxis = "elegance"
)

// AllReviewAxes is the ordered slice of all valid ReviewAxis values.
var AllReviewAxes = []ReviewAxis{AxisCorrectness, AxisTestQuality, AxisElegance}

// IsValid reports whether a is a known ReviewAxis value.
func (a ReviewAxis) IsValid() bool {
	switch a {
	case AxisCorrectness, AxisTestQuality, AxisElegance:
		return true
	}
	return false
}

// ─── SeverityLevel ──────────────────────────────────────────────────────────

// SeverityLevel classifies the severity of a code review finding.
// Used as the key type for ReviewCycleRecord.FindingCounts to prevent
// stringly-typed map access.
type SeverityLevel string

const (
	SeverityBlocker   SeverityLevel = "blocker"
	SeverityImportant SeverityLevel = "important"
	SeverityMinor     SeverityLevel = "minor"
)

// AllSeverityLevels is the ordered slice of all valid SeverityLevel values.
var AllSeverityLevels = []SeverityLevel{SeverityBlocker, SeverityImportant, SeverityMinor}

// IsValid reports whether s is a known SeverityLevel value.
func (s SeverityLevel) IsValid() bool {
	switch s {
	case SeverityBlocker, SeverityImportant, SeverityMinor:
		return true
	}
	return false
}

// ─── RoleId ──────────────────────────────────────────────────────────────────

// RoleId identifies an agent role within the protocol.
// Values match schema.xml <role id="..."> elements.
type RoleId string

const (
	RoleEpoch      RoleId = "epoch"
	RoleArchitect  RoleId = "architect"
	RoleReviewer   RoleId = "reviewer"
	RoleSupervisor RoleId = "supervisor"
	RoleWorker     RoleId = "worker"
)

// AllRoleIds is the ordered slice of all valid RoleId values.
var AllRoleIds = []RoleId{RoleEpoch, RoleArchitect, RoleReviewer, RoleSupervisor, RoleWorker}

// IsValid reports whether r is a known RoleId value.
func (r RoleId) IsValid() bool {
	switch r {
	case RoleEpoch, RoleArchitect, RoleReviewer, RoleSupervisor, RoleWorker:
		return true
	}
	return false
}
