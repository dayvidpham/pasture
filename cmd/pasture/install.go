package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/install/preferences"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/types"
)

// installCmd groups the scriptable installer and confirmed-state commands.
// Interactive preferences and the Bubble Tea frontend are intentionally
// deferred; these commands are the source-of-truth surfaces for automation and
// Home Manager.
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

Plan and status are read-only. apply-selection and apply-cell mutate native
installations and update confirmed state; they never contact a running daemon.`,
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
		store, err := registry.Load(statePath)
		if err != nil {
			printError(err)
			exitWithCode(1)
			return nil
		}
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if jsonOutput || resolveFormat() == types.OutputJSON {
			return writeInstallStatusJSON(cmd, statePath, store)
		}
		return writeInstallStatusText(cmd, statePath, store)
	},
}

// Status consumes the same Store that mutating installer frontends must load
// once and pass through inventory.View; no frontend may open a scope-specific
// state file or derive a second project index.
func writeInstallStatusText(cmd *cobra.Command, statePath string, store registry.Store) error {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Pasture installation state (%s)\n", statePath)
	if store.Len() == 0 {
		fmt.Fprintln(w, "  no cells recorded; nothing has been installed by Pasture yet")
		return nil
	}
	for _, row := range store.Status() {
		r := row.Record
		managed := "external"
		if r.Managed() {
			managed = "pasture-managed"
		}
		identity := row.Scope.String()
		if row.Scope == registry.ScopeProject {
			identity += ":" + row.ProjectRoot.String()
		}
		fmt.Fprintf(w, "  %-20s %-9s %-15s %-12s %s/%s (%s)\n",
			r.Cell().String(), r.Observation().String(), r.Strategy().String(), identity,
			r.Source().String(), managed, r.Trust().String())
		if r.LastOperation() != registry.OperationNone {
			fmt.Fprintf(w, "      last: %s -> %s\n", r.LastOperation(), r.LastOutcome())
		}
		if r.Diagnostic() != "" {
			fmt.Fprintf(w, "      note: %s\n", r.Diagnostic())
		}
	}
	return nil
}

type installStatusCellJSON struct {
	Scope       string `json:"scope"`
	ProjectRoot string `json:"project_root,omitempty"`
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

func writeInstallStatusJSON(cmd *cobra.Command, statePath string, store registry.Store) error {
	cells := make([]installStatusCellJSON, 0, store.Len())
	for _, row := range store.Status() {
		r := row.Record
		lastAction := ""
		if r.LastOperation() != registry.OperationNone {
			lastAction = r.LastOperation().String()
		}
		lastOutcome := ""
		if r.LastOutcome() != registry.OutcomeNone {
			lastOutcome = r.LastOutcome().String()
		}
		cells = append(cells, installStatusCellJSON{
			Scope:       row.Scope.String(),
			ProjectRoot: row.ProjectRoot.String(),
			Cell:        r.Cell().String(),
			Observation: r.Observation().String(),
			Strategy:    r.Strategy().String(),
			Source:      r.Source().String(),
			Managed:     r.Managed(),
			Trust:       r.Trust().String(),
			LastAction:  lastAction,
			LastOutcome: lastOutcome,
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
	installStatusCmd.Flags().Bool("json", false, "Write deterministic JSON status")
	installCmd.AddCommand(installPlanCmd, installStatusCmd)
	rootCmd.AddCommand(installCmd)
}
