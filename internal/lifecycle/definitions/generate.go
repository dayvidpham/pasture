package definitions

import (
	"embed"
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

//go:generate go run ./cmd/runtimedocgen

//go:embed generated/*.json
var runtimeBodies embed.FS

func generatedRuntimeBody(contract ir.RuntimeContractID) ([]byte, error) {
	for index, registered := range RuntimeContracts() {
		if registered == contract {
			body, err := runtimeBodies.ReadFile(fmt.Sprintf("generated/runtime-%d.json", index))
			if err != nil {
				return nil, failure("generatedRuntimeBody", "The compiled runtime document is missing; regenerate the artifact.", err)
			}
			return body, nil
		}
	}
	return nil, failure("generatedRuntimeBody", fmt.Sprintf("Contract %q has no compiled runtime document.", contract), nil)
}
