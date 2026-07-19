package main

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/promotion"
	"github.com/dayvidpham/pasture/internal/target/claudecode"
	"github.com/spf13/cobra"
)

// newPromoteStableCmd builds the `promote-pasture-stable` subcommand: the gated,
// guarded promotion of the moving pasture-stable release channel. It wires the
// production revision resolver and git-backed guarded pusher, projects the
// aggregate marketplace from the pinned target descriptors, and runs the ordered
// gate set before performing exactly one guarded ref update.
func newPromoteStableCmd() *cobra.Command {
	var (
		pastureRevision  string
		auraRevision     string
		expectedOldFlag  string
		remote           string
		pastureRepo      string
		auraRepo         string
		marketplacePath  string
		sourceRepo       string
		marketplaceName  string
		skipCommandGates bool
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
				auraRepo = pastureRepo
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

			// Project the aggregate marketplace from the pinned target descriptors
			// (no hand-maintained second catalog) and resolve the marketplace path
			// the Aura repository gate validates against.
			descriptor, err := claudecode.Descriptor()
			if err != nil {
				return err
			}
			projection, err := promotion.ProjectClaudeCode(descriptor, marketplaceName, sourceRepo, promotion.DefaultStableRef)
			if err != nil {
				return err
			}
			if marketplacePath == "" {
				marketplacePath = filepath.Join(absAuraRepo, ".claude-plugin", "marketplace.json")
			}

			gates, err := buildPromotionGates(
				pastureRepoID, auraRepoID,
				projection, marketplacePath,
				skipCommandGates,
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
	cmd.Flags().StringVar(&auraRepo, "aura-repo", "", "Aura working repository (default: --pasture-repo)")
	cmd.Flags().StringVar(&marketplacePath, "marketplace", "", "Path to the Aura marketplace.json the gate validates (default: <aura-repo>/.claude-plugin/marketplace.json)")
	cmd.Flags().StringVar(&sourceRepo, "source-repo", "dayvidpham/pasture", "GitHub owner/name the projected plugins are fetched from")
	cmd.Flags().StringVar(&marketplaceName, "marketplace-name", "aura-plugins", "Aggregate marketplace name")
	cmd.Flags().BoolVar(&skipCommandGates, "skip-command-gates", false, "Skip the subprocess test/fixture gates (keeps the in-process marketplace gate); use only when the caller runs those gates externally")

	_ = cmd.MarkFlagRequired("pasture-revision")
	_ = cmd.MarkFlagRequired("aura-revision")
	_ = cmd.MarkFlagRequired("expected-old")
	_ = cmd.MarkFlagRequired("remote")

	return cmd
}

// buildPromotionGates assembles the ordered production gate set: the in-process
// Aura marketplace/repository validation, then (unless skipped) the Pasture
// target package tests and the #39 activation fixtures, run at the caller's
// checked-out revisions.
func buildPromotionGates(
	pastureRepo, auraRepo effects.RepositoryID,
	projection promotion.Projection,
	marketplacePath string,
	skipCommandGates bool,
) ([]promotion.Gate, error) {
	marketGate, err := promotion.NewFuncGate("aura-marketplace-validation", func() error {
		return promotion.ValidateMarketplaceFile(marketplacePath, projection)
	})
	if err != nil {
		return nil, err
	}
	gates := []promotion.Gate{marketGate}

	if skipCommandGates {
		return gates, nil
	}

	pkgTests, err := promotion.NewCommandGate(
		"pasture-package-tests", pastureRepo, "go",
		[]string{"test", "./..."}, exec.LookPath, effects.DefaultCommandRunner,
	)
	if err != nil {
		return nil, err
	}
	activationFixtures, err := promotion.NewCommandGate(
		"activation-fixtures", pastureRepo, "go",
		[]string{"test", "./internal/install/..."}, exec.LookPath, effects.DefaultCommandRunner,
	)
	if err != nil {
		return nil, err
	}
	_ = auraRepo // reserved for a future Aura repository command gate
	return append(gates, pkgTests, activationFixtures), nil
}
