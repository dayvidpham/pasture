package types_test

import (
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/types"
)

func TestReviewCycleRecord_IsCleanExit(t *testing.T) {
	tests := []struct {
		name     string
		findings map[string]int
		want     bool
	}{
		{
			name:     "clean: all zeros",
			findings: map[string]int{"blocker": 0, "important": 0, "minor": 0},
			want:     true,
		},
		{
			name:     "clean: minors only",
			findings: map[string]int{"blocker": 0, "important": 0, "minor": 5},
			want:     true,
		},
		{
			name:     "dirty: has blocker",
			findings: map[string]int{"blocker": 1, "important": 0, "minor": 0},
			want:     false,
		},
		{
			name:     "dirty: has important",
			findings: map[string]int{"blocker": 0, "important": 2, "minor": 0},
			want:     false,
		},
		{
			name:     "dirty: has both",
			findings: map[string]int{"blocker": 1, "important": 3, "minor": 7},
			want:     false,
		},
		{
			name:     "clean: empty map (no findings recorded)",
			findings: map[string]int{},
			want:     true,
		},
		{
			name:     "clean: nil map",
			findings: nil,
			want:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := types.ReviewCycleRecord{
				SliceID:       "slice-1",
				Round:         1,
				FindingCounts: tc.findings,
				Timestamp:     time.Now(),
			}
			if got := r.IsCleanExit(); got != tc.want {
				t.Errorf("IsCleanExit() = %v, want %v (findings: %v)", got, tc.want, tc.findings)
			}
		})
	}
}

func TestReviewCycleRecord_Fields(t *testing.T) {
	now := time.Now()
	r := types.ReviewCycleRecord{
		SliceID: "aura-plugins-abc123",
		Round:   2,
		Votes: map[types.ReviewAxis]types.VoteType{
			types.AxisCorrectness: types.VoteAccept,
			types.AxisTestQuality: types.VoteAccept,
			types.AxisElegance:    types.VoteRevise,
		},
		FindingCounts: map[string]int{"blocker": 0, "important": 1, "minor": 3},
		Clean:         false,
		Timestamp:     now,
	}

	if r.SliceID != "aura-plugins-abc123" {
		t.Errorf("SliceID = %q, want %q", r.SliceID, "aura-plugins-abc123")
	}
	if r.Round != 2 {
		t.Errorf("Round = %d, want 2", r.Round)
	}
	if len(r.Votes) != 3 {
		t.Errorf("Votes count = %d, want 3", len(r.Votes))
	}
	if r.Votes[types.AxisElegance] != types.VoteRevise {
		t.Errorf("Votes[Elegance] = %v, want VoteRevise", r.Votes[types.AxisElegance])
	}
	if r.Clean {
		t.Error("Clean should be false (has 1 IMPORTANT)")
	}
	if r.IsCleanExit() {
		t.Error("IsCleanExit() should be false (has 1 IMPORTANT)")
	}
}

func TestEpochState_ReviewCycles(t *testing.T) {
	state := types.EpochState{
		EpochID: "test-epoch",
		ReviewCycles: map[string][]types.ReviewCycleRecord{
			"slice-1": {
				{SliceID: "slice-1", Round: 1, FindingCounts: map[string]int{"blocker": 1}, Clean: false},
				{SliceID: "slice-1", Round: 2, FindingCounts: map[string]int{"blocker": 0, "important": 0}, Clean: true},
			},
			"slice-2": {
				{SliceID: "slice-2", Round: 1, FindingCounts: map[string]int{"blocker": 0, "important": 0}, Clean: true},
			},
		},
	}

	if len(state.ReviewCycles) != 2 {
		t.Errorf("ReviewCycles count = %d, want 2 slices", len(state.ReviewCycles))
	}
	if len(state.ReviewCycles["slice-1"]) != 2 {
		t.Errorf("slice-1 rounds = %d, want 2", len(state.ReviewCycles["slice-1"]))
	}
	if state.ReviewCycles["slice-1"][0].IsCleanExit() {
		t.Error("slice-1 round 1 should not be clean (has blocker)")
	}
	if !state.ReviewCycles["slice-1"][1].IsCleanExit() {
		t.Error("slice-1 round 2 should be clean")
	}
}
