// Package codegen — goldmark-based markdown structure validation and extraction.
//
// This file provides two production functions for validating and querying
// generated SKILL.md documents via goldmark's AST:
//
//   - ValidateSkillStructure — checks heading hierarchy for common errors
//   - ExtractSection — extracts content under a heading by title
//
// These complement the unified skill generation pipeline by
// providing post-render structural validation.
package codegen

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// ─── Errors ─────────────────────────────────────────────────────────────────

// SkillStructureError describes a structural problem in a generated SKILL.md.
type SkillStructureError struct {
	// Problems is a list of structural violations found.
	Problems []string
}

// Error implements the error interface.
func (e *SkillStructureError) Error() string {
	return fmt.Sprintf(
		"codegen.ValidateSkillStructure: %d structural problem(s) found:\n  - %s\n"+
			"fix: review the generated markdown and ensure heading hierarchy is correct "+
			"(no duplicate H2 titles, H3 must be under an H2, etc.)",
		len(e.Problems),
		strings.Join(e.Problems, "\n  - "),
	)
}

// ─── ValidateSkillStructure ─────────────────────────────────────────────────

// ValidateSkillStructure validates the heading hierarchy of generated markdown.
//
// It parses the markdown into an AST via goldmark and checks for:
//   - Duplicate H2 titles (same title text at level 2)
//   - Orphan H3 headings (H3 that appear before any H2)
//
// Returns nil if the structure is valid.
// Returns a *SkillStructureError containing all violations found.
// Returns a plain error if goldmark parsing fails.
//
// Empty or whitespace-only markdown is considered valid (no headings to validate).
func ValidateSkillStructure(markdown []byte) error {
	if len(strings.TrimSpace(string(markdown))) == 0 {
		return nil
	}

	doc := goldmark.New().Parser().Parse(text.NewReader(markdown))

	var problems []string

	// Track H2 titles for duplicate detection.
	h2Titles := make(map[string]int) // title → count
	// Track the shallowest heading level seen so far for orphan detection.
	// An H3 is orphan only if no heading at level < 3 has appeared yet
	// (neither H1 nor H2). Sub-skill files legitimately use H1 → H3
	// (skipping H2 for figure headings), which is valid.
	shallowestSeen := 7 // deeper than any valid heading level (H1-H6)

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}

		title := HeadingTextFromAST(n, markdown)

		if h.Level < shallowestSeen {
			shallowestSeen = h.Level
		}

		switch h.Level {
		case 2:
			h2Titles[title]++
		case 3:
			if shallowestSeen >= 3 {
				problems = append(problems, fmt.Sprintf(
					"orphan H3 %q appears before any H1 or H2 heading — "+
						"H3 headings must be nested under an H1 or H2 parent",
					title,
				))
			}
		}

		return ast.WalkContinue, nil
	})

	// Check for duplicate H2 titles.
	for title, count := range h2Titles {
		if count > 1 {
			problems = append(problems, fmt.Sprintf(
				"duplicate H2 title %q appears %d times — "+
					"each H2 heading must have a unique title within the document",
				title, count,
			))
		}
	}

	if len(problems) > 0 {
		return &SkillStructureError{Problems: problems}
	}
	return nil
}

// ─── ExtractSection ─────────────────────────────────────────────────────────

// ExtractSection extracts a section's content by heading title from rendered markdown.
//
// It returns the content under the matching heading, starting after the heading
// line and extending up to (but not including) the next heading at the same or
// higher (lower number) level.
//
// Heading-level ambiguity: if the same title appears at H2 and H3, the H2 match
// is returned (first highest-level match). More generally, when multiple headings
// share the same title, the one at the shallowest (lowest-numbered) level wins.
//
// Returns an error if no heading with the given title is found.
func ExtractSection(markdown []byte, headingTitle string) ([]byte, error) {
	doc := goldmark.New().Parser().Parse(text.NewReader(markdown))

	// Single walk: record every heading's level, title, and source-line byte
	// extent in document order. All selection logic below operates on this
	// flat list, so the AST is traversed exactly once.
	type headingInfo struct {
		level int
		title string
		start int // byte offset of the heading's first source line (0 when Lines() is empty)
		stop  int // byte offset past the heading's last source line (0 when Lines() is empty)
	}
	var headings []headingInfo

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		info := headingInfo{level: h.Level, title: HeadingTextFromAST(n, markdown)}
		if lines := n.Lines(); lines.Len() > 0 {
			info.start = lines.At(0).Start
			info.stop = lines.At(lines.Len() - 1).Stop
		}
		headings = append(headings, info)
		return ast.WalkContinue, nil
	})

	// Best match: the first heading with the title at the shallowest level.
	best := -1
	for i, h := range headings {
		if h.title == headingTitle && (best < 0 || h.level < headings[best].level) {
			best = i
		}
	}
	if best < 0 {
		return nil, fmt.Errorf(
			"codegen.ExtractSection: heading %q not found in markdown — "+
				"where: searching all heading levels (H1-H6) — "+
				"fix: verify the heading title matches exactly (case-sensitive)",
			headingTitle,
		)
	}

	// Content starts after the matched heading's lines and ends at the next
	// heading at the same or shallower level. A zero end offset (no closing
	// heading, or one whose Lines() is empty) means the section runs to EOF.
	contentStart := headings[best].stop
	contentEnd := 0
	for _, h := range headings[best+1:] {
		if h.level <= headings[best].level {
			contentEnd = h.start
			break
		}
	}
	if contentEnd == 0 {
		contentEnd = len(markdown)
	}

	if contentStart >= contentEnd {
		// Empty section — return empty bytes, no error.
		return []byte{}, nil
	}

	// Trim leading/trailing whitespace from the extracted content but preserve
	// internal formatting.
	return []byte(strings.TrimSpace(string(markdown[contentStart:contentEnd]))), nil
}

// ─── Internal helpers ───────────────────────────────────────────────────────

// HeadingTextFromAST extracts the full text of a heading AST node by
// concatenating its child Text and String segment values.
func HeadingTextFromAST(n ast.Node, src []byte) string {
	var buf strings.Builder
	// Walk all descendants — not just direct children — so that text inside
	// inline formatting nodes (bold, italic, code spans) is captured.
	_ = ast.Walk(n, func(child ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := child.(type) {
		case *ast.Text:
			buf.Write(c.Value(src))
		case *ast.String:
			buf.Write(c.Value)
		}
		return ast.WalkContinue, nil
	})
	return buf.String()
}
