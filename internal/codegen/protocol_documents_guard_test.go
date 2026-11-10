package codegen

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var renderedSkillRoots = []string{"skills", ".agents/skills", ".opencode/skill", "internal/target/opencode/assets/skills", "internal/target/claudecode/assets/pasture-skills/skills", "agents", ".opencode/agent", "internal/target/opencode/assets/agents", "internal/target/claudecode/assets/pasture-agents/agents"}
var explicitAnchor = regexp.MustCompile(`<a\s+id=["']([^"']+)["']`)
var markdownLink = regexp.MustCompile(`\]\(([^)]+)\)`)

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

// requiredProtocolLinks names the links a surviving protocol document must
// carry. Every set is derived from a registry rather than written here, so the
// assertion moves when the registry moves and a document reduced to "see the
// skill" cannot satisfy it. A verb list would: the point is structure, not
// wording.
func requiredProtocolLinks(root, tree, name string) []string {
	switch name {
	case "AGENTS.md":
		roles := make([]string, 0, len(RoleSpecs))
		for _, spec := range RoleSpecs {
			roles = append(roles, strings.ToLower(spec.Name))
		}
		sort.Strings(roles)
		links := make([]string, 0, len(roles))
		for _, role := range roles {
			links = append(links, "../"+role+"/SKILL.md")
		}
		return links
	case "SKILLS.md":
		entries, err := os.ReadDir(filepath.Join(root, tree))
		if err != nil {
			return nil
		}
		var links []string
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == "protocol" {
				continue
			}
			links = append(links, "../"+entry.Name()+"/SKILL.md")
		}
		sort.Strings(links)
		return links
	case "SKILL.md":
		entries, err := os.ReadDir(filepath.Join(root, tree, "protocol"))
		if err != nil {
			return nil
		}
		var links []string
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" || entry.Name() == "SKILL.md" {
				continue
			}
			links = append(links, entry.Name())
		}
		sort.Strings(links)
		return links
	case "CONSTRAINTS.md":
		links := make([]string, 0, len(WritingStandardCarriers))
		for _, skill := range WritingStandardCarriers {
			links = append(links, "../"+skill+"/SKILL.md")
		}
		return links
	case "CLAUDE.md":
		// The directive points at the orchestrator role. The name comes from the
		// role registry, so a renamed role cannot leave a stale link here.
		for _, spec := range RoleSpecs {
			if len(spec.OwnedPhases) == len(requiredPhases()) {
				return []string{"../" + strings.ToLower(spec.Name) + "/SKILL.md"}
			}
		}
		return nil
	}
	return nil
}

// requiredPhases is the phase set the role registry declares.
func requiredPhases() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, spec := range RoleSpecs {
		for _, phase := range spec.OwnedPhases {
			if _, ok := seen[string(phase)]; ok {
				continue
			}
			seen[string(phase)] = struct{}{}
			out = append(out, string(phase))
		}
	}
	sort.Strings(out)
	return out
}

// requiredPhaseAnchors is the number of phases the role registry declares. The
// phase document keeps one anchor per phase so an inbound link has a landing
// place; the number comes from the registry, never from a literal.
func requiredPhaseAnchors() int {
	return len(requiredPhases())
}

// protocolDocumentFindings reports a surviving document that lacks the
// structure its own name and its registry promise.
func protocolDocumentFindings(root, tree, name string, data []byte) []string {
	text := string(data)
	var findings []string
	for _, want := range requiredProtocolLinks(root, tree, name) {
		if !strings.Contains(text, "]("+want+")") {
			findings = append(findings, fmt.Sprintf(
				"%s/%s: missing link %q — the document must map its registry entry to the owning skill; "+
					"why: a template, role or skill that the registry declares has no destination here; "+
					"where: internal/codegen/protocol_documents_guard_test.go requiredProtocolLinks; "+
					"fix: add the row, or remove the entry from the registry if the skill is gone",
				tree, name, want))
		}
	}
	if name == "CONSTRAINTS.md" {
		findings = append(findings, constraintRowFindings(text, tree)...)
	}
	switch name {
	case "PROCESS.md":
		want := requiredPhaseAnchors()
		if got := strings.Count(text, `<a id="phase-`); got < want {
			findings = append(findings, fmt.Sprintf(
				"%s/PROCESS.md: %d phase anchors, the phase registry declares %d — a phase without an anchor has no landing place for an inbound link",
				tree, got, want))
		}
	case "MR_TEMPLATE.md":
		if !strings.Contains(text, "\n- ") {
			findings = append(findings, fmt.Sprintf(
				"%s/MR_TEMPLATE.md: no field list — a template must name the fields it asks the author to fill", tree))
		}
	}
	return findings
}

// backtickedCitation matches a path-shaped file citation written in prose, the
// form the link walk cannot see. A bare file name is skipped: names such as
// EPOCH.md are runtime artefacts the user's project carries, not shipped files.
var backtickedCitation = regexp.MustCompile("`([A-Za-z0-9_.][A-Za-z0-9_./-]*\\.md)(#[A-Za-z0-9_-]+)?`")

