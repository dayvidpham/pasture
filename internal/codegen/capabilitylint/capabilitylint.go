// Package capabilitylint implements a narrow static-analysis rule for the
// typed capability escape hatch
// (github.com/dayvidpham/pasture/internal/codegen/ir): CapabilityID is a
// named string type (ir.CapabilityID string), not an opaque struct like
// ir.SemanticOperationID/ir.SkillID/ir.EffectID, so Go's own
// untyped-literal-assignability rule lets a raw string literal compile
// successfully as a DefineCapability/MustDefineCapability identity argument.
// The ir package's opaque struct-based ID types reject that shape at
// compile time; CapabilityID cannot, by the accepted contract's own required
// shape (`const CapabilityRenderDiagram CapabilityID = "..."`, a named
// string constant, not an opaque type). This package closes that gap with a
// syntactic rule instead.
//
// Check is deny-by-default: at every call to
// ir.DefineCapability/ir.MustDefineCapability, the identity argument is
// rejected unless it is exactly one of three recognized-safe shapes:
//
//  1. An *ast.Ident that resolves — via go/parser's identifier resolution
//     (ast.Ident.Obj), which is scope-aware within the file, so a
//     function-local variable or parameter that merely shares its name with
//     a package-level const is correctly treated as a *different* object,
//     not the constant — to a package-level `const ... CapabilityID = ...`
//     (or `... ir.CapabilityID = ...`) declaration in the same file.
//
//  2. An *ast.Ident that resolves to a *parameter* of the enclosing function
//     or function literal, declared with type CapabilityID (or
//     ir.CapabilityID), and never disqualified anywhere in that function's
//     body (including inside a nested function literal that captures it —
//     go/parser resolves a captured identifier to the SAME *ast.Object as
//     its outer declaration): "forwarded verbatim" is enforced by three
//     checks (see collectReassignedObjs), not merely claimed. A parameter is
//     disqualified — for every use of that identifier in the function, not
//     only uses after the disqualifying statement — the moment it is (a)
//     the target of a plain or compound assignment (`id = ...`, `id +=
//     ...`); (b) the Key or Value of a `for ... = range ...` clause (a range
//     clause using `=`, not `:=`, is a distinct *ast.RangeStmt reassignment
//     shape, not an *ast.AssignStmt); or (c) had its address taken at all
//     (`&id`) anywhere — conservative and syntactic: this checker cannot
//     track what a resulting pointer is later written through, so taking
//     the address at all forfeits the verbatim-forwarding claim, whether or
//     not that pointer is ever actually used to mutate the value. A short
//     variable declaration (`:=`, including a range clause's `:=` form)
//     never disqualifies anything: it always introduces a new *ast.Object,
//     separately rejected by rule 1's scope-aware resolution, not a
//     reassignment of the parameter it may shadow.
//
//     A parameter's type already constrains it to CapabilityID at compile
//     time, and forwarding it without disqualification cannot itself
//     introduce a raw-literal/computed bypass at this call site — exactly
//     the accepted contract's own error-returning DefineCapability form, the
//     sanctioned escape valve for "dynamic or user-supplied inputs"
//     (MustDefineCapability's own required implementation, forwarding its id
//     parameter to DefineCapability, is the canonical example of this shape,
//     and — stated precisely — is the ONLY way to express that sanctioned
//     dynamic path as module source this rule accepts cleanly: a bare local
//     variable holding a dynamic value is denied by rule 1).
//
//     Disclosed residual risk (deliberately accepted, not an oversight — and
//     the sole disclosed residual on this rule; the range-clause and
//     address-of disqualification checks above close every other
//     empirically demonstrated in-body mutation shape): this allowance is
//     scoped to ANY function or function literal anywhere in the module
//     with a CapabilityID-typed parameter, not only ir.MustDefineCapability's
//     own definition, and it is one hop only — Check inspects arguments
//     only at call expressions whose callee is literally named
//     DefineCapability/MustDefineCapability (see capabilityConstructorName),
//     so a wrapper function's OWN call sites (e.g. `func Wrap(id
//     CapabilityID, ...) { DefineCapability(id, ...) };
//     Wrap(CapabilityID("raw-literal"), ...)`) are never inspected — Wrap's
//     body is lint-clean by this allowance, and Wrap("...") is invisible to
//     Check entirely. Nothing in this package's module-wide gate test
//     catches that either, since it is not a call to DefineCapability/
//     MustDefineCapability by name. This is an accepted trade-off, not a
//     bug: restricting the allowance to only ir.MustDefineCapability's own
//     definition would make the accepted contract's sanctioned dynamic path
//     inexpressible anywhere else in real module source, which is worse.
//     The backstop for this residual is runtime, not static: every
//     identity — laundered through a wrapper or not — still passes
//     DefineCapability's own validateCapabilityID check and the registry's
//     duplicate/changed-contract conflict detection on every code path, and
//     a CapabilityID-typed parameter is itself a reviewable, greppable
//     choke point even when this lint cannot follow its callers. Only a
//     genuinely dynamic, caller-supplied identity should ever take this
//     shape — never wrap an otherwise-static identity in a forwarding
//     function merely to silence rule 1; a static identity belongs in a
//     `const ... CapabilityID = ...` declaration, not behind a parameter.
//
//  3. An *ast.SelectorExpr (pkg.SomeCapabilityID) whose left-hand identifier
//     both (i) matches one of the file's actual imported package names
//     (derived from file.Imports: the import's explicit alias if present,
//     otherwise the import path's last segment) and (ii) is not itself bound
//     to a local declaration in scope at that point (an import qualifier's
//     *ast.Object is nil, or — belt and suspenders — of Kind ast.Pkg; a
//     shadowing local variable, parameter, or struct value always carries
//     its own non-nil, non-Pkg Object) — a conservative allowance for
//     legitimate cross-package reuse of an already-declared, correctly
//     typed constant, which this single-file syntactic check cannot itself
//     verify, made scope-aware the same way rule 1 is so an import name
//     shadowed by an ordinary local (a common, unremarkable pattern for
//     short names like "fmt" or "io") cannot launder an arbitrary
//     struct-field selector as if it were that import.
//
// Every other shape — a raw string literal (parenthesized or not), string
// concatenation or any other binary expression, an explicit CapabilityID(...)
// conversion, the result of an arbitrary function call, a struct-field or
// other selector that is not a genuine, unshadowed import qualifier, a
// non-parameter local variable (typed or not), a disqualified parameter
// (reassigned, ranged over with `=`, or address-taken), or any other
// expression — is a Finding, even when Go's own assignability
// rule for a named string type lets every one of those shapes compile
// successfully on its own.
//
// Recognition boundary (separate from the argument-shape rules above): Check
// only recognizes a call expression as a lint target when its callee
// resolves, syntactically, to the literal name DefineCapability or
// MustDefineCapability (through parentheses and an explicit generic
// instantiation — see capabilityConstructorName). Function-value aliasing
// (`f := ir.MustDefineCapability[In, Out]; f("raw-literal", ...)`) is a
// known, deliberately out-of-scope evasion of this recognition boundary: it
// would require dataflow tracking of function values, not a syntactic
// check, and is explicitly not part of the accepted contract for this rule.
// The same runtime backstop (validateCapabilityID, registry conflict
// detection) still applies to it.
//
// Check is intentionally syntactic — go/ast/go/parser/go/token only, no
// go/types, no module loading, no golang.org/x/tools — so this package adds
// no new module dependency. Its scope-aware resolution relies on go/parser's
// legacy (but still functional, and not removed) identifier resolution; see
// collectTypedConstObjs, collectCapabilityIDParamObjs, and
// collectImportNames for the precise mechanism and its limits.
package capabilitylint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

