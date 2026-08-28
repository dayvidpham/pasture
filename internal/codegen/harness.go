package codegen

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

type HarnessName string

const (
	HarnessClaudeCode  HarnessName = "claude-code"
	HarnessOpenCode    HarnessName = "opencode"
	HarnessAntigravity HarnessName = "antigravity"
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
}

var ClaudeCodeTarget = TargetHarness{
	Name:             HarnessClaudeCode,
	SkillRoot:        "skills",
	SkillTemplate:    TemplateSkill,
	SubSkillTemplate: TemplateSubSkill,
	SkillWrite:       WriteMarkerMerge,
	Agents:           claudeCodeAgentEmitter{},
	Manifest:         claudeHooksEmitter{},
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
		if name == HarnessAntigravity {
			return nil, fmt.Errorf("codegen.ResolveHarness(%s): %w", name, runtime.AntigravityLifecycleContract())
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
				"use -targets=%s or -targets=%s,%s,%s",
			joinedHarnessNames(),
			HarnessClaudeCode,
			HarnessClaudeCode,
			HarnessOpenCode,
			HarnessCodex,
		)
	}
	return out, nil
}

// HarnessRoots names the two independent directory roots one harness emission
// uses:
//
//   - Source is the tree that holds every hand-authored input: the skill
//     directories copied verbatim (skills/<dir>), the marker-merge base
//     SKILL.md files a marker-merge target reads (skills/<dir>/SKILL.md), and
//     the figure YAML files (skills/protocol/figures). Nothing is ever written
//     below Source.
//   - Output is the tree that receives every emitted file.
//
// Because every input path is derived from Source, the two roots stay paired
// and no caller can mix one harness's inputs with another's outputs.
//
// A normal repository generation sets both to the module root; use RepoRoots
// for that case. Callers that render into a scratch directory (a temporary
// staging tree, or a test) keep Source on the real module and point Output
// somewhere else, so no input tree must be duplicated first.
type HarnessRoots struct {
	Source string
	Output string
}

// figuresDir is the directory of figure YAML files. It is an input, so it is
// always derived from Source; keeping the derivation here stops a caller from
// pairing one root with another tree's figures.
func (r HarnessRoots) figuresDir() string {
	return filepath.Join(r.Source, "skills", "protocol", "figures")
}

// skillDir is the hand-authored skill directory below Source. It holds both the
// verbatim skill trees and the marker-merge base files.
func (r HarnessRoots) skillDir() string {
	return filepath.Join(r.Source, "skills")
}

// skillPaths pairs the file a skill emission reads (Src, a hand-authored
// marker-merge base below HarnessRoots.Source) with the file it writes (Dst,
// below HarnessRoots.Output). A full-file target ignores Src.
type skillPaths struct {
	Src string
	Dst string
}

// inPlaceSkillPaths reads and writes one path, which is what an in-place
// repository generation does.
func inPlaceSkillPaths(path string) skillPaths {
	return skillPaths{Src: path, Dst: path}
}

// writeSkillFile writes one generated skill file and creates its parent
// directory first, because a split-root run writes into an empty output tree.
func writeSkillFile(path string, content string) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create parent directory for %q: %w", path, err)
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// RepoRoots returns the roots of an in-place generation, where one module root
// is both the source of the hand-authored trees and the destination of the
// generated files.
func RepoRoots(root string) HarnessRoots {
	return HarnessRoots{Source: root, Output: root}
}

// skillPathsFor names the two files of one skill directory: the hand-authored
// marker-merge base below Source, and the emitted file below Output. A
// full-file target has no base file and uses only Dst.
func (r HarnessRoots) skillPathsFor(h TargetHarness, dir string) skillPaths {
	return skillPaths{
		Src: filepath.Join(r.skillDir(), dir, "SKILL.md"),
		Dst: filepath.Join(r.Output, h.SkillRoot, dir, "SKILL.md"),
	}
}

// validate rejects an incomplete HarnessRoots before any file is read or
// written, so a caller that forgot a root gets a named fix instead of a
// confusing lstat failure on a relative path.
func (r HarnessRoots) validate(name HarnessName) error {
	var missing string
	switch {
	case r.Source == "" && r.Output == "":
		missing = "Source and Output are"
	case r.Source == "":
		missing = "Source is"
	case r.Output == "":
		missing = "Output is"
	default:
		return nil
	}
	return fmt.Errorf(
		"codegen.EmitHarness(%s): HarnessRoots.%s empty — "+
			"why: the harness reads the hand-authored verbatim trees below Source and writes every emitted file below Output, so both roots must be set; "+
			"where: internal/codegen/harness.go EmitHarness; "+
			"when: the roots were validated before the first read or write; "+
			"what it means for the caller: no file was read or written and generation stopped; "+
			"fix: pass codegen.RepoRoots(moduleRoot) for an in-place run, or set both Source and Output for a split run",
		name, missing,
	)
}

