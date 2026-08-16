package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/service"
	"github.com/dayvidpham/pasture/internal/types"
)

// The human-facing installer grammar is additive and per-cell:
//
//	pasture install                       -> help (interactive TUI deferred)
//	pasture install <harness>             -> help (ambiguous: no extensions named)
//	pasture install <harness> <ext>...    -> ensure exactly those cells; siblings untouched
//	pasture uninstall <harness> <ext>...  -> remove exactly those cells; siblings untouched
//
// A harness names one of claude-code (alias: claude), opencode, or codex; an
// extension names one of skills, agents, or hooks. Each named extension is
// applied as one independent cell through the shared installer service, so
// unnamed siblings on the same harness are never read or mutated. Cells are
// attempted independently and every outcome is reported, rather than stopping
// at the first failure — this is the scriptable-but-friendly surface, distinct
// from the exhaustive apply-selection document Home Manager consumes.

// parseHarnessAlias resolves a user-facing harness word to a canonical
// HarnessID, accepting the shorthand "claude" for "claude-code". It rejects
// unknown harnesses actionably instead of guessing.
func parseHarnessAlias(value string) (ir.HarnessID, error) {
	switch value {
	case "claude", "claude-code":
		return ir.HarnessClaudeCode, nil
	case "opencode":
		return ir.HarnessOpenCode, nil
	case "codex":
		return ir.HarnessCodex, nil
	}
	return ir.HarnessID(""), fmt.Errorf(
		"unknown harness %q; use one of claude (claude-code), opencode, or codex", value)
}

// resolveInstallCells turns positional CLI args ("<harness> <ext>...") into the
// exact set of cells the user named. It returns needHelp=true when the request
// is ambiguous (no args, or a lone harness with no extensions) so the caller
// can print usage rather than acting on an unstated intent.
func resolveInstallCells(args []string) (cells []cell.Cell, needHelp bool, err error) {
	if len(args) < 2 {
		return nil, true, nil
	}
	harness, err := parseHarnessAlias(args[0])
	if err != nil {
		return nil, false, err
	}
	seen := make(map[cell.Cell]struct{}, len(args)-1)
	for _, name := range args[1:] {
		axis, axisErr := cell.ParseExtension(name)
		if axisErr != nil {
			return nil, false, axisErr
		}
		c, cellErr := cell.New(harness, axis)
		if cellErr != nil {
			return nil, false, cellErr
		}
		if _, dup := seen[c]; dup {
			return nil, false, fmt.Errorf(
				"extension %q named more than once for harness %q; list each extension at most once",
				name, args[0])
		}
		seen[c] = struct{}{}
		cells = append(cells, c)
	}
	sort.SliceStable(cells, func(i, j int) bool { return cells[i].Index() < cells[j].Index() })
	return cells, false, nil
}

// applyNamedCells applies each named cell independently at the given desired
// state, collecting every per-cell row into one aggregate result. It never
// stops early: a failed cell is recorded and the remaining cells are still
// attempted, so one broken harness cannot mask the outcome of the others.
func applyNamedCells(cmd *cobra.Command, cells []cell.Cell, enabled bool, makeService installServiceFactory) (apply.Result, error) {
	source, err := sourceValue("installer")
	if err != nil {
		return apply.Result{}, err
	}
	svc, err := makeService()
	if err != nil {
		return apply.Result{}, fmt.Errorf("compose installer service: %w", err)
	}
	rows := make([]apply.ActionRow, 0, len(cells))
	allOK := true
	scope := apply.GlobalScope()
	for _, c := range cells {
		result, applyErr := svc.ApplyCell(cmd.Context(), service.CellRequest{Cell: c, Enabled: enabled, Scope: scope, Source: source})
		if applyErr != nil {
			allOK = false
			rows = append(rows, apply.NewActionRow(c, desiredOperation(enabled), apply.Failed(), apply.ManagementUnknown, registry.ObservationUnknown, applyErr.Error()))
			continue
		}
		if !result.OK() {
			allOK = false
		}
		rows = append(rows, result.Rows()...)
	}
	return apply.NewResult(source, scope.Kind(), allOK, rows), nil
}

func desiredOperation(enabled bool) apply.Operation {
	if enabled {
		return apply.Ensure()
	}
	return apply.RemoveOp()
}

func runInstallVerb(cmd *cobra.Command, args []string, enabled bool, makeService installServiceFactory) error {
	cells, needHelp, err := resolveInstallCells(args)
	if err != nil {
		return err
	}
	if needHelp {
		return cmd.Help()
	}
	result, err := applyNamedCells(cmd, cells, enabled, makeService)
	if err != nil {
		return err
	}
	installJSON, _ := cmd.Flags().GetBool("json")
	if installJSON || resolveFormat() == types.OutputJSON {
		if err := writeJSON(cmd.OutOrStdout(), result); err != nil {
			return err
		}
	} else if err := writeApplyText(cmd.OutOrStdout(), result); err != nil {
		return err
	}
	if !result.OK() {
		return fmt.Errorf("one or more cells failed; see the reported rows for the exact cell and diagnostic")
	}
	return nil
}

func newInstallVerbCommand(makeService installServiceFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install [harness] [extension...]",
		Short: "Install named Pasture extensions for one harness",
		Long: `install turns on exactly the named extensions for one harness and leaves that
harness's other extensions untouched.

  pasture install                       start the interactive installer (deferred; prints this help for now)
  pasture install claude                ambiguous — prints this help (name at least one extension)
  pasture install claude skills agents  install Claude skills and agents; Claude hooks are left as-is
  pasture install opencode skills       install OpenCode skills only

A harness is claude (alias for claude-code), opencode, or codex. An extension is
skills, agents, or hooks. Each named extension is applied independently and every
outcome is reported; the read-only "pasture install status" shows confirmed state.

The scriptable "apply-selection" and "apply-cell" commands remain available for
Home Manager and automation.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstallVerb(cmd, args, true, makeService)
		},
	}
	cmd.Flags().Bool("json", false, "Write the deterministic apply-result document")
	return cmd
}

func newUninstallVerbCommand(makeService installServiceFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall [harness] [extension...]",
		Short: "Uninstall named Pasture extensions for one harness",
		Long: `uninstall removes exactly the named extensions for one harness and leaves that
harness's other extensions untouched.

  pasture uninstall codex hooks         remove Codex hooks; Codex skills and agents are left as-is
  pasture uninstall claude skills       remove Claude skills only

A harness is claude (alias for claude-code), opencode, or codex. An extension is
skills, agents, or hooks. Only Pasture-managed cells are removed; exact external
installations are preserved. Each named extension is applied independently and
every outcome is reported.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstallVerb(cmd, args, false, makeService)
		},
	}
	cmd.Flags().Bool("json", false, "Write the deterministic apply-result document")
	return cmd
}
