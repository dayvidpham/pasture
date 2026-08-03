// Package cell defines the canonical harness/extension coordinate space shared
// by the Pasture installer and activation contract.
//
// A Cell is one (harness, extension) coordinate. Exactly nine cells exist:
// three harnesses (claude-code, opencode, codex) times three extension axes
// (skills, agents, hooks). Every installer document, activation contract, and
// confirmed inventory serializes its cells in one frozen canonical order so
// goldens and state files never drift on ordering.
package cell

import (
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// Extension is a strongly-typed extension axis. Its zero value is invalid; use
// SkillsAxis, AgentsAxis, or HooksAxis. The three axes are global: the
// installer asks once which axes apply to every enabled harness.
type Extension struct{ name string }

const (
	skillsAxisName = "skills"
	agentsAxisName = "agents"
	hooksAxisName  = "hooks"
)

// SkillsAxis is the skills extension axis.
func SkillsAxis() Extension { return Extension{name: skillsAxisName} }

// AgentsAxis is the agents extension axis.
func AgentsAxis() Extension { return Extension{name: agentsAxisName} }

// HooksAxis is the security-sensitive hooks extension axis (default disabled).
func HooksAxis() Extension { return Extension{name: hooksAxisName} }

// ParseExtension resolves a canonical axis name to its typed value. It rejects
// unknown axes actionably rather than returning a silent zero value.
func ParseExtension(value string) (Extension, error) {
	switch value {
	case skillsAxisName:
		return SkillsAxis(), nil
	case agentsAxisName:
		return AgentsAxis(), nil
	case hooksAxisName:
		return HooksAxis(), nil
	default:
		return Extension{}, newFault(
			"extension parse", "known extension axis",
			fmt.Sprintf("the axis name %q is not one of skills, agents, or hooks", value),
			"internal/install/cell.ParseExtension", "resolving an extension axis name",
			"the referenced axis cannot be placed in the canonical cell order",
			"use exactly one of the axis names: skills, agents, hooks", nil,
		)
	}
}

// String returns the canonical axis name.
func (e Extension) String() string { return e.name }

// IsValid reports whether e is one of the three canonical axes.
func (e Extension) IsValid() bool {
	switch e.name {
	case skillsAxisName, agentsAxisName, hooksAxisName:
		return true
	default:
		return false
	}
}

// MarshalText encodes the canonical axis name.
func (e Extension) MarshalText() ([]byte, error) {
	if !e.IsValid() {
		return nil, newFault(
			"extension encode", "valid extension axis",
			"the extension value is the invalid zero value",
			"internal/install/cell.Extension.MarshalText", "serializing an extension axis",
			"a document would be written with an empty axis name",
			"construct the axis with SkillsAxis, AgentsAxis, or HooksAxis before encoding", nil,
		)
	}
	return []byte(e.name), nil
}

// UnmarshalText decodes a canonical axis name.
func (e *Extension) UnmarshalText(text []byte) error {
	parsed, err := ParseExtension(string(text))
	if err != nil {
		return err
	}
	*e = parsed
	return nil
}

// canonicalExtensions is the frozen intra-harness axis order.
var canonicalExtensions = [...]Extension{SkillsAxis(), AgentsAxis(), HooksAxis()}

// CanonicalExtensions returns the three axes in frozen order.
func CanonicalExtensions() []Extension {
	return append([]Extension(nil), canonicalExtensions[:]...)
}

// canonicalHarnesses is the frozen harness order.
var canonicalHarnesses = [...]ir.HarnessID{
	ir.HarnessClaudeCode,
	ir.HarnessOpenCode,
	ir.HarnessCodex,
}

// CanonicalHarnesses returns the three harnesses in frozen order.
func CanonicalHarnesses() []ir.HarnessID {
	return append([]ir.HarnessID(nil), canonicalHarnesses[:]...)
}

// Cell is one validated (harness, extension) coordinate.
type Cell struct {
	harness   ir.HarnessID
	extension Extension
	valid     bool
}

// New validates and constructs a Cell. It rejects unknown harnesses or axes so
// no document can address a coordinate outside the nine-cell space.
func New(harness ir.HarnessID, extension Extension) (Cell, error) {
	if !harness.IsValid() {
		return Cell{}, newFault(
			"cell construction", "known harness identity",
			fmt.Sprintf("the harness %q is not one of claude-code, opencode, or codex", harness),
			"internal/install/cell.New", "constructing a harness/extension coordinate",
			"the coordinate cannot be ordered or activated",
			"pass one of ir.HarnessClaudeCode, ir.HarnessOpenCode, ir.HarnessCodex", nil,
		)
	}
	if !extension.IsValid() {
		return Cell{}, newFault(
			"cell construction", "known extension axis",
			"the extension is the invalid zero value",
			"internal/install/cell.New", "constructing a harness/extension coordinate",
			"the coordinate cannot be ordered or activated",
			"pass SkillsAxis, AgentsAxis, or HooksAxis", nil,
		)
	}
	return Cell{harness: harness, extension: extension, valid: true}, nil
}

// Harness returns the cell's harness identity.
func (c Cell) Harness() ir.HarnessID { return c.harness }

// Extension returns the cell's extension axis.
func (c Cell) Extension() Extension { return c.extension }

// IsValid reports whether the cell was validly constructed.
func (c Cell) IsValid() bool { return c.valid }

// String returns "harness.extension", e.g. "claude-code.skills".
func (c Cell) String() string {
	return fmt.Sprintf("%s.%s", c.harness, c.extension)
}

// Index returns the cell's position in the canonical nine-cell order, or -1 if
// the cell is invalid.
func (c Cell) Index() int {
	if !c.valid {
		return -1
	}
	for i, candidate := range canonicalCells {
		if candidate.harness == c.harness && candidate.extension == c.extension {
			return i
		}
	}
	return -1
}

// canonicalCells is the frozen nine-cell order:
// claude-code.skills, claude-code.agents, claude-code.hooks,
// opencode.skills,   opencode.agents,   opencode.hooks,
// codex.skills,      codex.agents,      codex.hooks.
var canonicalCells = buildCanonicalCells()

func buildCanonicalCells() []Cell {
	cells := make([]Cell, 0, len(canonicalHarnesses)*len(canonicalExtensions))
	for _, harness := range canonicalHarnesses {
		for _, extension := range canonicalExtensions {
			cells = append(cells, Cell{harness: harness, extension: extension, valid: true})
		}
	}
	return cells
}

// CanonicalCells returns all nine cells in frozen canonical order.
func CanonicalCells() []Cell {
	return append([]Cell(nil), canonicalCells...)
}

// ParseCell resolves a "harness.extension" string to its typed cell.
func ParseCell(value string) (Cell, error) {
	dot := strings.LastIndex(value, ".")
	if dot <= 0 || dot == len(value)-1 {
		return Cell{}, newFault(
			"cell parse", "harness.extension form",
			fmt.Sprintf("the value %q is not in harness.extension form", value),
			"internal/install/cell.ParseCell", "parsing a cell coordinate",
			"the coordinate cannot be resolved to one of the nine cells",
			"use a value like claude-code.skills or codex.hooks", nil,
		)
	}
	harness := ir.HarnessID(value[:dot])
	extension, err := ParseExtension(value[dot+1:])
	if err != nil {
		return Cell{}, err
	}
	return New(harness, extension)
}
