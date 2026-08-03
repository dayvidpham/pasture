package effects

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// shellMetaCharacters are the operators an execve-style argv must never carry.
// Their presence in an executable name or working directory means a caller is
// trying to smuggle shell semantics (expansion, control operators, redirection,
// globbing, pipelines) through data. Those constructs are unsupported here and
// must become dedicated semantic operations — see ClassifyShellConstruct.
const shellMetaCharacters = "|&;<>()$`\\\"'*?[]{}~!#"

func containsControl(value string) (rune, bool) {
	for _, r := range value {
		if unicode.IsControl(r) {
			return r, true
		}
	}
	return 0, false
}

// ExecutableRef names the program to run. It is opaque and constructor-owned:
// the executable is resolved at execution time through an injected lookup (see
// ExecutableResolver), never interpreted by a shell. A ref may carry a path
// separator (for an absolute or relative program path) but never a shell
// metacharacter.
type ExecutableRef struct {
	name        string
	constructed bool
}

// NewExecutableRef validates a program name or path for execve-style dispatch.
func NewExecutableRef(name string) (ExecutableRef, error) {
	if !utf8.ValidString(name) {
		return ExecutableRef{}, effectError(
			"executable reference is not valid UTF-8",
			"a program name must survive exact process dispatch and target rendering",
			"NewExecutableRef", "process operand validation",
			"the process effect cannot be constructed",
			"supply a valid UTF-8 program name or path", nil,
		)
	}
	if name == "" || strings.TrimSpace(name) != name {
		return ExecutableRef{}, effectError(
			"executable reference is empty or padded",
			"a program identity requires one exact non-padded spelling",
			"NewExecutableRef", "process operand validation",
			"the process effect cannot be dispatched deterministically",
			"supply a non-empty program name without surrounding whitespace", nil,
		)
	}
	if r, ok := containsControl(name); ok {
		return ExecutableRef{}, effectError(
			fmt.Sprintf("executable reference contains control character U+%04X", r),
			"control characters are unsafe in a portable program identity",
			"NewExecutableRef", "process operand validation",
			"the process effect cannot be represented safely",
			"remove control characters from the program name", nil,
		)
	}
	if i := strings.IndexAny(name, shellMetaCharacters); i >= 0 {
		return ExecutableRef{}, effectError(
			fmt.Sprintf("executable reference %q contains shell metacharacter %q", name, string(name[i])),
			"an executable is dispatched with execve semantics, so a shell operator here is an attempt to smuggle unsupported shell semantics through data",
			"NewExecutableRef", "process operand validation",
			"the effect would either fail to dispatch or silently gain shell behavior",
			"model the intended shell construct as a dedicated semantic operation instead of embedding it in the program name", nil,
		)
	}
	return ExecutableRef{name: name, constructed: true}, nil
}

func (e ExecutableRef) String() string { return e.name }
func (e ExecutableRef) IsValid() bool  { return e.constructed && e.name != "" }

// Argument is one literal argv element. It is passed verbatim to the program
// with execve semantics — never re-parsed by a shell — so it may legitimately
// contain spaces, quotes, or characters that would be special to a shell. Only
// NUL and invalid UTF-8, which cannot cross a process boundary, are rejected.
type Argument struct {
	value       string
	constructed bool
}

// NewArgument validates one literal argv element.
func NewArgument(value string) (Argument, error) {
	if !utf8.ValidString(value) {
		return Argument{}, effectError(
			"argument is not valid UTF-8",
			"an argv element must survive exact process dispatch and target rendering",
			"NewArgument", "process operand validation",
			"the process effect cannot be constructed",
			"supply a valid UTF-8 argument", nil,
		)
	}
	if strings.ContainsRune(value, 0) {
		return Argument{}, effectError(
			"argument contains a NUL byte",
			"a NUL byte cannot cross an execve argument boundary",
			"NewArgument", "process operand validation",
			"the process effect cannot be dispatched",
			"remove the NUL byte from the argument", nil,
		)
	}
	return Argument{value: value, constructed: true}, nil
}

func (a Argument) String() string { return a.value }
func (a Argument) IsValid() bool  { return a.constructed }

// EnvBinding is one typed environment variable binding. The value is a literal
// string, never shell-expanded: a value of "$HOME" is the five literal
// characters, not the caller's home directory.
type EnvBinding struct {
	name        string
	value       string
	constructed bool
}

