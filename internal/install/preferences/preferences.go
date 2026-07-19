// Package preferences models the persisted installer choices under the existing
// Pasture config root (~/.config/pasture/config.yaml, install: section).
//
// It is intentionally not a per-harness extension matrix: the user picks which
// harnesses are enabled and, once, which extension axes apply to every enabled
// harness. All harnesses default disabled. Skills and agents default enabled
// but stay inert until at least one harness is enabled. Hooks default disabled
// because they are security-sensitive; a previously saved explicit hooks opt-in
// is restored on load.
//
// The writer preserves unrelated Pasture configuration. This file stores user
// preferences only; confirmed operational evidence lives separately in the
// install-state inventory.
package preferences

import (
	"bytes"
	"fmt"
	"os"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/fsatomic"
	"github.com/dayvidpham/pasture/internal/install/selection"
	"gopkg.in/yaml.v3"
)

// installKey is the top-level config key that owns installer preferences.
const installKey = "install"

// Preferences is a validated set of installer choices.
type Preferences struct {
	harnesses map[ir.HarnessID]bool
	skills    bool
	agents    bool
	hooks     bool
}

// Default returns first-run preferences: all harnesses disabled, skills and
// agents enabled, hooks disabled.
func Default() Preferences {
	return Preferences{
		harnesses: map[ir.HarnessID]bool{
			ir.HarnessClaudeCode: false,
			ir.HarnessOpenCode:   false,
			ir.HarnessCodex:      false,
		},
		skills: true,
		agents: true,
		hooks:  false,
	}
}

// HarnessEnabled reports whether a harness is enabled.
func (p Preferences) HarnessEnabled(harness ir.HarnessID) bool { return p.harnesses[harness] }

// ExtensionEnabled reports whether a global extension axis is enabled.
func (p Preferences) ExtensionEnabled(axis cell.Extension) bool {
	switch axis.String() {
	case cell.SkillsAxis().String():
		return p.skills
	case cell.AgentsAxis().String():
		return p.agents
	case cell.HooksAxis().String():
		return p.hooks
	default:
		return false
	}
}

// WithHarness returns a copy with the harness set to enabled.
func (p Preferences) WithHarness(harness ir.HarnessID, enabled bool) (Preferences, error) {
	if !harness.IsValid() {
		return Preferences{}, cell.NewFault(
			"preferences update", "known harness",
			fmt.Sprintf("harness %q is not recognized", harness),
			"internal/install/preferences.WithHarness", "toggling a harness preference",
			"an unknown harness cannot be persisted",
			"pass one of claude-code, opencode, codex", nil,
		)
	}
	next := p.clone()
	next.harnesses[harness] = enabled
	return next, nil
}

// WithExtension returns a copy with the global extension axis set.
func (p Preferences) WithExtension(axis cell.Extension, enabled bool) (Preferences, error) {
	next := p.clone()
	switch axis.String() {
	case cell.SkillsAxis().String():
		next.skills = enabled
	case cell.AgentsAxis().String():
		next.agents = enabled
	case cell.HooksAxis().String():
		next.hooks = enabled
	default:
		return Preferences{}, cell.NewFault(
			"preferences update", "known extension axis",
			"the extension axis is not one of skills, agents, hooks",
			"internal/install/preferences.WithExtension", "toggling an extension preference",
			"an unknown axis cannot be persisted",
			"pass SkillsAxis, AgentsAxis, or HooksAxis", nil,
		)
	}
	return next, nil
}

func (p Preferences) clone() Preferences {
	harnesses := make(map[ir.HarnessID]bool, len(p.harnesses))
	for k, v := range p.harnesses {
		harnesses[k] = v
	}
	return Preferences{harnesses: harnesses, skills: p.skills, agents: p.agents, hooks: p.hooks}
}

// EffectiveSelection normalizes the global choices into the nine-cell effective
// selection: a cell is enabled iff its harness is enabled and its global axis is
// enabled. Disabled harnesses receive no extensions even when an axis is on.
func (p Preferences) EffectiveSelection() (selection.Selection, error) {
	states := make(map[cell.Cell]bool, len(cell.CanonicalCells()))
	for _, c := range cell.CanonicalCells() {
		states[c] = p.HarnessEnabled(c.Harness()) && p.ExtensionEnabled(c.Extension())
	}
	return selection.New(states)
}

// axesWire mirrors the install.extensions block with pointers so a strict decode
// can distinguish present-false from absent.
type axesWire struct {
	Skills *bool `yaml:"skills"`
	Agents *bool `yaml:"agents"`
	Hooks  *bool `yaml:"hooks"`
}

type harnessesWire struct {
	ClaudeCode *bool `yaml:"claude-code"`
	OpenCode   *bool `yaml:"opencode"`
	Codex      *bool `yaml:"codex"`
}

type installWire struct {
	Harnesses  harnessesWire `yaml:"harnesses"`
	Extensions axesWire      `yaml:"extensions"`
}

// Load reads preferences from path. A missing file or an absent install section
// yields Default(). Unknown harnesses or axes are rejected actionably.
//
// Symlink asymmetry (intentional, versus inventory.Load and Save): Load reads
// THROUGH a symlinked config.yaml on purpose. Under dotfile managers and Nix
// Home Manager, ~/.config/pasture/config.yaml is routinely a symlink into a
// version-controlled dotfiles source, and refusing to read it would break the
// supported managed-config workflow. This is safe for a read: the file is
// declared user-owned preference input (not confirmed trust evidence like the
// install-state inventory, which does reject a symlink before read), and every
// value it yields is re-validated (known harnesses/axes only) before use. The
// asymmetry is deliberate — Save (below) still refuses to REPLACE a symlink,
// because clobbering the link would silently detach the user's managed config
// from its source; a write instead directs the user to edit that source.
func Load(path string) (Preferences, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Preferences{}, cell.NewFault(
			"preferences load", "readable config file",
			fmt.Sprintf("the config file could not be read: %v", err),
			path, "loading installer preferences",
			"prior installer choices cannot be restored",
			"ensure the config file is readable, then retry", err,
		)
	}
	return decode(data, path)
}

