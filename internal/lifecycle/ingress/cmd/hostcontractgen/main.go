package main

import (
	"fmt"
	"os"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
)

// hostcontractgen validates the checked-in generated Claude manifest. Go's
// internal import rule permits this command, under ingress, to read host
// behavior while forbidding target-neutral registration and activation
// consumers from doing so.
func main() {
	contract := hostcontract.ClaudeCode2_1_210()
	if contract.Version == "" || len(contract.Fields) != 41 || len(contract.Events) != 30 {
		fmt.Fprintln(os.Stderr, "hostcontractgen: Claude contract is incomplete; expected one version, 41 fields, and 30 events")
		os.Exit(1)
	}
	for index, event := range contract.Events {
		if event.Kind == 0 || event.Name == "" {
			fmt.Fprintf(os.Stderr, "hostcontractgen: event ordinal %d is incomplete; assign a typed kind and native name\n", index+1)
			os.Exit(1)
		}
	}
}
