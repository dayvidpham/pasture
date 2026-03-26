// Package codegen — goldmark-based markdown structure validation and extraction.
//
// This file provides two production functions for validating and querying
// generated SKILL.md documents via goldmark's AST:
//
//   - ValidateSkillStructure — checks heading hierarchy for common errors
//   - ExtractSection — extracts content under a heading by title
//
// These complement the two-pass pipeline (header + body render) by
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

	// Phase 1: find the best match (shallowest level).
	type match struct {
		level int
		node  ast.Node
	}
	var bestMatch *match

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		title := HeadingTextFromAST(n, markdown)
		if title != headingTitle {
			return ast.WalkContinue, nil
		}
		if bestMatch == nil || h.Level < bestMatch.level {
			bestMatch = &match{
				level: h.Level,
				node:  n,
			}
		}
		return ast.WalkContinue, nil
	})

	if bestMatch == nil {
		return nil, fmt.Errorf(
			"codegen.ExtractSection: heading %q not found in markdown — "+
				"where: searching all heading levels (H1-H6) — "+
				"fix: verify the heading title matches exactly (case-sensitive)",
			headingTitle,
		)
	}

	// Phase 2: find the byte range of the section content.
	// Content starts after the heading line and ends at the next heading
	// at the same or shallower level (or EOF).
	targetLevel := bestMatch.level
	inSection := false
	var contentStart, contentEnd int

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}

		title := HeadingTextFromAST(n, markdown)

		if !inSection {
			if h.Level == targetLevel && title == headingTitle && n == bestMatch.node {
				inSection = true
				// Content starts after this heading's lines.
				// The heading node's Lines() gives us the source lines.
				lines := n.Lines()
				if lines.Len() > 0 {
					lastLine := lines.At(lines.Len() - 1)
					contentStart = lastLine.Stop
				}
			}
			return ast.WalkContinue, nil
		}

		// We are in the section — check if this heading closes it.
		if h.Level <= targetLevel {
			// This heading is at the same or higher level — section ends here.
			lines := n.Lines()
			if lines.Len() > 0 {
				contentEnd = lines.At(0).Start
			}
			return ast.WalkStop, nil
		}

		return ast.WalkContinue, nil
	})

	// If contentEnd was never set, the section extends to EOF.
	if contentEnd == 0 && inSection {
		contentEnd = len(markdown)
	}

	if contentStart >= contentEnd {
		// Empty section — return empty bytes, no error.
		return []byte{}, nil
	}

	content := markdown[contentStart:contentEnd]

	// Trim leading/trailing whitespace from the extracted content but preserve
	// internal formatting.
	return []byte(strings.TrimSpace(string(content))), nil
}

// ─── Internal helpers ───────────────────────────────────────────────────────

// HeadingTextFromAST extracts the full text of a heading AST node by
// concatenating its child Text and String segment values.
func HeadingTextFromAST(n ast.Node, src []byte) string {
	var buf strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch c := child.(type) {
		case *ast.Text:
			buf.Write(c.Value(src))
		case *ast.String:
			buf.Write(c.Value)
		}
	}
	return buf.String()
}
