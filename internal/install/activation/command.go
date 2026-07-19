package activation

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/install/cell"
)

// CommandSchema is a validated, literal command line: an executable and its
// exact argument vector. It carries no placeholder or shell metacharacter, so a
// native manager or version probe argv is fixture-frozen and never assembled by
// string interpolation at call time.
type CommandSchema struct {
	program string
	args    []string
	valid   bool
}

// NewCommandSchema validates and owns a literal argv. The program must be
// non-empty and no argument may be empty.
func NewCommandSchema(program string, args ...string) (CommandSchema, error) {
	if program == "" {
		return CommandSchema{}, cell.NewFault(
			"command schema construction", "non-empty program",
			"the program name is empty",
			"internal/install/activation.NewCommandSchema", "building a native command schema",
			"no executable can be invoked for this activation step",
			"provide the harness executable name, e.g. claude, opencode, or codex", nil,
		)
	}
	for i, arg := range args {
		if arg == "" {
			return CommandSchema{}, cell.NewFault(
				"command schema construction", "non-empty arguments",
				fmt.Sprintf("argument %d is empty", i),
				"internal/install/activation.NewCommandSchema", "building a native command schema",
				"an empty argument would change the native manager's parsing",
				"remove the empty argument or replace it with its literal value", nil,
			)
		}
	}
	owned := append([]string(nil), args...)
	return CommandSchema{program: program, args: owned, valid: true}, nil
}

// Program returns the executable name.
func (c CommandSchema) Program() string { return c.program }

// Args returns a copy of the literal argument vector.
func (c CommandSchema) Args() []string { return append([]string(nil), c.args...) }

// IsValid reports whether the schema was validly constructed.
func (c CommandSchema) IsValid() bool { return c.valid }

// String renders the argv for diagnostics.
func (c CommandSchema) String() string {
	out := c.program
	for _, a := range c.args {
		out += " " + a
	}
	return out
}
