package ir

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// SourceRange is a half-open byte range in one owned part source.
type SourceRange struct {
	Start int
	Stop  int
}

// Location retains canonical owner and source coordinates for diagnostics.
type Location struct {
	owner   string
	file    string
	section string
	range_  SourceRange
}

func NewLocation(owner, file, section string, sourceRange SourceRange) (Location, error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(owner) != owner ||
		strings.TrimSpace(file) == "" || strings.TrimSpace(file) != file ||
		strings.TrimSpace(section) == "" || strings.TrimSpace(section) != section ||
		!utf8.ValidString(owner) || !utf8.ValidString(file) || !utf8.ValidString(section) {
		return Location{}, documentError("location omits owner, file, or section", "every diagnostic needs a stable semantic owner and source coordinate", "provide non-empty owner, file, and section values", nil)
	}
	if !validRelativePath(file) {
		return Location{}, documentError(fmt.Sprintf("location file %q is not a safe relative path", file), "portable sources cannot name absolute or escaping paths", "use a normalized slash-separated relative path", nil)
	}
	if sourceRange.Start < 0 || sourceRange.Stop < sourceRange.Start {
		return Location{}, documentError("location source range is invalid", "source coordinates use a nonnegative half-open range", "set 0 <= Start <= Stop", nil)
	}
	return Location{owner: owner, file: file, section: section, range_: sourceRange}, nil
}

func (l Location) Owner() string      { return l.owner }
func (l Location) File() string       { return l.file }
func (l Location) Section() string    { return l.section }
func (l Location) Range() SourceRange { return l.range_ }
func (l Location) IsValid() bool {
	return l.owner != "" && l.section != "" && validRelativePath(l.file) && l.range_.Start >= 0 && l.range_.Stop >= l.range_.Start
}

// Part is a closed document sum. Concrete parts are immutable and cannot be
// implemented outside this package.
type Part interface {
	documentPart()
	partLocation() Location
	validatePart() error
}

type markdownPart struct {
	location Location
	source   []byte
	tree     ast.Node
	ranges   []SourceRange
}

type operationPart struct {
	location  Location
	operation SemanticOperation
}

type verbatimPart struct {
	location Location
	content  []byte
}

type targetLiteralPart struct {
	location Location
	cases    map[HarnessID]targetCaseValue
}

func (markdownPart) documentPart()      {}
func (operationPart) documentPart()     {}
func (verbatimPart) documentPart()      {}
func (targetLiteralPart) documentPart() {}

func (p markdownPart) partLocation() Location      { return p.location }
func (p operationPart) partLocation() Location     { return p.location }
func (p verbatimPart) partLocation() Location      { return p.location }
func (p targetLiteralPart) partLocation() Location { return p.location }

// Markdown parses source exactly once through Goldmark and retains the owned
// source plus AST coordinates in the returned opaque part.
func Markdown(source []byte, at Location) (Part, error) {
	if !at.IsValid() {
		return nil, documentError("Markdown location is zero or invalid", "parsed source must retain an actionable owner and range", "construct the location with NewLocation", nil)
	}
	owned := append([]byte(nil), source...)
	if len(owned) == 0 || !utf8.Valid(owned) {
		return nil, documentError("Markdown source is empty or invalid UTF-8", "portable Markdown must be exact valid text before Goldmark parsing", "supply non-empty valid UTF-8 Markdown", nil)
	}
	if at.range_.Stop > len(owned) {
		return nil, documentError(fmt.Sprintf("Markdown location stops at %d beyond %d source bytes", at.range_.Stop, len(owned)), "source coordinates must refer to the owned immutable buffer", "adjust the location range to the supplied source", nil)
	}
	tree := goldmark.New().Parser().Parse(text.NewReader(owned))
	ranges := collectASTRanges(tree)
	return markdownPart{location: at, source: owned, tree: tree, ranges: ranges}, nil
}