// NewEnvBinding validates an environment variable name and literal value.
func NewEnvBinding(name, value string) (EnvBinding, error) {
	if name == "" {
		return EnvBinding{}, effectError(
			"environment binding name is empty",
			"an environment variable requires one exact name",
			"NewEnvBinding", "process operand validation",
			"the process environment cannot be constructed",
			"supply a non-empty POSIX environment variable name", nil,
		)
	}
	for i, r := range name {
		valid := r == '_' || unicode.IsLetter(r) || (i > 0 && unicode.IsDigit(r))
		if !valid || r > unicode.MaxASCII {
			return EnvBinding{}, effectError(
				fmt.Sprintf("environment binding name %q is not a portable identifier", name),
				"environment names must be portable ASCII identifiers so two spellings never disagree",
				"NewEnvBinding", "process operand validation",
				"the environment binding cannot be represented portably",
				"use a name matching [A-Za-z_][A-Za-z0-9_]*", nil,
			)
		}
	}
	if strings.ContainsRune(name, '=') {
		return EnvBinding{}, effectError(
			fmt.Sprintf("environment binding name %q contains '='", name),
			"'=' separates a binding's name from its value and cannot appear in the name",
			"NewEnvBinding", "process operand validation",
			"the environment binding is ambiguous",
			"remove '=' from the environment variable name", nil,
		)
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return EnvBinding{}, effectError(
			fmt.Sprintf("environment binding %q has an invalid value", name),
			"a value must be valid UTF-8 without a NUL byte to cross a process boundary",
			"NewEnvBinding", "process operand validation",
			"the environment binding cannot be dispatched",
			"supply a valid UTF-8 value without a NUL byte", nil,
		)
	}
	return EnvBinding{name: name, value: value, constructed: true}, nil
}

func (b EnvBinding) Name() string  { return b.name }
func (b EnvBinding) Value() string { return b.value }
func (b EnvBinding) IsValid() bool { return b.constructed && b.name != "" }

// WorkingDirectoryKind distinguishes an explicit path working directory from an
// assignment-local worktree reference.
type WorkingDirectoryKind string

const (
	WorkingDirectoryPath     WorkingDirectoryKind = "path"
	WorkingDirectoryWorktree WorkingDirectoryKind = "worktree"
)

// WorkingDirectoryRef is the closed sum of the two ways a process may name its
// working directory: an explicit owned path, or an assignment-local worktree
// reference minted by the #38 IR. It is opaque and constructor-owned.
type WorkingDirectoryRef struct {
	kind        WorkingDirectoryKind
	path        OwnedPath
	worktree    ir.WorktreeRef
	constructed bool
}

// NewPathWorkingDirectory names the working directory by an exact owned path.
func NewPathWorkingDirectory(path OwnedPath) (WorkingDirectoryRef, error) {
	if !path.IsValid() {
		return WorkingDirectoryRef{}, effectError(
			"working directory path is zero or invalid",
			"a working directory must be an exact constructor-validated owned path",
			"NewPathWorkingDirectory", "process operand validation",
			"the process effect has no deterministic working directory",
			"construct the path with NewOwnedPath", nil,
		)
	}
	return WorkingDirectoryRef{kind: WorkingDirectoryPath, path: path, constructed: true}, nil
}

// NewWorktreeWorkingDirectory names the working directory by a worktree ref.
func NewWorktreeWorkingDirectory(ref ir.WorktreeRef) (WorkingDirectoryRef, error) {
	if !ref.IsValid() {
		return WorkingDirectoryRef{}, effectError(
			"working directory worktree reference is zero or invalid",
			"a worktree working directory must be a constructor-validated WorktreeRef",
			"NewWorktreeWorkingDirectory", "process operand validation",
			"the process effect has no deterministic working directory",
			"construct the reference with ir.NewWorktreeRef", nil,
		)
	}
	return WorkingDirectoryRef{kind: WorkingDirectoryWorktree, worktree: ref, constructed: true}, nil
}

func (w WorkingDirectoryRef) Kind() WorkingDirectoryKind { return w.kind }
func (w WorkingDirectoryRef) IsValid() bool              { return w.constructed }

// Path returns the owned path and true when the working directory is a path.
func (w WorkingDirectoryRef) Path() (OwnedPath, bool) {
	if w.kind != WorkingDirectoryPath {
		return OwnedPath{}, false
	}
	return w.path, true
}

