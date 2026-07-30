package activation

import "github.com/dayvidpham/pasture/internal/lifecycle/registration"

// ClaudeCode2_1_210 visibly withholds every event until its authentic fixture
// and production-path proof are both present.
func ClaudeCode2_1_210() ([]Entry, error) {
	events := registration.ClaudeCode2_1_210().Entries()
	out := make([]Entry, 0, len(events))
	for _, event := range events {
		if event.NativeName == "SessionStart" {
			entry, err := NewEnabled(event.Kind, FixtureEvidenceAuthentic, ProductionProofPassing)
			if err != nil {
				return nil, err
			}
			out = append(out, entry)
			continue
		}
		entry, err := NewWithheld(event.Kind, WithheldMissingFixture)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}
