package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// phaseCmd groups phase-transition verbs.
var phaseCmd = &cobra.Command{
	Use:   "phase",
	Short: "Drive epoch phase transitions",
}

var phaseAdvanceCmd = &cobra.Command{
	Use:   "advance",
	Short: "Advance an epoch to a phase",
	Long: `Send an advance-phase signal to a running epoch.

Valid phases: request, elicit, propose, review, plan-review, ratify, handoff,
impl-plan, worker-slices, code-review, impl-uat, landing, complete. Also accepts
the pX shorthand (p1..p12).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		epochId, _ := cmd.Flags().GetString("epoch-id")
		toPhaseStr, _ := cmd.Flags().GetString("to")
		triggeredBy, _ := cmd.Flags().GetString("triggered-by")
		condition, _ := cmd.Flags().GetString("condition")

		return runWithController(func(ctrl handlers.EpochController) (int, error) {
			return handlers.PhaseAdvance(ctrl, epochId, protocol.PhaseId(toPhaseStr), triggeredBy, condition, resolveFormat())
		})
	},
}

func init() {
	phaseAdvanceCmd.Flags().String("epoch-id", "", "Epoch ID (required)")
	phaseAdvanceCmd.Flags().String("to", "", "Target phase name or pX (required)")
	phaseAdvanceCmd.Flags().String("triggered-by", "", "Who/what triggered the advance (e.g. a role)")
	phaseAdvanceCmd.Flags().String("condition", "", "Protocol condition that was satisfied")
	_ = phaseAdvanceCmd.MarkFlagRequired("epoch-id")
	_ = phaseAdvanceCmd.MarkFlagRequired("to")

	phaseCmd.AddCommand(phaseAdvanceCmd)
	rootCmd.AddCommand(phaseCmd)
}