const (
	capabilityIDTypeName          = "CapabilityID"
	qualifiedCapabilityIDTypeName = "ir.CapabilityID"
	definePackageQualifier        = "ir"
	defineCapabilityFuncName      = "DefineCapability"
	mustDefineCapabilityFuncName  = "MustDefineCapability"
)

// Finding is one reported violation, with enough source position information
// to point a caller at the exact identity argument.
type Finding struct {
	Pos     token.Pos
	Message string
}

// CheckFile parses filename (src follows go/parser.ParseFile's src
// semantics: nil reads the file from disk; otherwise it may be a string,
// []byte, or io.Reader) and returns every canonical-definition-site
// violation Check finds.
//
// It deliberately does not pass parser.SkipObjectResolution: Check's
// scope-aware resolution (see collectTypedConstObjs, collectCapabilityIDParamObjs,
// and the scope-aware branch of rule 3 in checkIdentityArgument) depends on
// go/parser populating ast.Ident.Obj.
func CheckFile(fset *token.FileSet, filename string, src any) ([]Finding, error) {
	file, err := parser.ParseFile(fset, filename, src, parser.AllErrors)
	if err != nil {
		return nil, fmt.Errorf("capabilitylint: parse %s: %w", filename, err)
	}
	return Check(file), nil
}

