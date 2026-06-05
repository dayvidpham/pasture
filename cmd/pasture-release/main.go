// Command pasture-release manages versioning and release coordination across
// the Pasture polyrepo (Nix, GitHub Releases, go install, skill channels).
//
// Usage:
//
//	pasture-release patch|minor|major [flags]   — bump version and release
//	pasture-release --check                     — check version consistency
//	pasture-release registry <subcommand>       — manage plugin registry
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/internal/release"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/spf13/cobra"
)

// stdinIsTTY reports whether r is an interactive terminal. It is a package
// variable so tests can override it to exercise the interactive-prompt path
// with an injected (non-file) reader. The default inspects the underlying
// *os.File mode for a character device.
var stdinIsTTY = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// repoRoot resolves the git repository root from the current working directory.
func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf(
			"workflow error: cannot determine git repository root — %w — "+
				"ensure you are running pasture-release inside a git repository",
			err,
		)
	}
	root := filepath.Clean(string(out[:len(out)-1])) // trim trailing newline
	return root, nil
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// Cobra already prints the error; just exit non-zero.
		os.Exit(1)
	}
}

// ─── Root command ─────────────────────────────────────────────────────────────

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "pasture-release",
		Short: "Bump version, generate changelog, commit and tag a Pasture release",
		Long: `pasture-release automates semantic version bumps, changelog generation,
git commits, and tags across Pasture polyrepo plugins.

Examples:
  pasture-release patch                  # 0.1.0 → 0.1.1
  pasture-release minor                  # 0.1.0 → 0.2.0
  pasture-release major                  # 0.1.0 → 1.0.0
  pasture-release patch --dry-run        # preview without writing
  pasture-release patch --sync           # fix drift then bump
  pasture-release --check                # check version consistency
  pasture-release registry init          # create plugin registry
  pasture-release registry add <name>    # register a plugin`,
		SilenceUsage: true,
	}

	root.AddCommand(
		newBumpCmd("patch"),
		newBumpCmd("minor"),
		newBumpCmd("major"),
		newCheckCmd(),
		newRegistryCmd(),
	)
	return root
}

// ─── Bump commands (patch / minor / major) ────────────────────────────────────

func newBumpCmd(kind string) *cobra.Command {
	var (
		dryRun      bool
		sync        bool
		noChangelog bool
		noCommit    bool
		noTag       bool
		plugin      string
	)

	cmd := &cobra.Command{
		Use:   kind,
		Short: fmt.Sprintf("Bump the %s version component", kind),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse string to typed BumpKind at the CLI boundary (D13).
			bumpKind := types.BumpKind(kind)
			if !bumpKind.IsValid() {
				return fmt.Errorf(
					"validation error: unknown bump kind %q — "+
						"expected one of: major, minor, patch",
					kind,
				)
			}
			root, err := repoRoot()
			if err != nil {
				return err
			}
			return release.RunRelease(release.ReleaseOptions{
				BumpKind:    bumpKind,
				DryRun:      dryRun,
				Sync:        sync,
				NoChangelog: noChangelog,
				NoCommit:    noCommit,
				NoTag:       noTag,
				RepoRoot:    root,
				Plugin:      plugin,
			})
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would happen without making changes")
	cmd.Flags().BoolVar(&sync, "sync", false, "Sync version drift before bumping")
	cmd.Flags().BoolVar(&noChangelog, "no-changelog", false, "Skip changelog generation")
	cmd.Flags().BoolVar(&noCommit, "no-commit", false, "Skip git commit")
	cmd.Flags().BoolVar(&noTag, "no-tag", false, "Skip git tag")
	cmd.Flags().StringVar(&plugin, "plugin", "",
		"After commit/tag, sync this plugin's entry in its registered (cross-repo) "+
			"marketplace.json to the new version (leaves that marketplace's "+
			"metadata.version untouched)")
	return cmd
}

// ─── Check command ────────────────────────────────────────────────────────────

func newCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Check version consistency across all manifest files",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := repoRoot()
			if err != nil {
				return err
			}
			files, err := release.DiscoverVersionFiles(root)
			if err != nil {
				return err
			}
			if len(files) == 0 {
				return fmt.Errorf(
					"validation error: no version files found in %s — "+
						"pasture-release looks for: pyproject.toml, package.json, "+
						".claude-plugin/plugin.json, .claude-plugin/marketplace.json",
					root,
				)
			}

			versions := make(map[string]string, len(files))
			for _, vf := range files {
				v, err := vf.Read()
				if err != nil {
					return err
				}
				versions[vf.Name()] = v
			}

			consistent := true
			canonical := versions[files[0].Name()]
			for _, v := range versions {
				if v != canonical {
					consistent = false
					break
				}
			}

			fmt.Printf("Repository: %s\n", root)
			fmt.Printf("Version files (%d):\n", len(files))
			for _, vf := range files {
				marker := " "
				if !consistent && versions[vf.Name()] != canonical {
					marker = "*"
				}
				fmt.Printf("  %s %s: %s\n", marker, vf.Name(), versions[vf.Name()])
			}

			if consistent {
				fmt.Printf("\nAll files at %s\n", canonical)
				return nil
			}
			fmt.Println("\nDrift detected! Use --sync to align before bumping.")
			os.Exit(1)
			return nil
		},
	}
}

