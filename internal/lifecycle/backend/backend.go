// Package backend maps a legalized consultation into canonical receipt
// evidence and the typed response expected by the native host.
package backend

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/legalize"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
)

// Decision is the closed set of host decisions produced by the backend.
type Decision uint8

const (
	// DecisionProceed lets the native host continue after consultation.
	DecisionProceed Decision = iota + 1
)

// IsValid reports whether the decision is supported by this backend.
func (d Decision) IsValid() bool { return d == DecisionProceed }

// String returns the canonical decision spelling.
func (d Decision) String() string {
	if d == DecisionProceed {
		return "proceed"
	}
	return ""
}

// HostResponse is one constructor-validated response to the native host.
type HostResponse struct {
	decision    Decision
	constructed bool
}

func newHostResponse(decision Decision) HostResponse {
	return HostResponse{decision: decision, constructed: decision.IsValid()}
}

// Decision returns the response decision, or the invalid zero decision when
// the response was not constructed by this package.
func (r HostResponse) Decision() Decision {
	if !r.IsValid() {
		return 0
	}
	return r.decision
}

// IsValid reports whether the response was constructed with a valid decision.
func (r HostResponse) IsValid() bool { return r.constructed && r.decision.IsValid() }

// MarshalJSON returns the canonical compact host response.
func (r HostResponse) MarshalJSON() ([]byte, error) {
	if !r.IsValid() {
		return nil, fmt.Errorf("marshal lifecycle host response: the HostResponse value was not constructed by backend.BuildConsultation; no response was emitted; use the response returned by backend.BuildConsultation")
	}
	return []byte(`{"decision":"proceed"}`), nil
}

// ConsultationResponse implements waist.ConsultationResponse.
func (HostResponse) ConsultationResponse() {}

// BuildConsultation is the sole production entry point for backend mapping.
func BuildConsultation(interpreted receipt.Record, legalized legalize.Legalized) (receipt.ConsultationRecord, HostResponse, error) {
	return receipt.ConsultationRecord{}, HostResponse{}, fmt.Errorf("build lifecycle consultation: implementation is not available during the contract layer; no record or host response was produced; complete backend consultation mapping before calling BuildConsultation (interpreted valid: %t, legalized valid: %t)", interpreted.IsValid(), legalized.IsValid())
}

var _ waist.ConsultationResponse = HostResponse{}
