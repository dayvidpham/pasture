package codegen

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
)

// RequireOperationalSource checks assembled registries, including unused
// fragments and metadata, as well as every hand-authored operational input.
// Registry traversal is read-only validation, not registration or rendering.
func RequireOperationalSource(root string) error {
	if err := ValidateRoleToolSurface(); err != nil {
		return err
	}
	if err := ValidateCapabilityTiers(); err != nil {
		return err
	}
	if err := ValidateSignalKinds(); err != nil {
		return err
	}
	if err := ValidateWritingRules(); err != nil {
		return err
	}
	registries := map[string]any{
		"SkillBodySpecs": SkillBodySpecs, "SharedFragmentSpecs": SharedFragmentSpecs,
		"RoleSpecs": RoleSpecs, "ConstraintSpecs": ConstraintSpecs, "CommandSpecs": CommandSpecs,
		"PhaseSpecs": PhaseSpecs, "HandoffSpecs": HandoffSpecs, "FigureSpecs": FigureSpecs,
		"ChecklistSpecs": ChecklistSpecs, "WorkflowSpecs": WorkflowSpecs, "ReviewAxisSpecs": ReviewAxisSpecs,
		"ProcedureSteps": ProcedureSteps, "LabelSpecs": LabelSpecs, "CoordinationCommands": CoordinationCommands,
	}
	var docs []scan.OperationalDocument
	keys := make([]string, 0, len(registries))
	for key := range registries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		trackerRegistryStrings(reflect.ValueOf(registries[key]), key, func(owner, content string) {
			file := "internal/codegen/specs_data.go"
			switch key {
			case "SkillBodySpecs":
				if start, stop := strings.Index(owner, "["), strings.Index(owner, "]"); start >= 0 && stop > start {
					file = "internal/codegen/specs_data_body_" + strings.ReplaceAll(owner[start+1:stop], "-", "_") + ".go"
				}
			case "SharedFragmentSpecs":
				file = "internal/codegen/specs_data_fragments.go"
			}
			docs = append(docs, scan.OperationalDocument{
				Owner: owner, File: file, Content: []byte(content), Markdown: true,
			})
		})
	}
	if err := scan.ValidateOperationalDocuments(docs, nil, "canonical registry validation"); err != nil {
		return err
	}
	_, err := scan.CheckOperationalTree(root, scan.OperationalSourceRoots(), "canonical input validation")
	return err
}

func trackerRegistryStrings(value reflect.Value, owner string, visit func(string, string)) {
	if !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if !value.IsNil() {
			trackerRegistryStrings(value.Elem(), owner, visit)
		}
	case reflect.String:
		visit(owner, value.String())
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			trackerRegistryStrings(value.Field(i), owner+"."+value.Type().Field(i).Name, visit)
		}
	case reflect.Map:
		keys := value.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
		for _, key := range keys {
			trackerRegistryStrings(value.MapIndex(key), fmt.Sprintf("%s[%s]", owner, key), visit)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			trackerRegistryStrings(value.Index(i), fmt.Sprintf("%s[%d]", owner, i), visit)
		}
	}
}

// ValidateOperationalFiles checks exact candidate bytes before any publication.
// Verbatim projections inherit their canonical owner, never a weaker target
// exemption. Their digest/range identity must still match the exact history.
//
// target selects the per-target foreign-name scan over agent declarations. An
// empty target is the aggregate stage of a multi-target generation run: every
// target already passed its own EmitHarness validation, so only the
// target-independent checks run there.
func ValidateOperationalFiles(root string, files []GeneratedFile, step string, target HarnessName) error {
	if err := validateForeignToolNames(root, files, target, step); err != nil {
		return err
	}
	docs := make([]scan.OperationalDocument, 0, len(files))
	for _, file := range files {
		rel, err := filepath.Rel(root, file.Path)
		if err != nil {
			return fmt.Errorf("operational gate cannot locate candidate %q under %q: %w; outputs withheld; repair the emitter and regenerate", file.Path, root, err)
		}
		rel = filepath.ToSlash(rel)
		docs = append(docs, scan.OperationalDocument{Owner: scan.OperationalOwner(rel), File: rel, Content: []byte(file.Content)})
	}
	history, err := scan.OperationalHistory()
	if err != nil {
		return err
	}
	return scan.ValidateOperationalDocuments(docs, history, step)
}

// publishGeneratedFiles is reached only after the complete stage is validated.
func publishGeneratedFiles(files []GeneratedFile, opts GenerateOptions) error {
	for _, file := range files {
		if _, err := writeFullGeneratedFile(file.Path, file.Content, opts); err != nil {
			return err
		}
	}
	return nil
}

