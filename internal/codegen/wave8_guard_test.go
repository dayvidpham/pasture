package codegen

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var retiredMechanism = regexp.MustCompile(`(aura-swarm|\bTeamCreate\b|\bbeads\b|\.git/\.aura|\.beads/)`)

var taxonomyToken = regexp.MustCompile(`\b(?:SLICE-[0-9]+(?:-[0-9]+)*|WAVE-[0-9]+|p[0-9]+-(?:review|impl)(?:-[a-z]+)*)\b`)
var registryLabel = regexp.MustCompile("`pasture:p[0-9]+-[a-z0-9:-]+`")
var phaseRegistryLine = regexp.MustCompile(`^\s*(?:#{1,6}\s*)?` + "`?" + `p[0-9]+-[a-z0-9-]+`)

// Table rows, backticked label values, and phase registry lines are registry
// uses; other text is narrative, including text adjacent to inline labels.
func findInternalTaxonomyTokens(path, body string) []string {
	var findings []string
	for n, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "|") || phaseRegistryLine.MatchString(line) || strings.HasPrefix(trimmed, "**Role:**") && strings.Contains(line, "**Phases owned:**") {
			continue
		}
		line = registryLabel.ReplaceAllString(line, "")
		for _, token := range taxonomyToken.FindAllString(line, -1) {
			findings = append(findings, fmt.Sprintf("%s:%d: internal taxonomy %q in prose; why: readers should not need internal labels; where: narrative; fix: use a plain-language description", path, n+1, token))
		}
	}
	return findings
}