// validateSource proves the Source tree really holds the hand-authored inputs.
// Every target reads at least the marker-merge base files or the verbatim skill
// trees from <Source>/skills, so a wrong or missing Source must fail closed
// here instead of silently emitting a partial harness.
func (r HarnessRoots) validateSource(name HarnessName) error {
	for _, candidate := range []struct {
		path string
		what string
	}{
		{path: r.Source, what: "source root"},
		{path: r.skillDir(), what: "hand-authored skill directory"},
	} {
		info, err := os.Stat(candidate.path)
		if err != nil {
			return fmt.Errorf(
				"codegen.EmitHarness(%s): cannot read the %s %q — "+
					"why: the harness reads its hand-authored inputs (marker-merge base SKILL.md files, verbatim skill trees, and figures) below HarnessRoots.Source; "+
					"where: internal/codegen/harness.go EmitHarness; "+
					"when: the roots were checked before the first read or write; "+
					"what it means for the caller: no file was read or written and generation stopped; "+
					"fix: set HarnessRoots.Source to a module root that contains a skills/ directory, for example codegen.RepoRoots(moduleRoot): %w",
				name, candidate.what, candidate.path, err,
			)
		}
		if !info.IsDir() {
			return fmt.Errorf(
				"codegen.EmitHarness(%s): the %s %q is a file, not a directory — "+
					"why: the harness reads its hand-authored inputs below HarnessRoots.Source; "+
					"where: internal/codegen/harness.go EmitHarness; "+
					"when: the roots were checked before the first read or write; "+
					"what it means for the caller: no file was read or written and generation stopped; "+
					"fix: set HarnessRoots.Source to a module root directory that contains a skills/ directory",
				name, candidate.what, candidate.path,
			)
		}
	}
	return nil
}

// EmitHarness renders one harness target: role skills, command skills,
// verbatim skill copies, agents, and the manifest. roots.Source supplies every
// hand-authored input (verbatim trees, marker-merge base files, and figures);
// roots.Output receives all output.
func EmitHarness(roots HarnessRoots, h TargetHarness, opts GenerateOptions) ([]GeneratedFile, error) {
	if err := roots.validate(h.Name); err != nil {
		return nil, err
	}
	if err := roots.validateSource(h.Name); err != nil {
		return nil, err
	}
	root := roots.Output
	figuresDir := roots.figuresDir()
	var out []GeneratedFile

	for _, item := range roleSkillItems() {
		paths := roots.skillPathsFor(h, item.dir)
		generated, err := emitRoleSkill(h, item.role, paths, figuresDir, opts)
		if err != nil {
			return nil, fmt.Errorf("codegen.EmitHarness(%s): role skill %s: %w", h.Name, item.dir, err)
		}
		if generated.Path != "" {
			out = append(out, generated)
		}
	}

	for _, item := range commandSkillItems() {
		paths := roots.skillPathsFor(h, item.dir)
		generated, err := emitCommandSkill(h, item.commandID, paths, figuresDir, opts)
		if err != nil {
			return nil, fmt.Errorf("codegen.EmitHarness(%s): command skill %s: %w", h.Name, item.dir, err)
		}
		if generated.Path != "" {
			out = append(out, generated)
		}
	}

	for _, dir := range h.Verbatim {
		files, err := copyVerbatimSkill(roots, h.SkillRoot, dir, opts)
		if err != nil {
			return nil, fmt.Errorf("codegen.EmitHarness(%s): verbatim skill %s: %w", h.Name, dir, err)
		}
		out = append(out, files...)
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

// emitRoleSkill renders one role skill. A marker-merge target merges into the
// hand-authored base file at paths.Src (an input below HarnessRoots.Source) and
// writes the result to paths.Dst; a full-file target renders paths.Dst alone.
func emitRoleSkill(h TargetHarness, roleID protocol.RoleId, paths skillPaths, figuresDir string, opts GenerateOptions) (GeneratedFile, error) {
	if h.SkillWrite == WriteMarkerMerge {
		if _, err := os.Stat(paths.Src); os.IsNotExist(err) {
			return GeneratedFile{}, nil
		}
		content, err := generateSkillInto(roleID, paths, figuresDir, opts)
		return GeneratedFile{Path: paths.Dst, Content: content}, err
	}
	content, err := renderSkill(roleID, figuresDir, h.SkillTemplate)
	if err != nil {
		return GeneratedFile{}, err
	}
	return writeFullGeneratedFile(paths.Dst, content, opts)
}

// emitCommandSkill renders one command skill, with the same source/output split
// as emitRoleSkill.
func emitCommandSkill(h TargetHarness, commandID string, paths skillPaths, figuresDir string, opts GenerateOptions) (GeneratedFile, error) {
	if h.SkillWrite == WriteMarkerMerge {
		if _, err := os.Stat(paths.Src); os.IsNotExist(err) {
			return GeneratedFile{}, nil
		}
		content, err := generateSubSkillInto(commandID, paths, figuresDir, opts)
		return GeneratedFile{Path: paths.Dst, Content: content}, err
	}
	content, err := renderSubSkill(commandID, figuresDir, h.SubSkillTemplate)
	if err != nil {
		return GeneratedFile{}, err
	}
	return writeFullGeneratedFile(paths.Dst, content, opts)
}

// copyVerbatimSkill copies one hand-authored skill directory from
// <roots.Source>/skills/<dirName> to <roots.Output>/<targetSkillRoot>/<dirName>,
// recursively and byte for byte. The two roots are independent: the source tree
// is never written, and the output tree needs no copy of the source.
//
// The walk stays STRICT: a missing source directory is a real packaging fault
// and is reported, not skipped.
func copyVerbatimSkill(roots HarnessRoots, targetSkillRoot string, dirName string, opts GenerateOptions) ([]GeneratedFile, error) {
	srcRoot := filepath.Join(roots.skillDir(), dirName)
	dstRoot := filepath.Join(roots.Output, targetSkillRoot, dirName)
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
