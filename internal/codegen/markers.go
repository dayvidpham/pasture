// Package codegen provides utilities for code generation over SKILL.md files.
//
// It ports the marker parsing and replacement logic from gen_skills.py
// (Python) to Go, enabling the pasture codegen pipeline to operate on
// SKILL.md files that contain BEGIN/END marker pairs delimiting generated
// sections.
package codegen

import (
	"fmt"
	"strings"
)

// GeneratedBegin is the opening marker that delimits the generated section.
const GeneratedBegin = "<!-- BEGIN GENERATED FROM aura schema -->"

// GeneratedEnd is the closing marker that delimits the generated section.
const GeneratedEnd = "<!-- END GENERATED FROM aura schema -->"

// MarkerError is a typed error for marker validation failures.
//
// It contains actionable diagnostic information: what went wrong, where
// it failed (file path and line number), and how to fix it. Using a
// dedicated type instead of fmt.Errorf allows callers to assert on the
// specific failure kind without string parsing.
type MarkerError struct {
	// Path is the file path where the marker problem was found.
	Path string

	// Problem is a short description of what went wrong.
	// Examples: "missing both markers", "duplicate BEGIN", "END before BEGIN".
	Problem string

	// Line is the 1-based line number of the offending marker, or 0 when
	// the problem is not associated with a specific line (e.g., both markers
	// missing).
	Line int

	// Fix is an actionable remediation message that tells the user exactly
	// what to do to resolve the problem.
	Fix string
}

// Error implements the error interface.
//
// The message follows the actionable-error convention:
//
//	(1) what went wrong   — Problem field
//	(2) where it failed   — Path + Line
//	(3) how to fix it     — Fix field
func (e *MarkerError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf(
			"marker error in %s (line %d): %s — %s",
			e.Path, e.Line, e.Problem, e.Fix,
		)
	}
	return fmt.Sprintf(
		"marker error in %s: %s — %s",
		e.Path, e.Problem, e.Fix,
	)
}

// FindMarkerPositions returns the (beginIdx, endIdx) line indices for the
// BEGIN/END marker pair within lines.
//
// Indices are 0-based into the lines slice. The marker lines themselves are
// included (i.e., lines[beginIdx] == GeneratedBegin and
// lines[endIdx] == GeneratedEnd after stripping trailing newlines).
//
// path is used only for error messages and does not affect parsing.
//
// Returns a *MarkerError for each of the 6 failure cases:
//  1. Missing both markers — neither BEGIN nor END is present.
//  2. Missing BEGIN — END is present but BEGIN is absent.
//  3. Missing END — BEGIN is present but END is absent.
//  4. Duplicate BEGIN — BEGIN appears more than once.
//  5. Duplicate END — END appears more than once.
//  6. END before BEGIN — both markers present but in reversed order.
func FindMarkerPositions(lines []string, path string) (begin, end int, err error) {
	beginIdx := -1
	endIdx := -1
	dupBeginLine := -1
	dupEndLine := -1

	for i, line := range lines {
		stripped := strings.TrimRight(line, "\n")
		switch stripped {
		case GeneratedBegin:
			if beginIdx != -1 {
				dupBeginLine = i + 1 // 1-based for error messages
			} else {
				beginIdx = i
			}
		case GeneratedEnd:
			if endIdx != -1 {
				dupEndLine = i + 1
			} else {
				endIdx = i
			}
		}
	}

	// Check duplicate BEGIN before the "found" checks so that partial
	// duplicates (e.g., two BEGINs + one END) report the right error.
	if dupBeginLine > 0 {
		return 0, 0, &MarkerError{
			Path:    path,
			Problem: "duplicate BEGIN marker",
			Line:    dupBeginLine,
			Fix: fmt.Sprintf(
				"expected exactly one %q; remove the duplicate at line %d and re-run",
				GeneratedBegin, dupBeginLine,
			),
		}
	}
	if dupEndLine > 0 {
		return 0, 0, &MarkerError{
			Path:    path,
			Problem: "duplicate END marker",
			Line:    dupEndLine,
			Fix: fmt.Sprintf(
				"expected exactly one %q; remove the duplicate at line %d and re-run",
				GeneratedEnd, dupEndLine,
			),
		}
	}

	// Both missing
	if beginIdx == -1 && endIdx == -1 {
		return 0, 0, &MarkerError{
			Path:    path,
			Problem: "missing both markers",
			Line:    0,
			Fix: fmt.Sprintf(
				"add both %q and %q (in order) to the file, then re-run; "+
					"use --init to prepend them automatically",
				GeneratedBegin, GeneratedEnd,
			),
		}
	}

	// Missing BEGIN (END present)
	if beginIdx == -1 {
		return 0, 0, &MarkerError{
			Path:    path,
			Problem: "missing BEGIN marker",
			Line:    endIdx + 1,
			Fix: fmt.Sprintf(
				"%q found at line %d but %q is absent; "+
					"add the BEGIN marker above the END marker and re-run",
				GeneratedEnd, endIdx+1, GeneratedBegin,
			),
		}
	}

	// Missing END (BEGIN present)
	if endIdx == -1 {
		return 0, 0, &MarkerError{
			Path:    path,
			Problem: "missing END marker",
			Line:    beginIdx + 1,
			Fix: fmt.Sprintf(
				"%q found at line %d but %q is absent; "+
					"add the END marker below the BEGIN marker and re-run",
				GeneratedBegin, beginIdx+1, GeneratedEnd,
			),
		}
	}

	// END before BEGIN (reversed)
	if endIdx < beginIdx {
		return 0, 0, &MarkerError{
			Path:    path,
			Problem: "END marker appears before BEGIN marker",
			Line:    endIdx + 1,
			Fix: fmt.Sprintf(
				"END marker is at line %d but BEGIN marker is at line %d; "+
					"swap the markers so BEGIN comes first, then re-run",
				endIdx+1, beginIdx+1,
			),
		}
	}

	return beginIdx, endIdx, nil
}

