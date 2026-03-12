// Package codegen generates protocol documentation from canonical Go type definitions.
//
// This package ports the Python codegen tooling (gen_skills.py, gen_schema.py,
// gen_agents.py, context_injection.py) to Go, using text/template for Markdown
// generation and encoding/xml for schema.xml.
//
// Generated outputs:
//   - schema.xml: Protocol schema definition
//   - skills/{role}/SKILL.md: Role-level skill headers (marker-bounded)
//   - agents/{role}.md: Agent definition files (fully generated)
//
//go:generate go run ../../tools/codegen
package codegen
