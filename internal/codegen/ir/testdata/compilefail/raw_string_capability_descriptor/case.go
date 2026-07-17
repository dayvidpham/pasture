// Package compilefail is an isolated compile-fail fixture: a raw untyped
// string literal must not satisfy the opaque Capability[In, Out] boundary
// InvokeTool requires. Capability is a struct descriptor, not a string-based
// identity — unlike CapabilityID itself, no string value (typed or literal)
// can ever stand in for it, with or without the capabilitylint rule.
package compilefail

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func invalidUse() {
	ir.InvokeTool[input, output]("acme.diagram.render", input{Name: "example"})
}
