package promotion

import (
	"strings"

	"github.com/dayvidpham/pasture/internal/effects"
)

// Gate is one named promotion precondition. A promotion advances the channel
// only after every gate reports nil. A gate is evaluated at the request's named
// revisions and must fail closed: any error, non-zero command exit, or
// unverifiable check aborts the promotion before the ref is touched.
type Gate interface {
	// Name is the stable gate identity reported in progress output and failures.
	Name() string
	// Run evaluates the gate. A nil result means the gate passed; a non-nil
	// result is a six-part actionable error that aborts the promotion.
	Run() error
}

// RunGates evaluates gates in order and stops at the first failure. Because it
// returns before the caller performs any ref update, a gate failure leaves the
// remote channel exactly as it was. The returned error names the failing gate,
// its ordinal position, and the remediation.
func RunGates(gates []Gate) error {
	for i, g := range gates {
		if g == nil {
			return fault(
				"promotion gate is nil",
				"a nil gate cannot be evaluated and would silently pass",
				"promotion.RunGates", "gate evaluation",
				"the promotion would advance without running a required check",
				"supply only constructed gates in the gate set", nil,
			)
		}
		if err := g.Run(); err != nil {
			return fault(
				"promotion gate "+g.Name()+" failed",
				"a required promotion precondition did not pass at the named revisions",
				"promotion.RunGates", "gate "+g.Name()+" (position "+ordinal(i)+")",
				"the pasture-stable ref is left unchanged and the promotion is aborted",
				"resolve the failing gate at the named revisions, then re-run the promotion", err,
			)
		}
	}
	return nil
}

func ordinal(zeroBased int) string {
	// Small, allocation-light 1-based rendering for gate positions.
	n := zeroBased + 1
	if n <= 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// CommandGate runs one named command in a working directory and passes iff the
// command exits zero. It is the production gate for target package tests and the
// activation-fixture command: real checks executed at the caller's checked-out
// revision, never a stubbed pass. The command is dispatched through the same
// injected resolver/runner seams as the git effects.
type CommandGate struct {
	name       string
	dir        effects.RepositoryID
	executable string
	args       []string
	resolve    effects.ExecutableResolver
	run        effects.CommandRunner
}

// NewCommandGate wires a command gate. name identifies the gate; dir is the
// working directory the command runs in; executable is the program to resolve
// (for example "go"); args are its arguments (for example "test", "./..."). Pass
// exec.LookPath and effects.DefaultCommandRunner in production.
func NewCommandGate(
	name string,
	dir effects.RepositoryID,
	executable string,
	args []string,
	resolve effects.ExecutableResolver,
	run effects.CommandRunner,
) (CommandGate, error) {
	if strings.TrimSpace(name) == "" {
		return CommandGate{}, fault(
			"command gate name is empty",
			"every gate must be named so a failure is attributable",
			"promotion.NewCommandGate", "gate wiring",
			"a failing gate could not be identified",
			"pass a non-empty gate name", nil,
		)
	}
	if !dir.IsValid() {
		return CommandGate{}, fault(
			"command gate "+name+" has an invalid working directory",
			"a gate command runs in a concrete repository/working directory",
			"promotion.NewCommandGate", "gate wiring",
			"the gate cannot run its check at a named revision",
			"construct the directory with effects.NewRepositoryID", nil,
		)
	}
	if strings.TrimSpace(executable) == "" {
		return CommandGate{}, fault(
			"command gate "+name+" names no executable",
			"a command gate dispatches a resolved executable",
			"promotion.NewCommandGate", "gate wiring",
			"the gate has no command to run",
			"pass the executable name to resolve, such as \"go\"", nil,
		)
	}
	if resolve == nil || run == nil {
		return CommandGate{}, fault(
			"command gate "+name+" is missing an executable resolver or command runner",
			"the gate command must be resolved and executed through injected seams",
			"promotion.NewCommandGate", "gate wiring",
			"the gate cannot dispatch its command",
			"pass a non-nil resolver (exec.LookPath) and runner (effects.DefaultCommandRunner)", nil,
		)
	}
	return CommandGate{
		name:       name,
		dir:        dir,
		executable: executable,
		args:       append([]string(nil), args...),
		resolve:    resolve,
		run:        run,
	}, nil
}

// Name returns the gate identity.
func (g CommandGate) Name() string { return g.name }

// Run resolves and executes the gate command; a non-zero exit is a six-part
// failure that aborts the promotion.
func (g CommandGate) Run() error {
	path, err := g.resolve(g.executable)
	if err != nil {
		return fault(
			"gate "+g.name+" executable "+g.executable+" could not be resolved",
			"the gate command program is not installed or not on PATH",
			"promotion.CommandGate.Run", "gate "+g.name+" dispatch",
			"the promotion cannot verify this precondition and is aborted",
			"install "+g.executable+" or add it to PATH, then re-run the promotion", err,
		)
	}
	if _, err := g.run(g.dir.String(), path, g.args...); err != nil {
		return fault(
			"gate "+g.name+" command failed",
			"the gate command exited non-zero at the named revision",
			"promotion.CommandGate.Run", "gate "+g.name+" execution",
			"the candidate did not pass a required check and is not promoted",
			"fix the failing check in "+g.dir.String()+" at the named revision, then re-run", err,
		)
	}
	return nil
}

// FuncGate adapts an in-process check to the Gate interface. It is the
// production gate for the Aura marketplace/repository validation, which is a Go
// check rather than a subprocess. A nil function is a wiring error surfaced at
// Run time.
type FuncGate struct {
	name  string
	check func() error
}

// NewFuncGate wires an in-process gate from a named check function.
func NewFuncGate(name string, check func() error) (FuncGate, error) {
	if strings.TrimSpace(name) == "" {
		return FuncGate{}, fault(
			"func gate name is empty",
			"every gate must be named so a failure is attributable",
			"promotion.NewFuncGate", "gate wiring",
			"a failing gate could not be identified",
			"pass a non-empty gate name", nil,
		)
	}
	if check == nil {
		return FuncGate{}, fault(
			"func gate "+name+" has a nil check",
			"an in-process gate must carry a check function",
			"promotion.NewFuncGate", "gate wiring",
			"the gate would silently pass",
			"pass a non-nil check function", nil,
		)
	}
	return FuncGate{name: name, check: check}, nil
}

// Name returns the gate identity.
func (g FuncGate) Name() string { return g.name }

// Run evaluates the in-process check.
func (g FuncGate) Run() error {
	if g.check == nil {
		return fault(
			"func gate "+g.name+" has a nil check",
			"the gate was not validly constructed",
			"promotion.FuncGate.Run", "gate "+g.name+" execution",
			"the promotion would advance without running this check",
			"construct the gate with NewFuncGate", nil,
		)
	}
	return g.check()
}