// Check inspects one already-parsed file (see CheckFile's note on required
// parser.Mode) and returns every canonical-definition-site violation; see
// the package doc comment for the exact deny-by-default rule.
func Check(file *ast.File) []Finding {
	allowedObjs := collectTypedConstObjs(file)
	for obj := range collectCapabilityIDParamObjs(file) {
		allowedObjs[obj] = true
	}
	importNames := collectImportNames(file)

	var findings []Finding
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		funcName, ok := capabilityConstructorName(call.Fun)
		if !ok || len(call.Args) == 0 {
			return true
		}
		if finding, bad := checkIdentityArgument(funcName, call.Args[0], allowedObjs, importNames); bad {
			findings = append(findings, finding)
		}
		return true
	})
	return findings
}

// collectTypedConstObjs returns the set of *ast.Object pointers go/parser
// assigned to every `const <name> CapabilityID = ...` (or ir.CapabilityID)
// declared at package scope in file. Resolving by *ast.Object identity
// rather than by name is what makes checkIdentityArgument's *ast.Ident case
// scope-aware: a function-local variable or parameter that merely shares its
// name with one of these constants (Go allows silent shadowing via `:=` with
// no compiler warning) receives its own, different *ast.Object from
// go/parser, so it can never be mistaken for the package-level constant it
// shadows.
//
// Const specs that inherit their type from a preceding spec (Go's implicit
// iota-style repetition, where only the first ValueSpec in a block carries
// an explicit Type) are intentionally not resolved: the accepted contract's
// own required shape declares each capability identity individually with an
// explicit type, so requiring an explicit type on every spec keeps this
// resolution simple and avoids re-implementing iota propagation.
func collectTypedConstObjs(file *ast.File) map[*ast.Object]bool {
	objs := make(map[*ast.Object]bool)
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok || valueSpec.Type == nil || !isCapabilityIDTypeExpr(valueSpec.Type) {
				continue
			}
			for _, name := range valueSpec.Names {
				if name.Obj != nil {
					objs[name.Obj] = true
				}
			}
		}
	}
	return objs
}

// collectCapabilityIDParamObjs returns the set of *ast.Object pointers
// go/parser assigned to every parameter — of every *ast.FuncDecl and
// *ast.FuncLit in file — declared with type CapabilityID (or
// ir.CapabilityID) AND never disqualified anywhere in its owning function's
// body (see collectReassignedObjs for the three disqualification checks):
// "forwarded verbatim" (rule 2 in the package doc) is an enforced
// precondition, not merely a syntactic-shape check. Collecting across the
// whole file rather than tracking "the enclosing function of this specific
// call" during the walk is safe: go/parser's identifier resolution already
// scopes a parameter's *ast.Object to its own function (verified: two
// functions each declaring a same-named parameter get two distinct
// *ast.Object values, and a nested function literal that merely reads,
// reassigns, ranges over, or takes the address of a captured outer
// parameter resolves to that SAME outer *ast.Object, not a new one), so an
// *ast.Ident can only match an entry in this set by actually being bound to
// that exact, never-disqualified parameter declaration.
func collectCapabilityIDParamObjs(file *ast.File) map[*ast.Object]bool {
	reassigned := collectReassignedObjs(file)

	objs := make(map[*ast.Object]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		var params *ast.FieldList
		switch fn := node.(type) {
		case *ast.FuncDecl:
			params = fn.Type.Params
		case *ast.FuncLit:
			params = fn.Type.Params
		default:
			return true
		}
		if params == nil {
			return true
		}
		for _, field := range params.List {
			if !isCapabilityIDTypeExpr(field.Type) {
				continue
			}
			for _, name := range field.Names {
				if name.Obj != nil && !reassigned[name.Obj] {
					objs[name.Obj] = true
				}
			}
		}
		return true
	})
	return objs
}

