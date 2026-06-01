package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// taskAgentsCmd is the parent for `pasture task agents` subcommands.
// It accepts no positional args itself; the work happens in `list` / `show`.
var taskAgentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "List or show registered agents and their categories",
	Long: `Inspect the agents registered in pasture_well_known_agents and the
pasture-side categories stored in pasture_agent_categories.

Subcommands:
  list           list all registered agents
  show <id>      show one agent by wire-format AgentId`,
}

var taskAgentsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered agents with their categories",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		code, hErr := handlers.TaskAgentsList(cmd.OutOrStdout(), flagDBPath, resolveFormat())
		if hErr != nil {
			printError(hErr)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

var taskAgentsShowCmd = &cobra.Command{
	Use:   "show AGENT-ID",
	Short: "Show one agent by wire-format AgentId",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code, hErr := handlers.TaskAgentsShow(cmd.OutOrStdout(), flagDBPath, args[0], resolveFormat())
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
	taskAgentsCmd.AddCommand(taskAgentsListCmd)
	taskAgentsCmd.AddCommand(taskAgentsShowCmd)
	taskCmd.AddCommand(taskAgentsCmd)
}
