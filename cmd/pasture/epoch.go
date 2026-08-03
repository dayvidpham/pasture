package main

import (
	"github.com/spf13/cobra"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
)

// epochCmd contains the typed, deterministic workflow lifecycle surface. Task
// CRUD and knowledge-management operations remain under `pasture task`.
var epochCmd = &cobra.Command{
	Use:   "epoch",
	Short: "Run deterministic epoch lifecycle operations",
}

var epochInteractionModeCmd = &cobra.Command{
	Use:   "interaction-mode",
	Short: "Record or inspect the epoch interaction mode",
}

var epochInteractionModeSetCmd = &cobra.Command{
	Use:   "set <normal|afk>",
	Short: "Record an explicit interaction-mode decision",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochSetInteractionMode(cmd.Context(), invocation, epochFlag(cmd, "epoch"), args[0], epochFlag(cmd, "actor"))
		})
	},
}

var epochInteractionModeShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the effective interaction mode",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochShowInteractionMode(cmd.Context(), invocation, epochFlag(cmd, "epoch"))
		})
	},
}

var epochReviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Run typed review lifecycle operations",
}

var epochReviewStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a review for a proposal revision or implementation candidate",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochStartReview(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "subject"))
		})
	},
}

var epochReviewSubmitCmd = &cobra.Command{
	Use:   "submit",
	Short: "Submit a strict plan or implementation review",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochSubmitReview(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "round"), epochFlag(cmd, "axis"), epochFlag(cmd, "assignment"))
		})
	},
}

var epochReviewFinalizeCmd = &cobra.Command{
	Use:   "finalize",
	Short: "Finalize a complete three-axis review round",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochFinalizeReview(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "round"), epochFlag(cmd, "assignment"))
		})
	},
}

var epochSliceCmd = &cobra.Command{
	Use:   "slice",
	Short: "Run assignment-controlled slice lifecycle operations",
}

var epochSliceCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an implementation slice",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochCreateSlice(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "plan"), epochFlag(cmd, "assignment"))
		})
	},
}

var epochSliceCandidateCmd = &cobra.Command{
	Use:   "candidate",
	Short: "Manage an implementation candidate for a slice",
}

var epochSliceCandidateSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Record an immutable slice candidate",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochSetSliceCandidate(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "slice"), epochFlag(cmd, "repository"), epochFlag(cmd, "commit"), epochFlag(cmd, "assignment"))
		})
	},
}

var epochSliceReworkCmd = &cobra.Command{
	Use:   "rework",
	Short: "Replace a slice candidate after resolving review findings",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochReworkSlice(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "slice"), epochFlag(cmd, "candidate"), epochFlag(cmd, "assignment"))
		})
	},
}

var epochSliceCloseCmd = &cobra.Command{
	Use:   "close",
	Short: "Close a slice after its finalized review",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochCloseSlice(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "slice"), epochFlag(cmd, "candidate"), epochFlag(cmd, "review-round"), epochFlag(cmd, "assignment"))
		})
	},
}

var epochIntegrationCmd = &cobra.Command{
	Use:   "integration",
	Short: "Run assignment-controlled integration lifecycle operations",
}

var epochIntegrationCandidateSetCmd = &cobra.Command{
	Use:   "candidate-set",
	Short: "Manage integration candidate sets",
}

var epochIntegrationCandidateSetCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a complete multi-repository integration candidate",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochCreateIntegrationCandidate(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "plan"), epochFlag(cmd, "assignment"))
		})
	},
}

var epochIntegrationCandidateSetReworkCmd = &cobra.Command{
	Use:   "rework",
	Short: "Replace an integration candidate after resolving findings",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochReworkIntegrationCandidate(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "candidate"), epochFlag(cmd, "assignment"))
		})
	},
}

var epochIntegrationPublishRepositoryCmd = &cobra.Command{
	Use:   "publish-repository",
	Short: "Record verified publication for an integration member",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochPublishRepository(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "candidate"), epochFlag(cmd, "repository"), epochFlag(cmd, "ref"), epochFlag(cmd, "commit"), epochFlag(cmd, "assignment"))
		})
	},
}

var epochPlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Run plan UAT and ratification decisions",
}

var epochPlanUATCmd = &cobra.Command{
	Use:   "uat",
	Short: "Record an explicit Plan UAT decision",
}

var epochPlanUATAcceptCmd = &cobra.Command{
	Use:   "accept",
	Short: "Accept a plan through an explicit human decision",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochAcceptPlanUAT(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "proposal"), epochFlag(cmd, "actor"))
		})
	},
}

var epochPlanUATChangesRequestCmd = &cobra.Command{
	Use:   "changes-request",
	Short: "Request plan changes with strict UAT feedback input",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochRequestPlanChanges(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "proposal"), epochFlag(cmd, "actor"))
		})
	},
}

var epochPlanUATDeferCmd = &cobra.Command{
	Use:   "defer",
	Short: "Defer Plan UAT while the epoch is explicitly AFK",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochDeferPlanUAT(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "proposal"), epochFlag(cmd, "actor"))
		})
	},
}

