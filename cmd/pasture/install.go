package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/install/inventory"
	"github.com/dayvidpham/pasture/internal/install/preferences"
	"github.com/dayvidpham/pasture/internal/types"
)

// installCmd groups the installer preference and confirmed-state commands. The
// interactive TUI and the mutating apply-selection/apply-cell surfaces are
// delivered incrementally; the commands here are the read-only, source-of-truth
// views that both the TUI and Home Manager build on.
var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Inspect and normalize Pasture installer preferences and confirmed state",
	Long: `install works with two separate files:

  * ~/.config/pasture/config.yaml (install: section) — user preferences: which
    harnesses are enabled and one global set of extension axes (skills, agents,
    hooks). Skills and agents default on but stay inert until a harness is
    enabled; hooks default off.

  * ${XDG_STATE_HOME:-~/.local/state}/pasture/installations.yaml — the confirmed
    installation inventory: what Pasture actually installed, whether an uninstall
    completed, what remains, and the exact retry.

All commands here read those files; they never contact a running daemon.`,
}

// installPlanCmd normalizes saved preferences into the transient effective
// selection that the apply engine consumes.
var installPlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Normalize saved preferences into the effective per-cell selection",
	Long: `plan loads the install preferences, normalizes the global harness and extension
choices into the nine effective harness/extension cells (a cell is effective only
when its harness is enabled and its global axis is enabled), and prints the
resulting effective-selection document. It reads preferences only and never
mutates any file.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, _ := cmd.Flags().GetString("config")
		if configPath == "" {
			configPath = config.DefaultConfigPath()
		}
		prefs, err := preferences.Load(configPath)
		if err != nil {
			printError(err)
			exitWithCode(1)
			return nil
		}
		sel, err := prefs.EffectiveSelection()
		if err != nil {
			printError(err)
			exitWithCode(1)
			return nil
		}
		doc, err := sel.Marshal()
		if err != nil {
			printError(err)
			exitWithCode(1)
			return nil
		}
		if resolveFormat() == types.OutputJSON {
			out := map[string]any{}
			for _, cs := range sel.Ordered() {
				out[cs.Cell.String()] = cs.Enabled
			}
			payload := map[string]any{"config": configPath, "cells": out}
			encoded, _ := json.MarshalIndent(payload, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
			return nil
		}
		fmt.Fprint(cmd.OutOrStdout(), string(doc))
		return nil
	},
}

// installStatusCmd reports the confirmed installation inventory.
var installStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report what Pasture installed, what remains, and the exact retry",
	Long: `status loads the confirmed installation inventory and reports, per recorded cell,
what Pasture installed, whether an uninstall completed, what remains or is
unknown, the control source and native-trust disposition, and the last recorded
action, outcome, and actionable diagnostic. It reads the state file only.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		statePath, _ := cmd.Flags().GetString("state")
		if statePath == "" {
			statePath = defaultInstallStatePath()
		}
		inv, err := inventory.Load(statePath)
		if err != nil {
			printError(err)
			exitWithCode(1)
			return nil
		}
		if resolveFormat() == types.OutputJSON {
			return writeInstallStatusJSON(cmd, statePath, inv)
		}
		return writeInstallStatusText(cmd, statePath, inv)
	},
}

func writeInstallStatusText(cmd *cobra.Command, statePath string, inv inventory.Inventory) error {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Pasture installation state (%s)\n", statePath)
	if inv.Len() == 0 {
		fmt.Fprintln(w, "  no cells recorded; nothing has been installed by Pasture yet")
		return nil
	}
	for _, r := range inv.Ordered() {
		managed := "external"
		if r.Managed() {
			managed = "pasture-managed"
		}
		fmt.Fprintf(w, "  %-20s %-9s %-15s %s/%s (%s)\n",
			r.Cell().String(), r.Observation().String(), r.Strategy().String(),
			r.Source().String(), managed, r.Trust().String())
		if r.LastAction() != "" {
			fmt.Fprintf(w, "      last: %s -> %s\n", r.LastAction(), r.LastOutcome())
		}
		if r.Diagnostic() != "" {
			fmt.Fprintf(w, "      note: %s\n", r.Diagnostic())
		}
	}
	return nil
}

type installStatusCellJSON struct {
	Cell        string `json:"cell"`
	Observation string `json:"observation"`
	Strategy    string `json:"strategy"`
	Source      string `json:"source"`
	Managed     bool   `json:"managed"`
	Trust       string `json:"trust"`
	LastAction  string `json:"last_action,omitempty"`
	LastOutcome string `json:"last_outcome,omitempty"`
	Diagnostic  string `json:"diagnostic,omitempty"`
}

func writeInstallStatusJSON(cmd *cobra.Command, statePath string, inv inventory.Inventory) error {
	cells := make([]installStatusCellJSON, 0, inv.Len())
	for _, r := range inv.Ordered() {
		cells = append(cells, installStatusCellJSON{
			Cell:        r.Cell().String(),
			Observation: r.Observation().String(),
			Strategy:    r.Strategy().String(),
			Source:      r.Source().String(),
			Managed:     r.Managed(),
			Trust:       r.Trust().String(),
			LastAction:  r.LastAction(),
			LastOutcome: r.LastOutcome(),
			Diagnostic:  r.Diagnostic(),
		})
	}
	payload := map[string]any{"state_file": statePath, "cells": cells}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		printError(err)
		exitWithCode(1)
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
	return nil
}

// defaultInstallStatePath resolves the confirmed-state file under XDG_STATE_HOME.
func defaultInstallStatePath() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".", ".local", "state", "pasture", "installations.yaml")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "pasture", "installations.yaml")
}

func init() {
	installPlanCmd.Flags().String("config", "", "Path to the pasture config file (default: ~/.config/pasture/config.yaml)")
	installStatusCmd.Flags().String("state", "", "Path to the confirmed installation state file (default: $XDG_STATE_HOME/pasture/installations.yaml)")
	installCmd.AddCommand(installPlanCmd, installStatusCmd)
	rootCmd.AddCommand(installCmd)
}
