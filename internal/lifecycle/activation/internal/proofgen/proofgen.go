// Package proofgen writes the per-harness proof enum arms of the activation
// package from the proof declaration tables in the three harness target files.
//
// It reads SOURCE TEXT with go/ast and never imports the activation package.
// That is deliberate: a harness worker adds a target row that names a constant
// which does not exist yet, and the generator must still run on that tree to
// create it. A generator that imported the package would need the package to
// compile first, and the package cannot compile until the generator has run.
//
// Every arm carries an ordinal chosen by the harness file that declares it.
// Ordinals are partitioned per harness so that three workers can add arms to
// three files at once without colliding: Claude Code 1-99, Codex 100-199,
// OpenCode 200-299. Ordinal 0 is the invalid zero value of both enums and
// belongs to no harness. The generator refuses a duplicate ordinal, a
// duplicate arm name, an ordinal outside the range of the file that declares
// it, and any table shape it cannot read, and each refusal names the arms and
// files involved so the writer knows which row to change.
package proofgen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// Kind names one of the two proof enums.
type Kind string

const (
	KindCapture    Kind = "capture"
	KindProduction Kind = "production"
)

// tableTypeName is the element type of the declaration table that carries
// arms of this kind, as written in the target files.
func (k Kind) tableTypeName() string {
	switch k {
	case KindCapture:
		return "captureProofDeclaration"
	case KindProduction:
		return "productionProofDeclaration"
	default:
		return ""
	}
}

// enumName is the Go type the generated constants belong to.
func (k Kind) enumName() string {
	switch k {
	case KindCapture:
		return "CaptureProof"
	case KindProduction:
		return "ProductionProof"
	default:
		return ""
	}
}

// Harness binds one target file to its generated output and ordinal range.
type Harness struct {
	ID         ir.HarnessID
	Label      string
	TargetFile string
	Output     string
	Low, High  int
	// varPrefix is the identifier fragment of the generated per-harness arm
	// lists, so each harness file contributes its own list and the hand-written
	// package code concatenates them.
	varPrefix string
}

// Harnesses is the closed table of harness files the generator reads. A new
// harness is added here, with its own range, in the change that reviews it.
var Harnesses = []Harness{
	{ID: ir.HarnessClaudeCode, Label: "Claude Code", TargetFile: "claude_targets.go", Output: "proofs_claude.gen.go", Low: 1, High: 99, varPrefix: "claude"},
	{ID: ir.HarnessCodex, Label: "Codex", TargetFile: "codex_targets.go", Output: "proofs_codex.gen.go", Low: 100, High: 199, varPrefix: "codex"},
	{ID: ir.HarnessOpenCode, Label: "OpenCode", TargetFile: "opencode_targets.go", Output: "proofs_opencode.gen.go", Low: 200, High: 299, varPrefix: "openCode"},
}

// Arm is one proof arm read from a declaration table.
type Arm struct {
	Kind    Kind
	Name    string
	Ordinal int
	File    string
	Line    int
}

// Constant is the Go identifier of the generated constant for this arm.
func (a Arm) Constant() string { return a.Kind.enumName() + a.Name }

// Parse reads every capture and production proof arm declared in one target
// file. It refuses a file with no table of either kind and any row it cannot
// read as an ordinal plus an arm name.
func Parse(path string) ([]Arm, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("proofgen: parse %s: %w", path, err)
	}
	base := filepath.Base(path)
	found := map[Kind]bool{}
	var arms []Arm
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			// A table is written `var x = [...]T{...}`, so its element type sits
			// on the composite literal, not on the spec; accept either spelling.
			typeExpr := value.Type
			if typeExpr == nil && len(value.Values) == 1 {
				if lit, ok := value.Values[0].(*ast.CompositeLit); ok {
					typeExpr = lit.Type
				}
			}
			kind, ok := tableKind(typeExpr)
			if !ok {
				continue
			}
			if found[kind] {
				return nil, fmt.Errorf("proofgen: %s declares two %s proof tables; keep exactly one table per kind per harness file", base, kind)
			}
			found[kind] = true
			if len(value.Values) != 1 {
				return nil, fmt.Errorf("proofgen: %s line %d: the %s proof table must be one composite literal", base, fset.Position(value.Pos()).Line, kind)
			}
			table, ok := value.Values[0].(*ast.CompositeLit)
			if !ok {
				return nil, fmt.Errorf("proofgen: %s line %d: the %s proof table must be one composite literal", base, fset.Position(value.Pos()).Line, kind)
			}
			for _, element := range table.Elts {
				arm, err := parseArm(fset, base, kind, element)
				if err != nil {
					return nil, err
				}
				arms = append(arms, arm)
			}
		}
	}
	for _, kind := range []Kind{KindCapture, KindProduction} {
		if !found[kind] {
			return nil, fmt.Errorf("proofgen: %s declares no %s proof table (a var of element type %s); every harness file declares both tables, empty or not", base, kind, kind.tableTypeName())
		}
	}
	return arms, nil
}

