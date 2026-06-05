package release

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ─── Types ───────────────────────────────────────────────────────────────────

// PluginEntry describes a single registered plugin.
type PluginEntry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Remote string `json:"remote"`
}

// MarketplaceEntry groups plugins that share a marketplace.json.
type MarketplaceEntry struct {
	Path    string        `json:"path"`
	Plugins []PluginEntry `json:"plugins"`
}

// PluginRegistry is the top-level registry structure stored on disk.
type PluginRegistry struct {
	Marketplaces []MarketplaceEntry `json:"marketplaces"`

	// gitPull pulls the plugin repo at dir (default: git -C <dir> pull
	// --ff-only). Unexported so it is never serialized to the on-disk registry;
	// injected per-instance via WithGitPull so black-box tests can stub the
	// network call without a package-global. A nil value is lazily defaulted to
	// defaultGitPull inside SyncVersions, so registries built with a struct
	// literal (e.g. the CLI) work without a constructor.
	gitPull func(dir string) error

	// out receives non-error progress output emitted by SyncVersions (e.g. the
	// "still behind after pull" warning). Unexported (never serialized); a nil
	// value is lazily defaulted to os.Stdout inside SyncVersions. The CLI wires
	// this to cmd.OutOrStdout(); tests inject a buffer to assert the warning.
	out io.Writer
}

// WithGitPull injects the function SyncVersions uses to pull a plugin repo when
// the marketplace is ahead of the local checkout (PULL_PLUGIN). It mutates the
// receiver and returns it for chaining, so it composes with struct-literal
// construction:
//
//	var r release.PluginRegistry
//	r.WithGitPull(func(dir string) error { pulled = dir; return nil })
//
// Passing nil restores the lazy default (defaultGitPull).
func (r *PluginRegistry) WithGitPull(fn func(dir string) error) *PluginRegistry {
	r.gitPull = fn
	return r
}

// WithOutput sets the writer SyncVersions uses for non-error progress/warning
// output. It mutates the receiver and returns it for chaining. Passing nil
// restores the lazy default (os.Stdout).
func (r *PluginRegistry) WithOutput(w io.Writer) *PluginRegistry {
	r.out = w
	return r
}

// defaultGitPull fast-forward-pulls the git repository at dir. It is the
// production implementation injected lazily when PluginRegistry.gitPull is nil.
func defaultGitPull(dir string) error {
	_, err := gitRun(dir, "pull", "--ff-only")
	return err
}

// DefaultRegistryPath returns the user-global plugin registry path.
func DefaultRegistryPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "config", "aura", "plugins", "claude-plugin-registry.json")
}

// ─── Load / Save ─────────────────────────────────────────────────────────────

// Load reads the registry from path.
// Returns an empty registry (not nil) when the file does not exist yet.
func (r *PluginRegistry) Load(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		r.Marketplaces = []MarketplaceEntry{}
		return nil
	}
	if err != nil {
		return fmt.Errorf(
			"config error: cannot read registry at %s — %w — "+
				"check file permissions or run 'pasture-release registry init'",
			path, err,
		)
	}
	if err := json.Unmarshal(data, r); err != nil {
		return fmt.Errorf(
			"config error: registry at %s is malformed JSON — %w — "+
				"fix JSON syntax or delete the file and run 'pasture-release registry init'",
			path, err,
		)
	}
	return nil
}

// Save writes the registry to path.
// When dryRun is true, the JSON is printed but not written.
func (r *PluginRegistry) Save(path string, dryRun bool) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("config error: cannot marshal registry — %w", err)
	}
	data = append(data, '\n')
	if dryRun {
		fmt.Printf("[dry-run] would write registry to %s:\n%s\n", path, data)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf(
			"config error: cannot create registry directory %s — %w — "+
				"check directory permissions",
			filepath.Dir(path), err,
		)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf(
			"config error: cannot write registry to %s — %w — "+
				"check file permissions",
			path, err,
		)
	}
	return nil
}

// ─── Lookup ──────────────────────────────────────────────────────────────────

// FindPlugin finds a plugin by name or (when name is empty) by matching the
// resolved absolute path against cwd. Returns nil, nil when not found.
func (r *PluginRegistry) FindPlugin(name, cwd string) (*PluginEntry, *MarketplaceEntry) {
	resolvedCwd, _ := filepath.Abs(cwd)
	for i := range r.Marketplaces {
		m := &r.Marketplaces[i]
		for j := range m.Plugins {
			p := &m.Plugins[j]
			if name != "" {
				if p.Name == name {
					return p, m
				}
			} else {
				absPath, _ := filepath.Abs(p.Path)
				if absPath == resolvedCwd {
					return p, m
				}
			}
		}
	}
	return nil, nil
}

