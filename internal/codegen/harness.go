package codegen

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

type HarnessName string

const (
	HarnessClaudeCode HarnessName = "claude-code"
	HarnessOpenCode   HarnessName = "opencode"
)

type SkillWriteMode string

const (
	WriteMarkerMerge SkillWriteMode = "marker-merge"
	WriteFullFile    SkillWriteMode = "full-file"
)

type GeneratedFile struct {
	Path    string
	Content string
}

type AgentEmitter interface {
	Emit(root string, figuresDir string, opts GenerateOptions) ([]GeneratedFile, error)
}

type ManifestEmitter interface {
	Emit(root string, opts GenerateOptions) ([]GeneratedFile, error)
}

type TargetHarness struct {
	Name             HarnessName
	SkillRoot        string
	SkillTemplate    string
	SubSkillTemplate string
	SkillWrite       SkillWriteMode
	Agents           AgentEmitter
	Manifest         ManifestEmitter
	Verbatim         []string
	CanonicalSkills  []CanonicalSkillID
}

var ClaudeCodeTarget = TargetHarness{
	Name:             HarnessClaudeCode,
	SkillRoot:        "skills",
	SkillTemplate:    TemplateSkill,
	SubSkillTemplate: TemplateSubSkill,
	SkillWrite:       WriteMarkerMerge,
	Agents:           claudeCodeAgentEmitter{},
	CanonicalSkills:  []CanonicalSkillID{CanonicalSkillInstallCLI},
}

var OpenCodeTarget = TargetHarness{
	Name:             HarnessOpenCode,
	SkillRoot:        filepath.Join(".opencode", "skill"),
	SkillTemplate:    "templates/opencode_skill.go.tmpl",
	SubSkillTemplate: "templates/opencode_skill_sub.go.tmpl",
	SkillWrite:       WriteFullFile,
	Agents:           openCodeAgentEmitter{},
	Manifest:         openCodeManifestEmitter{},
	Verbatim:         openCodeVerbatimDirs,
	CanonicalSkills:  []CanonicalSkillID{CanonicalSkillInstallCLI},
}

var harnessRegistry = map[HarnessName]TargetHarness{
	HarnessClaudeCode: ClaudeCodeTarget,
	HarnessOpenCode:   OpenCodeTarget,
	HarnessCodex:      CodexTarget,
}

func ResolveHarness(targets []string) ([]TargetHarness, error) {
	if len(targets) == 0 {
		targets = []string{string(HarnessClaudeCode)}
	}
	out := make([]TargetHarness, 0, len(targets))
	for _, target := range targets {
		name := HarnessName(strings.TrimSpace(target))
		if name == "" {
			continue
		}
		harness, ok := harnessRegistry[name]
		if !ok {
			return nil, fmt.Errorf(
				"codegen.ResolveHarness: unknown target %q — registered targets: [%s]; "+
					"use -targets with one or more comma-separated registered targets",
				name,
				joinedHarnessNames(),
			)
		}
		out = append(out, harness)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf(
			"codegen.ResolveHarness: no targets were selected — registered targets: [%s]; "+
				"use -targets=%s or -targets=%s,%s",
			joinedHarnessNames(),
			HarnessClaudeCode,
			HarnessClaudeCode,
			HarnessOpenCode,
		)
	}
	return out, nil
}

func EmitHarness(root string, h TargetHarness, figuresDir string, opts GenerateOptions) ([]GeneratedFile, error) {
	var out []GeneratedFile

	for _, item := range roleSkillItems() {
		path := filepath.Join(root, h.SkillRoot, item.dir, "SKILL.md")
		generated, err := emitRoleSkill(h, item.role, path, figuresDir, opts)
		if err != nil {
			return nil, fmt.Errorf("codegen.EmitHarness(%s): role skill %s: %w", h.Name, item.dir, err)
		}
		if generated.Path != "" {
			out = append(out, generated)
		}
	}

	for _, item := range commandSkillItems() {
		path := filepath.Join(root, h.SkillRoot, item.dir, "SKILL.md")
		generated, err := emitCommandSkill(h, item.commandID, path, figuresDir, opts)
		if err != nil {
			return nil, fmt.Errorf("codegen.EmitHarness(%s): command skill %s: %w", h.Name, item.dir, err)
		}
		if generated.Path != "" {
			out = append(out, generated)
		}
	}

	for _, dir := range h.Verbatim {
		files, err := copyVerbatimSkill(root, h.SkillRoot, dir, opts)
		if err != nil {
			return nil, fmt.Errorf("codegen.EmitHarness(%s): verbatim skill %s: %w", h.Name, dir, err)
		}
		out = append(out, files...)
	}

	for _, skillID := range h.CanonicalSkills {
		generated, err := emitCanonicalSkill(root, h, skillID, opts)
		if err != nil {
			return nil, fmt.Errorf("codegen.EmitHarness(%s): canonical skill %s: %w", h.Name, skillID, err)
		}
		out = append(out, generated)
	}

	if h.Agents != nil {
		files, err := h.Agents.Emit(root, figuresDir, opts)
		if err != nil {
			return nil, fmt.Errorf("codegen.EmitHarness(%s): agents: %w", h.Name, err)
		}
		out = append(out, files...)
	}

	if h.Manifest != nil {
		files, err := h.Manifest.Emit(root, opts)
		if err != nil {
			return nil, fmt.Errorf("codegen.EmitHarness(%s): manifest: %w", h.Name, err)
		}
		out = append(out, files...)
	}

	return out, nil
}

type claudeCodeAgentEmitter struct{}

