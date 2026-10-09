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

	taskCmd.AddCommand(&cobra.Command{
		Use:   "comments ID",
		Short: "Read task comments in chronological order",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			code, err := handlers.TaskComments(cmd.OutOrStdout(), flagDBPath, args[0], resolveFormat())
			return finishTaskCommand(code, err)
		},
	})

	labelCmd := &cobra.Command{
		Use:   "label",
		Short: "Manage task labels",
	}
	for _, name := range []string{"add", "remove", "list"} {
		n := 2
		use := name + " ID LABEL"
		if name == "list" {
			n = 1
			use = "list ID"
		}

		labelCmd.AddCommand(&cobra.Command{
			Use:   use,
			Short: name + " task labels",
			Args:  cobra.ExactArgs(n),
			RunE: func(cmd *cobra.Command, args []string) error {
				var code int
				var err error
				switch cmd.Name() {
				case "add":
					code, err = handlers.TaskLabelAdd(cmd.OutOrStdout(), flagDBPath, args[0], args[1], resolveFormat())
				case "remove":
					code, err = handlers.TaskLabelRemove(cmd.OutOrStdout(), flagDBPath, args[0], args[1], resolveFormat())
				case "list":
					code, err = handlers.TaskLabelList(cmd.OutOrStdout(), flagDBPath, args[0], resolveFormat())
				}
				return finishTaskCommand(code, err)
			},
		})
	}
	taskCmd.AddCommand(labelCmd)

	agentsCmd := &cobra.Command{
		Use:   "agents",
		Short: "Register and discover comment authors",
	}
	agentsCmd.AddCommand(newTaskAgentsRegisterCmd())
	agentsCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all registered agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			code, err := handlers.TaskAgentsList(cmd.OutOrStdout(), flagDBPath, resolveFormat())
			return finishTaskCommand(code, err)
		},
	})
	agentsCmd.AddCommand(&cobra.Command{
		Use:   "show AGENT-ID",
		Short: "Show a registered agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			code, err := handlers.TaskAgentsShow(cmd.OutOrStdout(), flagDBPath, args[0], resolveFormat())
			return finishTaskCommand(code, err)
		},
	})
	taskCmd.AddCommand(agentsCmd)
}