// collectReassignedObjs returns the set of *ast.Object pointers that are
// disqualified from the parameter-forwarding allowance because their value
// is not provably the exact value the caller supplied, by any of three
// mechanisms this function checks anywhere in file — including inside a
// nested function literal, since go/parser resolves a captured identifier
// inside a closure to the SAME *ast.Object as its outer declaration
// (verified: a parameter reassigned, ranged over, or address-taken only
// inside a closure that captures it reports the identical *ast.Object as
// the parameter itself):
//
//  1. A plain assignment (`=` or a compound operator such as `+=`) —
//     *ast.AssignStmt with Tok != token.DEFINE — targeting the Object
//     directly.
//  2. A range-clause assignment — `for _, id = range xs { ... }`, an
//     *ast.RangeStmt with Tok == token.ASSIGN — rebinding the Object's Key
//     or Value on every iteration. This is a genuinely distinct AST node
//     from *ast.AssignStmt (go/ast does not represent a range clause as an
//     assignment statement), so it needs its own case.
//  3. Taking the Object's address at all (`&id`, an *ast.UnaryExpr with
//     Op == token.AND) — conservative and syntactic: tracking whatever the
//     resulting pointer's writes might later target is real dataflow
//     analysis, out of reach for this single-file syntactic checker, so
//     the address being taken at all forfeits the verbatim-forwarding
//     claim, regardless of whether that pointer is ever actually written
//     through.
//
// A short variable declaration (`:=`, including a range clause's Tok ==
// token.DEFINE form) is deliberately excluded from all three: it always
// introduces a new *ast.Object distinct from anything it might shadow
// (already handled, and separately rejected, by rule 1's scope-aware
// resolution), so it can never be mistaken for reassigning an existing
// parameter's Object.
func collectReassignedObjs(file *ast.File) map[*ast.Object]bool {
	reassigned := make(map[*ast.Object]bool)
	disqualify := func(expr ast.Expr) {
		if ident, ok := expr.(*ast.Ident); ok && ident.Obj != nil {
			reassigned[ident.Obj] = true
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AssignStmt:
			if n.Tok != token.DEFINE {
				for _, lhs := range n.Lhs {
					disqualify(lhs)
				}
			}
		case *ast.RangeStmt:
			if n.Tok == token.ASSIGN {
				disqualify(n.Key)
				disqualify(n.Value)
			}
		case *ast.UnaryExpr:
			if n.Op == token.AND {
				disqualify(n.X)
			}
		}
		return true
	})
	return reassigned
}

// collectImportNames returns the set of local names file's imports are
// reachable by: an import's explicit alias if present (including `_` and
// `.`, which can never legitimately match a SelectorExpr qualifier and are
// therefore harmless to include), otherwise the import path's last slash
// segment — the conventional derivation every simple (non-go/types) Go
// source tool uses, and the only one available without loading the imported
// package to read its true declared package name.
func collectImportNames(file *ast.File) map[string]bool {
	names := make(map[string]bool, len(file.Imports))
	for _, imp := range file.Imports {
		if imp.Name != nil {
			names[imp.Name.Name] = true
			continue
		}
		path := importPathValue(imp)
		if idx := lastSlash(path); idx >= 0 {
			path = path[idx+1:]
		}
		if path != "" {
			names[path] = true
		}
	}
	return names
}

