// Package selection defines the transient effective-selection document that the
// installer TUI and Home Manager both hand to the apply engine.
//
// The document is never the preference YAML: it carries only already-normalized
// effective cell state (harness enabled AND that global extension axis enabled).
// Neither apply-selection nor apply-cell reads or writes preference YAML; they
// consume this effective state and mutate only factual operational inventory.
//
// The schema requires all and only the three harnesses, each with all and only
// the three extension axes. Missing or extra harnesses/axes are rejected so an
// under-specified document can never silently disable a cell.
package selection

import (
	"bytes"
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"gopkg.in/yaml.v3"
)

// SchemaID is the frozen schema identifier for the effective-selection document.
const SchemaID = "pasture.install.effective-selection/v1"

// Selection is a validated effective-selection over exactly the nine cells.
type Selection struct {
	enabled map[string]bool // keyed by cell.String(); populated for all nine
	valid   bool
}

// New constructs a Selection from an exhaustive cell->enabled mapping. The map
// must contain exactly the nine canonical cells; missing or extra keys are
// rejected.
func New(states map[cell.Cell]bool) (Selection, error) {
	if len(states) != len(cell.CanonicalCells()) {
		return Selection{}, cell.NewFault(
			"selection construction", "exhaustive nine-cell mapping",
			fmt.Sprintf("the mapping has %d cells, not the required 9", len(states)),
			"internal/install/selection.New", "building an effective selection",
			"an under- or over-specified selection could silently skip a cell",
			"provide exactly one boolean for each of the nine canonical cells", nil,
		)
	}
	enabled := make(map[string]bool, len(states))
	for c, on := range states {
		if !c.IsValid() {
			return Selection{}, cell.NewFault(
				"selection construction", "valid cell key",
				"the mapping contains an invalid cell key",
				"internal/install/selection.New", "building an effective selection",
				"the selection cannot be ordered or applied",
				"construct every key with cell.New", nil,
			)
		}
		enabled[c.String()] = on
	}
	for _, c := range cell.CanonicalCells() {
		if _, ok := enabled[c.String()]; !ok {
			return Selection{}, cell.NewFault(
				"selection construction", "every canonical cell present",
				fmt.Sprintf("cell %s is missing from the mapping", c),
				"internal/install/selection.New", "building an effective selection",
				"the missing cell would have no defined desired state",
				fmt.Sprintf("add a boolean for cell %s", c), nil,
			)
		}
	}
	return Selection{enabled: enabled, valid: true}, nil
}

// IsValid reports whether the selection was validly constructed.
func (s Selection) IsValid() bool { return s.valid }

// Enabled returns the effective desired state for a cell.
func (s Selection) Enabled(c cell.Cell) bool { return s.enabled[c.String()] }

// Ordered returns the nine (cell, enabled) pairs in canonical order.
func (s Selection) Ordered() []CellState {
	out := make([]CellState, 0, len(cell.CanonicalCells()))
	for _, c := range cell.CanonicalCells() {
		out = append(out, CellState{Cell: c, Enabled: s.enabled[c.String()]})
	}
	return out
}

// CellState is one ordered cell and its effective desired state.
type CellState struct {
	Cell    cell.Cell
	Enabled bool
}

type axesWire struct {
	Skills *bool `yaml:"skills"`
	Agents *bool `yaml:"agents"`
	Hooks  *bool `yaml:"hooks"`
}

type cellsWire struct {
	ClaudeCode *axesWire `yaml:"claude-code"`
	OpenCode   *axesWire `yaml:"opencode"`
	Codex      *axesWire `yaml:"codex"`
}

type selectionWire struct {
	Schema string    `yaml:"schema"`
	Cells  cellsWire `yaml:"cells"`
}

