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
//go:embed templates
var templatesFS embed.FS
