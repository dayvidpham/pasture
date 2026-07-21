package codegen

import (
	"bytes"
	"fmt"
	"io/fs"
	"path/filepath"
	"text/template"
)

type CanonicalSkillID string

const CanonicalSkillInstallCLI CanonicalSkillID = "install-cli"

type installCLIRenderContext struct {
	Harness    HarnessName
	Identity   string
	Invocation string
}

var installCLIRenderContexts = map[HarnessName]installCLIRenderContext{
	HarnessClaudeCode: {
		Harness:    HarnessClaudeCode,
		Identity:   "Claude Code skill for automated Pasture binary installation.",
		Invocation: "Invoke via Claude Code:",
	},
	HarnessOpenCode: {
		Harness:    HarnessOpenCode,
		Identity:   "OpenCode skill for automated Pasture binary installation.",
		Invocation: "Invoke via OpenCode:",
	},
}

type installCLITemplateData struct {
	RenderContext installCLIRenderContext
	Body          string
}

func emitCanonicalSkill(root string, harness TargetHarness, skillID CanonicalSkillID, opts GenerateOptions) (GeneratedFile, error) {
	if skillID != CanonicalSkillInstallCLI {
		return GeneratedFile{}, fmt.Errorf("codegen.emitCanonicalSkill: skill %q has no renderer — register a typed canonical renderer before adding it to target %q", skillID, harness.Name)
	}
	content, err := renderInstallCLISkill(harness.Name)
	if err != nil {
		return GeneratedFile{}, err
	}
	path := filepath.Join(root, harness.SkillRoot, string(skillID), "SKILL.md")
	return writeFullGeneratedFile(path, content, opts)
}

func renderInstallCLISkill(harness HarnessName) (string, error) {
	renderContext, ok := installCLIRenderContexts[harness]
	if !ok {
		return "", fmt.Errorf("codegen.renderInstallCLISkill: harness %q has no install-cli render context — define its typed identity and invocation guidance before enabling the canonical skill", harness)
	}
	body, err := fs.ReadFile(templatesFS, "templates/install_cli_body.md")
	if err != nil {
		return "", fmt.Errorf("codegen.renderInstallCLISkill: failed to read canonical body templates/install_cli_body.md while rendering %q: %w — restore the embedded canonical source", harness, err)
	}
	tmpl, err := template.New("install_cli.go.tmpl").Option("missingkey=error").ParseFS(templatesFS, "templates/install_cli.go.tmpl")
	if err != nil {
		return "", fmt.Errorf("codegen.renderInstallCLISkill: failed to parse templates/install_cli.go.tmpl while rendering %q: %w — fix the target wrapper template", harness, err)
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, installCLITemplateData{RenderContext: renderContext, Body: string(body)}); err != nil {
		return "", fmt.Errorf("codegen.renderInstallCLISkill: template execution failed for harness %q: %w — check the typed render context and wrapper fields", harness, err)
	}
	return normalizeTrailingNewline(rendered.String()), nil
}
