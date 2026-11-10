package codegen

import (
	"strings"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

type userGate struct {
	phase   protocol.PhaseId
	purpose string
}

// userGates is the sole ordered declaration of user decisions that gate progress.
var userGates = []userGate{
	{protocol.PhaseRequest, "research depth"},
	{protocol.PhaseElicit, "requirements interview"},
	{protocol.PhasePlanReview, "plan UAT"},
	{protocol.PhaseImplPlan, "implementation effort and review budget"},
	{protocol.PhaseImplUAT, "implementation UAT"},
}

func renderUserGates() string {
	var entries []string
	for _, gate := range userGates {
		entries = append(entries, string(gate.phase)+" ("+gate.purpose+")")
	}
	return "User gates: [" + strings.Join(entries, "; ") + "]"
}
