// Package codegen generates protocol documentation from canonical Go type definitions.
//
// Typed Go specifications in this package are the source of truth. The
// generators use text/template for Markdown and encoding/xml/manual builders
// for schema.xml.
//
// Generated outputs:
//   - schema.xml: Protocol schema definition
//   - skills/{skill}/SKILL.md: Claude Code skills
//   - agents/{role}.md: Claude Code agent definitions
//   - .opencode/skill/{skill}/SKILL.md: OpenCode skills
//   - .opencode/agent/{role}[--{variant}].md: OpenCode agent definitions
//   - opencode.json: OpenCode manifest
//
//go:generate go run ../../tools/codegen --targets claude-code,opencode
package codegen
