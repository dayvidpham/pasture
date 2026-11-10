package codegen

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var renderedSkillRoots = []string{"skills", ".agents/skills", ".opencode/skill", "internal/target/opencode/assets/skills", "internal/target/claudecode/assets/pasture-skills/skills"}
var explicitAnchor = regexp.MustCompile(`<a\s+id=["']([^"']+)["']`)

func headingSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r), r == '_', r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

func markdownAnchors(data []byte) map[string]bool {
	anchors := map[string]bool{}
	doc := goldmark.DefaultParser().Parse(text.NewReader(data))
	counts := map[string]int{}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if h, ok := n.(*ast.Heading); ok {
				slug := headingSlug(string(h.Text(data)))
				count := counts[slug]
				counts[slug]++
				if count > 0 {
					slug = fmt.Sprintf("%s-%d", slug, count)
				}
				anchors[slug] = true
			}
		}
		return ast.WalkContinue, nil
	})
	for _, m := range explicitAnchor.FindAllSubmatch(data, -1) {
		anchors[string(m[1])] = true
	}
	return anchors
}

func checkMarkdownLinks(source string, data []byte) []string {
	var findings []string
	doc := goldmark.DefaultParser().Parse(text.NewReader(data))
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		link, ok := n.(*ast.Link)
		if !ok {
			return ast.WalkContinue, nil
		}
		dest := string(link.Destination)
		u, err := url.Parse(dest)
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s: invalid link %q: %v", source, dest, err))
			return ast.WalkContinue, nil
		}
		if u.IsAbs() || u.Host != "" || strings.HasPrefix(u.Path, "/") {
			return ast.WalkContinue, nil
		}
		target := source
		if u.Path != "" {
			target = filepath.Join(filepath.Dir(source), filepath.FromSlash(u.Path))
		}
		content, err := os.ReadFile(target)
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s: link %q missing target %s: %v", source, dest, target, err))
			return ast.WalkContinue, nil
		}
		if u.Fragment != "" && !markdownAnchors(content)[u.Fragment] {
			findings = append(findings, fmt.Sprintf("%s: link %q missing target %s#%s", source, dest, target, u.Fragment))
		}
		return ast.WalkContinue, nil
	})
	return findings
}

// A rule or ownership mapping, not a title or a navigation-only pointer.
var documentRule = regexp.MustCompile(`(?i)\b(must|never|do not|does not|owns?|owned|closes?|needs?|applies|advances|renders?|covers?|is not|is an? (?:index|rule)|use|read)\b`)

func documentHasContent(data []byte) bool {
	lines := strings.Split(string(data), "\n")
	inTable := false
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "|") && strings.Contains(s, "---") {
			inTable = true
			continue
		}
		if inTable && strings.HasPrefix(s, "|") && strings.Count(s, "|") >= 3 {
			return true
		}
		inTable = false
		if strings.HasPrefix(s, "- ") && documentRule.MatchString(s) {
			return true
		}
		if !strings.HasPrefix(s, "#") && !strings.HasPrefix(s, "[") && documentRule.MatchString(s) && !strings.Contains(s, "](") && len(strings.Fields(s)) >= 6 {
			return true
		}
	}
	return false
}

func TestRenderedMarkdownLinksResolve(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, tree := range renderedSkillRoots {
		t.Run(tree, func(t *testing.T) {
			err := filepath.WalkDir(filepath.Join(root, tree), func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() || filepath.Ext(path) != ".md" {
					return nil
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				for _, f := range checkMarkdownLinks(path, data) {
					t.Error(f)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestMarkdownLinkGuardSamples(t *testing.T) {
	root := t.TempDir()
	protocol := filepath.Join(root, "protocol")
	if err := os.Mkdir(protocol, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(protocol, "PROCESS.md")
	if err := os.WriteFile(target, []byte("# Workflow\n\n## Existing heading\n"), 0644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "SKILL.md")
	if got := checkMarkdownLinks(source, []byte("[old](protocol/PROCESS.md#deleted-heading)")); len(got) == 0 {
		t.Fatal("deleted heading was accepted")
	} else {
		t.Log(got[0])
	}
	if got := checkMarkdownLinks(source, []byte("[ok](protocol/PROCESS.md#existing-heading)")); len(got) != 0 {
		t.Fatal(got)
	}
}
func TestSurvivingProtocolDocumentsHaveContent(t *testing.T) {
	for _, tree := range renderedSkillRoots {
		for _, name := range []string{"AGENTS.md", "CLAUDE.md", "CONSTRAINTS.md", "MR_TEMPLATE.md", "PROCESS.md", "README.md", "SKILL.md", "SKILLS.md"} {
			path := filepath.Join("..", "..", tree, "protocol", name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Error(err)
				continue
			}
			if !documentHasContent(data) {
				t.Errorf("%s: document has no rule or ownership mapping beyond its pointer; restore substantive content", path)
			}
		}
	}
}
func TestProtocolContentGuardSamples(t *testing.T) {
	if documentHasContent([]byte("# Protocol\n\nSee the generated skill.\n")) {
		t.Fatal("pointer-only document was accepted")
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "skills", "protocol", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !documentHasContent(data) {
		t.Fatal("shipped document lacks content")
	}
}
