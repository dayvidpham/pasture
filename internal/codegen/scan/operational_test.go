package scan_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestOperationalTrackerFixtures(t *testing.T) {
	t.Parallel()
	var fixtures []struct {
		Name     string `yaml:"name"`
		File     string `yaml:"file"`
		Content  string `yaml:"content"`
		Reject   bool   `yaml:"reject"`
		History  bool   `yaml:"history"`
		Mutation string `yaml:"mutation"`
	}
	testutil.LoadFixtures(t, testutil.OperationalTracker, &fixtures)
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()
			doc := scan.OperationalDocument{Owner: fixture.File, File: fixture.File, Content: []byte(fixture.Content)}
			var history []scan.HistoricalTrackerEntry
			if fixture.History {
				candidates, err := scan.TrackerCandidates(doc)
				require.NoError(t, err)
				require.Len(t, candidates, 1)
				candidate := candidates[0]
				history = []scan.HistoricalTrackerEntry{{Owner: doc.Owner, Section: candidate.Location.Section(), Node: candidate.Node,
					Range: candidate.Location.Range(), Window: candidate.Window, Digest: scan.TrackerDigest(doc.Content)}}
				switch fixture.Mutation {
				case "owner":
					history[0].Owner = "other.md"
				case "section":
					history[0].Section = "Other section"
				case "node":
					history[0].Node = "Paragraph"
				case "range":
					history[0].Range.Start++
				case "window":
					history[0].Window += " altered"
				case "digest":
					history[0].Digest = "stale"
				case "framing":
					doc.Content = []byte("# Work\n\nRun `bd sync`.\n")
				case "content":
					doc.Content = append(doc.Content, []byte("Run `bd ready`.\n")...)
				case "missing":
					doc.Content = []byte("# Historical — non-operational quotation\nNothing to run.\n")
				}
			}
			err := scan.ValidateOperationalDocuments([]scan.OperationalDocument{doc}, history, "fixture validation")
			if fixture.Reject {
				require.Error(t, err)
				for _, text := range []string{fixture.File, "section", "range", "why", "fixture validation", "withheld", "migrate", "regenerate"} {
					require.Contains(t, err.Error(), text)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestOperationalHistoryPinsExactCanonicalQuotations(t *testing.T) {
	t.Parallel()
	history, err := scan.OperationalHistory()
	require.NoError(t, err)
	require.Len(t, history, 13)
	counts := map[string]int{}
	for _, entry := range history {
		counts[entry.Owner]++
	}
	require.Equal(t, map[string]int{
		"skills/protocol/UAT_EXAMPLE.md":                                    5,
		"skills/protocol/RESEARCH_EXAMPLE-nix-openclaw-req-ure-proposal.md": 3,
		"skills/protocol/user-request-revamp-v2.md":                         2,
		"skills/protocol/MIGRATION_v1_to_v2.md":                             3,
	}, counts)
}
