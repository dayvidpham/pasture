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
func ValidateOperationalFiles(root string, files []GeneratedFile, step string) error {
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