func collectASTRanges(tree ast.Node) []SourceRange {
	var ranges []SourceRange
	_ = ast.Walk(tree, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || node.Type() != ast.TypeBlock {
			return ast.WalkContinue, nil
		}
		lines := node.Lines()
		for index := 0; index < lines.Len(); index++ {
			segment := lines.At(index)
			ranges = append(ranges, SourceRange{Start: segment.Start, Stop: segment.Stop})
		}
		return ast.WalkContinue, nil
	})
	return ranges
}

// Operation inserts one already validated protocol-semantic operation.
func Operation(operation SemanticOperation, at Location) (Part, error) {
	if !at.IsValid() {
		return nil, documentError("operation location is zero or invalid", "semantic diagnostics require exact source ownership", "construct the location with NewLocation", nil)
	}
	normalized, err := normalizeSemanticOperation(operation)
	if err != nil {
		return nil, documentError("semantic operation is invalid", "operands, result slots, and scope must validate before entering a document", "construct the operation through its typed constructor", err)
	}
	return operationPart{location: at, operation: normalized}, nil
}

// Verbatim inserts portable text without inferring operational meaning.
func Verbatim(content []byte, at Location) (Part, error) {
	if !at.IsValid() {
		return nil, documentError("verbatim location is zero or invalid", "portable text still needs source ownership", "construct the location with NewLocation", nil)
	}
	owned := append([]byte(nil), content...)
	if len(owned) == 0 || !utf8.Valid(owned) {
		return nil, documentError("verbatim content is empty or invalid UTF-8", "portable target output is exact text", "supply non-empty valid UTF-8 content", nil)
	}
	return verbatimPart{location: at, content: owned}, nil
}

// TargetCase is a closed exhaustive target-literal case.
type TargetCase interface{ targetCase() }

type targetCaseValue struct {
	harness     HarnessID
	contract    RuntimeContractID
	content     []byte
	reason      string
	unsupported bool
}

func (targetCaseValue) targetCase() {}

func NewRuntimeContractID(harness HarnessID, name string) (RuntimeContractID, error) {
	if !harness.IsValid() {
		return "", documentError(fmt.Sprintf("runtime contract harness %q is unknown", harness), "contracts bind one enabled harness", "use a known HarnessID", nil)
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\t\r\n") {
		return "", documentError("runtime contract name is empty, padded, or multiline", "contract identities require one exact spelling", "supply a non-empty single-line name", nil)
	}
	prefix := string(harness) + "/"
	if strings.HasPrefix(name, prefix) {
		return RuntimeContractID(name), nil
	}
	return RuntimeContractID(prefix + name), nil
}

func runtimeContractHarness(contract RuntimeContractID) (HarnessID, bool) {
	value := string(contract)
	for _, harness := range canonicalHarnessIDs {
		if strings.HasPrefix(value, string(harness)+"/") && len(value) > len(harness)+1 {
			return harness, true
		}
	}
	return "", false
}

// LiteralFor constructs a contract-bound literal. Its harness is derived from
// the RuntimeContractID constructed by NewRuntimeContractID.
func LiteralFor(contract RuntimeContractID, content []byte, reason string) (TargetCase, error) {
	harness, ok := runtimeContractHarness(contract)
	if !ok {
		return nil, documentError(fmt.Sprintf("runtime contract %q has no enabled harness prefix", contract), "literal selection must be exhaustive by harness", "construct the contract with NewRuntimeContractID", nil)
	}
	return LiteralForHarness(harness, contract, content, reason)
}

// LiteralForHarness constructs a case while explicitly checking the harness.
func LiteralForHarness(harness HarnessID, contract RuntimeContractID, content []byte, reason string) (TargetCase, error) {
	derived, ok := runtimeContractHarness(contract)
	if !harness.IsValid() || !ok || derived != harness {
		return nil, documentError("literal harness and runtime contract do not match", "native syntax is valid only for its reviewed contract", "use the harness encoded by NewRuntimeContractID", nil)
	}
	owned := append([]byte(nil), content...)
	if len(owned) == 0 || !utf8.Valid(owned) || strings.TrimSpace(reason) == "" {
		return nil, documentError("contract-bound literal has empty/invalid content or reason", "raw native syntax requires exact text and an auditable justification", "supply valid UTF-8 content and a non-empty reason", nil)
	}
	return targetCaseValue{harness: harness, contract: contract, content: owned, reason: reason}, nil
}

