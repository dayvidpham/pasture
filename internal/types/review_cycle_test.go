package types_test

import (
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/types"
)

// allAcceptVotes returns a Votes map where all 3 axes voted ACCEPT.
func allAcceptVotes() map[types.ReviewAxis]types.VoteType {
	return map[types.ReviewAxis]types.VoteType{
		types.AxisCorrectness: types.VoteAccept,
		types.AxisTestQuality: types.VoteAccept,
		types.AxisElegance:    types.VoteAccept,
	}
}

func TestIsCleanExit_FindingCounts(t *testing.T) {
	tests := []struct {
		name     string
		findings map[types.SeverityLevel]int
		want     bool
	}{
		{
			name:     "clean: all zeros",
			findings: map[types.SeverityLevel]int{types.SeverityBlocker: 0, types.SeverityImportant: 0, types.SeverityMinor: 0},
			want:     true,
		},
		{
			name:     "clean: minors only",
			findings: map[types.SeverityLevel]int{types.SeverityBlocker: 0, types.SeverityImportant: 0, types.SeverityMinor: 5},
			want:     true,
		},
		{
			name:     "dirty: has blocker",
			findings: map[types.SeverityLevel]int{types.SeverityBlocker: 1, types.SeverityImportant: 0, types.SeverityMinor: 0},
			want:     false,
		},
		{
			name:     "dirty: has important",
			findings: map[types.SeverityLevel]int{types.SeverityBlocker: 0, types.SeverityImportant: 2, types.SeverityMinor: 0},
			want:     false,
		},
		{
			name:     "dirty: has both",
			findings: map[types.SeverityLevel]int{types.SeverityBlocker: 1, types.SeverityImportant: 3, types.SeverityMinor: 7},
			want:     false,
		},
		{
			name:     "clean: empty map",
			findings: map[types.SeverityLevel]int{},
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
				SliceId:       "slice-1",
				Round:         1,
				Votes:         allAcceptVotes(), // all ACCEPT — isolate finding counts
				FindingCounts: tc.findings,
				Timestamp:     time.Now(),
			}
			if got := r.IsCleanExit(); got != tc.want {
				t.Errorf("IsCleanExit() = %v, want %v (findings: %v)", got, tc.want, tc.findings)
			}
		})
	}
}

func TestIsCleanExit_VoteConsensus(t *testing.T) {
	cleanFindings := map[types.SeverityLevel]int{
		types.SeverityBlocker:   0,
		types.SeverityImportant: 0,
		types.SeverityMinor:     0,
	}

	tests := []struct {
		name  string
		votes map[types.ReviewAxis]types.VoteType
		want  bool
	}{
		{
			name:  "clean: all ACCEPT",
			votes: allAcceptVotes(),
			want:  true,
		},
		{
			name: "dirty: one REVISE",
			votes: map[types.ReviewAxis]types.VoteType{
				types.AxisCorrectness: types.VoteAccept,
				types.AxisTestQuality: types.VoteRevise,
				types.AxisElegance:    types.VoteAccept,
			},
			want: false,
		},
		{
			name: "dirty: all REVISE",
			votes: map[types.ReviewAxis]types.VoteType{
				types.AxisCorrectness: types.VoteRevise,
				types.AxisTestQuality: types.VoteRevise,
				types.AxisElegance:    types.VoteRevise,
			},
			want: false,
		},
		{
			name:  "dirty: missing vote (nil map)",
			votes: nil,
			want:  false,
		},
		{
			name:  "dirty: empty votes map",
			votes: map[types.ReviewAxis]types.VoteType{},
			want:  false,
		},
		{
			name: "dirty: only 2 of 3 axes voted ACCEPT",
			votes: map[types.ReviewAxis]types.VoteType{
				types.AxisCorrectness: types.VoteAccept,
				types.AxisTestQuality: types.VoteAccept,
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := types.ReviewCycleRecord{
				SliceId:       "slice-1",
				Round:         1,
				Votes:         tc.votes,
				FindingCounts: cleanFindings, // 0 findings — isolate vote check
				Timestamp:     time.Now(),
			}
			if got := r.IsCleanExit(); got != tc.want {
				t.Errorf("IsCleanExit() = %v, want %v (votes: %v)", got, tc.want, tc.votes)
			}
		})
	}
}

func TestReviewCycleRecord_Fields(t *testing.T) {
	now := time.Now()
	r := types.ReviewCycleRecord{
		SliceId: "aura-plugins-abc123",
		Round:   2,
		Votes: map[types.ReviewAxis]types.VoteType{
			types.AxisCorrectness: types.VoteAccept,
			types.AxisTestQuality: types.VoteAccept,
			types.AxisElegance:    types.VoteRevise,
		},
		FindingCounts: map[types.SeverityLevel]int{
			types.SeverityBlocker:   0,
			types.SeverityImportant: 1,
			types.SeverityMinor:     3,
		},
		Timestamp: now,
	}

	if r.SliceId != "aura-plugins-abc123" {
		t.Errorf("SliceId = %q, want %q", r.SliceId, "aura-plugins-abc123")
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
	if r.Timestamp != now {
		t.Errorf("Timestamp = %v, want %v", r.Timestamp, now)
	}
	// Not clean: has 1 IMPORTANT + 1 REVISE vote
	if r.IsCleanExit() {
		t.Error("IsCleanExit() should be false (has IMPORTANT + REVISE vote)")
	}
}

func TestEpochState_ReviewCycles(t *testing.T) {
	state := types.EpochState{
		EpochId: "test-epoch",
		ReviewCycles: map[string][]types.ReviewCycleRecord{
			"slice-1": {
				{
					SliceId: "slice-1", Round: 1,
					Votes:         allAcceptVotes(),
					FindingCounts: map[types.SeverityLevel]int{types.SeverityBlocker: 1},
				},
				{
					SliceId: "slice-1", Round: 2,
					Votes:         allAcceptVotes(),
					FindingCounts: map[types.SeverityLevel]int{types.SeverityBlocker: 0, types.SeverityImportant: 0},
				},
			},
			"slice-2": {
				{
					SliceId: "slice-2", Round: 1,
					Votes:         allAcceptVotes(),
					FindingCounts: map[types.SeverityLevel]int{types.SeverityBlocker: 0, types.SeverityImportant: 0},
				},
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

func TestSeverityLevel_IsValid(t *testing.T) {
	valid := []types.SeverityLevel{types.SeverityBlocker, types.SeverityImportant, types.SeverityMinor}
	for _, s := range valid {
		if !s.IsValid() {
			t.Errorf("%q.IsValid() = false, want true", s)
		}
	}
	invalid := types.SeverityLevel("critical")
	if invalid.IsValid() {
		t.Errorf("%q.IsValid() = true, want false", invalid)
	}
}
