// Command claudehost is an isolated stand-in for the Claude Code CLI used by
// the global installer integration suite. It implements exactly the bounded
// argv grammar the installer's reviewed activation contract issues
// (`claude --version`, `claude plugin marketplace list --json`,
// `claude plugin marketplace add|update`, `claude plugin list --available
// --json`, and `claude plugin install|update|uninstall <selector> --scope
// user`) against a JSON state file, so the production binary can be driven
// end to end without a real Claude installation, network access, or the
// caller's real home directory.
//
// It lives under testdata/ so the module's ordinary build, vet, and test
// patterns never compile it; the integration suite builds it explicitly by
// directory path.
//
// State file (PASTURE_FAKE_CLAUDE_STATE, required):
//
//	{
//	  "host_version": "2.1.261 (Claude Code)",   // printed by --version
//	  "marketplaces": [ ... ],                   // marketplace list --json body
//	  "installed":    [ ... ],                   // plugin list --available --json body
//	  "versions":     {"pasture-skills": "0.0.4"},
//	  "install_root": "/abs/path",               // parent of generated installPath
//	  "omit_version": false,                     // emit versionless rows + manifests
//	  "fail":         [{"match": "plugin install pasture-agents@aura-plugins",
//	                    "message": "..."}]
//	}
//
// Every invocation appends its exact argv to PASTURE_FAKE_CLAUDE_LOG (one
// space-joined line per call) so tests can assert which native commands the
// installer issued, and that untouched sibling cells were never mutated.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type pluginRow struct {
	ID          string  `json:"id"`
	Version     *string `json:"version,omitempty"`
	Scope       string  `json:"scope"`
	Enabled     *bool   `json:"enabled"`
	InstallPath string  `json:"installPath"`
	InstalledAt string  `json:"installedAt"`
	LastUpdated string  `json:"lastUpdated"`
}

type pluginList struct {
	Installed []pluginRow `json:"installed"`
	Available []struct{}  `json:"available"`
}

type failure struct {
	Match   string `json:"match"`
	Message string `json:"message"`
}

type state struct {
	HostVersion  string            `json:"host_version"`
	Marketplaces []json.RawMessage `json:"marketplaces"`
	Installed    []pluginRow       `json:"installed"`
	Versions     map[string]string `json:"versions"`
	InstallRoot  string            `json:"install_root"`
	OmitVersion  bool              `json:"omit_version"`
	Fail         []failure         `json:"fail"`
}

const timestamp = "2026-08-24T00:00:00Z"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	statePath := os.Getenv("PASTURE_FAKE_CLAUDE_STATE")
	if statePath == "" {
		return fmt.Errorf("claudehost: PASTURE_FAKE_CLAUDE_STATE is unset; the integration suite must point the isolated host at its own JSON state file")
	}
	if err := appendLog(args); err != nil {
		return err
	}
	current, err := load(statePath)
	if err != nil {
		return err
	}
	joined := strings.Join(args, " ")
	for _, f := range current.Fail {
		if f.Match != "" && strings.Contains(joined, f.Match) {
			return fmt.Errorf("claudehost: injected failure for %q: %s", joined, f.Message)
		}
	}
	switch {
	case len(args) == 1 && args[0] == "--version":
		fmt.Println(current.HostVersion)
		return nil
	case match(args, "plugin", "marketplace", "list", "--json"):
		return emit(current.Marketplaces)
	case len(args) == 6 && match(args[:3], "plugin", "marketplace", "add") && args[4] == "--scope" && args[5] == "user":
		return addMarketplace(statePath, current, args[3])
	case len(args) == 4 && match(args[:3], "plugin", "marketplace", "update"):
		return nil
	case match(args, "plugin", "list", "--available", "--json"):
		return emit(pluginList{Installed: current.Installed, Available: []struct{}{}})
	case len(args) == 5 && match(args[:2], "plugin", "install") && args[3] == "--scope" && args[4] == "user":
		return install(statePath, current, args[2])
	case len(args) == 5 && match(args[:2], "plugin", "update") && args[3] == "--scope" && args[4] == "user":
		return nil
	case len(args) == 5 && match(args[:2], "plugin", "uninstall") && args[3] == "--scope" && args[4] == "user":
		return uninstall(statePath, current, args[2])
	}
	return fmt.Errorf("claudehost: unsupported argv %q; the isolated host implements only the reviewed installer command grammar", joined)
}