func LiteralUnsupported(harness HarnessID, reason string) (TargetCase, error) {
	if !harness.IsValid() || strings.TrimSpace(reason) == "" || !utf8.ValidString(reason) {
		return nil, documentError("unsupported literal case has an invalid harness or reason", "every enabled harness needs an explicit auditable disposition", "supply a known harness and non-empty UTF-8 reason", nil)
	}
	return targetCaseValue{harness: harness, reason: reason, unsupported: true}, nil
}

// TargetLiteral validates exactly one case for every enabled harness.
func TargetLiteral(cases []TargetCase, at Location) (Part, error) {
	if !at.IsValid() {
		return nil, documentError("target-literal location is zero or invalid", "native escapes require exact review coordinates", "construct the location with NewLocation", nil)
	}
	byHarness := make(map[HarnessID]targetCaseValue, len(cases))
	for index, candidate := range cases {
		value, ok := candidate.(targetCaseValue)
		if !ok {
			if pointer, pointerOK := candidate.(*targetCaseValue); pointerOK && pointer != nil {
				value, ok = *pointer, true
			}
		}
		if !ok || !value.harness.IsValid() || strings.TrimSpace(value.reason) == "" {
			return nil, documentError(fmt.Sprintf("target case %d is zero or unknown", index), "target literals are a closed exhaustive sum", "construct cases with LiteralFor or LiteralUnsupported", nil)
		}
		if _, duplicate := byHarness[value.harness]; duplicate {
			return nil, documentError(fmt.Sprintf("target literal duplicates harness %q", value.harness), "each enabled harness needs exactly one disposition", "remove the duplicate case", nil)
		}
		byHarness[value.harness] = targetCaseValue{
			harness: value.harness, contract: value.contract,
			content: append([]byte(nil), value.content...), reason: value.reason, unsupported: value.unsupported,
		}
	}
	for _, harness := range canonicalHarnessIDs {
		if _, exists := byHarness[harness]; !exists {
			return nil, documentError(fmt.Sprintf("target literal omits enabled harness %q", harness), "raw native syntax must be exhaustive and cannot silently fall back", "add LiteralFor or LiteralUnsupported for the harness", nil)
		}
	}
	if len(byHarness) != len(canonicalHarnessIDs) {
		return nil, documentError("target literal includes an out-of-range harness", "the portable target set is closed", "remove cases outside AllHarnessIDs", nil)
	}
	return targetLiteralPart{location: at, cases: byHarness}, nil
}

func (p markdownPart) validatePart() error {
	if !p.location.IsValid() || len(p.source) == 0 || p.tree == nil || !utf8.Valid(p.source) {
		return documentError("Markdown part is zero or corrupt", "owned source, Goldmark AST, and location must remain complete", "construct it with Markdown", nil)
	}
	return nil
}
func (p operationPart) validatePart() error {
	if !p.location.IsValid() {
		return documentError("operation part location is invalid", "semantic diagnostics require exact ownership", "construct it with Operation", nil)
	}
	_, err := canonicalSemanticOperation(p.operation)
	return err
}
func (p verbatimPart) validatePart() error {
	if !p.location.IsValid() || len(p.content) == 0 || !utf8.Valid(p.content) {
		return documentError("verbatim part is zero or corrupt", "portable text must retain immutable valid UTF-8 bytes", "construct it with Verbatim", nil)
	}
	return nil
}
func (p targetLiteralPart) validatePart() error {
	if !p.location.IsValid() || len(p.cases) != len(canonicalHarnessIDs) {
		return documentError("target-literal part is zero or non-exhaustive", "native escapes require one disposition per enabled harness", "construct it with TargetLiteral", nil)
	}
	for _, harness := range canonicalHarnessIDs {
		if _, ok := p.cases[harness]; !ok {
			return documentError(fmt.Sprintf("target-literal part omits %q", harness), "native escapes cannot use implicit fallback", "reconstruct it with an exhaustive case set", nil)
		}
	}
	return nil
}

