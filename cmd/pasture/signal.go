package main

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// signalCmd groups review/completion signal verbs.
var signalCmd = &cobra.Command{
	Use:   "signal",
	Short: "Send review and completion signals to an epoch",
}

var signalVoteCmd = &cobra.Command{
	Use:   "vote",
	Short: "Record a review vote",
	Long: `Send a review-vote signal to a running epoch.

Axes: correctness, test_quality, elegance. Votes: ACCEPT, REVISE (case-insensitive).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		epochId, _ := cmd.Flags().GetString("epoch-id")
		axisStr, _ := cmd.Flags().GetString("axis")
		voteStr, _ := cmd.Flags().GetString("vote")
		reviewerId, _ := cmd.Flags().GetString("reviewer-id")

		axis := protocol.ReviewAxis(axisStr)
		vote := protocol.VoteType(strings.ToUpper(voteStr))

		return runWithController(
			func() error { return handlers.ValidateSignalVote(epochId, axis, vote) },
			func(ctrl handlers.EpochController) (int, error) {
				return handlers.SignalVote(ctrl, epochId, axis, vote, reviewerId, resolveFormat())
			})
	},
}

var signalCompleteCmd = &cobra.Command{
	Use:   "complete",
	Short: "Report a slice completion to the epoch",
	Long: `Send a slice-progress completion signal to a running epoch.

Use --output for a successful completion or --error for a failure; they are
mutually exclusive.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		epochId, _ := cmd.Flags().GetString("epoch-id")
		sliceId, _ := cmd.Flags().GetString("slice-id")

		var output, errMsg *string
		if cmd.Flags().Changed("output") {
			v, _ := cmd.Flags().GetString("output")
			output = &v
		}
		if cmd.Flags().Changed("error") {
			v, _ := cmd.Flags().GetString("error")
			errMsg = &v
		}

		return runWithController(
			func() error { return handlers.ValidateSignalComplete(epochId, sliceId, output, errMsg) },
			func(ctrl handlers.EpochController) (int, error) {
				return handlers.SignalComplete(ctrl, epochId, sliceId, output, errMsg, resolveFormat())
			})
	},
}

func init() {
	signalVoteCmd.Flags().String("epoch-id", "", "Epoch ID (required)")
	signalVoteCmd.Flags().String("axis", "", "Review axis: correctness, test_quality, or elegance (required)")
	signalVoteCmd.Flags().String("vote", "", "Vote: ACCEPT or REVISE (required)")
	signalVoteCmd.Flags().String("reviewer-id", "", "Reviewer agent identifier")
	_ = signalVoteCmd.MarkFlagRequired("epoch-id")
	_ = signalVoteCmd.MarkFlagRequired("axis")
	_ = signalVoteCmd.MarkFlagRequired("vote")

	signalCompleteCmd.Flags().String("epoch-id", "", "Epoch ID (required)")
	signalCompleteCmd.Flags().String("slice-id", "", "Slice ID (required)")
	signalCompleteCmd.Flags().String("output", "", "Completion output (mutually exclusive with --error)")
	signalCompleteCmd.Flags().String("error", "", "Completion error (mutually exclusive with --output)")
	_ = signalCompleteCmd.MarkFlagRequired("epoch-id")
	_ = signalCompleteCmd.MarkFlagRequired("slice-id")

	signalCmd.AddCommand(signalVoteCmd, signalCompleteCmd)
	rootCmd.AddCommand(signalCmd)
}