// Worktree returns the worktree ref and true when the working directory is a
// worktree reference.
func (w WorkingDirectoryRef) Worktree() (ir.WorktreeRef, bool) {
	if w.kind != WorkingDirectoryWorktree {
		return ir.WorktreeRef{}, false
	}
	return w.worktree, true
}

// CaptureID names one captured process output stream so a later pipeline step
// can consume it as previous-output input. It is a pipeline-local identity, not
// a portable namespaced identity.
type CaptureID struct {
	value       string
	constructed bool
}

// NewCaptureID validates a pipeline-local capture identity.
func NewCaptureID(value string) (CaptureID, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return CaptureID{}, effectError(
			"capture identity is empty or padded",
			"a capture identity links one step's output to a later step's input and requires one exact spelling",
			"NewCaptureID", "dataflow operand validation",
			"output-to-input dataflow cannot be linked",
			"supply a non-empty capture identity without surrounding whitespace", nil,
		)
	}
	if r, ok := containsControl(value); ok {
		return CaptureID{}, effectError(
			fmt.Sprintf("capture identity contains control character U+%04X", r),
			"control characters make a capture identity unsafe to compare",
			"NewCaptureID", "dataflow operand validation",
			"output-to-input dataflow cannot be linked safely",
			"remove control characters from the capture identity", nil,
		)
	}
	return CaptureID{value: value, constructed: true}, nil
}

func (c CaptureID) String() string { return c.value }
func (c CaptureID) IsValid() bool  { return c.constructed && c.value != "" }

// InputKind is the closed set of process stdin sources.
type InputKind string

const (
	// InputNone supplies no standard input.
	InputNone InputKind = "none"
	// InputLiteral supplies exact in-memory bytes.
	InputLiteral InputKind = "literal"
	// InputFile reads standard input from an owned file path.
	InputFile InputKind = "file"
	// InputPreviousOutput consumes a prior step's captured output, making
	// command substitution an explicit output-to-input dataflow edge.
	InputPreviousOutput InputKind = "previous-output"
)

// InputRef is the closed sum of process standard-input sources.
type InputRef struct {
	kind        InputKind
	literal     []byte
	file        OwnedPath
	previous    CaptureID
	constructed bool
}

// NoInput supplies no standard input.
func NoInput() InputRef { return InputRef{kind: InputNone, constructed: true} }

// NewLiteralInput supplies exact in-memory standard-input bytes.
func NewLiteralInput(content []byte) InputRef {
	owned := append([]byte(nil), content...)
	return InputRef{kind: InputLiteral, literal: owned, constructed: true}
}

// NewFileInput reads standard input from an owned file path.
func NewFileInput(path OwnedPath) (InputRef, error) {
	if !path.IsValid() {
		return InputRef{}, effectError(
			"file input path is zero or invalid",
			"a file input must name an exact owned path",
			"NewFileInput", "dataflow operand validation",
			"the process has no deterministic standard input",
			"construct the path with NewOwnedPath", nil,
		)
	}
	return InputRef{kind: InputFile, file: path, constructed: true}, nil
}

// NewPreviousOutputInput consumes a prior step's captured output as this step's
// standard input. This is the only supported form of command substitution.
func NewPreviousOutputInput(capture CaptureID) (InputRef, error) {
	if !capture.IsValid() {
		return InputRef{}, effectError(
			"previous-output input capture is zero or invalid",
			"command substitution is modeled as an explicit output-to-input dataflow edge and requires a valid capture identity",
			"NewPreviousOutputInput", "dataflow operand validation",
			"the substitution edge cannot be linked",
			"reference a capture minted by an earlier step's captured output", nil,
		)
	}
	return InputRef{kind: InputPreviousOutput, previous: capture, constructed: true}, nil
}

func (i InputRef) Kind() InputKind { return i.kind }
func (i InputRef) IsValid() bool   { return i.constructed }

// Literal returns the literal input bytes and true when the input is literal.
func (i InputRef) Literal() ([]byte, bool) {
	if i.kind != InputLiteral {
		return nil, false
	}
	return append([]byte(nil), i.literal...), true
}

// File returns the input file path and true when the input reads from a file.
func (i InputRef) File() (OwnedPath, bool) {
	if i.kind != InputFile {
		return OwnedPath{}, false
	}
	return i.file, true
}

// PreviousOutput returns the referenced capture and true when the input is a
// previous-output dataflow edge.
func (i InputRef) PreviousOutput() (CaptureID, bool) {
	if i.kind != InputPreviousOutput {
		return CaptureID{}, false
	}
	return i.previous, true
}

