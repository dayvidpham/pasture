package effects

import (
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// RunProcess is one modeled process execution. Every field is a typed operand,
// never a shell string: the executable is dispatched with execve semantics and
// arguments are passed verbatim. It is opaque and constructor-owned, and is an
// Effect variant classified native.
type RunProcess struct {
	executable  ExecutableRef
	arguments   []Argument
	directory   WorkingDirectoryRef
	environment []EnvBinding
	stdin       InputRef
	stdout      OutputRef
	stderr      OutputRef
	exit        ExitExpectation
	effects     ir.EffectSet
	constructed bool
}

// RunProcessSpec is the typed, non-opaque input to NewRunProcess. It exists so
// callers name operands by field rather than by positional argument order.
type RunProcessSpec struct {
	Executable  ExecutableRef
	Arguments   []Argument
	Directory   WorkingDirectoryRef
	Environment []EnvBinding
	Stdin       InputRef
	Stdout      OutputRef
	Stderr      OutputRef
	Exit        ExitExpectation
	Effects     ir.EffectSet
}

// NewRunProcess validates every operand and builds an immutable RunProcess. An
// unset Stdin/Stdout/Stderr defaults to no input and discarded output; Exit
// defaults to success-only; Directory is required.
func NewRunProcess(spec RunProcessSpec) (RunProcess, error) {
	if !spec.Executable.IsValid() {
		return RunProcess{}, effectError(
			"run-process executable is zero or invalid",
			"a process effect must name a constructor-validated executable",
			"NewRunProcess", "process effect validation",
			"the process cannot be dispatched",
			"construct the executable with NewExecutableRef", nil,
		)
	}
	for index, argument := range spec.Arguments {
		if !argument.IsValid() {
			return RunProcess{}, effectError(
				fmt.Sprintf("run-process argument %d is zero or invalid", index),
				"every argv element must be a constructor-validated Argument",
				"NewRunProcess", "process effect validation",
				"the process argument vector cannot be constructed",
				"construct every argument with NewArgument", nil,
			)
		}
	}
	if !spec.Directory.IsValid() {
		return RunProcess{}, effectError(
			"run-process working directory is zero or invalid",
			"a process effect must run in a deterministic constructor-validated working directory",
			"NewRunProcess", "process effect validation",
			"the process has no defined working directory",
			"construct the directory with NewPathWorkingDirectory or NewWorktreeWorkingDirectory", nil,
		)
	}
	seenEnv := make(map[string]struct{}, len(spec.Environment))
	for index, binding := range spec.Environment {
		if !binding.IsValid() {
			return RunProcess{}, effectError(
				fmt.Sprintf("run-process environment binding %d is zero or invalid", index),
				"every environment binding must be a constructor-validated EnvBinding",
				"NewRunProcess", "process effect validation",
				"the process environment cannot be constructed",
				"construct every binding with NewEnvBinding", nil,
			)
		}
		if _, duplicate := seenEnv[binding.Name()]; duplicate {
			return RunProcess{}, effectError(
				fmt.Sprintf("run-process environment binds %q more than once", binding.Name()),
				"a duplicate environment name lets two readers disagree on the effective value",
				"NewRunProcess", "process effect validation",
				"the process environment is ambiguous",
				"bind each environment variable exactly once", nil,
			)
		}
		seenEnv[binding.Name()] = struct{}{}
	}
	stdin := spec.Stdin
	if !stdin.IsValid() {
		stdin = NoInput()
	}
	stdout := spec.Stdout
	if !stdout.IsValid() {
		stdout = DiscardOutput()
	}
	stderr := spec.Stderr
	if !stderr.IsValid() {
		stderr = DiscardOutput()
	}
	exit := spec.Exit
	if !exit.IsValid() {
		exit = ExpectSuccess()
	}
	effectSet := spec.Effects
	if !effectSet.IsValid() {
		empty, err := ir.NewEffectSet()
		if err != nil {
			return RunProcess{}, err
		}
		effectSet = empty
	}
	return RunProcess{
		executable:  spec.Executable,
		arguments:   append([]Argument(nil), spec.Arguments...),
		directory:   spec.Directory,
		environment: append([]EnvBinding(nil), spec.Environment...),
		stdin:       stdin,
		stdout:      stdout,
		stderr:      stderr,
		exit:        exit,
		effects:     effectSet,
		constructed: true,
	}, nil
}

func (p RunProcess) Executable() ExecutableRef      { return p.executable }
func (p RunProcess) Arguments() []Argument          { return append([]Argument(nil), p.arguments...) }
func (p RunProcess) Directory() WorkingDirectoryRef { return p.directory }
func (p RunProcess) Environment() []EnvBinding      { return append([]EnvBinding(nil), p.environment...) }
func (p RunProcess) Stdin() InputRef                { return p.stdin }
func (p RunProcess) Stdout() OutputRef              { return p.stdout }
func (p RunProcess) Stderr() OutputRef              { return p.stderr }
func (p RunProcess) Exit() ExitExpectation          { return p.exit }
func (p RunProcess) Effects() ir.EffectSet          { return p.effects }
func (p RunProcess) IsValid() bool                  { return p.constructed && p.executable.IsValid() }
func (p RunProcess) Classify() RuntimeClass         { return RuntimeClassNative }
func (p RunProcess) isEffect()                      {}

// producedCaptures returns the set of capture IDs this process publishes via a
// captured stdout or stderr.
func (p RunProcess) producedCaptures() []CaptureID {
	var captures []CaptureID
	if capture, ok := p.stdout.Capture(); ok {
		captures = append(captures, capture)
	}
	if capture, ok := p.stderr.Capture(); ok {
		captures = append(captures, capture)
	}
	return captures
}

// consumedCapture returns the capture this process consumes as previous-output
// standard input, and whether it consumes one.
func (p RunProcess) consumedCapture() (CaptureID, bool) {
	return p.stdin.PreviousOutput()
}

// ProcessStep is one step in a pipeline. It wraps a RunProcess and is the unit
// of ordered, dataflow-linked execution.
type ProcessStep struct {
	process     RunProcess
	constructed bool
}

// NewProcessStep wraps a validated RunProcess as a pipeline step.
func NewProcessStep(process RunProcess) (ProcessStep, error) {
	if !process.IsValid() {
		return ProcessStep{}, effectError(
			"pipeline step process is zero or invalid",
			"a pipeline step must wrap a constructor-validated RunProcess",
			"NewProcessStep", "pipeline validation",
			"the step cannot be sequenced",
			"construct the process with NewRunProcess", nil,
		)
	}
	return ProcessStep{process: process, constructed: true}, nil
}

func (s ProcessStep) Process() RunProcess { return s.process }
func (s ProcessStep) IsValid() bool       { return s.constructed && s.process.IsValid() }

// Pipeline is an ordered sequence of process steps whose output-to-input
// dataflow is fully explicit: a step may consume a prior step's captured output
// as its standard input, but every such edge must reference a capture already
// produced by an earlier step. There is no implicit shell pipe. Pipelines are
// added only where the classified inventory requires ordered dataflow.
type Pipeline struct {
	steps       []ProcessStep
	constructed bool
}

// NewPipeline validates ordering and output-to-input dataflow. A previous-output
// standard input must reference a capture produced by a strictly earlier step,
// and every capture name must be produced exactly once.
func NewPipeline(steps ...ProcessStep) (Pipeline, error) {
	if len(steps) == 0 {
		return Pipeline{}, effectError(
			"pipeline has no steps",
			"a pipeline sequences at least one process step",
			"NewPipeline", "pipeline validation",
			"there is nothing to execute",
			"supply at least one process step", nil,
		)
	}
	produced := make(map[string]int)
	for index, step := range steps {
		if !step.IsValid() {
			return Pipeline{}, effectError(
				fmt.Sprintf("pipeline step %d is zero or invalid", index),
				"every step must be a constructor-validated ProcessStep",
				"NewPipeline", "pipeline validation",
				"the pipeline cannot be sequenced",
				"construct every step with NewProcessStep", nil,
			)
		}
		if consumed, ok := step.Process().consumedCapture(); ok {
			producer, known := produced[consumed.String()]
			if !known {
				return Pipeline{}, effectError(
					fmt.Sprintf("pipeline step %d consumes capture %q before any step produces it", index, consumed.String()),
					"command substitution is an explicit output-to-input edge and must flow from an earlier step",
					"NewPipeline", "pipeline dataflow validation",
					"the substitution edge has no source and the pipeline is not a valid dataflow",
					"capture the producing step's output before consuming it, or reorder the steps", nil,
				)
			}
			if producer >= index {
				return Pipeline{}, effectError(
					fmt.Sprintf("pipeline step %d consumes capture %q from a non-earlier step", index, consumed.String()),
					"dataflow must move forward so the pipeline stays acyclic and ordered",
					"NewPipeline", "pipeline dataflow validation",
					"the pipeline would depend on its own or a later output",
					"consume only captures produced by strictly earlier steps", nil,
				)
			}
		}
		for _, capture := range step.Process().producedCaptures() {
			if _, duplicate := produced[capture.String()]; duplicate {
				return Pipeline{}, effectError(
					fmt.Sprintf("pipeline capture %q is produced more than once", capture.String()),
					"a duplicate capture name makes an output-to-input edge ambiguous",
					"NewPipeline", "pipeline dataflow validation",
					"a later step cannot tell which producer it consumes",
					"give each captured output a unique capture identity", nil,
				)
			}
			produced[capture.String()] = index
		}
	}
	return Pipeline{steps: append([]ProcessStep(nil), steps...), constructed: true}, nil
}

// Steps returns a defensive copy of the ordered pipeline steps.
func (p Pipeline) Steps() []ProcessStep { return append([]ProcessStep(nil), p.steps...) }
func (p Pipeline) IsValid() bool        { return p.constructed && len(p.steps) > 0 }

// ShellConstruct is the closed set of shell constructs Pasture deliberately does
// not model as processes. Each must become a dedicated semantic operation; a
// lowerer may never smuggle one through an opaque `sh -c` string.
type ShellConstruct string

const (
	// ShellExpansion is variable, command, arithmetic, or tilde expansion.
	ShellExpansion ShellConstruct = "expansion"
	// ShellControlOperator is a control operator such as &&, ||, ;, &, !, or a
	// command separator such as a newline or tab.
	ShellControlOperator ShellConstruct = "control-operator"
	// ShellRedirection is stream redirection such as >, >>, <, or 2>&1.
	ShellRedirection ShellConstruct = "redirection"
	// ShellGlobbing is filename globbing such as *, ?, or [...].
	ShellGlobbing ShellConstruct = "globbing"
	// ShellPipeline is an unstructured `|` shell pipeline. Structured dataflow
	// uses Pipeline with explicit captures instead.
	ShellPipeline ShellConstruct = "pipeline"
	// ShellGrouping is subshell or brace-group syntax such as (...) or {...}.
	ShellGrouping ShellConstruct = "grouping"
	// ShellQuoting is quoting or escaping such as "...", '...', or a backslash
	// escape.
	ShellQuoting ShellConstruct = "quoting"
	// ShellComment is a shell comment introduced by #.
	ShellComment ShellConstruct = "comment"
)

// shellConstructGuidance names the fix for one classified construct.
func shellConstructGuidance(construct ShellConstruct) string {
	switch construct {
	case ShellExpansion:
		return "model the substituted value as an explicit output-to-input dataflow edge in a Pipeline"
	case ShellControlOperator:
		return "model each command as a distinct process step with explicit success expectations"
	case ShellRedirection:
		return "model the stream destination with a file or captured OutputRef/InputRef"
	case ShellPipeline:
		return "model the data flow with a structured Pipeline and explicit captures"
	case ShellGlobbing:
		return "name each exact owned path instead of a glob pattern"
	case ShellGrouping:
		return "model each grouped or subshell command sequence as distinct process steps"
	case ShellQuoting:
		return "pass the literal value as an Argument or EnvBinding instead of a quoted or escaped shell token"
	case ShellComment:
		return "remove the comment; a process effect carries no shell commentary"
	default:
		return "model the intended shell construct as a dedicated semantic operation instead of embedding it in a raw fragment"
	}
}

// shellMetaCharacterConstruct maps every individual rune in shellMetaCharacters
// (refs.go) to the ShellConstruct class it belongs to. ClassifyShellConstruct's
// single-character fallback scan is derived directly from this map, so the
// character set ExecutableRef rejects and the constructs ClassifyShellConstruct
// detects can never drift apart: every metacharacter classifies to exactly one
// construct. TestShellMetaCharacterConstructIsTotalOverShellMetaCharacters
// (process_internal_test.go) proves this map is total over shellMetaCharacters.
var shellMetaCharacterConstruct = map[rune]ShellConstruct{
	'|':  ShellPipeline,
	'&':  ShellControlOperator,
	';':  ShellControlOperator,
	'<':  ShellRedirection,
	'>':  ShellRedirection,
	'(':  ShellGrouping,
	')':  ShellGrouping,
	'$':  ShellExpansion,
	'`':  ShellExpansion,
	'\\': ShellQuoting,
	'"':  ShellQuoting,
	'\'': ShellQuoting,
	'*':  ShellGlobbing,
	'?':  ShellGlobbing,
	'[':  ShellGlobbing,
	']':  ShellGlobbing,
	'{':  ShellGrouping,
	'}':  ShellGrouping,
	'~':  ShellExpansion,
	'!':  ShellControlOperator,
	'#':  ShellComment,
}

func classifyShellConstructError(fragment string, construct ShellConstruct, token string) error {
	return effectError(
		fmt.Sprintf("shell fragment %q uses unsupported %s construct %q", fragment, construct, token),
		"operational semantics are modeled as typed process/pipeline/filesystem effects, not hidden in shell strings that a lowerer would have to render through an opaque `sh -c`",
		"ClassifyShellConstruct", "shell-construct classification",
		"the construct has no modeled semantics and cannot be lowered to any harness",
		shellConstructGuidance(construct)+", or add a dedicated semantic operation for this construct", nil,
	)
}

// ClassifyShellConstruct inspects a raw shell fragment for constructs outside
// the modeled process/pipeline algebra. If it finds one, it returns the
// classified construct and an actionable error naming the owner, location, and
// fix. It never produces an `sh -c` rendering: an unsupported construct must be
// promoted to a dedicated semantic operation instead. A fragment with no
// unsupported construct returns ("", nil).
//
// Detection proceeds in two passes. First, a small set of compound
// multi-character idioms (such as "2>&1" or "$(") are checked so common
// fragments classify with a higher-fidelity token in their error message.
// Second, a per-rune fallback scans the fragment against
// shellMetaCharacterConstruct — the same shellMetaCharacters set ExecutableRef
// enforces — so every metacharacter is classified even if it never appears in
// the compound list above. A command separator (newline or tab) is checked
// alongside the compound idioms: it is not itself a shellMetaCharacters entry
// (ExecutableRef instead rejects it via containsControl), but it carries the
// same command-separator semantics as ';' and so must not silently classify
// clean.
func ClassifyShellConstruct(fragment string) (ShellConstruct, error) {
	compound := []struct {
		construct ShellConstruct
		tokens    []string
	}{
		// Redirection and control-operator compounds are checked before the
		// bare "&" fallback so a compound token such as "2>&1" classifies as
		// redirection rather than matching the control operator it contains.
		{ShellRedirection, []string{">>", "2>&1"}},
		{ShellExpansion, []string{"$(", "${"}},
		{ShellControlOperator, []string{"&&", "||", "\n", "\t"}},
	}
	for _, check := range compound {
		for _, token := range check.tokens {
			if strings.Contains(fragment, token) {
				return check.construct, classifyShellConstructError(fragment, check.construct, token)
			}
		}
	}
	for _, r := range fragment {
		if construct, ok := shellMetaCharacterConstruct[r]; ok {
			return construct, classifyShellConstructError(fragment, construct, string(r))
		}
	}
	return "", nil
}