var epochPlanRatifyCmd = &cobra.Command{
	Use:   "ratify",
	Short: "Ratify a plan through an explicit human decision",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochRatifyPlan(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "proposal"), epochFlag(cmd, "review-round"), epochFlag(cmd, "plan-uat"), epochFlag(cmd, "actor"))
		})
	},
}

var epochImplementationCmd = &cobra.Command{
	Use:   "implementation",
	Short: "Run implementation UAT decisions",
}

var epochImplementationUATCmd = &cobra.Command{
	Use:   "uat",
	Short: "Record an explicit Implementation UAT decision",
}

var epochImplementationUATAcceptCmd = &cobra.Command{
	Use:   "accept",
	Short: "Accept an integration candidate through an explicit human decision",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochAcceptImplementationUAT(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "candidate"), epochFlag(cmd, "actor"))
		})
	},
}

var epochImplementationUATChangesRequestCmd = &cobra.Command{
	Use:   "changes-request",
	Short: "Request implementation changes with strict UAT feedback input",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochRequestImplementationChanges(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "candidate"), epochFlag(cmd, "actor"))
		})
	},
}

var epochLandCmd = &cobra.Command{
	Use:   "land",
	Short: "Record landing through an explicit human decision",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runEpochCommand(cmd, func(invocation handlers.EpochCommandInvocation) error {
			return handlers.EpochLand(cmd.Context(), invocation, epochFlag(cmd, "epoch"), epochFlag(cmd, "candidate"), epochFlag(cmd, "implementation-uat"), epochFlag(cmd, "actor"))
		})
	},
}

type epochStringFlag struct {
	name string
	use  string
}

func addEpochStringFlags(command *cobra.Command, flags ...epochStringFlag) {
	for _, flag := range flags {
		command.Flags().String(flag.name, "", flag.use)
	}
}

func addEpochInputFlag(command *cobra.Command) {
	command.Flags().String("input", "", "Read this command's strict JSON input from standard input; the only accepted value is -")
}

func epochFlag(command *cobra.Command, name string) string {
	value, _ := command.Flags().GetString(name)
	return value
}

func epochInvocation(command *cobra.Command) handlers.EpochCommandInvocation {
	return handlers.EpochCommandInvocation{
		DBPath:        flagDBPath,
		InputArgument: epochFlag(command, "input"),
		Input:         command.InOrStdin(),
		Output:        command.OutOrStdout(),
	}
}

func runEpochCommand(command *cobra.Command, run func(handlers.EpochCommandInvocation) error) error {
	if err := run(epochInvocation(command)); err != nil {
		printError(err)
		exitWithCode(pasterrors.ExitCode(err))
	}
	return nil
}

