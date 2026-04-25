package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// migrateCmd implements the top-level `pasture migrate [--dry-run]` command.
//
// PROPOSAL-2 §7.9: this command lives at the `pasture` level (NOT under
// `pasture task`) because migration is a database-level operation, not a
// task-level one. The handler ensures that the explicit-command path and the
// auto-on-open path (OpenTaskTracker) share one migrator implementation —
// see internal/handlers/migrate.go for the routing rationale.
var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run pending audit-database schema migrations (or preview with --dry-run)",
	Long: `Run pending forward migrations on the unified pasture database.

Without --dry-run: opens the file via the same audit subsystem that
OpenTaskTracker uses, runs audit.Migrate, and prints
"migrated <db-path> from v<from> to v<to>".

With --dry-run: prints the planned migrations and exits 0 without modifying
the file (the file's SHA-256 is unchanged before and after).

Already-current databases are a no-op: running 'pasture migrate' twice in a
row prints "migrated <db-path> from v<n> to v<n>" the second time.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dry, _ := cmd.Flags().GetBool("dry-run")
		in := handlers.MigrateInput{
			DBPath: flagDBPath,
			DryRun: dry,
		}
		code, hErr := handlers.Migrate(cmd.OutOrStdout(), in, resolveFormat())
		if hErr != nil {
			printError(hErr)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

func init() {
	migrateCmd.Flags().Bool("dry-run", false,
		"Print the planned migrations without modifying the file (file SHA-256 unchanged)")

	rootCmd.AddCommand(migrateCmd)
}