func tableKind(expr ast.Expr) (Kind, bool) {
	array, ok := expr.(*ast.ArrayType)
	if !ok {
		return "", false
	}
	ident, ok := array.Elt.(*ast.Ident)
	if !ok {
		return "", false
	}
	for _, kind := range []Kind{KindCapture, KindProduction} {
		if ident.Name == kind.tableTypeName() {
			return kind, true
		}
	}
	return "", false
}

func parseArm(fset *token.FileSet, base string, kind Kind, element ast.Expr) (Arm, error) {
	row, ok := element.(*ast.CompositeLit)
	line := fset.Position(element.Pos()).Line
	if !ok {
		return Arm{}, fmt.Errorf("proofgen: %s line %d: a %s proof row must be a keyed composite literal", base, line, kind)
	}
	arm := Arm{Kind: kind, File: base, Line: line}
	haveOrdinal, haveName := false, false
	for _, field := range row.Elts {
		pair, ok := field.(*ast.KeyValueExpr)
		if !ok {
			return Arm{}, fmt.Errorf("proofgen: %s line %d: a %s proof row must use keyed fields (ordinal: N, arm: \"Name\")", base, line, kind)
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "ordinal":
			lit, ok := pair.Value.(*ast.BasicLit)
			if !ok || lit.Kind != token.INT {
				return Arm{}, fmt.Errorf("proofgen: %s line %d: the ordinal of a %s proof row must be an integer literal, because the generator reads source text and cannot evaluate an expression", base, line, kind)
			}
			ordinal, err := strconv.Atoi(lit.Value)
			if err != nil {
				return Arm{}, fmt.Errorf("proofgen: %s line %d: ordinal %s is not a decimal integer", base, line, lit.Value)
			}
			arm.Ordinal, haveOrdinal = ordinal, true
		case "arm":
			lit, ok := pair.Value.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return Arm{}, fmt.Errorf("proofgen: %s line %d: the arm of a %s proof row must be a string literal", base, line, kind)
			}
			name, err := strconv.Unquote(lit.Value)
			if err != nil {
				return Arm{}, fmt.Errorf("proofgen: %s line %d: arm %s is not a Go string literal", base, line, lit.Value)
			}
			arm.Name, haveName = name, true
		}
	}
	if !haveOrdinal || !haveName {
		return Arm{}, fmt.Errorf("proofgen: %s line %d: a %s proof row needs both an ordinal and an arm name", base, line, kind)
	}
	if !token.IsIdentifier(arm.Name) || !token.IsExported(arm.Name) {
		return Arm{}, fmt.Errorf("proofgen: %s line %d: arm name %q must be an exported Go identifier fragment, because it becomes the constant %s%s", base, line, arm.Name, kind.enumName(), arm.Name)
	}
	return arm, nil
}

// Generate reads every harness file under dir, validates the arms across all
// of them, and returns the generated source per output file name.
func Generate(dir string) (map[string][]byte, error) {
	byHarness := make(map[ir.HarnessID][]Arm, len(Harnesses))
	var all []Arm
	for _, harness := range Harnesses {
		arms, err := Parse(filepath.Join(dir, harness.TargetFile))
		if err != nil {
			return nil, err
		}
		if err := refuseEmptyTables(harness, arms); err != nil {
			return nil, err
		}
		for _, arm := range arms {
			if arm.Ordinal == 0 {
				return nil, fmt.Errorf("proofgen: %s proof arm %q in %s has ordinal 0, which is reserved for the invalid zero value; choose an ordinal inside the %s range %d-%d", arm.Kind, arm.Name, arm.File, harness.Label, harness.Low, harness.High)
			}
			if arm.Ordinal < harness.Low || arm.Ordinal > harness.High {
				return nil, fmt.Errorf("proofgen: %s proof arm %q in %s has ordinal %d outside the %s range %d-%d; choose an ordinal inside the range of the harness that owns the file", arm.Kind, arm.Name, arm.File, arm.Ordinal, harness.Label, harness.Low, harness.High)
			}
		}
		byHarness[harness.ID] = arms
		all = append(all, arms...)
	}
	if err := refuseCollisions(all); err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(Harnesses))
	for _, harness := range Harnesses {
		source, err := render(harness, byHarness[harness.ID])
		if err != nil {
			return nil, err
		}
		out[harness.Output] = source
	}
	return out, nil
}

