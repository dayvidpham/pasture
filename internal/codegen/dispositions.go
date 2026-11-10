package codegen

import "strings"

// disposition is the closed vocabulary for user-confirmed feedback decisions.
type disposition string

const (
	fixNow    disposition = "FIX-NOW"
	deferItem disposition = "DEFER"
	wontDo    disposition = "WON'T-DO"
)

type dispositionEntry struct {
	value                disposition
	meaning, destination string
}

var dispositions = []dispositionEntry{
	{fixNow, "Resolve before this wave closes", "Back to the architect (Plan UAT) or the relevant slice (Impl UAT)"},
	{deferItem, "Acceptable to ship now; track for later", "The **sole** source feeding the FOLLOWUP epic"},
	{wontDo, "The user declined the item; record the user's reason", "Does not feed the FOLLOWUP epic and is not re-raised at every gate"},
}

func (d disposition) valid() bool {
	for _, entry := range dispositions {
		if d == entry.value {
			return true
		}
	}
	return false
}
func dispositionNames() string {
	names := make([]string, 0, len(dispositions))
	for _, entry := range dispositions {
		names = append(names, string(entry.value))
	}
	return strings.Join(names, ", ")
}
func dispositionTable() string {
	var b strings.Builder
	for _, entry := range dispositions {
		b.WriteString("| **" + string(entry.value) + "** | " + entry.meaning + " | " + entry.destination + " |\n")
	}
	return b.String()
}
