package codegen

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
	"gopkg.in/yaml.v3"
)

// openCodeAgentFrontmatter captures the keys an OpenCode agent .md may carry in
// its YAML frontmatter. permission is a nested map; the deprecated boolean
// tools: map must be ABSENT (assertNoToolsKey enforces that on the raw text).
type openCodeAgentFrontmatter struct {
	Description string            `yaml:"description"`
	Mode        string            `yaml:"mode"`
	Model       string            `yaml:"model"`
	Permission  map[string]string `yaml:"permission"`
}

// emitOpenCodeAgents renders the OpenCode harness into an isolated temp tree and
// returns the emitted .opencode/agent/<role>.md files keyed by role id.
func emitOpenCodeAgents(t *testing.T) map[protocol.RoleId]GeneratedFile {
	t.Helper()

	root := testModuleRoot(t)
	figuresDir := filepath.Join(root, "skills", "protocol", "figures")
	out := t.TempDir()

	files, err := openCodeAgentEmitter{}.Emit(out, figuresDir, GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("openCodeAgentEmitter.Emit: %v", err)
	}

	agentRoot := filepath.Join(out, ".opencode", "agent")
	byRole := make(map[protocol.RoleId]GeneratedFile)
	for _, f := range files {
		rel, err := filepath.Rel(agentRoot, f.Path)
		if err != nil || strings.Contains(rel, string(filepath.Separator)) {
			t.Fatalf("emitted agent file outside %q: %q", agentRoot, f.Path)
		}
		name := strings.TrimSuffix(rel, ".md")
		byRole[protocol.RoleId(name)] = f
	}
	return byRole
}

// decodeOpenCodeAgent splits the frontmatter from the body and decodes it. It
// also asserts the deprecated boolean tools: key is absent from the raw text.
func decodeOpenCodeAgent(t *testing.T, f GeneratedFile) (openCodeAgentFrontmatter, string) {
	t.Helper()
	fmText, body := splitFrontmatter(t, f.Path, f.Content)

	for _, line := range strings.Split(fmText, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "tools:") {
			t.Errorf("%s: frontmatter must not carry the deprecated boolean `tools:` map: %q", f.Path, line)
		}
	}

	dec := yaml.NewDecoder(strings.NewReader(fmText))
	dec.KnownFields(true)
	var fm openCodeAgentFrontmatter
	if err := dec.Decode(&fm); err != nil {
		t.Fatalf("%s: decode frontmatter: %v\n---\n%s", f.Path, err, fmText)
	}
	return fm, body
}

// TestOpenCodeAgentsEmitForToolRoles asserts the emitter produces exactly one
// agent file per role that has tools — the same role set the Claude agent
// emitter covers (all 5 protocol roles currently have tools).
func TestOpenCodeAgentsEmitForToolRoles(t *testing.T) {
	t.Parallel()

	byRole := emitOpenCodeAgents(t)

	var want []protocol.RoleId
	for roleID, spec := range RoleSpecs {
		if len(spec.Tools) > 0 {
			want = append(want, roleID)
		}
	}
	if len(byRole) != len(want) {
		t.Fatalf("emitted %d OpenCode agents, want %d (one per tool-bearing role)", len(byRole), len(want))
	}
	for _, roleID := range want {
		if _, ok := byRole[roleID]; !ok {
			t.Errorf("missing OpenCode agent for tool-bearing role %q", roleID)
		}
	}
}

// TestOpenCodeAgentArchitectMapping pins the architect (primary, opus, no edit)
// frontmatter contract.
func TestOpenCodeAgentArchitectMapping(t *testing.T) {
	t.Parallel()

	byRole := emitOpenCodeAgents(t)
	f, ok := byRole[protocol.RoleArchitect]
	if !ok {
		t.Fatalf("no OpenCode agent emitted for architect")
	}
	fm, body := decodeOpenCodeAgent(t, f)

	if fm.Mode != "primary" {
		t.Errorf("architect mode = %q, want primary", fm.Mode)
	}
	if fm.Model != "anthropic/claude-opus-4-8" {
		t.Errorf("architect model = %q, want anthropic/claude-opus-4-8", fm.Model)
	}
	if strings.TrimSpace(fm.Description) == "" {
		t.Errorf("architect description is empty")
	}

	// Least-privilege: deny-all seed, then read/glob/grep/bash/skill/task —
	// NO edit (architect has no Edit/Write tool), and SendMessage/Agent omitted.
	if got := fm.Permission["*"]; got != "deny" {
		t.Errorf("architect permission[*] = %q, want deny (deny-all seed)", got)
	}
	for _, perm := range []string{"read", "glob", "grep", "bash", "skill", "task"} {
		if got := fm.Permission[perm]; got != "allow" {
			t.Errorf("architect permission[%s] = %q, want allow", perm, got)
		}
	}
	if _, hasEdit := fm.Permission["edit"]; hasEdit {
		t.Errorf("architect must NOT be granted edit (read-only role), got permission map %v", fm.Permission)
	}
	// Agent + SendMessage have no OpenCode analog and must not leak any key.
	if _, hasWrite := fm.Permission["write"]; hasWrite {
		t.Errorf("architect must not carry a separate write permission, got %v", fm.Permission)
	}
	if len(fm.Permission) != 7 { // "*" + 6 granted
		t.Errorf("architect permission map has %d entries, want 7 (deny seed + 6 grants): %v", len(fm.Permission), fm.Permission)
	}

	// Body reuse: the Claude agent body is wrapped verbatim, so the role's
	// agent H1 heading must appear in the OpenCode body.
	if !strings.Contains(body, "# Architect Agent") {
		t.Errorf("architect OpenCode body missing reused Claude agent body heading '# Architect Agent'")
	}
}

