package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// phaseCmd is the "phase" subcommand group.
var phaseCmd = &cobra.Command{
	Use:   "phase",
	Short: "Phase management",
	Long:  "Manage phase transitions in a running epoch workflow.",
}

// phaseAdvanceCmd implements "pasture-msg phase advance".
var phaseAdvanceCmd = &cobra.Command{
	Use:   "advance",
	Short: "Advance the epoch to the next phase",
	Long: `Send a PhaseAdvanceSignal to the running epoch workflow.

Valid phase IDs: p1, p2, p3, p4, p5, p6, p7, p8, p9, p10, p11, p12, complete

The triggered-by field identifies who sent the signal (e.g. a role name or
automated trigger). The condition field describes the protocol transition
condition that was satisfied.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := resolveConfig(cmd)
		format := resolveFormat(cmd, cfg)

		epochID, _ := cmd.Flags().GetString("epoch-id")
		toPhaseStr, _ := cmd.Flags().GetString("to-phase")
		triggeredBy, _ := cmd.Flags().GetString("triggered-by")
		condition, _ := cmd.Flags().GetString("condition")

		// Validate phase at CLI boundary to give a clear error before connecting.
		toPhase, err := protocol.ParsePhaseId(toPhaseStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "validation error: invalid phase %q: %v\n", toPhaseStr, err)
			fmt.Fprintln(os.Stderr, "  fix: use a valid phase ID: p1, p2, ... p12, complete")
			exitWithCode(1)
		}

		code, err := handlers.PhaseAdvance(
			context.Background(),
			cfg.Connection,
			epochID, toPhase, triggeredBy, condition,
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
	phaseAdvanceCmd.Flags().String("epoch-id", "", "Epoch workflow ID (required)")
	phaseAdvanceCmd.Flags().String("to-phase", "", "Target phase ID (e.g., p2, p10, complete) (required)")
	phaseAdvanceCmd.Flags().String("triggered-by", "", "Identifier of the triggering entity (e.g., supervisor)")
	phaseAdvanceCmd.Flags().String("condition", "", "Protocol condition that was satisfied")
	_ = phaseAdvanceCmd.MarkFlagRequired("epoch-id")
	_ = phaseAdvanceCmd.MarkFlagRequired("to-phase")

	phaseCmd.AddCommand(phaseAdvanceCmd)
	rootCmd.AddCommand(phaseCmd)
}
