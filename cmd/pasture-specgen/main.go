// Command pasture-specgen writes the canonical OpenAPI document for the local
// session API. It is deliberately network-free and deterministic.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dayvidpham/pasture/internal/sessionapi"
)

func main() {
	out := flag.String("out", "", "write the document to this path instead of stdout")
	flag.Parse()
	data := sessionapi.OpenAPIBytes()
	if *out == "" {
		if _, err := os.Stdout.Write(data); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
