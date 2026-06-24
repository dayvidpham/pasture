// Package codegen — shared embed FS for all Go text/template files.
//
// This file declares the single embed.FS that all generator functions in this
// package share (agents.go, skills.go, etc.). Keeping the embed directive here
// avoids duplicate //go:embed declarations across multiple files.
package codegen

import "embed"

// templatesFS is the embedded filesystem containing all Go text/template files
// under internal/codegen/templates/. All generator functions in this package
// must load templates via this FS rather than from the real filesystem, so
// that the generated binary works without access to the source tree.
//
// The explicit `templates/_*` pattern is required because go:embed omits files
// whose names begin with "_" or "." by default; the shared skill-body partials
// (templates/_skill_body.go.tmpl, templates/_skill_sub_body.go.tmpl) carry a
// leading underscore so they sort apart from the harness skill templates and
// signal "partial, not a standalone template".
//
//go:embed templates templates/_*
var templatesFS embed.FS