func importPathValue(imp *ast.ImportSpec) string {
	value := imp.Path.Value
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return value[1 : len(value)-1]
	}
	return value
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

func isCapabilityIDTypeExpr(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == capabilityIDTypeName
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		return ok && pkg.Name == definePackageQualifier && t.Sel.Name == capabilityIDTypeName
	default:
		return false
	}
}

// capabilityConstructorName reports the bare constructor name
// ("DefineCapability" or "MustDefineCapability") a call expression's callee
// resolves to, unwrapping enclosing parentheses
// (`(ir.MustDefineCapability[In, Out])(...)`, a shape gofmt does not
// normalize away, exactly like the identity-argument case unparen already
// handles) and an explicit generic instantiation
// (ir.DefineCapability[In, Out] / DefineCapability[In, Out]) if present, and
// accepting both a package-qualified call (ir.DefineCapability(...), the
// normal external-caller shape) and a bare call (DefineCapability(...), the
// in-package/dot-import shape).
//
// This is a target-RECOGNITION boundary, not an argument-shape rule: a call
// is only ever inspected at all if its callee resolves, syntactically, to
// one of these two literal names. Aliasing the constructor to a function
// value (`f := ir.MustDefineCapability[In, Out]; f("raw-literal", ...)`) is
// a known, deliberately out-of-scope evasion of this boundary — see the
// package doc comment's "Recognition boundary" paragraph.
func capabilityConstructorName(fun ast.Expr) (string, bool) {
	switch f := unparen(fun).(type) {
	case *ast.IndexExpr:
		return capabilityConstructorName(f.X)
	case *ast.IndexListExpr:
		return capabilityConstructorName(f.X)
	case *ast.SelectorExpr:
		return matchConstructorName(f.Sel.Name)
	case *ast.Ident:
		return matchConstructorName(f.Name)
	default:
		return "", false
	}
}

func matchConstructorName(name string) (string, bool) {
	if name == defineCapabilityFuncName || name == mustDefineCapabilityFuncName {
		return name, true
	}
	return "", false
}

// unparen strips any number of enclosing parentheses so a parenthesized
// literal/conversion/expression is classified identically to its unwrapped
// form — gofmt does not remove redundant parens on its own, so
// `("acme.x/v1")` must be treated exactly like `"acme.x/v1"`.
func unparen(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.X
	}
}