func init() {
	addEpochStringFlags(epochInteractionModeSetCmd,
		epochStringFlag{"epoch", "Epoch task ID"},
		epochStringFlag{"actor", "Explicit registered human actor ID"},
	)
	addEpochStringFlags(epochInteractionModeShowCmd, epochStringFlag{"epoch", "Epoch task ID"})

	addEpochStringFlags(epochReviewStartCmd,
		epochStringFlag{"epoch", "Epoch task ID"},
		epochStringFlag{"subject", "Proposal revision or implementation candidate task ID"},
	)
	addEpochStringFlags(epochReviewSubmitCmd,
		epochStringFlag{"epoch", "Epoch task ID"},
		epochStringFlag{"round", "Review round task ID"},
		epochStringFlag{"axis", "Review axis: correctness, test-quality, or elegance"},
		epochStringFlag{"assignment", "Active reviewer assignment ID"},
	)
	addEpochInputFlag(epochReviewSubmitCmd)
	addEpochStringFlags(epochReviewFinalizeCmd,
		epochStringFlag{"epoch", "Epoch task ID"},
		epochStringFlag{"round", "Review round task ID"},
		epochStringFlag{"assignment", "Active governing assignment ID"},
	)

	addEpochStringFlags(epochSliceCreateCmd,
		epochStringFlag{"epoch", "Epoch task ID"},
		epochStringFlag{"plan", "Implementation plan task ID"},
		epochStringFlag{"assignment", "Active governing assignment ID"},
	)
	addEpochStringFlags(epochSliceCandidateSetCmd,
		epochStringFlag{"epoch", "Epoch task ID"},
		epochStringFlag{"slice", "Slice task ID"},
		epochStringFlag{"repository", "Repository identifier"},
		epochStringFlag{"commit", "Lowercase 40- or 64-hex Git object ID"},
		epochStringFlag{"assignment", "Active governing assignment ID"},
	)
	addEpochStringFlags(epochSliceReworkCmd,
		epochStringFlag{"epoch", "Epoch task ID"},
		epochStringFlag{"slice", "Slice task ID"},
		epochStringFlag{"candidate", "Current implementation candidate task ID"},
		epochStringFlag{"assignment", "Active governing assignment ID"},
	)
	addEpochInputFlag(epochSliceReworkCmd)
	addEpochStringFlags(epochSliceCloseCmd,
		epochStringFlag{"epoch", "Epoch task ID"},
		epochStringFlag{"slice", "Slice task ID"},
		epochStringFlag{"candidate", "Current implementation candidate task ID"},
		epochStringFlag{"review-round", "Finalized implementation review round task ID"},
		epochStringFlag{"assignment", "Active governing assignment ID"},
	)

	addEpochStringFlags(epochIntegrationCandidateSetCreateCmd,
		epochStringFlag{"epoch", "Epoch task ID"},
		epochStringFlag{"plan", "Implementation plan task ID"},
		epochStringFlag{"assignment", "Active governing assignment ID"},
	)
	addEpochInputFlag(epochIntegrationCandidateSetCreateCmd)
	addEpochStringFlags(epochIntegrationCandidateSetReworkCmd,
		epochStringFlag{"epoch", "Epoch task ID"},
		epochStringFlag{"candidate", "Current integration candidate task ID"},
		epochStringFlag{"assignment", "Active governing assignment ID"},
	)
	addEpochInputFlag(epochIntegrationCandidateSetReworkCmd)
	addEpochStringFlags(epochIntegrationPublishRepositoryCmd,
		epochStringFlag{"epoch", "Epoch task ID"},
		epochStringFlag{"candidate", "Current integration candidate task ID"},
		epochStringFlag{"repository", "Repository identifier"},
		epochStringFlag{"ref", "Verified remote Git ref"},
		epochStringFlag{"commit", "Lowercase 40- or 64-hex Git object ID"},
		epochStringFlag{"assignment", "Active governing assignment ID"},
	)

	for _, command := range []*cobra.Command{epochPlanUATAcceptCmd, epochPlanUATChangesRequestCmd, epochPlanUATDeferCmd} {
		addEpochStringFlags(command,
			epochStringFlag{"epoch", "Epoch task ID"},
			epochStringFlag{"proposal", "Proposal task ID"},
			epochStringFlag{"actor", "Explicit registered human actor ID"},
		)
	}
	addEpochInputFlag(epochPlanUATChangesRequestCmd)
	addEpochInputFlag(epochPlanUATDeferCmd)
	addEpochStringFlags(epochPlanRatifyCmd,
		epochStringFlag{"epoch", "Epoch task ID"},
		epochStringFlag{"proposal", "Proposal task ID"},
		epochStringFlag{"review-round", "Accepted review round task ID"},
		epochStringFlag{"plan-uat", "Accepted Plan UAT decision ID"},
		epochStringFlag{"actor", "Explicit registered human actor ID"},
	)

	for _, command := range []*cobra.Command{epochImplementationUATAcceptCmd, epochImplementationUATChangesRequestCmd} {
		addEpochStringFlags(command,
			epochStringFlag{"epoch", "Epoch task ID"},
			epochStringFlag{"candidate", "Integration candidate task ID"},
			epochStringFlag{"actor", "Explicit registered human actor ID"},
		)
	}
	addEpochInputFlag(epochImplementationUATChangesRequestCmd)
	addEpochStringFlags(epochLandCmd,
		epochStringFlag{"epoch", "Epoch task ID"},
		epochStringFlag{"candidate", "Integration candidate task ID"},
		epochStringFlag{"implementation-uat", "Accepted Implementation UAT decision ID"},
		epochStringFlag{"actor", "Explicit registered human actor ID"},
	)

	epochInteractionModeCmd.AddCommand(epochInteractionModeSetCmd, epochInteractionModeShowCmd)
	epochReviewCmd.AddCommand(epochReviewStartCmd, epochReviewSubmitCmd, epochReviewFinalizeCmd)
	epochSliceCandidateCmd.AddCommand(epochSliceCandidateSetCmd)
	epochSliceCmd.AddCommand(epochSliceCreateCmd, epochSliceCandidateCmd, epochSliceReworkCmd, epochSliceCloseCmd)
	epochIntegrationCandidateSetCmd.AddCommand(epochIntegrationCandidateSetCreateCmd, epochIntegrationCandidateSetReworkCmd)
	epochIntegrationCmd.AddCommand(epochIntegrationCandidateSetCmd, epochIntegrationPublishRepositoryCmd)
	epochPlanUATCmd.AddCommand(epochPlanUATAcceptCmd, epochPlanUATChangesRequestCmd, epochPlanUATDeferCmd)
	epochPlanCmd.AddCommand(epochPlanUATCmd, epochPlanRatifyCmd)
	epochImplementationUATCmd.AddCommand(epochImplementationUATAcceptCmd, epochImplementationUATChangesRequestCmd)
	epochImplementationCmd.AddCommand(epochImplementationUATCmd)
	epochCmd.AddCommand(epochInteractionModeCmd, epochReviewCmd, epochSliceCmd, epochIntegrationCmd, epochPlanCmd, epochImplementationCmd, epochLandCmd)
	rootCmd.AddCommand(epochCmd)
}
