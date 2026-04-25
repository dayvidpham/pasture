package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/types"
)

const version = "v0.1.0"

// rootCmd is the parent for all pasture subcommands.
// Global flags are registered here and resolved to per-command values via the
// flagFormat / flagDB helpers below.
var rootCmd = &cobra.Command{
	Use:   "pasture",
	Short: "Local task management for the Pasture toolkit",
	Long: `pasture manages tasks, dependencies, labels, and comments backed by a
local Provenance (PROV-O) SQLite database.

Unlike pasture-msg (which sends Temporal signals to the pastured daemon),
pasture operates entirely on the local task tracker — no daemon required.

Exit codes:
  0  success
  1  validation error (bad flags, missing arguments)
  2  storage error (cannot open or read tracker database)
  3  task error (not found, cycle detected, already closed, etc.)`,
	Version: version,
}

// Global flag values. Each subcommand reads these via the helper functions
// below to avoid threading the cobra.Command pointer through every handler.
var (
	flagDBPath    string
	flagFormat    string
	flagNamespace string
)

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&flagDBPath, "db", "",
		"Path to provenance SQLite database (env: PASTURE_DB_PATH, default: ~/.local/share/pasture/provenance.db)")
	pf.StringVar(&flagFormat, "format", "text",
		"Output format: text or json")
	pf.StringVar(&flagNamespace, "namespace", "",
		"Namespace URI for created tasks (default: derived from git remote, then file:// of cwd)")
}

// resolveFormat returns the typed OutputFormat for the current command.
// Defaults to text if --format was not supplied or is unknown.
func resolveFormat() types.OutputFormat {
	f := types.OutputFormat(flagFormat)
	if f.IsValid() {
		return f
	}
	return types.OutputText
}

// printError writes a structured error to stderr. RunE handlers call this
// before returning the error so the user always sees a message even when the
// cobra default error printer would otherwise truncate.
func printError(err error) {
	fmt.Fprintln(os.Stderr, err)
}