func checkBacktickedCitations(source string, data []byte, root string) []string {
	var findings []string
	for _, match := range backtickedCitation.FindAllStringSubmatch(string(data), -1) {
		target, fragment := match[1], strings.TrimPrefix(match[2], "#")
		if !strings.Contains(target, "/") {
			continue
		}
		// .agents.local is the one directory the epoch writes into the user's
		// project. Every other dotted root here is a shipped tree
		// (.agents/, .opencode/, .codex/), so its citations are checked.
		if head := strings.SplitN(target, "/", 2)[0]; head == ".agents.local" {
			continue
		}
		resolved := ""
		for _, base := range []string{filepath.Dir(source), root} {
			candidate := filepath.Join(base, filepath.FromSlash(target))
			if _, err := os.Stat(candidate); err == nil {
				resolved = candidate
				break
			}
		}
		if resolved == "" {
			findings = append(findings, fmt.Sprintf(
				"%s: citation %q names no file — why: a reader following it finds nothing; "+
					"where: internal/codegen/protocol_documents_guard_test.go checkBacktickedCitations; "+
					"fix: cite the file that holds the rule, or drop the citation", source, target))
			continue
		}
		if fragment == "" {
			continue
		}
		content, err := os.ReadFile(resolved)
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s: citation %q unreadable: %v", source, target, err))
			continue
		}
		if !markdownAnchors(content)[fragment] {
			findings = append(findings, fmt.Sprintf(
				"%s: citation %q names no heading %q in %s", source, target, fragment, resolved))
		}
	}
	return findings
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
func protocolTreeRoots(root string) []string {
	var trees []string
	for _, tree := range renderedSkillRoots {
		if info, err := os.Stat(filepath.Join(root, tree, "protocol")); err == nil && info.IsDir() {
			trees = append(trees, tree)
		}
	}
	return trees
}

func TestSurvivingProtocolDocumentsCarryTheirRegistryStructure(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, tree := range protocolTreeRoots(root) {
		dir := filepath.Join(root, tree, "protocol")
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Error(err)
				continue
			}
			for _, finding := range protocolDocumentFindings(root, tree, entry.Name(), data) {
				t.Error(finding)
			}
		}
	}
}

func TestProtocolDocumentGuardSamples(t *testing.T) {
	root := t.TempDir()
	tree := "skills"
	if err := os.MkdirAll(filepath.Join(root, tree, "epoch"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, tree, "epoch", "SKILL.md"), []byte("# Epoch\n"), 0644); err != nil {
		t.Fatal(err)
	}

	stub := []byte("# Roles\n\n- Use the generated role skills.\n")
	if got := protocolDocumentFindings(root, tree, "AGENTS.md", stub); len(got) == 0 {
		t.Fatal("a role index reduced to one bullet was accepted; the guard must require a row per registry role")
	} else {
		t.Log(got[0])
	}

	shipped, err := os.ReadFile(filepath.Join("..", "..", "skills", "protocol", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := protocolDocumentFindings(filepath.Join("..", ".."), "skills", "AGENTS.md", shipped); len(got) != 0 {
		t.Fatalf("the shipped role index was rejected: %v", got)
	}

	emptyProcess := []byte("# Workflow\n\nSee the generated skill.\n")
	if got := protocolDocumentFindings(root, tree, "PROCESS.md", emptyProcess); len(got) == 0 {
		t.Fatal("a phase document with no anchors was accepted")
	}
}

func TestBacktickedCitationsResolve(t *testing.T) {
	root := t.TempDir()
	keeper := filepath.Join(root, "skills", "keeper")
	if err := os.MkdirAll(keeper, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keeper, "SKILL.md"), []byte("# Keeper\n\n## Fields\n"), 0644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "skills", "epoch", "SKILL.md")

	if got := checkBacktickedCitations(source, []byte("See `../gone/SKILL.md` for the fields."), root); len(got) == 0 {
		t.Fatal("a citation of a file that does not exist was accepted")
	} else {
		t.Log(got[0])
	}
	if got := checkBacktickedCitations(source, []byte("See `../keeper/SKILL.md` for the fields."), root); len(got) != 0 {
		t.Fatalf("a resolvable citation was rejected: %v", got)
	}
	if got := checkBacktickedCitations(source, []byte("Write `EPOCH.md` at the project root."), root); len(got) != 0 {
		t.Fatalf("a runtime artefact name was treated as a shipped citation: %v", got)
	}
	if got := checkBacktickedCitations(source, []byte("See `../keeper/SKILL.md#gone`."), root); len(got) == 0 {
		t.Fatal("a citation of a missing heading was accepted")
	}
}

func TestRenderedBacktickedCitationsResolve(t *testing.T) {
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
				for _, finding := range checkBacktickedCitations(path, data, root) {
					t.Error(finding)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}


// constraintRowFindings checks that the writing row maps to exactly the skills
// that carry the writing fragment. Presence alone would let a wrong owner return
// beside the right ones.
func constraintRowFindings(text, tree string) []string {
	var row string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Writing to the user") {
			row = line
			break
		}
	}
	if row == "" {
		return []string{tree + "/CONSTRAINTS.md: no row maps 'Writing to the user' to an owner"}
	}
	got := map[string]struct{}{}
	for _, match := range markdownLink.FindAllStringSubmatch(row, -1) {
		got[match[1]] = struct{}{}
	}
	want := map[string]struct{}{}
	for _, skill := range WritingStandardCarriers {
		want["../"+skill+"/SKILL.md"] = struct{}{}
	}
	var findings []string
	for link := range want {
		if _, ok := got[link]; !ok {
			findings = append(findings, tree+"/CONSTRAINTS.md: writing row omits "+link)
		}
	}
	for link := range got {
		if _, ok := want[link]; !ok {
			findings = append(findings, tree+"/CONSTRAINTS.md: writing row names "+link+", which carries no writing section; fix: remove it, because a reader sent there finds no rule")
		}
	}
	return findings
}
