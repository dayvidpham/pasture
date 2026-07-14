// Package codegen generates protocol documentation from canonical Go type definitions.
//
// This package ports the Python codegen tooling (gen_skills.py, gen_schema.py,
// gen_agents.py, context_injection.py) to Go, using text/template for Markdown
// generation and encoding/xml for schema.xml.
//
// Generated outputs:
//   - schema.xml: Protocol schema definition
//   - skills/{skill}/SKILL.md: Claude Code skills
//   - agents/{role}.md: Claude Code agent definitions
//   - .opencode/skill/{skill}/SKILL.md: OpenCode skills
//   - .opencode/agent/{role}.md: OpenCode agent definitions
//
//go:generate go run ../../tools/codegen --targets claude-code,opencode
package codegen
