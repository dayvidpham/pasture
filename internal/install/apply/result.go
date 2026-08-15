// Package apply owns the typed request, result, error, and activator contracts
// plus concrete activation-strategy adapters. Package service alone owns
// orchestration, canonical execution order, and registry persistence.
package apply

import (
	"bytes"
	"encoding/json"

	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

// ResultSchemaID and ErrorSchemaID are the frozen transient document schemas.
const (
	ResultSchemaID = "pasture.install.apply-result/v1"
	ErrorSchemaID  = "pasture.install.apply-error/v1"
)

// Status is a typed action-row outcome.
type Status struct{ name string }

var (
	statusCompleted     = Status{name: "completed"}
	statusFailed        = Status{name: "failed"}
	statusUnattempted   = Status{name: "unattempted"}
	statusDeclarative   = Status{name: "managed_declaratively"}
	statusPendingTrust  = Status{name: "installed_pending_trust"}
	statusNoOp          = Status{name: "no_op"}
	canonicalStatusList = [...]Status{
		statusCompleted, statusFailed, statusUnattempted,
		statusDeclarative, statusPendingTrust, statusNoOp,
	}
)

// Status accessors for callers and goldens.
func Completed() Status             { return statusCompleted }
func Failed() Status                { return statusFailed }
func Unattempted() Status           { return statusUnattempted }
func ManagedDeclaratively() Status  { return statusDeclarative }
func InstalledPendingTrust() Status { return statusPendingTrust }
func NoOp() Status                  { return statusNoOp }

func (s Status) String() string { return s.name }
func (s Status) IsValid() bool {
	for _, c := range canonicalStatusList {
		if c.name == s.name {
			return true
		}
	}
	return false
}

// Operation is the intended action for a cell.
type Operation struct{ name string }

var (
	opEnsure  = Operation{name: "ensure"}
	opRemove  = Operation{name: "remove"}
	opInspect = Operation{name: "inspect"}
)

func Ensure() Operation   { return opEnsure }
func RemoveOp() Operation { return opRemove }
func Inspect() Operation  { return opInspect }

func (o Operation) String() string { return o.name }
func (o Operation) IsValid() bool  { return o == opEnsure || o == opRemove || o == opInspect }

// Management identifies whose authority, if any, the row represents.
type Management uint8

const (
	ManagementUnknown Management = iota
	ManagementPasture
	ManagementExternal
	ManagementDeclarative
)

func (m Management) String() string {
	switch m {
	case ManagementPasture:
		return "pasture_managed"
	case ManagementExternal:
		return "external"
	case ManagementDeclarative:
		return "managed_declaratively"
	default:
		return "unknown"
	}
}
func (m Management) IsValid() bool {
	return m >= ManagementUnknown && m <= ManagementDeclarative
}

// ActionRow is one ordered cell outcome.
type ActionRow struct {
	cell        cell.Cell
	operation   Operation
	status      Status
	management  Management
	observation registry.Observation
	diagnostic  string
}

func NewActionRow(c cell.Cell, operation Operation, status Status, management Management, observation registry.Observation, diagnostic string) ActionRow {
	return ActionRow{cell: c, operation: operation, status: status, management: management, observation: observation, diagnostic: diagnostic}
}
func (r ActionRow) Cell() cell.Cell                   { return r.cell }
func (r ActionRow) Operation() Operation              { return r.operation }
func (r ActionRow) Status() Status                    { return r.status }
func (r ActionRow) Management() Management            { return r.management }
func (r ActionRow) Observation() registry.Observation { return r.observation }
func (r ActionRow) Diagnostic() string                { return r.diagnostic }

// Result is the transient apply-result document.
type Result struct {
	source Source
	scope  registry.Scope
	rows   []ActionRow
	ok     bool
}

// Source returns the control source (installer or home-manager).
func (r Result) Source() Source        { return r.source }
func (r Result) Scope() registry.Scope { return r.scope }

// Rows returns the ordered action rows.
func (r Result) Rows() []ActionRow { return append([]ActionRow(nil), r.rows...) }

// OK reports whether every executed row succeeded (no failure occurred).
func (r Result) OK() bool { return r.ok }

func NewResult(source Source, scope registry.Scope, ok bool, rows []ActionRow) Result {
	return Result{source: source, scope: scope, ok: ok, rows: append([]ActionRow(nil), rows...)}
}

type rowWire struct {
	Index       int    `json:"index"`
	Cell        string `json:"cell"`
	Harness     string `json:"harness"`
	Extension   string `json:"extension"`
	Operation   string `json:"operation"`
	Status      string `json:"status"`
	Management  string `json:"management"`
	Observation string `json:"observation,omitempty"`
	Diagnostic  string `json:"diagnostic,omitempty"`
}

type resultWire struct {
	Schema string    `json:"schema"`
	Source string    `json:"source"`
	Scope  string    `json:"scope"`
	OK     bool      `json:"ok"`
	Cells  []rowWire `json:"cells"`
}

// MarshalJSON renders the frozen apply-result/v1 document with ordered rows.
func (r Result) MarshalJSON() ([]byte, error) {
	wire := resultWire{Schema: ResultSchemaID, Source: r.source.String(), Scope: r.scope.String(), OK: r.ok}
	for _, row := range r.rows {
		obs := ""
		if row.observation.IsValid() {
			obs = row.observation.String()
		}
		wire.Cells = append(wire.Cells, rowWire{
			Index:       row.cell.Index(),
			Cell:        row.cell.String(),
			Harness:     string(row.cell.Harness()),
			Extension:   row.cell.Extension().String(),
			Operation:   row.operation.String(),
			Status:      row.status.String(),
			Management:  row.management.String(),
			Observation: obs,
			Diagnostic:  row.diagnostic,
		})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(wire); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ApplyError is the transient pre-plan apply-error/v1 document.
type ApplyError struct {
	source      Source
	stage       string
	reason      string
	where       string
	impact      string
	fix         string
	remediation Remediation
}

func NewApplyError(source Source, stage, reason, where, impact, fix string, remediation Remediation) *ApplyError {
	return &ApplyError{source: source, stage: stage, reason: reason, where: where, impact: impact, fix: fix, remediation: remediation}
}

// Source returns the controller associated with the failed request.
func (e *ApplyError) Source() Source { return e.source }

// Stage returns the operation stage that rejected the request.
func (e *ApplyError) Stage() string { return e.stage }

// Reason returns the concrete reason the stage failed.
func (e *ApplyError) Reason() string { return e.reason }

// Location returns the production location that detected the failure.
func (e *ApplyError) Location() string { return e.where }

// Impact returns what the failure means for the caller and live state.
func (e *ApplyError) Impact() string { return e.impact }

// Fix returns the actionable repair instruction.
func (e *ApplyError) Fix() string { return e.fix }

// Remediation returns the closed caller action associated with the repair.
func (e *ApplyError) Remediation() Remediation { return e.remediation }

// Remediation is the closed caller action family carried by apply errors.
type Remediation uint8

const (
	RemediationInvalid Remediation = iota
	RemediationApplyCell
	RemediationApplySelection
	RemediationRerunInstaller
	RemediationRerunHomeManager
	RemediationManualNativeTrust
	RemediationManualRepair
)

func (r Remediation) String() string {
	switch r {
	case RemediationApplyCell:
		return "apply_cell"
	case RemediationApplySelection:
		return "apply_selection"
	case RemediationRerunInstaller:
		return "rerun_installer"
	case RemediationRerunHomeManager:
		return "rerun_home_manager"
	case RemediationManualNativeTrust:
		return "manual_native_trust"
	case RemediationManualRepair:
		return "manual_repair"
	default:
		return ""
	}
}

// Error implements the error interface.
func (e *ApplyError) Error() string {
	return "install apply: " + e.stage + " failed before mutation: " + e.reason + " (fix: " + e.fix + ")"
}

type applyErrorWire struct {
	Schema      string `json:"schema"`
	Source      string `json:"source"`
	Stage       string `json:"stage"`
	Reason      string `json:"reason"`
	Where       string `json:"where"`
	Impact      string `json:"impact"`
	Fix         string `json:"fix"`
	Remediation string `json:"remediation,omitempty"`
}

// MarshalJSON renders the frozen apply-error/v1 document.
func (e *ApplyError) MarshalJSON() ([]byte, error) {
	wire := applyErrorWire{
		Schema:      ErrorSchemaID,
		Source:      e.source.String(),
		Stage:       e.stage,
		Reason:      e.reason,
		Where:       e.where,
		Impact:      e.impact,
		Fix:         e.fix,
		Remediation: e.remediation.String(),
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(wire); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
