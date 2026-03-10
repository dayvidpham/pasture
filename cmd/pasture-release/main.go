// Command pasture-release manages versioning and release coordination across
// the Pasture polyrepo (Nix, GitHub Releases, go install, skill channels).
package main

import (
	"fmt"
	"os"
)

const version = "v0.1.0"

func main() {
	fmt.Printf("pasture-release %s\n", version)
	os.Exit(0)
}