// validateForeignToolNames scans the staged agent declarations of one target
// for a native tool name that belongs to another target. The foreign list is
// derived from the other targets' lowerings, so no authored name list can
// drift away from the contracts.
func validateForeignToolNames(root string, files []GeneratedFile, target HarnessName, step string) error {
	if target == "" {
		return nil
	}
	foreign, err := foreignToolNames(target)
	if err != nil {
		return fmt.Errorf("foreign tool scan for target %q could not derive its name set: %w", target, err)
	}
	foreignSet := make(map[string]struct{}, len(foreign))
	for _, name := range foreign {
		foreignSet[name] = struct{}{}
	}
	for _, file := range files {
		rel, err := filepath.Rel(root, file.Path)
		if err != nil {
			return fmt.Errorf("foreign tool scan cannot locate candidate %q under %q: %w; outputs withheld; repair the emitter and regenerate", file.Path, root, err)
		}
		rel = filepath.ToSlash(rel)
		if !isAgentDeclaration(target, rel) {
			continue
		}
		names, err := declaredToolNames(rel, file.Content)
		if err != nil {
			return fmt.Errorf(
				"foreign tool scan could not read the %s agent declaration %s — "+
					"why: an unrecognised declaration shape would make this guard pass vacuously; "+
					"where: internal/codegen/operational_gate.go validateForeignToolNames; "+
					"when: before %s published any byte; "+
					"what it means: the guard cannot prove the target ships only its own native names; "+
					"fix: emit the declaration in the single-line generated form or extend the parser: %w",
				target, rel, step, err,
			)
		}
		for _, name := range names {
			if _, bad := foreignSet[strings.ToLower(name)]; bad {
				return fmt.Errorf(
					"foreign tool name %q in %s — why: the %s agent declaration names a tool that belongs to another harness; where: internal/codegen/operational_gate.go foreign tool scan; when: before %s published any byte; what it means: the target would ship a name its host does not expose; fix: resolve the tool through runtime.LookupHarnessTool or runtime.LookupOperationBinding for %s",
					name, rel, target, step, target,
				)
			}
		}
	}
	return nil
}

// isAgentDeclaration reports whether rel is one of the target's generated agent
// definition paths. The foreign scan reads declarations only, never prose: the
// shared agent body legitimately discusses other harnesses' concepts.
func isAgentDeclaration(target HarnessName, rel string) bool {
	switch target {
	case HarnessClaudeCode:
		return strings.HasPrefix(rel, "agents/") && strings.HasSuffix(rel, ".md")
	case HarnessOpenCode:
		return strings.HasPrefix(rel, ".opencode/agent/") && strings.HasSuffix(rel, ".md")
	case HarnessCodex:
		return strings.HasPrefix(rel, ".codex/agents/") && strings.HasSuffix(rel, ".toml")
	default:
		return false
	}
}

// declaredToolNames extracts the tool names a generated agent declaration
// actually grants: the Claude `tools:` frontmatter list, the OpenCode
// `permission:` keys set to "allow", or the Codex `functions` array.
//
// It fails closed. An agent declaration the parser cannot recognise returns an
// error rather than an empty list, because an empty list would make the
// foreign-name guard pass vacuously whenever a template shape changed.
func declaredToolNames(rel, content string) ([]string, error) {
	if strings.HasSuffix(rel, ".toml") {
		names, found := parseCodexAgentFunctions(content)
		if !found {
			return nil, fmt.Errorf(
				"%s has no `functions = [...]` declaration — "+
					"why: a Codex agent profile declares its native functions in that single-line array; "+
					"where: internal/codegen/operational_gate.go declaredToolNames; "+
					"fix: emit the functions array in the generated agent profile",
				rel,
			)
		}
		return names, nil
	}
	return markdownDeclaredTools(rel, content)
}

func markdownDeclaredTools(rel, content string) ([]string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, fmt.Errorf(
			"%s has no leading `---` frontmatter block — "+
				"why: the scan reads tool declarations from frontmatter; "+
				"where: internal/codegen/operational_gate.go markdownDeclaredTools; "+
				"fix: emit the generated agent with YAML frontmatter",
			rel,
		)
	}
	var names []string
	sawTools := false
	sawPermission := false
	inPermissions := false
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		if strings.HasPrefix(trimmed, "tools:") {
			sawTools = true
			raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "tools:"))
			if raw == "" {
				return nil, fmt.Errorf(
					"%s has a `tools:` key with no inline list — "+
						"why: the scan reads the generated single-line comma-separated form, and a block list is an unrecognised shape; "+
						"where: internal/codegen/operational_gate.go markdownDeclaredTools; "+
						"fix: emit `tools: A, B` on one line",
					rel,
				)
			}
			for _, name := range strings.Split(raw, ",") {
				if name = strings.TrimSpace(name); name != "" {
					names = append(names, name)
				}
			}
			continue
		}
		if trimmed == "permission:" || trimmed == "permissions:" {
			sawPermission = true
			inPermissions = true
			continue
		}
		if !inPermissions {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inPermissions = false
			continue
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found || strings.TrimSpace(value) != "allow" {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"`)
		if key != "" && key != "*" {
			names = append(names, key)
		}
	}
	if !sawTools && !sawPermission {
		return nil, fmt.Errorf(
			"%s declares neither a `tools:` line nor a `permission:` block — "+
				"why: an agent declaration must name its granted surface, and an unrecognised shape would let the foreign-name guard pass vacuously; "+
				"where: internal/codegen/operational_gate.go markdownDeclaredTools; "+
				"fix: emit the tools line or permission block in the generated agent",
			rel,
		)
	}
	return names, nil
}