// PartLocation returns a copy of a part's retained source location.
func PartLocation(part Part) (Location, error) {
	if part == nil {
		return Location{}, documentError("part is nil", "nil has no source location", "supply a constructor-produced Part", nil)
	}
	return part.partLocation(), nil
}

// MarkdownSourceRanges returns defensive AST-derived source ranges.
func MarkdownSourceRanges(part Part) ([]SourceRange, error) {
	value, ok := part.(markdownPart)
	if !ok {
		if pointer, pointerOK := part.(*markdownPart); pointerOK && pointer != nil {
			value, ok = *pointer, true
		}
	}
	if !ok {
		return nil, documentError(fmt.Sprintf("part %T is not Markdown", part), "AST source ranges exist only for Goldmark-backed Markdown parts", "pass the value returned by Markdown", nil)
	}
	return append([]SourceRange(nil), value.ranges...), nil
}

// Document is an immutable validated sequence of opaque parts.
type Document struct{ parts []Part }

func NewDocument(parts ...Part) (Document, error) {
	if len(parts) == 0 {
		return Document{}, documentError("document has no parts", "a compilable document needs at least one explicit semantic/prose part", "supply constructor-produced parts", nil)
	}
	owned := make([]Part, len(parts))
	for index, part := range parts {
		if part == nil {
			return Document{}, documentError(fmt.Sprintf("document part %d is nil", index), "the part sum has no nil variant", "replace it with Markdown, Operation, Verbatim, or TargetLiteral", nil)
		}
		if err := part.validatePart(); err != nil {
			return Document{}, documentError(fmt.Sprintf("document part %d is invalid", index), "documents exist only after all invariants pass", "reconstruct the failing part", err)
		}
		owned[index] = clonePart(part)
	}
	return Document{parts: owned}, nil
}

func clonePart(part Part) Part {
	switch value := part.(type) {
	case markdownPart:
		value.source = append([]byte(nil), value.source...)
		value.ranges = append([]SourceRange(nil), value.ranges...)
		return value
	case operationPart:
		if request, ok := value.operation.(RequestUserDecision); ok {
			validated, err := validatedRequest(request)
			if err == nil {
				value.operation = validated
			}
		}
		return value
	case verbatimPart:
		value.content = append([]byte(nil), value.content...)
		return value
	case targetLiteralPart:
		cloned := make(map[HarnessID]targetCaseValue, len(value.cases))
		for harness, targetCase := range value.cases {
			targetCase.content = append([]byte(nil), targetCase.content...)
			cloned[harness] = targetCase
		}
		value.cases = cloned
		return value
	default:
		return part
	}
}

func (d Document) Len() int { return len(d.parts) }

// OperationLowerer converts semantic intent for one runtime contract. Native
// function names belong only inside implementations supplied by the runtime layer.
type OperationLowerer interface {
	Lower(operation SemanticOperation, at Location) ([]byte, error)
}

type OperationLowererFunc func(operation SemanticOperation, at Location) ([]byte, error)

func (f OperationLowererFunc) Lower(operation SemanticOperation, at Location) ([]byte, error) {
	return f(operation, at)
}

// NativeValidator validates a complete in-memory tree against a target loader
// or schema without publishing files.
type NativeValidator interface{ Validate(RenderedTree) error }

type NativeValidatorFunc func(RenderedTree) error

func (f NativeValidatorFunc) Validate(tree RenderedTree) error { return f(tree) }

// Target is one validated in-memory compilation profile.
type Target struct {
	harness    HarnessID
	contract   RuntimeContractID
	outputPath string
	lowerer    OperationLowerer
	validators []NativeValidator
}

