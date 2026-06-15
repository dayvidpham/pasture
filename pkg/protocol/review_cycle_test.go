package protocol_test

import (
	"testing"
	"time"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// allAcceptVotes returns a Votes map where all 3 axes voted ACCEPT.
func allAcceptVotes() map[protocol.ReviewAxis]protocol.VoteType {
	return map[protocol.ReviewAxis]protocol.VoteType{
		protocol.AxisCorrectness: protocol.VoteAccept,
		protocol.AxisTestQuality: protocol.VoteAccept,
		protocol.AxisElegance:    protocol.VoteAccept,
	}
}

func TestIsCleanExit_FindingCounts(t *testing.T) {
	tests := []struct {
		name     string
		findings map[protocol.SeverityLevel]int
		want     bool
	}{
		{
			name:     "clean: all zeros",
			findings: map[protocol.SeverityLevel]int{protocol.SeverityBlocker: 0, protocol.SeverityImportant: 0, protocol.SeverityMinor: 0},
			want:     true,
		},
		{
			name:     "clean: minors only",
			findings: map[protocol.SeverityLevel]int{protocol.SeverityBlocker: 0, protocol.SeverityImportant: 0, protocol.SeverityMinor: 5},
			want:     true,
		},
		{
			name:     "dirty: has blocker",
			findings: map[protocol.SeverityLevel]int{protocol.SeverityBlocker: 1, protocol.SeverityImportant: 0, protocol.SeverityMinor: 0},
			want:     false,
		},
		{
			name:     "dirty: has important",
			findings: map[protocol.SeverityLevel]int{protocol.SeverityBlocker: 0, protocol.SeverityImportant: 2, protocol.SeverityMinor: 0},
			want:     false,
		},
		{
			name:     "dirty: has both",
			findings: map[protocol.SeverityLevel]int{protocol.SeverityBlocker: 1, protocol.SeverityImportant: 3, protocol.SeverityMinor: 7},
			want:     false,
		},
		{
			name:     "clean: empty map",
			findings: map[protocol.SeverityLevel]int{},
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
			r := protocol.ReviewCycleRecord{
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
	cleanFindings := map[protocol.SeverityLevel]int{
		protocol.SeverityBlocker:   0,
		protocol.SeverityImportant: 0,
		protocol.SeverityMinor:     0,
	}

	tests := []struct {
		name  string
		votes map[protocol.ReviewAxis]protocol.VoteType
		want  bool
	}{
		{
			name:  "clean: all ACCEPT",
			votes: allAcceptVotes(),
			want:  true,
		},
		{
			name: "dirty: one REVISE",
			votes: map[protocol.ReviewAxis]protocol.VoteType{
				protocol.AxisCorrectness: protocol.VoteAccept,
				protocol.AxisTestQuality: protocol.VoteRevise,
				protocol.AxisElegance:    protocol.VoteAccept,
			},
			want: false,
		},
		{
			name: "dirty: all REVISE",
			votes: map[protocol.ReviewAxis]protocol.VoteType{
				protocol.AxisCorrectness: protocol.VoteRevise,
				protocol.AxisTestQuality: protocol.VoteRevise,
				protocol.AxisElegance:    protocol.VoteRevise,
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
			votes: map[protocol.ReviewAxis]protocol.VoteType{},
			want:  false,
		},
		{
			name: "dirty: only 2 of 3 axes voted ACCEPT",
			votes: map[protocol.ReviewAxis]protocol.VoteType{
				protocol.AxisCorrectness: protocol.VoteAccept,
				protocol.AxisTestQuality: protocol.VoteAccept,
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := protocol.ReviewCycleRecord{
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
	r := protocol.ReviewCycleRecord{
		SliceId: "aura-plugins-abc123",
		Round:   2,
		Votes: map[protocol.ReviewAxis]protocol.VoteType{
			protocol.AxisCorrectness: protocol.VoteAccept,
			protocol.AxisTestQuality: protocol.VoteAccept,
			protocol.AxisElegance:    protocol.VoteRevise,
		},
		FindingCounts: map[protocol.SeverityLevel]int{
			protocol.SeverityBlocker:   0,
			protocol.SeverityImportant: 1,
			protocol.SeverityMinor:     3,
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
	if r.Votes[protocol.AxisElegance] != protocol.VoteRevise {
		t.Errorf("Votes[Elegance] = %v, want VoteRevise", r.Votes[protocol.AxisElegance])
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
	state := protocol.EpochState{
		EpochId: "test-epoch",
		ReviewCycles: map[string][]protocol.ReviewCycleRecord{
			"slice-1": {
				{
					SliceId: "slice-1", Round: 1,
					Votes:         allAcceptVotes(),
					FindingCounts: map[protocol.SeverityLevel]int{protocol.SeverityBlocker: 1},
				},
				{
					SliceId: "slice-1", Round: 2,
					Votes:         allAcceptVotes(),
					FindingCounts: map[protocol.SeverityLevel]int{protocol.SeverityBlocker: 0, protocol.SeverityImportant: 0},
				},
			},
			"slice-2": {
				{
					SliceId: "slice-2", Round: 1,
					Votes:         allAcceptVotes(),
					FindingCounts: map[protocol.SeverityLevel]int{protocol.SeverityBlocker: 0, protocol.SeverityImportant: 0},
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