func (claudeCodeAgentEmitter) Emit(root string, figuresDir string, opts GenerateOptions) ([]GeneratedFile, error) {
	var out []GeneratedFile
	for roleID, roleSpec := range RoleSpecs {
		if len(roleSpec.Tools) == 0 {
			continue
		}
		path := filepath.Join(root, "agents", fmt.Sprintf("%s.md", roleID))
		content, err := GenerateAgent(roleID, path, figuresDir, opts)
		if err != nil {
			return nil, err
		}
		if content != "" {
			out = append(out, GeneratedFile{Path: path, Content: content})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out, nil
}

type roleSkillItem struct {
	role protocol.RoleId
	dir  string
}

type commandSkillItem struct {
	commandID string
	dir       string
}

var roleSkillDirs = map[protocol.RoleId]string{
	protocol.RoleSupervisor: "supervisor",
	protocol.RoleWorker:     "worker",
	protocol.RoleReviewer:   "reviewer",
	protocol.RoleArchitect:  "architect",
	protocol.RoleEpoch:      "epoch",
}

var commandSkillDirs = map[string]string{
	"cmd-sup-plan":      "supervisor-plan-tasks",
	"cmd-sup-spawn":     "supervisor-spawn-worker",
	"cmd-impl-review":   "impl-review",
	"cmd-arch-handoff":  "architect-handoff",
	"cmd-arch-propose":  "architect-propose-plan",
	"cmd-arch-ratify":   "architect-ratify",
	"cmd-arch-review":   "architect-request-review",
	"cmd-explore":       "explore",
	"cmd-impl-slice":    "impl-slice",
	"cmd-research":      "research",
	"cmd-rev-comment":   "reviewer-comment",
	"cmd-rev-code":      "reviewer-review-code",
	"cmd-rev-plan":      "reviewer-review-plan",
	"cmd-rev-vote":      "reviewer-vote",
	"cmd-status":        "status",
	"cmd-sup-commit":    "supervisor-commit",
	"cmd-sup-track":     "supervisor-track-progress",
	"cmd-swarm":         "swarm",
	"cmd-user-elicit":   "user-elicit",
	"cmd-user-request":  "user-request",
	"cmd-user-uat":      "user-uat",
	"cmd-work-blocked":  "worker-blocked",
	"cmd-work-complete": "worker-complete",
	"cmd-work-impl":     "worker-implement",
}

func emitRoleSkill(h TargetHarness, roleID protocol.RoleId, path string, figuresDir string, opts GenerateOptions) (GeneratedFile, error) {
	if h.SkillWrite == WriteMarkerMerge {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return GeneratedFile{}, nil
		}
		content, err := GenerateSkill(roleID, path, figuresDir, opts)
		return GeneratedFile{Path: path, Content: content}, err
	}
	content, err := renderSkill(roleID, figuresDir, h.SkillTemplate)
	if err != nil {
		return GeneratedFile{}, err
	}
	return writeFullGeneratedFile(path, content, opts)
}

func emitCommandSkill(h TargetHarness, commandID string, path string, figuresDir string, opts GenerateOptions) (GeneratedFile, error) {
	if h.SkillWrite == WriteMarkerMerge {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return GeneratedFile{}, nil
		}
		content, err := GenerateSubSkill(commandID, path, figuresDir, opts)
		return GeneratedFile{Path: path, Content: content}, err
	}
	content, err := renderSubSkill(commandID, figuresDir, h.SubSkillTemplate)
	if err != nil {
		return GeneratedFile{}, err
	}
	return writeFullGeneratedFile(path, content, opts)
}

func copyVerbatimSkill(root string, targetSkillRoot string, dirName string, opts GenerateOptions) ([]GeneratedFile, error) {
	srcRoot := filepath.Join(root, "skills", dirName)
	dstRoot := filepath.Join(root, targetSkillRoot, dirName)
	var out []GeneratedFile
	if err := filepath.WalkDir(srcRoot, func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcRoot, srcPath)
		if err != nil {
			return err
		}
		contentBytes, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		generated, err := writeFullGeneratedFile(filepath.Join(dstRoot, rel), string(contentBytes), opts)
		if err != nil {
			return err
		}
		out = append(out, generated)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func writeFullGeneratedFile(path string, content string, opts GenerateOptions) (GeneratedFile, error) {
	oldContent := ""
	if data, err := os.ReadFile(path); err == nil {
		oldContent = string(data)
	}
	if opts.Diff && oldContent != content {
		fmt.Print(unifiedDiff(path, path, oldContent, content))
	}
	if opts.Write {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return GeneratedFile{}, fmt.Errorf("create parent directory for %q: %w", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return GeneratedFile{}, fmt.Errorf("write generated file %q: %w", path, err)
		}
	}
	return GeneratedFile{Path: path, Content: content}, nil
}

func roleSkillItems() []roleSkillItem {
	out := make([]roleSkillItem, 0, len(roleSkillDirs))
	for role, dir := range roleSkillDirs {
		out = append(out, roleSkillItem{role: role, dir: dir})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].dir < out[j].dir
	})
	return out
}

func commandSkillItems() []commandSkillItem {
	out := make([]commandSkillItem, 0, len(commandSkillDirs))
	for commandID, dir := range commandSkillDirs {
		out = append(out, commandSkillItem{commandID: commandID, dir: dir})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].dir < out[j].dir
	})
	return out
}

func joinedHarnessNames() string {
	names := make([]string, 0, len(harnessRegistry))
	for name := range harnessRegistry {
		names = append(names, string(name))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
