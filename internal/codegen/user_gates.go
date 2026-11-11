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
// Each purpose carries its workflow ordinal (p1..p12): two protocol constants name
// neighbouring phases, so PhaseReview is the plan review (p4) and PhasePlanReview
// is the plan acceptance test (p5).
var userGates = []userGate{
	{protocol.PhaseRequest, "p1 research depth"},
	{protocol.PhaseElicit, "p2 requirements interview"},
	{protocol.PhasePlanReview, "p5 plan UAT"},
	{protocol.PhaseImplPlan, "p8 implementation effort and review budget"},
	{protocol.PhaseImplUAT, "p11 implementation UAT"},
}

func renderUserGates() string {
	var entries []string
	for _, gate := range userGates {
		entries = append(entries, string(gate.phase)+" ("+gate.purpose+")")
	}
	return "User gates: [" + strings.Join(entries, "; ") + "]"
}