// ─── Registry command tree ────────────────────────────────────────────────────

func newRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Manage the plugin registry",
	}
	cmd.AddCommand(
		newRegistryInitCmd(),
		newRegistryAddCmd(),
		newRegistryListCmd(),
		newRegistryRemoveCmd(),
		newRegistryExecCmd(),
		newRegistrySyncVersionsCmd(),
		newRegistryReleaseOrderCmd(),
	)
	return cmd
}

func newRegistryInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create an empty plugin registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := release.DefaultRegistryPath()
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf(
					"config error: registry already exists at %s — "+
						"delete it manually or use 'registry add' to extend it",
					path,
				)
			}
			r := &release.PluginRegistry{Marketplaces: []release.MarketplaceEntry{}}
			if err := r.Save(path, false); err != nil {
				return err
			}
			fmt.Printf("Initialized empty registry at %s\n", path)
			return nil
		},
	}
}

func newRegistryAddCmd() *cobra.Command {
	var (
		pluginPath      string
		remote          string
		marketplacePath string
		yes             bool
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a plugin to the registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			regPath := release.DefaultRegistryPath()

			var r release.PluginRegistry
			if err := r.Load(regPath); err != nil {
				return err
			}

			absPath, err := filepath.Abs(pluginPath)
			if err != nil {
				return fmt.Errorf("validation error: invalid plugin path %q — %w", pluginPath, err)
			}

			// Auto-detect remote if not provided.
			if remote == "" {
				out, err := exec.Command("git", "remote", "get-url", "origin").Output()
				if err == nil {
					remote = filepath.Clean(string(out[:len(out)-1]))
				}
			}

			// Resolve marketplace path.
			absMp, err := filepath.Abs(marketplacePath)
			if err != nil {
				return fmt.Errorf("validation error: invalid marketplace path %q — %w", marketplacePath, err)
			}

			// Check for duplicate.
			existing, _ := r.FindPlugin(name, absPath)
			if existing != nil && !yes {
				fmt.Printf("Plugin %q already registered at %s\n", name, existing.Path)
				fmt.Print("Update? [y/N] ")
				var answer string
				fmt.Scan(&answer)
				if answer != "y" && answer != "yes" {
					fmt.Println("Aborted.")
					return nil
				}
			}

			// Remove old entry if it exists, then add fresh.
			if existing != nil {
				// Remove all plugins with this name.
				var newMPs []release.MarketplaceEntry
				for _, m := range r.Marketplaces {
					var newPlugins []release.PluginEntry
					for _, p := range m.Plugins {
						if p.Name != name {
							newPlugins = append(newPlugins, p)
						}
					}
					newMPs = append(newMPs, release.MarketplaceEntry{Path: m.Path, Plugins: newPlugins})
				}
				r.Marketplaces = newMPs
			}

			// Find or create the marketplace entry.
			found := false
			for i := range r.Marketplaces {
				if r.Marketplaces[i].Path == absMp {
					r.Marketplaces[i].Plugins = append(r.Marketplaces[i].Plugins, release.PluginEntry{
						Name: name, Path: absPath, Remote: remote,
					})
					found = true
					break
				}
			}
			if !found {
				r.Marketplaces = append(r.Marketplaces, release.MarketplaceEntry{
					Path:    absMp,
					Plugins: []release.PluginEntry{{Name: name, Path: absPath, Remote: remote}},
				})
			}

			if err := r.Save(regPath, false); err != nil {
				return err
			}
			fmt.Printf("Added %q to registry.\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&pluginPath, "path", ".", "Local path to the plugin repo (default: current directory)")
	cmd.Flags().StringVar(&remote, "remote", "", "Remote URL (auto-detected from git origin if omitted)")
	cmd.Flags().StringVar(&marketplacePath, "marketplace", "", "Path to marketplace.json (required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation on duplicate")
	_ = cmd.MarkFlagRequired("marketplace")
	return cmd
}

func newRegistryListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all registered plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			var r release.PluginRegistry
			if err := r.Load(release.DefaultRegistryPath()); err != nil {
				return err
			}
			for _, m := range r.Marketplaces {
				for _, p := range m.Plugins {
					fmt.Printf("%s → %s (%s)\n", m.Path, p.Name, p.Path)
				}
			}
			return nil
		},
	}
}

func newRegistryRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a plugin from the registry by name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			regPath := release.DefaultRegistryPath()
			var r release.PluginRegistry
			if err := r.Load(regPath); err != nil {
				return err
			}
			var newMPs []release.MarketplaceEntry
			for _, m := range r.Marketplaces {
				var newPlugins []release.PluginEntry
				for _, p := range m.Plugins {
					if p.Name != name {
						newPlugins = append(newPlugins, p)
					}
				}
				newMPs = append(newMPs, release.MarketplaceEntry{Path: m.Path, Plugins: newPlugins})
			}
			r.Marketplaces = newMPs
			if err := r.Save(regPath, false); err != nil {
				return err
			}
			fmt.Printf("Removed %q from registry.\n", name)
			return nil
		},
	}
}

func newRegistryExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec <cmd> [args...]",
		Short: "Run a command in each registered plugin directory",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var r release.PluginRegistry
			if err := r.Load(release.DefaultRegistryPath()); err != nil {
				return err
			}
			return r.Exec(args[0], args[1:]...)
		},
	}
}

// driftChangeCount returns the number of actionable pending changes in plan,
// excluding display-only DriftConsistent rows. The "N change(s) pending" footer
// and the apply/no-op gating count changes only — never the full roster.
func driftChangeCount(plan []release.VersionDrift) int {
	n := 0
	for _, d := range plan {
		if d.Action.IsChange() {
			n++
		}
	}
	return n
}

// printReconcilePreview renders the canonical, user-approved reconciliation
// table over the FULL roster — every registered plugin, including the ones that
// are already consistent. The middle comparison glyph ({>,==,<}) between
// plugin.json and the marketplace is retained; the leading ACTION arrow is
// directional — → to push a version into the marketplace, ← to pull a plugin
// repo; a consistent plugin prints `consistent` with no arrow (display only,
// never an action). The footer counts only actionable changes (DriftConsistent
// rows are excluded). When dryRun is true the footer notes that nothing was
// written or pulled.
func printReconcilePreview(w io.Writer, plan []release.VersionDrift, dryRun bool) {
	fmt.Fprintln(w, "Reconciling registered plugins (plugin.json  ⟷  marketplace entry):")
	for _, d := range plan {
		switch d.Action {
		case release.DriftWriteMarketplace:
			fmt.Fprintf(w,
				"   %s  plugin.json %s  >  marketplace %s   → UPDATE marketplace entry → %s\n",
				d.Plugin, d.PluginVersion, d.MarketplaceVersion, d.PluginVersion,
			)
		case release.DriftPullPlugin:
			fmt.Fprintf(w,
				"   %s  plugin.json %s  <  marketplace %s   ← GIT PULL plugin repo (local behind released %s)\n",
				d.Plugin, d.PluginVersion, d.MarketplaceVersion, d.MarketplaceVersion,
			)
		case release.DriftConsistent:
			fmt.Fprintf(w,
				"   %s  plugin.json %s  ==  marketplace %s   consistent\n",
				d.Plugin, d.PluginVersion, d.MarketplaceVersion,
			)
		case release.DriftWriteFile:
			fmt.Fprintf(w,
				"   %s  %s  %s  →  %s   (sync intra-plugin version file)\n",
				d.Plugin, d.File, d.Got, d.Want,
			)
		}
	}
	changes := driftChangeCount(plan)
	if dryRun {
		fmt.Fprintf(w,
			"%d change(s) pending  ·  dry-run: nothing written, no repos pulled\n",
			changes,
		)
	} else {
		fmt.Fprintf(w, "%d change(s) pending\n", changes)
	}
}