// checkIdentityArgument is deny-by-default: it allows exactly the three
// recognized-safe shapes documented on the package, and returns a Finding
// for every other ast.Expr kind, including ones with no dedicated case below
// (the final default branch). allowedIdentObjs is the union of
// collectTypedConstObjs (package-level typed consts) and
// collectCapabilityIDParamObjs (CapabilityID-typed function/function-literal
// parameters) — the two *ast.Ident-resolvable safe shapes.
func checkIdentityArgument(funcName string, arg ast.Expr, allowedIdentObjs map[*ast.Object]bool, importNames map[string]bool) (Finding, bool) {
	arg = unparen(arg)

	switch expr := arg.(type) {
	case *ast.Ident:
		if expr.Obj != nil && allowedIdentObjs[expr.Obj] {
			return Finding{}, false
		}
		return Finding{Pos: expr.Pos(), Message: fmt.Sprintf(
			"what: %s's identity argument %q does not resolve to a package-level `const %s %s = ...` "+
				"declaration, or a never-disqualified %s-typed parameter of the enclosing function, in this "+
				"file; "+
				"why: canonical capability identities must be declared, typed, package-level constants, or "+
				"a typed parameter forwarded verbatim from the caller — not a local variable (even one "+
				"explicitly typed %s), not a value of unverified origin, and not a same-named local that "+
				"merely shadows a real package-level constant (Go allows silent shadowing with no compiler "+
				"warning); "+
				"where: capability identity argument of %s; "+
				"phase: capability identity lint; "+
				"impact: the capability's declaration site cannot be statically verified, and a shadowing "+
				"local identifier can silently defeat this rule with no diagnostic; "+
				"fix: declare `const %s %s = ...` at package scope in this file and reference it directly "+
				"(a same-package constant declared in a DIFFERENT file is not visible to this file-scoped "+
				"check — either duplicate the declaration in this file, or export it from another package "+
				"and reference it via the qualified `pkg.%s` selector form); only a genuinely dynamic, "+
				"caller-supplied identity should instead arrive as a %s-typed parameter (the "+
				"error-returning DefineCapability contract for dynamic inputs) — never wrap an otherwise-"+
				"static identity in a forwarding function merely to silence this rule",
			funcName, expr.Name, expr.Name, qualifiedCapabilityIDTypeName, qualifiedCapabilityIDTypeName,
			qualifiedCapabilityIDTypeName, funcName, expr.Name, qualifiedCapabilityIDTypeName, expr.Name, qualifiedCapabilityIDTypeName,
		)}, true

	case *ast.SelectorExpr:
		if pkg, ok := expr.X.(*ast.Ident); ok && importNames[pkg.Name] && isUnshadowedImportQualifier(pkg) {
			return Finding{}, false
		}
		return Finding{Pos: expr.Pos(), Message: fmt.Sprintf(
			"what: %s is called with a selector expression (%s) that is not a reference to one of this "+
				"file's imported packages; "+
				"why: an unqualified selector — a struct field, a method value, a locally shadowed import "+
				"name, or any other non-import selector — is a value of unverified origin, exactly like a "+
				"local variable (a local, parameter, or struct value can share an import's name — Go "+
				"permits this shadowing silently — so a name match on its own is not sufficient; this "+
				"selector's qualifier is checked against go/parser's scope resolution too, and does not "+
				"resolve to an actual import); "+
				"where: capability identity argument of %s; "+
				"phase: capability identity lint; "+
				"impact: the capability's declaration site cannot be statically verified; "+
				"fix: declare `const <Name> %s = ...` at package scope and pass <Name> to %s instead",
			funcName, selectorText(expr), funcName, qualifiedCapabilityIDTypeName, funcName,
		)}, true

	case *ast.BasicLit:
		if expr.Kind == token.STRING {
			return Finding{Pos: expr.Pos(), Message: fmt.Sprintf(
				"what: %s is called with a raw string literal capability identity %s; "+
					"why: CapabilityID is a named string type, so Go's untyped-literal assignability "+
					"lets a raw literal compile here even though it bypasses the canonical "+
					"declared-constant identity contract; "+
					"where: capability identity argument of %s; "+
					"phase: capability identity lint; "+
					"impact: the capability has no stable, greppable, typed declaration site, and "+
					"nothing prevents two unrelated call sites from silently diverging on the same "+
					"spelling; "+
					"fix: declare `const <Name> %s = %s` at package scope and pass <Name> to %s instead "+
					"of the literal",
				funcName, expr.Value, funcName, qualifiedCapabilityIDTypeName, expr.Value, funcName,
			)}, true
		}

	case *ast.BinaryExpr:
		return Finding{Pos: expr.Pos(), Message: fmt.Sprintf(
			"what: %s is called with a computed binary expression (e.g. string concatenation) as the "+
				"capability identity instead of a declared constant; "+
				"why: a computed expression has the same undeclared, ungreppable identity problem as a "+
				"raw literal — its value cannot be statically verified as the constant it appears to be; "+
				"where: capability identity argument of %s; "+
				"phase: capability identity lint; "+
				"impact: the capability has no canonical declaration site to review or reuse; "+
				"fix: declare `const <Name> %s = ...` at package scope with the full, final identity and "+
				"pass <Name> to %s instead of computing it inline",
			funcName, funcName, qualifiedCapabilityIDTypeName, funcName,
		)}, true

	case *ast.CallExpr:
		if conversionName, ok := capabilityIDConversionName(expr.Fun); ok {
			return Finding{Pos: expr.Pos(), Message: fmt.Sprintf(
				"what: %s is called with an inline %s(...) conversion instead of a declared constant; "+
					"why: an inline conversion has the same undeclared, ungreppable identity problem as a "+
					"raw literal — it just wraps the literal in the named type at the call site; "+
					"where: capability identity argument of %s; "+
					"phase: capability identity lint; "+
					"impact: the capability has no canonical declaration site to review or reuse; "+
					"fix: declare `const <Name> %s = ...` at package scope and pass <Name> to %s instead "+
					"of an inline conversion",
				funcName, conversionName, funcName, qualifiedCapabilityIDTypeName, funcName,
			)}, true
		}
		return Finding{Pos: expr.Pos(), Message: fmt.Sprintf(
			"what: %s is called with the result of a function/method call as the capability identity "+
				"instead of a declared constant; "+
				"why: a call result's value cannot be statically verified — it may return a different "+
				"identity on every invocation, and is exactly the 'value of unverified origin' this rule "+
				"exists to reject; "+
				"where: capability identity argument of %s; "+
				"phase: capability identity lint; "+
				"impact: the capability has no canonical, stable declaration site to review or reuse; "+
				"fix: declare `const <Name> %s = ...` at package scope with a literal identity and pass "+
				"<Name> to %s instead of calling a function for it",
			funcName, funcName, qualifiedCapabilityIDTypeName, funcName,
		)}, true
	}

	return Finding{Pos: arg.Pos(), Message: fmt.Sprintf(
		"what: %s is called with an identity argument of an unrecognized expression shape (%T) instead "+
			"of a declared constant; "+
			"why: this rule is deny-by-default — only a reference to a package-level `const ... %s = ...` "+
			"declaration, a never-disqualified %s-typed parameter of the enclosing function, or a qualified "+
			"reference to a constant declared in an imported package, is accepted; "+
			"where: capability identity argument of %s; "+
			"phase: capability identity lint; "+
			"impact: the capability's declaration site cannot be statically verified; "+
			"fix: declare `const <Name> %s = ...` at package scope and pass <Name> to %s instead",
		funcName, arg, qualifiedCapabilityIDTypeName, qualifiedCapabilityIDTypeName, funcName, qualifiedCapabilityIDTypeName, funcName,
	)}, true
}