// HasMarkers returns true if content contains both the BEGIN and END markers.
//
// This is a fast pre-check used by --init mode to decide whether to prepend
// markers before calling FindMarkerPositions. It does not validate ordering
// or uniqueness.
func HasMarkers(content string) bool {
	return strings.Contains(content, GeneratedBegin) && strings.Contains(content, GeneratedEnd)
}

// ReplaceMarkerRegion replaces the content between (and including) the
// BEGIN/END markers in oldContent with rendered.
//
// The dropPrefix parameter controls how the content before the BEGIN marker
// is handled:
//
//   - dropPrefix=true: everything before BEGIN is dropped. The generated
//     content (rendered) owns the full frontmatter and heading. This matches
//     gen_skill behaviour where the template owns the complete file header.
//
//   - dropPrefix=false: everything before BEGIN is preserved. The content
//     before the BEGIN marker (e.g., a hand-authored h1 heading) is kept
//     verbatim. This matches gen_sub_skill behaviour.
//
// In both modes everything after the END marker is preserved — that is the
// hand-authored body that lives below the generated section.
//
// rendered must already include the BEGIN and END marker lines themselves
// (as the Python template renders the full BEGIN…END block).
//
// Returns a *MarkerError if oldContent has malformed markers.
func ReplaceMarkerRegion(oldContent, rendered string, dropPrefix bool) (string, error) {
	lines := strings.SplitAfter(oldContent, "\n")

	// SplitAfter on a trailing newline produces a final empty element; trim it
	// so line indices match splitlines(keepends=True) semantics.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	beginIdx, endIdx, err := FindMarkerPositions(lines, "")
	if err != nil {
		return "", err
	}

	// Ensure rendered ends with a newline for clean concatenation.
	if !strings.HasSuffix(rendered, "\n") {
		rendered += "\n"
	}

	// Preserve the hand-authored body below END.
	bodyLines := lines[endIdx+1:]
	body := strings.Join(bodyLines, "")

	if dropPrefix {
		// Template owns everything before BEGIN; discard the prefix.
		return rendered + body, nil
	}

	// Preserve everything before BEGIN (e.g., hand-authored h1 heading).
	prefixLines := lines[:beginIdx]
	prefix := strings.Join(prefixLines, "")
	return prefix + rendered + body, nil
}

// PrependMarkers adds the BEGIN/END marker pair to the top of content when
// content does not already contain the markers.
//
// It is used by --init mode to prepare files before generation. The returned
// string has the markers on the first two lines, followed by a blank line,
// followed by the original content.
//
// If content already contains both markers (as reported by HasMarkers),
// it is returned unchanged to avoid double-prepending.
func PrependMarkers(content string) string {
	if HasMarkers(content) {
		return content
	}
	return GeneratedBegin + "\n" + GeneratedEnd + "\n\n" + content
}
