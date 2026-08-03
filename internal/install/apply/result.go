// Package apply is the source-agnostic activation engine. It builds an in-memory
// plan from a normalized effective selection plus the confirmed inventory,
// executes cells in the frozen canonical order, stops at the first failure
// (marking later executable rows unattempted), records the strongest confirmed
// observation, and returns a transient apply-result document. It never reads or
// writes preference YAML and never persists its result document.
package apply

import (
	"bytes"
	"encoding/json"

	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/inventory"
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

// ActionRow is one ordered cell outcome.
type ActionRow struct {
	cell        cell.Cell
	operation   Operation
	status      Status
	observation inventory.Observation
	diagnostic  string
}

func (r ActionRow) Cell() cell.Cell                    { return r.cell }
func (r ActionRow) Operation() Operation               { return r.operation }
func (r ActionRow) Status() Status                     { return r.status }
func (r ActionRow) Observation() inventory.Observation { return r.observation }
func (r ActionRow) Diagnostic() string                 { return r.diagnostic }

// Result is the transient apply-result document.
type Result struct {
	source string
	rows   []ActionRow
	ok     bool
}

// Source returns the control source (installer or home-manager).
func (r Result) Source() string { return r.source }

// Rows returns the ordered action rows.
func (r Result) Rows() []ActionRow { return append([]ActionRow(nil), r.rows...) }

// OK reports whether every executed row succeeded (no failure occurred).
func (r Result) OK() bool { return r.ok }

type rowWire struct {
	Cell        string `json:"cell"`
	Operation   string `json:"operation"`
	Status      string `json:"status"`
	Observation string `json:"observation,omitempty"`
	Diagnostic  string `json:"diagnostic,omitempty"`
}

type resultWire struct {
	Schema string    `json:"schema"`
	Source string    `json:"source"`
	OK     bool      `json:"ok"`
	Cells  []rowWire `json:"cells"`
}

// MarshalJSON renders the frozen apply-result/v1 document with ordered rows.
func (r Result) MarshalJSON() ([]byte, error) {
	wire := resultWire{Schema: ResultSchemaID, Source: r.source, OK: r.ok}
	for _, row := range r.rows {
		obs := ""
		if row.observation.IsValid() {
			obs = row.observation.String()
		}
		wire.Cells = append(wire.Cells, rowWire{
			Cell:        row.cell.String(),
			Operation:   row.operation.String(),
			Status:      row.status.String(),
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
	source      string
	stage       string
	reason      string
	where       string
	impact      string
	fix         string
	remediation string
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
		Source:      e.source,
		Stage:       e.stage,
		Reason:      e.reason,
		Where:       e.where,
		Impact:      e.impact,
		Fix:         e.fix,
		Remediation: e.remediation,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(wire); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
