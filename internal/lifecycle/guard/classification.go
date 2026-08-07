package guard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
)

type JournalClass uint8

const (
	ImmutableSnapshot JournalClass = iota + 1
	ImmutableFact
	DerivedProjection
	BuildInput
	NonJournalValue
)

// journalClasses is intentionally exhaustive only for journal-facing and build
// types. Non-journal structs opt in mechanically by embedding nonJournalValue;
// aliases and interfaces are mechanically non-journal. Thus adding an exported
// struct without either decision fails closed without a central exception list.
// journalClasses classifies every journal-facing model type. Entries for types
// not yet declared in the model package are inert (F4): the map is only ever
// consulted for types the parser actually finds, so pre-landing a classification
// for a type an adjacent Stage-3 pillar will declare later keeps that pillar
// file-disjoint from this shared map without changing behavior here. The
// pre-landed entries below are: LinkRecord (SLICE-3 lineage, an immutable
// per-host predecessor edge) and the three disclosure facts (SLICE-4 context
// disclosure, immutable plan/attempt/result facts). LifecycleMetamodelManifest is NOT
// listed: it embeds nonJournalValue and is mechanically non-journal.
var journalClasses = map[string]JournalClass{
	"DefinitionSnapshot":    ImmutableSnapshot,
	"OccurrenceRecord":      ImmutableSnapshot,
	"InterpretedRecord":     ImmutableSnapshot,
	"DefinitionStateFact":   ImmutableFact,
	"DefinitionStateRecord": DerivedProjection,
	// Pre-landed cross-pillar entries (inert until the owning slice declares the type):
	"LinkRecord":            ImmutableSnapshot, // SLICE-3 (lineage)
	"DisclosurePlanFact":    ImmutableFact,     // SLICE-4 (context disclosure)
	"DisclosureAttemptFact": ImmutableFact,     // SLICE-4 (context disclosure)
	"DisclosureResultFact":  ImmutableFact,     // SLICE-4 (context disclosure)
}

var lifecycleStatusEnums = map[string]struct{}{
	"DefinitionStatus": {},
}

func ValidateModelDirectory(dir string) error {
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, dir, func(info fs.FileInfo) bool { return true }, 0)
	if err != nil {
		return fmt.Errorf("parse lifecycle model directory %q: %w", dir, err)
	}
	var failures []string
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			for _, declaration := range file.Decls {
				gen, ok := declaration.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					typeSpec := spec.(*ast.TypeSpec)
					if !typeSpec.Name.IsExported() {
						continue
					}
					declared, explicit := journalClasses[typeSpec.Name.Name]
					nonJournal := mechanicallyNonJournal(typeSpec)
					if explicit == nonJournal {
						failures = append(failures, typeSpec.Name.Name+" must be classified exactly once")
						continue
					}
					if explicit {
						failures = append(failures, shapeFailures(typeSpec, declared)...)
					}
				}
			}
		}
	}
	sort.Strings(failures)
	if len(failures) != 0 {
		return fmt.Errorf("lifecycle model classification failed: %v", failures)
	}
	return nil
}

func mechanicallyNonJournal(spec *ast.TypeSpec) bool {
	structure, ok := spec.Type.(*ast.StructType)
	if !ok {
		return true
	}
	for _, field := range structure.Fields.List {
		if len(field.Names) == 0 {
			if id, ok := field.Type.(*ast.Ident); ok && id.Name == "nonJournalValue" {
				return true
			}
		}
	}
	return false
}

func shapeFailures(spec *ast.TypeSpec, class JournalClass) []string {
	structure, ok := spec.Type.(*ast.StructType)
	if !ok {
		return []string{spec.Name.Name + " journal class must be a struct"}
	}
	hasSnapshot := false
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if name.Name == "SnapshotJournalID" {
				hasSnapshot = true
			}
		}
		if class == ImmutableSnapshot {
			if fieldType, ok := field.Type.(*ast.Ident); ok {
				if _, status := lifecycleStatusEnums[fieldType.Name]; status {
					return []string{spec.Name.Name + " immutable snapshot contains lifecycle status " + fieldType.Name}
				}
			}
		}
	}
	if class == DerivedProjection && !hasSnapshot {
		return []string{spec.Name.Name + " derived projection lacks SnapshotJournalID"}
	}
	return nil
}

func ModelDirectoryFromGuardPackage(guardDir string) string {
	return filepath.Clean(filepath.Join(guardDir, "..", "model"))
}
