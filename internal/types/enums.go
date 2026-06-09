// Package types defines internal enums and value types for the Pasture daemon
// and CLI. These types are not part of the public API surface but are shared
// across internal packages.
//
// Internal packages import pkg/protocol directly for public convergence types
// (D11: no internal/types/aliases.go). The review/role enums (VoteType,
// ReviewAxis, SeverityLevel, RoleId) live in pkg/protocol; this package keeps
// only the CLI/release value types below.
package types

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
