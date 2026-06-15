package engine

import (
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// TestProvenancePhaseMapping_Locked pins EVERY pasture-phase → provenance-phase
// pair. The two enums are independent types describing the same lifecycle; this
// lock catches any future reordering or rename on either side (which would
// otherwise silently mis-attribute activities) by failing loudly here.
func TestProvenancePhaseMapping_Locked(t *testing.T) {
	t.Parallel()
	want := map[protocol.PhaseId]provenance.Phase{
		protocol.PhaseRequest:      provenance.PhaseRequest,
		protocol.PhaseElicit:       provenance.PhaseElicit,
		protocol.PhasePropose:      provenance.PhasePropose,
		protocol.PhaseReview:       provenance.PhaseReview,
		protocol.PhasePlanReview:   provenance.PhasePlanUAT,
		protocol.PhaseRatify:       provenance.PhaseRatify,
		protocol.PhaseHandoff:      provenance.PhaseHandoff,
		protocol.PhaseImplPlan:     provenance.PhaseImplPlan,
		protocol.PhaseWorkerSlices: provenance.PhaseWorkerSlices,
		protocol.PhaseCodeReview:   provenance.PhaseCodeReview,
		protocol.PhaseImplUAT:      provenance.PhaseImplUAT,
		protocol.PhaseLanding:      provenance.PhaseLanding,
		protocol.PhaseComplete:     provenance.PhaseUnscoped,
	}

	for pid, exp := range want {
		if got := provenancePhase(pid); got != exp {
			t.Errorf("provenancePhase(%q) = %v (%q), want %v (%q)",
				pid, got, got.String(), exp, exp.String())
		}
	}

	// Completeness: every known pasture phase (pipeline + terminal) must be in
	// the lock map, so a newly-added phase can't slip through unmapped.
	for _, pid := range protocol.AllPhaseIds {
		if _, ok := want[pid]; !ok {
			t.Errorf("phase %q is not covered by the locked phase map", pid)
		}
	}

	// The two enums must also agree on cardinality up to the terminal phase:
	// pasture has 12 pipeline phases + complete; provenance's named phases run
	// PhaseRequest..PhaseLanding (12) + PhaseUnscoped. Guard the upper bound so a
	// provenance enum addition before PhaseUnscoped is noticed.
	if provenance.PhaseLanding != provenance.Phase(11) {
		t.Errorf("provenance.PhaseLanding = %d, want 11 — provenance phase enum reordered", provenance.PhaseLanding)
	}
}
