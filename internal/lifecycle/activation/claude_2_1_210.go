package activation

import "github.com/dayvidpham/pasture/internal/lifecycle/registration"

// ClaudeCode2_1_210 visibly withholds every event until its authentic fixture
// and production-path proof are both present.
func ClaudeCode2_1_210() []Entry {
	events := registration.ClaudeCode2_1_210().Entries()
	out := make([]Entry, 0, len(events))
	for _, event := range events {
		if event.NativeName == "SessionStart" {
			entry, _ := NewWithheld(event.Kind, WithheldProductionProofMissing)
			out = append(out, entry)
			continue
		}
		entry, _ := NewWithheld(event.Kind, WithheldMissingFixture)
		out = append(out, entry)
	}
	return out
}
