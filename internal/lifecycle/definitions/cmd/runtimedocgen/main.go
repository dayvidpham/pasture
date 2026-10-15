package main

import (
	"fmt"
	"os"

	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
)

func main() {
	if err := os.MkdirAll("generated", 0755); err != nil {
		panic(err)
	}
	for index, contract := range definitions.RuntimeContracts() {
		body, err := definitions.GenerateRuntimeBody(contract)
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(fmt.Sprintf("generated/runtime-%d.json", index), body, 0644); err != nil {
			panic(err)
		}
	}
}
