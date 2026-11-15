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

var taskAssignmentStartCmd = &cobra.Command{
	Use:   "start TASK-ID",
	Short: "Start an owner-responsibility assignment on an existing task",
	Long:  "Start an owner-responsibility assignment. The actor records attribution; it does not authenticate the caller. Run one supervisor process per store: the released API has no cross-process no-active-owner precondition.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := handlers.TaskStartAssignment(cmd.Context(), handlers.TaskStartAssignmentInput{
			DBPath:           flagDBPath,
			TaskID:           args[0],
			Assignment:       taskAssignmentFlag(cmd, "assignment"),
			Actor:            taskAssignmentFlag(cmd, "actor"),
			Occupant:         taskAssignmentFlag(cmd, "occupant"),
			ParentTask:       taskAssignmentFlag(cmd, "parent-task"),
			ParentAssignment: taskAssignmentFlag(cmd, "parent-assignment"),
			Output:           cmd.OutOrStdout(),
		})
		if err != nil {
			printError(err)
			exitWithCode(pasterrors.ExitCode(err))
		}
		return nil
	},
}

var taskAssignmentShowCmd = &cobra.Command{
	Use:   "show TASK-ID",
	Short: "Show the active owner-responsibility assignment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := handlers.TaskShowAssignment(cmd.Context(), handlers.TaskShowAssignmentInput{
			DBPath: flagDBPath,
			TaskID: args[0],
			Output: cmd.OutOrStdout(),
		})
		if err != nil {
			printError(err)
			exitWithCode(pasterrors.ExitCode(err))
		}
		return nil
	},
}

var taskAssignmentEndCmd = &cobra.Command{
	Use:   "end TASK-ID",
	Short: "End the exact active assignment on the owner-responsibility slot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := handlers.TaskEndAssignment(cmd.Context(), handlers.TaskEndAssignmentInput{
			DBPath:     flagDBPath,
			TaskID:     args[0],
			Assignment: taskAssignmentFlag(cmd, "assignment"),
			Actor:      taskAssignmentFlag(cmd, "actor"),
			Output:     cmd.OutOrStdout(),
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
	taskAssignmentStartCmd.Flags().String("assignment", "", "New assignment ID")
	taskAssignmentStartCmd.Flags().String("actor", "", "Committing registered actor ID (attribution, not authentication)")
	taskAssignmentStartCmd.Flags().String("occupant", "", "Registered worker occupant ID")
	taskAssignmentStartCmd.Flags().String("parent-task", "", "Parent task ID (requires --parent-assignment)")
	taskAssignmentStartCmd.Flags().String("parent-assignment", "", "Exact active parent assignment ID")
	taskAssignmentEndCmd.Flags().String("assignment", "", "Exact active assignment ID")
	taskAssignmentEndCmd.Flags().String("actor", "", "Committing registered actor ID (attribution, not authentication)")
	taskAssignmentTransferCmd.Flags().String("slot", "", "Assignment slot: owner-responsibility")
	taskAssignmentTransferCmd.Flags().String("assignment", "", "Successor assignment ID")
	taskAssignmentTransferCmd.Flags().String("actor", "", "Committing registered actor ID")
	taskAssignmentTransferCmd.Flags().String("occupant", "", "Successor registered occupant ID")
	taskAssignmentCmd.AddCommand(taskAssignmentStartCmd, taskAssignmentShowCmd, taskAssignmentEndCmd, taskAssignmentTransferCmd)
	taskCmd.AddCommand(taskAssignmentCmd)
}
