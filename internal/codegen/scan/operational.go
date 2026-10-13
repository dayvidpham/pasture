package scan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Lexical candidates are deliberately not a verb allowlist: an unknown command
// is rejected as ambiguous. Escapes, split shell words and argv strings retain
// their original coordinates; the gate never rewrites an emitted byte.
// The invisible spelling candidates cover zero-width space, non-joiner,
// joiner and BOM: common copy/paste ambiguity, not Unicode normalization.
// Other format characters remain literal shell-token characters; this is not
// a promise to detect every visually confusable spelling.
var trackerWord = regexp.MustCompile(`b(?:\\\r?\n|\\|["'\x{200B}\x{200C}\x{200D}\x{FEFF}])*d`)
var trackerArgument = regexp.MustCompile(`^[\s"'` + "`" + `,\\]*(--?[^\s"'` + "`" + `,]+|[A-Za-z][A-Za-z0-9_-]*)`)
var trackerHeredoc = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z_0-9]*)`)

// TrackerCandidates discovers command-shaped legacy tracker uses in exact
// bytes. Goldmark supplies the section/node vocabulary for Markdown, while
// shell/config payloads are conservatively inspected even inside strings and
// heredocs (which frequently carry executable prompts).
func TrackerCandidates(doc OperationalDocument) ([]TrackerCandidate, error) {
	if !utf8.Valid(doc.Content) {
		return nil, fmt.Errorf("operational tracker scan: %s is not UTF-8; outputs withheld; restore valid source and regenerate", doc.File)
	}
	var out []TrackerCandidate
	for _, match := range trackerWord.FindAllIndex(doc.Content, -1) {
		start, stop := match[0], match[1]
		if start > 0 && wordByte(doc.Content[start-1]) || stop < len(doc.Content) && (wordByte(doc.Content[stop]) || doc.Content[stop] == '-') {
			continue
		}
		windowStart, windowStop := lineWindow(doc.Content, start, stop)
		prefix := string(doc.Content[windowStart:start])
		shell := filepath.Ext(doc.File) == ".sh"
		// The suffix starts at the token, not its end, so it includes any
		// token-internal quotes when proving an isolated shell literal inert.
		if shell && !inTrackerHeredoc(doc.Content[:windowStart]) && shellNonExecutable(prefix, string(doc.Content[start:windowStop])) {
			continue
		}
		section, node := "body", "CommandPayload"
		markdown := doc.Markdown || filepath.Ext(doc.File) == ".md"
		if markdown {
			section, node = trackerMarkdownContext(doc.Content, start)
		}
		// An adjacent arrow in prose describes a migration between tools,
		// rather than supplying a command token or an executable operand.
		if markdown && node == "Paragraph" && strings.HasPrefix(string(doc.Content[stop:]), "→") {
			continue
		}
		argument := trackerArgument.FindSubmatchIndex(doc.Content[stop:])
		if argument != nil {
			word := string(doc.Content[stop+argument[2] : stop+argument[3]])
			// A plain table column named "bd task" labels historical task
			// identifiers, not an executable recipe. Code spans, arguments and
			// every other command spelling still pass through the strict gate.
			if markdown && node == "Paragraph" && word == "task" && trackerTaskColumn(doc.Content, windowStart, windowStop) {
				continue
			}
			// Bare mentions of a binary in prose are not command recipes. This
			// narrow syntactic distinction is unavailable inside shell/config.
			if filepath.Ext(doc.File) == ".md" && (node == "Paragraph" || node == "ListItem" || node == "Heading") && proseTrackerMention(prefix, word) {
				continue
			}
			stop += argument[1]
		} else if filepath.Ext(doc.File) == ".md" && node != "CodeSpan" && !strings.HasPrefix(node, "FencedCodeBlock") && node != "CodeBlock" && !commandCue(prefix) {
			continue
		}
		location, err := ir.NewLocation(doc.Owner, doc.File, section, ir.SourceRange{Start: start, Stop: stop})
		if err != nil {
			return nil, err
		}
		windowStart, windowStop = lineWindow(doc.Content, start, stop)
		line := strings.TrimSpace(string(doc.Content[windowStart:windowStop]))
		out = append(out, TrackerCandidate{
			Location: location, Node: node, Window: line, Text: string(doc.Content[start:stop]),
			Reason: "executable or ambiguous command-shaped legacy tracker usage is prohibited",
		})
	}
	return out, nil
}

func wordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

func commandCue(prefix string) bool {
	lower := strings.ToLower(prefix)
	for _, cue := range []string{"run ", "execute ", "using ", "via ", "call ", "invoke ", "use ", "start ", "launch ", "`", "$(", "eval", "-c"} {
		if strings.Contains(lower, cue) {
			return true
		}
	}
	return false
}