// TestOpenCodeAgentWorkerMapping pins the worker (subagent, sonnet, with edit)
// frontmatter contract.
func TestOpenCodeAgentWorkerMapping(t *testing.T) {
	t.Parallel()

	byRole := emitOpenCodeAgents(t)
	f, ok := byRole[protocol.RoleWorker]
	if !ok {
		t.Fatalf("no OpenCode agent emitted for worker")
	}
	fm, _ := decodeOpenCodeAgent(t, f)

	if fm.Mode != "subagent" {
		t.Errorf("worker mode = %q, want subagent", fm.Mode)
	}
	if fm.Model != "anthropic/claude-sonnet-4-6" {
		t.Errorf("worker model = %q, want anthropic/claude-sonnet-4-6", fm.Model)
	}

	if got := fm.Permission["*"]; got != "deny" {
		t.Errorf("worker permission[*] = %q, want deny (deny-all seed)", got)
	}
	// Worker has Edit+Write tools → edit allowed; plus read/glob/grep/bash/skill.
	for _, perm := range []string{"read", "glob", "grep", "bash", "skill", "edit"} {
		if got := fm.Permission[perm]; got != "allow" {
			t.Errorf("worker permission[%s] = %q, want allow", perm, got)
		}
	}
	// Worker has no Task tool → no task permission.
	if _, hasTask := fm.Permission["task"]; hasTask {
		t.Errorf("worker has no Task tool and must not be granted task permission, got %v", fm.Permission)
	}
}

// TestOpenCodeAgentReviewerSubagent asserts the reviewer is a subagent (it has
// no Agent/Task tool), guarding against a primary misclassification.
func TestOpenCodeAgentReviewerSubagent(t *testing.T) {
	t.Parallel()

	byRole := emitOpenCodeAgents(t)
	f, ok := byRole[protocol.RoleReviewer]
	if !ok {
		t.Fatalf("no OpenCode agent emitted for reviewer")
	}
	fm, _ := decodeOpenCodeAgent(t, f)

	if fm.Mode != "subagent" {
		t.Errorf("reviewer mode = %q, want subagent", fm.Mode)
	}

	if got := fm.Permission["*"]; got != "deny" {
		t.Errorf("reviewer permission[*] = %q, want deny (deny-all seed)", got)
	}
	// Positive grants: reviewer has Read/Glob/Grep/Bash/Skill (SendMessage has no
	// OpenCode analog and is omitted). Mirrors the architect/worker assertions.
	for _, perm := range []string{"read", "glob", "grep", "bash", "skill"} {
		if got := fm.Permission[perm]; got != "allow" {
			t.Errorf("reviewer permission[%s] = %q, want allow", perm, got)
		}
	}
	if _, hasEdit := fm.Permission["edit"]; hasEdit {
		t.Errorf("reviewer is read-only and must not be granted edit, got %v", fm.Permission)
	}
	// Reviewer has no Task tool → no task permission.
	if _, hasTask := fm.Permission["task"]; hasTask {
		t.Errorf("reviewer has no Task tool and must not be granted task permission, got %v", fm.Permission)
	}
}

// TestOpenCodeAgentEveryModelMaps asserts every distinct RoleSpec.Model nickname
// in use resolves through the generator's openCodeModel lookup table (fail-hard
// if a role's model has no mapping).
func TestOpenCodeAgentEveryModelMaps(t *testing.T) {
	t.Parallel()

	for roleID, spec := range RoleSpecs {
		if len(spec.Tools) == 0 {
			continue
		}
		got, ok := openCodeModel[spec.Model]
		if !ok {
			t.Errorf("role %q model nickname %q has no OpenCode model mapping in openCodeModel", roleID, spec.Model)
			continue
		}
		if !strings.HasPrefix(got, "anthropic/") {
			t.Errorf("role %q model %q maps to %q, want an anthropic/<id> qualified id", roleID, spec.Model, got)
		}
	}
}