func NewTarget(
	harness HarnessID,
	contract RuntimeContractID,
	outputPath string,
	lowerer OperationLowerer,
	validators ...NativeValidator,
) (Target, error) {
	derived, ok := runtimeContractHarness(contract)
	if !harness.IsValid() || !ok || derived != harness {
		return Target{}, compileError("target harness and runtime contract are invalid or mismatched", "target selection must use one reviewed harness-bound contract", Location{}, "target validation", "construct the contract with NewRuntimeContractID for this harness", nil)
	}
	if !validRelativePath(outputPath) {
		return Target{}, compileError(fmt.Sprintf("target output path %q is unsafe", outputPath), "RenderedTree contains only normalized relative files", Location{}, "target validation", "use a slash-separated relative path", nil)
	}
	ownedValidators := append([]NativeValidator(nil), validators...)
	for index, validator := range ownedValidators {
		if validator == nil {
			return Target{}, compileError(fmt.Sprintf("native validator %d is nil", index), "every declared validation stage must be executable", Location{}, "target validation", "remove nil validators", nil)
		}
	}
	return Target{harness: harness, contract: contract, outputPath: outputPath, lowerer: lowerer, validators: ownedValidators}, nil
}

func (t Target) Harness() HarnessID                 { return t.harness }
func (t Target) RuntimeContract() RuntimeContractID { return t.contract }
func (t Target) OutputPath() string                 { return t.outputPath }

// RenderedFile is an immutable relative file.
type RenderedFile struct {
	path    string
	content []byte
}

func NewRenderedFile(relativePath string, content []byte) (RenderedFile, error) {
	if !validRelativePath(relativePath) || len(content) == 0 {
		return RenderedFile{}, compileError(fmt.Sprintf("rendered file %q has an unsafe path or empty content", relativePath), "complete trees require immutable non-empty relative files", Location{}, "tree validation", "use a normalized relative path and non-empty content", nil)
	}
	return RenderedFile{path: relativePath, content: append([]byte(nil), content...)}, nil
}

func (f RenderedFile) Path() string    { return f.path }
func (f RenderedFile) Content() []byte { return append([]byte(nil), f.content...) }

// RenderedTree is one complete immutable set of validated relative files.
type RenderedTree struct{ files map[string]RenderedFile }

func NewRenderedTree(files ...RenderedFile) (RenderedTree, error) {
	if len(files) == 0 {
		return RenderedTree{}, compileError("rendered tree has no files", "successful compilation returns one complete output set", Location{}, "tree validation", "supply at least one validated RenderedFile", nil)
	}
	owned := make(map[string]RenderedFile, len(files))
	for index, file := range files {
		if !validRelativePath(file.path) || len(file.content) == 0 {
			return RenderedTree{}, compileError(fmt.Sprintf("rendered file %d is zero or invalid", index), "forged files cannot enter an immutable tree", Location{}, "tree validation", "construct each file with NewRenderedFile", nil)
		}
		if _, duplicate := owned[file.path]; duplicate {
			return RenderedTree{}, compileError(fmt.Sprintf("rendered path %q is duplicated", file.path), "one complete tree has exactly one content value per path", Location{}, "tree validation", "deduplicate output paths", nil)
		}
		file.content = append([]byte(nil), file.content...)
		owned[file.path] = file
	}
	return RenderedTree{files: owned}, nil
}

func (t RenderedTree) Paths() []string {
	paths := make([]string, 0, len(t.files))
	for filePath := range t.files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	return paths
}

func (t RenderedTree) File(filePath string) (RenderedFile, bool) {
	file, ok := t.files[filePath]
	if !ok {
		return RenderedFile{}, false
	}
	file.content = append([]byte(nil), file.content...)
	return file, true
}

func (t RenderedTree) Len() int { return len(t.files) }

