// Command pasture-msg is the Pasture CLI — sends control messages to the
// pastured daemon via Temporal signals and queries.
package main

import (
	"fmt"
	"os"
)

const version = "v0.1.0"

func main() {
	fmt.Printf("pasture-msg %s\n", version)
	os.Exit(0)
}