// Parse decodes and validates an effective-selection document. Unknown keys,
// missing harnesses, and missing axes are all rejected.
func Parse(data []byte) (Selection, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var wire selectionWire
	if err := decoder.Decode(&wire); err != nil {
		return Selection{}, cell.NewFault(
			"selection decode", "well-formed effective-selection document",
			fmt.Sprintf("the document could not be decoded: %v", err),
			"internal/install/selection.Parse", "decoding an effective-selection document",
			"the desired cell state cannot be trusted",
			"provide a document with schema, cells.{claude-code,opencode,codex}.{skills,agents,hooks} and no extra keys", err,
		)
	}
	if wire.Schema != SchemaID {
		return Selection{}, cell.NewFault(
			"selection decode", "frozen schema identifier",
			fmt.Sprintf("schema is %q, not %q", wire.Schema, SchemaID),
			"internal/install/selection.Parse", "decoding an effective-selection document",
			"a document of a different schema version could carry incompatible cell semantics",
			fmt.Sprintf("set schema to %q", SchemaID), nil,
		)
	}
	states := make(map[cell.Cell]bool, len(cell.CanonicalCells()))
	if err := collectHarness(states, ir.HarnessClaudeCode, wire.Cells.ClaudeCode); err != nil {
		return Selection{}, err
	}
	if err := collectHarness(states, ir.HarnessOpenCode, wire.Cells.OpenCode); err != nil {
		return Selection{}, err
	}
	if err := collectHarness(states, ir.HarnessCodex, wire.Cells.Codex); err != nil {
		return Selection{}, err
	}
	return New(states)
}

func collectHarness(states map[cell.Cell]bool, harness ir.HarnessID, axes *axesWire) error {
	if axes == nil {
		return cell.NewFault(
			"selection decode", "all three harnesses present",
			fmt.Sprintf("harness %q is absent from cells", harness),
			"internal/install/selection.Parse", "decoding an effective-selection document",
			"the missing harness would have no defined desired state",
			fmt.Sprintf("add a cells.%s block with skills, agents, and hooks", harness), nil,
		)
	}
	pairs := []struct {
		axis cell.Extension
		val  *bool
	}{
		{cell.SkillsAxis(), axes.Skills},
		{cell.AgentsAxis(), axes.Agents},
		{cell.HooksAxis(), axes.Hooks},
	}
	for _, p := range pairs {
		if p.val == nil {
			return cell.NewFault(
				"selection decode", "all three axes present",
				fmt.Sprintf("harness %q is missing the %s axis", harness, p.axis),
				"internal/install/selection.Parse", "decoding an effective-selection document",
				"a missing axis would have no defined desired state",
				fmt.Sprintf("add %s under cells.%s", p.axis, harness), nil,
			)
		}
		c, err := cell.New(harness, p.axis)
		if err != nil {
			return err
		}
		states[c] = *p.val
	}
	return nil
}

// Marshal encodes the selection in canonical harness order.
func (s Selection) Marshal() ([]byte, error) {
	if !s.valid {
		return nil, cell.NewFault(
			"selection encode", "validly constructed selection",
			"the selection is the invalid zero value",
			"internal/install/selection.Marshal", "encoding an effective-selection document",
			"an empty document would be written",
			"construct the selection with selection.New or selection.Parse", nil,
		)
	}
	axesFor := func(harness ir.HarnessID) *axesWire {
		get := func(axis cell.Extension) *bool {
			c, _ := cell.New(harness, axis)
			v := s.enabled[c.String()]
			return &v
		}
		return &axesWire{Skills: get(cell.SkillsAxis()), Agents: get(cell.AgentsAxis()), Hooks: get(cell.HooksAxis())}
	}
	wire := selectionWire{
		Schema: SchemaID,
		Cells: cellsWire{
			ClaudeCode: axesFor(ir.HarnessClaudeCode),
			OpenCode:   axesFor(ir.HarnessOpenCode),
			Codex:      axesFor(ir.HarnessCodex),
		},
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(wire); err != nil {
		return nil, cell.NewFault(
			"selection encode", "serializable selection",
			fmt.Sprintf("the selection could not be encoded: %v", err),
			"internal/install/selection.Marshal", "encoding an effective-selection document",
			"the document cannot be handed to the apply engine",
			"report this as an internal encoder failure", err,
		)
	}
	_ = encoder.Close()
	return buf.Bytes(), nil
}