func newRegistrySyncVersionsCmd() *cobra.Command {
	var dryRun bool
	var nonInteractive bool
	var registryPath string
	cmd := &cobra.Command{
		Use:   "sync-versions",
		Short: "Reconcile plugin versions against the registry's marketplaces",
		Long: `Detect and apply version drift across registered plugins.

For each plugin this reconciles two things:
  • intra-plugin version files (pyproject.toml, package.json, plugin.json …)
  • the cross-repo marketplace entry, NEWEST-WINS: a newer plugin.json pushes
    its version into the marketplace entry (→); a newer marketplace pulls the
    plugin repo to catch up (←). The marketplace's own metadata.version is
    never touched.

By default, if any change is pending the full set is previewed and confirmed
with a single [y/N] prompt. Use --non-interactive to apply without prompting
(scripts/CI) or --dry-run to preview only.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if registryPath == "" {
				registryPath = release.DefaultRegistryPath()
			}
			var r release.PluginRegistry
			if err := r.Load(registryPath); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			r.WithOutput(out)

			// Plan: detect all pending changes with NO side effects. The plan
			// holds the FULL roster — actionable drift AND display-only
			// DriftConsistent rows — so the preview shows every registered plugin.
			plan, err := r.SyncVersions(true)
			if err != nil {
				return err
			}
			// Gate on the count of ACTIONABLE changes only; consistent rows are
			// display-only and never warrant a preview/prompt on their own.
			changes := driftChangeCount(plan)
			if changes == 0 {
				fmt.Fprintln(out, "All plugins are version-consistent.")
				return nil
			}

			// Render the FULL-roster preview: every registered plugin, drift rows
			// and display-only `consistent` rows together (Impl-UAT C1).
			printReconcilePreview(out, plan, dryRun)

			// --dry-run: preview only — no writes, no pulls, no prompt.
			if dryRun {
				return nil
			}

			// Interactive confirmation (unless --non-interactive).
			if !nonInteractive {
				if !stdinIsTTY(cmd.InOrStdin()) {
					return fmt.Errorf(
						"refusing to run `registry sync-versions` on a non-interactive " +
							"terminal (non-TTY), command needs user confirmation by default. " +
							"Re-run command with `--non-interactive` flag to run on non-TTY " +
							"with no confirmations, or run with `--dry-run` to preview changes.",
					)
				}
				fmt.Fprint(out, "Apply these change(s)? [y/N]: ")
				reader := bufio.NewReader(cmd.InOrStdin())
				line, _ := reader.ReadString('\n')
				switch strings.ToLower(strings.TrimSpace(line)) {
				case "y", "yes":
					// proceed
				default:
					fmt.Fprintln(out, "Aborted; no changes written, no repos pulled.")
					return nil
				}
			}

			// Apply.
			if _, err := r.SyncVersions(false); err != nil {
				return err
			}
			fmt.Fprintf(out, "Applied %d change(s).\n", changes)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview pending changes without writing or pulling")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Apply changes without prompting (scripts/CI)")
	cmd.Flags().StringVar(&registryPath, "registry", "", "Path to the plugin registry (default: user-global registry)")
	return cmd
}

func newRegistryReleaseOrderCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "release-order",
		Short: "Print plugins in topological release order (dependencies first)",
		RunE: func(cmd *cobra.Command, args []string) error {
			var r release.PluginRegistry
			if err := r.Load(release.DefaultRegistryPath()); err != nil {
				return err
			}
			order, err := r.ReleaseOrder()
			if err != nil {
				return err
			}
			for i, p := range order {
				fmt.Printf("%d. %s (%s)\n", i+1, p.Name, p.Path)
			}
			return nil
		},
	}
}
