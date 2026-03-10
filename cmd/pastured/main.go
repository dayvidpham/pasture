// Command pastured is the Pasture daemon — a Temporal worker that runs
// aura-protocol workflows and activities for multi-agent orchestration.
package main

import (
	"fmt"
	"os"
)

const version = "v0.1.0"

func main() {
	fmt.Printf("pastured %s\n", version)
	os.Exit(0)
}