// ─── Exec ────────────────────────────────────────────────────────────────────

// Exec runs cmd with args in each registered plugin directory.
// It prints each working directory before running and collects all errors.
func (r *PluginRegistry) Exec(cmd string, args ...string) error {
	var errs []string
	for _, m := range r.Marketplaces {
		for _, p := range m.Plugins {
			fmt.Printf("==> %s (%s)\n", p.Name, p.Path)
			c := exec.Command(cmd, args...) //nolint:gosec // cmd is user-provided intentionally
			c.Dir = p.Path
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", p.Name, err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf(
			"workflow error: exec failed in %d plugin(s) — %s — "+
				"check the command output above for details",
			len(errs), strings.Join(errs, "; "),
		)
	}
	return nil
}

// ─── SyncVersions ────────────────────────────────────────────────────────────

// DriftAction is the strongly-typed action a single VersionDrift entry
// represents. The CLI preview switches on Action (not on a Got/Want comparison)
// to render the correct line, per the canonical sync-versions output format.
type DriftAction int

const (
	// DriftWriteFile is an intra-plugin version-file fix: a non-canonical
	// version file in the plugin is rewritten to the plugin's canonical version.
	DriftWriteFile DriftAction = iota
	// DriftWriteMarketplace pushes the plugin's plugin.json version into the
	// cross-repo marketplace entry (plugin newer than marketplace; pv > mv).
	DriftWriteMarketplace
	// DriftPullPlugin git-pulls the plugin repo because the marketplace
	// advertises a newer released version than the local checkout (mv > pv).
	DriftPullPlugin
	// DriftConsistent is a display-only, no-op entry: the plugin's plugin.json
	// version already matches its marketplace entry (pv == mv). It is emitted so
	// the reconciliation preview can render the FULL roster (every registered
	// plugin, not just drifted ones), but it is NEVER an action — it is excluded
	// from the pending-change count and is never written or pulled.
	DriftConsistent
)

// IsChange reports whether a VersionDrift represents an actionable pending
// change (a marketplace write, a plugin pull, or an intra-plugin file fix) as
// opposed to a display-only DriftConsistent row. The CLI counts only changes
// for the "N change(s) pending" footer and the apply/no-op gating.
func (a DriftAction) IsChange() bool {
	return a != DriftConsistent
}

// VersionDrift describes a single pending change detected by SyncVersions.
//
// For DriftWriteFile (intra-plugin), File is the drifted version file, Got its
// current version, and Want the canonical version it will be rewritten to.
//
// For DriftWriteMarketplace / DriftPullPlugin (cross-repo marketplace
// reconciliation), PluginVersion (pv, from .claude-plugin/plugin.json) and
// MarketplaceVersion (mv, from the marketplace entry) carry the two compared
// versions, File names the artefact that changes (the marketplace.json for a
// write, the plugin repo dir for a pull), and Want/Got mirror the winning and
// losing version for uniform reporting.
type VersionDrift struct {
	Plugin string
	File   string
	Want   string // target/winning version
	Got    string // current/losing version
	Action DriftAction

	// PluginVersion (pv) and MarketplaceVersion (mv) are populated only for the
	// cross-repo marketplace reconciliation actions.
	PluginVersion      string
	MarketplaceVersion string
}

