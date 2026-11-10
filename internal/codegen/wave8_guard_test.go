package codegen

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var retiredMechanism = regexp.MustCompile(`(aura-swarm|\bTeamCreate\b|\bbeads\b|\.git/\.aura|\.beads/)`)

// Capture fixtures contain authentic host bytes, not generated guidance.
func findRetiredMechanismNames(path, body string) []string {
	if strings.Contains("/"+filepath.ToSlash(path)+"/", "/testdata/fixtures/") {
		return nil
	}
	var findings []string
	for n, line := range strings.Split(body, "\n") {
		for _, match := range retiredMechanism.FindAllString(line, -1) {
			findings = append(findings, fmt.Sprintf("%s:%d: retired mechanism %q appears in shipped text; why: it directs readers to a retired mechanism; fix: replace it with the current operation", path, n+1, match))
		}
	}
	return findings
}

func TestRetiredMechanismNames(t *testing.T) {
	if got := findRetiredMechanismNames("sample.md", "Run aura-swarm."); len(got) == 0 {
		t.Fatal("retired mechanism was accepted")
	}
	if got := findRetiredMechanismNames("sample.md", "Use the tracker operation."); len(got) != 0 {
		t.Fatal(got)
	}
	roots := append(append([]string{}, renderedSkillRoots...), ".codex/agents")
	for _, root := range roots {
		path := filepath.Join("..", "..", root)
		err := filepath.WalkDir(path, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, finding := range findRetiredMechanismNames(path, string(data)) {
				t.Error(finding)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

var stubLine = regexp.MustCompile(`STUB\(([^)]*)\)\s*->\s*(\S*)`)
var stubSuccessor = regexp.MustCompile(`^(?:#[0-9]+|[A-Z]+-[0-9]+|https?://\S+)$`)
var stubSlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// findStubSuccessorResolves checks the proposal's syntactic successor grammar;
// it does not claim that an external issue or task exists.
func findStubSuccessorResolves(path, body string) []string {
	var findings []string
	for n, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, "STUB(") {
			continue
		}
		m := stubLine.FindStringSubmatch(line)
		if len(m) == 0 || !stubSlug.MatchString(m[1]) || !stubSuccessor.MatchString(m[2]) {
			findings = append(findings, fmt.Sprintf("%s:%d: stub has no valid successor; why: an untracked stub can be forgotten; fix: use STUB(slug) -> #issue, TRACKER-123, or an issue/task URL", path, n+1))
		}
	}
	return findings
}

func TestStubSuccessorResolves(t *testing.T) {
	if got := findStubSuccessorResolves("sample", "STUB(parser) -> TBD"); len(got) == 0 {
		t.Fatal("unresolved stub was accepted")
	}
	if got := findStubSuccessorResolves("sample", "STUB(parser) -> #123"); len(got) != 0 {
		t.Fatal(got)
	}
	for _, root := range renderedSkillRoots {
		err := filepath.WalkDir(filepath.Join("..", "..", root), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, finding := range findStubSuccessorResolves(path, string(data)) {
				t.Error(finding)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// findDispositionEnumIncludesWontDo validates values and generated guidance.
func findDispositionEnumIncludesWontDo(values []disposition, guidance string) []string {
	var findings []string
	for _, value := range values {
		if !value.valid() {
			findings = append(findings, fmt.Sprintf("disposition %q is unregistered; why: feedback must have a closed disposition; where: UAT; fix: use a registered disposition", value))
		}
	}
	for _, value := range []disposition{fixNow, deferItem, wontDo} {
		if !value.valid() || !strings.Contains(guidance, string(value)) {
			findings = append(findings, fmt.Sprintf("disposition %q missing; why: users need all decisions; where: UAT guidance; fix: register and render it", value))
		}
	}
	return findings
}

func TestDispositionEnumIncludesWontDo(t *testing.T) {
	guidance := dispositionNames()
	for _, sample := range []struct {
		name   string
		values []disposition
		bad    bool
	}{
		{"invented", []disposition{"INVENTED"}, true},
		{"canonical", []disposition{fixNow, deferItem, wontDo}, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			got := findDispositionEnumIncludesWontDo(sample.values, guidance)
			if (len(got) > 0) != sample.bad {
				t.Fatal(got)
			}
		})
	}
	for _, root := range []string{"skills", ".agents/skills", ".opencode/skill", "internal/target/opencode/assets/skills", "internal/target/claudecode/assets/pasture-skills/skills"} {
		path := filepath.Join("..", "..", root, "user-uat", "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range findDispositionEnumIncludesWontDo(nil, string(data)) {
			t.Error(path, finding)
		}
	}
}

func findQuestionStimulusFields(where, guidance string) []string {
	var findings []string
	for _, field := range []string{"header", "question text", "option label", "description", "multi-select", "preview"} {
		if !strings.Contains(strings.ToLower(guidance), field) {
			findings = append(findings, fmt.Sprintf("%s: missing %s; why: the user's stimulus cannot be reconstructed; fix: record this field", where, field))
		}
	}
	return findings
}

func TestQuestionStimulusFields(t *testing.T) {
	clean := "header question text option label description multi-select preview"
	for _, sample := range []struct {
		name, text string
		bad        bool
	}{
		{"missing preview", strings.TrimSuffix(clean, " preview"), true},
		{"complete", clean, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			got := findQuestionStimulusFields(sample.name, sample.text)
			if (len(got) > 0) != sample.bad {
				t.Fatal(got)
			}
		})
	}
	for _, finding := range findQuestionStimulusFields("C-ure-verbatim", ConstraintSpecs["C-ure-verbatim"].Then) {
		t.Error(finding)
	}
}
