package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/types"
)

const version = "v0.1.0"

// rootCmd is the parent for all pasture subcommands.
// Global flags are registered here and resolved to per-command values via the
// flagFormat / flagDB helpers below.
var rootCmd = &cobra.Command{
	Use:   "pasture",
	Short: "Local task management for the Pasture toolkit",
	Long: `pasture manages tasks, dependencies, labels, comments, and the audit-event
record backed by the unified Pasture SQLite database at
~/.local/share/pasture/pasture.db (PROPOSAL-2 §7.1).

Unlike pasture-msg (which sends Temporal signals to the pastured daemon),
pasture operates entirely on the local task + audit tracker — no daemon
required. The audit subsystem and Provenance subsystem share one file; the
auto-on-open migrator brings legacy databases up to the current schema on
first use (PROPOSAL-2 §7.10).

Exit codes:
  0  success
  1  validation error (bad flags, missing arguments)
  2  connection error (cannot open the database file)
  3  task error (not found, cycle detected, already closed, etc.)
  4  config error
  5  storage error (migration / schema failure)`,
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
		"Path to the unified pasture SQLite database (env: PASTURE_DB_PATH, default: ~/.local/share/pasture/pasture.db)")
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

// printError writes a structured error report to stderr and is used by RunE
// handlers to produce actionable output before returning the error.
//
// When the error chain contains a *pasterrors.StructuredError, the full
// Report (category, what, why, impact, fix) is written instead of just the
// one-line Error() string. This ensures operators see the Problem / Reason /
// Impact / How-to-fix body rather than a truncated single-line message.
func printError(err error) {
	var se *pasterrors.StructuredError
	if errors.As(err, &se) {
		se.Report(os.Stderr)
		return
	}
	fmt.Fprintln(os.Stderr, err)
}