func TestInternalTaxonomyTokens(t *testing.T) {
	for _, sample := range []struct {
		name, text string
		bad        bool
	}{
		{"narrative", "Review SLICE-1 now.", true},
		{"registry", "| `pasture:p10-impl:s10-review` |\n## p10-code-review\nUse `pasture:p10-impl:s10-review`.", false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			got := findInternalTaxonomyTokens(sample.name, sample.text)
			if (len(got) > 0) != sample.bad {
				t.Fatal(got)
			}
		})
	}
	for _, root := range append(append([]string{}, renderedSkillRoots...), ".codex/agents") {
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
			for _, finding := range findInternalTaxonomyTokens(path, string(data)) {
				t.Error(finding)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

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
	// Only a body that discusses dispositions is held to the full set: a body
	// that merely says "deferred" is not describing the disposition model.
	if lower := strings.ToLower(guidance); strings.Contains(lower, "fix-now") || strings.Contains(lower, "won't-do") || strings.Contains(lower, "disposition") {
		for _, value := range []disposition{fixNow, deferItem, wontDo} {
			if !value.valid() || !strings.Contains(guidance, string(value)) {
				findings = append(findings, fmt.Sprintf("disposition %q missing; why: a body that names a disposition must name the closed set; where: %s; fix: render all three from the registry", value, guidanceSource(guidance)))
			}
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
	for _, root := range wave8Roots {
		dir := filepath.Join("..", "..", root)
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(path) != ".md" {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, finding := range findDispositionEnumIncludesWontDo(nil, string(data)) {
				t.Error(path, finding)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func findQuestionStimulusFields(where, guidance string) []string {
	var findings []string
	lower := strings.ToLower(guidance)
	// The fields come from the one registry, so this guard cannot drift from the
	// guidance that renders it.
	for _, field := range QuestionRecordFields {
		if !strings.Contains(lower, strings.ToLower(field.Phrase)) {
			findings = append(findings, fmt.Sprintf("%s: missing %q (%s); why: the user's stimulus cannot be reconstructed; fix: record this field", where, field.Phrase, field.Name))
		}
	}
	return findings
}

// wave8Roots are the rendered trees a shipped instruction can reach.
var wave8Roots = []string{"skills", ".agents/skills", ".opencode/skill", "internal/target/opencode/assets/skills", "internal/target/claudecode/assets/pasture-skills/skills"}

func TestQuestionStimulusFields(t *testing.T) {
	var parts []string
	for _, field := range QuestionRecordFields {
		parts = append(parts, field.Phrase)
	}
	clean := strings.Join(parts, " ")
	for _, sample := range []struct {
		name, text string
		bad        bool
	}{
		{"missing preview", strings.Join(parts[:len(parts)-1], " "), true},
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

// findUserGateList checks the bracketed enumeration wherever a body lists it.
func findUserGateList(path, body string, gates []userGate) []string {
	var entries []string
	for _, gate := range gates {
		entries = append(entries, string(gate.phase)+" ("+gate.purpose+")")
	}
	expected := strings.Join(entries, "; ")
	var findings []string
	for _, line := range strings.Split(body, "\n") {
		if start := strings.Index(line, "User gates: ["); start >= 0 {
			value := line[start+len("User gates: ["):]
			end := strings.Index(value, "]")
			if end < 0 || value[:end] != expected {
				findings = append(findings, fmt.Sprintf("%s: user gate list differs; why: gate order or membership drifted; where: gate enumeration; fix: render the canonical list", path))
			}
		}
		if strings.Contains(line, "user-gated phases are:") || strings.Contains(line, "user-gated phases only:") {
			findings = append(findings, fmt.Sprintf("%s: duplicate gate list; why: it can drift; where: body; fix: render the canonical list", path))
		}
	}
	return findings
}

func TestUserGateList(t *testing.T) {
	good := renderUserGates()
	bad := strings.Replace(good, "; ", "; extra-gate; ", 1)
	if got := findUserGateList("defective", bad, userGates); len(got) == 0 {
		t.Fatal("extra gate accepted")
	}
	if got := findUserGateList("clean", good, userGates); len(got) != 0 {
		t.Fatal(got)
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(good, "User gates: ["), "]"), "; ")
	parts[0], parts[1] = parts[1], parts[0]
	if got := findUserGateList("reordered", "User gates: ["+strings.Join(parts, "; ")+"]", userGates); len(got) == 0 {
		t.Fatal("reordered gates accepted")
	}
	for _, root := range renderedSkillRoots {
		err := filepath.WalkDir(filepath.Join("..", "..", root), func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, finding := range findUserGateList(path, string(data), userGates) {
				t.Error(finding)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// The handoff skill owns the create recipe; architect is only a pointer.
func findHandoffExportStep(architect, handoff string) []string {
	var findings []string
	create := regexp.MustCompile(`(?s)(?:\[\[rec:create\]\]|\$\{?EPOCH_REC\}?"?\s+create).*?--phase[= ]+handoff`)
	if create.MatchString(architect) {
		findings = append(findings, "handoff create recipe is duplicated; why: recipes can drift; where: architect skill; fix: invoke architect-handoff instead")
	}
	if !strings.Contains(architect, "architect-handoff") {
		findings = append(findings, "handoff pointer missing; why: the procedure cannot be found; where: architect skill; fix: invoke architect-handoff")
	}
	if !create.MatchString(handoff) {
		findings = append(findings, "handoff create recipe missing; why: no canonical creation step exists; where: architect-handoff; fix: restore the create step")
	}
	return findings
}

func TestHandoffExportStep(t *testing.T) {
	owner := `HANDOFF_ID_URI=$([[rec:create]] --phase handoff)`
	if got := findHandoffExportStep("architect-handoff\n"+owner, owner); len(got) == 0 {
		t.Fatal("duplicate recipe accepted")
	}
	if got := findHandoffExportStep("Invoke architect-handoff", owner); len(got) != 0 {
		t.Fatal(got)
	}
	for _, root := range []string{"skills", ".agents/skills", ".opencode/skill", "internal/target/opencode/assets/skills", "internal/target/claudecode/assets/pasture-skills/skills"} {
		a, err := os.ReadFile(filepath.Join("..", "..", root, "architect", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		h, err := os.ReadFile(filepath.Join("..", "..", root, "architect-handoff", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range findHandoffExportStep(string(a), string(h)) {
			t.Error(root, finding)
		}
	}
}

// wantUserGates is written independently of the registry: the phase a gate
// stops at is a workflow fact, and a guard that reads it from the same list it
// checks cannot see the list change.
var wantUserGates = []struct {
	Phase   string
	Purpose string
}{
	{"request", "p1 research depth"},
	{"elicit", "p2 requirements interview"},
	{"plan-review", "p5 plan UAT"},
	{"impl-plan", "p8 implementation effort and review budget"},
	{"impl-uat", "p11 implementation UAT"},
}

// TestUserGateListMatchesTheWorkflow pins the gate set and its order, so a gate
// moved to a neighbouring phase fails even though the registry renders whatever
// it is told.
func TestUserGateListMatchesTheWorkflow(t *testing.T) {
	require.Len(t, userGates, len(wantUserGates), "the gate count must match the pinned workflow")
	for i, want := range wantUserGates {
		assert.Equal(t, want.Phase, string(userGates[i].phase), "gate %d phase", i)
		assert.Equal(t, want.Purpose, userGates[i].purpose, "gate %d purpose", i)
	}
}

// guidanceSource names the input for a finding message without threading a path
// through the pure core.
func guidanceSource(guidance string) string {
	if len(guidance) > 60 {
		return guidance[:60] + "..."
	}
	return guidance
}
