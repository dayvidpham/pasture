package scan

import (
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// newGoldmarkParser returns a Goldmark parser using the exact configuration
// internal/codegen/ir.Markdown uses (goldmark.New(), no extensions), so this
// package's AST shape can never silently drift from the one #38 compiles.
// This package cannot reuse ir.Markdown/ir.MarkdownSourceRanges directly:
// #38 deliberately keeps the parsed ast.Node tree unexported (Document is an
// opaque compiler input, not a general-purpose AST-inspection API), so a
// classification scanner that needs node kinds must parse independently with
// the identical configuration rather than fork #38's Part/Document types.
func newGoldmarkParser() goldmark.Markdown { return goldmark.New() }

// scanFileCandidates parses source through #38's exact Goldmark
// configuration and reports every closed-pattern-registry match found in
// prose, inline code, fenced/indented code, list items, blockquotes, block
// HTML (including HTML comments), and inline raw HTML, in deterministic AST
// (i.e. source) order. owner and file identify the candidate's canonical
// owner and relative path (see OwnerManifest); this package treats every
// scanned file as its own owner (see doc.go).
func scanFileCandidates(owner, file string, source []byte) ([]Candidate, error) {
	if len(source) == 0 || !utf8.Valid(source) {
		return nil, diagnostic(
			"owner source is empty or invalid UTF-8",
			"every active owner must be exact valid UTF-8 Markdown before Goldmark parsing",
			"scan.scanFileCandidates:"+file, "candidate scanning",
			"the owner cannot be scanned for candidates",
			"ensure the owner file is non-empty valid UTF-8",
			nil,
		)
	}

	tree := newGoldmarkParser().Parser().Parse(text.NewReader(source))

	var candidates []Candidate
	section := "body"
	var walkErr error
	_ = ast.Walk(tree, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch value := node.(type) {
		case *ast.Heading:
			if heading := headingText(value, source); heading != "" {
				section = heading
			}
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			found, err := blockCandidates(owner, file, section, node, source)
			if err != nil {
				walkErr = err
				return ast.WalkStop, err
			}
			candidates = append(candidates, found...)
			return ast.WalkSkipChildren, nil
		case *ast.HTMLBlock:
			found, err := htmlBlockCandidates(owner, file, section, value, source)
			if err != nil {
				walkErr = err
				return ast.WalkStop, err
			}
			candidates = append(candidates, found...)
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			found, err := textCandidates(owner, file, section, node, source)
			if err != nil {
				walkErr = err
				return ast.WalkStop, err
			}
			candidates = append(candidates, found...)
		case *ast.RawHTML:
			found, err := rawHTMLCandidates(owner, file, section, value, source)
			if err != nil {
				walkErr = err
				return ast.WalkStop, err
			}
			candidates = append(candidates, found...)
		}
		return ast.WalkContinue, nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return candidates, nil
}

// nearestContainerLabel walks node's ancestor chain and returns the nearest
// meaningful Goldmark block/inline container kind. Code spans take priority
// over their enclosing paragraph/list/blockquote so an inline code example is
// never mislabeled as plain prose.
func nearestContainerLabel(node ast.Node) string {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Kind() {
		case ast.KindCodeSpan:
			return "CodeSpan"
		case ast.KindHeading:
			return "Heading"
		case ast.KindListItem:
			return "ListItem"
		case ast.KindBlockquote:
			return "Blockquote"
		case ast.KindParagraph:
			return "Paragraph"
		}
	}
	return "Text"
}

// textCandidates matches the pattern registry against one inline *ast.Text
// node's exact source segment. The reported Candidate range is the precise
// byte span of the match itself (segment.Start + local match offset),
// because an *ast.Text segment always indexes one contiguous run of source —
// unlike a multi-line code block, no offset-mapping assumption is required
// here.
func textCandidates(owner, file, section string, node ast.Node, source []byte) ([]Candidate, error) {
	textNode, ok := node.(*ast.Text)
	if !ok {
		return nil, nil
	}
	segment := textNode.Segment
	value := string(segment.Value(source))
	label := nearestContainerLabel(node)

	var out []Candidate
	for _, match := range matchPatterns(value) {
		start := segment.Start + match.start
		stop := segment.Start + match.stop
		candidate, err := buildCandidate(owner, file, label, section, match.id, start, stop, source)
		if err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, nil
}

// blockCandidates matches the pattern registry against one leaf
// FencedCodeBlock/CodeBlock node's concatenated line text.
func blockCandidates(owner, file, section string, node ast.Node, source []byte) ([]Candidate, error) {
	return segmentsCandidates(owner, file, section, blockLabel(node, source), node.Lines(), source)
}

// htmlBlockCandidates matches the pattern registry against one HTMLBlock
// node, including its ClosureLine: HTMLBlock.Lines() alone omits the closing
// line (e.g. the "-->" of an HTML comment, or a closing tag), so a pattern
// occurrence placed on that final line would otherwise never be scanned — a
// "commented-out" native-syntax example is still live content to an LLM
// reading the raw file.
func htmlBlockCandidates(owner, file, section string, node *ast.HTMLBlock, source []byte) ([]Candidate, error) {
	combined := text.NewSegments()
	lines := node.Lines()
	for i := 0; i < lines.Len(); i++ {
		combined.Append(lines.At(i))
	}
	if node.HasClosure() {
		combined.Append(node.ClosureLine)
	}
	return segmentsCandidates(owner, file, section, "HTMLBlock", combined, source)
}

// rawHTMLCandidates matches the pattern registry against one inline RawHTML
// node's segments (e.g. an inline "<!-- ... -->" comment or raw tag).
func rawHTMLCandidates(owner, file, section string, node *ast.RawHTML, source []byte) ([]Candidate, error) {
	return segmentsCandidates(owner, file, section, "RawHTML", node.Segments, source)
}

// segmentsCandidates is the shared matcher for every multi-segment leaf
// construct (fenced/indented code blocks, HTML blocks, inline raw HTML):
// Goldmark's own line/segment boundaries for one such construct are
// contiguous (each segment's Stop equals the next segment's Start), so a
// match's local offset maps directly onto the first segment's Start;
// blockConcatenationIsContiguous defends that assumption and falls back to
// the whole-construct range (never a wrong range) if a future Goldmark
// revision ever produces a non-contiguous segment set.
func segmentsCandidates(owner, file, section, label string, segments *text.Segments, source []byte) ([]Candidate, error) {
	if segments == nil || segments.Len() == 0 {
		return nil, nil
	}
	first := segments.At(0)
	last := segments.At(segments.Len() - 1)
	value := string(segments.Value(source))
	contiguous := blockConcatenationIsContiguous(segments, len(value))

	var out []Candidate
	for _, match := range matchPatterns(value) {
		start, stop := first.Start, last.Stop
		if contiguous {
			start = first.Start + match.start
			stop = first.Start + match.stop
		}
		candidate, err := buildCandidate(owner, file, label, section, match.id, start, stop, source)
		if err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, nil
}

// buildCandidate constructs one Candidate from an already-resolved absolute
// [start, stop) match range, computing its content window (the enclosing
// source line — see Candidate.ContentWindow and lineWindow) uniformly for
// every call site (inline text, code blocks, HTML blocks, inline raw HTML).
func buildCandidate(owner, file, astNode, section string, pattern PatternID, start, stop int, source []byte) (Candidate, error) {
	windowStart, windowStop := lineWindow(source, start, stop)
	contentWindow := strings.TrimSpace(string(source[windowStart:windowStop]))
	return newCandidate(owner, file, astNode, section, pattern, ir.SourceRange{Start: start, Stop: stop}, string(source[start:stop]), contentWindow)
}

// lineWindow returns the byte range of every physical source line touched by
// [start, stop): it scans backward from start to the character after the
// preceding newline (or the start of source) and forward from stop to the
// next newline (or the end of source). A multi-line match (not expected from
// this package's short literal patterns, but handled defensively) expands to
// cover every line it spans, never truncating it.
func lineWindow(source []byte, start, stop int) (int, int) {
	windowStart := start
	for windowStart > 0 && source[windowStart-1] != '\n' {
		windowStart--
	}
	windowStop := stop
	for windowStop < len(source) && source[windowStop] != '\n' {
		windowStop++
	}
	return windowStart, windowStop
}

// blockConcatenationIsContiguous reports whether segments' entries abut end
// to end (so segments.Value's local offsets map onto absolute source offsets
// by a single first.Start addition) by checking the concatenated value's
// length against the first/last segment span.
func blockConcatenationIsContiguous(segments *text.Segments, valueLen int) bool {
	if segments.Len() == 0 {
		return false
	}
	first := segments.At(0)
	last := segments.At(segments.Len() - 1)
	return valueLen == last.Stop-first.Start
}

// blockLabel returns the AST node label for a leaf code block, including the
// fenced info-string language when present (e.g. "FencedCodeBlock:bash").
func blockLabel(node ast.Node, source []byte) string {
	if fenced, ok := node.(*ast.FencedCodeBlock); ok {
		language := strings.TrimSpace(string(fenced.Language(source)))
		if language == "" {
			language = "text"
		}
		return "FencedCodeBlock:" + language
	}
	return "CodeBlock"
}

// headingText concatenates a Heading node's own descendant text, used to
// track the nearest preceding section for every subsequent candidate.
func headingText(node ast.Node, source []byte) string {
	var buf strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		appendNodeText(&buf, child, source)
	}
	return strings.TrimSpace(buf.String())
}

func appendNodeText(buf *strings.Builder, node ast.Node, source []byte) {
	switch value := node.(type) {
	case *ast.Text:
		buf.Write(value.Segment.Value(source))
		return
	case *ast.String:
		buf.Write(value.Value)
		return
	}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		appendNodeText(buf, child, source)
	}
}