// SyncVersions detects every pending version change across all registered
// plugins and, when dryRun is false, applies them.
//
// It performs two independent reconciliations per plugin:
//
//  1. INTRA-PLUGIN file drift — the canonical version (first discovered file,
//     usually pyproject.toml) is propagated to any sibling version file that
//     disagrees (DriftWriteFile).
//
//  2. CROSS-REPO marketplace reconciliation (NEWEST-WINS) — the plugin's own
//     .claude-plugin/plugin.json version (pv, selected EXPLICITLY) is compared
//     against the version the registered marketplace advertises for it (mv).
//     pv > mv writes the marketplace entry (DriftWriteMarketplace); mv > pv
//     fast-forward-pulls the plugin repo (DriftPullPlugin); pv == mv is a no-op.
//     The marketplace's own metadata.version is NEVER touched.
//
// When dryRun is true, nothing is written or pulled — the returned slice is the
// plan of pending changes. Per-plugin errors (e.g. a malformed manifest or a
// non-fast-forwardable pull) are aggregated: the offending plugin is skipped
// and the others continue; the aggregated error is returned alongside the
// drift that was still detected.
func (r *PluginRegistry) SyncVersions(dryRun bool) ([]VersionDrift, error) {
	gitPull := r.gitPull
	if gitPull == nil {
		gitPull = defaultGitPull
	}
	out := r.out
	if out == nil {
		out = os.Stdout
	}

	var allDrift []VersionDrift
	var errs []string

	for _, m := range r.Marketplaces {
		for _, p := range m.Plugins {
			// ── (1) intra-plugin version-file drift ──
			files, err := DiscoverVersionFiles(p.Path)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", p.Name, err))
			} else if len(files) > 0 {
				canonical, err := files[0].Read()
				if err != nil {
					errs = append(errs, fmt.Sprintf("%s canonical read: %v", p.Name, err))
				} else {
					for _, vf := range files[1:] {
						got, err := vf.Read()
						if err != nil {
							errs = append(errs, fmt.Sprintf("%s %s read: %v", p.Name, vf.Name(), err))
							continue
						}
						if got != canonical {
							allDrift = append(allDrift, VersionDrift{
								Plugin: p.Name,
								File:   vf.Name(),
								Want:   canonical,
								Got:    got,
								Action: DriftWriteFile,
							})
							if !dryRun {
								if wErr := vf.Write(canonical, false); wErr != nil {
									errs = append(errs, fmt.Sprintf(
										"%s %s write: %v", p.Name, vf.Name(), wErr,
									))
								}
							}
						}
					}
				}
			}

			// ── (2) cross-repo marketplace reconciliation (newest-wins) ──
			drift, mErr := reconcileMarketplace(m, p, dryRun, gitPull, out)
			if drift != nil {
				allDrift = append(allDrift, *drift)
			}
			if mErr != nil {
				errs = append(errs, mErr.Error())
			}
		}
	}

	if len(errs) > 0 {
		return allDrift, fmt.Errorf(
			"workflow error: sync-versions encountered %d error(s) — %s",
			len(errs), strings.Join(errs, "; "),
		)
	}
	return allDrift, nil
}

