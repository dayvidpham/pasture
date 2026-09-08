package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/spf13/cobra"
)

func newGateRebuildIndexCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "rebuild-index",
		Short: "Recover the assignment index from authenticated task history",
		Long: `Recover the derived index of authenticated started assignments. This maintenance
command does not determine whether an assignment is active. It leaves authoritative
task and assignment history unchanged.

Repeating without --reset over unchanged history preserves assignment rows and the
coverage watermark. An incomplete run resumes its captured journal snapshot from
the durable cursor. Run again after completion to include later history.

A dirty generation requires --reset. Using --reset retires only derived data and
starts a new generation; it does not repair damaged authoritative history.
Use --generation to refuse a recovery or reset if another reset changed the
generation. On a mismatch, confirm the database path and use the reported current
value with --generation, or omit the guard only after confirming the change.

A proven-empty assignment history is initialized automatically; no manual rebuild
is required for first use. Use this command for recovery of missing or incomplete
coverage, not on every lifecycle hook.

The database path must not contain ?, #, or %: the SQLite opener interprets them
as URI syntax rather than literal filename characters. Select an unambiguous
filesystem path with --db or PASTURE_DB_PATH.

Example:
  pasture gate rebuild-index --db /path/to/pasture.db`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reset, err := cmd.Flags().GetBool("reset")
			if err != nil {
				return err
			}
			path := flagDBPath
			if path == "" {
				path = tasks.DefaultDBPath()
			}
			// The shared opener embeds this path in a SQLite URI. Refuse aliases
			// before it can create or maintain a different file than requested.
			if strings.ContainsAny(path, "?#%") {
				return fmt.Errorf("gate rebuild-index: database path %q contains URI syntax (?, #, or %%) that the SQLite opener does not treat as a literal filename. No assignment rebuild was started; provide --db or PASTURE_DB_PATH with an unambiguous filesystem path without those characters", path)
			}
			tracker, err := tasks.OpenTaskTracker(path)
			if err != nil {
				return fmt.Errorf("gate rebuild-index: opening database %q failed: %w. No assignment rebuild was started; provide --db with the intended database file in a writable directory, not a directory or connection string", path, err)
			}
			defer tracker.Close()
			options := tasks.AssignmentIndexRebuildOptions{Reset: reset}
			if cmd.Flags().Changed("generation") {
				value, err := cmd.Flags().GetInt64("generation")
				if err != nil {
					return err
				}
				options.ExpectedGeneration = &value
			}
			if err := tasks.RebuildAssignmentIndex(cmd.Context(), tracker, options); err != nil {
				// The shared stale-index wrapper describes hook catch-up persistence.
				// Report its actual recovery cause at this operator boundary instead.
				var stale *tasks.IndexStaleError
				if errors.As(err, &stale) && stale.Cause != nil {
					err = stale.Cause
				}
				return fmt.Errorf("gate rebuild-index: recovery of database %q did not complete: %w. Inspect the cause before retrying; a saved scan can remain. For a dirty generation, use --reset. For a generation mismatch, confirm the database path and use the reported current value with --generation, or omit the guard only after confirming the change", path, err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Assignment index rebuilt and its captured journal prefix verified.")
			return err
		},
	}
	command.Flags().Bool("reset", false, "Start a new recovery generation from zero, retiring the derived index only")
	command.Flags().Int64("generation", 0, "Refuse the operation if the current generation differs from this value")
	return command
}

func init() {
	gate := &cobra.Command{Use: "gate", Short: "Maintain the derived assignment index used by task gates"}
	gate.AddCommand(newGateRebuildIndexCommand())
	rootCmd.AddCommand(gate)
}