func selectorText(expr *ast.SelectorExpr) string {
	if pkg, ok := expr.X.(*ast.Ident); ok {
		return pkg.Name + "." + expr.Sel.Name
	}
	return "<expr>." + expr.Sel.Name
}

// isUnshadowedImportQualifier reports whether ident, already confirmed to
// share its name with one of the file's imports (importNames[ident.Name]),
// is actually bound to that import rather than to a local declaration that
// merely shares its name. go/parser's legacy resolver never assigns an
// *ast.Object to a genuine package qualifier — verified empirically: in a
// file importing "fmt", the "fmt" in `fmt.Sprintf(...)` has a nil Obj, while
// a local `fmt := someStruct{...}` shadowing that name gets its own non-nil,
// Kind-Var Object, and every subsequent `fmt.Field` in that scope resolves
// to the SAME shadowing Object, not the import. The ast.Pkg check is
// defense in depth for the (currently unobserved) case where a future
// go/parser version does assign an Object to an import qualifier — that
// Object's Kind would be ast.Pkg, never anything a local declaration can
// produce.
func isUnshadowedImportQualifier(ident *ast.Ident) bool {
	return ident.Obj == nil || ident.Obj.Kind == ast.Pkg
}

func capabilityIDConversionName(fun ast.Expr) (string, bool) {
	switch f := fun.(type) {
	case *ast.Ident:
		if f.Name == capabilityIDTypeName {
			return f.Name, true
		}
	case *ast.SelectorExpr:
		if pkg, ok := f.X.(*ast.Ident); ok && pkg.Name == definePackageQualifier && f.Sel.Name == capabilityIDTypeName {
			return qualifiedCapabilityIDTypeName, true
		}
	}
	return "", false
}
