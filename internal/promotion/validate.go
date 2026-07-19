package promotion

import (
	"encoding/json"
	"os"
	"strings"
)

// marketplaceFile is the subset of the Aura .claude-plugin/marketplace.json
// structural schema the promotion gate validates against. It mirrors the shipped
// marketplace.json shape: a named catalog with a plugins array whose entries
// carry a name, a {source, repo} source, and a version.
type marketplaceFile struct {
	Name    string `json:"name"`
	Plugins []struct {
		Name   string `json:"name"`
		Source struct {
			Source string `json:"source"`
			Repo   string `json:"repo"`
		} `json:"source"`
		Version string `json:"version"`
	} `json:"plugins"`
}

// ValidateMarketplaceFile is the Aura marketplace/repository gate. It parses the
// marketplace.json at path against the shipped structural schema and confirms
// every projected plugin (name, version, source) is present and exactly matches
// the target-owned projection, so the published catalog can never drift from the
// component IDs and versions the target descriptors publish.
//
// It validates the projected plugins are present and exact; it does not require
// the marketplace to contain only those plugins (the catalog may aggregate
// unrelated plugins such as agentfilter). It is a structural validation against
// the delivered marketplace.json contract, not a live upstream JSON-schema fetch.
func ValidateMarketplaceFile(path string, projection Projection) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fault(
			"aura marketplace file could not be read at "+path,
			"the marketplace/repository gate validates the aggregate catalog on disk",
			"promotion.ValidateMarketplaceFile", "aura marketplace gate",
			"the promotion cannot confirm the published catalog matches the target projection",
			"pass --marketplace to the marketplace.json path, or ensure it exists in the aura repo", err,
		)
	}
	var file marketplaceFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fault(
			"aura marketplace file at "+path+" is not valid JSON",
			"the marketplace catalog must parse against the shipped structural schema",
			"promotion.ValidateMarketplaceFile", "aura marketplace gate",
			"the promotion cannot validate a malformed catalog",
			"fix the JSON syntax in the marketplace file", err,
		)
	}
	if strings.TrimSpace(file.Name) == "" {
		return fault(
			"aura marketplace file at "+path+" has no catalog name",
			"a valid marketplace declares its catalog name",
			"promotion.ValidateMarketplaceFile", "aura marketplace gate",
			"the catalog cannot be identified or published",
			"add a top-level \"name\" to the marketplace file", nil,
		)
	}

	byName := make(map[string]struct {
		source, repo, version string
	}, len(file.Plugins))
	for _, p := range file.Plugins {
		byName[p.Name] = struct{ source, repo, version string }{p.Source.Source, p.Source.Repo, p.Version}
	}

	for _, want := range projection.Entries {
		got, ok := byName[want.Name]
		if !ok {
			return fault(
				"aura marketplace is missing projected plugin "+want.Name,
				"every target-published component must appear as a selectable marketplace plugin",
				"promotion.ValidateMarketplaceFile", "aura marketplace gate",
				"consumers would not be able to install the "+want.ComponentID+" component",
				"regenerate the marketplace entries from the target projection so "+want.Name+" is present", nil,
			)
		}
		if got.version != want.Version {
			return fault(
				"aura marketplace plugin "+want.Name+" version "+got.version+" does not match the projected "+want.Version,
				"the catalog version must equal the exact version the target descriptor published",
				"promotion.ValidateMarketplaceFile", "aura marketplace gate",
				"the catalog would advertise a version that does not match the promoted artifacts",
				"update the "+want.Name+" marketplace version to "+want.Version, nil,
			)
		}
		if got.source != string(want.Source.Source) || got.repo != want.Source.Repo {
			return fault(
				"aura marketplace plugin "+want.Name+" source does not match the projection",
				"the catalog source must equal the projected {source, repo}",
				"promotion.ValidateMarketplaceFile", "aura marketplace gate",
				"consumers would fetch the plugin from the wrong source",
				"set the "+want.Name+" source to "+string(want.Source.Source)+" "+want.Source.Repo, nil,
			)
		}
	}
	return nil
}
