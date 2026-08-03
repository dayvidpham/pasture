package scan

import (
	"strings"
	"unicode/utf8"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// Candidate is one reported occurrence of a closed PatternID inside one
// active owner's Goldmark AST. Candidate is immutable and can only be
// constructed by this package's own scanning (see scanFileCandidates); a
// caller can inspect one but never fabricate one, so a Candidate handed to
// Classify always names a real, exact source occurrence.
type Candidate struct {
	location      ir.Location
	astNode       string
	pattern       PatternID
	snippet       string
	contentWindow string
}

// newCandidate validates and constructs one Candidate. astNode is the
// Goldmark AST node context (e.g. "CodeSpan", "Paragraph", "ListItem",
// "Blockquote", "FencedCodeBlock:bash") and section is the nearest preceding
// heading text (or "body" when the candidate precedes every heading).
// snippet is the exact matched text (e.g. "Skill(/"), reported as-is and
// used for display; contentWindow is the wider enclosing source line (see
// ContentWindow) classification matching keys on.
func newCandidate(owner, file, astNode, section string, pattern PatternID, at ir.SourceRange, snippet, contentWindow string) (Candidate, error) {
	location, err := ir.NewLocation(owner, file, section, at)
	if err != nil {
		return Candidate{}, diagnostic(
			"candidate location is invalid",
			"every reported candidate must retain an exact, actionable source coordinate",
			"scan.newCandidate", "candidate construction",
			"the candidate cannot enter the inventory",
			"construct the location with a non-empty owner/file/section and a valid range",
			err,
		)
	}
	if !pattern.IsValid() {
		return Candidate{}, diagnostic(
			"candidate pattern is unknown",
			"the pattern registry is a closed, code-owned set (see PatternIDs)",
			"scan.newCandidate", "candidate construction",
			"the candidate cannot be classified against a known pattern",
			"match the candidate against one of PatternIDs()",
			nil,
		)
	}
	if strings.TrimSpace(astNode) == "" {
		return Candidate{}, diagnostic(
			"candidate AST node context is empty",
			"every candidate must report which Goldmark node kind it was found in",
			"scan.newCandidate", "candidate construction",
			"a reviewer cannot tell prose from code from a list item",
			"supply the non-empty Goldmark node-kind label",
			nil,
		)
	}
	if snippet == "" || !utf8.ValidString(snippet) {
		return Candidate{}, diagnostic(
			"candidate snippet is empty or invalid UTF-8",
			"every candidate must carry the exact matched source text",
			"scan.newCandidate", "candidate construction",
			"the candidate cannot be reviewed or classified",
			"supply the exact non-empty valid UTF-8 matched snippet",
			nil,
		)
	}
	if contentWindow == "" || !utf8.ValidString(contentWindow) {
		return Candidate{}, diagnostic(
			"candidate content window is empty or invalid UTF-8",
			"the content window (the enclosing source line) is what the classification manifest keys on, distinct from the short matched snippet",
			"scan.newCandidate", "candidate construction",
			"the candidate cannot be matched against a checked-in classification entry",
			"supply the exact non-empty valid UTF-8 enclosing line",
			nil,
		)
	}
	return Candidate{location: location, astNode: astNode, pattern: pattern, snippet: snippet, contentWindow: contentWindow}, nil
}

// Location returns the candidate's canonical owner/file/section and exact
// byte source range.
func (c Candidate) Location() ir.Location { return c.location }

// ASTNode returns the Goldmark node-kind context the candidate was found in.
func (c Candidate) ASTNode() string { return c.astNode }

// Pattern returns the closed pattern identity that matched.
func (c Candidate) Pattern() PatternID { return c.pattern }

// Snippet returns the exact matched source text (e.g. "Skill(/"). This is
// the precise, narrow match reported for display/diagnostics; it is not the
// classification-manifest matching key — see ContentWindow.
func (c Candidate) Snippet() string { return c.snippet }

// ContentWindow returns the enclosing source line around the match — the
// wider "reviewed content" a maintainer actually looked at when classifying
// this occurrence. Classify keys on this (plus Section, to further scope
// disambiguation — see ClassificationEntry) rather than on Snippet, which is
// frequently identical across many unrelated occurrences of the same
// pattern (e.g. every "Skill(/" call shares the same Snippet) and would
// otherwise degrade the classification key to raw encounter order.
func (c Candidate) ContentWindow() string { return c.contentWindow }

// IsValid reports whether every Candidate invariant holds.
func (c Candidate) IsValid() bool {
	return c.location.IsValid() && c.pattern.IsValid() && strings.TrimSpace(c.astNode) != "" &&
		c.snippet != "" && utf8.ValidString(c.snippet) &&
		c.contentWindow != "" && utf8.ValidString(c.contentWindow)
}
