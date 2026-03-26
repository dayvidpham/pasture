// mdtest_helpers_test.go — goldmark-based markdown assertion helpers.
//
// These helpers are used by codegen tests that need to assert structural
// properties of generated Markdown documents (headings, sections, tables,
// code blocks) without brittle string matching against full output.
//
// Usage pattern:
//
//	doc, src := parseMD(t, markdownString)
//	assertSectionExists(t, doc, src, 2, "My Section")
//	assertSectionContains(t, doc, src, 2, "My Section", "expected text")
//	assertHasTable(t, doc, src, 2, "My Section")
package codegen_test

import (
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// stripFrontmatter removes YAML frontmatter (--- ... ---\n) from src.
// If no frontmatter is present, src is returned unchanged.
func stripFrontmatter(src string) string {
	if !strings.HasPrefix(src, "---\n") {
		return src
	}
	rest := src[4:]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return src
	}
	return rest[idx+5:]
}

// parseMD strips YAML frontmatter from src, parses the remainder with
// goldmark (with the GitHub-Flavored Markdown table extension enabled),
// and returns the document root node and the source bytes used for all
// subsequent node-text extractions.
func parseMD(t *testing.T, src string) (ast.Node, []byte) {
	t.Helper()
	body := stripFrontmatter(src)
	srcBytes := []byte(body)
	md := goldmark.New(goldmark.WithExtensions(extension.Table))
	reader := text.NewReader(srcBytes)
	doc := md.Parser().Parse(reader)
	return doc, srcBytes
}

// headingText extracts the full text content of a heading node by
// concatenating all Text and String segment values found in its children.
func headingText(n ast.Node, src []byte) string {
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

// findSection locates the first heading node at the given level whose text
// matches title (exact match). Returns the heading node, or nil if not found.
func findSection(doc ast.Node, src []byte, level int, title string) ast.Node {
	var found ast.Node
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if h, ok := n.(*ast.Heading); ok && h.Level == level {
			if headingText(n, src) == title {
				found = n
				return ast.WalkStop, nil
			}
		}
		return ast.WalkContinue, nil
	})
	return found
}

// sectionChildren returns the text of all direct child headings (level =
// parentLevel+1) that fall under the section identified by parentLevel and
// parentTitle. A heading is considered "under" the parent section when it
// appears after the parent heading and before the next heading of the same
// or higher level.
func sectionChildren(doc ast.Node, src []byte, parentLevel int, parentTitle string) []string {
	childLevel := parentLevel + 1
	inSection := false
	var children []string

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		if !inSection {
			if h.Level == parentLevel && headingText(n, src) == parentTitle {
				inSection = true
			}
			return ast.WalkContinue, nil
		}
		// We are inside the parent section.
		if h.Level <= parentLevel {
			// Reached a sibling or ancestor heading — stop.
			return ast.WalkStop, nil
		}
		if h.Level == childLevel {
			children = append(children, headingText(n, src))
		}
		return ast.WalkContinue, nil
	})
	return children
}

// hasCodeBlock reports whether the section identified by sectionLevel and
// sectionTitle contains at least one fenced code block. Nodes are
// considered inside the section when they appear after the section heading
// and before the next heading of the same or higher level.
func hasCodeBlock(doc ast.Node, src []byte, sectionLevel int, sectionTitle string) bool {
	inSection := false
	found := false

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if h, ok := n.(*ast.Heading); ok {
			if !inSection {
				if h.Level == sectionLevel && headingText(n, src) == sectionTitle {
					inSection = true
				}
				return ast.WalkContinue, nil
			}
			// Sibling or ancestor heading ends the section.
			if h.Level <= sectionLevel {
				return ast.WalkStop, nil
			}
			return ast.WalkContinue, nil
		}
		if inSection && n.Kind() == ast.KindFencedCodeBlock {
			found = true
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return found
}

// assertSectionExists fails the test if no heading at level with text title
// is found in doc.
func assertSectionExists(t *testing.T, doc ast.Node, src []byte, level int, title string) {
	t.Helper()
	if findSection(doc, src, level, title) == nil {
		t.Errorf("expected heading H%d %q but it was not found in the document", level, title)
	}
}

// assertSectionContains fails the test if the text content found immediately
// under the section (between this heading and the next heading of the same
// or higher level) does not contain substring.
func assertSectionContains(t *testing.T, doc ast.Node, src []byte, level int, sectionTitle string, substring string) {
	t.Helper()
	headingNode := findSection(doc, src, level, sectionTitle)
	if headingNode == nil {
		t.Errorf("section H%d %q not found; cannot assert it contains %q", level, sectionTitle, substring)
		return
	}

	// Collect all text from nodes following the heading until the next
	// heading of the same or higher level.
	var buf strings.Builder
	inSection := false

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if h, ok := n.(*ast.Heading); ok {
			if !inSection {
				if h.Level == level && headingText(n, src) == sectionTitle {
					inSection = true
				}
				return ast.WalkContinue, nil
			}
			if h.Level <= level {
				return ast.WalkStop, nil
			}
			return ast.WalkContinue, nil
		}
		if !inSection {
			return ast.WalkContinue, nil
		}
		switch c := n.(type) {
		case *ast.Text:
			buf.Write(c.Value(src))
		case *ast.String:
			buf.Write(c.Value)
		}
		return ast.WalkContinue, nil
	})

	if !strings.Contains(buf.String(), substring) {
		t.Errorf("section H%d %q does not contain %q\ngot section text: %q", level, sectionTitle, substring, buf.String())
	}
}

