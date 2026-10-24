package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// projectCmd owns the explicit project-to-epoch mapping surface. Project
// identity comes from Peasant (projectHash); the mapped epoch is a Pasture task.
// The mapping is a project-level relationship, separate from session bindings.
var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Map Peasant projects to epochs",
	Long: `Map a Peasant-owned project identity to a Pasture epoch.

The project hash is the opaque 64-character lowercase hex identity Peasant
reports; Pasture never derives or recomputes it. The epoch must already exist in
the task store. A project has at most one active mapping: binding a new epoch
closes the previous mapping and keeps the full interval history, and unbind
closes the active mapping without deleting history.

These mappings are a project-level relationship. They never write, override, or
rank session bindings, and the session read model does not consult them.

Commands:
  project list                         show projects with an active mapping
  project show <projectHash>           show the active mapping and history
  project bind <projectHash> --epoch <id> [--role <role>]
  project unbind <projectHash>         close the active mapping

The commands operate on an existing store and never create one.`,
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List projects with an active epoch mapping",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		code, err := handlers.ProjectList(cmd.Context(), handlers.ProjectListInput{
			DBPath: flagDBPath,
			Output: cmd.OutOrStdout(),
		}, resolveFormat())
		return finishSessionCommand(code, err)
	},
}

var projectShowCmd = &cobra.Command{
	Use:   "show <projectHash>",
	Short: "Show one project's active mapping and history",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := handlers.ProjectShow(cmd.Context(), handlers.ProjectShowInput{
			DBPath:      flagDBPath,
			ProjectHash: args[0],
			Output:      cmd.OutOrStdout(),
		}, resolveFormat())
		return finishSessionCommand(code, err)
	},
}

var projectBindCmd = &cobra.Command{
	Use:   "bind <projectHash> --epoch <epoch-id>",
	Short: "Map a project to an epoch",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		epoch, _ := cmd.Flags().GetString("epoch")
		role, _ := cmd.Flags().GetString("role")
		expected, _ := cmd.Flags().GetString("expected-binding")
		code, err := handlers.ProjectBind(cmd.Context(), handlers.ProjectBindInput{
			DBPath:          flagDBPath,
			ProjectHash:     args[0],
			EpochID:         epoch,
			Role:            role,
			ExpectedBinding: expected,
			Output:          cmd.OutOrStdout(),
		}, resolveFormat())
		return finishSessionCommand(code, err)
	},
}

var projectUnbindCmd = &cobra.Command{
	Use:   "unbind <projectHash>",
	Short: "Close a project's active epoch mapping",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		expected, _ := cmd.Flags().GetString("expected-binding")
		code, err := handlers.ProjectUnbind(cmd.Context(), handlers.ProjectUnbindInput{
			DBPath:          flagDBPath,
			ProjectHash:     args[0],
			ExpectedBinding: expected,
			Output:          cmd.OutOrStdout(),
		}, resolveFormat())
		return finishSessionCommand(code, err)
	},
}

func init() {
	projectBindCmd.Flags().String("epoch", "", "Epoch task ID to map the project to")
	projectBindCmd.Flags().String("role", string(tasks.DefaultProjectBindingRole), "Project role in the epoch: epoch, architect, reviewer, supervisor, or worker")
	projectBindCmd.Flags().String("expected-binding", "", "Only change the mapping if this binding ID is still active")
	projectUnbindCmd.Flags().String("expected-binding", "", "Only close the mapping if this binding ID is still active")

	projectCmd.AddCommand(projectListCmd, projectShowCmd, projectBindCmd, projectUnbindCmd)
	rootCmd.AddCommand(projectCmd)
}
