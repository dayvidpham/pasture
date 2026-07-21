package promotion

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/target/claudecode"
)

// pluginManifestPath is the fixed in-bundle path of a Claude Code plugin's
// manifest. Each published component bundle carries exactly this file.
const pluginManifestPath = ".claude-plugin/plugin.json"

// SourceKind is the closed set of marketplace plugin source kinds.
type SourceKind string

const (
	// SourceGitSubdir selects one plugin root within an exact repository commit.
	SourceGitSubdir SourceKind = "git-subdir"
)

// IsValid reports whether the source kind is a known kind.
func (k SourceKind) IsValid() bool { return k == SourceGitSubdir }

// PluginSource names where a marketplace plugin is fetched from. It mirrors the
// Aura .claude-plugin/marketplace.json plugin source shape exactly.
type PluginSource struct {
	Source SourceKind `json:"source"`
	URL    string     `json:"url"`
	Path   string     `json:"path"`
	SHA    string     `json:"sha"`
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

	// ComponentID is the stable activation identity this marketplace plugin
	// projects. Downstream activation and Home Manager bind to this selector. It is not part
	// of the Claude marketplace.json wire shape, so it is omitted from that
	// serialization but exposed on the typed projection.
	ComponentID string `json:"-"`
}

// Projection is the aggregate marketplace catalog projected from one or more
// target descriptors and pinned at an exact source commit.
// It is the release/distribution evidence consumed by Aura Home Manager and
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

const canonicalPastureURL = "https://github.com/dayvidpham/pasture.git"

var projectedComponents = [...]struct {
	name        string
	componentID string
	path        string
}{
	{"pasture-agents", claudecode.AgentsComponentID, "internal/target/claudecode/assets/pasture-agents"},
	{"pasture-hooks", claudecode.HooksComponentID, "internal/target/claudecode/assets/pasture-hooks"},
	{"pasture-skills", claudecode.SkillsComponentID, "internal/target/claudecode/assets/pasture-skills"},
}

// ProjectClaudeCodeTree projects manifests from an immutable Pasture checkout,
// and is the only projection authority used by validation and publication.
func ProjectClaudeCodeTree(root, marketplaceName, sourceCommit string) (Projection, error) {
	entries := make([]MarketplaceEntry, 0, len(projectedComponents))
	for _, component := range projectedComponents {
		manifestPath := filepath.Join(root, filepath.FromSlash(component.path), pluginManifestPath)
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return Projection{}, fault("candidate plugin manifest could not be read at "+manifestPath, "projection must be derived from the exact Pasture candidate tree", "promotion.ProjectClaudeCodeTree", "marketplace projection", "the candidate catalog cannot be proven", "ensure every generated split plugin manifest is readable", err)
		}
		var manifest pluginManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return Projection{}, fault("candidate plugin manifest is malformed at "+manifestPath, "plugin manifests are typed marketplace inputs", "promotion.ProjectClaudeCodeTree", "marketplace projection", "the candidate catalog cannot be proven", "fix plugin.json in the named Pasture commit", err)
		}
		if manifest.Name != component.name {
			return Projection{}, fault("candidate plugin manifest at "+manifestPath+" names "+manifest.Name+" instead of "+component.name, "each canonical source path must contain its matching plugin identity", "promotion.ProjectClaudeCodeTree", "marketplace projection", "the candidate catalog could map a plugin to the wrong bytes", "regenerate the split plugin assets with matching paths and manifests", nil)
		}
		if err := validateManifest(component.componentID, manifest, PastureRepository); err != nil {
			return Projection{}, err
		}
		entries = append(entries, MarketplaceEntry{Name: manifest.Name, Description: manifest.Description, Version: manifest.Version, ComponentID: component.componentID, Source: PluginSource{Source: SourceGitSubdir, URL: canonicalPastureURL, Path: component.path, SHA: sourceCommit}})
	}
	if strings.TrimSpace(marketplaceName) == "" || strings.TrimSpace(marketplaceName) != marketplaceName {
		return Projection{}, fault("marketplace name is empty or noncanonical", "the exact catalog identity is part of release evidence", "promotion.ProjectClaudeCodeTree", "marketplace projection", "the projected catalog cannot be matched unambiguously", "use the canonical marketplace name", nil)
	}
	if _, err := effects.NewCommitOID(sourceCommit); err != nil {
		return Projection{}, fault("projection source commit is not a full commit id", "marketplace sources must pin immutable plugin bytes", "promotion.ProjectClaudeCodeTree", "marketplace projection", "consumers could resolve moving or ambiguous content", "pass the exact Pasture candidate commit", err)
	}
	if err := validateEntries(entries); err != nil {
		return Projection{}, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name+"\x00"+entries[i].ComponentID < entries[j].Name+"\x00"+entries[j].ComponentID
	})
	return Projection{MarketplaceName: marketplaceName, SourceRef: sourceCommit, Entries: entries}, nil
}

func validateManifest(componentID string, manifest pluginManifest, repo string) error {
	if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Name) != manifest.Name || strings.TrimSpace(manifest.Version) == "" || strings.TrimSpace(manifest.Version) != manifest.Version || strings.TrimSpace(manifest.Description) == "" || strings.TrimSpace(manifest.Description) != manifest.Description {
		return fault("plugin manifest for component "+componentID+" has blank or noncanonical fields", "marketplace identity, version, and description must be exact non-padded values", "promotion.validateManifest", "marketplace projection", "the catalog would be ambiguous or noncanonical", "fix the component plugin.json fields", nil)
	}
	wantRepository := "https://github.com/" + repo
	if strings.TrimSuffix(manifest.Repository, ".git") != wantRepository {
		return fault("plugin manifest for component "+componentID+" names repository "+manifest.Repository, "manifest provenance must match the canonical projected Pasture repository", "promotion.validateManifest", "marketplace projection", "the catalog could advertise bytes from a different repository", "set plugin.json repository to "+wantRepository, nil)
	}
	return nil
}

func validateEntries(entries []MarketplaceEntry) error {
	names := make(map[string]struct{}, len(entries))
	components := make(map[string]struct{}, len(entries))
	paths := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if _, duplicate := names[entry.Name]; duplicate {
			return fault("projected marketplace contains duplicate plugin name "+entry.Name, "plugin names must identify exactly one selectable source", "promotion.validateEntries", "marketplace projection", "catalog resolution would depend on entry order", "give every generated component a unique plugin name", nil)
		}
		if _, duplicate := components[entry.ComponentID]; duplicate {
			return fault("projected marketplace contains duplicate component identity "+entry.ComponentID, "component selectors must map one-to-one to marketplace entries", "promotion.validateEntries", "marketplace projection", "activation could select ambiguous content", "give every generated component a unique component identity", nil)
		}
		if entry.Source.Source != SourceGitSubdir || entry.Source.URL != canonicalPastureURL {
			return fault("projected marketplace source for "+entry.Name+" is not canonical", "every split plugin must use the exact Pasture git-subdir endpoint", "promotion.validateEntries", "marketplace projection", "consumers could fetch unrelated bytes", "derive sources from the immutable Pasture candidate", nil)
		}
		if _, duplicate := paths[entry.Source.Path]; duplicate {
			return fault("projected marketplace contains duplicate source path "+entry.Source.Path, "each split plugin must resolve a pairwise-distinct plugin root", "promotion.validateEntries", "marketplace projection", "multiple plugin names could fetch the same bytes", "restore the exact component-to-path mapping", nil)
		}
		names[entry.Name] = struct{}{}
		components[entry.ComponentID] = struct{}{}
		paths[entry.Source.Path] = struct{}{}
	}
	return nil
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
