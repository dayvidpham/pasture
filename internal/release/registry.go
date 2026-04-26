package release

import (
	"encoding/json"
	"fmt"
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

// VersionDrift describes a version mismatch detected in a plugin.
type VersionDrift struct {
	Plugin string
	File   string
	Want   string // canonical version
	Got    string // actual version in the file
}

// SyncVersions detects version drift across all plugins and files.
// The canonical version for each plugin is taken from the first discovered
// version file (usually pyproject.toml).
// When dryRun is false, drifted files are updated to the canonical version.
func (r *PluginRegistry) SyncVersions(dryRun bool) ([]VersionDrift, error) {
	var allDrift []VersionDrift
	var errs []string

	for _, m := range r.Marketplaces {
		for _, p := range m.Plugins {
			files, err := DiscoverVersionFiles(p.Path)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", p.Name, err))
				continue
			}
			if len(files) == 0 {
				continue
			}

			canonical, err := files[0].Read()
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s canonical read: %v", p.Name, err))
				continue
			}

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

	if len(errs) > 0 {
		return allDrift, fmt.Errorf(
			"workflow error: sync-versions encountered %d error(s) — %s",
			len(errs), strings.Join(errs, "; "),
		)
	}
	return allDrift, nil
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
