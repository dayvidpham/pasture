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

// installedPluginWire is the reviewed schema of one `installed` entry of
// `claude plugin list --available --json`. It is decoded with unknown fields
// disallowed, so every key Claude emits must be named here; a new key is a
// contract change that must be reviewed, not silently accepted.
type installedPluginWire struct {
	ID          string  `json:"id"`
	Version     *string `json:"version,omitempty"`
	Scope       string  `json:"scope"`
	Enabled     *bool   `json:"enabled"`
	InstallPath string  `json:"installPath"`
	InstalledAt string  `json:"installedAt"`
	LastUpdated string  `json:"lastUpdated"`

	// Claude Code 2.1.322 also emits the three keys below. They are
	// recognised so the strict decode admits them, and ignored deliberately:
	// pasture installs only user-scope plugins, and these keys describe
	// project-scope activation, which never decides ownership of a pasture
	// cell. Older hosts omit them, so each is optional.
	//
	// ProjectEnabled is a boolean on every installed entry: whether the
	// plugin is enabled for the current project.
	ProjectEnabled *bool `json:"projectEnabled,omitempty"`
	// ProjectPath is a string, present only on project-scope entries: the
	// project directory that installed the plugin.
	ProjectPath *string `json:"projectPath,omitempty"`
	// ReadFromFolder is a string (a directory path, not a boolean), present
	// only on project-scope entries: the local folder Claude reads the plugin
	// from.
	ReadFromFolder *string `json:"readFromFolder,omitempty"`
}

// availablePluginWire is the reviewed schema of one `available` entry of
// `claude plugin list --available --json`, decoded with unknown fields
// disallowed.
type availablePluginWire struct {
	PluginID        string  `json:"pluginId"`
	Name            string  `json:"name"`
	Description     *string `json:"description,omitempty"`
	MarketplaceName string  `json:"marketplaceName"`
	Version         *string `json:"version,omitempty"`
	// Source is a relative path string or a typed source object; see
	// availablePluginSource. It is validated for presence and ignored.
	Source availablePluginSource `json:"source"`
	// InstallCount is a JSON integer that Claude Code 2.1.322 emits on some
	// available entries: the marketplace's install count. It is recognised
	// so the strict decode admits it, and ignored deliberately: popularity
	// never decides ownership of a pasture cell.
	InstallCount *int64 `json:"installCount,omitempty"`
}

// availablePluginSource is the `source` of an available entry. Claude Code
// emits either a relative path string (for example "./pasture-skills") or an
// object carrying a string `source` discriminator (for example
// {"source":"url","url":"...","sha":"..."} or {"source":"git-subdir",...}).
// The installer never uses the value; it only requires that one of the two
// documented shapes is present. The object's other members are the
// marketplace author's per-kind source specification, echoed from the
// marketplace manifest rather than defined by the Claude CLI, so they are
// ignored deliberately; the discriminator is still required.
type availablePluginSource struct {
	present bool
}

func (s *availablePluginSource) UnmarshalJSON(data []byte) error {
	var path string
	if err := json.Unmarshal(data, &path); err == nil {
		if path == "" {
			return fmt.Errorf("available plugin source is an empty string")
		}
		s.present = true
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return fmt.Errorf("available plugin source %s is neither a path string nor a source object", data)
	}
	var kind string
	raw, ok := object["source"]
	if !ok {
		return fmt.Errorf("available plugin source object lacks its \"source\" discriminator")
	}
	if err := json.Unmarshal(raw, &kind); err != nil || kind == "" {
		return fmt.Errorf("available plugin source object has discriminator %s, want a non-empty string", raw)
	}
	s.present = true
	return nil
}

type pluginListWire struct {
	Installed []installedPluginWire `json:"installed"`
	Available []availablePluginWire `json:"available"`
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
		if id == "" || row.Scope == "" || row.Enabled == nil || row.InstallPath == "" || row.InstalledAt == "" || row.LastUpdated == "" {
			return nil, fmt.Errorf("installed plugin row %d lacks required id/scope/enabled/installPath/installedAt/lastUpdated", i)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("installed plugin %q appears more than once", id)
		}
		at := strings.LastIndex(id, "@")
		if at <= 0 || at == len(id)-1 {
			return nil, fmt.Errorf("installed plugin row %d has selector %q outside plugin@marketplace form", i, id)
		}
		name, marketplaceName := id[:at], id[at+1:]
		seen[id] = struct{}{}
		result = append(result, pluginRow{ID: id, Name: name, Marketplace: marketplaceName, Version: row.Version, Scope: row.Scope, Enabled: *row.Enabled, InstallPath: row.InstallPath})
	}
	for i, row := range wire.Available {
		id := row.PluginID
		if id == "" || row.Name == "" || row.MarketplaceName == "" || !row.Source.present {
			return nil, fmt.Errorf("available plugin row %d lacks pluginId/name/marketplaceName/source", i)
		}
		at := strings.LastIndex(id, "@")
		if at <= 0 || at == len(id)-1 || id[:at] != row.Name || id[at+1:] != row.MarketplaceName {
			return nil, fmt.Errorf("available plugin row %d has selector %q inconsistent with name %q and marketplace %q", i, id, row.Name, row.MarketplaceName)
		}
		if _, duplicate := seen["available:"+id]; duplicate {
			return nil, fmt.Errorf("available plugin %q appears more than once", id)
		}
		seen["available:"+id] = struct{}{}
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
