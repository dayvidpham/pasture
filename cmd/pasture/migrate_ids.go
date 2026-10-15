package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// migrateIDsCmd implements `pasture migrate ids [--dry-run]`: the one-time
// offline conversion of the store's task/actor identity format.
//
// It lives under `pasture migrate` because it is a database-level operation,
// not a `task` verb. Plain `pasture migrate` keeps its schema-only behaviour;
// this command performs the identity conversion BEFORE any normal migration or
// open, because a normal open refuses an unconverted store.
var migrateIDsCmd = &cobra.Command{
	Use:   "ids",
	Short: "Convert the store's task/actor identity format once (offline; or preview with --dry-run)",
	Long: `Convert the unified pasture database from the retired two-dash task identity
format ("<namespace>--<uuid>") to the slash format ("<namespace>/<uuid>") once.

This is an OFFLINE operation: stop every writer (the daemon and any CLI or hook
process) before running it without --dry-run. The command makes a consistent
backup first, then rewrites every typed identity, canonical mutation and
allocation receipt in one transaction, replays the lifecycle projection from
the converted journal, verifies owner integrity, and publishes the format
marker LAST. Any failure rolls the whole transaction back.

With --dry-run: reports the store path, format, affected rows/fields, runtime
blockers and the backup location, and leaves the file's bytes (and SHA-256)
unchanged.

After conversion the old two-dash IDs never resolve again, and an older pasture
binary refuses this database for every command until it is upgraded. The backup
keeps the original bytes.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dry, _ := cmd.Flags().GetBool("dry-run")
		backupDir, _ := cmd.Flags().GetString("backup-dir")
		in := handlers.MigrateIDsInput{
			DBPath:    flagDBPath,
			DryRun:    dry,
			BackupDir: backupDir,
		}
		code, hErr := handlers.MigrateIDs(cmd.OutOrStdout(), in, resolveFormat())
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
	migrateIDsCmd.Flags().Bool("dry-run", false,
		"Report what the conversion would change without modifying the file (file SHA-256 unchanged)")
	migrateIDsCmd.Flags().String("backup-dir", "",
		"Directory for the consistent backup (default: the database's own directory)")

	migrateCmd.AddCommand(migrateIDsCmd)
}
