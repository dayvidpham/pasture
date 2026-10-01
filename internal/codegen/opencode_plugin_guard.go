package codegen

import (
	"fmt"
	"regexp"
	"strings"
)

// OpenCode plugin import patterns beyond a line-leading static import: a
// dynamic import call and a re-export from another module. Both need host
// resolution at load time exactly like a static import does.
var (
	openCodeDynamicImportRE = regexp.MustCompile(`\bimport\s*\(`)
	openCodeExportFromRE    = regexp.MustCompile(`(?m)^\s*export\s+.*\bfrom\b\s*["']`)
)

// OpenCodePluginImportViolation reports why the given generated OpenCode
// plugin source performs a runtime import, or "" when it performs none.
//
// The real host resolves no package at plugin load time, so the emitted
// plugin must load with no sibling beside it: no static import statement,
// no dynamic import call, no re-export from another module, and no CommonJS
// require. A type-only import is allowed: the transpiler erases it before
// load, so it needs no host resolution. The retired plugin-package specifier
// and its define helper are refused by name as well, since either would
// reintroduce the host-resolution failure.
//
// Tests hold the generated module to this helper directly — including the
// mutation control, which must trip this same guard — so relaxing the guard
// turns those tests red instead of passing silently.
func OpenCodePluginImportViolation(module string) string {
	stripped := stripTypeScriptComments(module)
	if strings.Contains(stripped, "@opencode/plugin") {
		return `references the plugin package specifier "@opencode/plugin"; the plugin must perform no runtime import`
	}
	if strings.Contains(stripped, "Plugin.define") {
		return `calls Plugin.define; the plugin must default-export the plain definition object`
	}
	if strings.Contains(stripped, "require(") {
		return `uses require(); the plugin must perform no runtime imports`
	}
	for _, line := range strings.Split(stripped, "\n") {
		trimmed := strings.TrimSpace(line)
		rest, ok := cutImportPrefix(trimmed)
		if !ok {
			continue
		}
		if rest == "type" || strings.HasPrefix(rest, "type ") || strings.HasPrefix(rest, "type\t") || strings.HasPrefix(rest, "type;") {
			continue // type-only imports are erased before load
		}
		return fmt.Sprintf("has runtime import statement %q; the plugin must have zero", strings.TrimSpace(line))
	}
	if openCodeDynamicImportRE.MatchString(stripped) {
		return `uses a dynamic import() call; the plugin must perform no runtime imports`
	}
	if openCodeExportFromRE.MatchString(stripped) {
		return `re-exports from another module; the plugin must perform no runtime imports`
	}
	return ""
}

// cutImportPrefix reports the remainder after a leading runtime import
// keyword, if the trimmed line opens one. A type-only remainder is returned
// as-is for the caller to allow.
func cutImportPrefix(trimmed string) (string, bool) {
	for _, prefix := range []string{"import ", "import\t"} {
		if rest, ok := strings.CutPrefix(trimmed, prefix); ok {
			return strings.TrimSpace(rest), true
		}
	}
	if trimmed == "import" {
		return "", true
	}
	for _, prefix := range []string{`import"`, `import'`} {
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimPrefix(trimmed, "import"), true
		}
	}
	return "", false
}

// stripTypeScriptComments blanks line and block comments while preserving
// string contents, so prose can never satisfy a structural match. The
// generated plugin holds no regular-expression literal, so a slash is either
// division or a comment opener.
func stripTypeScriptComments(source string) string {
	out := []byte(source)
	var quote byte
	for i := 0; i < len(out); i++ {
		c := out[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
			continue
		}
		switch {
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '/' && i+1 < len(out) && out[i+1] == '/':
			for ; i < len(out) && out[i] != '\n'; i++ {
				out[i] = ' '
			}
		case c == '/' && i+1 < len(out) && out[i+1] == '*':
			for ; i < len(out); i++ {
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i], out[i+1] = ' ', ' '
					i++
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
			}
		}
	}
	return string(out)
}