// assertHasTable fails the test if the section identified by sectionLevel
// and sectionTitle does not contain a table node.
func assertHasTable(t *testing.T, doc ast.Node, src []byte, sectionLevel int, sectionTitle string) {
	t.Helper()
	inSection := false
	found := false

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if h, ok := n.(*ast.Heading); ok {
			if !inSection {
				if h.Level == sectionLevel && headingText(n, src) == sectionTitle {
					inSection = true
				}
				return ast.WalkContinue, nil
			}
			if h.Level <= sectionLevel {
				return ast.WalkStop, nil
			}
			return ast.WalkContinue, nil
		}
		if inSection && n.Kind() == extast.KindTable {
			found = true
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})

	if !found {
		t.Errorf("section H%d %q does not contain a table", sectionLevel, sectionTitle)
	}
}

// assertAnySectionExists fails the test if no heading with the given title
// exists in doc at any heading level (H1–H6).
// Use this when the fixture specifies a heading title without committing to a
// specific depth (e.g., fixtures written as "## Title" where the actual
// template may render it at H3).
func assertAnySectionExists(t *testing.T, doc ast.Node, src []byte, title string) {
	t.Helper()
	for level := 1; level <= 6; level++ {
		if findSection(doc, src, level, title) != nil {
			return
		}
	}
	t.Errorf("expected a heading %q at any level (H1–H6) but it was not found in the document", title)
}

// countHeadings counts the number of heading nodes at the given level in doc.
func countHeadings(doc ast.Node, src []byte, level int) int {
	count := 0
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if h, ok := n.(*ast.Heading); ok && h.Level == level {
			count++
		}
		return ast.WalkContinue, nil
	})
	return count
}

// assertIsNestedUnder verifies that a heading with childTitle appears within
// the section scope of a heading with parentTitle, and at a deeper level.
//
// "Nested under" means:
//  1. A heading with text parentTitle exists at some level P.
//  2. A heading with text childTitle exists at some level C where C > P.
//  3. childTitle appears AFTER parentTitle and BEFORE the next heading at
//     level <= P (which would close the parent section).
//
// Level skips (e.g. H1 → H3) are explicitly allowed — only the parent-child
// relationship matters, not strict monotonic progression.
func assertIsNestedUnder(t *testing.T, doc ast.Node, src []byte, parentTitle, childTitle string) {
	t.Helper()

	// Phase 1: find the parent heading and record its level.
	parentLevel := 0
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		if headingText(n, src) == parentTitle {
			parentLevel = h.Level
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})

	if parentLevel == 0 {
		t.Errorf("assertIsNestedUnder: parent heading %q not found in document", parentTitle)
		return
	}

	// Phase 2: walk headings in document order; once inside the parent
	// section, check that childTitle appears before the section closes.
	inSection := false
	found := false

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		if !inSection {
			if h.Level == parentLevel && headingText(n, src) == parentTitle {
				inSection = true
			}
			return ast.WalkContinue, nil
		}
		// Inside parent section — a heading at level <= parentLevel closes it.
		if h.Level <= parentLevel {
			return ast.WalkStop, nil
		}
		// Any deeper heading with the child title satisfies the assertion.
		if headingText(n, src) == childTitle {
			found = true
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})

	if !found {
		t.Errorf(
			"assertIsNestedUnder: heading %q was not found nested under %q\n"+
				"(expected a heading with that title to appear after %q and before the next H%d or shallower heading)",
			childTitle, parentTitle, parentTitle, parentLevel,
		)
	}
}