// refuseEmptyTables is the control against a silent empty generation. If the
// source walk stopped matching the tables, Parse would return no arms, the
// generator would emit empty proof files, the drift gate would stay green and
// every ordinal-range assertion would pass over an empty set. So a harness
// file must yield at least one arm of EACH kind: every shipped harness has at
// least one enabled event, and an enabled event carries both proofs.
func refuseEmptyTables(harness Harness, arms []Arm) error {
	counts := map[Kind]int{}
	for _, arm := range arms {
		counts[arm.Kind]++
	}
	for _, kind := range []Kind{KindCapture, KindProduction} {
		if counts[kind] == 0 {
			return fmt.Errorf("proofgen: %s declares no %s proof arm at all; a harness file must declare at least one arm of each kind, so either the table is empty or the source walk no longer matches its rows, and an empty generation would pass every range check over nothing", harness.TargetFile, kind)
		}
	}
	return nil
}

func refuseCollisions(arms []Arm) error {
	type ordinalKey struct {
		kind    Kind
		ordinal int
	}
	type nameKey struct {
		kind Kind
		name string
	}
	byOrdinal := map[ordinalKey]Arm{}
	byName := map[nameKey]Arm{}
	for _, arm := range arms {
		if first, taken := byOrdinal[ordinalKey{arm.Kind, arm.Ordinal}]; taken {
			return fmt.Errorf("proofgen: %s proof ordinal %d is declared twice: arm %q in %s and arm %q in %s; every arm needs its own ordinal inside its harness range", arm.Kind, arm.Ordinal, first.Name, first.File, arm.Name, arm.File)
		}
		byOrdinal[ordinalKey{arm.Kind, arm.Ordinal}] = arm
		if first, taken := byName[nameKey{arm.Kind, arm.Name}]; taken {
			return fmt.Errorf("proofgen: %s proof arm %q is declared twice: in %s and in %s; arm names are one namespace across the harness files because each becomes one constant", arm.Kind, arm.Name, first.File, arm.File)
		}
		byName[nameKey{arm.Kind, arm.Name}] = arm
	}
	return nil
}

func render(harness Harness, arms []Arm) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by \"go generate\"; DO NOT EDIT.\n")
	fmt.Fprintf(&b, "// Source: the proof declaration tables in %s.\n\n", harness.TargetFile)
	fmt.Fprintf(&b, "package activation\n\n")
	for _, kind := range []Kind{KindCapture, KindProduction} {
		var of []Arm
		for _, arm := range arms {
			if arm.Kind == kind {
				of = append(of, arm)
			}
		}
		sort.SliceStable(of, func(i, j int) bool { return of[i].Ordinal < of[j].Ordinal })
		listName := harness.varPrefix + "Generated" + kind.enumName() + "s"
		fmt.Fprintf(&b, "// %s %s proofs, ordinals %d-%d.\n", harness.Label, kind, harness.Low, harness.High)
		if len(of) > 0 {
			fmt.Fprintf(&b, "const (\n")
			for _, arm := range of {
				fmt.Fprintf(&b, "\t%s %s = %d\n", arm.Constant(), kind.enumName(), arm.Ordinal)
			}
			fmt.Fprintf(&b, ")\n\n")
		}
		fmt.Fprintf(&b, "// %s lists the generated %s proof arms of %s by name, in ordinal order.\n", listName, kind, harness.TargetFile)
		fmt.Fprintf(&b, "var %s = []named%s{\n", listName, kind.enumName())
		for _, arm := range of {
			fmt.Fprintf(&b, "\t{name: %q, proof: %s},\n", arm.Name, arm.Constant())
		}
		fmt.Fprintf(&b, "}\n\n")
	}
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("proofgen: gofmt the generated %s: %w", harness.Output, err)
	}
	return formatted, nil
}

// Write generates and writes every output file under dir. It writes a file
// only when its bytes change, so an unchanged tree stays untouched.
func Write(dir string) error {
	files, err := Generate(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, name)
		if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, files[name]) {
			continue
		}
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			return fmt.Errorf("proofgen: write %s: %w", path, err)
		}
	}
	return nil
}
