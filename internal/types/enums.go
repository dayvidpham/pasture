// Package types defines internal enums and value types for the Pasture daemon
// and CLI. These types are not part of the public API surface but are shared
// across internal packages.
//
// Internal packages import pkg/protocol directly for public convergence types
// (D11: no internal/types/aliases.go).
package types

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
// Values are lowercase wire-format strings used in JSON and Temporal serialization.
type ReviewAxis string

const (
	AxisCorrectness  ReviewAxis = "correctness"
	AxisTestQuality  ReviewAxis = "test_quality"
	AxisElegance     ReviewAxis = "elegance"
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

// ─── OutputFormat ────────────────────────────────────────────────────────────

// OutputFormat specifies the CLI output format for pasture-msg commands.
type OutputFormat string

const (
	OutputJSON OutputFormat = "json"
	OutputText OutputFormat = "text"
)

// AllOutputFormats is the ordered slice of all valid OutputFormat values.
var AllOutputFormats = []OutputFormat{OutputJSON, OutputText}

// IsValid reports whether f is a known OutputFormat value.
func (f OutputFormat) IsValid() bool {
	switch f {
	case OutputJSON, OutputText:
		return true
	}
	return false
}

// ─── AuditTrailBackend ───────────────────────────────────────────────────────

// AuditTrailBackend specifies the audit trail storage backend for pastured.
type AuditTrailBackend string

const (
	BackendMemory AuditTrailBackend = "memory"
	BackendSqlite AuditTrailBackend = "sqlite"
)

// AllAuditTrailBackends is the ordered slice of all valid AuditTrailBackend values.
var AllAuditTrailBackends = []AuditTrailBackend{BackendMemory, BackendSqlite}

// IsValid reports whether b is a known AuditTrailBackend value.
func (b AuditTrailBackend) IsValid() bool {
	switch b {
	case BackendMemory, BackendSqlite:
		return true
	}
	return false
}

// ─── BumpKind ────────────────────────────────────────────────────────────────

// BumpKind specifies the semver component to increment in pasture-release.
type BumpKind string

const (
	BumpMajor BumpKind = "major"
	BumpMinor BumpKind = "minor"
	BumpPatch BumpKind = "patch"
)

// AllBumpKinds is the ordered slice of all valid BumpKind values.
var AllBumpKinds = []BumpKind{BumpMajor, BumpMinor, BumpPatch}

// IsValid reports whether k is a known BumpKind value.
func (k BumpKind) IsValid() bool {
	switch k {
	case BumpMajor, BumpMinor, BumpPatch:
		return true
	}
	return false
}

// ─── Domain ──────────────────────────────────────────────────────────────────

// Domain classifies a phase into a high-level lifecycle domain.
// Values match schema.xml <enum name="DomainType"> entries.
type Domain string

const (
	DomainUser Domain = "user"
	DomainPlan Domain = "plan"
	DomainImpl Domain = "impl"
)

// AllDomains is the ordered slice of all valid Domain values.
var AllDomains = []Domain{DomainUser, DomainPlan, DomainImpl}

// IsValid reports whether d is a known Domain value.
func (d Domain) IsValid() bool {
	switch d {
	case DomainUser, DomainPlan, DomainImpl:
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
