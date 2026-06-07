package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// taskEventsCmd implements `pasture task events` (PROPOSAL-2 §7.9).
//
// At least one of {--epoch-id, --context-kind+--context-id} MUST be supplied.
// The handler enforces this and surfaces an actionable error if missing.
var taskEventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Query audit events with optional filters",
	Long: `Query audit events recorded by epoch workflows and free-floating handlers.

At least one top-level filter must be supplied:
  --epoch-id <id>                              all events for one epoch
  --context-kind <K> --context-id <ID>         all events tied to a context

Optional filters narrow the result further:
  --phase <p>      filter by phase (e.g. p9, code-review)
  --agent <name>   filter by agent (matched against the event's recording role)
  --type <T>       filter by EventType (e.g. PhaseTransition)
  --since <ts>     RFC3339 timestamp or Unix epoch (seconds or nanoseconds)

Context kinds: EpochContext, SliceContext, ReviewContext, FollowupContext,
GitContext, SkillContext, SessionContext.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		in := handlers.TaskEventsInput{
			DBPath: flagDBPath,
		}

		if cmd.Flags().Changed("epoch-id") {
			in.EpochId, _ = cmd.Flags().GetString("epoch-id")
		}
		if cmd.Flags().Changed("phase") {
			raw, _ := cmd.Flags().GetString("phase")
			ph, err := protocol.ParsePhaseId(raw)
			if err != nil {
				printError(err)
				exitWithCode(1)
			}
			in.Phase = &ph
		}
		if cmd.Flags().Changed("agent") {
			in.Agent, _ = cmd.Flags().GetString("agent")
		}
		if cmd.Flags().Changed("type") {
			raw, _ := cmd.Flags().GetString("type")
			et, err := handlers.ParseEventTypeFlag(raw)
			if err != nil {
				printError(err)
				exitWithCode(1)
			}
			in.EventType = &et
		}
		if cmd.Flags().Changed("since") {
			raw, _ := cmd.Flags().GetString("since")
			ts, err := handlers.ParseSinceFlag(raw)
			if err != nil {
				printError(err)
				exitWithCode(1)
			}
			in.Since = &ts
		}
		if cmd.Flags().Changed("context-kind") {
			raw, _ := cmd.Flags().GetString("context-kind")
			k, err := handlers.ParseContextKindFlag(raw)
			if err != nil {
				printError(err)
				exitWithCode(1)
			}
			in.ContextKind = &k
		}
		if cmd.Flags().Changed("context-id") {
			in.ContextId, _ = cmd.Flags().GetString("context-id")
		}

		code, hErr := handlers.TaskEvents(cmd.OutOrStdout(), in, resolveFormat())
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
	taskEventsCmd.Flags().String("epoch-id", "", "Filter by epoch ID (wire-format Provenance TaskId)")
	taskEventsCmd.Flags().String("phase", "", "Filter by phase (e.g. p9, code-review)")
	taskEventsCmd.Flags().String("agent", "", "Filter by agent (matched against Role until v3 backfill lands)")
	taskEventsCmd.Flags().String("type", "", "Filter by EventType (e.g. PhaseTransition)")
	taskEventsCmd.Flags().String("since", "", "RFC3339 timestamp or Unix epoch (seconds or nanoseconds)")
	taskEventsCmd.Flags().String("context-kind", "",
		"Context kind (EpochContext, SliceContext, ReviewContext, FollowupContext, GitContext, SkillContext, SessionContext)")
	taskEventsCmd.Flags().String("context-id", "",
		"Context id (epoch task-id, git SHA, skill run-id, etc.)")

	taskCmd.AddCommand(taskEventsCmd)
}