// Compile validates and renders entirely in memory. On every error it returns
// the zero RenderedTree; this package has no filesystem publisher dependency.
func Compile(document Document, target Target) (RenderedTree, error) {
	if len(document.parts) == 0 {
		return RenderedTree{}, compileError("document is zero or empty", "only NewDocument establishes part invariants", Location{}, "document validation", "construct the document with NewDocument", nil)
	}
	derived, ok := runtimeContractHarness(target.contract)
	if !target.harness.IsValid() || !ok || derived != target.harness || !validRelativePath(target.outputPath) {
		return RenderedTree{}, compileError("target is zero, invalid, or internally mismatched", "only NewTarget establishes a complete runtime selection", Location{}, "target validation", "construct the target with NewTarget", nil)
	}
	for index, part := range document.parts {
		if part == nil {
			return RenderedTree{}, compileError(fmt.Sprintf("document part %d is nil", index), "nil has no portable rendering", Location{}, "document validation", "reconstruct the document", nil)
		}
		if err := part.validatePart(); err != nil {
			return RenderedTree{}, compileError(fmt.Sprintf("document part %d failed validation", index), "all semantic and source invariants must pass before rendering", part.partLocation(), "document validation", "reconstruct the failing part", err)
		}
	}
	var output bytes.Buffer
	for _, part := range document.parts {
		switch value := part.(type) {
		case markdownPart:
			_, _ = output.Write(value.source)
		case verbatimPart:
			_, _ = output.Write(value.content)
		case operationPart:
			if target.lowerer == nil {
				return RenderedTree{}, compileError("target has no semantic operation lowerer", "operations cannot be guessed from prose or runtime-like names", value.location, "operation lowering", "provide an exhaustive version-bounded OperationLowerer", nil)
			}
			rendered, err := target.lowerer.Lower(value.operation, value.location)
			if err != nil {
				return RenderedTree{}, compileError("semantic operation lowering failed", "the selected runtime contract could not preserve the operation", value.location, "operation lowering", "add or correct the explicit runtime binding", err)
			}
			if len(rendered) == 0 || !utf8.Valid(rendered) {
				return RenderedTree{}, compileError("operation lowerer returned empty or invalid UTF-8", "generated Markdown/native syntax must be exact portable text", value.location, "operation lowering", "return non-empty valid UTF-8 bytes", nil)
			}
			_, _ = output.Write(rendered)
		case targetLiteralPart:
			selected := value.cases[target.harness]
			if selected.unsupported {
				return RenderedTree{}, compileError(fmt.Sprintf("target literal is unsupported: %s", selected.reason), "the source explicitly declines semantics-preserving syntax for this harness", value.location, "target literal selection", "choose another target or add a reviewed contract-bound literal", nil)
			}
			if selected.contract != target.contract {
				return RenderedTree{}, compileError(fmt.Sprintf("target literal binds contract %q, selected target is %q", selected.contract, target.contract), "raw syntax cannot cross runtime contract versions", value.location, "target literal selection", "compile with the exact bound contract or add a new exhaustive literal", nil)
			}
			_, _ = output.Write(selected.content)
		default:
			return RenderedTree{}, compileError(fmt.Sprintf("unknown document part %T", part), "the document sum is closed", part.partLocation(), "rendering", "construct only supported Part variants", nil)
		}
	}
	file, err := NewRenderedFile(target.outputPath, output.Bytes())
	if err != nil {
		return RenderedTree{}, err
	}
	tree, err := NewRenderedTree(file)
	if err != nil {
		return RenderedTree{}, err
	}
	for index, validator := range target.validators {
		if err := validator.Validate(tree); err != nil {
			return RenderedTree{}, compileError(fmt.Sprintf("native validator %d rejected the complete tree", index), "successful output must satisfy every selected in-memory loader/schema", Location{}, "native validation", "correct the rendered output or target binding", err)
		}
	}
	return tree, nil
}

func validRelativePath(value string) bool {
	return value != "" && !strings.Contains(value, "\\") && !strings.ContainsRune(value, 0) &&
		!strings.HasPrefix(value, "/") && path.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../")
}

func documentError(what, why, fix string, cause error) error {
	return diagnostic(what, why, "typed Document", "document construction", "no Document is produced", fix, cause)
}

func compileError(what, why string, at Location, phase, fix string, cause error) error {
	where := "typed compiler"
	if at.IsValid() {
		where = fmt.Sprintf("%s:%s:%d-%d", at.file, at.section, at.range_.Start, at.range_.Stop)
	}
	return diagnostic(what, why, where, phase, "no RenderedTree is returned and no publisher is invoked", fix, cause)
}
