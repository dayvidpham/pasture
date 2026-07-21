package promotion

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/internal/effects"
)

type marketplaceFile struct {
	Name    string                   `json:"name"`
	Plugins []marketplacePluginEntry `json:"plugins"`
}

type marketplacePluginEntry struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Source      marketplacePluginSource `json:"source"`
	Version     string                  `json:"version"`
}

// marketplacePluginSource models the supported source union at the local schema
// boundary. Fields from another variant are rejected rather than ignored.
type marketplacePluginSource struct {
	Source string `json:"source"`
	Repo   string `json:"repo"`
	URL    string `json:"url"`
	Path   string `json:"path"`
	Ref    string `json:"ref"`
	SHA    string `json:"sha"`
}

func (s *marketplacePluginSource) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for field := range fields {
		switch field {
		case "source", "repo", "url", "path", "ref", "sha":
		default:
			return fmt.Errorf("unknown marketplace source field %q", field)
		}
	}
	type sourceAlias marketplacePluginSource
	var decoded sourceAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*s = marketplacePluginSource(decoded)
	return nil
}

// ValidateMarketplaceFile reads and validates one marketplace file.
func ValidateMarketplaceFile(path string, projection Projection) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fault("aura marketplace file could not be read at "+path, "the marketplace gate reads the file from the immutable Aura candidate checkout", "promotion.ValidateMarketplaceFile", "aura marketplace gate", "the promotion cannot confirm the candidate catalog", "ensure the named Aura commit contains .claude-plugin/marketplace.json", err)
	}
	return ValidateMarketplace(data, path, projection)
}

// ValidateMarketplace validates catalog identity, entry uniqueness, supported
// source-union boundaries, and every projected split plugin's exact source.
func ValidateMarketplace(data []byte, location string, projection Projection) error {
	if strings.TrimSpace(projection.MarketplaceName) == "" || strings.TrimSpace(projection.MarketplaceName) != projection.MarketplaceName {
		return fault("marketplace projection has a blank or padded catalog identity", "validation requires a canonical expected catalog", "promotion.ValidateMarketplace", "projection validation", "the Aura candidate could be compared with ambiguous evidence", "construct the projection from the exact Pasture candidate", nil)
	}
	if err := validateEntries(projection.Entries); err != nil {
		return err
	}
	var file marketplaceFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fault("aura marketplace file at "+location+" is not valid JSON", "the marketplace catalog must parse against the local typed schema boundary", "promotion.ValidateMarketplace", "aura marketplace gate", "the promotion cannot validate a malformed catalog", "fix the JSON syntax in the named Aura commit", err)
	}
	if file.Name != projection.MarketplaceName || strings.TrimSpace(file.Name) != file.Name {
		return fault("aura marketplace name "+file.Name+" does not match "+projection.MarketplaceName, "promotion evidence is bound to one exact canonical catalog identity", "promotion.ValidateMarketplace", "aura marketplace gate", "a different or noncanonical catalog could be certified", "set the top-level name to "+projection.MarketplaceName, nil)
	}
	byName := make(map[string]marketplacePluginEntry, len(file.Plugins))
	for _, plugin := range file.Plugins {
		if strings.TrimSpace(plugin.Name) == "" || strings.TrimSpace(plugin.Name) != plugin.Name || strings.TrimSpace(plugin.Version) == "" || strings.TrimSpace(plugin.Version) != plugin.Version {
			return fault("aura marketplace contains a blank or padded plugin identity/version", "catalog entries require canonical exact names and versions", "promotion.ValidateMarketplace", "aura marketplace gate", "plugin selection would be ambiguous or noncanonical", "remove surrounding whitespace and fill every required field", nil)
		}
		if _, exists := byName[plugin.Name]; exists {
			return fault("aura marketplace contains duplicate plugin name "+plugin.Name, "each plugin identity must select exactly one catalog entry", "promotion.ValidateMarketplace", "aura marketplace gate", "catalog behavior would depend on parser or entry order", "remove or rename the duplicate entry", nil)
		}
		if err := validateMarketplaceSource(plugin.Name, plugin.Source); err != nil {
			return err
		}
		byName[plugin.Name] = plugin
	}
	for _, want := range projection.Entries {
		got, ok := byName[want.Name]
		if !ok {
			return fault("aura marketplace is missing projected plugin "+want.Name, "every target-published component must appear once", "promotion.ValidateMarketplace", "aura marketplace gate", "consumers could not install "+want.ComponentID, "add the exact generated entry to the named Aura commit", nil)
		}
		if got.Version != want.Version || got.Description != want.Description {
			return fault("aura marketplace metadata for "+want.Name+" does not match the projection", "version and description must equal the exact candidate manifest", "promotion.ValidateMarketplace", "aura marketplace gate", "the catalog would describe different artifacts", "regenerate the entry from the Pasture candidate manifests", nil)
		}
		if got.Source.Source != string(want.Source.Source) || got.Source.URL != want.Source.URL || got.Source.Path != want.Source.Path || got.Source.SHA != want.Source.SHA || got.Source.Repo != "" || got.Source.Ref != "" {
			return fault("aura marketplace source for "+want.Name+" does not match the immutable projection", "the git-subdir URL, distinct path, and exact sha must all match", "promotion.ValidateMarketplace", "aura marketplace gate", "consumers could fetch a repository root, moving ref, or wrong plugin", "regenerate the full source object from the Pasture candidate", nil)
		}
	}
	return nil
}

func validateMarketplaceSource(name string, source marketplacePluginSource) error {
	fail := func(reason, fix string) error {
		return fault("aura marketplace plugin "+name+" has an invalid source boundary", reason, "promotion.validateMarketplaceSource", "aura marketplace gate", "the source cannot be resolved canonically", fix, nil)
	}
	switch source.Source {
	case "github":
		if strings.TrimSpace(source.Repo) == "" || strings.TrimSpace(source.Repo) != source.Repo || source.URL != "" || source.Path != "" || (source.Ref != "" && source.SHA != "") {
			return fail("github sources require repo, forbid git-subdir fields, and may select at most one of ref or sha", "use the documented github source shape")
		}
		parts := strings.Split(source.Repo, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fail("github repo must use the canonical owner/name form", "set repo to the exact GitHub owner and repository name")
		}
	case string(SourceGitSubdir):
		cleanPath := filepath.Clean(source.Path)
		parsedURL, urlErr := url.ParseRequestURI(source.URL)
		if strings.TrimSpace(source.URL) == "" || strings.TrimSpace(source.URL) != source.URL || urlErr != nil || parsedURL.Scheme == "" || strings.TrimSpace(source.Path) == "" || strings.TrimSpace(source.Path) != source.Path || strings.Contains(source.Path, "\\") || cleanPath != source.Path || filepath.IsAbs(source.Path) || source.Path == ".." || strings.HasPrefix(source.Path, ".."+string(filepath.Separator)) || source.Repo != "" || (source.Ref == "" && source.SHA == "") {
			return fail("git-subdir sources require a canonical url, relative path, and ref or sha", "set url/path plus a schema-supported ref or sha selector")
		}
	default:
		return fail("the source discriminator is missing or unsupported", "use github or git-subdir")
	}
	if strings.TrimSpace(source.Ref) != source.Ref || strings.TrimSpace(source.SHA) != source.SHA {
		return fail("source selectors must be canonical non-padded values", "remove surrounding whitespace from ref or sha")
	}
	if source.SHA != "" {
		if _, err := effects.NewCommitOID(source.SHA); err != nil {
			return fail("source sha must be a full lowercase commit id", "set sha to the exact candidate commit")
		}
	}
	return nil
}
