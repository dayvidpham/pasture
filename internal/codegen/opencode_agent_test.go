package codegen

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/testutil"
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
		if strings.Contains(name, "--") {
			continue
		}
		byRole[protocol.RoleId(name)] = f
	}
	return byRole
}

type openCodeVariantFixture struct {
	Provider OpenCodeProviderID  `yaml:"provider"`
	Model    OpenCodeModelID     `yaml:"model"`
	Slug     OpenCodeVariantSlug `yaml:"slug"`
}

func (f openCodeVariantFixture) variant() OpenCodeProviderVariant {
	return OpenCodeProviderVariant{Provider: f.Provider, Model: f.Model, Slug: f.Slug}
}

type openCodeVariantInvalidCase struct {
	Name      string                   `yaml:"name"`
	Variants  []openCodeVariantFixture `yaml:"variants"`
	WantError string                   `yaml:"want_error"`
}

type openCodeVariantSuite struct {
	ValidVariants         []openCodeVariantFixture     `yaml:"valid_variants"`
	CrossProviderSameSlug []openCodeVariantFixture     `yaml:"cross_provider_same_slug"`
	InvalidCases          []openCodeVariantInvalidCase `yaml:"invalid_cases"`
}

func normalizeAgentBodyForHarnessParity(t *testing.T, body string) string {
	t.Helper()
	const heading = "## Instruction Sources\n\n"
	start := strings.Index(body, heading)
	if start < 0 {
		t.Fatalf("agent body has no Instruction Sources section:\n%s", body)
	}
	rest := body[start+len(heading):]
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		t.Fatalf("Instruction Sources section has no following semantic section:\n%s", body)
	}
	return body[:start] + rest[end+1:]
}

func loadOpenCodeVariantSuite(t *testing.T) openCodeVariantSuite {
	t.Helper()
	var suite openCodeVariantSuite
	testutil.LoadFixtures(t, testutil.CodegenOpenCodeAgentVariants, &suite)
	return suite
}

func emitOpenCodeAgentFiles(t *testing.T, variants []OpenCodeProviderVariant) []GeneratedFile {
	t.Helper()
	root := testModuleRoot(t)
	figuresDir := filepath.Join(root, "skills", "protocol", "figures")
	files, err := (openCodeAgentEmitter{Variants: variants}).Emit(t.TempDir(), figuresDir, GenerateOptions{})
	if err != nil {
		t.Fatalf("openCodeAgentEmitter.Emit: %v", err)
	}
	return files
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

func TestOpenCodeAgentsEmitLegacyAndUnpinnedDefaults(t *testing.T) {
	t.Parallel()

	files := emitOpenCodeAgentFiles(t, nil)
	byName := make(map[string]GeneratedFile, len(files))
	for _, file := range files {
		byName[filepath.Base(file.Path)] = file
	}

	toolRoleCount := 0
	for roleID, spec := range RoleSpecs {
		if len(spec.Tools) == 0 {
			continue
		}
		toolRoleCount++
		legacy, legacyOK := byName[string(roleID)+".md"]
		defaultFile, defaultOK := byName[string(roleID)+"--default.md"]
		if !legacyOK || !defaultOK {
			t.Errorf("role %q outputs: legacy=%t default=%t", roleID, legacyOK, defaultOK)
			continue
		}

		legacyFrontmatter, legacyBody := decodeOpenCodeAgent(t, legacy)
		defaultFrontmatter, defaultBody := decodeOpenCodeAgent(t, defaultFile)
		if defaultFrontmatter.Model != "" {
			t.Errorf("%s default model = %q, want omitted", roleID, defaultFrontmatter.Model)
		}
		if strings.Contains(strings.Split(defaultFile.Content, "---\n")[1], "model:") {
			t.Errorf("%s default frontmatter contains a model key", roleID)
		}
		if defaultFrontmatter.Mode != legacyFrontmatter.Mode || defaultFrontmatter.Description != legacyFrontmatter.Description {
			t.Errorf("%s default changed mode/description from legacy", roleID)
		}
		if !reflect.DeepEqual(defaultFrontmatter.Permission, legacyFrontmatter.Permission) {
			t.Errorf("%s default permissions differ from legacy: default=%v legacy=%v", roleID, defaultFrontmatter.Permission, legacyFrontmatter.Permission)
		}
		if defaultBody != legacyBody {
			t.Errorf("%s default body differs from reused legacy body", roleID)
		}
	}
	if len(files) != toolRoleCount*2 {
		t.Errorf("emitted %d files, want %d legacy plus default files", len(files), toolRoleCount*2)
	}
}

func TestOpenCodeProviderVariantExtensionIsDeterministic(t *testing.T) {
	t.Parallel()

	suite := loadOpenCodeVariantSuite(t)
	variants := make([]OpenCodeProviderVariant, 0, len(suite.ValidVariants))
	for index := len(suite.ValidVariants) - 1; index >= 0; index-- {
		variants = append(variants, suite.ValidVariants[index].variant())
	}
	files := emitOpenCodeAgentFiles(t, variants)

	var workerNames []string
	for _, file := range files {
		name := filepath.Base(file.Path)
		if strings.HasPrefix(name, "worker--") {
			workerNames = append(workerNames, name)
		}
	}
	wantNames := []string{
		"worker--acme--balanced.md",
		"worker--default.md",
		"worker--example-ai--fast.md",
	}
	if !reflect.DeepEqual(workerNames, wantNames) {
		t.Fatalf("worker variant order = %v, want %v", workerNames, wantNames)
	}

	for _, file := range files {
		if filepath.Base(file.Path) != "worker--acme--balanced.md" {
			continue
		}
		frontmatter, body := decodeOpenCodeAgent(t, file)
		if frontmatter.Model != "acme/model-2.1" {
			t.Errorf("extension model = %q, want acme/model-2.1", frontmatter.Model)
		}
		legacy, err := renderOpenCodeAgent(protocol.RoleWorker, filepath.Join(testModuleRoot(t), "skills", "protocol", "figures"))
		if err != nil {
			t.Fatalf("render legacy worker: %v", err)
		}
		_, legacyBody := splitFrontmatter(t, "worker.md", legacy)
		if body != legacyBody {
			t.Errorf("provider extension changed the shared worker body")
		}
		return
	}
	t.Fatal("worker--acme--balanced.md was not emitted")
}

func TestOpenCodeProviderVariantsAllowCrossProviderSlugReuse(t *testing.T) {
	t.Parallel()

	suite := loadOpenCodeVariantSuite(t)
	variants := make([]OpenCodeProviderVariant, 0, len(suite.CrossProviderSameSlug))
	for _, fixture := range suite.CrossProviderSameSlug {
		variants = append(variants, fixture.variant())
	}
	files := emitOpenCodeAgentFiles(t, variants)

	want := map[string]bool{
		"worker--acme--fast.md":       false,
		"worker--example-ai--fast.md": false,
	}
	for _, file := range files {
		name := filepath.Base(file.Path)
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("cross-provider shared slug did not emit %s", name)
		}
	}
}

