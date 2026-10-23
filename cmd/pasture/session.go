package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// sessionCmd inspects the harness sessions recorded in the local pasture
// database. It is a read-only surface: it refreshes the session cache from the
// lifecycle journal, then renders the projection. Explicit bind and unbind are
// served by the local API, not by this command.
var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Inspect recorded harness sessions",
	Long: `Inspect the harness sessions recorded in the local pasture database.

Sessions come from the lifecycle journal and from explicit epoch bindings.
"list" shows one page as a compact table, or as the same JSON page the local
API returns. "show" renders one session identified by its harness and native
session ID.

These commands read the local database; they never contact a running daemon
and never change a session's binding or activity state.`,
}

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recorded harness sessions",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		harness, _ := cmd.Flags().GetString("harness")
		epoch, _ := cmd.Flags().GetString("epoch")
		state, _ := cmd.Flags().GetString("state")
		limit, _ := cmd.Flags().GetInt("limit")
		cursor, _ := cmd.Flags().GetString("cursor")

		code, err := handlers.SessionList(cmd.Context(), handlers.SessionListInput{
			DBPath:  flagDBPath,
			Harness: harness,
			Epoch:   epoch,
			State:   state,
			Limit:   limit,
			Cursor:  cursor,
			Output:  cmd.OutOrStdout(),
		}, resolveFormat())
		return finishSessionCommand(code, err)
	},
}

var sessionShowCmd = &cobra.Command{
	Use:   "show <harness> <session>",
	Short: "Show one recorded harness session",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := handlers.SessionShow(cmd.Context(), handlers.SessionShowInput{
			DBPath:  flagDBPath,
			Harness: args[0],
			Session: args[1],
			Output:  cmd.OutOrStdout(),
		}, resolveFormat())
		return finishSessionCommand(code, err)
	},
}

func init() {
	sessionListCmd.Flags().String("harness", "", "Only sessions for this harness (for example claude-code)")
	sessionListCmd.Flags().String("epoch", "", "Only sessions whose latest binding is this epoch ID")
	sessionListCmd.Flags().String("state", "", "Only sessions in this activity state: active, idle, ended, or unknown")
	sessionListCmd.Flags().Int("limit", 50, "Maximum number of sessions to return (1-100)")
	sessionListCmd.Flags().String("cursor", "", "Resume after the cursor printed by a previous page")

	sessionCmd.AddCommand(sessionListCmd, sessionShowCmd)
	rootCmd.AddCommand(sessionCmd)
}

// finishSessionCommand prints an actionable error report and exits with the
// handler's category-mapped code. Successful handlers print through the command
// writer and return a zero code.
func finishSessionCommand(code int, err error) error {
	if err != nil {
		printError(err)
	}
	if code != 0 {
		exitWithCode(code)
	}
	return nil
}
