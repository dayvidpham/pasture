package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// taskCommentCmd groups generic task comments.
var taskCommentCmd = &cobra.Command{
	Use:   "comment",
	Short: "Add an attributed comment to a task",
}

var taskCommentAddCmd = &cobra.Command{
	Use:   "add ID BODY",
	Short: "Add a comment to a task",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		author, _ := cmd.Flags().GetString("author")
		code, err := handlers.TaskCommentAdd(cmd.OutOrStdout(), handlers.TaskCommentAddInput{
			DBPath:   flagDBPath,
			IdStr:    args[0],
			AuthorId: author,
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

func init() {
	taskCommentAddCmd.Flags().String("author", "", "Registered author actor ID")
	taskCommentCmd.AddCommand(taskCommentAddCmd)
	taskCmd.AddCommand(taskCommentCmd)
}
