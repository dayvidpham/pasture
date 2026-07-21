package main

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/promotion"
	"github.com/spf13/cobra"
)

// newPromoteStableCmd builds the `promote-pasture-stable` subcommand: the gated,
// guarded promotion of the moving pasture-stable release channel. It wires the
// production revision resolver and git-backed guarded pusher, projects the
// aggregate marketplace from the pinned target descriptors, and runs the ordered
// gate set before performing exactly one guarded ref update.
func newPromoteStableCmd() *cobra.Command {
	var (
		pastureRevision string
		auraRevision    string
		expectedOldFlag string
		remote          string
		pastureRepo     string
		auraRepo        string
	)

	cmd := &cobra.Command{
		Use:   "promote-pasture-stable",
		Short: "Gated, guarded promotion of the moving pasture-stable release channel",
		Long: `promote-pasture-stable advances the pasture-stable ref to a reviewed Pasture
revision after an ordered gate set passes at the named revisions.

It re-reads the remote ref immediately before publication and performs exactly
one guarded update (git push --force-with-lease); it never overwrites a racing
publisher. A stale --expected-old, a racing advance, or a different ref is
rejected and leaves the channel unchanged.

Example:
  pasture-release promote-pasture-stable \
    --pasture-revision <sha> --aura-revision <sha> \
    --expected-old <sha|absent> --remote origin`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Default the pasture repo to the current git root.
			if pastureRepo == "" {
				root, err := repoRoot()
				if err != nil {
					return err
				}
				pastureRepo = root
			}
			if auraRepo == "" {
				return fmt.Errorf("validation error: --aura-repo is required because Aura and Pasture provenance must be verified as distinct repositories")
			}
			absPastureRepo, err := filepath.Abs(pastureRepo)
			if err != nil {
				return fmt.Errorf("validation error: invalid --pasture-repo %q — %w", pastureRepo, err)
			}
			absAuraRepo, err := filepath.Abs(auraRepo)
			if err != nil {
				return fmt.Errorf("validation error: invalid --aura-repo %q — %w", auraRepo, err)
			}

			pastureRepoID, err := effects.NewRepositoryID(absPastureRepo)
			if err != nil {
				return err
			}
			auraRepoID, err := effects.NewRepositoryID(absAuraRepo)
			if err != nil {
				return err
			}
			stableRef, err := effects.NewRemoteRef(promotion.DefaultStableRef)
			if err != nil {
				return err
			}
			expectedOld, err := promotion.ParseExpectedOld(expectedOldFlag)
			if err != nil {
				return err
			}

			request, err := promotion.NewPromotionRequest(
				pastureRepoID, pastureRevision,
				auraRepoID, auraRevision,
				remote, stableRef, expectedOld,
			)
			if err != nil {
				return err
			}

			pastureSnapshot, err := promotion.PrepareRepositorySnapshot(pastureRepoID, pastureRevision, remote, promotion.PastureRepository)
			if err != nil {
				return err
			}
			defer pastureSnapshot.Close()
			auraSnapshot, err := promotion.PrepareRepositorySnapshot(auraRepoID, auraRevision, "origin", promotion.AuraRepository)
			if err != nil {
				return err
			}
			defer auraSnapshot.Close()

			projection, err := promotion.ProjectClaudeCodeTree(pastureSnapshot.Repository.String(), "aura-plugins", pastureSnapshot.Commit.String())
			if err != nil {
				return err
			}

			gates, err := buildPromotionGates(
				pastureSnapshot.Repository,
				projection,
				filepath.Join(auraSnapshot.Repository.String(), ".claude-plugin", "marketplace.json"),
			)
			if err != nil {
				return err
			}

			resolver, err := promotion.NewGitRevisionResolver(exec.LookPath, effects.DefaultCommandRunner)
			if err != nil {
				return err
			}
			pusher, err := effects.NewGitRepositoryPusher(exec.LookPath, effects.DefaultCommandRunner, remote)
			if err != nil {
				return err
			}
			promoter, err := promotion.NewPromoter(resolver, pusher)
			if err != nil {
				return err
			}

			result, err := promoter.Promote(request, gates)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "promoted %s\n", result.Ref)
			fmt.Fprintf(out, "  commit:  %s\n", result.Commit)
			fmt.Fprintf(out, "  tree:    %s\n", result.Tree)
			fmt.Fprintf(out, "  outcome: %s\n", result.Outcome)
			fmt.Fprintf(out, "  marketplace: %s (%d plugins, ref %s)\n",
				projection.MarketplaceName, len(projection.Entries), projection.SourceRef)
			for _, e := range projection.Entries {
				fmt.Fprintf(out, "    - %s %s [%s]\n", e.Name, e.Version, e.ComponentID)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&pastureRevision, "pasture-revision", "", "Pasture revision (sha) to publish to pasture-stable (required)")
	cmd.Flags().StringVar(&auraRevision, "aura-revision", "", "Aura revision (sha) the marketplace/repository gate validates (required)")
	cmd.Flags().StringVar(&expectedOldFlag, "expected-old", "", "Expected current pasture-stable commit, or 'absent' for a first publication (required)")
	cmd.Flags().StringVar(&remote, "remote", "", "Git remote to publish the channel to (required)")
	cmd.Flags().StringVar(&pastureRepo, "pasture-repo", "", "Pasture working repository (default: current git root)")
	cmd.Flags().StringVar(&auraRepo, "aura-repo", "", "Aura repository containing the named marketplace revision (required; origin must identify dayvidpham/aura-plugins)")

	_ = cmd.MarkFlagRequired("pasture-revision")
	_ = cmd.MarkFlagRequired("aura-revision")
	_ = cmd.MarkFlagRequired("expected-old")
	_ = cmd.MarkFlagRequired("remote")
	_ = cmd.MarkFlagRequired("aura-repo")

	return cmd
}

// buildPromotionGates assembles the mandatory production gate set against
// detached immutable candidate checkouts.
func buildPromotionGates(
	pastureRepo effects.RepositoryID,
	projection promotion.Projection,
	marketplacePath string,
) ([]promotion.Gate, error) {
	marketGate, err := promotion.NewFuncGate("aura-marketplace-validation", func() error {
		return promotion.ValidateMarketplaceFile(marketplacePath, projection)
	})
	if err != nil {
		return nil, err
	}
	pkgTests, err := promotion.NewCommandGate(
		"pasture-package-tests", pastureRepo, "go",
		[]string{"test", "-race", "./..."}, exec.LookPath, effects.DefaultCommandRunner,
	)
	if err != nil {
		return nil, err
	}
	activationFixtures, err := promotion.NewCommandGate(
		"activation-fixtures", pastureRepo, "go",
		[]string{"test", "-race", "./internal/install/..."}, exec.LookPath, effects.DefaultCommandRunner,
	)
	if err != nil {
		return nil, err
	}
	return []promotion.Gate{marketGate, pkgTests, activationFixtures}, nil
}
