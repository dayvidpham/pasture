package promotion

import (
	"encoding/json"
	"io"
	"io/fs"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/target/claudecode"
)

// pluginManifestPath is the fixed in-bundle path of a Claude Code plugin's
// manifest. Each published component bundle carries exactly this file.
const pluginManifestPath = ".claude-plugin/plugin.json"

// SourceKind is the closed set of marketplace plugin source kinds. The aggregate
// Aura marketplace publishes GitHub-hosted plugins only in this wave.
type SourceKind string

const (
	// SourceGitHub is a GitHub-hosted plugin source.
	SourceGitHub SourceKind = "github"
)

// IsValid reports whether the source kind is a known kind.
func (k SourceKind) IsValid() bool { return k == SourceGitHub }

// PluginSource names where a marketplace plugin is fetched from. It mirrors the
// Aura .claude-plugin/marketplace.json plugin source shape exactly.
type PluginSource struct {
	Source SourceKind `json:"source"`
	Repo   string     `json:"repo"`
}

// MarketplaceEntry is one projected plugin in the aggregate Aura marketplace. It
// is derived entirely from a target descriptor's published component identity
// and its bundle manifest — never hand-maintained — so the catalog cannot drift
// from the target-owned component IDs and versions.
type MarketplaceEntry struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Source      PluginSource `json:"source"`
	Version     string       `json:"version"`

	// ComponentID is the stable activation identity (for example
	// "claude-code/skills") this marketplace plugin projects. It is the selector
	// downstream activation (#39) and Home Manager (#8) bind to. It is not part
	// of the Claude marketplace.json wire shape, so it is omitted from that
	// serialization but exposed on the typed projection.
	ComponentID string `json:"-"`
}

// Projection is the aggregate marketplace catalog projected from one or more
// target descriptors, pinned at a source ref (the promoted pasture-stable ref).
// It is the release/distribution evidence consumed by Aura Home Manager (#8) and
// release validation: name, source, ref, selectors, and resolved versions.
type Projection struct {
	MarketplaceName string
	SourceRef       string
	Entries         []MarketplaceEntry
}

// pluginManifest is the subset of a Claude Code plugin.json the projection reads.
type pluginManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Repository  string `json:"repository"`
}

// ProjectClaudeCode projects a Claude Code target descriptor into aggregate
// marketplace entries — one independent, selectable plugin per component
// (skills, agents, hooks) — reading each plugin's name/version/description from
// its own bundle manifest. marketplaceName names the aggregate catalog; repo is
// the GitHub "owner/name" the plugins are fetched from; sourceRef is the pinned
// ref the catalog resolves against (the promoted pasture-stable ref).
func ProjectClaudeCode(
	descriptor claudecode.TargetDescriptor,
	marketplaceName string,
	repo string,
	sourceRef string,
) (Projection, error) {
	if !descriptor.IsValid() {
		return Projection{}, fault(
			"claude code target descriptor is zero or invalid",
			"the marketplace catalog is projected from a validly constructed target descriptor",
			"promotion.ProjectClaudeCode", "marketplace projection",
			"no component identities or versions are available to project",
			"build the descriptor with claudecode.Descriptor()", nil,
		)
	}
	if strings.TrimSpace(marketplaceName) == "" {
		return Projection{}, fault(
			"marketplace name is empty",
			"the aggregate catalog must be named so it can be published and validated",
			"promotion.ProjectClaudeCode", "marketplace projection",
			"the catalog cannot be identified",
			"pass a non-empty marketplace name, such as aura-plugins", nil,
		)
	}
	if strings.TrimSpace(repo) == "" {
		return Projection{}, fault(
			"plugin source repo is empty",
			"every projected plugin resolves to a GitHub owner/name source",
			"promotion.ProjectClaudeCode", "marketplace projection",
			"the catalog cannot state where its plugins are fetched from",
			"pass the source repo as owner/name, such as dayvidpham/pasture", nil,
		)
	}
	if strings.TrimSpace(sourceRef) == "" {
		return Projection{}, fault(
			"source ref is empty",
			"the catalog is pinned at the ref it resolves against so consumers observe a reviewed revision",
			"promotion.ProjectClaudeCode", "marketplace projection",
			"the published catalog would have no resolvable ref",
			"pass the pinned ref, such as "+DefaultStableRef, nil,
		)
	}

	var entries []MarketplaceEntry
	for _, component := range descriptor.Components() {
		manifest, err := readPluginManifest(component.Bundle().Open)
		if err != nil {
			return Projection{}, fault(
				"could not read plugin manifest for component "+component.ID().String(),
				"each published component bundle must carry its "+pluginManifestPath,
				"promotion.ProjectClaudeCode", "marketplace projection",
				"the projected entry would be missing its name or version",
				"regenerate the target descriptor so every component bundle embeds a valid plugin.json", err,
			)
		}
		if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Version) == "" {
			return Projection{}, fault(
				"plugin manifest for component "+component.ID().String()+" is missing a name or version",
				"a marketplace entry needs the exact plugin name and version the target published",
				"promotion.ProjectClaudeCode", "marketplace projection",
				"the catalog would advertise an unnamed or unversioned plugin",
				"regenerate the component so its plugin.json declares name and version", nil,
			)
		}
		entries = append(entries, MarketplaceEntry{
			Name:        manifest.Name,
			Description: manifest.Description,
			Source:      PluginSource{Source: SourceGitHub, Repo: repo},
			Version:     manifest.Version,
			ComponentID: component.ID().String(),
		})
	}

	// Canonical order: sort entries by plugin name so the projection is
	// deterministic regardless of component iteration order.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	return Projection{
		MarketplaceName: marketplaceName,
		SourceRef:       sourceRef,
		Entries:         entries,
	}, nil
}

// Selectors returns the projected component identities (the activation selectors)
// in the catalog's canonical order.
func (p Projection) Selectors() []string {
	out := make([]string, 0, len(p.Entries))
	for _, e := range p.Entries {
		out = append(out, e.ComponentID)
	}
	return out
}

// FindEntry returns the projected entry for a plugin name and true when present.
func (p Projection) FindEntry(name string) (MarketplaceEntry, bool) {
	for _, e := range p.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return MarketplaceEntry{}, false
}

// readPluginManifest opens and decodes a component bundle's plugin.json using
// the bundle's Open function.
func readPluginManifest(open func(string) (fs.File, error)) (pluginManifest, error) {
	f, err := open(pluginManifestPath)
	if err != nil {
		return pluginManifest{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return pluginManifest{}, err
	}
	var manifest pluginManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return pluginManifest{}, err
	}
	return manifest, nil
}