func TestOpenCodeTargetEmitsCompleteProviderInventory(t *testing.T) {
	t.Parallel()

	emitter, ok := OpenCodeTarget.Agents.(openCodeAgentEmitter)
	if !ok {
		t.Fatalf("OpenCodeTarget.Agents type = %T, want openCodeAgentEmitter", OpenCodeTarget.Agents)
	}
	files := emitOpenCodeAgentFiles(t, emitter.Variants)
	if len(files) != 45 {
		t.Fatalf("OpenCode target emitted %d agent definitions, want 45 (5 roles x legacy, default, and 7 provider variants)", len(files))
	}

	wantModels := map[string]string{
		"--default.md":               "",
		"--anthropic--fable-5.md":    "anthropic/claude-fable-5",
		"--anthropic--haiku-4-5.md":  "anthropic/claude-haiku-4-5",
		"--anthropic--opus-4-8.md":   "anthropic/claude-opus-4-8",
		"--anthropic--sonnet-5.md":   "anthropic/claude-sonnet-5",
		"--openai--gpt-5-6-luna.md":  "openai/gpt-5.6-luna",
		"--openai--gpt-5-6-sol.md":   "openai/gpt-5.6-sol",
		"--openai--gpt-5-6-terra.md": "openai/gpt-5.6-terra",
	}
	byName := make(map[string]GeneratedFile, len(files))
	for _, file := range files {
		name := filepath.Base(file.Path)
		if _, exists := byName[name]; exists {
			t.Fatalf("OpenCode target emitted duplicate agent filename %q", name)
		}
		byName[name] = file
	}

	for roleID, roleSpec := range RoleSpecs {
		if len(roleSpec.Tools) == 0 {
			continue
		}
		legacyName := string(roleID) + ".md"
		legacy, exists := byName[legacyName]
		if !exists {
			t.Errorf("role %s is missing legacy definition %s", roleID, legacyName)
			continue
		}
		_, sharedBody := decodeOpenCodeAgent(t, legacy)
		if strings.Contains(sharedBody, "~/.claude") || strings.Contains(sharedBody, "Claude Code") {
			t.Errorf("role %s legacy OpenCode body contains Claude-only instruction guidance", roleID)
		}

		for suffix, wantModel := range wantModels {
			name := string(roleID) + suffix
			file, exists := byName[name]
			if !exists {
				t.Errorf("role %s is missing selectable definition %s", roleID, name)
				continue
			}
			frontmatter, body := decodeOpenCodeAgent(t, file)
			if frontmatter.Model != wantModel {
				t.Errorf("%s model = %q, want %q", name, frontmatter.Model, wantModel)
			}
			if body != sharedBody {
				t.Errorf("%s body differs from shared semantic body in %s", name, legacyName)
			}
			if strings.Contains(body, "~/.claude") || strings.Contains(body, "Claude Code") {
				t.Errorf("%s contains Claude-only instruction guidance", name)
			}
		}
	}
}

