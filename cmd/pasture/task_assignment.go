package main

import (
	"github.com/spf13/cobra"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
)

// taskAssignmentCmd groups generic task-assignment operations. These are not
// epoch lifecycle controls and remain available below the task namespace.
var taskAssignmentCmd = &cobra.Command{
	Use:   "assignment",
	Short: "Manage task assignments",
}

var taskAssignmentTransferCmd = &cobra.Command{
	Use:   "transfer TASK-ID",
	Short: "Transfer the active owner-responsibility assignment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := handlers.TaskTransferAssignment(cmd.Context(), handlers.TaskTransferAssignmentInput{
			DBPath:         flagDBPath,
			TaskID:         args[0],
			Slot:           taskAssignmentFlag(cmd, "slot"),
			NextAssignment: taskAssignmentFlag(cmd, "assignment"),
			Actor:          taskAssignmentFlag(cmd, "actor"),
			Occupant:       taskAssignmentFlag(cmd, "occupant"),
			Output:         cmd.OutOrStdout(),
		})
		if err != nil {
			printError(err)
			exitWithCode(pasterrors.ExitCode(err))
		}
		return nil
	},
}

func taskAssignmentFlag(command *cobra.Command, name string) string {
	value, _ := command.Flags().GetString(name)
	return value
}

func init() {
	taskAssignmentTransferCmd.Flags().String("slot", "", "Assignment slot: owner-responsibility")
	taskAssignmentTransferCmd.Flags().String("assignment", "", "Successor assignment ID")
	taskAssignmentTransferCmd.Flags().String("actor", "", "Committing registered actor ID")
	taskAssignmentTransferCmd.Flags().String("occupant", "", "Successor registered occupant ID")
	taskAssignmentCmd.AddCommand(taskAssignmentTransferCmd)
	taskCmd.AddCommand(taskAssignmentCmd)
}
