package scan

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed manifest/operational_history.json
var operationalHistoryJSON []byte

// OperationalHistory is the exact reviewed quotation inventory. Native-call
// dispositions cannot exempt tracker commands. The canonical count is pinned
// separately, and every consumer receives its own copy.
func OperationalHistory() ([]HistoricalTrackerEntry, error) {
	var entries []HistoricalTrackerEntry
	decoder := json.NewDecoder(bytes.NewReader(operationalHistoryJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("operational history inventory cannot be decoded: %w; stage outputs withheld; repair internal/codegen/scan/manifest/operational_history.json and regenerate", err)
	}
	if len(entries) != 13 {
		return nil, fmt.Errorf("operational history inventory has %d entries, expected exactly 13 reviewed quotations; stage outputs withheld; review every changed owner/section/range/window and regenerate", len(entries))
	}
	seen := map[HistoricalTrackerEntry]bool{}
	for _, entry := range entries {
		if entry.Owner == "" || entry.Section == "" || entry.Node == "" || entry.Range.Start < 0 || entry.Range.Stop <= entry.Range.Start || entry.Window == "" || len(entry.Digest) != 64 || seen[entry] {
			return nil, fmt.Errorf("operational history inventory has incomplete or duplicate identity for %s; stage outputs withheld; repair the exact owner/section/node/range/window/digest entry and regenerate", entry.Owner)
		}
		seen[entry] = true
	}
	return entries, nil
}
