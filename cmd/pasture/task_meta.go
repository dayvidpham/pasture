package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// taskLabelCmd is the parent for label management subcommands.
var taskLabelCmd = &cobra.Command{
	Use:   "label",
	Short: "Manage task labels",
}

var taskLabelAddCmd = &cobra.Command{
	Use:   "add ID LABEL",
	Short: "Attach a label to a task",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := handlers.TaskLabelAdd(cmd.OutOrStdout(), flagDBPath, args[0], args[1], resolveFormat())
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

var taskLabelRemoveCmd = &cobra.Command{
	Use:   "remove ID LABEL",
	Short: "Detach a label from a task",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := handlers.TaskLabelRemove(cmd.OutOrStdout(), flagDBPath, args[0], args[1], resolveFormat())
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

// taskCommentCmd implements `pasture task comment add`.
var taskCommentCmd = &cobra.Command{
	Use:   "comment",
	Short: "Manage comments on a task",
}

var taskCommentAddCmd = &cobra.Command{
	Use:   "add ID BODY",
	Short: "Add a comment to a task",
	Args:  cobra.ExactArgs(2),
	Long: `Add a timestamped comment to a task.

The author must be a registered Provenance agent. Pass --author with the
wire-format AgentID. Auto-resolution of the current user via git config is
tracked as a follow-up; for now agents must be registered out of band (see
the provenance Tracker.RegisterHumanAgent / RegisterSoftwareAgent APIs).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		author, _ := cmd.Flags().GetString("author")
		code, err := handlers.TaskCommentAdd(cmd.OutOrStdout(), handlers.TaskCommentAddInput{
			DBPath:   flagDBPath,
			IDStr:    args[0],
			AuthorID: author,
			Body:     args[1],
		}, resolveFormat())
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

// taskCommentsCmd implements `pasture task comments ID`.
var taskCommentsCmd = &cobra.Command{
	Use:   "comments ID",
	Short: "List all comments on a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := handlers.TaskComments(cmd.OutOrStdout(), flagDBPath, args[0], resolveFormat())
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

func init() {
	taskCommentAddCmd.Flags().String("author", "",
		"Wire-format AgentID of the comment author (required) — register agents via the Tracker API")
	_ = taskCommentAddCmd.MarkFlagRequired("author")

	taskLabelCmd.AddCommand(taskLabelAddCmd)
	taskLabelCmd.AddCommand(taskLabelRemoveCmd)

	taskCommentCmd.AddCommand(taskCommentAddCmd)

	taskCmd.AddCommand(taskLabelCmd)
	taskCmd.AddCommand(taskCommentCmd)
	taskCmd.AddCommand(taskCommentsCmd)
}
