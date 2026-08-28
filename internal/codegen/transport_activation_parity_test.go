package codegen_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTransportWiresOnlyActivatedEvents asserts, for every harness Pasture
// generates, that the committed transport artifact wires EXACTLY the events the
// harness activation manifest marks as enabled.
//
// The transport is the only path by which a host can reach the lifecycle
// handler. A wired event that activation withholds is a host-visible defect: the
// host spawns a process for each occurrence, the handler refuses the event, and
// the host gets a refusal diagnostic instead of a decision. The wiring also
// contradicts the harness activation report, which records the same event as
// withheld. A withheld event that is never wired can never reach the handler.
//
// The comparison uses the committed artifacts, not the emitters, so the test
// fails on a stale regeneration as well as on an emitter defect.
func TestTransportWiresOnlyActivatedEvents(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	t.Run("claude-code", func(t *testing.T) {
		t.Parallel()
		enabled := enabledEventsFromActivationReport(t, filepath.Join(root, "hooks", "pasture-activation.json"))
		wired := claudeWiredLifecycleEvents(t, filepath.Join(root, "hooks", "hooks.json"))
		requireSameEvents(t, "hooks/hooks.json", enabled, wired)
	})

	t.Run("codex", func(t *testing.T) {
		t.Parallel()
		enabled := enabledEventsFromActivationReport(t, filepath.Join(root, ".codex", "pasture-codex-activation.json"))
		wired := codexWiredLifecycleEvents(t, filepath.Join(root, ".codex", "hooks.json"))
		requireSameEvents(t, ".codex/hooks.json", enabled, wired)

		runners := codexEventRunnerNames(t, filepath.Join(root, ".codex", "hooks", "events"))
		requireSameEvents(t, ".codex/hooks/events", enabled, runners)
	})

	t.Run("opencode", func(t *testing.T) {
		t.Parallel()
		enabled := enabledEventsFromOpenCodeManifest(t, filepath.Join(root, ".opencode", "pasture-opencode.json"))
		wired := openCodeWiredLifecycleEvents(t, filepath.Join(root, ".opencode", "plugins", "pasture-lifecycle.ts"))
		requireSameEvents(t, ".opencode/plugins/pasture-lifecycle.ts", enabled, wired)
	})
}

// activationReportFile mirrors the committed Claude and Codex activation audit
// reports. Only the fields this parity check reads are declared.
type activationReportFile struct {
	Events []struct {
		Event string `json:"event"`
		State string `json:"state"`
	} `json:"events"`
}

// openCodeTargetManifestFile mirrors the committed OpenCode target manifest.
// Its activation array carries the same event/state shape as the two report
// files, nested one level down.
type openCodeTargetManifestFile struct {
	Activation []struct {
		Event string `json:"event"`
		State string `json:"state"`
	} `json:"activation"`
}

func enabledEventsFromActivationReport(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	var report activationReportFile
	readGeneratedJSON(t, path, &report)
	enabled := make(map[string]struct{})
	for _, entry := range report.Events {
		if entry.State == "enabled" {
			enabled[entry.Event] = struct{}{}
		}
	}
	require.NotEmpty(t, enabled,
		"activation report %q enables no event; a harness with an empty enabled set cannot be checked for transport parity — regenerate the report or remove the harness from this test", path)
	return enabled
}

func enabledEventsFromOpenCodeManifest(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	var manifest openCodeTargetManifestFile
	readGeneratedJSON(t, path, &manifest)
	enabled := make(map[string]struct{})
	for _, entry := range manifest.Activation {
		if entry.State == "enabled" {
			enabled[entry.Event] = struct{}{}
		}
	}
	require.NotEmpty(t, enabled,
		"OpenCode target manifest %q enables no event; regenerate the manifest before running the transport parity check", path)
	return enabled
}

// claudeHooksFile mirrors the committed Claude hooks configuration. Claude
// groups carry non-lifecycle commands too (git discipline, task priming), so the
// parity check counts only groups that invoke `pasture hook lifecycle`.
type claudeHooksFile struct {
	Hooks map[string][]struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func claudeWiredLifecycleEvents(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	var config claudeHooksFile
	readGeneratedJSON(t, path, &config)
	wired := make(map[string]struct{})
	for event, groups := range config.Hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if lifecycleCommand.MatchString(hook.Command) {
					wired[event] = struct{}{}
				}
			}
		}
	}
	return wired
}

// codexHooksFile mirrors the committed Codex host hook configuration.
type codexHooksFile struct {
	Hooks map[string][]struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func codexWiredLifecycleEvents(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	var config codexHooksFile
	readGeneratedJSON(t, path, &config)
	wired := make(map[string]struct{})
	for event, groups := range config.Hooks {
		require.NotEmpty(t, groups,
			"%s: event %q has an empty hook group; every wired Codex event must invoke exactly one runner", path, event)
		wired[event] = struct{}{}
	}
	return wired
}

func codexEventRunnerNames(t *testing.T, dir string) map[string]struct{} {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoErrorf(t, err,
		"read Codex runner directory %q: %v — the generated Codex hooks package must contain one runner per enabled event; run `make generate`", dir, err)
	runners := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sh" {
			continue
		}
		runners[entry.Name()[:len(entry.Name())-len(".sh")]] = struct{}{}
	}
	return runners
}

// lifecycleEventFlag captures the native event name each generated OpenCode
// handler passes to `pasture hook lifecycle`.
var lifecycleEventFlag = regexp.MustCompile(`"--event",\s*"([^"]+)"`)

// lifecycleCommand recognizes a command string that invokes the Pasture
// lifecycle handler, in either the Claude or the Codex spelling.
var lifecycleCommand = regexp.MustCompile(`hook lifecycle`)

func openCodeWiredLifecycleEvents(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoErrorf(t, err,
		"read generated OpenCode plugin %q: %v — run `make generate` to restore the committed transport artifact", path, err)
	wired := make(map[string]struct{})
	for _, match := range lifecycleEventFlag.FindAllStringSubmatch(string(source), -1) {
		wired[match[1]] = struct{}{}
	}
	return wired
}

func readGeneratedJSON(t *testing.T, path string, target any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoErrorf(t, err,
		"read generated artifact %q: %v — run `make generate` to restore the committed output", path, err)
	require.NoErrorf(t, json.Unmarshal(raw, target),
		"parse generated artifact %q as JSON — the committed artifact is malformed; run `make generate`", path)
}

func requireSameEvents(t *testing.T, artifact string, enabled, wired map[string]struct{}) {
	t.Helper()
	var extra, missing []string
	for event := range wired {
		if _, ok := enabled[event]; !ok {
			extra = append(extra, event)
		}
	}
	for event := range enabled {
		if _, ok := wired[event]; !ok {
			missing = append(missing, event)
		}
	}
	sort.Strings(extra)
	sort.Strings(missing)
	require.Emptyf(t, extra,
		"%s wires %v, which the activation manifest withholds. The host would spawn a process for each occurrence and get a refusal from the lifecycle handler instead of a decision, and the transport would contradict the activation audit report. Filter the emitter on the activation manifest and run `make generate`.",
		artifact, extra)
	require.Emptyf(t, missing,
		"%s does not wire %v, which the activation manifest enables. An enabled event with no transport entry never reaches the handler. Regenerate with `make generate`.",
		artifact, missing)
}