func trackerTaskColumn(content []byte, start, stop int) bool {
	line := strings.TrimSpace(string(content[start:stop]))
	if !strings.HasPrefix(line, "|") || strings.ContainsAny(line, "`\\") {
		return false
	}
	found := false
	for _, cell := range strings.Split(line, "|") {
		if strings.TrimSpace(cell) == "bd task" {
			found = true
		}
	}
	if !found || stop >= len(content) {
		return false
	}
	next := strings.TrimSpace(strings.SplitN(string(content[stop:]), "\n", 2)[0])
	// lineWindow stops before the newline; read the following line instead.
	if next == "" {
		next = strings.SplitN(string(content[stop+1:]), "\n", 2)[0]
	}
	for _, cell := range strings.Split(strings.TrimSpace(next), "|") {
		cell = strings.TrimSpace(cell)
		if cell == "" {
			continue
		}
		cell = strings.Trim(cell, ":")
		if len(cell) < 3 || strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return strings.Contains(next, "---")
}

func proseTrackerMention(prefix, next string) bool {
	if strings.TrimSpace(prefix) == "" || commandCue(prefix) {
		return false
	}
	switch next {
	case "and", "or", "is", "was", "are", "were", "binary", "from", "for", "with", "available":
		return true
	default:
		return false
	}
}

// Only an ordinary shell comment or echo/printf literal is a proven inert
// context. Substitution in a double-quoted literal executes; evaluation and
// command-string wrappers are not inert. Prompts in heredocs remain scanned.
func shellNonExecutable(prefix, after string) bool {
	var quote byte
	escaped := false
	commandStart := 0
	for i := 0; i < len(prefix); i++ {
		b := prefix[i]
		if escaped {
			escaped = false
			continue
		}
		if b == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if b == quote {
				quote = 0
			}
			continue
		}
		switch b {
		case '\'', '"':
			quote = b
		case '#':
			if i == 0 || prefix[i-1] == ' ' || prefix[i-1] == '\t' {
				return true
			}
		case ';', '|', '&':
			commandStart = i + 1
		}
	}
	command := strings.TrimSpace(prefix[commandStart:])
	if quote != '\'' || !(strings.HasPrefix(command, "echo '") || strings.HasPrefix(command, "printf '")) {
		return false
	}
	// An echo/printf string handed to a shell (or staged into a later recipe)
	// is executable, not a quotation. Only a complete isolated literal output
	// command is proven inert; unknown consumers and suffixes reject.
	closing := strings.IndexByte(after, '\'')
	return closing >= 0 && strings.TrimSpace(after[closing+1:]) == ""
}

func inTrackerHeredoc(before []byte) bool {
	delimiter := ""
	for _, line := range strings.Split(string(before), "\n") {
		if delimiter != "" {
			if strings.TrimSpace(line) == delimiter {
				delimiter = ""
			}
			continue
		}
		if match := trackerHeredoc.FindStringSubmatch(line); match != nil {
			delimiter = match[1]
		}
	}
	return delimiter != ""
}

func trackerMarkdownContext(source []byte, offset int) (string, string) {
	section, label := "body", "Markdown"
	tree := newGoldmarkParser().Parser().Parse(text.NewReader(source))
	_ = ast.Walk(tree, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if heading, ok := node.(*ast.Heading); ok && heading.Lines().Len() > 0 && heading.Lines().At(0).Start <= offset {
			section = headingText(heading, source)
		}
		if value, ok := node.(*ast.Text); ok && value.Segment.Start <= offset && offset < value.Segment.Stop {
			label = nearestContainerLabel(node)
		}
		if node.Type() == ast.TypeBlock && node.Lines().Len() > 0 {
			lines := node.Lines()
			if lines.At(0).Start <= offset && offset < lines.At(lines.Len()-1).Stop {
				switch node.(type) {
				case *ast.FencedCodeBlock, *ast.CodeBlock:
					label = blockLabel(node, source)
				case *ast.HTMLBlock:
					label = "HTMLBlock"
				}
			}
		}
		return ast.WalkContinue, nil
	})
	return section, label
}

// TrackerDigest binds historical framing to exact content, not a whole-file
// exemption: every command still needs its own owner/section/range/window row.
func TrackerDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func historicalTrackerMatch(doc OperationalDocument, candidate TrackerCandidate, entry HistoricalTrackerEntry) bool {
	loc := candidate.Location
	framed := bytes.Contains(doc.Content, []byte("non-operational quotation")) || bytes.Contains(doc.Content, []byte("Historical only:"))
	return framed && entry.Owner == doc.Owner && entry.Section == loc.Section() && entry.Node == candidate.Node &&
		entry.Range == loc.Range() && entry.Window == candidate.Window && entry.Digest == TrackerDigest(doc.Content)
}

// ValidateOperationalDocuments is the unconditional publication predicate.
// Native-call classifications and runtime-literal tags are intentionally not
// inputs. A historical entry authorizes only a visibly framed exact quotation.
func ValidateOperationalDocuments(docs []OperationalDocument, history []HistoricalTrackerEntry, step string) error {
	for _, doc := range docs {
		candidates, err := TrackerCandidates(doc)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			allowed := false
			for _, entry := range history {
				if historicalTrackerMatch(doc, candidate, entry) {
					allowed = true
					break
				}
			}
			if !allowed {
				loc := candidate.Location
				return fmt.Errorf("operational tracker gate rejected %q — why: %s; owner %s; file %s; section %q; node %s; range [%d,%d); text %q; step %s; all stage outputs withheld with no partial output; migrate the canonical source to supported pasture task commands and regenerate with make generate",
					candidate.Window, candidate.Reason, loc.Owner(), loc.File(), loc.Section(), candidate.Node,
					loc.Range().Start, loc.Range().Stop, candidate.Text, step)
			}
		}
		for _, entry := range history {
			if entry.Owner != doc.Owner {
				continue
			}
			matched := false
			for _, candidate := range candidates {
				if historicalTrackerMatch(doc, candidate, entry) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("operational tracker historical identity changed — owner %s; file %s; section %q; node %s; range [%d,%d); text %q; why: a pinned quotation or its framing/content digest no longer matches; step %s; all stage outputs withheld with no partial output; migrate current commands, review the exact historical entry in internal/codegen/scan/manifest/operational_history.json, and regenerate",
					entry.Owner, doc.File, entry.Section, entry.Node, entry.Range.Start, entry.Range.Stop, entry.Window, step)
			}
		}
	}
	return nil
}
