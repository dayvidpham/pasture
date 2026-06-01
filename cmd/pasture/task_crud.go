package main

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// taskCreateCmd implements `pasture task create`.
var taskCreateCmd = &cobra.Command{
	Use:   "create TITLE",
	Short: "Create a new task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		desc, _ := cmd.Flags().GetString("description")
		typeStr, _ := cmd.Flags().GetString("type")
		priStr, _ := cmd.Flags().GetString("priority")
		phaseStr, _ := cmd.Flags().GetString("phase")

		tt, err := parseTaskType(typeStr)
		if err != nil {
			printError(err)
			exitWithCode(1)
		}
		pr, err := parsePriority(priStr)
		if err != nil {
			printError(err)
			exitWithCode(1)
		}
		ph, err := parsePhase(phaseStr)
		if err != nil {
			printError(err)
			exitWithCode(1)
		}

		code, hErr := handlers.TaskCreate(cmd.OutOrStdout(), handlers.TaskCreateInput{
			DBPath:      flagDBPath,
			Namespace:   flagNamespace,
			Title:       args[0],
			Description: desc,
			Type:        tt,
			Priority:    pr,
			Phase:       ph,
		}, resolveFormat())
		if hErr != nil {
			printError(hErr)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

// taskShowCmd implements `pasture task show ID`.
var taskShowCmd = &cobra.Command{
	Use:   "show ID",
	Short: "Show a task by its ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := handlers.TaskShow(cmd.OutOrStdout(), flagDBPath, args[0], resolveFormat())
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

// taskUpdateCmd implements `pasture task update ID --status=... --priority=... etc`.
var taskUpdateCmd = &cobra.Command{
	Use:   "update ID",
	Short: "Update fields on an existing task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		in := handlers.TaskUpdateInput{
			DBPath: flagDBPath,
			IdStr:  args[0],
		}
		if cmd.Flags().Changed("title") {
			s, _ := cmd.Flags().GetString("title")
			in.Title = &s
		}
		if cmd.Flags().Changed("description") {
			s, _ := cmd.Flags().GetString("description")
			in.Description = &s
		}
		if cmd.Flags().Changed("notes") {
			s, _ := cmd.Flags().GetString("notes")
			in.Notes = &s
		}
		if cmd.Flags().Changed("status") {
			raw, _ := cmd.Flags().GetString("status")
			st, err := parseStatus(raw)
			if err != nil {
				printError(err)
				exitWithCode(1)
			}
			in.Status = &st
		}
		if cmd.Flags().Changed("priority") {
			raw, _ := cmd.Flags().GetString("priority")
			pr, err := parsePriority(raw)
			if err != nil {
				printError(err)
				exitWithCode(1)
			}
			in.Priority = &pr
		}
		if cmd.Flags().Changed("phase") {
			raw, _ := cmd.Flags().GetString("phase")
			ph, err := parsePhase(raw)
			if err != nil {
				printError(err)
				exitWithCode(1)
			}
			in.Phase = &ph
		}

		code, err := handlers.TaskUpdate(cmd.OutOrStdout(), in, resolveFormat())
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

// taskCloseCmd implements `pasture task close ID --reason="..."`.
var taskCloseCmd = &cobra.Command{
	Use:   "close ID",
	Short: "Close a task with an optional reason",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reason, _ := cmd.Flags().GetString("reason")
		code, err := handlers.TaskClose(cmd.OutOrStdout(), flagDBPath, args[0], reason, resolveFormat())
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

// taskListCmd implements `pasture task list [--status=open] [--label=...] [--phase=...] etc`.
var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tasks with optional filters",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		in := handlers.TaskListInput{DBPath: flagDBPath}

		if cmd.Flags().Changed("status") {
			raw, _ := cmd.Flags().GetString("status")
			st, err := parseStatus(raw)
			if err != nil {
				printError(err)
				exitWithCode(1)
			}
			in.Status = &st
		}
		if cmd.Flags().Changed("priority") {
			raw, _ := cmd.Flags().GetString("priority")
			pr, err := parsePriority(raw)
			if err != nil {
				printError(err)
				exitWithCode(1)
			}
			in.Priority = &pr
		}
		if cmd.Flags().Changed("type") {
			raw, _ := cmd.Flags().GetString("type")
			tt, err := parseTaskType(raw)
			if err != nil {
				printError(err)
				exitWithCode(1)
			}
			in.Type = &tt
		}
		if cmd.Flags().Changed("phase") {
			raw, _ := cmd.Flags().GetString("phase")
			ph, err := parsePhase(raw)
			if err != nil {
				printError(err)
				exitWithCode(1)
			}
			in.Phase = &ph
		}
		if cmd.Flags().Changed("label") {
			in.Label, _ = cmd.Flags().GetString("label")
		}
		if flagNamespace != "" {
			// Filter list results to the global --namespace when one was given.
			in.Namespace = flagNamespace
		}

		code, err := handlers.TaskList(cmd.OutOrStdout(), in, resolveFormat())
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
	// task create flags
	taskCreateCmd.Flags().String("description", "", "Long-form description for the task")
	taskCreateCmd.Flags().String("type", "task", "Task type: bug, feature, task, epic, chore")
	taskCreateCmd.Flags().String("priority", "medium", "Priority: critical, high, medium, low, backlog (also accepts 0..4 / P0..P4)")
	taskCreateCmd.Flags().String("phase", "unscoped",
		"Phase: request, elicit, propose, review, plan_uat, ratify, handoff, impl_plan, worker_slices, code_review, impl_uat, landing, unscoped")

	// task update flags
	taskUpdateCmd.Flags().String("title", "", "New title")
	taskUpdateCmd.Flags().String("description", "", "New description")
	taskUpdateCmd.Flags().String("notes", "", "Replace the notes field")
	taskUpdateCmd.Flags().String("status", "", "New status: open, in_progress, closed")
	taskUpdateCmd.Flags().String("priority", "", "New priority: critical, high, medium, low, backlog (also accepts 0..4 / P0..P4)")
	taskUpdateCmd.Flags().String("phase", "", "New phase (see `pasture task create --help` for values)")

	// task close flags
	taskCloseCmd.Flags().String("reason", "", "Human-readable reason for closing the task")

	// task list flags
	taskListCmd.Flags().String("status", "", "Filter by status: open, in_progress, closed")
	taskListCmd.Flags().String("priority", "", "Filter by priority")
	taskListCmd.Flags().String("type", "", "Filter by task type")
	taskListCmd.Flags().String("phase", "", "Filter by phase")
	taskListCmd.Flags().String("label", "", "Filter by label name")
	// Namespace filtering is driven by the global --namespace flag (registered on rootCmd).

	taskCmd.AddCommand(taskCreateCmd)
	taskCmd.AddCommand(taskShowCmd)
	taskCmd.AddCommand(taskUpdateCmd)
	taskCmd.AddCommand(taskCloseCmd)
	taskCmd.AddCommand(taskListCmd)
}

// parseStatus converts a CLI string into a provenance.Status. Accepts the
// canonical wire values (open, in_progress, closed).
func parseStatus(raw string) (provenance.Status, error) {
	var st provenance.Status
	if err := st.UnmarshalText([]byte(raw)); err != nil {
		return st, fmt.Errorf("invalid --status %q: %w — valid values: open, in_progress, closed", raw, err)
	}
	return st, nil
}

// parseTaskType converts a CLI string into a provenance.TaskType.
func parseTaskType(raw string) (provenance.TaskType, error) {
	var tt provenance.TaskType
	if err := tt.UnmarshalText([]byte(raw)); err != nil {
		return tt, fmt.Errorf("invalid --type %q: %w — valid values: bug, feature, task, epic, chore", raw, err)
	}
	return tt, nil
}

// parsePhase converts a CLI string into a provenance.Phase.
func parsePhase(raw string) (provenance.Phase, error) {
	var ph provenance.Phase
	if err := ph.UnmarshalText([]byte(raw)); err != nil {
		return ph, fmt.Errorf("invalid --phase %q: %w — see `pasture task create --help` for valid values", raw, err)
	}
	return ph, nil
}

// parsePriority accepts named values (critical/high/medium/low/backlog) as
// well as the numeric 0..4 / P0..P4 conventions used throughout the protocol.
func parsePriority(raw string) (provenance.Priority, error) {
	var pr provenance.Priority

	// Try the canonical name first (cheap, no allocations).
	if err := pr.UnmarshalText([]byte(raw)); err == nil {
		return pr, nil
	}

	// Allow "P0".."P4" as well as bare digits.
	digits := raw
	if len(raw) >= 2 && (raw[0] == 'P' || raw[0] == 'p') {
		digits = raw[1:]
	}
	n, err := strconv.Atoi(digits)
	if err == nil {
		pr = provenance.Priority(n)
		if pr.IsValid() {
			return pr, nil
		}
	}
	return pr, fmt.Errorf("invalid --priority %q — valid values: critical, high, medium, low, backlog (or 0..4 / P0..P4)", raw)
}
