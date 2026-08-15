package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type marketplaceSource uint8

type codecUnavailableError struct{ cause error }

func (e *codecUnavailableError) Error() string { return e.cause.Error() }
func (e *codecUnavailableError) Unwrap() error { return e.cause }

const (
	marketplaceSourceInvalid marketplaceSource = iota
	marketplaceSourceGitHub
	marketplaceSourceGit
	marketplaceSourceURL
	marketplaceSourceDirectory
)

type marketplace struct {
	Name, Repo, URL, Path, InstallLocation string
	Source                                 marketplaceSource
}

type marketplaceWire struct {
	Name            string `json:"name"`
	Source          string `json:"source"`
	Repo            string `json:"repo,omitempty"`
	URL             string `json:"url,omitempty"`
	Path            string `json:"path,omitempty"`
	InstallLocation string `json:"installLocation"`
}

type pluginRow struct {
	ID, Name, Marketplace, Scope, InstallPath string
	Version                                   *string
	Enabled                                   bool
}

type pluginRowWire struct {
	ID              string  `json:"id"`
	PluginID        string  `json:"pluginId"`
	Name            string  `json:"name"`
	Description     *string `json:"description,omitempty"`
	MarketplaceName string  `json:"marketplaceName"`
	Version         *string `json:"version,omitempty"`
	Scope           string  `json:"scope,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
	InstallPath     string  `json:"installPath,omitempty"`
	InstalledAt     string  `json:"installedAt,omitempty"`
	LastUpdated     string  `json:"lastUpdated,omitempty"`
	Source          string  `json:"source,omitempty"`
}

type pluginListWire struct {
	Installed []pluginRowWire `json:"installed"`
	Available []pluginRowWire `json:"available"`
}

func decodeMarketplaces(data []byte) ([]marketplace, error) {
	var wire []marketplaceWire
	if err := decodeExact(data, &wire); err != nil {
		return nil, &codecUnavailableError{cause: fault("Claude marketplace probe decode", "documented marketplace JSON array", err.Error(), "decodeMarketplaces", "classifying native marketplace state before mutation", "no native action can safely start", "repair or upgrade Claude so `claude plugin marketplace list --json` emits the reviewed schema", err)}
	}
	seen := map[string]struct{}{}
	result := make([]marketplace, 0, len(wire))
	for i, row := range wire {
		if row.Name == "" || row.InstallLocation == "" {
			return nil, fmt.Errorf("marketplace row %d lacks name or installLocation", i)
		}
		if _, duplicate := seen[row.Name]; duplicate {
			return nil, fmt.Errorf("marketplace %q appears more than once", row.Name)
		}
		seen[row.Name] = struct{}{}
		parsed := marketplace{Name: row.Name, Repo: row.Repo, URL: row.URL, Path: row.Path, InstallLocation: row.InstallLocation}
		switch row.Source {
		case "github":
			parsed.Source = marketplaceSourceGitHub
			if row.Repo == "" || row.URL != "" || row.Path != "" {
				return nil, fmt.Errorf("github marketplace %q has contradictory source fields", row.Name)
			}
		case "git":
			parsed.Source = marketplaceSourceGit
			if row.URL == "" || row.Repo != "" || row.Path != "" {
				return nil, fmt.Errorf("git marketplace %q has contradictory source fields", row.Name)
			}
		case "url":
			parsed.Source = marketplaceSourceURL
			if row.URL == "" || row.Repo != "" || row.Path != "" {
				return nil, fmt.Errorf("URL marketplace %q has contradictory source fields", row.Name)
			}
		case "directory":
			parsed.Source = marketplaceSourceDirectory
			if row.Path == "" || row.Repo != "" || row.URL != "" {
				return nil, fmt.Errorf("directory marketplace %q has contradictory source fields", row.Name)
			}
		default:
			return nil, fmt.Errorf("marketplace %q has unknown source discriminator %q", row.Name, row.Source)
		}
		result = append(result, parsed)
	}
	return result, nil
}

func decodePlugins(data []byte) ([]pluginRow, error) {
	var wire pluginListWire
	if err := decodeExact(data, &wire); err != nil {
		return nil, &codecUnavailableError{cause: fault("Claude plugin probe decode", "documented installed/available JSON object", err.Error(), "decodePlugins", "classifying native plugin state before mutation", "no native action can safely start", "repair or upgrade Claude so `claude plugin list --available --json` emits the reviewed schema", err)}
	}
	seen := map[string]struct{}{}
	result := make([]pluginRow, 0, len(wire.Installed))
	for i, row := range wire.Installed {
		id := row.ID
		if id == "" {
			id = row.PluginID
		}
		if id == "" || row.Scope == "" || row.Enabled == nil || row.InstallPath == "" {
			return nil, fmt.Errorf("installed plugin row %d lacks required id/scope/enabled/installPath", i)
		}
		if row.ID != "" && row.PluginID != "" && row.ID != row.PluginID {
			return nil, fmt.Errorf("installed plugin row %d has conflicting id %q and pluginId %q", i, row.ID, row.PluginID)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("installed plugin %q appears more than once", id)
		}
		at := strings.LastIndex(id, "@")
		if at <= 0 || at == len(id)-1 {
			return nil, fmt.Errorf("installed plugin row %d has selector %q outside plugin@marketplace form", i, id)
		}
		name, marketplaceName := id[:at], id[at+1:]
		if row.Name != "" && row.Name != name {
			return nil, fmt.Errorf("installed plugin row %d name %q contradicts selector %q", i, row.Name, id)
		}
		if row.MarketplaceName != "" && row.MarketplaceName != marketplaceName {
			return nil, fmt.Errorf("installed plugin row %d marketplace %q contradicts selector %q", i, row.MarketplaceName, id)
		}
		seen[id] = struct{}{}
		result = append(result, pluginRow{ID: id, Name: name, Marketplace: marketplaceName, Version: row.Version, Scope: row.Scope, Enabled: *row.Enabled, InstallPath: row.InstallPath})
	}
	for i, row := range wire.Available {
		id := row.PluginID
		if id == "" {
			id = row.ID
		}
		if id == "" || row.Name == "" || row.MarketplaceName == "" || row.Source == "" {
			return nil, fmt.Errorf("available plugin row %d lacks pluginId/name/marketplaceName/source", i)
		}
	}
	return result, nil
}

func decodeExact(data []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON contains a second value")
		}
		return err
	}
	return nil
}

func isPastureIdentity(row pluginRow) bool {
	return strings.HasPrefix(row.ID, "pasture") || strings.HasPrefix(row.Name, "pasture")
}