// reconcileMarketplace performs the newest-wins comparison between a plugin's
// own .claude-plugin/plugin.json version (pv) and the version its registered
// marketplace advertises (mv).
//
// It returns (nil, nil) when there is nothing to do — the plugin has no
// plugin.json, the marketplace file does not exist, or pv == mv. It returns a
// non-nil VersionDrift describing the pending/applied change otherwise, and an
// actionable error (alongside the drift) when applying the change fails.
func reconcileMarketplace(
	m MarketplaceEntry,
	p PluginEntry,
	dryRun bool,
	gitPull func(dir string) error,
	out io.Writer,
) (*VersionDrift, error) {
	pluginJSONPath := filepath.Join(p.Path, ".claude-plugin", "plugin.json")
	if _, err := os.Stat(pluginJSONPath); err != nil {
		// No plugin.json → this plugin has no cross-repo marketplace identity to
		// reconcile; the intra-plugin pass already covered its files.
		return nil, nil
	}
	pv, err := NewJsonVersionFile(".claude-plugin/plugin.json", pluginJSONPath).Read()
	if err != nil {
		return nil, fmt.Errorf("%s plugin.json read: %w", p.Name, err)
	}

	if _, err := os.Stat(m.Path); err != nil {
		// Marketplace file missing → cannot reconcile against it. Surfaced as a
		// skip (not an error) so a registry pointing at a not-yet-created
		// marketplace does not fail the whole run.
		return nil, nil
	}
	mv, err := ReadPluginVersion(m.Path, p.Name)
	if err != nil {
		return nil, fmt.Errorf("%s marketplace read: %w", p.Name, err)
	}

	cmp, err := CompareVersions(pv, mv)
	if err != nil {
		return nil, fmt.Errorf("%s version compare: %w", p.Name, err)
	}

	switch {
	case cmp > 0: // pv > mv → push plugin version into marketplace entry
		drift := &VersionDrift{
			Plugin:             p.Name,
			File:               m.Path,
			Want:               pv,
			Got:                mv,
			Action:             DriftWriteMarketplace,
			PluginVersion:      pv,
			MarketplaceVersion: mv,
		}
		if !dryRun {
			if wErr := WritePluginVersion(m.Path, p.Name, pv, false); wErr != nil {
				return drift, fmt.Errorf("%s marketplace write: %w", p.Name, wErr)
			}
		}
		return drift, nil

	case cmp < 0: // mv > pv → pull the plugin repo to catch up to the release
		drift := &VersionDrift{
			Plugin:             p.Name,
			File:               p.Path,
			Want:               mv,
			Got:                pv,
			Action:             DriftPullPlugin,
			PluginVersion:      pv,
			MarketplaceVersion: mv,
		}
		if dryRun {
			return drift, nil
		}
		absRepo, aerr := filepath.Abs(p.Path)
		if aerr != nil {
			absRepo = p.Path
		}
		if pErr := gitPull(p.Path); pErr != nil {
			return drift, fmt.Errorf(
				"workflow error: cannot fast-forward plugin %s at %s — local has "+
					"uncommitted or divergent changes blocking 'git pull --ff-only' "+
					"(%w) — resolve or commit them (inspect with 'git -C %s status') "+
					"then re-run sync-versions, or release the plugin to update the "+
					"marketplace instead",
				p.Name, absRepo, pErr, absRepo,
			)
		}
		// `git pull --ff-only` already fetched, so a re-read reflects fresh
		// remote state. If still behind, the release tag likely isn't pushed yet.
		if newPV, rErr := NewJsonVersionFile(".claude-plugin/plugin.json", pluginJSONPath).Read(); rErr == nil {
			if c, cErr := CompareVersions(newPV, mv); cErr == nil && c < 0 {
				fmt.Fprintf(out,
					"warning: plugin %s at %s is still behind the marketplace "+
						"(local %s < released %s) after 'git pull --ff-only' — the "+
						"plugin repo may not have pushed the release commit/tag yet; "+
						"re-run sync-versions once it is published\n",
					p.Name, absRepo, newPV, mv,
				)
			}
		}
		return drift, nil

	default: // pv == mv → consistent: emit a display-only no-op row (never applied)
		return &VersionDrift{
			Plugin:             p.Name,
			File:               m.Path,
			Want:               pv,
			Got:                mv,
			Action:             DriftConsistent,
			PluginVersion:      pv,
			MarketplaceVersion: mv,
		}, nil
	}
}

// ─── ReleaseOrder ─────────────────────────────────────────────────────────────

// ReleaseOrder returns the plugins in topological dependency order (leaves first).
// Currently there is no explicit dependency graph in the registry, so all
// plugins are returned in their declaration order. Cycle detection is included
// for future use when dependency edges are added.
//
// Each plugin entry is returned once; the result is safe to release in order.
func (r *PluginRegistry) ReleaseOrder() ([]PluginEntry, error) {
	// Collect all plugins preserving declaration order (no edges yet).
	seen := make(map[string]bool)
	var order []PluginEntry
	for _, m := range r.Marketplaces {
		for _, p := range m.Plugins {
			if seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			order = append(order, p)
		}
	}
	return order, nil
}

// TopologicalSort performs a Kahn's-algorithm topological sort over a generic
// dependency graph. Returns an error if a cycle is detected.
//
// nodes is the set of node IDs; edges maps each node to its dependencies
// (things that must be released before it).
func TopologicalSort(nodes []string, edges map[string][]string) ([]string, error) {
	// Build in-degree map and adjacency list.
	inDegree := make(map[string]int, len(nodes))
	adj := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		inDegree[n] = 0
	}
	for _, n := range nodes {
		for _, dep := range edges[n] {
			adj[dep] = append(adj[dep], n)
			inDegree[n]++
		}
	}

	// Enqueue all nodes with zero in-degree.
	var queue []string
	for _, n := range nodes {
		if inDegree[n] == 0 {
			queue = append(queue, n)
		}
	}

	var result []string
	for len(queue) > 0 {
		// Pop front.
		cur := queue[0]
		queue = queue[1:]
		result = append(result, cur)
		for _, next := range adj[cur] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(result) != len(nodes) {
		return nil, fmt.Errorf(
			"workflow error: cycle detected in dependency graph — "+
				"processed %d of %d nodes — "+
				"remove the circular dependency between plugins",
			len(result), len(nodes),
		)
	}
	return result, nil
}
