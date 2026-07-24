package codegen

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"unicode/utf8"

	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/pelletier/go-toml/v2"
)

const codexAgentNamespace = "pasture-"

type codexAgentTemplateData struct {
	Name        string
	Description string
	Body        string
}

type codexAgentEmitter struct{}

func (codexAgentEmitter) Emit(_ string, outputRoot string, figuresDir string, opts GenerateOptions) ([]GeneratedFile, error) {
	var out []GeneratedFile
	for roleID, roleSpec := range RoleSpecs {
		if len(roleSpec.Tools) == 0 {
			continue
		}
		content, err := renderCodexAgent(roleID, figuresDir)
		if err != nil {
			return nil, fmt.Errorf("codegen.codexAgentEmitter.Emit: render Codex agent for role %q failed: %w", roleID, err)
		}
		name := codexAgentNamespace + string(roleID)
		path := filepath.Join(outputRoot, ".codex", "agents", name+".toml")
		generated, err := writeFullGeneratedFile(path, content, opts)
		if err != nil {
			return nil, fmt.Errorf("codegen.codexAgentEmitter.Emit: write Codex agent for role %q to %q failed: %w", roleID, path, err)
		}
		out = append(out, generated)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func renderCodexAgent(roleID protocol.RoleId, figuresDir string) (string, error) {
	roleSpec, ok := RoleSpecs[roleID]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderCodexAgent: role %q not found in RoleSpecs — verify the role ID is defined in specs_data.go",
			roleID,
		)
	}

	claudeAgent, err := renderAgent(roleID, figuresDir)
	if err != nil {
		return "", fmt.Errorf("codegen.renderCodexAgent: render canonical agent for role %q failed: %w", roleID, err)
	}
	body, err := stripFrontmatter(claudeAgent)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.renderCodexAgent: strip Claude frontmatter from canonical agent for role %q failed: %w — renderAgent output must start with a `---`-delimited YAML frontmatter block",
			roleID, err,
		)
	}
	if err := validateCodexMultilineLiteral(body); err != nil {
		return "", fmt.Errorf(
			"codegen.renderCodexAgent: canonical body for role %q cannot be represented safely as a TOML multiline literal: %w — remove the unsupported sequence or update the Codex emitter to use a lossless encoding",
			roleID, err,
		)
	}

	tmpl, err := template.New("codex_agent.go.tmpl").
		Option("missingkey=error").
		ParseFS(templatesFS, "templates/codex_agent.go.tmpl")
	if err != nil {
		return "", fmt.Errorf("codegen.renderCodexAgent: parse templates/codex_agent.go.tmpl failed: %w", err)
	}

	name, err := quoteCodexTOMLBasicString(codexAgentNamespace + string(roleID))
	if err != nil {
		return "", fmt.Errorf("codegen.renderCodexAgent: encode Codex agent name for role %q failed: %w", roleID, err)
	}
	description, err := quoteCodexTOMLBasicString(roleSpec.Description)
	if err != nil {
		return "", fmt.Errorf("codegen.renderCodexAgent: encode Codex agent description for role %q failed: %w", roleID, err)
	}

	data := codexAgentTemplateData{
		Name:        name,
		Description: description,
		Body:        body,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("codegen.renderCodexAgent: execute codex_agent.go.tmpl for role %q failed: %w", roleID, err)
	}
	content := strings.TrimRight(buf.String(), "\n") + "\n"
	var parsed map[string]any
	if err := toml.Unmarshal([]byte(content), &parsed); err != nil {
		return "", fmt.Errorf(
			"codegen.renderCodexAgent: generated TOML for role %q is invalid: %w — check the role metadata and canonical agent body for TOML-incompatible content",
			roleID, err,
		)
	}
	return content, nil
}

// quoteCodexTOMLBasicString encodes a UTF-8 Go string as a TOML basic string.
// strconv.Quote is not sufficient here because Go accepts escape sequences,
// such as \a, that TOML does not. Keeping this encoder local prevents a future
// role description from producing a syntactically invalid custom-agent file.
func quoteCodexTOMLBasicString(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("value contains invalid UTF-8")
	}

	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}

func validateCodexMultilineLiteral(body string) error {
	if !utf8.ValidString(body) {
		return fmt.Errorf("body contains invalid UTF-8")
	}
	if strings.Contains(body, "'''") {
		return fmt.Errorf("body contains the TOML multiline literal delimiter `'''`")
	}
	if strings.ContainsRune(body, '\r') {
		return fmt.Errorf("body contains a carriage return, which TOML would normalize and change")
	}
	for _, r := range body {
		if (r >= 0 && r <= 0x08) || (r >= 0x0b && r <= 0x0c) || (r >= 0x0e && r <= 0x1f) || r == 0x7f {
			return fmt.Errorf("body contains TOML-forbidden control character U+%04X", r)
		}
	}
	return nil
}
