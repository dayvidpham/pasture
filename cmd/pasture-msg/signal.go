package main

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

// signalCmd is the "signal" subcommand group.
var signalCmd = &cobra.Command{
	Use:   "signal",
	Short: "Send signals to running workflows",
	Long:  "Send Temporal signals to running epoch or slice workflows.",
}

// signalVoteCmd implements "pasture-msg signal vote".
var signalVoteCmd = &cobra.Command{
	Use:   "vote",
	Short: "Send a review vote signal",
	Long: `Send a ReviewVoteSignal to the EpochWorkflow.

Axes: correctness, test_quality, elegance
Votes: ACCEPT, REVISE (case-insensitive)

The reviewer-id identifies the agent submitting the vote. It is optional but
recommended for audit trail completeness.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := resolveConfig(cmd)
		if err != nil {
			return err
		}
		format := resolveFormat(cmd, cfg)

		epochID, _ := cmd.Flags().GetString("epoch-id")
		axisStr, _ := cmd.Flags().GetString("axis")
		voteStr, _ := cmd.Flags().GetString("vote")
		reviewerID, _ := cmd.Flags().GetString("reviewer-id")

		// Normalize vote to uppercase (D13: normalize at CLI boundary).
		vote := types.VoteType(strings.ToUpper(voteStr))
		axis := types.ReviewAxis(axisStr)

		code, err := handlers.SignalVote(
			context.Background(),
			cfg.Connection,
			epochID, axis, vote, reviewerID,
			format,
			nil,
		)
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

// signalCompleteCmd implements "pasture-msg signal complete".
var signalCompleteCmd = &cobra.Command{
	Use:   "complete",
	Short: "Signal slice completion",
	Long: `Send a SliceProgressSignal marking a slice as completed.

Use --output for successful completion or --error for failed completion.
These flags are mutually exclusive.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := resolveConfig(cmd)
		if err != nil {
			return err
		}
		format := resolveFormat(cmd, cfg)

		epochID, _ := cmd.Flags().GetString("epoch-id")
		sliceID, _ := cmd.Flags().GetString("slice-id")

		var output *string
		var errMsg *string

		if cmd.Flags().Changed("output") {
			v, _ := cmd.Flags().GetString("output")
			output = &v
		}
		if cmd.Flags().Changed("error") {
			v, _ := cmd.Flags().GetString("error")
			errMsg = &v
		}

		code, err := handlers.SignalComplete(
			context.Background(),
			cfg.Connection,
			epochID, sliceID,
			output, errMsg,
			format,
			nil,
		)
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
	// signal vote flags
	signalVoteCmd.Flags().String("epoch-id", "", "Epoch workflow ID (required)")
	signalVoteCmd.Flags().String("axis", "", "Review axis: correctness, test_quality, or elegance (required)")
	signalVoteCmd.Flags().String("vote", "", "Vote: ACCEPT or REVISE (case-insensitive) (required)")
	signalVoteCmd.Flags().String("reviewer-id", "", "Reviewer agent identifier")
	_ = signalVoteCmd.MarkFlagRequired("epoch-id")
	_ = signalVoteCmd.MarkFlagRequired("axis")
	_ = signalVoteCmd.MarkFlagRequired("vote")

	// signal complete flags
	signalCompleteCmd.Flags().String("epoch-id", "", "Epoch workflow ID (required)")
	signalCompleteCmd.Flags().String("slice-id", "", "Slice workflow ID (required)")
	signalCompleteCmd.Flags().String("output", "", "Completion output message (mutually exclusive with --error)")
	signalCompleteCmd.Flags().String("error", "", "Completion error message (mutually exclusive with --output)")
	_ = signalCompleteCmd.MarkFlagRequired("epoch-id")
	_ = signalCompleteCmd.MarkFlagRequired("slice-id")

	signalCmd.AddCommand(signalVoteCmd)
	signalCmd.AddCommand(signalCompleteCmd)
	rootCmd.AddCommand(signalCmd)
}