func TestOpenCodeTargetInventoryRejectsMissingStaleAndDuplicateEntries(t *testing.T) {
	t.Parallel()

	emitter := OpenCodeTarget.Agents.(openCodeAgentEmitter)
	files := emitOpenCodeAgentFiles(t, emitter.Variants)
	expected := make(map[string]string, len(files))
	for _, file := range files {
		name := filepath.Base(file.Path)
		if _, exists := expected[name]; exists {
			t.Fatalf("production inventory contains duplicate filename %q", name)
		}
		expected[name] = "production emitter"
	}

	t.Run("missing", func(t *testing.T) {
		actual := make(map[string]string, len(expected)-1)
		for name := range expected {
			if name != "worker--default.md" {
				actual[name] = "checked-out tree"
			}
		}
		missing, stale := registrySetDiff(expected, actual)
		if !reflect.DeepEqual(missing, []string{"worker--default.md"}) || len(stale) != 0 {
			t.Fatalf("missing inventory diff = (%v, %v), want ([worker--default.md], [])", missing, stale)
		}
	})

	t.Run("stale", func(t *testing.T) {
		actual := make(map[string]string, len(expected)+1)
		for name := range expected {
			actual[name] = "checked-out tree"
		}
		actual["worker--retired.md"] = "checked-out tree"
		missing, stale := registrySetDiff(expected, actual)
		if len(missing) != 0 || !reflect.DeepEqual(stale, []string{"worker--retired.md"}) {
			t.Fatalf("stale inventory diff = (%v, %v), want ([], [worker--retired.md])", missing, stale)
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		variants := append([]OpenCodeProviderVariant(nil), emitter.Variants...)
		variants = append(variants, variants[0])
		files, err := (openCodeAgentEmitter{Variants: variants}).Emit(t.TempDir(), "", GenerateOptions{})
		if err == nil || !strings.Contains(err.Error(), "repeats") {
			t.Fatalf("duplicate catalog Emit error = %v, want actionable duplicate rejection", err)
		}
		if files != nil {
			t.Fatalf("duplicate catalog returned %d generated files, want none", len(files))
		}
	})
}

func TestAgentSemanticBodyParityAcrossHarnesses(t *testing.T) {
	t.Parallel()

	figuresDir := filepath.Join(testModuleRoot(t), "skills", "protocol", "figures")
	for roleID, roleSpec := range RoleSpecs {
		if len(roleSpec.Tools) == 0 {
			continue
		}
		claudeBody, err := renderAgentBody(roleID, figuresDir, agentHarnessClaudeCode)
		if err != nil {
			t.Fatalf("render Claude body for %s: %v", roleID, err)
		}
		openCodeBody, err := renderAgentBody(roleID, figuresDir, agentHarnessOpenCode)
		if err != nil {
			t.Fatalf("render OpenCode body for %s: %v", roleID, err)
		}
		if got, want := normalizeAgentBodyForHarnessParity(t, openCodeBody), normalizeAgentBodyForHarnessParity(t, claudeBody); got != want {
			t.Errorf("role %s semantic body differs across harnesses", roleID)
		}
		if strings.Contains(openCodeBody, "~/.claude") || strings.Contains(openCodeBody, "Claude Code") {
			t.Errorf("role %s OpenCode body contains Claude-only instruction guidance", roleID)
		}
	}
}

func TestOpenCodeProviderVariantValidationRejectsFixtureCasesBeforeWrite(t *testing.T) {
	t.Parallel()

	suite := loadOpenCodeVariantSuite(t)
	for _, testCase := range suite.InvalidCases {
		testCase := testCase
		t.Run(testCase.Name, func(t *testing.T) {
			t.Parallel()
			variants := make([]OpenCodeProviderVariant, 0, len(testCase.Variants))
			for _, fixture := range testCase.Variants {
				variants = append(variants, fixture.variant())
			}
			out := t.TempDir()
			files, err := (openCodeAgentEmitter{Variants: variants}).Emit(out, "", GenerateOptions{Write: true})
			if err == nil || !strings.Contains(err.Error(), testCase.WantError) {
				t.Fatalf("Emit error = %v, want error containing %q", err, testCase.WantError)
			}
			if files != nil {
				t.Errorf("Emit returned files after validation failure: %v", files)
			}
			entries, readErr := os.ReadDir(out)
			if readErr != nil {
				t.Fatalf("read output directory: %v", readErr)
			}
			if len(entries) != 0 {
				t.Errorf("validation failure wrote output entries: %v", entries)
			}
		})
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

	// The canonical role body supplies the agent H1 heading independently of
	// either harness wrapper.
	if !strings.Contains(body, "# Architect Agent") {
		t.Errorf("architect OpenCode body missing canonical heading '# Architect Agent'")
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