func match(args []string, want ...string) bool {
	if len(args) != len(want) {
		return false
	}
	for i := range want {
		if args[i] != want[i] {
			return false
		}
	}
	return true
}

func appendLog(args []string) error {
	path := os.Getenv("PASTURE_FAKE_CLAUDE_LOG")
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("claudehost: open command log %q: %w", path, err)
	}
	defer file.Close()
	if _, err := fmt.Fprintln(file, strings.Join(args, " ")); err != nil {
		return fmt.Errorf("claudehost: append command log %q: %w", path, err)
	}
	return nil
}

func load(path string) (state, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return state{}, fmt.Errorf("claudehost: read state %q: %w", path, err)
	}
	var current state
	if err := json.Unmarshal(data, &current); err != nil {
		return state{}, fmt.Errorf("claudehost: decode state %q: %w", path, err)
	}
	return current, nil
}

func save(path string, current state) error {
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return fmt.Errorf("claudehost: encode state %q: %w", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("claudehost: write state %q: %w", path, err)
	}
	return nil
}

func emit(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("claudehost: encode response: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func addMarketplace(path string, current state, repo string) error {
	for _, raw := range current.Marketplaces {
		var row struct {
			Repo string `json:"repo"`
		}
		if err := json.Unmarshal(raw, &row); err == nil && row.Repo == repo {
			return nil
		}
	}
	entry := map[string]string{
		"name":            "aura-plugins",
		"source":          "github",
		"repo":            repo,
		"installLocation": filepath.Join(current.InstallRoot, "marketplaces", "aura-plugins"),
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("claudehost: encode marketplace %q: %w", repo, err)
	}
	current.Marketplaces = append(current.Marketplaces, raw)
	return save(path, current)
}

func install(path string, current state, selector string) error {
	name, _, ok := strings.Cut(selector, "@")
	if !ok {
		return fmt.Errorf("claudehost: selector %q is not <package>@<marketplace>", selector)
	}
	version, known := current.Versions[name]
	if !known {
		return fmt.Errorf("claudehost: package %q has no seeded version; the integration suite must seed every installable package version", name)
	}
	for _, row := range current.Installed {
		if row.ID == selector {
			return nil
		}
	}
	installPath := filepath.Join(current.InstallRoot, "cache", "aura-plugins", name, version)
	enabled := true
	row := pluginRow{ID: selector, Scope: "user", Enabled: &enabled, InstallPath: installPath, InstalledAt: timestamp, LastUpdated: timestamp}
	if current.OmitVersion {
		if err := writeManifest(installPath, name, version); err != nil {
			return err
		}
	} else {
		copied := version
		row.Version = &copied
	}
	current.Installed = append(current.Installed, row)
	return save(path, current)
}

func writeManifest(installPath, name, version string) error {
	dir := filepath.Join(installPath, ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("claudehost: create manifest directory %q: %w", dir, err)
	}
	body := fmt.Sprintf("{\n  \"name\": %q,\n  \"version\": %q\n}\n", name, version)
	manifest := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		return fmt.Errorf("claudehost: write manifest %q: %w", manifest, err)
	}
	return nil
}

func uninstall(path string, current state, selector string) error {
	kept := make([]pluginRow, 0, len(current.Installed))
	for _, row := range current.Installed {
		if row.ID == selector {
			continue
		}
		kept = append(kept, row)
	}
	current.Installed = kept
	return save(path, current)
}
