package promotion

import (
	"encoding/json"
	"io"
	"io/fs"
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

var componentIDByPluginName = map[string]string{
	"pasture-skills": claudecode.SkillsComponentID,
	"pasture-agents": claudecode.AgentsComponentID,
	"pasture-hooks":  claudecode.HooksComponentID,
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
	if repo != PastureRepository {
		return Projection{}, fault("plugin source repo is not the canonical Pasture repository", "projection provenance is derived rather than caller-selectable", "promotion.ProjectClaudeCode", "marketplace projection", "the catalog could point at unrelated source bytes", "use "+PastureRepository, nil)
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
	if _, err := effects.NewCommitOID(sourceRef); err != nil {
		return Projection{}, fault("plugin source revision is not a full commit id", "marketplace entries pin exact immutable plugin bytes", "promotion.ProjectClaudeCode", "marketplace projection", "consumers could resolve a moving or ambiguous source", "pass the exact Pasture candidate commit", err)
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
		if err := validateManifest(component.ID().String(), manifest, repo); err != nil {
			return Projection{}, err
		}
		if componentIDByPluginName[manifest.Name] != component.ID().String() {
			return Projection{}, fault(
				"component "+component.ID().String()+" does not match plugin identity "+manifest.Name,
				"target component and manifest identities must map one-to-one",
				"promotion.ProjectClaudeCode", "marketplace projection",
				"the catalog could select the wrong generated plugin root",
				"regenerate the target descriptor and split plugin manifests together", nil,
			)
		}
		path := filepath.ToSlash(filepath.Join("internal", "target", "claudecode", "assets", manifest.Name))
		entries = append(entries, MarketplaceEntry{
			Name:        manifest.Name,
			Description: manifest.Description,
			Source:      PluginSource{Source: SourceGitSubdir, URL: canonicalPastureURL, Path: path, SHA: sourceRef},
			Version:     manifest.Version,
			ComponentID: component.ID().String(),
		})
	}

	if err := validateEntries(entries); err != nil {
		return Projection{}, err
	}
	// Use a total key so input permutations always produce the same projection.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name+"\x00"+entries[i].ComponentID < entries[j].Name+"\x00"+entries[j].ComponentID
	})

	return Projection{
		MarketplaceName: marketplaceName,
		SourceRef:       sourceRef,
		Entries:         entries,
	}, nil
}

// ProjectClaudeCodeTree projects manifests from an immutable Pasture checkout,
// rather than from the binary's own embedded descriptor.
func ProjectClaudeCodeTree(root, marketplaceName, sourceCommit string) (Projection, error) {
	pattern := filepath.Join(root, "internal", "target", "claudecode", "assets", "*", pluginManifestPath)
	manifestPaths, err := filepath.Glob(pattern)
	if err != nil {
		return Projection{}, fault("candidate plugin manifest inventory could not be enumerated", "projection discovers generated plugin roots in the exact Pasture candidate tree", "promotion.ProjectClaudeCodeTree", "marketplace projection", "the candidate catalog cannot be proven", "repair the generated Claude Code asset layout", err)
	}
	entries := make([]MarketplaceEntry, 0, len(componentIDByPluginName))
	for _, manifestPath := range manifestPaths {
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return Projection{}, fault("candidate plugin manifest could not be read at "+manifestPath, "projection must be derived from the exact Pasture candidate tree", "promotion.ProjectClaudeCodeTree", "marketplace projection", "the candidate catalog cannot be proven", "ensure every generated split plugin manifest is readable", err)
		}
		var manifest pluginManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return Projection{}, fault("candidate plugin manifest is malformed at "+manifestPath, "plugin manifests are typed marketplace inputs", "promotion.ProjectClaudeCodeTree", "marketplace projection", "the candidate catalog cannot be proven", "fix plugin.json in the named Pasture commit", err)
		}
		componentID, projected := componentIDByPluginName[manifest.Name]
		if !projected {
			continue
		}
		if err := validateManifest(componentID, manifest, PastureRepository); err != nil {
			return Projection{}, err
		}
		sourcePath, err := filepath.Rel(root, filepath.Dir(filepath.Dir(manifestPath)))
		if err != nil {
			return Projection{}, fault("candidate plugin source path could not be made relative for "+manifest.Name, "git-subdir sources are rooted in the exact candidate checkout", "promotion.ProjectClaudeCodeTree", "marketplace projection", "the source path cannot be published canonically", "repair the candidate checkout layout", err)
		}
		sourcePath = filepath.ToSlash(sourcePath)
		entries = append(entries, MarketplaceEntry{Name: manifest.Name, Description: manifest.Description, Version: manifest.Version, ComponentID: componentID, Source: PluginSource{Source: SourceGitSubdir, URL: canonicalPastureURL, Path: sourcePath, SHA: sourceCommit}})
	}
	if len(entries) != len(componentIDByPluginName) {
		return Projection{}, fault("candidate split plugin inventory is incomplete", "the exact candidate must contain one generated manifest for skills, agents, and hooks", "promotion.ProjectClaudeCodeTree", "marketplace projection", "the aggregate marketplace would omit a selectable component", "regenerate all Claude Code split plugin assets in the named commit", nil)
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
	for _, entry := range entries {
		if _, duplicate := names[entry.Name]; duplicate {
			return fault("projected marketplace contains duplicate plugin name "+entry.Name, "plugin names must identify exactly one selectable source", "promotion.validateEntries", "marketplace projection", "catalog resolution would depend on entry order", "give every generated component a unique plugin name", nil)
		}
		if _, duplicate := components[entry.ComponentID]; duplicate {
			return fault("projected marketplace contains duplicate component identity "+entry.ComponentID, "component selectors must map one-to-one to marketplace entries", "promotion.validateEntries", "marketplace projection", "activation could select ambiguous content", "give every generated component a unique component identity", nil)
		}
		names[entry.Name] = struct{}{}
		components[entry.ComponentID] = struct{}{}
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