func decode(data []byte, path string) (Preferences, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return Preferences{}, cell.NewFault(
			"preferences load", "well-formed YAML",
			fmt.Sprintf("the config file is not valid YAML: %v", err),
			path, "parsing installer preferences",
			"prior installer choices cannot be restored",
			"repair the YAML syntax, keeping the file human-editable", err,
		)
	}
	installNode := findMappingValue(&root, installKey)
	if installNode == nil {
		return Default(), nil
	}
	installBytes, err := yaml.Marshal(installNode)
	if err != nil {
		return Preferences{}, cell.NewFault(
			"preferences load", "re-encodable install section",
			fmt.Sprintf("the install section could not be re-encoded for validation: %v", err),
			path, "isolating the install section",
			"installer preferences cannot be validated",
			"report this as an internal decode failure", err,
		)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(installBytes))
	decoder.KnownFields(true)
	var wire installWire
	if err := decoder.Decode(&wire); err != nil {
		return Preferences{}, cell.NewFault(
			"preferences load", "known harnesses and axes only",
			fmt.Sprintf("the install section has an unknown or malformed key: %v", err),
			path, "validating installer preferences",
			"an unrecognized harness or axis would be silently ignored",
			"use only harnesses.{claude-code,opencode,codex} and extensions.{skills,agents,hooks}", err,
		)
	}
	prefs := Default()
	applyBool(&prefs.harnesses, ir.HarnessClaudeCode, wire.Harnesses.ClaudeCode)
	applyBool(&prefs.harnesses, ir.HarnessOpenCode, wire.Harnesses.OpenCode)
	applyBool(&prefs.harnesses, ir.HarnessCodex, wire.Harnesses.Codex)
	if wire.Extensions.Skills != nil {
		prefs.skills = *wire.Extensions.Skills
	}
	if wire.Extensions.Agents != nil {
		prefs.agents = *wire.Extensions.Agents
	}
	if wire.Extensions.Hooks != nil {
		prefs.hooks = *wire.Extensions.Hooks
	}
	return prefs, nil
}

func applyBool(harnesses *map[ir.HarnessID]bool, harness ir.HarnessID, value *bool) {
	if value != nil {
		(*harnesses)[harness] = *value
	}
}

// Save persists preferences to path, preserving every unrelated top-level
// Pasture config key. If saving fails, the caller must perform no external
// action.
//
// Unlike Load, Save refuses to write through a symlinked config.yaml. fsatomic
// enforces this as defense-in-depth, but Save pre-checks it here to return a
// config-specific fix: a managed config symlinked from a dotfiles source must be
// edited at that source, not clobbered by an atomic replace that would detach
// the link.
func Save(path string, prefs Preferences) error {
	if info, err := os.Lstat(path); err == nil && info.Mode().Type()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(path)
		return cell.NewFault(
			"preferences save", "regular-file or absent config destination",
			fmt.Sprintf("the config file %q is a symlink to %q", path, target),
			path, "checking the config type before an atomic preference save",
			"an atomic replace would detach the symlink and stop your dotfiles or Home Manager source from managing this config",
			"edit the install: section in the file your dotfiles manager links here (the symlink's source), then re-apply, instead of writing through the link", nil,
		)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return cell.NewFault(
			"preferences save", "readable existing config",
			fmt.Sprintf("the existing config could not be read to preserve unrelated keys: %v", err),
			path, "reading before an atomic preference save",
			"unrelated Pasture configuration could be lost",
			"ensure the config file is readable, then retry", err,
		)
	}
	var root yaml.Node
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := yaml.Unmarshal(existing, &root); err != nil {
			return cell.NewFault(
				"preferences save", "well-formed existing config",
				fmt.Sprintf("the existing config is not valid YAML: %v", err),
				path, "reading before an atomic preference save",
				"unrelated Pasture configuration cannot be preserved",
				"repair the YAML syntax, then retry", err,
			)
		}
	}
	installNode, err := encodeInstall(prefs)
	if err != nil {
		return err
	}
	setMappingValue(&root, installKey, installNode)

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		return cell.NewFault(
			"preferences save", "serializable config document",
			fmt.Sprintf("the merged config could not be encoded: %v", err),
			path, "encoding the merged preference document",
			"preferences cannot be written and no external action should follow",
			"report this as an internal encode failure", err,
		)
	}
	_ = encoder.Close()
	return fsatomic.WriteFile(path, buf.Bytes(), 0o644)
}

func encodeInstall(prefs Preferences) (*yaml.Node, error) {
	claude := prefs.HarnessEnabled(ir.HarnessClaudeCode)
	opencode := prefs.HarnessEnabled(ir.HarnessOpenCode)
	codex := prefs.HarnessEnabled(ir.HarnessCodex)
	wire := installWire{
		Harnesses:  harnessesWire{ClaudeCode: &claude, OpenCode: &opencode, Codex: &codex},
		Extensions: axesWire{Skills: &prefs.skills, Agents: &prefs.agents, Hooks: &prefs.hooks},
	}
	var node yaml.Node
	if err := node.Encode(wire); err != nil {
		return nil, cell.NewFault(
			"preferences save", "encodable install section",
			fmt.Sprintf("the install section could not be encoded: %v", err),
			"internal/install/preferences.encodeInstall", "building the install node",
			"preferences cannot be written",
			"report this as an internal encode failure", err,
		)
	}
	return &node, nil
}