// OutputKind is the closed set of process stdout/stderr sinks.
type OutputKind string

const (
	// OutputDiscard drops the stream.
	OutputDiscard OutputKind = "discard"
	// OutputCaptured captures the stream under a CaptureID for later dataflow.
	OutputCaptured OutputKind = "captured"
	// OutputFile writes the stream to an owned file path.
	OutputFile OutputKind = "file"
)

// OutputRef is the closed sum of process standard-output/standard-error sinks.
type OutputRef struct {
	kind        OutputKind
	capture     CaptureID
	file        OwnedPath
	constructed bool
}

// DiscardOutput drops the stream.
func DiscardOutput() OutputRef { return OutputRef{kind: OutputDiscard, constructed: true} }

// NewCapturedOutput captures the stream under capture for later dataflow.
func NewCapturedOutput(capture CaptureID) (OutputRef, error) {
	if !capture.IsValid() {
		return OutputRef{}, effectError(
			"captured output capture is zero or invalid",
			"a captured stream must be named so a later step can consume it",
			"NewCapturedOutput", "dataflow operand validation",
			"output-to-input dataflow cannot be established",
			"construct the capture with NewCaptureID", nil,
		)
	}
	return OutputRef{kind: OutputCaptured, capture: capture, constructed: true}, nil
}

// NewFileOutput writes the stream to an owned file path.
func NewFileOutput(path OwnedPath) (OutputRef, error) {
	if !path.IsValid() {
		return OutputRef{}, effectError(
			"file output path is zero or invalid",
			"a file output must name an exact owned path",
			"NewFileOutput", "dataflow operand validation",
			"the process output has no deterministic destination",
			"construct the path with NewOwnedPath", nil,
		)
	}
	return OutputRef{kind: OutputFile, file: path, constructed: true}, nil
}

func (o OutputRef) Kind() OutputKind { return o.kind }
func (o OutputRef) IsValid() bool    { return o.constructed }

// Capture returns the capture id and true when the output is captured.
func (o OutputRef) Capture() (CaptureID, bool) {
	if o.kind != OutputCaptured {
		return CaptureID{}, false
	}
	return o.capture, true
}

// File returns the output file path and true when the output writes to a file.
func (o OutputRef) File() (OwnedPath, bool) {
	if o.kind != OutputFile {
		return OwnedPath{}, false
	}
	return o.file, true
}

// ExitExpectation is the immutable sorted, deduplicated set of process exit
// codes accepted as success. Its zero value is invalid: an effect must state
// exactly which exits it treats as success.
type ExitExpectation struct {
	codes       []int
	constructed bool
}

// ExpectSuccess is the common expectation that only exit code 0 is success.
func ExpectSuccess() ExitExpectation {
	return ExitExpectation{codes: []int{0}, constructed: true}
}

// NewExitExpectation builds an expected-exit set from one or more codes.
func NewExitExpectation(codes ...int) (ExitExpectation, error) {
	if len(codes) == 0 {
		return ExitExpectation{}, effectError(
			"exit expectation is empty",
			"a process effect must state at least one exit code it treats as success",
			"NewExitExpectation", "process operand validation",
			"success cannot be evaluated for the effect",
			"supply at least one expected exit code, or use ExpectSuccess", nil,
		)
	}
	canonical := append([]int(nil), codes...)
	for _, code := range canonical {
		if code < 0 || code > 255 {
			return ExitExpectation{}, effectError(
				fmt.Sprintf("exit code %d is out of range", code),
				"a portable process exit status is 0..255",
				"NewExitExpectation", "process operand validation",
				"the exit expectation cannot be represented portably",
				"use exit codes in the range 0..255", nil,
			)
		}
	}
	sort.Ints(canonical)
	deduped := canonical[:0]
	for _, code := range canonical {
		if len(deduped) == 0 || deduped[len(deduped)-1] != code {
			deduped = append(deduped, code)
		}
	}
	owned := append([]int(nil), deduped...)
	return ExitExpectation{codes: owned, constructed: true}, nil
}

// Codes returns a defensive copy of the accepted exit codes in ascending order.
func (e ExitExpectation) Codes() []int { return append([]int(nil), e.codes...) }

func (e ExitExpectation) IsValid() bool { return e.constructed && len(e.codes) > 0 }

// Accepts reports whether code is in the success set.
func (e ExitExpectation) Accepts(code int) bool {
	for _, c := range e.codes {
		if c == code {
			return true
		}
	}
	return false
}
