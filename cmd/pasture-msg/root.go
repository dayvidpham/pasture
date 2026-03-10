// Package main implements the pasture-msg CLI — a command-line tool for sending
// control messages to the pastured daemon via Temporal signals and queries.
//
// Command structure:
//
//	pasture-msg
//	├── epoch  start | cancel | terminate
//	├── query  state
//	├── signal vote | complete
//	├── phase  advance
//	└── session register
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/types"
)

const version = "v0.1.0"

// rootCmd is the parent for all pasture-msg subcommands.
// Global flags are registered here and resolved into config by each RunE handler.
var rootCmd = &cobra.Command{
	Use:   "pasture-msg",
	Short: "Send control messages to the pastured daemon",
	Long: `pasture-msg sends signals and queries to a running pastured epoch workflow
via the Temporal API. It is designed to be called from Claude Code hooks and
automation scripts.

Exit codes:
  0  success
  1  validation or configuration error
  2  connection error (Temporal server unreachable)
  3  workflow error (workflow not found, signal or query failed)`,
	Version: version,
	// RunE is not set — root command prints help when no subcommand is given.
}

// Global flag values (populated by cobra flag binding, resolved to config in RunE).
var (
	flagNamespace  string
	flagTaskQueue  string
	flagAddress    string
	flagFormat     string
	flagConfigFile string
)

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&flagNamespace, "namespace", "", "Temporal namespace (env: TEMPORAL_NAMESPACE, default: default)")
	pf.StringVar(&flagTaskQueue, "task-queue", "", "Temporal task queue (env: TEMPORAL_TASK_QUEUE, default: pasture)")
	pf.StringVar(&flagAddress, "address", "", "Temporal server address (env: TEMPORAL_ADDRESS, default: localhost:7233)")
	pf.StringVar(&flagFormat, "format", "", "Output format: json or text (default: text)")
	pf.StringVar(&flagConfigFile, "config", "", "Config file path (default: ~/.config/pasture/config.yaml)")
}

// resolveConfig resolves the full PastureMsgConfig for the current command.
// CLI flags override environment variables which override the YAML config file.
func resolveConfig(cmd *cobra.Command) config.PastureMsgConfig {
	if flagConfigFile != "" {
		return config.ResolvePastureMsgConfigFromFile(cmd, flagConfigFile)
	}
	return config.ResolvePastureMsgConfig(cmd)
}

// resolveFormat resolves the output format from the --format flag or config default.
func resolveFormat(cmd *cobra.Command, cfg config.PastureMsgConfig) types.OutputFormat {
	if f := cmd.Flags().Lookup("format"); f != nil && f.Changed {
		return types.OutputFormat(flagFormat)
	}
	pf := cmd.Root().PersistentFlags().Lookup("format")
	if pf != nil && pf.Changed {
		return types.OutputFormat(flagFormat)
	}
	return cfg.DefaultFormat
}

// printError writes a structured error report to stderr and is used by RunE
// handlers to produce actionable output before returning the error.
func printError(err error) {
	fmt.Fprintln(os.Stderr, err)
}
